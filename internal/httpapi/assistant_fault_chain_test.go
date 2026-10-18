package httpapi

import (
	"github.com/google/uuid"
	"testing"
	"time"

	"ops-platform/internal/analytics"
	"ops-platform/internal/assistant"
	"ops-platform/internal/graph"
	"ops-platform/internal/investigation"
	"ops-platform/internal/resource"
)

func TestFaultChainEvaluationUsesFrozenOrdinaryTurn(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	target := resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: "pod-a"}.String()
	scope := graph.Scope{Tenant: "tenant-a", Cluster: "cluster-a", Namespaces: []string{"apps"}, Resources: []string{target}}
	input := assistant.TurnInput{RequestedAt: now, Binding: assistant.ConversationBinding{Timezone: "UTC", ResourceContext: &assistant.ResourceContext{Resources: []assistant.Candidate{{CanonicalID: target, Namespace: "apps"}}, TimeRange: assistant.TimeRange{From: now.Add(-10 * time.Minute), To: now.Add(-5 * time.Minute), Timezone: "UTC"}}}}
	job := investigation.Job{Scope: scope, Assistant: &investigation.AssistantTask{TaskKind: "resource_troubleshooting", AuthorizationDomain: "operational"}}
	request, err := faultChainEvaluationRequest(input, job)
	if err != nil || request.TargetCanonicalID != target || !request.Fault.From.Equal(input.Binding.ResourceContext.TimeRange.From) || request.Baseline != nil || request.Recovery != nil || request.MaxEdges != 4 {
		t.Fatal("ordinary turn did not use frozen resource context", err)
	}
	explicit := analytics.QueryTimeRange{From: now.Add(-2 * time.Minute), To: now.Add(-time.Minute), Timezone: "UTC"}
	input.RequestedTimeRange = &explicit
	request, err = faultChainEvaluationRequest(input, job)
	if err != nil || request.Fault != explicit {
		t.Fatal("explicit frozen message window was ignored", err)
	}
	for _, test := range []string{"audit", "other-task", "missing-target", "ambiguous", "namespace", "future-window", "out-of-scope"} {
		t.Run(test, func(t *testing.T) {
			in := input
			j := job
			task := *job.Assistant
			j.Assistant = &task
			rc := *input.Binding.ResourceContext
			rc.Resources = append([]assistant.Candidate{}, rc.Resources...)
			in.Binding.ResourceContext = &rc
			window := *input.RequestedTimeRange
			in.RequestedTimeRange = &window
			j.Scope = analytics.CloneScope(job.Scope)
			switch test {
			case "audit":
				j.Assistant.AuthorizationDomain = "audit"
			case "other-task":
				j.Assistant.TaskKind = "audit_query"
			case "missing-target":
				in.Binding.ResourceContext = nil
			case "ambiguous":
				rc.Resources = append(rc.Resources, rc.Resources[0])
			case "namespace":
				rc.Resources[0].Namespace = "other"
			case "future-window":
				window.To = now.Add(time.Minute)
			case "out-of-scope":
				j.Scope.Resources = []string{"unrelated"}
			}
			if _, err := faultChainEvaluationRequest(in, j); err == nil {
				t.Fatal("unqualified frozen context was accepted")
			}
		})
	}
}

