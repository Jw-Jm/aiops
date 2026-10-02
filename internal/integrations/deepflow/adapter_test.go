package deepflow

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"ops-platform/internal/datascope"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/resource"
	"os"
	"testing"
	"time"
)

type mappings map[string]Endpoint

func (m mappings) ByCanonical(ctx context.Context, id string) (Endpoint, error) { return m[id], nil }
func (m mappings) ByBackend(ctx context.Context, id uint64) (Endpoint, error) {
	for _, e := range m {
		if e.PodID == id {
			return e, nil
		}
	}
	return Endpoint{}, nil
}
func TestDeepFlowFixedTemplatesAndCanonicalMapping(t *testing.T) {
	now := time.Date(2026, 10, 1, 0, 0, 1, 0, time.UTC)
	id := resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: "pod-1"}.String()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/query/" || r.Header.Get("X-Org-Id") != "2" {
			t.Fatal("not Querier scoped")
		}
		r.ParseForm()
		if r.Form.Get("db") != "flow_log" {
			t.Fatal("database mismatch")
		}
		raw, err := os.ReadFile("../../../test/fixtures/deepflow/v7.2.0/l4-flow-response.json")
		if err != nil {
			t.Error(err)
			return
		}
		w.Write(raw)
	}))
	defer s.Close()
	b := evidence.Binding{Tenant: "tenant-a", SourceID: "df-a", Revision: 1, BackendLogicalID: "querier-a", Endpoint: s.URL, ScopeMapping: datascope.Mapping{Scopes: map[string][]string{"organization": {"2"}, "cluster": {"cluster-a"}, "namespace": {"apps"}}}}
	a, err := New(b, s.Client(), mappings{id: Endpoint{CanonicalID: id, Namespace: "apps", PodID: 1, ClusterID: 2, NamespaceID: 3}}, func(context.Context, evidence.Binding) error { return nil }, false, "fixture_only")
	if err != nil {
		t.Fatal(err)
	}
	q := evidence.Query{ResourceCanonicalID: id, Namespace: "apps", From: now.Add(-time.Minute), To: now, Limit: 10, Scope: graph.Scope{Tenant: "tenant-a", Cluster: "cluster-a", Namespaces: []string{"apps"}}}
	for _, operation := range []string{"GetResourceNetworkHealth", "GetNetworkDependencies", "GetNetworkPath", "FindTCPRetransmission", "FindPacketLoss", "FindConnectionFailure"} {
		q.Template = operation
		got, err := a.Query(context.Background(), q)
		if err != nil || len(got.Evidence) != 1 {
			t.Fatalf("%s: %+v %v", operation, got, err)
		}
	}
	q.Template = "GetTraceContext"
	if _, err := a.Query(context.Background(), q); err != ErrDisabled {
		t.Fatal("trace enabled")
	}
	q.Template = "SELECT *"
	if _, err := a.Query(context.Background(), q); err == nil {
		t.Fatal("generic SQL accepted")
	}
	for _, bad := range []string{"clickhouse://localhost:9000", "http://localhost:8123", "http://clickhouse:80"} {
		b.Endpoint = bad
		if _, err := New(b, s.Client(), mappings{}, func(context.Context, evidence.Binding) error { return nil }, false, "fixture_only"); err == nil {
			t.Fatal("ClickHouse accepted")
		}
	}
}

