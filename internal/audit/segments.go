package audit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/archive"
	"ops-platform/internal/bundle"
	"ops-platform/internal/crypto"
	"ops-platform/internal/integrations/openbao"
	"ops-platform/internal/persistence"
)

const maxSegmentRecords = 10000

type SegmentArchive interface {
	Put(context.Context, archive.ObjectDescriptor, io.Reader) (archive.ObjectRef, error)
	Get(context.Context, uuid.UUID, archive.ObjectRef) ([]byte, error)
	Find(context.Context, uuid.UUID, uuid.UUID, string) (archive.ObjectRef, bool, error)
}

type SegmentProtector interface {
	Seal(context.Context, uuid.UUID, uuid.UUID, []byte) (crypto.Envelope, error)
	Open(context.Context, uuid.UUID, uuid.UUID, crypto.Envelope) ([]byte, error)
}

type SegmentSigner interface {
	TransitSign(context.Context, string, []byte) (openbao.TransitSignature, error)
	TransitVerify(context.Context, string, []byte, string) error
}

type SegmentService struct {
	pool      persistence.TxBeginner
	archive   SegmentArchive
	protector SegmentProtector
	signer    SegmentSigner
	key       string
	retention time.Duration
	now       func() time.Time
}

func NewSegmentService(pool persistence.TxBeginner, archiveStore SegmentArchive, protector SegmentProtector, signer SegmentSigner, signingKey string, retention time.Duration) (*SegmentService, error) {
	if pool == nil || archiveStore == nil || protector == nil || signer == nil || signingKey == "" || retention <= 0 {
		return nil, errors.New("audit segment dependencies and signing key are required")
	}
	return &SegmentService{pool: pool, archive: archiveStore, protector: protector, signer: signer, key: signingKey, retention: retention, now: time.Now}, nil
}

type segmentDescriptor struct {
	FormatVersion string
	PreviousID    *uuid.UUID
	TenantID      uuid.UUID
	ID            uuid.UUID
	First         int64
	Last          int64
	Count         int
	Root          string
	Status        string
	Ref           archive.ObjectRef
	Digest        string
	Sig           string
	KeyVer        string
}

type recordRow struct {
	seq       int64
	Entry     Entry
	CreatedAt time.Time
	Digest    []byte
	Canonical []byte
}

