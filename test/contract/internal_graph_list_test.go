package contract_test

import (
	"encoding/json"
	"ops-platform/internal/contract"
	"testing"
)

func TestInternalListHasNoAnchorButPreservesCanonicalTenantChecks(t *testing.T) {
	q := map[string]any{"queryKind": "list", "canonicalId": "", "expectedOwnerEpoch": 1, "maxDepth": 1, "maxNodes": 1, "maxEdges": 1, "relationKinds": []string{}, "scope": map[string]any{"tenantId": "tenant-a", "clusterUid": "cluster-a", "namespaces": []string{"apps"}, "resources": []string{}, "clusterScoped": false, "authorizationRevision": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}
	for _, test := range []struct {
		name, kind, id string
		valid          bool
	}{
		{"unanchored list", "list", "", true},
		{"empty entity", "entity", "", false},
		{"foreign list anchor", "list", "k8s+v1://tenant-b/cluster-a/core/Pod/pod-b", false},
		{"foreign entity", "entity", "k8s+v1://tenant-b/cluster-a/core/Pod/pod-b", false},
		{"invalid list anchor", "list", "invalid", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			q["queryKind"] = test.kind
			q["canonicalId"] = test.id
			raw, _ := json.Marshal(q)
			err := contract.Validate("https://ops.local/schemas/internal-graph-query/v1", raw)
			if (err == nil) != test.valid {
				t.Fatalf("valid=%t error=%v", test.valid, err)
			}
		})
	}
}
