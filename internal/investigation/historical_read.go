package investigation

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/audit"
	"ops-platform/internal/auth"
	"ops-platform/internal/graph"
	"ops-platform/internal/identity"
)

// Only read paths may use a reviewed legacy mapping. Worker and cancel paths
// keep the exact frozen owner and their existing authority checks.
func currentRead(ctx context.Context, tx pgx.Tx, j Job, subject string) error {
	if j.Subject == subject {
		return current(ctx, tx, j)
	}
	native, ok := auth.NativeSessionFromContext(ctx)
	if !ok || native.Session.TenantID != j.TenantID || native.Session.Subject != subject ||
		j.SchemaVersion != "investigation-job/v2" || !j.authority.valid() ||
		(j.authority.source != "" && j.authority.source != JobAuthorityLegacy) ||
		graph.ScopeDigest(j.Scope) != j.EffectiveScopeDigest {
		return ErrDenied
	}
	mappings, err := identity.MappedLegacySubjectsTx(ctx, tx, j.TenantID, native.Session.UserID)
	if err != nil {
		return ErrDenied
	}
	var mapping identity.LegacySubjectMapping
	for _, candidate := range mappings {
		if candidate.LegacySubject == j.Subject {
			mapping = candidate
			break
		}
	}
	if mapping.MappingID == uuid.Nil {
		return ErrDenied
	}
	grant, err := graph.EffectiveTx(ctx, tx, j.TenantID.String(), subject, j.Scope.Cluster, true)
	if err != nil || !ScopeCoversMappedOwner(grant, j.Scope) {
		return ErrDenied
	}
	if err := currentPolicy(ctx, tx, j); err != nil {
		return err
	}
	_, err = audit.Append(ctx, tx, audit.Entry{
		TenantID: j.TenantID, RecordID: uuid.Must(uuid.NewV7()),
		EventType: "investigation.history.mapped_owner.read", EntityKind: "investigation", EntityID: j.JobID,
		Subject: subject, Payload: map[string]any{"jobId": j.JobID, "ownerSubject": j.Subject,
			"mappingId": mapping.MappingID, "mappingEvidence": mapping.EvidenceDigest, "authority": "current-native"},
	})
	return err
}
