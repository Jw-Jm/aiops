package httpapi

import (
	"encoding/json"
	"testing"

	"ops-platform/internal/source"
)

func TestSourceScopeCannotClaimVerificationOrNullBindings(t *testing.T) {
	for _, raw := range []string{`null`, `{"verified":true}`, `{"verification":"verified"}`, `{"nativeTenant":null}`, `{"nativeTenant":""}`, `{"scopes":null}`, `{"scopes":{"namespace":null}}`, `{"requiredLabels":null}`} {
		var mapping source.DataScopeMapping
		if decodeScopeMapping(json.RawMessage(raw), &mapping) == nil {
			t.Fatalf("accepted unverified binding: %s", raw)
		}
	}
	response := sourceRegistrationJSON(source.SourceRegistration{Status: "active", BackendLogicalID: "backend-a", DataScopeMapping: source.DataScopeMapping{NativeTenant: "account-a"}})
	capability := response["queryCapability"].(map[string]string)
	if capability["state"] != "disabled" || capability["verification"] != "unverified" {
		t.Fatal("registration declaration granted query permission")
	}
}

// An existing row whose declaration cannot be collected is presented as an
// incompatibility that the update path can repair, not as a pending check.
func TestSourceRegistrationPresentsIncompatibleStoredScope(t *testing.T) {
	const clusterUID = "79014400-9453-4cb4-9678-0fae47981d79"
	incompatible := source.SourceRegistration{SourceType: "kubernetes", Status: "active", BackendLogicalID: "backend-a", ClusterUID: clusterUID, DataScopeMapping: source.DataScopeMapping{NativeTenant: "account-a", Scopes: map[string][]string{"cluster": {clusterUID}, "namespace": {"ops-a"}}}}
	capability := sourceRegistrationJSON(incompatible)["queryCapability"].(map[string]string)
	if capability["reason"] != "incompatible_data_scope_mapping" {
		t.Fatalf("incompatible stored scope presented as %q", capability["reason"])
	}
	supported := incompatible
	supported.DataScopeMapping = source.DataScopeMapping{Scopes: map[string][]string{"cluster": {clusterUID}, "namespace": {"ops-a"}}}
	capability = sourceRegistrationJSON(supported)["queryCapability"].(map[string]string)
	if capability["reason"] != "adapter_scope_verification_pending" {
		t.Fatalf("supported stored scope presented as %q", capability["reason"])
	}
}
