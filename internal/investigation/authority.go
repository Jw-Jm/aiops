package investigation

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/auth"
	"ops-platform/internal/authz"
)

// Persisted identity sources for a v2 Incident investigation Job. They match the
// database constraint added with the provenance columns and are never inferred
// from a subject prefix.
const (
	JobAuthorityLegacy = "legacy"
	JobAuthorityNative = "native"
)

// jobAuthority is the persisted identity basis of a v2 Job. The type and the Job
// field are unexported so the investigation-job/v2 JSON stays byte-identical and
// an HTTP client can never supply the source; every read path repopulates it from
// the Job row.
type jobAuthority struct {
	source  string
	userID  uuid.UUID
	version int
}

// valid reports whether the triple is one of the encodings the database
// constraint allows. An empty source is only the historical legacy encoding: a v2
// Job written before the provenance columns existed could only have been admitted
// through platform.role_bindings, because the native v2 path did not exist.
func (a jobAuthority) valid() bool {
	switch a.source {
	case JobAuthorityNative:
		return a.userID != uuid.Nil && a.version >= 1
	case JobAuthorityLegacy, "":
		return a.userID == uuid.Nil && a.version == 0
	default:
		return false
	}
}

func (a jobAuthority) native() bool {
	return a.source == JobAuthorityNative && a.valid()
}

// same compares the semantic identity of two provenances. A row written before the
// provenance columns existed reads back as the empty source while a new request is
// captured as the explicit legacy source; both mean "admitted through
// platform.role_bindings" and must replay as the same Job. Native never equals
// legacy, and an invalid encoding never equals anything. The stored encoding is
// never rewritten.
func (a jobAuthority) same(b jobAuthority) bool {
	if !a.valid() || !b.valid() || a.native() != b.native() {
		return false
	}
	if !a.native() {
		return true
	}
	return a.userID == b.userID && a.version == b.version
}

// reproveNativeSessionTx re-runs the session-bound native proof inside the caller's
// write transaction. Capturing the source from the request context is not enough:
// between the HTTP pre-check and this transaction the session may have been
// revoked, the password reset, or the credential/session version rotated while the
// account stayed active. Admitting or replaying a Job must therefore re-prove the
// exact tenant/subject/user/version it is about to persist, and the session row
// itself, in the same transaction that writes the Job.
func reproveNativeSessionTx(ctx context.Context, tx pgx.Tx, j Job) error {
	native, ok := auth.NativeSessionFromContext(ctx)
	if !ok {
		return ErrDenied
	}
	if native.Session.TenantID != j.TenantID || native.Session.Subject != j.Subject ||
		native.Session.UserID != j.authority.userID || native.Session.AuthorizationVersion != j.authority.version {
		return ErrDenied
	}
	authorized, err := authz.NativeRoleAuthorized(ctx, tx, auth.RequestContext{TenantID: j.TenantID, Subject: j.Subject}, "operator")
	if err != nil || !authorized {
		return ErrDenied
	}
	return nil
}

// jobAuthorityFromContext captures the identity source that the current request
// proved. It reads only the trusted middleware context. A native session that is
// missing its account or version fails closed instead of being described as
// legacy.
func jobAuthorityFromContext(ctx context.Context) (jobAuthority, error) {
	if native, ok := auth.NativeSessionFromContext(ctx); ok {
		if native.Session.UserID == uuid.Nil || native.Session.AuthorizationVersion < 1 {
			return jobAuthority{}, ErrDenied
		}
		return jobAuthority{source: JobAuthorityNative, userID: native.Session.UserID, version: native.Session.AuthorizationVersion}, nil
	}
	return jobAuthority{source: JobAuthorityLegacy}, nil
}

// AuthoritySource reports the persisted identity source of this Job: legacy (the
// historical empty encoding included) or native. For a v3 assistant job the
// source lives in its frozen assistant binding; a v2 job carries it in the
// provenance columns.
func (j Job) AuthoritySource() string {
	if j.Assistant != nil {
		return j.Assistant.AuthorizationSource
	}
	return j.authority.source
}

// NativeAuthority returns the frozen native account/version of this Job. It is
// only present for a native job admitted through a native session.
func (j Job) NativeAuthority() (uuid.UUID, int, bool) {
	if !j.authority.native() {
		return uuid.Nil, 0, false
	}
	return j.authority.userID, j.authority.version, true
}
