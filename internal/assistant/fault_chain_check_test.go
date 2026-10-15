package assistant

import (
	"github.com/google/uuid"
	"ops-platform/internal/analytics"
	"ops-platform/internal/graph"
	"ops-platform/internal/investigation"
	"ops-platform/internal/resource"
	"testing"
	"time"
)

func TestFaultChainCheckBindsOriginalGapSelectionAndWindow(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	parent := investigation.FaultChainContext{MissingEvidence: []string{"committed_scoped_resource_relations"}, Request: investigation.FaultChainRequest{SchemaVersion: "fault-chain-request/v1", TargetCanonicalID: "target", Fault: analytics.QueryTimeRange{From: now.Add(-time.Minute), To: now, Timezone: "UTC"}, MaxEdges: 4}, Edges: []investigation.FaultChainEdge{{Relation: resource.Relation{From: "target", To: "peer", Kind: "communicates_with"}, RelationDigest: graph.ScopeDigest("relation"), CallDirection: "outgoing", MissingEvidence: []string{"shared_resource_cause_not_checked", "network_window_statistics", "historical_relation_unverified"}, Observations: []investigation.ChainObservation{{RelatedRelations: []resource.Relation{{From: "target", To: "node"}}}}}}}
	check := investigation.FaultChainReadOnlyCheck{OriginTurnID: uuid.New(), RelationDigest: parent.Edges[0].RelationDigest, GapCode: "shared_resource_cause_not_checked", CanonicalID: "node", Tool: "get_resource_health"}
	for _, name := range []string{"health", "global-gap", "global-forged-relation", "dependencies", "statistics", "window", "target", "maxEdges", "relation", "gap", "tool", "undisclosed", "wrong-peer", "direction", "recursive"} {
		t.Run(name, func(t *testing.T) {
			request := parent.Request
			candidate := check
			request.ReadOnlyCheck = &candidate
			original := parent
			want := false
			switch name {
			case "global-gap", "global-forged-relation":
				candidate.GapCode = "committed_scoped_resource_relations"
				candidate.Tool = "get_dependencies"
				candidate.CanonicalID = "target"
				candidate.RelationDigest = ""
				want = name == "global-gap"
				if !want {
					candidate.RelationDigest = graph.ScopeDigest("forged")
				}
			case "health":
				want = true
			case "dependencies":
				candidate.GapCode = "historical_relation_unverified"
				candidate.Tool = "get_dependencies"
				candidate.CanonicalID = "target"
				want = true
			case "statistics", "wrong-peer", "direction":
				candidate.GapCode = "network_window_statistics"
				candidate.Tool = "execute_analysis_query"
				candidate.CanonicalID = "peer"
				candidate.Direction = "outgoing"
				want = name == "statistics"
				if name == "wrong-peer" {
					candidate.CanonicalID = "node"
				}
				if name == "direction" {
					candidate.Direction = "incoming"
				}
			case "window":
				request.Fault.From = request.Fault.From.Add(-time.Second)
			case "target":
				request.TargetCanonicalID = "other"
			case "maxEdges":
				request.MaxEdges = 8
			case "relation":
				candidate.RelationDigest = graph.ScopeDigest("other")
			case "gap":
				candidate.GapCode = "clock_and_event_time_unverified"
			case "tool":
				candidate.Tool = "get_dependencies"
			case "undisclosed":
				candidate.CanonicalID = "other"
			case "recursive":
				original.Request.ReadOnlyCheck = &check
			}
			if got := validateFaultChainCheck(request, original); got != want {
				t.Fatalf("accepted=%v want=%v", got, want)
			}
		})
	}
}
