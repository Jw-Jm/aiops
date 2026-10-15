package investigation

import (
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/analytics"
	"ops-platform/internal/contract"
	"ops-platform/internal/graph"
	"ops-platform/internal/resource"
)

func TestRecoveryRequiresBaselineBothProtocolsAndLatestHealthySnapshots(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	window := func(offset int) *analytics.QueryTimeRange {
		return &analytics.QueryTimeRange{From: now.Add(time.Duration(offset) * time.Minute), To: now.Add(time.Duration(offset+1) * time.Minute), Timezone: "UTC"}
	}
	id := func(uid string) string {
		return resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: uid}.String()
	}
	a, b := id("a"), id("b")
	request := FaultChainRequest{SchemaVersion: "fault-chain-request/v1", TargetCanonicalID: a, Baseline: window(-3), Fault: *window(-2), Recovery: window(-1), MaxEdges: 4}
	source := uuid.NewString()
	relation := resource.Relation{From: a, To: b, Kind: "communicates_with", ObservedAt: now.Add(-90 * time.Second), Provenance: resource.Provenance{SourceRegistrationID: source}}
	query := func(dataset string, recovery bool) StoredQueryResult {
		w, prior := &request.Fault, request.Baseline
		base, current := float64(1), float64(5)
		if recovery {
			w, prior = request.Recovery, &request.Fault
			base, current = 5, 0
		}
		stats := []analytics.Statistic{{Key: "baseline.responses", Value: 1000, Unit: "responses"}, {Key: "current.responses", Value: 1000, Unit: "responses"}, {Key: "baseline.5xx", Value: base * 10, Unit: "responses"}, {Key: "current.5xx", Value: current * 10, Unit: "responses"}, {Key: "baseline.mean_response_ms", Value: base * 10, Unit: "milliseconds"}, {Key: "current.mean_response_ms", Value: current * 10, Unit: "milliseconds"}}
		protocol, point, unit := "http", "s-p", "percent"
		if dataset == "network.tcp-summary" {
			protocol, point, unit = "tcp", "s", "observations"
			stats = []analytics.Statistic{{Key: "baseline.failure_observations", Value: base, Unit: "observations"}, {Key: "current.failure_observations", Value: current, Unit: "observations"}, {Key: "baseline.retransmissions", Value: base, Unit: "retransmissions"}, {Key: "current.retransmissions", Value: current, Unit: "retransmissions"}}
		}
		return BuildStoredQueryResult(analytics.QueryResult{SchemaVersion: "query-result/v1", DatasetID: dataset, PlanDigest: graph.ScopeDigest([]any{dataset, recovery}), CatalogDigest: graph.ScopeDigest("catalog"), ScopeDigest: graph.ScopeDigest("scope"), Status: "complete", Exact: true, Unit: unit, Dimension: "comparison", Rows: []analytics.Row{{Dimension: "baseline", Value: base}, {Dimension: "current", Value: current}}, TimeRange: w, ComparisonTimeRange: prior, SnapshotReadAt: now, Freshness: "fresh", Warnings: []string{}, DegradedSources: []string{}, Statistics: stats, SourceManifest: &analytics.SourceManifest{SourceID: source, Revision: 1, BindingDigest: graph.ScopeDigest("binding"), QualificationDigest: graph.ScopeDigest(dataset), QueryHash: graph.ScopeDigest("query"), TemplateVersion: "fixture/v1", CoverageFrom: request.Baseline.From, CoverageTo: now}, NetworkContext: &analytics.NetworkContext{Selection: analytics.NetworkSelection{TargetCanonicalID: a, PeerCanonicalID: b, Direction: "outgoing"}, ObservationPoint: point, Population: "scoped_single_observation_point_records", Protocol: protocol, TimeReliable: true, CoverageComplete: true}, DerivationEvidenceRefs: []string{uuid.NewString()}})
	}
	original := []StoredQueryResult{query("network.http-summary", false), query("network.http-summary", true), query("network.tcp-summary", false), query("network.tcp-summary", true)}
	for _, name := range []string{"qualified", "missing-baseline", "unreliable", "partial", "policy-changed", "still-above-baseline", "missing-health", "late-not-ready", "late-unknown", "half-open-health", "transport-missing", "no-responses"} {
		t.Run(name, func(t *testing.T) {
			req := request
			qs := slices.Clone(original)
			hs := []FaultChainResourceHealth{}
			for _, pod := range []string{a, b} {
				hs = append(hs, FaultChainResourceHealth{StepID: uuid.New(), CanonicalID: pod, ObservedAt: request.Recovery.From.Add(time.Second), ConditionType: "Ready", ConditionStatus: "True", Sources: []graph.SourceAuthority{{SourceRegistrationID: source, Revision: 1, ScopeDigest: graph.ScopeDigest("native")}}})
			}
			r := qs[1].Result
			n := *r.NetworkContext
			r.NetworkContext = &n
			r.Statistics = slices.Clone(r.Statistics)
			switch name {
			case "missing-baseline":
				req.Baseline = nil

			case "unreliable":
				n.TimeReliable = false
			case "partial":
				r.Exact = false
				r.Status = "partial"
			case "policy-changed":
				n.ObservationPoint = "c-p"
			case "still-above-baseline":
				r.Statistics[3].Value = 20
			case "missing-health":
				hs = hs[:1]
			case "late-not-ready", "late-unknown":
				h := hs[1]
				h.StepID = uuid.New()
				h.ConditionStatus = "False"
				if name == "late-unknown" {
					h.ConditionStatus = "Unknown"
				}
				h.ObservedAt = h.ObservedAt.Add(time.Second)
				hs = append(hs, h)
			case "half-open-health":
				hs[1].ObservedAt = request.Recovery.To
			case "transport-missing":
				qs = qs[:2]
			case "no-responses":
				r.Statistics[1].Value = 0
			}
			qs[1] = BuildStoredQueryResult(r)
			for i := range qs {
				qs[i] = BuildStoredQueryResult(qs[i].Result)
			}
			if name == "missing-baseline" {
				qs = []StoredQueryResult{qs[1], qs[3]}
			}
			out, err := EvaluateFaultChainWithResourceHealth(req, []resource.Relation{relation}, qs, now, hs, nil)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, ob := range out.Edges[0].Observations {
				if ob.RuleID == "R5/recovery-consistency" {
					found = true
					if ob.ResourceHealth == nil || ob.ResourceHealth.ConditionStatus != "True" || len(ob.Supports) != 4 {
						t.Fatal("missing committed parents", ob)
					}
				}
			}
			if found != (name == "qualified") {
				t.Fatal("recovery gate violated", name, out)
			}
			if found {
				if err := contract.Validate("https://ops.local/schemas/fault-chain-context/v1", raw(out)); err != nil {
					t.Fatal(err)
				}
			}
			if !found && !slices.Contains(out.Edges[0].MissingEvidence, "recovery_baseline_and_health_unverified") {
				t.Fatal("recovery gap lost")
			}
		})
	}
}
