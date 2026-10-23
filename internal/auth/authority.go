package auth

import (
	"context"

	"github.com/google/uuid"
)

// Authority sources for persisted decisions. A decision that outlives its
// browser session (a Worker job, a command execution) freezes which identity
// system proved it, at which version, so the asynchronous recheck never guesses
// or falls back to the other system.
const (
	AuthoritySourceNative = "native"
	AuthoritySourceLegacy = "legacy"
)

// AuthorityRef is the persisted identity basis of one decision.
type AuthorityRef struct {
	Source               string
	UserID               uuid.UUID
	AuthorizationVersion int
}

// AuthorityFromContext returns the identity system the current request proves
// its authority with. A native session is never described as legacy.
func AuthorityFromContext(ctx context.Context) AuthorityRef {
	if native, ok := NativeSessionFromContext(ctx); ok && native.Session.UserID != uuid.Nil {
		return AuthorityRef{
			Source:               AuthoritySourceNative,
			UserID:               native.Session.UserID,
			AuthorizationVersion: native.Session.AuthorizationVersion,
		}
	}
	return AuthorityRef{Source: AuthoritySourceLegacy}
}

// Native reports whether the decision was admitted by a native account.
func (ref AuthorityRef) Native() bool {
	return ref.Source == AuthoritySourceNative && ref.UserID != uuid.Nil && ref.AuthorizationVersion >= 1
}
