package investigation

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"ops-platform/internal/auth"
	"ops-platform/internal/graph"
	"ops-platform/internal/identity"
)

func nativeSessionContext(t *testing.T, userID uuid.UUID, version int) context.Context {
	t.Helper()
	return auth.WithNativeSession(context.Background(), auth.NativeSessionContext{Session: identity.ActiveSession{
		TenantID: uuid.Must(uuid.NewV7()), UserID: userID, Subject: "user:" + userID.String(),
		AuthorizationVersion: version,
	}})
}

// The identity source of a new v2 Job comes only from the trusted authentication
// context: a native session is never described as legacy, and an incomplete
// native session fails closed instead of degrading to the legacy table.
func TestJobAuthorityFromContext(t *testing.T) {
	userID := uuid.Must(uuid.NewV7())
	native, err := jobAuthorityFromContext(nativeSessionContext(t, userID, 3))
	if err != nil {
		t.Fatalf("native authority: %v", err)
	}
	if native.source != JobAuthorityNative || native.userID != userID || native.version != 3 {
		t.Fatalf("native authority = %+v", native)
	}
	legacy, err := jobAuthorityFromContext(context.Background())
	if err != nil || legacy != (jobAuthority{source: JobAuthorityLegacy}) {
		t.Fatalf("legacy authority = %+v (%v)", legacy, err)
	}
	if _, err := jobAuthorityFromContext(nativeSessionContext(t, uuid.Nil, 1)); !errors.Is(err, ErrDenied) {
		t.Fatalf("native session without an account = %v", err)
	}
	if _, err := jobAuthorityFromContext(nativeSessionContext(t, userID, 0)); !errors.Is(err, ErrDenied) {
		t.Fatalf("native session without a version = %v", err)
	}
}

func TestJobAuthorityEncoding(t *testing.T) {
	userID := uuid.Must(uuid.NewV7())
	cases := []struct {
		name    string
		value   jobAuthority
		valid   bool
		native  bool
		source  string
		wantErr bool
	}{
		{name: "native", value: jobAuthority{source: JobAuthorityNative, userID: userID, version: 2}, valid: true, native: true, source: JobAuthorityNative},
		{name: "native without account", value: jobAuthority{source: JobAuthorityNative, version: 2}, source: JobAuthorityNative},
		{name: "native without version", value: jobAuthority{source: JobAuthorityNative, userID: userID}, source: JobAuthorityNative},
		{name: "legacy", value: jobAuthority{source: JobAuthorityLegacy}, valid: true, source: JobAuthorityLegacy},
		{name: "legacy with account", value: jobAuthority{source: JobAuthorityLegacy, userID: userID}, source: JobAuthorityLegacy},
		{name: "historical empty", value: jobAuthority{}, valid: true, source: ""},
		{name: "historical with account", value: jobAuthority{userID: userID, version: 1}},
		{name: "unknown source", value: jobAuthority{source: "keycloak"}, source: "keycloak"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.value.valid(); got != tc.valid {
				t.Fatalf("valid = %v", got)
			}
			if got := tc.value.native(); got != tc.native {
				t.Fatalf("native = %v", got)
			}
			job := Job{TenantID: uuid.Must(uuid.NewV7()), JobID: uuid.Must(uuid.NewV7()), authority: tc.value}
			if got := job.AuthoritySource(); got != tc.source {
				t.Fatalf("source = %q", got)
			}
			gotUser, gotVersion, ok := job.NativeAuthority()
			if ok != tc.native || (tc.native && (gotUser != userID || gotVersion != 2)) {
				t.Fatalf("native authority = %s/%d/%v", gotUser, gotVersion, ok)
			}
		})
	}
	// A v3 job reports the frozen assistant binding instead of the v2 columns.
	assistant := Job{Assistant: &AssistantTask{AuthorizationSource: AuditSourceNative}}
	if assistant.AuthoritySource() != AuditSourceNative {
		t.Fatalf("v3 authority source = %q", assistant.AuthoritySource())
	}
	if _, _, ok := assistant.NativeAuthority(); ok {
		t.Fatal("v3 job reported a v2 native authority")
	}
}