// SealNext persists a bounded immutable segment, then stores its encrypted
// archive and Transit signature. A pending segment is resumed after a crash.
func (s *SegmentService) SealNext(ctx context.Context, tenantID uuid.UUID) (uuid.UUID, bool, error) {
	if tenantID == uuid.Nil {
		return uuid.Nil, false, errors.New("audit tenant is required")
	}
	var segment segmentDescriptor
	var found bool
	err := persistence.WithTenantTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		var err error
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(770037, hashtext($1))", tenantID.String()); err != nil {
			return fmt.Errorf("lock audit segment boundary: %w", err)
		}
		segment, found, err = loadPendingSegment(ctx, tx, tenantID)
		if err != nil || found {
			return err
		}
		var previous *int64
		if err := tx.QueryRow(ctx, "SELECT max(last_audit_seq) FROM audit.signed_segments WHERE tenant_id = $1 AND status = 'signed'", tenantID).Scan(&previous); err != nil {
			return fmt.Errorf("read last signed audit sequence: %w", err)
		}
		rows, err := readAuditRows(ctx, tx, tenantID, previous, maxSegmentRecords+1)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		if len(rows) < maxSegmentRecords && rows[0].CreatedAt.After(s.now().Add(-5*time.Minute)) {
			return nil
		}
		if len(rows) > maxSegmentRecords {
			rows = rows[:maxSegmentRecords]
		}
		oldest, newest := rows[0].CreatedAt, rows[0].CreatedAt
		for index, row := range rows {
			if row.CreatedAt.Before(oldest) {
				oldest = row.CreatedAt
			}
			if row.CreatedAt.After(newest) {
				newest = row.CreatedAt
			}
			if newest.Sub(oldest) > 5*time.Minute {
				rows = rows[:index]
				break
			}
		}
		id, err := uuid.NewV7()
		if err != nil {
			return errors.New("audit segment identity unavailable")
		}
		segment = segmentDescriptor{TenantID: tenantID, ID: id, First: rows[0].EntrySeq(), Last: rows[len(rows)-1].EntrySeq(), Count: len(rows), Root: formatMerkleRoot(MerkleRoot(canonicalRows(rows))), Status: "pending_signature"}
		if _, err := tx.Exec(ctx, "SELECT audit.begin_signed_segment($1,$2,$3,$4,$5,$6)", tenantID, segment.ID, segment.First, segment.Last, segment.Count, segment.Root); err != nil {
			return fmt.Errorf("create pending signed audit segment: %w", err)
		}
		found = true
		return nil
	})
	if err != nil || !found {
		return uuid.Nil, false, err
	}
	segment, err = s.loadSegmentRows(ctx, tenantID, segment.ID)
	if err != nil {
		return segment.ID, false, err
	}
	if segment.Status == "signed" {
		return segment.ID, true, nil
	}
	plain, err := s.segmentPlaintext(ctx, segment)
	if err != nil {
		return segment.ID, false, err
	}
	ref, exists, err := s.archive.Find(ctx, tenantID, segment.ID, "audit-segment")
	if err != nil {
		return segment.ID, false, fmt.Errorf("inspect pending audit archive: %w", err)
	}
	if exists {
		sealedBytes, err := s.archive.Get(ctx, tenantID, ref)
		if err != nil {
			return segment.ID, false, fmt.Errorf("read pending audit archive: %w", err)
		}
		var envelope crypto.Envelope
		if err := json.Unmarshal(sealedBytes, &envelope); err != nil {
			return segment.ID, false, errors.New("pending audit archive envelope is invalid")
		}
		storedPlain, err := s.protector.Open(ctx, tenantID, segment.ID, envelope)
		if err != nil || !bytes.Equal(storedPlain, plain) {
			return segment.ID, false, errors.New("pending audit archive differs from immutable segment records")
		}
	} else {
		envelope, err := s.protector.Seal(ctx, tenantID, segment.ID, plain)
		if err != nil {
			return segment.ID, false, errors.New("audit segment encryption unavailable")
		}
		archiveBytes, err := json.Marshal(envelope)
		if err != nil {
			return segment.ID, false, err
		}
		ref, err = s.archive.Put(ctx, archive.ObjectDescriptor{
			TenantID: tenantID, ObjectID: segment.ID, Category: "audit-segment",
			ContentType: "application/json", RetainUntil: s.now().Add(s.retention).UTC(),
		}, bytes.NewReader(archiveBytes))
		if err != nil {
			return segment.ID, false, fmt.Errorf("archive encrypted audit segment: %w", err)
		}
	}
	manifest, err := manifestBytes(segment, ref)
	if err != nil {
		return segment.ID, false, err
	}
	signature, err := s.sealProof(ctx, segment, manifest)
	if err != nil {
		return segment.ID, false, err
	}
	keyVersion := signature.KeyVersion
	if err := persistence.WithTenantTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		refJSON, err := json.Marshal(ref)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, "SELECT audit.finish_signed_segment($1,$2,$3,$4,$5::jsonb,$6)", tenantID, segment.ID, signature.Signature, keyVersion, refJSON, ref.Digest)
		return err
	}); err != nil {
		return segment.ID, false, fmt.Errorf("commit signed audit segment: %w", err)
	}
	return segment.ID, true, nil
}

// VerifyRange verifies signed coverage for all tenant records whose global
// audit sequence falls in [first,last], including the complete archived bytes
// for every overlapping segment. Both boundaries must identify tenant records;
// missing boundaries or legacy manifests without predecessor proofs fail closed.
func (s *SegmentService) VerifyRange(ctx context.Context, tenantID uuid.UUID, first, last int64) error {
	if tenantID == uuid.Nil || first <= 0 || last < first {
		return errors.New("invalid audit verification range")
	}
	var segments []segmentDescriptor
	var records []recordRow
	if err := persistence.WithTenantTx(ctx, s.pool, tenantID, func(tx pgx.Tx) error {
		var err error
		segments, err = listSegments(ctx, tx, tenantID, first, last)
		if err != nil {
			return err
		}
		records, err = readAuditRange(ctx, tx, tenantID, first, last)
		return err
	}); err != nil {
		return err
	}
	if len(records) == 0 || len(segments) == 0 || records[0].EntrySeq() != first || records[len(records)-1].EntrySeq() != last {
		return errors.New("audit range has missing tenant-record boundaries or signed coverage")
	}
	verified := map[uuid.UUID]bool{}
	for _, segment := range segments {
		if segment.Status != "signed" {
			return errors.New("audit range contains an unsigned segment")
		}
		if err := s.verifySegment(ctx, tenantID, segment); err != nil {
			return err
		}
		if err := s.verifyPredecessors(ctx, tenantID, segment, verified); err != nil {
			return err
		}
	}
	for _, row := range records {
		covered := false
		for _, segment := range segments {
			if row.EntrySeq() >= segment.First && row.EntrySeq() <= segment.Last {
				covered = true
				break
			}
		}
		if !covered {
			return fmt.Errorf("audit sequence %d is not covered by a signed segment", row.EntrySeq())
		}
	}
	return nil
}

