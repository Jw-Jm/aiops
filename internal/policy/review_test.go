package policy

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"ops-platform/internal/configregistry"
)

func TestPolicyPublicationRejectsExternalAndNondeterministicBuiltins(t *testing.T) {
	compiler, _ := NewBundleCompiler(configregistry.Ed25519TrustStore{})
	for _, expression := range []string{`time.now_ns() > 0`, `uuid.rfc4122("x") != ""`, `http.send({"method":"GET","url":"https://example.invalid"}).status_code == 200`} {
		module := `package ops.policy
default tool_decision := {"allow":false,"risk":"high","reasons":[],"requiresStepUp":false}
default action_decision := {"allow":false,"risk":"high","reasons":[],"requiresStepUp":false}
tool_decision := {"allow":true,"risk":"low","reasons":[],"requiresStepUp":false} if { input.toolAllowed; ` + expression + ` }`
		content, _ := json.Marshal(map[string]any{"schemaVersion": "policy-registry/v1", "name": "unsafe", "modules": []map[string]string{{"id": "unsafe.rego", "content": module}}})
		if err := compiler.ValidatePolicyPublication(context.Background(), content); err == nil {
			t.Fatalf("publication accepted forbidden builtin: %s", expression)
		}
	}
}

func TestPolicyRaisedRiskRequiresAcknowledgement(t *testing.T) {
	ctx := context.Background()
	tenant, cluster := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	compiler, version, key, err := testSignedPolicy(ctx, t, tenant)
	if err != nil {
		t.Fatal(err)
	}
	var content map[string]any
	if err := json.Unmarshal(version.Content, &content); err != nil {
		t.Fatal(err)
	}
	for _, module := range content["modules"].([]any) {
		m := module.(map[string]any)
		m["content"] = strings.ReplaceAll(m["content"].(string), `"risk": input.risk`, `"risk": "high"`)
	}
	version.Content, _ = json.Marshal(content)
	message, digest, _ := configregistry.SigningPayload(tenant, version.Kind, version.LogicalName, version.Content)
	version.Digest, version.Signature = digest, ed25519.Sign(key, message)
	evaluator, _ := NewEvaluator(&staticPolicyResolver{version: version}, compiler, "baseline")
	input := operatorPolicyInput(tenant, cluster)
	input.RequestType, input.Risk = RequestAction, RiskLow
	input.ActualCommandDigest = "sha256:" + strings.Repeat("a", 64)
	input.ConfirmedCommandDigest = input.ActualCommandDigest
	assertDenied(t, evaluator, ctx, input)
}

func TestPolicyDecisionMustContainAllRequiredFields(t *testing.T) {
	compiler, _ := NewBundleCompiler(configregistry.Ed25519TrustStore{})
	for _, result := range []string{`{"risk":"high"}`, `{"allow":false,"risk":"high","reasons":[]}`, `{"allow":false,"risk":"high","reasons":null,"requiresStepUp":false}`} {
		module := `package ops.policy
default tool_decision := ` + result + `
default action_decision := ` + result
		content, _ := json.Marshal(map[string]any{"schemaVersion": "policy-registry/v1", "name": "incomplete", "modules": []map[string]string{{"id": "missing.rego", "content": module}}})
		if err := compiler.ValidatePolicyPublication(context.Background(), content); err == nil {
			t.Errorf("accepted incomplete decision %s", result)
		}
	}
}