// A persisted v2 Job must be re-proven through its own source branch, and an
// unknown or incomplete triple is denied before any database work.
func TestCurrentRejectsUnknownProvenance(t *testing.T) {
	job := Job{SchemaVersion: "investigation-job/v2", TenantID: uuid.Must(uuid.NewV7()), JobID: uuid.Must(uuid.NewV7()),
		Subject: "user:test", Scope: graph.Scope{Cluster: "cluster-a"}, PolicyVersion: uuid.Must(uuid.NewV7()).String(),
		authority: jobAuthority{source: "keycloak"}}
	if err := current(context.Background(), nil, job); !errors.Is(err, ErrDenied) {
		t.Fatalf("unknown source current = %v", err)
	}
	job.authority = jobAuthority{source: JobAuthorityNative, version: 1}
	if err := current(context.Background(), nil, job); !errors.Is(err, ErrDenied) {
		t.Fatalf("incomplete native current = %v", err)
	}
	job.authority = jobAuthority{source: JobAuthorityNative, userID: uuid.Must(uuid.NewV7()), version: 1}
	job.JobID = uuid.Nil
	if err := current(context.Background(), nil, job); !errors.Is(err, ErrDenied) {
		t.Fatalf("unpersisted native current = %v", err)
	}
	if err := admit(context.Background(), nil, job); !errors.Is(err, ErrDenied) {
		t.Fatalf("unpersisted admit = %v", err)
	}
}

// A historical row (empty source) and an explicit legacy capture describe the
// same identity system, so an idempotent replay must not conflict; native never
// equals legacy and an invalid encoding equals nothing.
func TestJobAuthoritySameSource(t *testing.T) {
	userID := uuid.Must(uuid.NewV7())
	native := jobAuthority{source: JobAuthorityNative, userID: userID, version: 1}
	cases := []struct {
		name string
		a, b jobAuthority
		same bool
	}{
		{name: "historical vs legacy", a: jobAuthority{}, b: jobAuthority{source: JobAuthorityLegacy}, same: true},
		{name: "legacy vs historical", a: jobAuthority{source: JobAuthorityLegacy}, b: jobAuthority{}, same: true},
		{name: "same native", a: native, b: native, same: true},
		{name: "native version changed", a: native, b: jobAuthority{source: JobAuthorityNative, userID: userID, version: 2}},
		{name: "native vs legacy", a: native, b: jobAuthority{source: JobAuthorityLegacy}},
		{name: "legacy vs native", a: jobAuthority{}, b: native},
		{name: "unknown vs legacy", a: jobAuthority{source: "keycloak"}, b: jobAuthority{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.a.same(tc.b); got != tc.same {
				t.Fatalf("same = %v", got)
			}
			if got := tc.b.same(tc.a); got != tc.same {
				t.Fatalf("reverse same = %v", got)
			}
		})
	}
}

// The write-transaction re-proof rejects any session that is not exactly the one
// whose source is being persisted, before it can touch the authorization query.
func TestReproveNativeSessionRejectsForeignSource(t *testing.T) {
	tenant := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	job := Job{TenantID: tenant, JobID: uuid.Must(uuid.NewV7()), Subject: "user:native",
		authority: jobAuthority{source: JobAuthorityNative, userID: userID, version: 1}}
	session := func(tenant uuid.UUID, subject string, user uuid.UUID, version int) context.Context {
		return auth.WithNativeSession(context.Background(), auth.NativeSessionContext{Session: identity.ActiveSession{
			TenantID: tenant, Subject: subject, UserID: user, AuthorizationVersion: version,
		}})
	}
	if err := reproveNativeSessionTx(context.Background(), nil, job); !errors.Is(err, ErrDenied) {
		t.Fatalf("missing session = %v", err)
	}
	cases := map[string]context.Context{
		"tenant":  session(uuid.Must(uuid.NewV7()), job.Subject, userID, 1),
		"subject": session(tenant, "user:other", userID, 1),
		"account": session(tenant, job.Subject, uuid.Must(uuid.NewV7()), 1),
		"version": session(tenant, job.Subject, userID, 2),
	}
	for name, ctx := range cases {
		if err := reproveNativeSessionTx(ctx, nil, job); !errors.Is(err, ErrDenied) {
			t.Fatalf("%s mismatch = %v", name, err)
		}
	}
}
