package incident

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"ops-platform/internal/auth"
	"ops-platform/internal/graph"
	"ops-platform/internal/identity"
	"ops-platform/internal/resource"
)

func authorityFixture(tenant uuid.UUID, cluster string, namespaces []string) (graph.Scope, Incident) {
	canonical := resource.CanonicalID{Domain: "k8s", Tenant: tenant.String(), Scope: cluster, APIGroup: "core", Kind: "Pod", StableID: "api-0"}.String()
	scope := graph.Scope{Tenant: tenant.String(), Cluster: cluster, Namespaces: namespaces, AuthorizationRevision: "sha256:revision"}
	item := Incident{SchemaVersion: "incident/v2", TenantID: tenant.String(), IncidentID: uuid.Must(uuid.NewV7()).String(),
		ClusterUID: cluster, ResourceCanonicalID: canonical, Namespace: "apps", State: "open", Revision: 1,
		Fingerprint: "fixture", PolicyVersion: PolicyVersion}
	return scope, item
}

// The primary Incident and every related resource must stay inside the authority
// proven for the transaction. The native branch performs no second database read,
// so this is a pure containment decision.
func TestOperationAuthorityResourceContainment(t *testing.T) {
	tenant := uuid.Must(uuid.NewV7())
	scope, item := authorityFixture(tenant, "cluster-a", []string{"apps"})
	authority := operationAuthority{scope: scope, tenant: tenant, subject: "user:native", native: true}
	if err := authority.authorizeResource(context.Background(), nil, item.ClusterUID, item.ResourceCanonicalID, item.Namespace); err != nil {
		t.Fatalf("in-scope resource: %v", err)
	}
	if err := authority.authorizeResource(context.Background(), nil, item.ClusterUID, item.ResourceCanonicalID, "private"); !errors.Is(err, graph.ErrScope) {
		t.Fatalf("foreign namespace = %v", err)
	}
	other := resource.CanonicalID{Domain: "k8s", Tenant: tenant.String(), Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: "other"}.String()
	// An empty resource list on the authority means "all resources"; a resource
	// limited authority refuses any other canonical id.
	limitedResources := authority
	limitedResources.scope.Resources = []string{item.ResourceCanonicalID}
	if err := limitedResources.authorizeResource(context.Background(), nil, item.ClusterUID, item.ResourceCanonicalID, item.Namespace); err != nil {
		t.Fatalf("listed resource: %v", err)
	}
	if err := limitedResources.authorizeResource(context.Background(), nil, item.ClusterUID, other, "apps"); !errors.Is(err, graph.ErrScope) {
		t.Fatalf("resource not listed in the authority: %v", err)
	}
	empty := operationAuthority{scope: scope, tenant: tenant, native: true}
	if err := empty.authorizeResource(context.Background(), nil, item.ClusterUID, item.ResourceCanonicalID, item.Namespace); !errors.Is(err, graph.ErrScope) {
		t.Fatalf("missing subject = %v", err)
	}
	restricted, restrictedItem := authorityFixture(tenant, "cluster-a", []string{"private"})
	limited := operationAuthority{scope: restricted, tenant: tenant, subject: "user:native", native: true}
	if err := limited.authorizeResource(context.Background(), nil, restrictedItem.ClusterUID, restrictedItem.ResourceCanonicalID, restrictedItem.Namespace); !errors.Is(err, graph.ErrScope) {
		t.Fatalf("incident outside the granted namespace = %v", err)
	}
}

// authorizeTx refuses every mismatch before it can touch a transaction, so an
// untrusted scope never reaches the business write.
func TestAuthorizeTxRejectsForgedScope(t *testing.T) {
	tenant := uuid.Must(uuid.NewV7())
	scope, item := authorityFixture(tenant, "cluster-a", []string{"apps"})
	cases := []struct {
		name    string
		scope   graph.Scope
		subject string
	}{
		{name: "empty subject", scope: scope, subject: ""},
		{name: "foreign tenant", scope: graph.Scope{Tenant: uuid.Must(uuid.NewV7()).String(), Cluster: scope.Cluster, Namespaces: scope.Namespaces}, subject: "user:native"},
		{name: "foreign cluster", scope: graph.Scope{Tenant: scope.Tenant, Cluster: "cluster-b", Namespaces: scope.Namespaces}, subject: "user:native"},
		{name: "foreign namespace", scope: graph.Scope{Tenant: scope.Tenant, Cluster: scope.Cluster, Namespaces: []string{"private"}}, subject: "user:native"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := authorizeTx(context.Background(), nil, tc.scope, tc.subject, item); !errors.Is(err, graph.ErrScope) {
				t.Fatalf("authorize = %v", err)
			}
		})
	}
	// A native session bound to another subject is refused before the grant proof.
	native := auth.WithNativeSession(context.Background(), auth.NativeSessionContext{Session: identity.ActiveSession{
		TenantID: tenant, UserID: uuid.Must(uuid.NewV7()), Subject: "user:other", AuthorizationVersion: 1,
	}})
	if _, err := authorizeTx(native, nil, scope, "user:native", item); !errors.Is(err, graph.ErrScope) {
		t.Fatalf("native subject mismatch = %v", err)
	}
	// The exports used by the HTTP idempotency admission keep the same rule.
	if err := Authorize(context.Background(), nil, scope, "", item); !errors.Is(err, graph.ErrScope) {
		t.Fatalf("exported Authorize = %v", err)
	}
	if err := AuthorizeAll(context.Background(), nil, scope, "", item); !errors.Is(err, graph.ErrScope) {
		t.Fatalf("exported AuthorizeAll = %v", err)
	}
}
