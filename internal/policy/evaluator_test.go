package policy

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/auth"
	"ops-platform/internal/configregistry"
	"ops-platform/policies"
)

func TestEvaluatorAllowsScopedReadOnlyToolsAndReturnsDeterministicDecision(t *testing.T) {
	ctx := context.Background()
	tenantID, clusterID := uuid.New(), uuid.New()
	compiler, version, _, err := testSignedPolicy(ctx, t, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	resolver := &staticPolicyResolver{version: version}
	evaluator, err := NewEvaluator(resolver, compiler, "baseline")
	if err != nil {
		t.Fatal(err)
	}
	input := operatorPolicyInput(tenantID, clusterID)
	input.RequestType = RequestTool
	input.ToolName = "query_metrics"
	input.AllowedTools = []string{"query_metrics"}
	input.ToolReadOnly = true
	first, err := evaluator.Evaluate(ctx, input)
	if err != nil || !first.Allow || first.PolicyVersion == "" || first.DecisionID == "" || first.Risk != RiskLow {
		t.Fatalf("scoped read-only tool decision = %#v, %v", first, err)
	}
	second, err := evaluator.Evaluate(ctx, input)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("same bundle and input did not produce a deterministic decision: first=%#v second=%#v err=%v", first, second, err)
	}
}

