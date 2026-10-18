package deepflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ops-platform/internal/datascope"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/resource"
)

func TestLiveRefreshFailureRecordsStageTimings(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	native := httptest.NewServer(http.NotFoundHandler())
	defer native.Close()
	client, _ := kubernetes.NewClient(native.URL, native.Client(), 20, 20)
	mapper := &LiveMapper{native: client, authorize: func(context.Context, evidence.Binding) error { return nil }}
	if _, _, err := mapper.refresh(t.Context()); err == nil {
		t.Fatal("missing native namespace accepted")
	}
	var record struct {
		Stage   string           `json:"stage"`
		StageMs map[string]int64 `json:"stageMs"`
	}
	if err := json.Unmarshal(logs.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if _, ok := record.StageMs["binding"]; !ok || record.Stage != "native_before" {
		t.Fatal("completed stage timing or failing stage missing", record)
	}
	if _, ok := record.StageMs["native_before"]; !ok {
		t.Fatal("failing stage duration missing", record)
	}
}

func TestLiveUIDContainerLifetimeAndL7Qualification(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	uid := "b5488530-7adc-4f94-a7c4-611bbcc6cc8c"
	nsUID := "d115d180-d747-4727-8cc9-218024e1a944"
	cluster := "a94a0648-ce43-4905-b5c7-3d7b937283b5"
	container := strings.Repeat("a", 64)
	var nativeContainer atomic.Value
	nativeContainer.Store(container)
	var allowed atomic.Bool
	allowed.Store(true)
	var nativeReads atomic.Int64
	var onDetailRead atomic.Value
	onDetailRead.Store(func() {})
	backendContainer := container
	duplicateTag := false
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nativeReads.Add(1)
		switch r.URL.Path {
		case "/api/v1/namespaces/kube-system":
			fmt.Fprintf(w, `{"metadata":{"uid":%q}}`, cluster)
		case "/api/v1/namespaces/apps":
			fmt.Fprintf(w, `{"metadata":{"uid":%q}}`, nsUID)
		case "/api/v1/namespaces/apps/pods":
			json.NewEncoder(w).Encode(map[string]any{"metadata": map[string]string{"resourceVersion": "1"}, "items": []any{map[string]any{"metadata": map[string]any{"uid": uid, "name": "canary", "namespace": "apps", "creationTimestamp": now.Add(-time.Minute)}, "status": map[string]any{"containerStatuses": []any{map[string]any{"containerID": "containerd://" + nativeContainer.Load().(string), "state": map[string]any{"running": map[string]any{"startedAt": now.Add(-30 * time.Second)}}}}}}}})
		default:
			t.Errorf("unexpected native path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer native.Close()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Org-Id") != "1" {
			t.Error("organization fence missing")
		}
		if strings.HasPrefix(r.URL.Path, "/v1/info/") {
			json.NewEncoder(w).Encode(map[string]any{"OPT_STATUS": "SUCCESS", "DATA": map[string]any{"Verified": true, "SyncAt": now, "PodClusters": []any{map[string]string{"lcuuid": "cluster-lcuuid", "name": "canary-cluster"}}, "Pods": []any{map[string]any{"name": "canary", "lcuuid": uid, "container_ids": backendContainer, "created_at": now.Add(-time.Minute), "pod_namespace_lcuuid": nsUID, "pod_cluster_lcuuid": "cluster-lcuuid"}}}})
			return
		}
		_ = r.ParseForm()
		sql := r.Form.Get("sql")
		if strings.HasPrefix(sql, "SHOW TAG") {
			values := [][]any{{1, "canary"}}
			if duplicateTag {
				values = append(values, []any{2, "canary"})
			}
			json.NewEncoder(w).Encode(map[string]any{"OPT_STATUS": "SUCCESS", "result": map[string]any{"columns": []string{"value", "display_name"}, "values": values}})
			return
		}
		if !strings.Contains(sql, "FROM l7_flow_log") || strings.Contains(sql, "request_resource") || strings.Contains(sql, "trace_id") {
			t.Error("unbounded L7 template")
		}
		onDetailRead.Load().(func())()
		json.NewEncoder(w).Encode(map[string]any{"OPT_STATUS": "SUCCESS", "result": map[string]any{"columns": []string{"pod_id_0", "pod_id_1", "retrans_tx", "retrans_rx", "status", "tap_side", "observed_at", "l7_protocol", "response_code", "rrt", "request_resource"}, "values": [][]any{{1, 1, 0, 0, 3, "c-p", now.Add(-5 * time.Second).Unix(), 20, 503, 2500, "/secret?password=do-not-export"}}}})
	}))
	defer backend.Close()
	binding := evidence.Binding{SourceType: "deepflow", Tenant: "tenant-a", SourceID: "df-a", Revision: 1, BackendLogicalID: "querier", Endpoint: backend.URL, ScopeMapping: datascope.Mapping{Scopes: map[string][]string{"organization": {"1"}, "cluster": {cluster}, "namespace": {"apps"}}}}
	authorize := func(context.Context, evidence.Binding) error {
		if !allowed.Load() {
			return evidence.ErrScopeUnverified
		}
		return nil
	}
	client, _ := kubernetes.NewClient(native.URL, native.Client(), 20, 20)
	mapper, err := NewLiveMapper(LiveConfig{ControllerEndpoint: backend.URL, DomainLCUUID: "606f047e-450c-41a8-a2bf-a1f4d610f2da", ClusterUID: cluster}, binding, client, backend.Client(), authorize)
	if err != nil {
		t.Fatal(err)
	}
	id := resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: cluster, APIGroup: "core", Kind: "Pod", StableID: uid}.String()
	endpoint, err := mapper.ByCanonical(t.Context(), id)
	if err != nil || endpoint.PodID != 1 || !endpoint.IdentityValidFrom.Equal(now.Add(-30*time.Second)) {
		t.Fatalf("live identity %+v %v", endpoint, err)
	}
	// Pair snapshots use no shared flow map; background refresh must not consume
	// their bounded identity-check time while holding that map's lock.
	mapper.mu.Lock()
	pairDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		snapshot, err := mapper.beginSummaryPair(ctx, id, id)
		if err == nil {
			_, _, err = mapper.finishSummaryPair(ctx, snapshot)
		}
		pairDone <- err
	}()
	select {
	case err := <-pairDone:
		mapper.mu.Unlock()
		if err != nil {
			t.Fatal("local pair snapshot blocked by unrelated flow refresh", err)
		}
	case <-time.After(2 * time.Second):
		mapper.mu.Unlock()
		<-pairDone
		t.Fatal("pair snapshot waited for unrelated shared flow map")
	}
	adapter, err := New(binding, backend.Client(), mapper, authorize, true, "live")
	if err != nil {
		t.Fatal(err)
	}
	query := evidence.Query{Template: "GetL7Context", ResourceCanonicalID: id, Namespace: "apps", From: now.Add(-20 * time.Second), To: now, Limit: 10, Scope: graph.Scope{Tenant: "tenant-a", Cluster: cluster, Namespaces: []string{"apps"}}}
	if _, err = adapter.Query(t.Context(), query); err != ErrDisabled {
		t.Fatal("L7 enabled without actual proof")
	}
	if err = adapter.QualifyL7(t.Context(), query); err != nil {
		t.Fatal(err)
	}
	result, err := adapter.Query(t.Context(), query)
	if err != nil || len(result.Evidence) != 1 {
		t.Fatal("qualified L7", result, err)
	}
	if strings.Contains(string(result.Evidence[0].Data), "password") || !strings.Contains(string(result.Evidence[0].Data), "responseTimeMicroseconds") {
		t.Fatal("L7 projection includes payload or misses latency")
	}
	// The foreground read must not depend on another query's mutable flow map.
	mapper.mu.Lock()
	readCount := nativeReads.Load()
	completed := make(chan error, 1)
	go func() {
		_, err := adapter.Query(t.Context(), query)
		completed <- err
	}()
	select {
	case err := <-completed:
		mapper.mu.Unlock()
		if err != nil {
			t.Fatal("request-local identity read failed", err)
		}
	case <-time.After(time.Second):
		mapper.mu.Unlock()
		<-completed
		t.Fatal("foreground identity read waited for another query's map lock")
	}
	if calls := nativeReads.Load() - readCount; calls != 6 {
		t.Fatal("one native snapshot must enclose the entire backend read", calls)
	}
	for _, change := range []string{"restart", "revoke"} {
		onDetailRead.Store(func() {
			if change == "restart" {
				nativeContainer.Store(strings.Repeat("c", 64))
			} else {
				allowed.Store(false)
			}
		})
		result, err := adapter.Query(t.Context(), query)
		onDetailRead.Store(func() {})
		nativeContainer.Store(container)
		allowed.Store(true)
		if !errors.Is(err, evidence.ErrScopeUnverified) || len(result.Evidence) != 0 {
			t.Fatal("identity/source change during the row read published evidence", change, err)
		}
	}
	query.Template = "GetTraceContext"
	if _, err = adapter.Query(t.Context(), query); err != ErrDisabled {
		t.Fatal("Trace enabled")
	}
	query.Template = "GetL7Context"
	adapter.l7Until = now.Add(-time.Second)
	if _, err = adapter.Query(t.Context(), query); err != ErrDisabled {
		t.Fatal("expired L7 proof usable")
	}
	backendContainer = strings.Repeat("b", 64)
	if _, err = mapper.ByCanonical(t.Context(), id); err == nil {
		t.Fatal("same name wrong backend container accepted")
	}
	backendContainer = container
	duplicateTag = true
	if _, err = mapper.ByCanonical(t.Context(), id); err == nil {
		t.Fatal("ambiguous backend ID accepted")
	}
	duplicateTag = false
	if _, err = mapper.ByCanonical(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	nativeContainer.Store(strings.Repeat("c", 64))
	if err = mapper.ValidateCurrent(t.Context()); err == nil {
		t.Fatal("container restart during query accepted")
	}
	nativeContainer.Store(container)
	allowed.Store(false)
	if _, err = mapper.ByCanonical(t.Context(), id); err == nil {
		t.Fatal("withdrawn source usable")
	}
	if _, err = New(binding, backend.Client(), FrozenMappings{}, authorize, false, "live"); err == nil {
		t.Fatal("frozen mappings promoted to live")
	}
}

