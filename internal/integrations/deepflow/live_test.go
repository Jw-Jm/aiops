package deepflow

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ops-platform/internal/datascope"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/resource"
)

func TestLiveUIDContainerLifetimeAndL7Qualification(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	uid := "b5488530-7adc-4f94-a7c4-611bbcc6cc8c"
	nsUID := "d115d180-d747-4727-8cc9-218024e1a944"
	cluster := "a94a0648-ce43-4905-b5c7-3d7b937283b5"
	container := strings.Repeat("a", 64)
	nativeContainer := container
	backendContainer := container
	duplicateTag := false
	allowed := true
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/namespaces/kube-system":
			fmt.Fprintf(w, `{"metadata":{"uid":%q}}`, cluster)
		case "/api/v1/namespaces/apps":
			fmt.Fprintf(w, `{"metadata":{"uid":%q}}`, nsUID)
		case "/api/v1/namespaces/apps/pods":
			json.NewEncoder(w).Encode(map[string]any{"metadata": map[string]string{"resourceVersion": "1"}, "items": []any{map[string]any{"metadata": map[string]any{"uid": uid, "name": "canary", "namespace": "apps", "creationTimestamp": now.Add(-time.Minute)}, "status": map[string]any{"containerStatuses": []any{map[string]any{"containerID": "containerd://" + nativeContainer, "state": map[string]any{"running": map[string]any{"startedAt": now.Add(-30 * time.Second)}}}}}}}})
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
		json.NewEncoder(w).Encode(map[string]any{"OPT_STATUS": "SUCCESS", "result": map[string]any{"columns": []string{"pod_id_0", "pod_id_1", "retrans_tx", "retrans_rx", "status", "tap_side", "observed_at", "l7_protocol", "response_code", "rrt", "request_resource"}, "values": [][]any{{1, 1, 0, 0, 3, "c-p", now.Add(-5 * time.Second).Unix(), 20, 503, 2500, "/secret?password=do-not-export"}}}})
	}))
	defer backend.Close()
	binding := evidence.Binding{SourceType: "deepflow", Tenant: "tenant-a", SourceID: "df-a", Revision: 1, BackendLogicalID: "querier", Endpoint: backend.URL, ScopeMapping: datascope.Mapping{Scopes: map[string][]string{"organization": {"1"}, "cluster": {cluster}, "namespace": {"apps"}}}}
	authorize := func(context.Context, evidence.Binding) error {
		if !allowed {
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
	nativeContainer = strings.Repeat("c", 64)
	if err = mapper.ValidateCurrent(t.Context()); err == nil {
		t.Fatal("container restart during query accepted")
	}
	nativeContainer = container
	allowed = false
	if _, err = mapper.ByCanonical(t.Context(), id); err == nil {
		t.Fatal("withdrawn source usable")
	}
	if _, err = New(binding, backend.Client(), FrozenMappings{}, authorize, false, "live"); err == nil {
		t.Fatal("frozen mappings promoted to live")
	}
}