// A predecessor is covered by the Transit signature, so removing a complete
// middle segment or rewriting its link cannot hide a gap between valid roots.
func (s *SegmentService) verifyPredecessors(ctx context.Context, tenant uuid.UUID, segment segmentDescriptor, verified map[uuid.UUID]bool) error {
	visited := map[uuid.UUID]bool{}
	for {
		if segment.FormatVersion != "audit-segment/v2" {
			return errors.New("legacy audit manifest has no signed predecessor proof")
		}
		if verified[segment.ID] {
			return nil
		}
		if visited[segment.ID] {
			return errors.New("audit predecessor cycle")
		}
		visited[segment.ID] = true
		if segment.PreviousID == nil {
			break
		}
		var previous segmentDescriptor
		if err := persistence.WithTenantTx(ctx, s.pool, tenant, func(tx pgx.Tx) error {
			var err error
			previous, err = loadSegment(ctx, tx, tenant, *segment.PreviousID)
			return err
		}); err != nil {
			return errors.New("audit signed predecessor is missing")
		}
		if previous.Status != "signed" || previous.Last >= segment.First {
			return errors.New("audit predecessor range is invalid")
		}
		manifest, err := manifestBytes(previous, previous.Ref)
		if err != nil {
			return err
		}
		if err := s.signer.TransitVerify(ctx, s.key, manifest, previous.Sig); err != nil {
			return errors.New("audit predecessor signature verification failed")
		}
		segment = previous
	}
	for id := range visited {
		verified[id] = true
	}
	return nil
}

type archivedProof struct {
	Manifest   json.RawMessage `json:"manifest"`
	Signature  string          `json:"signature"`
	KeyVersion string          `json:"keyVersion"`
}

func (s *SegmentService) readProof(ctx context.Context, segment segmentDescriptor, manifest []byte) (openbao.TransitSignature, bool, error) {
	ref, exists, err := s.archive.Find(ctx, segment.TenantID, segment.ID, "audit-proof")
	if err != nil || !exists {
		return openbao.TransitSignature{}, exists, err
	}
	encoded, err := s.archive.Get(ctx, segment.TenantID, ref)
	if err != nil {
		return openbao.TransitSignature{}, true, err
	}
	if _, err := bundle.CanonicalizeJSON(encoded); err != nil {
		return openbao.TransitSignature{}, true, errors.New("audit proof JSON is invalid")
	}
	var proof archivedProof
	if json.Unmarshal(encoded, &proof) != nil {
		return openbao.TransitSignature{}, true, errors.New("audit proof is invalid")
	}
	canonical, err := bundle.CanonicalizeJSON(proof.Manifest)
	version, versionErr := openbao.TransitVersion(proof.Signature)
	if err != nil || versionErr != nil || version != proof.KeyVersion || !bytes.Equal(canonical, manifest) {
		return openbao.TransitSignature{}, true, errors.New("archived audit proof differs from signed metadata")
	}
	if err := s.signer.TransitVerify(ctx, s.key, manifest, proof.Signature); err != nil {
		return openbao.TransitSignature{}, true, errors.New("archived audit proof signature is invalid")
	}
	return openbao.TransitSignature{Signature: proof.Signature, KeyVersion: proof.KeyVersion}, true, nil
}

