package resource

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestCanonicalIdentityRoundTrip(t *testing.T) {
	id := CanonicalID{"k8s", "tenant-a", "cluster/01", "core", "Pod", "000Ab/C"}
	parsed, err := ParseCanonicalID(id.String())
	if err != nil || parsed != id {
		t.Fatalf("round trip: %+v %v", parsed, err)
	}
	for _, raw := range []string{"k8s:v1://tenant-a/c/core/Pod/id", "k8s+v1://TENANT-A/c/core/Pod/id", id.String() + "?token=secret", "k8s+v1://tenant-a/c/core/Pod/"} {
		if _, err := ParseCanonicalID(raw); err == nil {
			t.Fatalf("accepted noncanonical %q", raw)
		}
	}
}

func TestResolverDeterministicAndNoNameSerialMerge(t *testing.T) {
	r := NewResolver()
	ref := SourceRef{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: "Pod", UID: "001ABC", Name: "web", ObservedAt: time.Now()}
	a, err := r.Resolve(context.Background(), ref)
	if err != nil || a.Status != Matched || a.RuleVersion == "" || len(a.SourceValues) == 0 {
		t.Fatalf("%+v %v", a, err)
	}
	for _, f := range []func(*SourceRef){func(v *SourceRef) { v.Tenant = "tenant-b" }, func(v *SourceRef) { v.Scope = "cluster-b" }, func(v *SourceRef) { v.UID = "001abc" }, func(v *SourceRef) { v.UID = "1ABC" }, func(v *SourceRef) { v.UID = "replacement" }} {
		other := ref
		f(&other)
		b, _ := r.Resolve(context.Background(), other)
		if b.CanonicalID == a.CanonicalID {
			t.Fatal("different immutable identity merged")
		}
	}
	ref.UID = ""
	ref.Serial = "same-serial"
	if b, _ := r.Resolve(context.Background(), ref); b.Status != Unresolved || b.CanonicalID.String() != "" {
		t.Fatal("name/serial granted identity")
	}
}

func TestResolverConflictsAndHosts(t *testing.T) {
	r := NewResolver()
	now := time.Now()
	h := SourceRef{Domain: "hardware", Tenant: "tenant-a", Scope: "dc-a", APIGroup: "redfish", Kind: "PhysicalServer", UUID: "550E8400-E29B-41D4-A716-446655440000", ObservedAt: now}
	a, _ := r.Resolve(context.Background(), h)
	h.UUID = strings.ToLower(h.UUID)
	b, _ := r.Resolve(context.Background(), h)
	if a.CanonicalID != b.CanonicalID || a.Status != Matched {
		t.Fatal("UUID normalization failed")
	}
	n := SourceRef{Domain: "k8s", Tenant: h.Tenant, Scope: "cluster-a", APIGroup: "core", Kind: "Node", UID: "node-uid", UUID: h.UUID, ObservedAt: now, Candidates: []CanonicalID{a.CanonicalID}}
	node, _ := r.Resolve(context.Background(), n)
	if node.CanonicalID == a.CanonicalID || len(node.Relations) != 1 || node.Relations[0].Kind != "hosts" {
		t.Fatalf("server and node conflated: %+v", node)
	}
	n.Candidates = append(n.Candidates, CanonicalID{"hardware", h.Tenant, "dc-b", "redfish", "PhysicalServer", h.UUID})
	if got, _ := r.Resolve(context.Background(), n); got.Status != Conflicted || len(got.Relations) != 0 {
		t.Fatal("ambiguous host join chosen")
	}
	n.Candidates = []CanonicalID{{"hardware", "tenant-b", "dc-a", "redfish", "PhysicalServer", h.UUID}}
	if got, _ := r.Resolve(context.Background(), n); got.Status != Unresolved || len(got.Relations) != 0 {
		t.Fatal("cross-tenant join")
	}
	h.UUID = "00000000-0000-0000-0000-000000000000"
	if got, _ := r.Resolve(context.Background(), h); got.Status != Unresolved {
		t.Fatal("zero UUID matched")
	}
}

func TestDeterministicIdentityGolden10000(t *testing.T) {
	now := time.Now()
	resolver := NewResolver()
	matched := 0
	ids := map[string]bool{}
	for n := 0; n < 10000; n++ {
		ref := SourceRef{Domain: "k8s", Tenant: fmt.Sprintf("tenant-%d", n%10), Scope: fmt.Sprintf("cluster-%d", n%100), APIGroup: "core", Kind: "Pod", UID: fmt.Sprintf("UID-%06d", n), Serial: "DUPLICATE", Name: "same-name", ObservedAt: now}
		out, err := resolver.Resolve(context.Background(), ref)
		if err != nil || out.Status != Matched {
			t.Fatalf("golden %d: %+v %v", n, out, err)
		}
		if out.CanonicalID.StableID != ref.UID || ids[out.CanonicalID.String()] {
			t.Fatalf("false merge %d", n)
		}
		ids[out.CanonicalID.String()] = true
		matched++
		// Deletion/recreation retains the name/serial but must change the identity.
		ref.UID += "-recreated"
		recreated, err := resolver.Resolve(context.Background(), ref)
		if err != nil || recreated.CanonicalID == out.CanonicalID {
			t.Fatalf("recreation merge %d", n)
		}
	}
	recall := float64(matched) / 10000
	if recall < 0.999 {
		t.Fatalf("recall %f", recall)
	}
	t.Logf("deterministic identity golden: 10000 source identities, 10000 recreated UIDs, 10 tenants, 100 cluster scopes; recall=%f false merges=0; synthetic rule conformance only", recall)
}
