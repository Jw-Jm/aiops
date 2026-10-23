package assistant

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
	"ops-platform/internal/auth"
	"ops-platform/internal/graph"
	"ops-platform/internal/identity"
	"ops-platform/internal/investigation"
)

func mappedReadSubjects(actor string) readSubjects {
	return readSubjects{
		actor: actor,
		mappings: map[string]identity.LegacySubjectMapping{
			"legacy-owner": {
				MappingID:      uuid.MustParse("11111111-1111-4111-8111-111111111111"),
				LegacySubject:  "legacy-owner",
				EvidenceDigest: "sha256:" + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			},
		},
	}
}

func TestReadSubjectsAllowOnlyExplicitMappings(t *testing.T) {
	rs := mappedReadSubjects("user:native")
	if subjects := rs.subjects(); !slices.Contains(subjects, "user:native") || !slices.Contains(subjects, "legacy-owner") || len(subjects) != 2 {
		t.Fatalf("owner allowlist = %v", subjects)
	}
	own, err := rs.authority("user:native")
	if err != nil || own.Mapped || own.CurrentSubject != "user:native" {
		t.Fatalf("own history authority = %+v, %v", own, err)
	}
	mapped, err := rs.authority("legacy-owner")
	if err != nil {
		t.Fatalf("mapped authority: %v", err)
	}
	if !mapped.Mapped || mapped.OwnerSubject != "legacy-owner" || mapped.MappingID == uuid.Nil || mapped.EvidenceDigest == "" {
		t.Fatalf("mapped authority = %+v", mapped)
	}
	if _, err := rs.authority("legacy-unmapped"); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("unmapped owner authority error = %v", err)
	}
	if _, err := rs.authority(""); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("empty owner authority error = %v", err)
	}
}

func TestOwnerReadNeverAuthorisesAnotherSubject(t *testing.T) {
	c := Conversation{OwnerSubject: "legacy-owner"}
	read := ownerRead(c, "legacy-owner")
	if read.Mapped || read.OwnerSubject != "legacy-owner" || read.CurrentSubject != "legacy-owner" {
		t.Fatalf("owner-exact read = %+v", read)
	}
}

func TestMappedReadWithoutNativeSessionIsDenied(t *testing.T) {
	c := Conversation{OwnerSubject: "legacy-owner", AuthorizationDomain: "operational", ClusterUID: "cluster-a"}
	read := investigation.HistoricalRead{
		OwnerSubject:   "legacy-owner",
		CurrentSubject: "user:native",
		Mapped:         true,
		MappingID:      uuid.MustParse("11111111-1111-4111-8111-111111111111"),
	}
	// A request context without a proven native session must never reach the
	// legacy binding table or the mapping allowlist.
	if _, err := authorizeRead(context.Background(), nil, c, read); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("mapped read without a native session error = %v", err)
	}
}

func TestMappedReadRequiresFrozenSnapshotCoveringTheExecutionScope(t *testing.T) {
	tenant := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	ctx := auth.WithNativeSession(context.Background(), auth.NativeSessionContext{Session: identity.ActiveSession{TenantID: tenant, Subject: "user:native"}})
	read := investigation.HistoricalRead{
		OwnerSubject:   "legacy-owner",
		CurrentSubject: "user:native",
		Mapped:         true,
		MappingID:      uuid.MustParse("11111111-1111-4111-8111-111111111111"),
	}
	frozen := graph.Scope{Tenant: tenant.String(), Cluster: "cluster-a", Namespaces: []string{"apps"}}
	execution := graph.Scope{Tenant: tenant.String(), Cluster: "cluster-a", Namespaces: []string{"apps", "private"}}
	c := Conversation{
		TenantID: tenant, OwnerSubject: "legacy-owner", AuthorizationDomain: "operational", ClusterUID: "cluster-a",
		Binding: ConversationBinding{GrantSnapshot: frozen, ExecutionScope: execution},
	}
	// The frozen snapshot no longer contains the frozen execution scope, so the
	// binding is corrupt and denied before any current grant is consulted.
	if _, err := authorizeRead(ctx, nil, c, read); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("corrupted frozen binding error = %v", err)
	}
}
