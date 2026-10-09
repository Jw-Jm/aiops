package action

import (
	"context"
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
