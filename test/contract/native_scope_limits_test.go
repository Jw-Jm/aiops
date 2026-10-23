package contract_test

import (
	"encoding/json"
	"fmt"
	platformcontract "ops-platform/internal/contract"
	"testing"
)

func TestNativeScopeContractLimits(t *testing.T) {
	for _, tc := range []struct {
		clusters, namespaces int
		accepted             bool
	}{
		{1, 100, true}, {1, 200, true}, {1, 201, false}, {50, 0, true}, {51, 0, false},
	} {
		t.Run(fmt.Sprintf("clusters%d_namespaces%d", tc.clusters, tc.namespaces), func(t *testing.T) {
			clusters, namespaces := []string{}, []string{}
			for i := 0; i < tc.clusters; i++ {
				clusters = append(clusters, fmt.Sprintf("cluster-%d", i))
			}
			for i := 0; i < tc.namespaces; i++ {
				namespaces = append(namespaces, fmt.Sprintf("ns-%d", i))
			}
			raw, err := json.Marshal(map[string]any{"clusterUids": clusters, "namespaces": namespaces})
			if err != nil {
				t.Fatal(err)
			}
			err = platformcontract.Validate(uiV12IdentitySchema+"#/$defs/scope", raw)
			if (err == nil) != tc.accepted {
				t.Fatalf("scope accepted=%v expected=%v: %v", err == nil, tc.accepted, err)
			}
		})
	}
}