func TestDeepFlowRejectsRowsViolatingOperationPredicate(t *testing.T) {
	now := time.Now()
	id := resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: "pod-1"}.String()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"OPT_STATUS":"SUCCESS","result":{"columns":["pod_id_0","pod_id_1","retrans_tx","retrans_rx","status","tap_side","observed_at"],"values":[[1,1,0,0,0,"c-p",%d]]}}`, now.Unix())
	}))
	defer s.Close()
	b := evidence.Binding{Tenant: "tenant-a", SourceID: "df-a", Revision: 1, BackendLogicalID: "querier-a", Endpoint: s.URL, ScopeMapping: datascope.Mapping{Scopes: map[string][]string{"organization": {"2"}, "cluster": {"cluster-a"}, "namespace": {"apps"}}}}
	a, err := New(b, s.Client(), mappings{id: Endpoint{CanonicalID: id, Namespace: "apps", PodID: 1, ClusterID: 2, NamespaceID: 3}}, func(context.Context, evidence.Binding) error { return nil }, false, "fixture_only")
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"FindConnectionFailure", "FindTCPRetransmission"} {
		q := evidence.Query{Template: op, ResourceCanonicalID: id, Namespace: "apps", From: now.Add(-time.Minute), To: now, Limit: 10, Scope: graph.Scope{Tenant: "tenant-a", Cluster: "cluster-a", Namespaces: []string{"apps"}}}
		got, err := a.Query(context.Background(), q)
		if err != nil || !got.Partial || len(got.Evidence) != 0 || got.Freshness != "unavailable" {
			t.Fatalf("backend violated %s predicate: %+v %v", op, got, err)
		}
	}
}

func TestDeepFlowFrozenContractMissingExtraTimeoutEmptyAndUnknown(t *testing.T) {
	frozen, err := os.ReadFile("../../../test/fixtures/deepflow/v7.2.0/l4-flow-response.json")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 1, 0, 0, 1, 0, time.UTC)
	id := resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: "pod-1"}.String()
	for _, name := range []string{"extra-field", "missing-field", "timeout", "empty", "unknown-mapping"} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if name == "timeout" {
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
					select {
					case <-r.Context().Done():
					case <-time.After(200 * time.Millisecond):
					}
					return
				}
				var doc map[string]any
				json.Unmarshal(frozen, &doc)
				result := doc["result"].(map[string]any)
				switch name {
				case "extra-field":
					doc["new-publisher-field"] = "ignored"
				case "missing-field":
					result["columns"] = []string{"pod_id_0"}
				case "empty":
					result["values"] = []any{}
				case "unknown-mapping":
					row := result["values"].([]any)[0].([]any)
					row[1] = float64(999)
				}
				json.NewEncoder(w).Encode(doc)
			}))
			defer server.Close()
			b := evidence.Binding{Tenant: "tenant-a", SourceID: "df-a", Revision: 1, BackendLogicalID: "df-fixture", Endpoint: server.URL, ScopeMapping: datascope.Mapping{Scopes: map[string][]string{"organization": {"2"}, "cluster": {"cluster-a"}, "namespace": {"apps"}}}}
			a, err := New(b, server.Client(), mappings{id: Endpoint{CanonicalID: id, Namespace: "apps", PodID: 1, ClusterID: 2, NamespaceID: 3}}, func(context.Context, evidence.Binding) error { return nil }, false, "fixture_only")
			if err != nil {
				t.Fatal(err)
			}
			q := evidence.Query{Template: "GetNetworkDependencies", ResourceCanonicalID: id, Namespace: "apps", From: now.Add(-time.Minute), To: now, Limit: 10, TimeoutMillis: 50, Scope: graph.Scope{Tenant: "tenant-a", Cluster: "cluster-a", Namespaces: []string{"apps"}}}
			got, err := a.Query(context.Background(), q)
			if err != nil || got.QueryHash == "" {
				t.Fatalf("contract query=%+v %v", got, err)
			}
			if name == "extra-field" {
				if got.Partial || len(got.Evidence) != 1 {
					t.Fatal("additive publisher field rejected")
				}
				return
			}
			if len(got.Evidence) != 0 {
				t.Fatal("invalid/empty/unmapped source emitted facts")
			}
			if name == "missing-field" || name == "timeout" {
				if !got.Partial || got.Freshness != "unavailable" || len(got.DegradedSources) == 0 {
					t.Fatal("source failure not degraded")
				}
			}
			if name == "unknown-mapping" && !got.Partial {
				t.Fatal("unknown mapping treated complete")
			}
		})
	}
}
