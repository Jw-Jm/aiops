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