func (s *SegmentService) sealProof(ctx context.Context, segment segmentDescriptor, manifest []byte) (openbao.TransitSignature, error) {
	if proof, exists, err := s.readProof(ctx, segment, manifest); err != nil {
		return proof, err
	} else if exists {
		return proof, nil
	}
	signature, err := s.signer.TransitSign(ctx, s.key, manifest)
	if err != nil {
		return signature, errors.New("audit segment signing unavailable")
	}
	version, err := openbao.TransitVersion(signature.Signature)
	if err != nil || version != signature.KeyVersion {
		return signature, errors.New("audit segment signer returned an invalid key version")
	}
	if err := s.signer.TransitVerify(ctx, s.key, manifest, signature.Signature); err != nil {
		return signature, errors.New("audit segment signature self-verification failed")
	}
	encoded, err := json.Marshal(archivedProof{manifest, signature.Signature, signature.KeyVersion})
	if err != nil {
		return signature, err
	}
	_, err = s.archive.Put(ctx, archive.ObjectDescriptor{TenantID: segment.TenantID, ObjectID: segment.ID, Category: "audit-proof", ContentType: "application/json", RetainUntil: s.now().Add(s.retention).UTC()}, bytes.NewReader(encoded))
	if err != nil {
		return signature, errors.New("signed audit proof archive unavailable")
	}
	return signature, nil
}

func (s *SegmentService) verifySegment(ctx context.Context, tenant uuid.UUID, segment segmentDescriptor) error {
	manifest, err := manifestBytes(segment, segment.Ref)
	if err != nil {
		return err
	}
	if err := s.signer.TransitVerify(ctx, s.key, manifest, segment.Sig); err != nil {
		return errors.New("audit segment signature verification failed")
	}
	if segment.FormatVersion == "audit-segment/v2" {
		proof, exists, err := s.readProof(ctx, segment, manifest)
		if err != nil || !exists || proof.Signature != segment.Sig || proof.KeyVersion != segment.KeyVer {
			return errors.New("signed audit proof archive is missing or inconsistent")
		}
	}
	if segment.Ref.Digest != segment.Digest || segment.Ref.ObjectID != segment.ID || segment.Ref.TenantID != tenant {
		return errors.New("audit segment archive reference mismatch")
	}
	sealedBytes, err := s.archive.Get(ctx, tenant, segment.Ref)
	if err != nil {
		return fmt.Errorf("read signed audit segment archive: %w", err)
	}
	var envelope crypto.Envelope
	if err := json.Unmarshal(sealedBytes, &envelope); err != nil {
		return errors.New("audit segment encrypted envelope is invalid")
	}
	plain, err := s.protector.Open(ctx, tenant, segment.ID, envelope)
	if err != nil {
		return errors.New("audit segment decryption failed")
	}
	var dbRows []recordRow
	if err := persistence.WithTenantTx(ctx, s.pool, tenant, func(tx pgx.Tx) error {
		var err error
		dbRows, err = readAuditRowsRange(ctx, tx, tenant, segment.First, segment.Last)
		return err
	}); err != nil {
		return err
	}
	if len(dbRows) != segment.Count || len(bytes.Split(plain, []byte{'\n'}))-1 != segment.Count {
		return errors.New("audit segment record count mismatch")
	}
	if formatMerkleRoot(MerkleRoot(canonicalRows(dbRows))) != segment.Root {
		return errors.New("audit segment Merkle root mismatch")
	}
	if !bytes.Equal(plain, ndjson(dbRows)) {
		return errors.New("audit segment records differ from the database")
	}
	return nil
}

func (s *SegmentService) loadSegmentRows(ctx context.Context, tenant, id uuid.UUID) (segmentDescriptor, error) {
	var segment segmentDescriptor
	err := persistence.WithTenantTx(ctx, s.pool, tenant, func(tx pgx.Tx) error {
		var err error
		segment, err = loadSegment(ctx, tx, tenant, id)
		if err != nil {
			return err
		}
		if segment.Status == "signed" {
			return nil
		}
		rows, err := readAuditRowsRange(ctx, tx, tenant, segment.First, segment.Last)
		if err != nil {
			return err
		}
		if len(rows) != segment.Count || formatMerkleRoot(MerkleRoot(canonicalRows(rows))) != segment.Root {
			return errors.New("pending audit segment no longer matches immutable records")
		}
		return nil
	})
	return segment, err
}

func (s *SegmentService) segmentPlaintext(ctx context.Context, segment segmentDescriptor) ([]byte, error) {
	var rows []recordRow
	err := persistence.WithTenantTx(ctx, s.pool, segment.TenantID, func(tx pgx.Tx) error {
		var err error
		rows, err = readAuditRowsRange(ctx, tx, segment.TenantID, segment.First, segment.Last)
		return err
	})
	if err != nil {
		return nil, err
	}
	if len(rows) != segment.Count || formatMerkleRoot(MerkleRoot(canonicalRows(rows))) != segment.Root {
		return nil, errors.New("audit segment changed before archive")
	}
	return ndjson(rows), nil
}

