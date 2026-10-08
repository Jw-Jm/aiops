package evidence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/archive"
	"ops-platform/internal/audit"
	"ops-platform/internal/contract"
	platformcrypto "ops-platform/internal/crypto"
	"ops-platform/internal/graph"
	"ops-platform/internal/persistence"
	"ops-platform/internal/resource"
	"time"
)

type ArchiveRef struct {
	BackendLogicalID     string            `json:"backendLogicalId"`
	Object               archive.ObjectRef `json:"object"`
	EncryptionKeyVersion string            `json:"encryptionKeyVersion"`
	PlaintextDigest      string            `json:"plaintextDigest"`
	CiphertextDigest     string            `json:"ciphertextDigest"`
}
type ArchiveService struct {
	Pool             persistence.TxBeginner
	Store            *archive.Store
	Protector        platformcrypto.Protector
	BackendLogicalID string
}

func validateCapture(e Evidence, namespace string, retain time.Time) error {
	tenant, err := uuid.Parse(e.TenantID)
	if err != nil || tenant == uuid.Nil {
		return ErrArgument
	}
	id, err := uuid.Parse(e.EvidenceID)
	if err != nil || id == uuid.Nil {
		return ErrArgument
	}
	source, err := uuid.Parse(e.SourceRegistrationID)
	if err != nil || source == uuid.Nil {
		return ErrArgument
	}
	canonical, err := resource.ParseCanonicalID(e.ResourceCanonicalID)
	if err != nil || canonical.Tenant != e.TenantID || !e.EffectiveScope.Allows(e.ResourceCanonicalID, namespace) || e.EffectiveScope.AuthorizationRevision == "" {
		return ErrArgument
	}
	if e.SchemaVersion != "evidence/v2" || e.SourceRevision < 1 || e.SourceSystem == "" || e.BackendLogicalID == "" || e.QueryTemplateVersion == "" || e.QueryHash == "" || e.EvaluatedAt.IsZero() || e.EvaluatedAt.After(time.Now().Add(time.Minute)) || e.ObservedFrom.IsZero() || e.ObservedTo.Before(e.ObservedFrom) || e.SourceRetentionUntil.IsZero() || len(e.Data) == 0 || len(e.Data) > 64<<10 || Digest(e.Data) != e.ContentDigest || retain.Before(e.EvaluatedAt.Add(180*24*time.Hour)) {
		return ErrArgument
	}
	e.ReplayState = "archive_pending"
	e.ArchiveRef = nil
	e.FactSlice = nil
	if e.DerivationEvidenceRefs == nil {
		e.DerivationEvidenceRefs = []string{}
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return ErrArgument
	}
	if err := contract.Validate("https://ops.local/schemas/evidence/v2", raw); err != nil {
		return err
	}
	return nil
}

func (s *ArchiveService) Capture(ctx context.Context, e Evidence, namespace string, retain time.Time) error {
	if err := s.Prepare(ctx, e, namespace, retain); err != nil {
		return err
	}
	return s.Recover(ctx, uuid.MustParse(e.TenantID), uuid.MustParse(e.EvidenceID))
}