func TestCanonicalPairDoesNotResolveUnrelatedBackendTags(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	cluster, nsUID := "a94a0648-ce43-4905-b5c7-3d7b937283b5", "d115d180-d747-4727-8cc9-218024e1a944"
	container := strings.Repeat("a", 64)
	nativeReads := 0
	mutateDuringSummary := false
	pods := []any{}
	cloud := []any{}
	for _, name := range []string{"caller", "dependency", "unrelated"} {
		pods = append(pods, map[string]any{"metadata": map[string]any{"uid": name, "name": name, "namespace": "apps", "creationTimestamp": now.Add(-time.Minute)}, "status": map[string]any{"containerStatuses": []any{map[string]any{"containerID": "containerd://" + container, "state": map[string]any{"running": map[string]any{"startedAt": now.Add(-time.Minute)}}}}}})
		cloud = append(cloud, map[string]any{"name": name, "lcuuid": name, "container_ids": container, "created_at": now.Add(-time.Minute), "pod_namespace_lcuuid": nsUID, "pod_cluster_lcuuid": "cloud-cluster"})
	}
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nativeReads++
		switch r.URL.Path {
		case "/api/v1/namespaces/kube-system":
			fmt.Fprintf(w, `{"metadata":{"uid":%q}}`, cluster)
		case "/api/v1/namespaces/apps":
			fmt.Fprintf(w, `{"metadata":{"uid":%q}}`, nsUID)
		default:
			if mutateDuringSummary && container != strings.Repeat("a", 64) {
				pod := pods[0].(map[string]any)
				status := pod["status"].(map[string]any)["containerStatuses"].([]any)[0].(map[string]any)
				status["containerID"] = "containerd://" + container
			}
			json.NewEncoder(w).Encode(map[string]any{"metadata": map[string]string{}, "items": pods})
		}
	}))
	defer native.Close()
	tags := 0
	allowUnrelated := false
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/info/") {
			json.NewEncoder(w).Encode(map[string]any{"OPT_STATUS": "SUCCESS", "DATA": map[string]any{"Verified": true, "SyncAt": now, "PodClusters": []any{map[string]string{"lcuuid": "cloud-cluster", "name": "cluster"}}, "Pods": cloud}})
			return
		}
		r.ParseForm()
		sql := r.Form.Get("sql")
		if strings.Contains(sql, "GROUP BY response_code") {
			if mutateDuringSummary {
				container = strings.Repeat("b", 64)
			}
			fmt.Fprint(w, `{"OPT_STATUS":"SUCCESS","result":{"columns":["response_code","observations","duration_us"],"values":[[200,1000,2000000]]}}`)
			return
		}
		tags++
		if strings.Contains(sql, "unrelated") && !allowUnrelated {
			t.Error("unrelated tag read consumed the selected pair's budget")
			w.WriteHeader(503)
			return
		}
		id := 1
		if strings.Contains(sql, "pod='dependency'") {
			id = 2
		}
		if strings.Contains(sql, "pod='unrelated'") {
			id = 3
		}
		fmt.Fprintf(w, `{"OPT_STATUS":"SUCCESS","result":{"columns":["value"],"values":[[%d]]}}`, id)
	}))
	defer backend.Close()
	binding := evidence.Binding{SourceType: "deepflow", Tenant: "tenant", SourceID: "source", Revision: 1, BackendLogicalID: "querier", Endpoint: backend.URL, ScopeMapping: datascope.Mapping{Scopes: map[string][]string{"organization": {"1"}, "cluster": {cluster}, "namespace": {"apps"}}}}
	client, _ := kubernetes.NewClient(native.URL, native.Client(), 20, 20)
	mapper, err := NewLiveMapper(LiveConfig{ControllerEndpoint: backend.URL, DomainLCUUID: nsUID, ClusterUID: cluster}, binding, client, backend.Client(), func(context.Context, evidence.Binding) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	id := func(name string) string {
		return resource.CanonicalID{Domain: "k8s", Tenant: "tenant", Scope: cluster, APIGroup: "core", Kind: "Pod", StableID: name}.String()
	}
	left, right, err := mapper.ByCanonicalPair(t.Context(), id("caller"), id("dependency"))
	if err != nil || left.CanonicalID != id("caller") || right.CanonicalID != id("dependency") || tags != 4 {
		t.Fatalf("pair=%+v %+v tags=%d err=%v", left, right, tags, err)
	}
	allowUnrelated = true
	if _, err = mapper.ByCanonical(t.Context(), id("caller")); err != nil {
		t.Fatal(err)
	}
	if _, _, err = mapper.ByCanonicalPair(t.Context(), id("caller"), id("dependency")); err != nil {
		t.Fatal(err)
	}
	other, err := mapper.ByBackend(t.Context(), 3)
	if err != nil || other.CanonicalID != id("unrelated") {
		t.Fatal("pair query overwrote an active legacy flow mapping", other, err)
	}
	if err := mapper.ValidateCurrent(t.Context()); err != nil {
		t.Fatal("native fence lost", err)
	}

	adapter, _ := New(binding, backend.Client(), mapper, func(context.Context, evidence.Binding) error { return nil }, true, "fixture_only")
	q := SummaryQuery{Target: id("caller"), Peer: id("dependency"), Direction: "outgoing", ObservationPoint: "s-p", From: now.Add(-30 * time.Second), To: now, Scope: graph.Scope{Tenant: "tenant", Cluster: cluster, Namespaces: []string{"apps"}}, L7: true}
	count := nativeReads
	summaries, results, err := adapter.SummaryBatch(t.Context(), []SummaryQuery{q})
	if err != nil || len(summaries) != 1 || len(results) != 1 || nativeReads-count != 6 {
		t.Fatalf("batch fences/counts: summaries=%d reads=%d err=%v", len(summaries), nativeReads-count, err)
	}
	mutateDuringSummary = true
	summaries, results, err = adapter.SummaryBatch(t.Context(), []SummaryQuery{q})
	if err == nil || len(summaries) != 0 || len(results) != 0 {
		t.Fatal("native container changed during window read but batch was published", err)
	}

}
