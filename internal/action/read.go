package action

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/auth"
	"ops-platform/internal/graph"
)

func effectiveRead(ctx context.Context, tx pgx.Tx, a auth.RequestContext, b Binding) (bool, error) {
	scope, err := graph.EffectiveTx(ctx, tx, a.TenantID.String(), a.Subject, b.ClusterUID, true)
	ns := ""
	if b.Namespace != nil {
		ns = *b.Namespace
	}
	if err != nil || !scope.Allows(b.Target, ns) {
		return false, ErrDenied
	}
	return true, nil
}

// readable filters a list page with the same rule as effectiveRead but treats a
// denied row as invisible instead of failing the whole listing, and caches the
// effective scope per cluster inside one page scan. A subject with no effective
// scope (graph.ErrScope) simply sees nothing; infrastructure errors still fail.
func readable(ctx context.Context, tx pgx.Tx, a auth.RequestContext, b Binding, scopes map[string]graph.Scope) (bool, error) {
	scope, ok := scopes[b.ClusterUID]
	if !ok {
		var err error
		scope, err = graph.EffectiveTx(ctx, tx, a.TenantID.String(), a.Subject, b.ClusterUID, true)
		if err != nil {
			if errors.Is(err, graph.ErrScope) {
				scope = graph.Scope{Tenant: a.TenantID.String(), Cluster: b.ClusterUID, Namespaces: []string{}, Resources: []string{}}
			} else {
				return false, err
			}
		}
		scopes[b.ClusterUID] = scope
	}
	ns := ""
	if b.Namespace != nil {
		ns = *b.Namespace
	}
	return scope.Allows(b.Target, ns), nil
}
