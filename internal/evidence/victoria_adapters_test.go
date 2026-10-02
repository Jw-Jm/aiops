package evidence

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"ops-platform/internal/datascope"
	"ops-platform/internal/graph"
	"ops-platform/internal/resource"
	"strings"
	"testing"
	"time"
)

func TestVictoriaBoundScopeAndInjection(t *testing.T) {
	now := time.Now()
	id := resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: "uid-1"}.String()
	called := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		q := r.URL.Query().Get("query")
		for _, filter := range []string{`tenant="tenant-a"`, `cluster="cluster-a"`, `namespace="apps"`, `uid="uid-1"`} {
			if !strings.Contains(q, filter) {
				t.Errorf("missing mandatory filter: %s", q)
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "matrix", "result": []any{map[string]any{"metric": map[string]string{"tenant": "tenant-a", "cluster": "cluster-a", "namespace": "apps", "uid": "uid-1"}, "values": []any{[]any{now.Unix(), "1"}}}}}})
	}))
	defer s.Close()
	b := Binding{Tenant: "tenant-a", SourceID: "source-a", Revision: 1, BackendLogicalID: "vm-a", Endpoint: s.URL, ScopeMapping: datascope.Mapping{RequiredLabels: map[string]string{"tenant": "tenant-a"}, Scopes: map[string][]string{"cluster": {"cluster-a"}, "namespace": {"apps"}}}}
	adapter, _ := NewVictoria("victoriametrics", b, s.Client(), func(context.Context, Binding) error { return nil })
	q := Query{ResourceCanonicalID: id, Namespace: "apps", Template: "pod-phase/v1", From: now.Add(-time.Minute), To: now, Limit: 10, Scope: graph.Scope{Tenant: "tenant-a", Cluster: "cluster-a", Namespaces: []string{"apps"}}}
	result, err := adapter.Query(context.Background(), q)
	if err != nil || result.Partial || len(result.Evidence) != 1 {
		t.Fatalf("%+v %v", result, err)
	}
	for _, injection := range []string{"up OR secret", `pod-phase/v1{tenant=~".*"}`, "labels", "series", "topk(9,secret)"} {
		bad := q
		bad.Template = injection
		if _, err := adapter.Query(context.Background(), bad); err == nil {
			t.Fatal("query injection accepted")
		}
	}
	bad := q
	bad.Namespace = "other"
	if _, err := adapter.Query(context.Background(), bad); err == nil {
		t.Fatal("foreign namespace accepted")
	}
	if called != 1 {
		t.Fatal("invalid requests reached backend")
	}
	b.ScopeMapping.Scopes = map[string][]string{}
	denied, _ := NewVictoria("victoriametrics", b, s.Client(), func(context.Context, Binding) error { return nil })
	if _, err := denied.Query(context.Background(), q); err == nil {
		t.Fatal("empty scope enabled")
	}
}
func TestVictoriaMissingTenantAndTimeoutDegrade(t *testing.T) {
	now := time.Now()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "matrix", "result": []any{map[string]any{"metric": map[string]string{"namespace": "apps"}}}}})
	}))
	defer s.Close()
	b := Binding{Tenant: "tenant-a", SourceID: "source-a", Revision: 1, BackendLogicalID: "vm-a", Endpoint: s.URL, ScopeMapping: datascope.Mapping{RequiredLabels: map[string]string{"tenant": "tenant-a"}, Scopes: map[string][]string{"cluster": {"cluster-a"}, "namespace": {"apps"}}}}
	adapter, _ := NewVictoria("victoriametrics", b, s.Client(), func(context.Context, Binding) error { return nil })
	q := Query{ResourceCanonicalID: resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: "uid-1"}.String(), Namespace: "apps", Template: "pod-phase/v1", From: now.Add(-time.Minute), To: now, Limit: 10, Scope: graph.Scope{Tenant: "tenant-a", Cluster: "cluster-a", Namespaces: []string{"apps"}}}
	got, err := adapter.Query(context.Background(), q)
	if err != nil || !got.Partial || len(got.DegradedSources) == 0 || len(got.Evidence) != 0 {
		t.Fatal("untrusted unlabeled source not degraded")
	}
	if Redact(`password=abc Bearer abc.def secret: x`) == `password=abc Bearer abc.def secret: x` {
		t.Fatal("no redaction")
	}
}
