package graph

import "testing"

func TestScopeAllowsClusterScopedCoversNamespacedResources(t *testing.T) {
	id := "k8s+v1://tenant-a/cluster-a/core/Pod/pod-a"
	cluster := Scope{Tenant: "tenant-a", Cluster: "cluster-a", ClusterScoped: true, Namespaces: []string{}, Resources: []string{}}
	if !cluster.Allows(id, "apps") || !cluster.Allows(id, "") {
		t.Fatal("cluster-scoped grant must cover every namespace in the cluster")
	}
	if cluster.Allows("k8s+v1://tenant-a/cluster-b/core/Pod/pod-a", "apps") {
		t.Fatal("cluster-scoped grant crossed into another cluster")
	}
	pinned := Scope{Tenant: "tenant-a", Cluster: "cluster-a", ClusterScoped: true, Namespaces: []string{}, Resources: []string{id}}
	if pinned.Allows("k8s+v1://tenant-a/cluster-a/core/Pod/pod-b", "apps") {
		t.Fatal("resource allowlist bypassed by cluster scope")
	}
	namespaced := Scope{Tenant: "tenant-a", Cluster: "cluster-a", ClusterScoped: false, Namespaces: []string{"apps"}, Resources: []string{}}
	if !namespaced.Allows(id, "apps") || namespaced.Allows(id, "other") || namespaced.Allows(id, "") {
		t.Fatal("namespace-scoped grant semantics changed")
	}
}
