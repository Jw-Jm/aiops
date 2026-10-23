package investigation

import (
	"testing"

	"ops-platform/internal/graph"
)

func TestScopeCoversMappedOwnerIgnoresOnlyIdentityRevision(t *testing.T) {
	frozen := graph.Scope{
		Tenant: "tenant-a", Cluster: "cluster-a", Namespaces: []string{"apps"},
		Resources: []string{"k8s:tenant-a:cluster-a:core:Pod:apps/web"}, AuthorizationRevision: "sha256:frozen",
	}
	current := frozen
	current.AuthorizationRevision = "sha256:native"
	if !ScopeCoversMappedOwner(current, frozen) {
		t.Fatal("equal scope with a different identity revision must still cover the frozen scope")
	}

	cases := []struct {
		name  string
		scope graph.Scope
		grant graph.Scope
	}{
		{"other tenant", frozen, graph.Scope{Tenant: "tenant-b", Cluster: "cluster-a", Namespaces: []string{"apps"}, Resources: frozen.Resources}},
		{"other cluster", frozen, graph.Scope{Tenant: "tenant-a", Cluster: "cluster-b", Namespaces: []string{"apps"}, Resources: frozen.Resources}},
		{"missing namespace", frozen, graph.Scope{Tenant: "tenant-a", Cluster: "cluster-a", Resources: frozen.Resources}},
		{"missing explicit resource", frozen, graph.Scope{Tenant: "tenant-a", Cluster: "cluster-a", Namespaces: []string{"apps"}, Resources: []string{"k8s:other"}}},
		{
			"namespace-limited grant for cluster-scoped history",
			graph.Scope{Tenant: "tenant-a", Cluster: "cluster-a", ClusterScoped: true},
			graph.Scope{Tenant: "tenant-a", Cluster: "cluster-a", Namespaces: []string{"apps"}},
		},
	}
	for _, test := range cases {
		if ScopeCoversMappedOwner(test.grant, test.scope) {
			t.Fatalf("%s: current grant covered a frozen scope it does not contain", test.name)
		}
	}
}
