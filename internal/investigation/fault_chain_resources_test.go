package investigation

import (
	"encoding/json"
	"github.com/google/uuid"
	"ops-platform/internal/analytics"
	"ops-platform/internal/contract"
	"ops-platform/internal/graph"
	"ops-platform/internal/resource"
	"slices"
	"testing"
	"time"
)

func TestSharedResourceRequiresOwnSnapshotTwoCallersAndQualifiedTime(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	id := func(kind, uid string) string {
		return resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: kind, StableID: uid}.String()
	}
	a, b, c, d, node := id("Pod", "a"), id("Pod", "b"), id("Pod", "c"), id("Pod", "d"), id("Node", "node")
	fault := analytics.QueryTimeRange{From: now.Add(-time.Minute), To: now, Timezone: "UTC"}
	request := FaultChainRequest{SchemaVersion: "fault-chain-request/v1", TargetCanonicalID: a, Fault: fault, MaxEdges: 4}
	native, df := uuid.NewString(), uuid.NewString()
	path := func(from, to, kind, source string) resource.Relation {
		return resource.Relation{From: from, To: to, Kind: kind, ObservedAt: now.Add(-30 * time.Second), ValidFrom: now.Add(-30 * time.Second), Provenance: resource.Provenance{SourceRegistrationID: source, RuleVersion: "fixture/v1", ObservedAt: now.Add(-30 * time.Second)}}
	}
	relation := path(a, b, "communicates_with", df)
	paths := []resource.Relation{relation, path(a, node, "scheduled_on", native), path(c, node, "scheduled_on", native)}
	query := func(from, to string) StoredQueryResult {
		result := analytics.QueryResult{SchemaVersion: "query-result/v1", DatasetID: "network.http-summary", PlanDigest: graph.ScopeDigest(from), CatalogDigest: graph.ScopeDigest("catalog"), ScopeDigest: graph.ScopeDigest("scope"), Status: "complete", Exact: true, Unit: "percent", Dimension: "", Rows: []analytics.Row{{Dimension: "current", Value: 5}}, TimeRange: &fault, SnapshotReadAt: now, Freshness: "fresh", Warnings: []string{}, DegradedSources: []string{}, SourceManifest: &analytics.SourceManifest{SourceID: df, Revision: 1, BindingDigest: graph.ScopeDigest("b"), QualificationDigest: graph.ScopeDigest("q"), QueryHash: graph.ScopeDigest("query"), TemplateVersion: "fixture/v1", CoverageFrom: fault.From, CoverageTo: fault.To}, Statistics: []analytics.Statistic{{Key: "current.responses", Value: 1000, Unit: "responses"}, {Key: "current.5xx", Value: 50, Unit: "responses"}}, NetworkContext: &analytics.NetworkContext{Selection: analytics.NetworkSelection{TargetCanonicalID: from, PeerCanonicalID: to, Direction: "outgoing"}, ObservationPoint: "s-p", Population: "completed_http_session_observations", Protocol: "http", TimeReliable: true, CoverageComplete: true}, DerivationEvidenceRefs: []string{uuid.NewString()}}
		return BuildStoredQueryResult(result)
	}
	queries := []StoredQueryResult{query(a, b), query(c, d)}
	snapshot := graph.Result{Freshness: "fresh", CollectedAt: now, ObservationSources: []graph.SourceAuthority{{SourceRegistrationID: native, Revision: 1, ScopeDigest: graph.ScopeDigest("native")}}, Nodes: []resource.Entity{{CanonicalID: node, Kind: "Node", UpdatedAt: now.Add(-20 * time.Second), SourceRefs: []string{native}, Attributes: map[string]any{"status": map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": "False"}}}}}}}
	health := CommittedFaultChainResourceHealth(uuid.New(), snapshot)
	if len(health) != 1 {
		t.Fatal("qualified own snapshot missing")
	}
	for _, name := range []string{"qualified", "one-caller", "wrong-uid", "outside-window", "missing-relation", "wrong-relation-source", "unreliable-network", "partial", "no-own-health", "normal-second-caller", "common-downstream"} {
		t.Run(name, func(t *testing.T) {
			hs := slices.Clone(health)
			rs := slices.Clone(paths)
			qs := slices.Clone(queries)
			switch name {
			case "normal-second-caller":
				value := qs[1].Result
				value.Rows = []analytics.Row{{Dimension: "current", Value: 0}}
				value.Statistics = []analytics.Statistic{{Key: "current.responses", Value: 1000, Unit: "responses"}, {Key: "current.5xx", Value: 0, Unit: "responses"}}
				qs[1] = BuildStoredQueryResult(value)
			case "common-downstream":
				hs[0].CanonicalID = b
				rs = []resource.Relation{relation, path(c, b, "communicates_with", df)}
				qs[1] = query(c, b)
			case "one-caller":
				qs = qs[:1]
			case "wrong-uid":
				hs[0].CanonicalID = id("Node", "replacement")
			case "outside-window":
				hs[0].ObservedAt = fault.To
			case "missing-relation":
				rs = rs[:2]
			case "wrong-relation-source":
				rs[2].Provenance.SourceRegistrationID = uuid.NewString()
			case "unreliable-network":
				value := qs[1].Result
				n := *value.NetworkContext
				n.TimeReliable = false
				value.NetworkContext = &n
				qs[1] = BuildStoredQueryResult(value)
			case "partial":
				value := qs[1].Result
				value.Exact = false
				value.Status = "partial"
				qs[1] = BuildStoredQueryResult(value)
			case "no-own-health":
				hs = nil
			}
			out, err := EvaluateFaultChainWithResourceHealth(request, rs, qs, now, hs, nil)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, edge := range out.Edges {
				for _, ob := range edge.Observations {
					if ob.RuleID == "R3/shared-resource-observation" {
						found = true
						if ob.ResourceHealth == nil || ob.ResourceHealth.StepID != health[0].StepID || len(ob.RelatedRelations) != 2 || len(ob.Supports) != 2 || len(ob.FactRefs) != 2 || len(out.QueryResultRefs) != 2 {
							t.Fatal("shared resource lost immutable parents", ob)
						}
					}
				}
			}
			if found != (name == "qualified" || name == "common-downstream") {
				t.Fatal("shared resource predicate violated", name, out)
			}
			if found {
				raw, _ := json.Marshal(out)
				if err := contract.Validate("https://ops.local/schemas/fault-chain-context/v1", raw); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
	snapshot.Nodes[0].Attributes["status"] = map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": "Unknown"}}}
	unknown := CommittedFaultChainResourceHealth(uuid.New(), snapshot)
	if len(unknown) != 1 || unknown[0].ConditionStatus != "Unknown" {
		t.Fatal("unknown health lost; could replay earlier Ready=True")
	}
	snapshot.Nodes[0].Attributes["status"] = map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": "False"}, map[string]any{"type": "Ready", "status": "True"}}}
	if len(CommittedFaultChainResourceHealth(uuid.New(), snapshot)) != 0 {
		t.Fatal("conflicting Ready conditions accepted")
	}
	snapshot.Partial = true
	if len(CommittedFaultChainResourceHealth(uuid.New(), snapshot)) != 0 {
		t.Fatal("partial native snapshot qualified")
	}
	snapshot.Partial = false
	snapshot.ObservationSources = nil
	if len(CommittedFaultChainResourceHealth(uuid.New(), snapshot)) != 0 {
		t.Fatal("older unbound native snapshot qualified")
	}
}
