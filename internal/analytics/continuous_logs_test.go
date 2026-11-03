package analytics

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ops-platform/internal/datascope"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/resource"
)

func TestContinuousLogsRequiresFreshNativeIdentity(t *testing.T) {
	now := time.Now().UTC()
	row := map[string]string{"tenant": "tenant", "cluster": "cluster", "collector": evidence.KubernetesLogCollector, "kubernetes.pod_namespace": "apps", "kubernetes.pod_uid": "11111111-1111-4111-8111-111111111111", "_time": now.Add(-time.Second).Format(time.RFC3339Nano)}
	status := http.StatusOK
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status); json.NewEncoder(w).Encode(row) }))
	defer server.Close()
	b := evidence.Binding{SourceType: "victorialogs", Tenant: "tenant", Endpoint: server.URL, ScopeMapping: datascope.Mapping{RequiredLabels: map[string]string{"tenant": "tenant", "cluster": "cluster", "collector": evidence.KubernetesLogCollector}, Scopes: map[string][]string{"cluster": {"cluster"}, "namespace": {"apps"}}}}
	q := Qualification{Continuous: true, DatasetID: "logs.records", Operation: "samples", CoverageFrom: now.Add(-time.Hour)}
	v, err := NewVictoria(b, server.Client(), func(context.Context, evidence.Binding) error { return nil }, []Qualification{q})
	if err != nil {
		t.Fatal(err)
	}
	digest := q.Digest()
	if err = v.RefreshContinuousLogs(t.Context()); err != nil {
		t.Fatal(err)
	}
	fresh := v.currentQualifications()[0]
	if !fresh.VerifiedUntil.After(now) || fresh.CoverageTo.IsZero() || fresh.Digest() != digest {
		t.Fatalf("unstable or absent proof: %+v", fresh)
	}
	for _, test := range []struct {
		name, key, value string
		status           int
	}{{"wrong tenant", "tenant", "other", 200}, {"wrong namespace", "kubernetes.pod_namespace", "other", 200}, {"forged UID", "kubernetes.pod_uid", "invalid", 200}, {"stale", "_time", now.Add(-2 * time.Minute).Format(time.RFC3339Nano), 200}, {"future", "_time", now.Add(time.Minute).Format(time.RFC3339Nano), 200}, {"outage", "tenant", "tenant", 503}} {
		t.Run(test.name, func(t *testing.T) {
			old := row[test.key]
			row[test.key] = test.value
			status = test.status
			if v.RefreshContinuousLogs(t.Context()) == nil || !v.currentQualifications()[0].VerifiedUntil.IsZero() {
				t.Fatal("invalid source kept its lease")
			}
			row[test.key] = old
			status = 200
			if v.RefreshContinuousLogs(t.Context()) != nil {
				t.Fatal("source failed to recover")
			}
		})
	}
	changed := q
	changed.CollectorVersion = "different"
	if changed.Digest() == digest {
		t.Fatal("collector change didn't invalidate proof")
	}
	static := q
	static.Continuous = false
	changed = static
	changed.CoverageTo = now
	if changed.Digest() == static.Digest() {
		t.Fatal("static coverage digest changed semantics")
	}
}

func TestKubernetesArchiveUsesNativeUIDAndExposesContainerOrigin(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	tenant := "11111111-1111-4111-8111-111111111111"
	uid := "11111111-1111-4111-8111-111111111112"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		query := r.Form.Get("query")
		if !strings.Contains(query, `kubernetes.pod_uid:"`+uid+`"`) || !strings.Contains(query, `kubernetes.pod_namespace:"apps"`) {
			t.Error("query lost native identity", query)
		}
		json.NewEncoder(w).Encode(map[string]string{"_time": now.Add(-time.Second).Format(time.RFC3339Nano), "_msg": "token=hidden", "tenant": tenant, "cluster": "cluster", "collector": evidence.KubernetesLogCollector, "kubernetes.pod_namespace": "apps", "kubernetes.pod_uid": uid, "uid": "forged-payload", "namespace": "forged-payload", "kubernetes.container_name": "sidecar", "kubernetes.container_id": "containerd://real-container", "kubernetes.namespace_uid": "real-namespace"})
	}))
	defer server.Close()
	b := evidence.Binding{SourceType: "victorialogs", SourceID: uid, Tenant: tenant, Revision: 1, Endpoint: server.URL, ScopeMapping: datascope.Mapping{RequiredLabels: map[string]string{"tenant": tenant, "collector": evidence.KubernetesLogCollector}, Scopes: map[string][]string{"cluster": {"cluster"}, "namespace": {"apps"}}}}
	proof := Qualification{DatasetID: "logs.records", Operation: "samples", SourceRevision: 1, BindingDigest: evidence.BindingScopeDigest(b), EvidenceDigest: graph.ScopeDigest("proof"), CollectorVersion: "v1.52.0-ops1", VerifiedUntil: now.Add(time.Hour), CoverageFrom: now.Add(-time.Hour), CoverageTo: now, MaxWindowSeconds: 3600}
	v, err := NewVictoria(b, server.Client(), func(context.Context, evidence.Binding) error { return nil }, []Qualification{proof})
	if err != nil {
		t.Fatal(err)
	}
	id := resource.CanonicalID{Domain: "k8s", Tenant: tenant, Scope: "cluster", APIGroup: "core", Kind: "Pod", StableID: uid}.String()
	intent := QueryIntent{SchemaVersion: "query-intent/v1", DatasetID: "logs.records", Operation: "samples", Metrics: []string{"log_count"}, Dimensions: []string{}, Filters: []Filter{}, Sort: []Sort{}, Limit: 2, TimeRange: &QueryTimeRange{From: now.Add(-time.Minute), To: now, Timezone: "UTC"}}
	grant := graph.Scope{Tenant: tenant, Cluster: "cluster", Namespaces: []string{"apps"}, Resources: []string{id}, AuthorizationRevision: "v1"}
	plan, err := v.Compile(intent, Binding{Subject: "operator", Grant: grant, Selection: Selection{CanonicalIDs: []string{id}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := v.Execute(t.Context(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Samples) != 1 || result.Samples[0].Fields["container"] != "sidecar" || result.Samples[0].Fields["containerId"] != "containerd://real-container" || strings.Contains(result.Samples[0].Message, "hidden") {
		t.Fatalf("lost native origin or redaction: %+v", result)
	}
}
