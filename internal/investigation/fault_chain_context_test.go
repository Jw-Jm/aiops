package investigation

import (
	"github.com/google/uuid"
	"ops-platform/internal/analytics"
	"ops-platform/internal/archive"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/resource"
	"slices"
	"testing"
	"time"
)

func TestFaultChainWindowRulesAndStatisticReferences(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	id := resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: "a"}.String()
	window := analytics.QueryTimeRange{From: now.Add(-time.Minute), To: now, Timezone: "UTC"}
	r := FaultChainRequest{SchemaVersion: "fault-chain-request/v1", TargetCanonicalID: id, Fault: window, MaxEdges: 4}
	scope := graph.Scope{Tenant: "tenant-a", Cluster: "cluster-a", Namespaces: []string{"apps"}}
	if !r.Valid(scope, "UTC", now) {
		t.Fatal("valid frozen request rejected")
	}
	baseline := window
	r.Baseline = &baseline
	if r.Valid(scope, "UTC", now) {
		t.Fatal("overlap accepted")
	}
	r.Baseline = nil
	widened := window
	widened.From = widened.From.Add(-time.Second)
	if r.Allows(&widened) {
		t.Fatal("window widened")
	}
	result := analytics.QueryResult{SchemaVersion: "query-result/v1", DatasetID: "network.http-5xx-rate", PlanDigest: graph.ScopeDigest("plan"), CatalogDigest: graph.ScopeDigest("catalog"), ScopeDigest: graph.ScopeDigest(scope), Status: "complete", Exact: true, Unit: "percent", Dimension: "", Rows: []analytics.Row{{Dimension: "current", Value: 5}}, SnapshotReadAt: now, Freshness: "fresh", Warnings: []string{}, DegradedSources: []string{}, SourceManifest: &analytics.SourceManifest{SourceID: uuid.NewString(), Revision: 1, BindingDigest: graph.ScopeDigest("b"), QualificationDigest: graph.ScopeDigest("q"), QueryHash: graph.ScopeDigest("query"), TemplateVersion: "fixture/v1", CoverageFrom: now.Add(-time.Hour), CoverageTo: now}, Statistics: []analytics.Statistic{{Key: "current.responses", Value: 1000, Unit: "responses"}}}
	stored := BuildStoredQueryResult(result)
	if validateStoredQueryResult(stored) != nil || len(stored.Facts) != 2 || stored.Facts[1].Unit != "responses" || stored.Facts[1].Column != "statistic" {
		t.Fatal("statistic cell lost its unit or identity")
	}
	stored.Facts[1].Value = 0
	if validateStoredQueryResult(stored) == nil {
		t.Fatal("altered denominator accepted")
	}
}

