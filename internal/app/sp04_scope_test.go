package app

import (
	"ops-platform/internal/datascope"
	"ops-platform/internal/evidence"
	"testing"
)

func TestCollectorScopeMustEqualRegisteredKubernetesScope(t *testing.T) {
	const (
		tenant    = "01a177f1-372f-75b7-be90-e8121d1445f5"
		sourceID  = "01a17c3c-ef66-70cf-9ff1-cad25a8bee34"
		clusterID = "79014400-9453-4cb4-9678-0fae47981d79"
		namespace = "ops-native-v12-r24-20261026"
	)
	cluster := SP04Cluster{
		Tenant: tenant, ClusterUID: clusterID, SourceID: sourceID, BackendLogicalID: "backend-a",
		SourceRevision: 2, Namespaces: []string{namespace}, LeaseNamespace: namespace,
	}
	binding := evidence.Binding{
		Tenant: tenant, SourceID: sourceID, Revision: 2, SourceType: "kubernetes", BackendLogicalID: "backend-a",
		ScopeMapping: datascope.Mapping{Scopes: map[string][]string{"cluster": {clusterID}, "namespace": {namespace}}},
	}
	if err := validateCollectorScope(cluster, binding); err != nil {
		t.Fatalf("matched collection scope rejected: %v", err)
	}
	for name, mutate := range map[string]func(*SP04Cluster, *evidence.Binding){
		"missing namespace": func(c *SP04Cluster, _ *evidence.Binding) { c.Namespaces = nil },
		"extra namespace": func(c *SP04Cluster, _ *evidence.Binding) {
			c.Namespaces = []string{namespace, "other"}
		},
		"partial scope": func(c *SP04Cluster, _ *evidence.Binding) { c.Namespaces = []string{} },
		"duplicate": func(c *SP04Cluster, _ *evidence.Binding) {
			c.Namespaces = []string{namespace, namespace}
		},
		"invalid name":   func(c *SP04Cluster, _ *evidence.Binding) { c.Namespaces = []string{"Invalid_NS"} },
		"native tenant":  func(_ *SP04Cluster, b *evidence.Binding) { b.ScopeMapping.NativeTenant = "account-a" },
		"revision drift": func(c *SP04Cluster, _ *evidence.Binding) { c.SourceRevision = 3 },
		"backend drift":  func(c *SP04Cluster, _ *evidence.Binding) { c.BackendLogicalID = "backend-b" },
		"lease outside scope": func(c *SP04Cluster, _ *evidence.Binding) {
			c.LeaseNamespace = "kube-system"
		},
		"registered scope widened": func(_ *SP04Cluster, b *evidence.Binding) {
			b.ScopeMapping.Scopes["namespace"] = []string{namespace, "other"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			changedCluster, changedBinding := cluster, binding
			changedCluster.Namespaces = append([]string(nil), cluster.Namespaces...)
			changedBinding.ScopeMapping = datascope.Mapping{NativeTenant: binding.ScopeMapping.NativeTenant, Scopes: map[string][]string{}}
			for key, values := range binding.ScopeMapping.Scopes {
				changedBinding.ScopeMapping.Scopes[key] = append([]string(nil), values...)
			}
			mutate(&changedCluster, &changedBinding)
			if err := validateCollectorScope(changedCluster, changedBinding); err == nil {
				t.Fatal("collection scope drift accepted")
			}
		})
	}
}
