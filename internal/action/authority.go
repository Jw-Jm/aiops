package action

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/auth"
	"ops-platform/internal/graph"
)

// commandAuthorityFromContext freezes the identity system that proved this
// command. A command outlives the browser session: the post-check later re-proves
// the same source without a session.
func commandAuthorityFromContext(ctx context.Context) Authority {
	ref := auth.AuthorityFromContext(ctx)
	return Authority{Source: ref.Source, UserID: ref.UserID, AuthorizationVersion: ref.AuthorizationVersion}
}

// scopeTx rebuilds the frozen authority of one execution binding. It is the only
// place that decides which identity system may prove a stored command, so the
// dispatch, claim and post-check paths cannot drift apart.
func (a Authority) scopeTx(ctx context.Context, tx pgx.Tx, b Binding) (graph.Scope, error) {
	native, err := a.nativeForRecheck()
	if err != nil {
		return graph.Scope{}, ErrDenied
	}
	if native {
		scope, err := graph.EffectivePersistedNativeTx(ctx, tx, b.TenantID.String(), b.Subject, b.ClusterUID, a.UserID, a.AuthorizationVersion)
		if err != nil {
			return graph.Scope{}, ErrDenied
		}
		return scope, nil
	}
	scope, err := graph.EffectiveTx(ctx, tx, b.TenantID.String(), b.Subject, b.ClusterUID, true)
	if err != nil {
		return graph.Scope{}, ErrDenied
	}
	return scope, nil
}

// authorityScopeTx re-proves one execution's authority in the caller's
// transaction. Only three encodings are accepted: a complete native authority, an
// explicit legacy authority, or the historical empty encoding (which can only be
// legacy, because a native command was rejected before this column existed). An
// unknown source or an inconsistent mix is refused: the identity system is never
// switched silently.
func (e Execution) authorityScopeTx(ctx context.Context, tx pgx.Tx) (graph.Scope, error) {
	return e.Authority.scopeTx(ctx, tx, e.Binding)
}

// bareAuthority classifies an authority for the dispatch/claim path, which has no
// live session. It reports whether the identity system can be re-proved at all,
// so a corrupt column fails the dispatch instead of falling back to legacy.
func (a Authority) bareAuthority() error {
	_, err := a.nativeForRecheck()
	return err
}

// nativeForRecheck classifies the persisted authority. It returns true only for a
// complete native authority and false only for an explicit legacy or the
// historical empty encoding. Anything else is refused rather than classified.
func (a Authority) nativeForRecheck() (bool, error) {
	switch a.Source {
	case auth.AuthoritySourceNative:
		if a.UserID == uuid.Nil || a.AuthorizationVersion < 1 {
			return false, ErrDenied
		}
		return true, nil
	case auth.AuthoritySourceLegacy, "":
		if a.UserID != uuid.Nil || a.AuthorizationVersion != 0 {
			return false, ErrDenied
		}
		return false, nil
	default:
		return false, ErrDenied
	}
}
