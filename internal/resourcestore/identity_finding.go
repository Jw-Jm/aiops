package resourcestore

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/contract"
	"ops-platform/internal/finding"
	"ops-platform/internal/resource"
	"time"
)

// Internal identity conflicts use the same aggregate and delivery contract as other Findings.
func identityConflictFinding(ctx context.Context, tx pgx.Tx, tenant, source uuid.UUID, clusterID *uuid.UUID, ref resource.SourceRef, resolution resource.Resolution, observedID resource.CanonicalID) error {
	fingerprint := finding.Hash([]any{"identity-conflict/v2", source.String(), ref.Scope, ref.Kind, resolution.SourceValues})
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fingerprint)).String()
	var revision int64
	var received time.Time
	if err := tx.QueryRow(ctx, `SELECT revision,clock_timestamp() FROM platform.source_registrations WHERE tenant_id=$1 AND source_id=$2 AND status='active'`, tenant, source).Scan(&revision, &received); err != nil {
		return err
	}
	data, _ := json.Marshal(map[string]any{"resolutionStatus": "conflicted", "ruleVersion": resolution.RuleVersion, "sourceValues": resolution.SourceValues})
	envelope := finding.Envelope{SchemaVersion: "finding-envelope/v2", TenantID: tenant.String(), SourceRegistrationID: source.String(), ClusterUID: ref.Scope, EventID: id, IdempotencyKey: id, ResourceCanonicalID: observedID.String(), Namespace: ref.Namespace, RuleID: "identity-conflict", RuleFamily: "identity", NormalizedSymptom: "identity_conflicted", OccurrenceID: id, StartsAt: ref.ObservedAt, ObservedAt: ref.ObservedAt, TimeReliable: false, State: "firing", Severity: "warning", Payload: data, EvidenceRefs: []string{}}
	if err := envelope.Validate(tenant.String(), source.String(), ref.Scope); err != nil {
		return err
	}
	f := finding.Finding{Envelope: envelope, FindingID: id, SourceFingerprint: finding.Fingerprint(tenant.String(), source.String(), ref.Scope, envelope), AggregateRevision: 1, SourceRevision: revision, ReceivedAt: received, FirstReceivedAt: received, Digest: finding.SemanticDigest(envelope)}
	raw, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if err := contract.Validate("https://ops.local/schemas/finding/v2", raw); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO finding.records(tenant_id,finding_id,source_id,cluster_id,schema_version,event_id,payload,observed_at,received_at,source_fingerprint,occurrence_id,aggregate_revision,lifecycle_state,resource_canonical_id,namespace,cluster_uid,source_sequence,semantic_digest) VALUES($1,$2,$3,$4,'finding-envelope/v2',$5,$6,$7,$8,$9,$5,1,'firing',$10,$11,$12,0,$13) ON CONFLICT(tenant_id,source_id,event_id) DO NOTHING`, tenant, id, source, clusterID, id, raw, ref.ObservedAt, received, f.SourceFingerprint, observedID.String(), ref.Namespace, ref.Scope, f.Digest)
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO finding.outbox(tenant_id,outbox_id,finding_id,event_type,schema_version,payload,aggregate_revision) VALUES($1,$2,$2,'finding.changed','finding/v2',$3,1)`, tenant, id, raw)
	return err
}