type signedManifest struct {
	Version   string            `json:"version"`
	TenantID  uuid.UUID         `json:"tenantId"`
	SegmentID uuid.UUID         `json:"segmentId"`
	First     int64             `json:"firstAuditSeq"`
	Last      int64             `json:"lastAuditSeq"`
	Count     int               `json:"recordCount"`
	Root      string            `json:"merkleRoot"`
	Object    archive.ObjectRef `json:"object"`
}

func manifestBytes(segment segmentDescriptor, ref archive.ObjectRef) ([]byte, error) {
	base := signedManifest{segment.FormatVersion, segment.TenantID, segment.ID, segment.First, segment.Last, segment.Count, segment.Root, ref}
	var value any = base
	if base.Version == "audit-segment/v2" {
		value = struct {
			signedManifest
			PreviousID *uuid.UUID `json:"previousSegmentId"`
		}{base, segment.PreviousID}
	} else if base.Version != "audit-segment/v1" {
		return nil, errors.New("unsupported audit manifest version")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return bundle.CanonicalizeJSON(encoded)
}

func loadPendingSegment(ctx context.Context, tx pgx.Tx, tenant uuid.UUID) (segmentDescriptor, bool, error) {
	var segment segmentDescriptor
	segment.TenantID = tenant
	err := tx.QueryRow(ctx, `SELECT segment_id, first_audit_seq, last_audit_seq, record_count, merkle_root, status
		FROM audit.signed_segments WHERE tenant_id = $1 AND status = 'pending_signature' ORDER BY first_audit_seq LIMIT 1`, tenant).
		Scan(&segment.ID, &segment.First, &segment.Last, &segment.Count, &segment.Root, &segment.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return segmentDescriptor{}, false, nil
	}
	return segment, err == nil, err
}

func loadSegment(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID) (segmentDescriptor, error) {
	var segment segmentDescriptor
	segment.TenantID = tenant
	var refJSON []byte
	err := tx.QueryRow(ctx, `SELECT segment_id, first_audit_seq, last_audit_seq, record_count, merkle_root,
		format_version, previous_segment_id, status, COALESCE(object_ref, 'null'::jsonb), COALESCE(object_digest, ''), COALESCE(signature, ''), COALESCE(signing_key_version, '')
		FROM audit.signed_segments WHERE tenant_id = $1 AND segment_id = $2`, tenant, id).
		Scan(&segment.ID, &segment.First, &segment.Last, &segment.Count, &segment.Root, &segment.FormatVersion, &segment.PreviousID, &segment.Status, &refJSON, &segment.Digest, &segment.Sig, &segment.KeyVer)
	if err != nil {
		return segmentDescriptor{}, err
	}
	if segment.Status == "signed" {
		if err := json.Unmarshal(refJSON, &segment.Ref); err != nil {
			return segmentDescriptor{}, errors.New("audit segment archive reference is invalid")
		}
	}
	return segment, nil
}

func listSegments(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, first, last int64) ([]segmentDescriptor, error) {
	rows, err := tx.Query(ctx, `SELECT segment_id, first_audit_seq, last_audit_seq, record_count, merkle_root,
		format_version, previous_segment_id, status, COALESCE(object_ref, 'null'::jsonb), COALESCE(object_digest, ''), COALESCE(signature, ''), COALESCE(signing_key_version, '')
		FROM audit.signed_segments WHERE tenant_id = $1 AND last_audit_seq >= $2 AND first_audit_seq <= $3 ORDER BY first_audit_seq`, tenant, first, last)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []segmentDescriptor
	for rows.Next() {
		segment := segmentDescriptor{TenantID: tenant}
		var refJSON []byte
		if err := rows.Scan(&segment.ID, &segment.First, &segment.Last, &segment.Count, &segment.Root, &segment.FormatVersion, &segment.PreviousID, &segment.Status, &refJSON, &segment.Digest, &segment.Sig, &segment.KeyVer); err != nil {
			return nil, err
		}
		if segment.Status == "signed" {
			if err := json.Unmarshal(refJSON, &segment.Ref); err != nil {
				return nil, errors.New("audit segment archive reference is invalid")
			}
		}
		result = append(result, segment)
	}
	return result, rows.Err()
}

func readAuditRows(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, after *int64, limit int) ([]recordRow, error) {
	var afterValue int64
	if after != nil {
		afterValue = *after
	}
	rows, err := tx.Query(ctx, `SELECT audit_seq, tenant_id, record_id, event_type, entity_kind, entity_id, subject, record, canonical_digest, created_at
		FROM audit.records WHERE tenant_id = $1 AND audit_seq > $2 ORDER BY audit_seq LIMIT $3`, tenant, afterValue, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAuditRows(rows)
}

func readAuditRowsRange(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, first, last int64) ([]recordRow, error) {
	rows, err := tx.Query(ctx, `SELECT audit_seq, tenant_id, record_id, event_type, entity_kind, entity_id, subject, record, canonical_digest, created_at
		FROM audit.records WHERE tenant_id = $1 AND audit_seq BETWEEN $2 AND $3 ORDER BY audit_seq`, tenant, first, last)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAuditRows(rows)
}

func readAuditRange(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, first, last int64) ([]recordRow, error) {
	return readAuditRowsRange(ctx, tx, tenant, first, last)
}

type auditRows interface {
	Next() bool
	Scan(...any) error
	Err() error
}

func scanAuditRows(rows auditRows) ([]recordRow, error) {
	var result []recordRow
	for rows.Next() {
		var row recordRow
		var tenant uuid.UUID
		var recordJSON []byte
		var entityID *uuid.UUID
		if err := rows.Scan(&row.seq, &tenant, &row.Entry.RecordID, &row.Entry.EventType, &row.Entry.EntityKind, &entityID, &row.Entry.Subject, &recordJSON, &row.Digest, &row.CreatedAt); err != nil {
			return nil, err
		}
		row.Entry.TenantID = tenant
		if entityID != nil {
			row.Entry.EntityID = *entityID
		}
		row.Entry.CreatedAt = row.CreatedAt.UTC()
		decoder := json.NewDecoder(bytes.NewReader(recordJSON))
		decoder.UseNumber()
		if err := decoder.Decode(&row.Entry.Payload); err != nil || row.Entry.Payload == nil {
			return nil, errors.New("audit record payload is invalid")
		}
		if containsSecretField(row.Entry.Payload) {
			return nil, errors.New("audit record contains a prohibited secret or raw command field")
		}
		if err := digestCanonicalRow(&row); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func (r recordRow) EntrySeq() int64 { return r.seq }

func canonicalRows(rows []recordRow) [][]byte {
	result := make([][]byte, len(rows))
	for i := range rows {
		result[i] = rows[i].Canonical
	}
	return result
}

func ndjson(rows []recordRow) []byte {
	var output bytes.Buffer
	for _, row := range rows {
		output.Write(row.Canonical)
		output.WriteByte('\n')
	}
	return output.Bytes()
}

func digestCanonicalRow(row *recordRow) error {
	canonical, err := canonicalEntry(row.Entry)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(canonical)
	if !bytes.Equal(sum[:], row.Digest) {
		legacy := struct {
			TenantID   uuid.UUID
			RecordID   uuid.UUID
			EventType  string
			EntityKind string
			EntityID   uuid.UUID
			Subject    string
			Payload    map[string]any
		}{row.Entry.TenantID, row.Entry.RecordID, row.Entry.EventType, row.Entry.EntityKind, row.Entry.EntityID, row.Entry.Subject, row.Entry.Payload}
		legacyBytes, marshalErr := json.Marshal(legacy)
		legacySum := sha256.Sum256(legacyBytes)
		if marshalErr == nil && bytes.Equal(legacySum[:], row.Digest) {
			row.Canonical = canonical
			return nil
		}
		legacyPayloadJSON, marshalErr := json.Marshal(row.Entry.Payload)
		if marshalErr != nil {
			return errors.New("audit record canonical digest mismatch")
		}
		var legacyPayload map[string]any
		if err := json.Unmarshal(legacyPayloadJSON, &legacyPayload); err != nil {
			return errors.New("audit record canonical digest mismatch")
		}
		legacy.Payload = legacyPayload
		legacyBytes, marshalErr = json.Marshal(legacy)
		legacySum = sha256.Sum256(legacyBytes)
		if marshalErr != nil || !bytes.Equal(legacySum[:], row.Digest) {
			return errors.New("audit record canonical digest mismatch")
		}
	}
	row.Canonical = canonical
	return nil
}
