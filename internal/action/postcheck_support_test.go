package action

import (
	"ops-platform/internal/resource"
	"testing"
)

func TestPostCheckApplicabilityNeverClaimsQualifiedOrSupportsForeignKinds(t *testing.T) {
	for _, c := range []struct{ domain, kind, recipe string }{{"k8s", "Node", "node-failure"}, {"k8s", "PersistentVolumeClaim", "pvc-csi-failure"}, {"k8s", "Pod", "pod-health"}, {"k8s", "VirtualMachine", ""}, {"hardware", "PhysicalServer", ""}} {
		id := resource.CanonicalID{Domain: c.domain, Tenant: "tenant", Scope: "cluster", APIGroup: "core", Kind: c.kind, StableID: "uid"}.String()
		support := PostCheckApplicability(id)
		if support.Recipe != c.recipe {
			t.Fatal(c, support)
		}
		if c.recipe == "" && support.Status != "unsupported" || c.recipe != "" && support.Status != "requires_qualified_source" {
			t.Fatal("declaration claimed verified support", support)
		}
	}
	if PostCheckApplicability("invalid").Status != "unsupported" {
		t.Fatal("malformed target supported")
	}
}

func TestNativePostCheckAPIGroupsRemainExplicit(t *testing.T) {
	for _, tc := range []struct{ group, kind, recipe string }{{"apps", "Deployment", "deployment-health"}, {"discovery.k8s.io", "EndpointSlice", "service-endpoints-health"}, {"core", "Service", "service-endpoints-health"}, {"apps", "Pod", ""}, {"kubevirt.io", "VirtualMachine", ""}} {
		id := resource.CanonicalID{Domain: "k8s", Tenant: "tenant", Scope: "cluster", APIGroup: tc.group, Kind: tc.kind, StableID: "uid"}.String()
		if got := PostCheckApplicability(id); got.Recipe != tc.recipe {
			t.Fatal(tc, got)
		}
	}
}
