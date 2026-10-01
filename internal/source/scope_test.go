package source

import "testing"

func TestScopeBindingRejectsUnboundedAndAmbiguousDeclarations(t *testing.T) {
	for name, mapping := range map[string]DataScopeMapping{
		"unknown dimension": {Scopes: map[string][]string{"region": {"a"}}},
		"wildcard":          {Scopes: map[string][]string{"namespace": {"*"}}},
		"regex":             {Scopes: map[string][]string{"namespace": {"prod|dev"}}},
		"duplicate":         {Scopes: map[string][]string{"namespace": {"prod", "prod"}}},
		"empty literal":     {Scopes: map[string][]string{"namespace": {""}}},
		"control":           {NativeTenant: "tenant\x01"},
		"label selector":    {RequiredLabels: map[string]string{"tenant": "a|b"}},
	} {
		t.Run(name, func(t *testing.T) {
			if validScopeBinding("backend-a", mapping) {
				t.Fatal("invalid scope declaration was accepted")
			}
		})
	}
	mapping := DataScopeMapping{NativeTenant: "account-a", Scopes: map[string][]string{"namespace": {"prod"}}, RequiredLabels: map[string]string{"tenant_id": "a"}}
	if validScopeBinding("", mapping) || !validScopeBinding("backend-a", mapping) {
		t.Fatal("scope did not require a backend identity")
	}
	if !validScopeBinding("", DataScopeMapping{}) || !validScopeBinding("backend-a", DataScopeMapping{Scopes: map[string][]string{"namespace": {}}}) {
		t.Fatal("empty scopes must remain representable without granting query access")
	}
}
