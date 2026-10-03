package finding

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/auth"
	"ops-platform/internal/persistence"
	"ops-platform/internal/resource"
	"ops-platform/internal/source"
	"sort"
	"time"
)

type Service struct{ Pool persistence.TxBeginner }

// Ingest revalidates registration and scope while holding the registration row
// against concurrent revoke/rotate. Every business write is in this transaction.
func (s Service) Ingest(ctx context.Context, b source.BoundSourceContext, e Envelope) (Finding, Disposition, error) {
	var f Finding
	disposition := Accepted
	if s.Pool == nil || b.TenantID == uuid.Nil || b.SourceID == uuid.Nil || b.ClusterUID == "" {
		return f, "", ErrUnauthorized
	}
	if err := e.Validate(b.TenantID.String(), b.SourceID.String(), b.ClusterUID); err != nil {
		if errors.Is(err, ErrConflict) {
			if auditErr := s.reject(ctx, b, e, err); auditErr != nil {
				return f, "", auditErr
			}
		}
		return f, "", err
	}
	e.StartsAt = e.StartsAt.UTC()
	e.ObservedAt = e.ObservedAt.UTC()
	e.TenantID = b.TenantID.String()
	e.SourceRegistrationID = b.SourceID.String()
	e.ClusterUID = b.ClusterUID
	// Normalize optional claims before computing the server semantic digest.
	digest := SemanticDigest(e)
	fingerprint := Fingerprint(e.TenantID, e.SourceRegistrationID, e.ClusterUID, e)
	err := persistence.WithTenantTx(ctx, s.Pool, b.TenantID, func(tx pgx.Tx) error {
		if err := CheckBound(ctx, tx, b, e.Namespace, e.SchemaVersion); err != nil {
			return err
		}
		var sourceType string
		if err := tx.QueryRow(ctx, `SELECT source_type FROM platform.source_registrations WHERE tenant_id=$1 AND source_id=$2`, b.TenantID, b.SourceID).Scan(&sourceType); err != nil {
			return err
		}
		canonical, err := resource.ParseCanonicalID(e.ResourceCanonicalID)
		if err != nil {
			return ErrInvalid
		}
		if sourceType == "redfish" && canonical.Domain != "hardware" || (sourceType == "kubernetes" || sourceType == "k8sgpt") && canonical.Domain != "k8s" {
			return ErrUnauthorized
		}
		var actualNamespace string
		identityErr := tx.QueryRow(ctx, `SELECT namespace FROM platform.resource_entities WHERE tenant_id=$1 AND canonical_id=$2 AND deleted_at IS NULL`, b.TenantID, e.ResourceCanonicalID).Scan(&actualNamespace)
		if identityErr != nil && !errors.Is(identityErr, pgx.ErrNoRows) {
			return identityErr
		}
		if identityErr == nil && actualNamespace != e.Namespace {
			return ErrUnauthorized
		}
		if actor, ok := auth.RequestContextFromContext(ctx); ok {
			var allowed bool
			if actor.TenantID != b.TenantID {
				return ErrUnauthorized
			}
			if err := tx.QueryRow(ctx, `SELECT platform.sp05_lock_operator_scope($1,$2,$3,$4)`, b.TenantID, actor.Subject, b.ClusterUID, e.Namespace).Scan(&allowed); err != nil {
				return err
			}
			if !allowed {
				return ErrUnauthorized
			}
		}

		refs, err := verifyEvidence(ctx, tx, b, e)
		if err != nil {
			return err
		}
		keys := []string{"event|" + e.EventID, "idempotency|" + e.IdempotencyKey}
		sort.Strings(keys)
		// Cross-key duplicates and first occurrences serialize even before a row exists.
		for _, key := range append(keys, "occurrence|"+fingerprint+"|"+e.OccurrenceID) {
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, e.TenantID+"|"+e.SourceRegistrationID+"|"+key); err != nil {
				return err
			}
		}
		var existingID string
		for _, key := range []struct{ kind, value string }{{"event", e.EventID}, {"idempotency", e.IdempotencyKey}} {
			var previousDigest, id string
			err := tx.QueryRow(ctx, `SELECT digest,finding_id FROM finding.transport_keys WHERE tenant_id=$1 AND source_id=$2 AND key_kind=$3 AND key_value=$4`, b.TenantID, b.SourceID, key.kind, key.value).Scan(&previousDigest, &id)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if err == nil {
				if previousDigest != digest || existingID != "" && existingID != id {
					return ErrConflict
				}
				existingID = id
			}
		}
		if existingID != "" {
			var err error
			f, err = Load(ctx, tx, b.TenantID, existingID)
			if err != nil {
				return err
			}
			disposition = Duplicate
			if err := bindTransport(ctx, tx, b, e, digest, existingID); err != nil {
				return err
			}
			// New transport aliases have an inbox receipt and a timeline entry even
			// though they do not mutate the aggregate or enqueue another delivery.
			if _, err := tx.Exec(ctx, `INSERT INTO finding.inbox(tenant_id,event_id,source_id,request_digest,state,finding_id) VALUES($1,$2,$3,$4,'accepted',$5) ON CONFLICT DO NOTHING`, b.TenantID, e.EventID, b.SourceID, digest, existingID); err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `INSERT INTO finding.timeline(tenant_id,finding_id,event_id,source_id,disposition,aggregate_revision,digest,observed_at) VALUES($1,$2,$3,$4,'duplicate',$5,$6,$7) ON CONFLICT DO NOTHING`, b.TenantID, existingID, e.EventID, b.SourceID, f.AggregateRevision, digest, e.ObservedAt)
			return err
		}
		var raw []byte
		err = tx.QueryRow(ctx, `SELECT payload FROM finding.records WHERE tenant_id=$1 AND source_fingerprint=$2 AND occurrence_id=$3 FOR UPDATE`, b.TenantID, fingerprint, e.OccurrenceID).Scan(&raw)
		var old *Finding
		if err == nil {
			var previous Finding
			if json.Unmarshal(raw, &previous) != nil {
				return ErrInvalid
			}
			old = &previous
			if previous.SourceSequence > 0 && previous.SourceSequence == e.SourceSequence && previous.Digest != digest {
				return ErrConflict
			}
			if !previous.StartsAt.Equal(e.StartsAt) || previous.RuleFamily != e.RuleFamily || previous.ResourceCanonicalID != e.ResourceCanonicalID {
				return ErrConflict
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var received time.Time
		if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&received); err != nil {
			return err
		}
		state, changed := Reduce(old, e)
		if old == nil {
			f = Finding{Envelope: e, FindingID: uuid.Must(uuid.NewV7()).String(), SourceFingerprint: fingerprint, AggregateRevision: 1, ReceivedAt: received, FirstReceivedAt: received, SourceRevision: b.RegistrationRevision, Digest: digest}
		} else {
			f = *old
			if changed {
				f.Envelope = e
				f.State = state
				f.AggregateRevision++
				f.ReceivedAt = received
				f.SourceRevision = b.RegistrationRevision
				f.Digest = digest
			} else {
				disposition = Stale
			}
		}
		payload, _ := json.Marshal(f)
		if changed {
			if old == nil {
				_, err = tx.Exec(ctx, `INSERT INTO finding.records(tenant_id,finding_id,source_id,cluster_id,schema_version,event_id,payload,observed_at,received_at,source_fingerprint,occurrence_id,aggregate_revision,lifecycle_state,resource_canonical_id,namespace,cluster_uid,source_sequence,semantic_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`, b.TenantID, f.FindingID, b.SourceID, b.ClusterID, e.SchemaVersion, e.EventID, payload, e.ObservedAt, received, fingerprint, e.OccurrenceID, f.AggregateRevision, f.State, e.ResourceCanonicalID, e.Namespace, b.ClusterUID, e.SourceSequence, digest)
			} else {
				_, err = tx.Exec(ctx, `UPDATE finding.records SET payload=$3,observed_at=$4,received_at=$5,aggregate_revision=$6,lifecycle_state=$7,source_sequence=$8,semantic_digest=$9 WHERE tenant_id=$1 AND finding_id=$2`, b.TenantID, f.FindingID, payload, e.ObservedAt, received, f.AggregateRevision, f.State, e.SourceSequence, digest)
			}
			if err != nil {
				return err
			}
		}
		if err := retainEvidence(ctx, tx, b.TenantID, f.FindingID, refs); err != nil {
			return err
		}
		if err := bindTransport(ctx, tx, b, e, digest, f.FindingID); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO finding.inbox(tenant_id,event_id,source_id,request_digest,state,finding_id,received_at) VALUES($1,$2,$3,$4,'accepted',$5,$6)`, b.TenantID, e.EventID, b.SourceID, digest, f.FindingID, received)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO finding.timeline(tenant_id,finding_id,event_id,source_id,disposition,aggregate_revision,digest,observed_at,received_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, b.TenantID, f.FindingID, e.EventID, b.SourceID, disposition, f.AggregateRevision, digest, e.ObservedAt, received)
		if err != nil {
			return err
		}
		if changed {
			_, err = tx.Exec(ctx, `INSERT INTO finding.outbox(tenant_id,outbox_id,finding_id,event_type,schema_version,payload,aggregate_revision) VALUES($1,$2,$3,'finding.changed','finding/v2',$4,$5)`, b.TenantID, uuid.Must(uuid.NewV7()), f.FindingID, payload, f.AggregateRevision)
		}
		return err
	})
	if errors.Is(err, ErrConflict) {
		if auditErr := s.reject(ctx, b, e, err); auditErr != nil {
			return f, "", auditErr
		}
	}
	return f, disposition, err
}

// Conflict receipts live outside the rolled-back mutation transaction. Only a
// currently trusted binding may create them, and no raw payload is retained.
func (s Service) reject(ctx context.Context, b source.BoundSourceContext, e Envelope, cause error) error {
	return persistence.WithTenantTx(ctx, s.Pool, b.TenantID, func(tx pgx.Tx) error {
		if err := CheckBound(ctx, tx, b, e.Namespace, e.SchemaVersion); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO finding.rejections(tenant_id,source_id,digest,error_code) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, b.TenantID, b.SourceID, Hash([]string{e.EventID, SemanticDigest(e), cause.Error()}), cause.Error())
		return err
	})
}
func bindTransport(ctx context.Context, tx pgx.Tx, b source.BoundSourceContext, e Envelope, digest, id string) error {
	for _, key := range []struct{ kind, value string }{{"event", e.EventID}, {"idempotency", e.IdempotencyKey}} {
		if _, err := tx.Exec(ctx, `INSERT INTO finding.transport_keys VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, b.TenantID, b.SourceID, key.kind, key.value, digest, id); err != nil {
			return err
		}
	}
	return nil
}
func Load(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, id string) (Finding, error) {
	var raw []byte
	var f Finding
	err := tx.QueryRow(ctx, `SELECT payload FROM finding.records WHERE tenant_id=$1 AND finding_id=$2`, tenant, id).Scan(&raw)
	if err == nil {
		err = json.Unmarshal(raw, &f)
	}
	return f, err
}
func CheckBound(ctx context.Context, tx pgx.Tx, b source.BoundSourceContext, ns, schema string) error {
	var valid bool
	err := tx.QueryRow(ctx, `SELECT platform.sp05_lock_source_binding($1,$2,$3,$4,$5,$6,$7,$8)`, b.TenantID, b.SourceID, b.RegistrationRevision, b.CredentialRevision, b.ClusterID, b.ClusterUID, ns, schema).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrUnauthorized
	}
	return nil
}
