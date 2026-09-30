package configregistry

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

func TestKindSpecificSchemasRejectOtherRegistryPayloads(t *testing.T) {
	valid := map[Kind]json.RawMessage{
		KindPolicy: json.RawMessage(`{"schemaVersion":"policy-registry/v1","name":"safe-default","modules":[{"id":"baseline","content":"package ops.policy\ndefault allow := false"}]}`),
		KindRecipe: json.RawMessage(`{"schemaVersion":"recipe-registry/v1","name":"health-check","steps":[{"stepId":"collect","toolName":"query_metrics","input":{}}]}`),
		KindTool:   json.RawMessage(`{"schemaVersion":"tool-registry/v1","name":"query_metrics","readOnly":true,"inputSchema":{"type":"object"},"outputSchema":{"type":"object"}}`),
	}
	for kind, content := range valid {
		if err := ValidateContent(kind, content); err != nil {
			t.Errorf("valid %s content rejected: %v", kind, err)
		}
	}
	for _, test := range []struct {
		kind    Kind
		content json.RawMessage
	}{
		{KindPolicy, valid[KindRecipe]},
		{KindRecipe, valid[KindTool]},
		{KindTool, valid[KindPolicy]},
		{KindPolicy, json.RawMessage(`{"schemaVersion":"policy-registry/v1","name":"unsafe"}`)},
	} {
		if err := ValidateContent(test.kind, test.content); err == nil {
			t.Errorf("%s accepted content for another/incomplete registry schema", test.kind)
		}
	}
}

func TestSignedPublicationBindsTenantKindNameAndContent(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tenantID := uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987330")
	content := json.RawMessage(`{"schemaVersion":"recipe-registry/v1","name":"health-check","steps":[{"stepId":"collect","toolName":"query_metrics","input":{}}]}`)
	message, digest, err := SigningPayload(tenantID, KindRecipe, "health-check", content)
	if err != nil {
		t.Fatalf("construct signing payload: %v", err)
	}
	if len(digest) != len("sha256:")+64 {
		t.Fatalf("content digest is not a sha256 identifier: %q", digest)
	}
	trust := Ed25519TrustStore{Keys: map[string]ed25519.PublicKey{"config-test-key": publicKey}}
	if err := trust.Verify(context.Background(), "config-test-key", message, ed25519.Sign(privateKey, message)); err != nil {
		t.Fatalf("valid publication signature rejected: %v", err)
	}
	for name, changed := range map[string]func() ([]byte, error){
		"tenant": func() ([]byte, error) {
			value, _, err := SigningPayload(uuid.New(), KindRecipe, "health-check", content)
			return value, err
		},
		"kind": func() ([]byte, error) {
			value, _, err := SigningPayload(tenantID, KindTool, "health-check", content)
			return value, err
		},
		"name": func() ([]byte, error) {
			value, _, err := SigningPayload(tenantID, KindRecipe, "other-recipe", content)
			return value, err
		},
		"content": func() ([]byte, error) {
			value, _, err := SigningPayload(tenantID, KindRecipe, "health-check", json.RawMessage(`{"schemaVersion":"recipe-registry/v1","name":"health-check","steps":[]}`))
			return value, err
		},
	} {
		t.Run(name, func(t *testing.T) {
			payload, err := changed()
			if err != nil {
				t.Fatal(err)
			}
			if err := trust.Verify(context.Background(), "config-test-key", payload, ed25519.Sign(privateKey, message)); err == nil {
				t.Fatal("signature was reusable for changed publication identity or content")
			}
		})
	}
	if err := trust.Verify(context.Background(), "unknown-key", message, ed25519.Sign(privateKey, message)); err == nil {
		t.Fatal("unknown signer key was accepted")
	}
}

func TestRegistryScopeValidationDetectsInvalidAndConflictingScopeShapes(t *testing.T) {
	clusterID := uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987332")
	valid := []Scope{
		{Type: ScopeTenant},
		{Type: ScopeCluster, ClusterID: clusterID},
		{Type: ScopeNamespace, ClusterID: clusterID, Namespace: "system"},
	}
	for _, scope := range valid {
		if err := scope.Validate(); err != nil {
			t.Errorf("valid scope rejected: %#v: %v", scope, err)
		}
	}
	invalid := []Scope{
		{Type: ScopeTenant, ClusterID: clusterID},
		{Type: ScopeCluster},
		{Type: ScopeNamespace, ClusterID: clusterID},
		{Type: ScopeNamespace, ClusterID: clusterID, Namespace: "../system"},
		{Type: "virtualization", ClusterID: clusterID},
	}
	for _, scope := range invalid {
		if err := scope.Validate(); err == nil {
			t.Errorf("invalid or ambiguous scope was accepted: %#v", scope)
		}
	}
}
