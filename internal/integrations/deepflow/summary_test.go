package deepflow

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"ops-platform/internal/datascope"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/resource"
	"strings"
	"testing"
	"time"
)

func TestSummaryAggregatesBeyondDetailLimitAndRechecksScope(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	id := func(uid string) string {
		return resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: uid}.String()
	}
	target, peer := id("a"), id("b")
	calls := 0
	allowed := true
	bad := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = r.ParseForm()
		sql := r.Form.Get("sql")
		for _, need := range []string{"Count(row)", "time < ", "pod_id_0 = 1 AND pod_id_1 = 2", "tap_side = 's-p'", "l7_protocol = 20"} {
			if !strings.Contains(sql, need) {
				t.Errorf("missing fixed predicate %s", need)
			}
		}
		if !strings.Contains(sql, "GROUP BY response_code LIMIT 401") || strings.Contains(sql, "request_resource") {
			t.Error("detail/sensitive query used")
		}
		if r.Header.Get("X-Org-Id") != "1" {
			t.Error("missing organization")
		}
		errors := 50
		if bad {
			errors = -1
		}
		fmt.Fprintf(w, `{"OPT_STATUS":"SUCCESS","result":{"columns":["response_code","observations","duration_us"],"values":[[200,950,237500000],[503,%d,12500000]]}}`, errors)
	}))
	defer server.Close()
	binding := evidence.Binding{SourceType: "deepflow", Tenant: "tenant-a", SourceID: "df-a", Revision: 1, BackendLogicalID: "querier", Endpoint: server.URL, ScopeMapping: datascope.Mapping{Scopes: map[string][]string{"organization": {"1"}, "cluster": {"cluster-a"}, "namespace": {"apps"}}}}
	adapter, _ := New(binding, server.Client(), mappings{target: Endpoint{CanonicalID: target, Namespace: "apps", PodID: 1, ClusterID: 2, NamespaceID: 3}, peer: Endpoint{CanonicalID: peer, Namespace: "apps", PodID: 2, ClusterID: 2, NamespaceID: 3}}, func(context.Context, evidence.Binding) error {
		if !allowed {
			return evidence.ErrScopeUnverified
		}
		return nil
	}, true, "fixture_only")
	q := SummaryQuery{Target: target, Peer: peer, Direction: "outgoing", ObservationPoint: "s-p", From: now.Add(-time.Minute), To: now, Scope: graph.Scope{Tenant: "tenant-a", Cluster: "cluster-a", Namespaces: []string{"apps"}}, L7: true}
	s, out, err := adapter.Summary(t.Context(), q)
	if err != nil || out.Partial || s.Responses != 1000 || s.Errors != 50 || s.DurationMicroseconds != 250000000 || len(out.Evidence) != 1 || out.Evidence[0].TimeReliable {
		t.Fatalf("aggregate %+v %+v %v", s, out, err)
	}
	q.Scope.Resources = []string{target}
	if _, _, err = adapter.Summary(t.Context(), q); err != evidence.ErrScopeUnverified || calls != 1 {
		t.Fatal("scope widened")
	}
	q.Scope.Resources = nil
	q.ObservationPoint = "s-p' OR 1=1"
	if _, _, err = adapter.Summary(t.Context(), q); err == nil || calls != 1 {
		t.Fatal("untrusted observation strategy accepted")
	}
	q.ObservationPoint = "s-p"
	bad = true
	if _, out, err = adapter.Summary(t.Context(), q); err != nil || !out.Partial || len(out.Evidence) != 0 {
		t.Fatal("invalid numerator became evidence")
	}
	allowed = false
	if _, _, err = adapter.Summary(t.Context(), q); err != evidence.ErrScopeUnverified {
		t.Fatal("revoked source queried")
	}
}

func TestTCPRecordSummaryUsesProtocolAndDoesNotInventPacketLossRate(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	id := func(uid string) string {
		return resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: uid}.String()
	}
	target, peer := id("a"), id("b")
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		sql := r.Form.Get("sql")
		if !strings.Contains(sql, "AND protocol = 6 GROUP BY status LIMIT 257") || !strings.Contains(sql, "Count(row)") {
			t.Error("TCP population not fixed")
		}
		fmt.Fprint(w, `{"OPT_STATUS":"SUCCESS","result":{"columns":["status","observations","retrans_tx","retrans_rx"],"values":[[0,1000,2,3],[3,5,1,0]]}}`)
	}))
	defer backend.Close()
	b := evidence.Binding{SourceType: "deepflow", Tenant: "tenant-a", SourceID: "df", Revision: 1, Endpoint: backend.URL, ScopeMapping: datascope.Mapping{Scopes: map[string][]string{"organization": {"1"}, "cluster": {"cluster-a"}, "namespace": {"apps"}}}}
	a, _ := New(b, backend.Client(), mappings{target: Endpoint{CanonicalID: target, Namespace: "apps", PodID: 1, ClusterID: 2, NamespaceID: 3}, peer: Endpoint{CanonicalID: peer, Namespace: "apps", PodID: 2, ClusterID: 2, NamespaceID: 3}}, func(context.Context, evidence.Binding) error { return nil }, false, "fixture_only")
	s, result, err := a.Summary(t.Context(), SummaryQuery{Target: target, Peer: peer, Direction: "outgoing", ObservationPoint: "s-p", From: now.Add(-time.Minute), To: now, Scope: graph.Scope{Tenant: "tenant-a", Cluster: "cluster-a", Namespaces: []string{"apps"}}})
	if err != nil || result.Partial || s.Observations != 1005 || s.Retransmissions != 6 || s.Failures != 5 || s.Protocol != "tcp" || s.TimeReliable {
		t.Fatalf("TCP record counts %+v %v", s, err)
	}
}
