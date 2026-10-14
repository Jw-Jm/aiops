package httpapi

import (
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
