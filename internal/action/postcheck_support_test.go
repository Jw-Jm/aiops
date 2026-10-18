package action

import (
	"ops-platform/internal/resource"
	"testing"
)

func TestPostCheckApplicabilityNeverClaimsQualifiedOrSupportsForeignKinds(t *testing.T) {
	for _, c := range []struct{ domain, kind, recipe string }{{"k8s", "Node", "node-failure"}, {"k8s", "PersistentVolumeClaim", "pvc-csi-failure"}, {"k8s", "Pod", ""}, {"k8s", "VirtualMachine", ""}, {"hardware", "PhysicalServer", ""}} {
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
