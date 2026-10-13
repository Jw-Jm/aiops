package graph

import "testing"

func TestNarrowedAssistantGrant(t *testing.T) {
	grant := Scope{Tenant: "tenant", Cluster: "cluster", AuthorizationRevision: "revision", Namespaces: []string{"apps", "private"}}
	narrow := grant
	narrow.Namespaces = []string{"apps"}
	claims := Claims{Scope: narrow, GrantSnapshotDigest: ScopeDigest(grant)}
	if !claims.AllowsCurrent(grant) {
		t.Fatal("signed subset rejected")
	}
	legacy := Claims{Scope: narrow}
	if legacy.AllowsCurrent(grant) {
		t.Fatal("legacy equality boundary changed")
	}
	for _, mutate := range []func(*Scope){func(s *Scope) { s.Namespaces = []string{"outside"} }, func(s *Scope) { s.ClusterScoped = true }, func(s *Scope) { s.Cluster = "other" }, func(s *Scope) { s.Tenant = "other" }, func(s *Scope) { s.AuthorizationRevision = "new" }} {
		bad := narrow
		mutate(&bad)
		claims.Scope = bad
		if claims.AllowsCurrent(grant) {
			t.Fatalf("scope expansion accepted: %+v", bad)
		}
	}
	claims.Scope = narrow
	grant.AuthorizationRevision = "revoked"
	if claims.AllowsCurrent(grant) {
		t.Fatal("changed current grant accepted")
	}
}
