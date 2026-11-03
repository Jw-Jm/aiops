package source

import "testing"

// A Kubernetes source used to accept a nativeTenant declaration at
// registration and then fail every collection with source_scope_unverified.
// The write boundary now applies the same rule as resourcestore.FilterSnapshot
// and the SP-04 collector.
func TestKubernetesSourceScopeMatchesRuntimeCollectionModel(t *testing.T) {
	const clusterUID = "79014400-9453-4cb4-9678-0fae47981d79"
	supported := DataScopeMapping{Scopes: map[string][]string{"cluster": {clusterUID}, "namespace": {"ops-a"}}}
	if err := validateTypeScope("kubernetes", "backend-a", supported, clusterUID); err != nil {
		t.Fatalf("supported Kubernetes scope rejected: %v", err)
	}
	for name, tc := range map[string]struct {
		mapping   DataScopeMapping
		clusterID string
	}{
		"native tenant":          {DataScopeMapping{NativeTenant: "account-a", Scopes: map[string][]string{"cluster": {clusterUID}, "namespace": {"ops-a"}}}, clusterUID},
		"no namespace":           {DataScopeMapping{Scopes: map[string][]string{"cluster": {clusterUID}}}, clusterUID},
		"foreign cluster":        {DataScopeMapping{Scopes: map[string][]string{"cluster": {"other"}, "namespace": {"ops-a"}}}, clusterUID},
		"two clusters":           {DataScopeMapping{Scopes: map[string][]string{"cluster": {clusterUID, "other"}, "namespace": {"ops-a"}}}, clusterUID},
		"no cluster identity":    {supported, ""},
		"account dimension":      {DataScopeMapping{Scopes: map[string][]string{"cluster": {clusterUID}, "namespace": {"ops-a"}, "account": {"a"}}}, clusterUID},
		"undeclared data scope":  {DataScopeMapping{}, clusterUID},
		"required label allowed": {DataScopeMapping{Scopes: map[string][]string{"cluster": {clusterUID}, "namespace": {"ops-a"}}, RequiredLabels: map[string]string{"app": "web"}}, clusterUID},
	} {
		t.Run(name, func(t *testing.T) {
			err := validateTypeScope("kubernetes", "backend-a", tc.mapping, tc.clusterID)
			if name == "required label allowed" {
				if err != nil {
					t.Fatalf("object label filter rejected: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("incompatible Kubernetes data scope accepted")
			}
		})
	}
	if err := validateTypeScope("victoriametrics", "backend-a", DataScopeMapping{NativeTenant: "account-a", Scopes: map[string][]string{"cluster": {"c"}, "namespace": {"a"}}}, ""); err != nil {
		t.Fatalf("other source types lost their declaration model: %v", err)
	}
	if err := validateTypeScope("kubernetes", "", supported, clusterUID); err == nil {
		t.Fatal("scope without a backend identity accepted")
	}
	registration := SourceRegistration{SourceType: "kubernetes", BackendLogicalID: "backend-a", ClusterUID: clusterUID, DataScopeMapping: DataScopeMapping{Scopes: map[string][]string{"cluster": {clusterUID}, "namespace": {"ops-a"}}}}
	if !registration.ScopeSupported() {
		t.Fatal("compatible registration presented as incompatible")
	}
	registration.DataScopeMapping.NativeTenant = "account-a"
	if registration.ScopeSupported() {
		t.Fatal("incompatible registration presented as compatible")
	}
}

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