func TestEvaluatorFailsClosedAcrossTenantAndScopeBoundaries(t *testing.T) {
	ctx := context.Background()
	tenantID, clusterID := uuid.New(), uuid.New()
	compiler, version, _, err := testSignedPolicy(ctx, t, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	resolver := &staticPolicyResolver{version: version}
	evaluator, err := NewEvaluator(resolver, compiler, "baseline")
	if err != nil {
		t.Fatal(err)
	}
	input := operatorPolicyInput(tenantID, clusterID)
	input.RequestType, input.ToolName, input.ToolReadOnly = RequestTool, "query_metrics", true
	input.AllowedTools = []string{"query_metrics"}

	crossTenant := input
	crossTenant.Request = auth.RequestContext{TenantID: uuid.New(), Subject: "operator", Roles: []auth.Role{auth.Operator}}
	assertDenied(t, evaluator, ctx, crossTenant)

	crossScope := input
	crossScope.Scope.ClusterID = uuid.New()
	assertDenied(t, evaluator, ctx, crossScope)

	tenantWide := input
	tenantWide.Scope = configregistry.Scope{Type: configregistry.ScopeTenant}
	assertDenied(t, evaluator, ctx, tenantWide)

	roleMismatch := input
	roleMismatch.Request.Roles = []auth.Role{auth.PlatformAdmin}
	assertDenied(t, evaluator, ctx, roleMismatch)

	resolver.err = configregistry.ErrNotFound
	assertDenied(t, evaluator, ctx, input)
}

func TestEvaluatorAllowsOnlyVerifiedAgentReadOnlyToolInvocation(t *testing.T) {
	ctx := context.Background()
	tenantID, clusterID := uuid.New(), uuid.New()
	compiler, version, _, err := testSignedPolicy(ctx, t, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := NewEvaluator(&staticPolicyResolver{version: version}, compiler, "baseline")
	if err != nil {
		t.Fatal(err)
	}
	scope := configregistry.Scope{Type: configregistry.ScopeCluster, ClusterID: clusterID}
	input := PolicyInput{
		Request:       auth.RequestContext{TenantID: tenantID, Subject: "agent"},
		PrincipalType: PrincipalAgent, RequestType: RequestTool, Scope: scope,
		Invocation: &InvocationContext{Verified: true, TenantID: tenantID, Scope: scope, AllowedTools: []string{"query_metrics"}},
		ToolName:   "query_metrics", ToolReadOnly: true,
	}
	if decision, err := evaluator.Evaluate(ctx, input); err != nil || !decision.Allow {
		t.Fatalf("verified, scoped read-only agent tool was rejected: decision=%#v err=%v", decision, err)
	}
	input.Invocation.Verified = false
	assertDenied(t, evaluator, ctx, input)
}

func TestEvaluatorRejectsAgentWritesRootWithoutStepUpAndCommandDigestMismatch(t *testing.T) {
	ctx := context.Background()
	tenantID, clusterID := uuid.New(), uuid.New()
	compiler, version, _, err := testSignedPolicy(ctx, t, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	resolver := &staticPolicyResolver{version: version}
	evaluator, err := NewEvaluator(resolver, compiler, "baseline")
	if err != nil {
		t.Fatal(err)
	}

	agentWrite := PolicyInput{
		Request:       auth.RequestContext{TenantID: tenantID, Subject: "agent", Roles: nil},
		PrincipalType: PrincipalAgent, RequestType: RequestAction,
		Scope:      configregistry.Scope{Type: configregistry.ScopeCluster, ClusterID: clusterID},
		Invocation: &InvocationContext{Verified: true, TenantID: tenantID, Scope: configregistry.Scope{Type: configregistry.ScopeCluster, ClusterID: clusterID}},
	}
	assertDenied(t, evaluator, ctx, agentWrite)

	rootAction := operatorPolicyInput(tenantID, clusterID)
	rootAction.RequestType = RequestAction
	rootAction.RootLevel = true
	rootAction.RiskAcknowledged = true
	rootAction.ActualCommandDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	rootAction.ConfirmedCommandDigest = rootAction.ActualCommandDigest
	decision, err := evaluator.Evaluate(ctx, rootAction)
	if err != nil || decision.Allow || !decision.RequiresStepUp {
		t.Fatalf("root action without step-up was not denied with a step-up requirement: decision=%#v err=%v", decision, err)
	}
	rootAction.StepUpVerified = true
	if decision, err := evaluator.Evaluate(ctx, rootAction); err != nil || !decision.Allow || !decision.RequiresStepUp {
		t.Fatalf("root action with verified step-up was rejected: decision=%#v err=%v", decision, err)
	}
	clusterAction := rootAction
	clusterAction.RootLevel = false
	clusterAction.ClusterLevel = true
	clusterAction.StepUpVerified = false
	if decision, err := evaluator.Evaluate(ctx, clusterAction); err != nil || decision.Allow || !decision.RequiresStepUp {
		t.Fatalf("cluster action without step-up was not denied with a step-up requirement: decision=%#v err=%v", decision, err)
	}

	mismatched := rootAction
	mismatched.ConfirmedCommandDigest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	assertDenied(t, evaluator, ctx, mismatched)
}

func TestDecisionIdentityChangesWhenRiskAcknowledgementChanges(t *testing.T) {
	ctx := context.Background()
	tenantID, clusterID := uuid.New(), uuid.New()
	compiler, version, _, err := testSignedPolicy(ctx, t, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := NewEvaluator(&staticPolicyResolver{version: version}, compiler, "baseline")
	if err != nil {
		t.Fatal(err)
	}
	input := operatorPolicyInput(tenantID, clusterID)
	input.RequestType = RequestAction
	input.Risk = RiskMedium
	input.ActualCommandDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	input.ConfirmedCommandDigest = input.ActualCommandDigest
	denied, err := evaluator.Evaluate(ctx, input)
	if err == nil || denied.Allow {
		t.Fatalf("unacknowledged medium-risk action was not denied: decision=%#v err=%v", denied, err)
	}
	input.RiskAcknowledged = true
	allowed, err := evaluator.Evaluate(ctx, input)
	if err != nil || !allowed.Allow {
		t.Fatalf("acknowledged medium-risk action was denied: decision=%#v err=%v", allowed, err)
	}
	if denied.DecisionID == allowed.DecisionID {
		t.Fatalf("materially different decisions shared id %q", denied.DecisionID)
	}
}

func TestEvaluatorRejectsPlatformAdminWithoutExplicitOperatorExecutionRole(t *testing.T) {
	ctx := context.Background()
	tenantID, clusterID := uuid.New(), uuid.New()
	compiler, version, _, err := testSignedPolicy(ctx, t, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := NewEvaluator(&staticPolicyResolver{version: version}, compiler, "baseline")
	if err != nil {
		t.Fatal(err)
	}
	input := operatorPolicyInput(tenantID, clusterID)
	input.RequestType = RequestAction
	input.PrincipalType = PrincipalPlatformAdmin
	input.Request.Roles = []auth.Role{auth.PlatformAdmin}
	input.ActualCommandDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	input.ConfirmedCommandDigest = input.ActualCommandDigest
	decision, err := evaluator.Evaluate(ctx, input)
	if err == nil || decision.Allow {
		t.Fatalf("platform_admin inherited operator execution permission: decision=%#v err=%v", decision, err)
	}
}

func TestEvaluatorRejectsBadSignatureAndCompileFailureKeepsPreviousCompiledBundle(t *testing.T) {
	ctx := context.Background()
	tenantID, clusterID := uuid.New(), uuid.New()
	compiler, version, _, err := testSignedPolicy(ctx, t, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	resolver := &staticPolicyResolver{version: version}
	evaluator, err := NewEvaluator(resolver, compiler, "baseline")
	if err != nil {
		t.Fatal(err)
	}
	input := operatorPolicyInput(tenantID, clusterID)
	input.RequestType, input.ToolName, input.ToolReadOnly, input.AllowedTools = RequestTool, "query_metrics", true, []string{"query_metrics"}
	if decision, err := evaluator.Evaluate(ctx, input); err != nil || !decision.Allow {
		t.Fatalf("valid signed policy rejected: decision=%#v err=%v", decision, err)
	}

	corrupt := version
	corrupt.Signature = append([]byte(nil), version.Signature...)
	corrupt.Signature[0] ^= 0xff
	resolver.version = corrupt
	assertDenied(t, evaluator, ctx, input)

	resolver.version = version
	invalidContent := json.RawMessage(`{"schemaVersion":"policy-registry/v1","name":"baseline","modules":[{"id":"bad.rego","content":"package ops.policy\nthis is not valid rego"}]}`)
	if err := compiler.ValidatePolicyPublication(ctx, invalidContent); err == nil {
		t.Fatal("invalid Rego bundle compiled successfully")
	}
	if decision, err := evaluator.Evaluate(ctx, input); err != nil || !decision.Allow {
		t.Fatalf("failed compilation replaced a previous valid policy: decision=%#v err=%v", decision, err)
	}
}

func TestUntrustedSignatureKeyIsRejected(t *testing.T) {
	ctx := context.Background()
	tenantID := uuid.New()
	_, version, _, err := testSignedPolicy(ctx, t, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	compiler, err := NewBundleCompiler(configregistry.Ed25519TrustStore{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := compiler.Prepare(ctx, version); !errors.Is(err, ErrPolicyBundleSignature) {
		t.Fatalf("bundle signed by an untrusted key returned %v", err)
	}
}

func TestCompilerRequiresBothDecisionEntrypointsAndDefaultDeny(t *testing.T) {
	compiler, err := NewBundleCompiler(configregistry.Ed25519TrustStore{})
	if err != nil {
		t.Fatal(err)
	}
	for name, module := range map[string]string{
		"missing entrypoints": "package ops.policy\ndefault allow := false",
		"default allow": `package ops.policy
tool_decision := {"allow": true, "risk": "low", "reasons": [], "requiresStepUp": false}
action_decision := {"allow": true, "risk": "low", "reasons": [], "requiresStepUp": false}`,
	} {
		content, err := json.Marshal(map[string]any{
			"schemaVersion": "policy-registry/v1", "name": "unsafe",
			"modules": []map[string]string{{"id": "baseline.rego", "content": module}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := compiler.ValidatePolicyPublication(context.Background(), content); !errors.Is(err, ErrPolicyBundleInvalid) {
			t.Errorf("%s policy bundle validation returned %v", name, err)
		}
	}
}

func operatorPolicyInput(tenantID, clusterID uuid.UUID) PolicyInput {
	return PolicyInput{
		Request:       auth.RequestContext{TenantID: tenantID, Subject: "operator", Roles: []auth.Role{auth.Operator}, ClusterScopes: []uuid.UUID{clusterID}},
		PrincipalType: PrincipalOperator,
		Scope:         configregistry.Scope{Type: configregistry.ScopeCluster, ClusterID: clusterID},
	}
}

func assertDenied(t *testing.T, evaluator *PolicyEvaluator, ctx context.Context, input PolicyInput) {
	t.Helper()
	decision, err := evaluator.Evaluate(ctx, input)
	if err == nil || decision.Allow {
		t.Fatalf("unsafe or out-of-scope policy request was not fail-closed: decision=%#v err=%v", decision, err)
	}
}

type staticPolicyResolver struct {
	version configregistry.PublishedVersion
	err     error
}

func (resolver *staticPolicyResolver) ResolveActive(_ context.Context, _ uuid.UUID, _ configregistry.Kind, _ string, _ configregistry.Scope, _ time.Time) (configregistry.PublishedVersion, error) {
	if resolver.err != nil {
		return configregistry.PublishedVersion{}, resolver.err
	}
	return resolver.version, nil
}

func testSignedPolicy(ctx context.Context, t *testing.T, tenantID uuid.UUID) (*BundleCompiler, configregistry.PublishedVersion, ed25519.PrivateKey, error) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, configregistry.PublishedVersion{}, nil, err
	}
	toolModule, err := policies.FS.ReadFile("tool/v1/tool.rego")
	if err != nil {
		return nil, configregistry.PublishedVersion{}, nil, err
	}
	actionModule, err := policies.FS.ReadFile("action/v1/action.rego")
	if err != nil {
		return nil, configregistry.PublishedVersion{}, nil, err
	}
	content, err := json.Marshal(map[string]any{
		"schemaVersion": "policy-registry/v1", "name": "baseline",
		"modules": []map[string]string{{"id": "tool/v1/tool.rego", "content": string(toolModule)}, {"id": "action/v1/action.rego", "content": string(actionModule)}},
	})
	if err != nil {
		return nil, configregistry.PublishedVersion{}, nil, err
	}
	message, digest, err := configregistry.SigningPayload(tenantID, configregistry.KindPolicy, "baseline", content)
	if err != nil {
		return nil, configregistry.PublishedVersion{}, nil, err
	}
	version := configregistry.PublishedVersion{
		TenantID: tenantID, VersionID: uuid.New(), Kind: configregistry.KindPolicy, LogicalName: "baseline",
		VersionNumber: 1, Content: content, Digest: digest, Signature: ed25519.Sign(privateKey, message), SignerKeyID: "unit-key",
		PublishedAt: time.Now().UTC(),
	}
	compiler, err := NewBundleCompiler(configregistry.Ed25519TrustStore{Keys: map[string]ed25519.PublicKey{"unit-key": publicKey}})
	if err != nil {
		return nil, configregistry.PublishedVersion{}, nil, err
	}
	if err := compiler.ValidatePolicyPublication(ctx, content); err != nil {
		return nil, configregistry.PublishedVersion{}, nil, err
	}
	return compiler, version, privateKey, nil
}
