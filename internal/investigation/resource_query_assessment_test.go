package investigation

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/analytics"
	"ops-platform/internal/graph"
	"ops-platform/internal/resource"
)

// The judgement of a committed metric query names the committed Step that produced
// it, not the query result. The source-step reader only accepts a real step of the
// same job, so a query result id in the judgement can never be resolved. The fact
// references keep the query result: they point at the observation itself.
func TestResourceQueryAssessmentNamesTheCommittedStep(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	target := resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: "Node", StableID: "node-a"}.String()
	window := analytics.QueryTimeRange{From: now.Add(-time.Minute), To: now, Timezone: "UTC"}
	request := FaultChainRequest{SchemaVersion: "fault-chain-request/v1", TargetCanonicalID: target, Fault: window, MaxEdges: 4}
	scope := graph.Scope{Tenant: "tenant-a", Cluster: "cluster-a", Namespaces: []string{"apps"}}
	result := analytics.QueryResult{
		SchemaVersion: "query-result/v1", DatasetID: "metrics.node-ready",
		PlanDigest: graph.ScopeDigest("plan"), CatalogDigest: graph.ScopeDigest("catalog"),
		ScopeDigest: graph.ScopeDigest(scope), Status: "complete", Exact: true,
		Unit: "resources", Dimension: "", Rows: []analytics.Row{{Dimension: "current", Value: 0}},
		TimeRange: &window, SnapshotReadAt: now, Freshness: "fresh",
		Warnings: []string{}, DegradedSources: []string{},
		SourceManifest: &analytics.SourceManifest{SourceID: uuid.NewString(), Revision: 1,
			BindingDigest: graph.ScopeDigest("b"), QualificationDigest: graph.ScopeDigest("q"),
			QueryHash: graph.ScopeDigest("query"), TemplateVersion: "fixture/v1",
			CoverageFrom: now.Add(-time.Hour), CoverageTo: now},
	}
	stored := BuildStoredQueryResult(result)
	step := uuid.Must(uuid.NewV7())
	assessments := AssessResourceQuery(request, target, step, stored, false)
	if len(assessments) != 1 {
		t.Fatalf("assessments = %+v", assessments)
	}
	assessment := assessments[0]
	if len(assessment.Supports) != 1 || assessment.Supports[0] != step {
		t.Fatalf("supports = %v, want the committed step %s", assessment.Supports, step)
	}
	if len(assessment.Conflicts) != 0 {
		t.Fatalf("a not-ready statistic must not be a counterevidence: %v", assessment.Conflicts)
	}
	if len(assessment.FactRefs) == 0 {
		t.Fatal("the judgement lost its fact references")
	}
	for _, ref := range assessment.FactRefs {
		if ref.QueryResultID != stored.QueryResultID {
			t.Fatalf("fact reference = %s, want the query result %s", ref.QueryResultID, stored.QueryResultID)
		}
	}
	// A healthy observation is the counterevidence of the same committed step.
	healthy := result
	healthy.Rows = []analytics.Row{{Dimension: "current", Value: 1}}
	healthyStored := BuildStoredQueryResult(healthy)
	conflicts := AssessResourceQuery(request, target, step, healthyStored, false)
	if len(conflicts) != 1 || len(conflicts[0].Conflicts) != 1 || conflicts[0].Conflicts[0] != step {
		t.Fatalf("counterevidence = %+v, want the committed step %s", conflicts, step)
	}
	if len(conflicts[0].Supports) != 0 {
		t.Fatalf("a healthy observation must not support the fault: %v", conflicts[0].Supports)
	}
}
