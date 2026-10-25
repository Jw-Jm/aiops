package incident

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/auth"
	"ops-platform/internal/authz"
	"ops-platform/internal/graph"
)

// operationAuthority is the authority proven once for one Incident write
// transaction. It is created only by authorizeTx, and every related resource in
// the same transaction (the Incident, each linked Finding and each manual
// Evidence row) must satisfy it. It never crosses a transaction boundary and
// never comes from a request body.
type operationAuthority struct {
	scope   graph.Scope
	tenant  uuid.UUID
	subject string
	native  bool
}

// authorizeTx re-proves the caller inside the caller's business write
// transaction and returns the authority for the remaining related resources.
//
// The branch is chosen by the trusted identity source, never by a subject prefix:
//   - native: the live session and its operator grant are re-proven, then the
//     current operator scope is rebuilt from identity.role_grants under the
//     shared account -> grant lock order (graph.EffectivePersistedNativeTx). A
//     revocation that commits first is refused, and the account/grant rows stay
//     locked until this business transaction commits.
//   - legacy: the frozen platform.sp05_lock_operator_scope predicate keeps its
//     exact lock order and semantics.
//
// A native request never reads platform.role_bindings and a legacy request never
// borrows a native grant.
func authorizeTx(ctx context.Context, tx pgx.Tx, scope graph.Scope, subject string, i Incident) (operationAuthority, error) {
	a := operationAuthority{scope: scope, subject: subject}
	if tx == nil || subject == "" || scope.Tenant != i.TenantID || scope.Cluster != i.ClusterUID || !scope.Allows(i.ResourceCanonicalID, i.Namespace) {
		return a, graph.ErrScope
	}
	tenant, err := uuid.Parse(i.TenantID)
	if err != nil {
		return a, graph.ErrScope
	}
	a.tenant = tenant
	native, ok := auth.NativeSessionFromContext(ctx)
	if !ok {
		if err := lockLegacyOperator(ctx, tx, tenant, subject, i.ClusterUID, i.Namespace); err != nil {
			return a, err
		}
		return a, nil
	}
	session := native.Session
	if session.TenantID != tenant || session.Subject != subject ||
		session.UserID == uuid.Nil || session.AuthorizationVersion < 1 {
		return a, graph.ErrScope
	}
	// The session-bound check pins the session, account and grant rows in this
	// transaction, so an already committed revocation or disable is refused.
	allowed, err := authz.NativeRoleAuthorized(ctx, tx, auth.RequestContext{TenantID: tenant, Subject: subject}, "operator")
	if err != nil || !allowed {
		return a, graph.ErrScope
	}
	// Rebuild the exact current operator scope for this cluster. The caller's
	// scope may be narrower, but it can never exceed the live grant.
	current, err := graph.EffectivePersistedNativeTx(ctx, tx, i.TenantID, subject, i.ClusterUID, session.UserID, session.AuthorizationVersion)
	if err != nil || !graph.ScopeCovers(current, scope) || !current.Allows(i.ResourceCanonicalID, i.Namespace) {
		return a, graph.ErrScope
	}
	a.native = true
	return a, nil
}

// authorizeResource checks one related resource (a linked Finding or an Evidence
// row) against the authority already proven for this transaction. The legacy
// branch keeps its per-resource sp05 lock exactly as before; the native branch is
// covered by the rebuilt grant and its held shared locks.
func (a operationAuthority) authorizeResource(ctx context.Context, tx pgx.Tx, cluster, resourceCanonicalID, namespace string) error {
	if a.subject == "" || !a.scope.Allows(resourceCanonicalID, namespace) {
		return graph.ErrScope
	}
	if a.native {
		return nil
	}
	return lockLegacyOperator(ctx, tx, a.tenant, a.subject, cluster, namespace)
}

// lockLegacyOperator is the unchanged legacy fence. It re-locks the tenant,
// cluster and active operator bindings of the exact namespace and returns
// graph.ErrScope when the subject holds no matching binding.
func lockLegacyOperator(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, subject, cluster, namespace string) error {
	var current bool
	if err := tx.QueryRow(ctx, `SELECT platform.sp05_lock_operator_scope($1,$2,$3,$4)`, tenant, subject, cluster, namespace).Scan(&current); err != nil {
		return err
	}
	if !current {
		return graph.ErrScope
	}
	return nil
}