func TestFaultChainRelationReadKeepsFrozenIdentityWindowAndSelection(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	target := resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: "pod-a"}.String()
	window := analytics.QueryTimeRange{From: now.Add(-20 * time.Minute), To: now.Add(-19 * time.Minute), Timezone: "UTC"}
	input := assistant.TurnInput{RequestedAt: now, FaultChainRequest: &investigation.FaultChainRequest{SchemaVersion: "fault-chain-request/v2", TargetCanonicalID: target, Fault: window, MaxEdges: 1}}
	j := investigation.Job{Scope: graph.Scope{Tenant: "tenant-a", Cluster: "cluster-a", Namespaces: []string{"apps", "other"}, ClusterScoped: true}, Assistant: &investigation.AssistantTask{AuthorizationDomain: "operational"}}
	for _, restricted := range []bool{false, true} {
		if restricted {
			j.Scope.Resources = []string{target}
		}
		scope, err := faultChainRelationScope(input, j, target, &window, "apps")
		if err != nil || scope.ClusterScoped || len(scope.Namespaces) != 1 || scope.Namespaces[0] != "apps" || !graph.ContainsScope(j.Scope, scope) || len(scope.Resources) != len(j.Scope.Resources) {
			t.Fatal("relation read expanded the frozen selection", scope, err)
		}
		if len(j.Scope.Namespaces) != 2 || !j.Scope.ClusterScoped {
			t.Fatal("read mutated job scope")
		}
	}
	for _, name := range []string{"new-window", "other-target", "outside-namespace", "node", "missing-window"} {
		t.Run(name, func(t *testing.T) {
			id, ns, w := target, "apps", &window
			copy := window
			switch name {
			case "new-window":
				copy.From = now.Add(-time.Minute)
				copy.To = now
				w = &copy
			case "other-target":
				id = target + "-replacement"
			case "outside-namespace":
				ns = "unselected"
			case "node":
				id = "k8s+v1://tenant-a/cluster-a/core/Node/node-a"
			case "missing-window":
				w = nil
			}
			if _, err := faultChainRelationScope(input, j, id, w, ns); err == nil {
				t.Fatal("unfrozen relation request admitted")
			}
		})
	}
}

func TestIncidentFaultChainRequiresResolvedReferentAndFrozenWindow(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	target := resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: "pod-a"}.String()
	incident := uuid.New()
	window := analytics.QueryTimeRange{From: now.Add(-time.Minute), To: now, Timezone: "UTC"}
	for _, name := range []string{"resolved", "ambiguous", "missing-window", "wrong-incident", "outside-scope"} {
		t.Run(name, func(t *testing.T) {
			input := assistant.TurnInput{RequestedAt: now, RequestedTimeRange: &window, Binding: assistant.ConversationBinding{IncidentID: incident, Timezone: "UTC"}, ReferenceResolution: &assistant.ReferenceResolution{Status: "resolved", CanonicalID: target, Candidates: []string{target}}}
			job := investigation.Job{IncidentID: incident, Scope: graph.Scope{Tenant: "tenant-a", Cluster: "cluster-a", Namespaces: []string{"apps"}, Resources: []string{target}}, Assistant: &investigation.AssistantTask{TaskKind: "incident_followup", AuthorizationDomain: "operational"}}
			switch name {
			case "ambiguous":
				input.ReferenceResolution.Status = "clarification"
			case "missing-window":
				input.RequestedTimeRange = nil
			case "wrong-incident":
				input.Binding.IncidentID = uuid.New()
			case "outside-scope":
				job.Scope.Resources = []string{"other"}
			}
			request, err := faultChainEvaluationRequest(input, job)
			if name == "resolved" {
				if err != nil || request.TargetCanonicalID != target || request.Fault != window {
					t.Fatal("frozen incident binding lost", err)
				}
			} else if err == nil {
				t.Fatal("unresolved incident accepted")
			}
		})
	}
}

func TestFrozenOperationalFollowupKeepsBaselineAndRecovery(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	tenant := uuid.NewString()
	target := resource.CanonicalID{Domain: "k8s", Tenant: tenant, Scope: "cluster", APIGroup: "core", Kind: "Pod", StableID: "uid"}.String()
	window := func(minutes int) *analytics.QueryTimeRange {
		return &analytics.QueryTimeRange{From: now.Add(time.Duration(minutes-1) * time.Minute), To: now.Add(time.Duration(minutes) * time.Minute), Timezone: "UTC"}
	}
	request := investigation.FaultChainRequest{SchemaVersion: "fault-chain-request/v2", TargetCanonicalID: target, Fault: *window(-2), Baseline: window(-3), Recovery: window(-1), MaxEdges: 4}
	input := assistant.TurnInput{FrozenCorrelationRequest: &request, RequestedAt: now, Binding: assistant.ConversationBinding{Timezone: "UTC"}}
	j := investigation.Job{Scope: graph.Scope{Tenant: tenant, Cluster: "cluster", Namespaces: []string{"apps"}}, Assistant: &investigation.AssistantTask{AuthorizationDomain: "operational"}}
	got, err := faultChainEvaluationRequest(input, j)
	if err != nil || graph.ScopeDigest(got) != graph.ScopeDigest(request) {
		t.Fatal("followup replaced frozen comparison windows", err)
	}
}
