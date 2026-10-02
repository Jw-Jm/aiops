package resourcestore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/contract"
	"ops-platform/internal/resource"
	"strings"
	"time"
)

// Identity conflicts are producer Findings. Incident reduction belongs to SP-05.
func identityConflictFinding(ctx context.Context, tx pgx.Tx, tenant, source uuid.UUID, clusterID *uuid.UUID, ref resource.SourceRef, resolution resource.Resolution, observedID resource.CanonicalID) error {
	fingerprintInput, _ := json.Marshal(struct {
		Source, Scope, Kind string
		Values              map[string]string
	}{source.String(), ref.Scope, ref.Kind, resolution.SourceValues})
	digest := sha256.Sum256(fingerprintInput)
	fingerprint := "sha256:" + hex.EncodeToString(digest[:])
	id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(fingerprint))
	payload := map[string]any{"schemaVersion": "finding-envelope/v1", "eventId": id.String(), "tenantId": tenant.String(), "clusterUid": ref.Scope, "source": map[string]any{"system": "entity-resolver", "instance": source.String(), "ruleId": "identity-conflict", "ruleVersion": strings.ReplaceAll(resolution.RuleVersion, "/", ":")}, "sourceFingerprint": fingerprint, "occurrenceId": id.String(), "lifecycleState": "firing", "startsAt": ref.ObservedAt, "resolvedAt": nil, "observedAt": ref.ObservedAt, "receivedAt": time.Now().UTC(), "resourceCanonicalId": observedID.String(), "status": "identity_conflicted", "severity": "warning", "payloadDigest": fingerprint, "idempotencyKey": fingerprint, "evidenceRefs": []string{}, "extensions": map[string]any{"resolutionStatus": "conflicted", "ruleVersion": strings.ReplaceAll(resolution.RuleVersion, "/", ":")}}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if err := contract.Validate("https://ops.local/schemas/finding-envelope/v1", raw); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO finding.records(tenant_id,finding_id,source_id,cluster_id,schema_version,event_id,payload,observed_at) VALUES($1,$2,$3,$4,'finding-envelope/v1',$5,$6,$7) ON CONFLICT(tenant_id,source_id,event_id) DO NOTHING`, tenant, id, source, clusterID, id.String(), raw, ref.ObservedAt)
	if err != nil || tag.RowsAffected() == 0 {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO finding.outbox(tenant_id,outbox_id,finding_id,event_type,schema_version,payload) VALUES($1,$2,$2,'finding.received','finding-envelope/v1',$3)`, tenant, id, raw)
	return err
}