// Prepare commits the pending intent and Transit envelope before collection is
// acknowledged. External object upload may then run asynchronously; a restart
// recovers the durable ciphertext without requiring the vanished source fact.
func (s *ArchiveService) CaptureQuery(ctx context.Context, e Evidence, namespace string, retain time.Time, q Query) error {
	if err := s.prepare(ctx, e, namespace, retain, &q); err != nil {
		return err
	}
	return s.Recover(ctx, uuid.MustParse(e.TenantID), uuid.MustParse(e.EvidenceID))
}
func (s *ArchiveService) Prepare(ctx context.Context, e Evidence, namespace string, retain time.Time) error {
	return s.prepare(ctx, e, namespace, retain, nil)
}
func (s *ArchiveService) prepare(ctx context.Context, e Evidence, namespace string, retain time.Time, q *Query) error {
	if err := validateCapture(e, namespace, retain); err != nil {
		return err
	}
	tenant, err := uuid.Parse(e.TenantID)
	if err != nil {
		return err
	}
	id, err := uuid.Parse(e.EvidenceID)
	if err != nil {
		return err
	}
	if s.Pool == nil || s.Store == nil || s.Protector == nil || s.BackendLogicalID == "" {
		return ErrArgument
	}
	e.ReplayState = "archive_pending"
	e.FactSlice = nil
	e.ArchiveRef = nil
	if e.DerivationEvidenceRefs == nil {
		e.DerivationEvidenceRefs = []string{}
	}
	var metadata []byte
	var queryArgs []byte
	if q != nil {
		if q.ResourceCanonicalID != e.ResourceCanonicalID || q.Namespace != namespace || graph.ScopeDigest(q.Scope) != graph.ScopeDigest(e.EffectiveScope) || q.From.After(e.ObservedFrom) || (q.To.Before(e.ObservedTo) && !(e.SourceSystem == "redfish" && !e.ObservedTo.After(q.To.Add(3*time.Second)))) || q.Limit < 1 || q.Limit > 200 || !q.To.After(q.From) || q.To.Sub(q.From) > time.Hour {
			return ErrArgument
		}
		queryArgs, _ = json.Marshal(q)
	}
	// Intent precedes encryption/upload. No raw fact slice is stored in PostgreSQL.
	err = persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
		if err := retentionLock(ctx, tx, tenant); err != nil {
			return err
		}
		scopeDigest, err := evidenceSourceDigest(ctx, tx, e)
		if err != nil {
			return err
		}
		if e.SourceScopeDigest == "" || e.SourceScopeDigest != scopeDigest {
			return ErrScopeUnverified
		}
		metadata, _ = json.Marshal(e)
		tag, err := tx.Exec(ctx, `INSERT INTO platform.evidence_metadata(tenant_id,evidence_id,source_id,canonical_id,namespace,metadata,content_digest,replay_state,retain_until) VALUES($1,$2,$3,$4,$5,$6,$7,'archive_pending',$8) ON CONFLICT(tenant_id,evidence_id) DO NOTHING`, tenant, id, e.SourceRegistrationID, e.ResourceCanonicalID, namespace, metadata, e.ContentDigest, retain)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 1 {
			canonical, _ := resource.ParseCanonicalID(e.ResourceCanonicalID)
			var valid bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform.source_registrations s JOIN platform.tenants t USING(tenant_id) LEFT JOIN platform.cluster_registrations c USING(tenant_id,cluster_id) WHERE s.tenant_id=$1 AND s.source_id=$2 AND s.revision=$3 AND s.status='active' AND t.status='active' AND s.source_type=$4 AND s.backend_logical_id=$5 AND ((c.status='active' AND c.cluster_uid=$6) OR (c.cluster_id IS NULL AND s.data_scope_mapping->'scopes'->'cluster' @> jsonb_build_array($6::text))))`, tenant, e.SourceRegistrationID, e.SourceRevision, e.SourceSystem, e.BackendLogicalID, canonical.Scope).Scan(&valid); err != nil {
				return err
			}
			if !valid {
				return ErrScopeUnverified
			}
		}
		var digest string
		var storedMetadata []byte
		var deleting bool
		var previousNamespace string
		if err := tx.QueryRow(ctx, `SELECT content_digest,metadata,deleting,namespace FROM platform.evidence_metadata WHERE tenant_id=$1 AND evidence_id=$2 FOR NO KEY UPDATE`, tenant, id).Scan(&digest, &storedMetadata, &deleting, &previousNamespace); err != nil {
			return err
		}
		var previous Evidence
		if json.Unmarshal(storedMetadata, &previous) != nil {
			return errors.New("invalid evidence metadata")
		}
		// Two Workers can commit the first observation of one immutable
		// Kubernetes UID/RV simultaneously. Only the fixed collector template
		// may reuse the winning durable observation; all identity/content/scope
		// checks below still apply, and caller queries are never normalized.
		if q == nil && e.Type == "resource_state" && previous.Type == "resource_state" && e.SourceSystem == "kubernetes" && previous.SourceSystem == "kubernetes" && e.QueryTemplateVersion == "kubernetes-projection/v1" && previous.QueryTemplateVersion == e.QueryTemplateVersion {
			e.EvaluatedAt = previous.EvaluatedAt
			e.ObservedFrom = previous.ObservedFrom
			e.ObservedTo = previous.ObservedTo
			e.SourceRetentionUntil = previous.SourceRetentionUntil
		}
		if deleting || digest != e.ContentDigest || previous.ResourceCanonicalID != e.ResourceCanonicalID || previous.SourceRegistrationID != e.SourceRegistrationID || previous.SourceRevision != e.SourceRevision || previous.SourceScopeDigest != e.SourceScopeDigest || previous.QueryHash != e.QueryHash || previous.QueryTemplateVersion != e.QueryTemplateVersion || previous.BackendLogicalID != e.BackendLogicalID || previous.EffectiveScope.AuthorizationRevision != e.EffectiveScope.AuthorizationRevision || graph.ScopeDigest(previous.EffectiveScope) != graph.ScopeDigest(e.EffectiveScope) || previousNamespace != namespace || previous.Type != e.Type || previous.DataClass != e.DataClass || !previous.ObservedFrom.Equal(e.ObservedFrom) || !previous.ObservedTo.Equal(e.ObservedTo) || previous.IndependenceGroup != e.IndependenceGroup || graph.ScopeDigest(previous.DerivationEvidenceRefs) != graph.ScopeDigest(e.DerivationEvidenceRefs) {
			return errors.New("IDEMPOTENCY_CONFLICT")
		}
		_, err = tx.Exec(ctx, `INSERT INTO platform.evidence_archive_intents(tenant_id,evidence_id,object_id,backend_logical_id,status,query_args) VALUES($1,$2,$2,$3,'pending',$4) ON CONFLICT(tenant_id,evidence_id) DO NOTHING`, tenant, id, s.BackendLogicalID, queryArgs)
		if err != nil {
			return err
		}
		if q != nil {
			var previousArgs []byte
			if err := tx.QueryRow(ctx, `SELECT query_args FROM platform.evidence_archive_intents WHERE tenant_id=$1 AND evidence_id=$2`, tenant, id).Scan(&previousArgs); err != nil {
				return err
			}
			if len(previousArgs) > 0 {
				var original Query
				if json.Unmarshal(previousArgs, &original) != nil || Digest(previousArgs) != Digest(queryArgs) {
					// PostgreSQL jsonb reorders object keys; compare normalized typed arguments.
					originalArgs, _ := json.Marshal(original)
					if Digest(originalArgs) != Digest(queryArgs) {
						return errors.New("IDEMPOTENCY_CONFLICT")
					}
				}
			}
		}

		if err := retentionLock(ctx, tx, tenant); err != nil {
			return err
		}
		for _, dependency := range e.DerivationEvidenceRefs {
			dependencyID, err := uuid.Parse(dependency)
			if err != nil {
				return ErrArgument
			}
			var deleting bool
			if err := tx.QueryRow(ctx, `SELECT deleting FROM platform.evidence_metadata WHERE tenant_id=$1 AND evidence_id=$2 FOR NO KEY UPDATE`, tenant, dependencyID).Scan(&deleting); err != nil {
				return err
			}
			if deleting {
				return ErrArgument
			}
			if _, err := tx.Exec(ctx, `INSERT INTO platform.evidence_dependencies(tenant_id,referrer_id,dependency_id) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, tenant, id, dependencyID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	envelope, err := s.Protector.Seal(ctx, tenant, id, e.Data)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	err = persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE platform.evidence_archive_intents SET envelope_bytes=$3,ciphertext_digest=$4,encryption_key_version=$5 WHERE tenant_id=$1 AND evidence_id=$2 AND status='pending' AND envelope_bytes IS NULL`, tenant, id, encoded, Digest(encoded), envelope.KeyVersion)
		return err
	})
	if err != nil {
		return err
	}
	return nil
}
func (s *ArchiveService) Recover(ctx context.Context, tenant, id uuid.UUID) error {
	var encoded []byte
	var plaintextDigest, ciphertextDigest, backend, status string
	var retain time.Time
	err := persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT i.envelope_bytes,m.content_digest,i.ciphertext_digest,i.backend_logical_id,i.status,m.retain_until FROM platform.evidence_archive_intents i JOIN platform.evidence_metadata m USING(tenant_id,evidence_id) WHERE i.tenant_id=$1 AND i.evidence_id=$2 AND NOT m.deleting`, tenant, id).Scan(&encoded, &plaintextDigest, &ciphertextDigest, &backend, &status, &retain)
	})
	if err != nil {
		return err
	}
	if status == "verified" {
		_, _, err := s.Read(ctx, tenant, id)
		return err
	}
	if status != "pending" || len(encoded) == 0 || backend != s.BackendLogicalID {
		return errors.New("archive unavailable: pending encryption or backend mismatch")
	}
	if Digest(encoded) != ciphertextDigest {
		return archive.ErrDigestMismatch
	}
	var envelope platformcrypto.Envelope
	if json.Unmarshal(encoded, &envelope) != nil {
		return archive.ErrInvalidObject
	}
	plain, err := s.Protector.Open(ctx, tenant, id, envelope)
	if err != nil || Digest(plain) != plaintextDigest {
		return archive.ErrDigestMismatch
	}
	ref, found, err := s.Store.Find(ctx, tenant, id, "evidence")
	if err != nil {
		return err
	}
	if !found {
		retain = maxTime(retain, time.Now().Add(366*24*time.Hour))
		ref, err = s.Store.Put(ctx, archive.ObjectDescriptor{TenantID: tenant, ObjectID: id, Category: "evidence", ContentType: "application/vnd.ops.transit-envelope+json", ExpectedDigest: ciphertextDigest, RetainUntil: retain}, bytes.NewReader(encoded))
		if err != nil {
			return err
		}
	}
	if ref.VersionID == "" || ref.Digest != ciphertextDigest {
		return archive.ErrInvalidObject
	}
	stored, err := s.Store.Get(ctx, tenant, ref)
	if err != nil || !bytes.Equal(stored, encoded) {
		return archive.ErrDigestMismatch
	}
	var storedEnvelope platformcrypto.Envelope
	if json.Unmarshal(stored, &storedEnvelope) != nil {
		return archive.ErrInvalidObject
	}
	verified, err := s.Protector.Open(ctx, tenant, id, storedEnvelope)
	if err != nil || Digest(verified) != plaintextDigest {
		return archive.ErrDigestMismatch
	}
	object := ArchiveRef{backend, ref, envelope.KeyVersion, plaintextDigest, ciphertextDigest}
	raw, _ := json.Marshal(object)
	return persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
		if err := retentionLock(ctx, tx, tenant); err != nil {
			return err
		}
		var digest, current string
		var deleting bool
		if err := tx.QueryRow(ctx, `SELECT content_digest,replay_state,deleting FROM platform.evidence_metadata WHERE tenant_id=$1 AND evidence_id=$2 FOR NO KEY UPDATE`, tenant, id).Scan(&digest, &current, &deleting); err != nil {
			return err
		}
		if deleting || digest != plaintextDigest {
			return archive.ErrInvalidObject
		}
		if current == "archived_verified" {
			return nil
		}
		tag, err := tx.Exec(ctx, `UPDATE platform.evidence_archive_intents SET status='verified',object_ref=$3,verified_at=clock_timestamp(),last_error=NULL WHERE tenant_id=$1 AND evidence_id=$2 AND status='pending' AND ciphertext_digest=$4`, tenant, id, raw, ciphertextDigest)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return errors.New("archive intent changed")
		}
		_, err = tx.Exec(ctx, `UPDATE platform.evidence_metadata SET replay_state='archived_verified' WHERE tenant_id=$1 AND evidence_id=$2`, tenant, id)
		if err != nil {
			return err
		}
		recordID := uuid.Must(uuid.NewV7())
		_, err = audit.Append(ctx, tx, audit.Entry{TenantID: tenant, RecordID: recordID, EntityID: id, EntityKind: "evidence", EventType: "evidence.archived_verified", Subject: "platform-worker", Payload: map[string]any{"backendLogicalId": backend, "contentDigest": plaintextDigest, "ciphertextDigest": ciphertextDigest, "keyVersion": envelope.KeyVersion}})
		if err != nil {
			return err
		}
		return Protect(ctx, tx, tenant, id, recordID, "audit", time.Now().Add(365*24*time.Hour), false)
	})
}
func (s *ArchiveService) Read(ctx context.Context, tenant, id uuid.UUID) ([]byte, ArchiveRef, error) {
	var raw []byte
	err := persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT i.object_ref FROM platform.evidence_archive_intents i JOIN platform.evidence_metadata m USING(tenant_id,evidence_id) WHERE i.tenant_id=$1 AND i.evidence_id=$2 AND i.status='verified' AND m.replay_state='archived_verified' AND NOT m.deleting`, tenant, id).Scan(&raw)
	})
	if err != nil {
		return nil, ArchiveRef{}, err
	}
	var ref ArchiveRef
	if json.Unmarshal(raw, &ref) != nil || ref.BackendLogicalID != s.BackendLogicalID || ref.Object.VersionID == "" || ref.EncryptionKeyVersion == "" {
		return nil, ref, archive.ErrInvalidObject
	}
	stored, err := s.Store.Get(ctx, tenant, ref.Object)
	if err != nil {
		return nil, ref, err
	}
	if Digest(stored) != ref.CiphertextDigest {
		return nil, ref, archive.ErrDigestMismatch
	}
	var envelope platformcrypto.Envelope
	if json.Unmarshal(stored, &envelope) != nil || envelope.KeyVersion != ref.EncryptionKeyVersion {
		return nil, ref, archive.ErrInvalidObject
	}
	plain, err := s.Protector.Open(ctx, tenant, id, envelope)
	if err != nil || Digest(plain) != ref.PlaintextDigest {
		return nil, ref, archive.ErrDigestMismatch
	}
	return plain, ref, nil
}
func (s *ArchiveService) RecoverTenant(ctx context.Context, tenant uuid.UUID) error {
	ids := []uuid.UUID{}
	err := persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT evidence_id FROM platform.evidence_archive_intents WHERE tenant_id=$1 AND status='pending' AND envelope_bytes IS NOT NULL ORDER BY evidence_id LIMIT 100`, tenant)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	})
	if err != nil {
		return err
	}
	var last error
	for _, id := range ids {
		if err := s.Recover(ctx, tenant, id); err != nil {
			last = err
			_ = persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
				_, e := tx.Exec(ctx, `UPDATE platform.evidence_archive_intents SET attempts=attempts+1,last_error='recovery_unavailable' WHERE tenant_id=$1 AND evidence_id=$2 AND status='pending'`, tenant, id)
				return e
			})
		}
	}
	return last
}

func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
