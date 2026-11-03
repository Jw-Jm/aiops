package investigation

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"ops-platform/internal/auth"
	"ops-platform/internal/graph"
	"ops-platform/internal/identity"
	"testing"
)

func TestMappedIncidentReadNeverBorrowsNativeJobAuthority(t *testing.T) {
	tenant, user := uuid.New(), uuid.New()
	subject := "user:" + user.String()
	ctx := auth.WithNativeSession(context.Background(), auth.NativeSessionContext{Session: identity.ActiveSession{TenantID: tenant, UserID: user, Subject: subject}})
	scope := graph.Scope{Tenant: tenant.String(), Cluster: "cluster", Namespaces: []string{"apps"}}
	base := Job{SchemaVersion: "investigation-job/v2", TenantID: tenant, JobID: uuid.New(), Subject: "old-owner", Scope: scope, EffectiveScopeDigest: graph.ScopeDigest(scope), authority: jobAuthority{source: JobAuthorityLegacy}}
	for _, tc := range []struct {
		name   string
		change func(*Job)
	}{
		{"native row", func(j *Job) { j.authority = jobAuthority{source: JobAuthorityNative, userID: uuid.New(), version: 1} }},
		{"invalid provenance", func(j *Job) { j.authority = jobAuthority{source: JobAuthorityLegacy, userID: uuid.New()} }},
		{"assistant row uses its separate read path", func(j *Job) { j.SchemaVersion = "investigation-job/v3" }},
		{"foreign tenant", func(j *Job) { j.TenantID = uuid.New() }},
		{"broken frozen digest", func(j *Job) { j.EffectiveScopeDigest = "changed" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			j := base
			tc.change(&j)
			if err := currentRead(ctx, nil, j, subject); !errors.Is(err, ErrDenied) {
				t.Fatalf("read error=%v", err)
			}
		})
	}
	if err := currentRead(context.Background(), nil, base, subject); !errors.Is(err, ErrDenied) {
		t.Fatalf("sessionless read=%v", err)
	}
}