func TestFaultChainCorrelationKeepsTemporalAndCausalGaps(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	id := func(uid string) string {
		return resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: uid}.String()
	}
	target, peer := id("a"), id("b")
	baseline := analytics.QueryTimeRange{From: now.Add(-3 * time.Minute), To: now.Add(-2 * time.Minute), Timezone: "UTC"}
	fault := analytics.QueryTimeRange{From: now.Add(-time.Minute), To: now, Timezone: "UTC"}
	request := FaultChainRequest{SchemaVersion: "fault-chain-request/v1", TargetCanonicalID: target, Fault: fault, Baseline: &baseline, MaxEdges: 4}
	relation := resource.Relation{From: target, To: peer, Kind: "communicates_with", ObservedAt: now, Provenance: resource.Provenance{SourceRegistrationID: "source"}}
	result := analytics.QueryResult{SchemaVersion: "query-result/v1", DatasetID: "network.http-5xx-rate", PlanDigest: graph.ScopeDigest("plan"), CatalogDigest: graph.ScopeDigest("catalog"), ScopeDigest: graph.ScopeDigest("scope"), Status: "complete", Exact: true, Unit: "percent", Dimension: "comparison", Rows: []analytics.Row{{Dimension: "baseline", Value: 1}, {Dimension: "current", Value: 5}, {Dimension: "increase", Value: 4}}, TimeRange: &fault, ComparisonTimeRange: &baseline, SnapshotReadAt: now, Freshness: "fresh", Warnings: []string{}, DegradedSources: []string{}, SourceManifest: &analytics.SourceManifest{SourceID: uuid.NewString(), Revision: 1, BindingDigest: graph.ScopeDigest("b"), QualificationDigest: graph.ScopeDigest("q"), QueryHash: graph.ScopeDigest("query"), TemplateVersion: "fixture/v1", CoverageFrom: now.Add(-time.Hour), CoverageTo: now}, Statistics: []analytics.Statistic{{Key: "current.responses", Value: 1000, Unit: "responses"}, {Key: "baseline.responses", Value: 1000, Unit: "responses"}}, NetworkContext: &analytics.NetworkContext{Selection: analytics.NetworkSelection{TargetCanonicalID: target, PeerCanonicalID: peer, Direction: "outgoing"}, ObservationPoint: "s-p", Population: "completed_http_session_observations", Protocol: "http", CoverageComplete: true}, DerivationEvidenceRefs: []string{uuid.NewString()}}
	query := BuildStoredQueryResult(result)
	if query.Facts[2].Unit != "percentage_points" {
		t.Fatal("rate difference mislabeled as relative percent")
	}
	out, err := EvaluateFaultChain(request, []resource.Relation{relation}, []StoredQueryResult{query}, now)
	if err != nil || len(out.Edges) != 1 || len(out.Edges[0].Observations) != 1 || out.Edges[0].TemporalAssessment != "unreliable" || out.Edges[0].Observations[0].RuleID != "R1/http-response-increase" || len(out.Edges[0].MissingEvidence) == 0 {
		t.Fatalf("context %+v %v", out, err)
	}
	// Fixture clocks are explicitly qualified only for this finite-predicate
	// test. Actual DeepFlow runtime clocks remain unverified.
	change := evidence.Evidence{EvidenceID: uuid.NewString(), TenantID: "tenant-a", ResourceCanonicalID: peer, Type: "change_event", SourceRegistrationID: uuid.NewString(), SourceSystem: "fixture-change-collector", SourceRevision: 1, SourceScopeDigest: graph.ScopeDigest("change-source"), TimeReliable: true, ObservedFrom: baseline.From.Add(time.Second), ObservedTo: baseline.From.Add(2 * time.Second), ReplayState: "archived_verified", ContentDigest: graph.ScopeDigest("fixture-change"), ArchiveRef: &evidence.ArchiveRef{Object: archive.ObjectRef{VersionID: "fixture-v1"}, PlaintextDigest: graph.ScopeDigest("fixture-change")}}
	for _, name := range []string{"qualified", "change-clock-unreliable", "network-clock-unreliable", "different-uid", "different-tenant", "overlapping-event", "outside-baseline", "archive-pending", "archive-proof-missing", "not-a-change", "no-baseline", "partial-statistics"} {
		t.Run("R4/"+name, func(t *testing.T) {
			candidate := change
			input := request
			stats := result
			network := *result.NetworkContext
			network.TimeReliable = true
			stats.NetworkContext = &network
			switch name {
			case "change-clock-unreliable":
				candidate.TimeReliable = false
			case "network-clock-unreliable":
				network.TimeReliable = false
			case "different-uid":
				candidate.ResourceCanonicalID = id("replacement-pod")
			case "different-tenant":
				candidate.TenantID = "other"
			case "overlapping-event":
				candidate.ObservedTo = fault.From
			case "outside-baseline":
				candidate.ObservedFrom = baseline.From.Add(-time.Second)
			case "archive-pending":
				candidate.ReplayState = "archive_pending"
			case "archive-proof-missing":
				candidate.ArchiveRef = nil
			case "not-a-change":
				candidate.Type = "log"
			case "no-baseline":
				input.Baseline = nil
				stats.ComparisonTimeRange = nil
				stats.Dimension = ""
				stats.Rows = []analytics.Row{{Dimension: "current", Value: 5}}
				stats.Statistics = []analytics.Statistic{{Key: "current.responses", Value: 1000, Unit: "responses"}, {Key: "current.5xx", Value: 50, Unit: "responses"}}
			case "partial-statistics":
				stats.Exact = false
				stats.Status = "partial"
			}
			got, err := EvaluateFaultChain(input, []resource.Relation{relation}, []StoredQueryResult{BuildStoredQueryResult(stats)}, now, []evidence.Evidence{candidate, candidate})
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, ob := range got.Edges[0].Observations {
				if ob.RuleID != "R4/change-before-observation-window" {
					continue
				}
				found = true
				if len(ob.FactRefs) == 0 || !slices.Contains(ob.Supports, candidate.EvidenceID) || len(ob.Supports) != 2 || !slices.Contains(got.EvidenceRefs, candidate.EvidenceID) {
					t.Fatal("change lost exact committed parents", ob)
				}
			}
			if found != (name == "qualified") {
				t.Fatal("unqualified change became an association", name, got)
			}
			if found && slices.Contains(got.Edges[0].MissingEvidence, "change_source_not_checked") {
				t.Fatal("qualified change left wrong missing source marker")
			}
		})
	}
	// Silence without a population is a gap, never counterevidence.
	positive := result
	result.DatasetID = "network.http-5xx"
	result.Unit = "responses"
	result.Rows = []analytics.Row{{Dimension: "current", Value: 0}}
	result.ComparisonTimeRange = nil
	result.Dimension = ""
	result.Statistics = []analytics.Statistic{{Key: "current.responses", Value: 0, Unit: "responses"}}
	out, err = EvaluateFaultChain(request, []resource.Relation{relation}, []StoredQueryResult{BuildStoredQueryResult(result)}, now)
	if err != nil || len(out.Edges[0].Observations) != 0 {
		t.Fatal("empty population treated as conflict")
	}
	result.Statistics[0].Value = 1000
	out, err = EvaluateFaultChain(request, []resource.Relation{relation}, []StoredQueryResult{BuildStoredQueryResult(result)}, now)
	if err != nil || len(out.Edges[0].Observations) != 1 || len(out.Edges[0].Observations[0].Conflicts) != 1 {
		t.Fatal("nonempty narrow contradiction unavailable")
	}
	result = positive
	result.Exact = false
	result.Status = "partial"
	query = BuildStoredQueryResult(result)
	out, err = EvaluateFaultChain(request, []resource.Relation{relation}, []StoredQueryResult{query}, now)
	if err != nil || len(out.Edges[0].Observations) != 0 || out.Edges[0].ComparisonStatus != "coverage_insufficient" {
		t.Fatal("partial observation used as causal evidence")
	}
	wrong := fault
	wrong.From = wrong.From.Add(-time.Second)
	result.TimeRange = &wrong
	query = BuildStoredQueryResult(result)
	if _, err = EvaluateFaultChain(request, []resource.Relation{relation}, []StoredQueryResult{query}, now); err != ErrDenied {
		t.Fatal("out-of-window fact accepted")
	}
}
