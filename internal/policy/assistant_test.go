package policy

import (
	"context"
	"github.com/google/uuid"
	"ops-platform/internal/auth"
	"ops-platform/internal/configregistry"
	"testing"
)

func TestAuditAssistantDoesNotInheritOperatorTools(t *testing.T) {
	ctx := context.Background()
	tenant := uuid.New()
	compiler, version, _, err := testSignedPolicy(ctx, t, tenant)
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := NewEvaluator(&staticPolicyResolver{version: version}, compiler, "baseline")
	if err != nil {
		t.Fatal(err)
	}
	input := PolicyInput{Request: auth.RequestContext{TenantID: tenant, Subject: "admin", Roles: []auth.Role{auth.PlatformAdmin}}, PrincipalType: PrincipalPlatformAdmin, RequestType: RequestTool, Scope: configregistry.Scope{Type: configregistry.ScopeTenant}, ToolName: "execute_analysis_query", AllowedTools: []string{"execute_analysis_query", "query_metrics"}, ToolReadOnly: true, Risk: RiskLow, AssistantTaskKind: "data_query", AuthorizationDomain: "audit", DatasetID: "audit.events"}
	if decision, err := evaluator.Evaluate(ctx, input); err != nil || !decision.Allow {
		t.Fatalf("audit query denied: %+v %v", decision, err)
	}
	for _, change := range []func(*PolicyInput){func(v *PolicyInput) { v.DatasetID = "resources.current" }, func(v *PolicyInput) { v.ToolName = "query_metrics" }, func(v *PolicyInput) { v.AuthorizationDomain = "operational" }, func(v *PolicyInput) { v.AssistantTaskKind = "resource_troubleshooting" }, func(v *PolicyInput) { v.AssistantTaskKind = "" }, func(v *PolicyInput) { v.Request.Roles = []auth.Role{auth.Operator} }} {
		v := input
		change(&v)
		assertDenied(t, evaluator, ctx, v)
	}
}
