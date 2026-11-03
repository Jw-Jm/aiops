package analytics

import (
	"context"
	"net/http"
	"ops-platform/internal/datascope"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/resource"
	"testing"
	"time"
)

func TestQualificationBindsCapturedResourceUIDs(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	tenant := "11111111-1111-4111-8111-111111111111"
	source := evidence.Binding{SourceID: "11111111-1111-4111-8111-111111111112", SourceType: "victorialogs", Tenant: tenant, Revision: 1, Endpoint: "https://logs.example.test", ScopeMapping: datascope.Mapping{RequiredLabels: map[string]string{"tenant": tenant}, Scopes: map[string][]string{"cluster": {"cluster"}, "namespace": {"apps"}}}}
	proof := Qualification{ResourceUIDs: []string{"captured"}, DatasetID: "logs.records", Operation: "samples", SourceRevision: 1, BindingDigest: evidence.BindingScopeDigest(source), EvidenceDigest: graph.ScopeDigest("controlled UID proof"), CollectorVersion: "controlled/v1", VerifiedUntil: now.Add(time.Hour), CoverageFrom: now.Add(-time.Hour), CoverageTo: now, MaxWindowSeconds: 3600}
	adapter, err := NewVictoria(source, http.DefaultClient, func(context.Context, evidence.Binding) error { return nil }, []Qualification{proof})
	if err != nil {
		t.Fatal(err)
	}
	proof.ResourceUIDs[0] = "caller-mutated-proof"
	intent := QueryIntent{SchemaVersion: "query-intent/v1", DatasetID: "logs.records", Operation: "samples", Metrics: []string{"log_count"}, Dimensions: []string{}, Filters: []Filter{}, Sort: []Sort{}, Limit: 2, TimeRange: &QueryTimeRange{From: now.Add(-time.Minute), To: now, Timezone: "UTC"}}
	scope := graph.Scope{Tenant: tenant, Cluster: "cluster", Namespaces: []string{"apps"}, AuthorizationRevision: "version"}
	for _, uid := range []string{"", "captured", "recreated"} {
		t.Run(uid, func(t *testing.T) {
			selection := Selection{}
			current := scope
			if uid != "" {
				id := resource.CanonicalID{Domain: "k8s", Tenant: tenant, Scope: "cluster", APIGroup: "core", Kind: "Pod", StableID: uid}.String()
				selection.CanonicalIDs = []string{id}
				current.Resources = []string{id}
			}
			_, err := adapter.Compile(intent, Binding{Subject: "operator", Grant: scope, Selection: selection})
			caps, caperr := adapter.Capabilities(t.Context(), current)
			if uid == "captured" {
				if err != nil || caperr != nil || len(caps) != 1 {
					t.Fatalf("captured UID rejected: %v %v %v", err, caperr, caps)
				}
			} else if err == nil || len(caps) != 0 {
				t.Fatalf("unobserved UID was qualified: %v %v", err, caps)
			}
		})
	}
}
