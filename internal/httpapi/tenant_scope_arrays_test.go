package httpapi

import (
	"encoding/json"
	"github.com/google/uuid"
	"testing"
)

func TestRoleBindingEmptyScopesKeepDatabaseArrayContract(t *testing.T) {
	cluster := uuid.NewString()
	for _, body := range []map[string]any{
		{"subject": "current-admin", "role": "operator", "clusterScopes": []any{cluster}, "namespaceScopes": []any{}},
		{"subject": "current-admin", "role": "platform_admin"},
	} {
		input, err := parseRoleBindingInput(body)
		if err != nil {
			t.Fatal(err)
		}
		for _, scope := range []any{input.ClusterScopes, input.NamespaceScopes} {
			raw, err := json.Marshal(scope)
			if err != nil {
				t.Fatal(err)
			}
			if len(raw) == 0 || raw[0] != '[' {
				t.Fatalf("valid scope violates persisted jsonb array contract: %s", raw)
			}
		}
	}
	for _, name := range []string{"clusterScopes", "namespaceScopes"} {
		body := map[string]any{"subject": "current-admin", "role": "operator", name: nil}
		if _, err := parseRoleBindingInput(body); err == nil {
			t.Fatalf("explicit null %s accepted", name)
		}
	}
}
