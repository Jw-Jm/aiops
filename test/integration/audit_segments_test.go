package integration

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"ops-platform/internal/archive"
	"ops-platform/internal/audit"
	protect "ops-platform/internal/crypto"
	"ops-platform/internal/integrations/openbao"
	"ops-platform/internal/persistence"
)

const expectedAuditSegmentLimit = 10000

func TestAuditSegmentsAppendConcurrentlyRecoverAndVerifyTenantScoped(t *testing.T) {
	ctx, admin, migrationDir, dbURL := newMigrationDatabase(t)
	if err := goose.UpToContext(ctx, admin, migrationDir, 1); err != nil {
		t.Fatal(err)
	}
	if err := runRemainingMigrationsAsMigrationRole(t, ctx, admin, dbURL, migrationDir); err != nil {
		t.Fatalf("migrate isolated database: %v", err)
	}
	tenantA, tenantB := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{tenantA, tenantB} {
		if _, err := admin.ExecContext(ctx, `INSERT INTO platform.tenants (tenant_id, slug, display_name) VALUES ($1, $2, $2)`, id, "tenant-"+id.String()[:8]); err != nil {
			t.Fatal(err)
		}
	}

	poolConfig, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		t.Fatal(err)
	}
	poolConfig.MaxConns = 20
	poolConfig.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, `SET ROLE worker_runtime_role`)
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	appendEntry := func(tenant uuid.UUID, at time.Time) int64 {
		t.Helper()
		var seq int64
		err := persistence.WithTenantTx(ctx, pool, tenant, func(tx pgx.Tx) error {
			value, err := audit.Append(ctx, tx, audit.Entry{
				TenantID: tenant, RecordID: uuid.New(), EventType: "source.registered",
				EntityKind: "source", EntityID: uuid.New(), Subject: "integration-user",
				Payload: map[string]any{"action": "register", "commandDigest": "sha256:0123"}, CreatedAt: at,
			})
			seq = value.Audit
			return err
		})
		if err != nil {
			t.Fatalf("append tenant audit row: %v", err)
		}
		return seq
	}
	created := time.Now().UTC().Add(-6 * time.Minute)
	firstSeq := appendEntry(tenantA, created)
	otherTenantSeq := appendEntry(tenantB, created)
	if firstSeq == otherTenantSeq {
		t.Fatal("global audit sequence was not unique across tenants")
	}
	if err := persistence.WithTenantTx(ctx, pool, tenantA, func(tx pgx.Tx) error {
		var auditSeq, tenantSeq int64
		if err := tx.QueryRow(ctx, `SELECT audit_seq, tenant_seq FROM audit.records WHERE tenant_id=$1 AND audit_seq=$2`, tenantA, firstSeq).Scan(&auditSeq, &tenantSeq); err != nil {
			return err
		}
		if auditSeq != tenantSeq {
			return fmt.Errorf("new audit sequence columns differ: audit=%d tenant=%d", auditSeq, tenantSeq)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	backend := &integrationArchiveBackend{objects: map[string]archive.StoredObject{}}
	archiveStore, err := archive.NewStore(backend, 16<<20)
	if err != nil {
		t.Fatal(err)
	}
	protector := integrationProtector{}
	signer, err := newIntegrationSigner()
	if err != nil {
		t.Fatal(err)
	}
	service, err := audit.NewSegmentService(pool, archiveStore, protector, &signer, "audit-signing", 30*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	id, sealed, err := service.SealNext(ctx, tenantA)
	if err != nil || !sealed || id == uuid.Nil {
		t.Fatalf("seal aged tenant segment: id=%s sealed=%v err=%v", id, sealed, err)
	}
	if id.Version() != 7 {
		t.Errorf("audit segment ID version=%d, expected UUIDv7", id.Version())
	}
	if _, exists, err := archiveStore.Find(ctx, tenantA, id, "audit-proof"); err != nil || !exists {
		t.Errorf("signed manifest proof was not archived: exists=%v err=%v", exists, err)
	}
	if err := service.VerifyRange(ctx, tenantA, firstSeq, firstSeq); err != nil {
		t.Fatalf("verify signed tenant segment: %v", err)
	}
	if err := service.VerifyRange(ctx, tenantB, otherTenantSeq, otherTenantSeq); err == nil {
		t.Fatal("tenant B verified tenant A's archived audit segment")
	}

	// Corrupt the stored encrypted object and confirm verification fails closed.
	var objectKey string
	if err := admin.QueryRowContext(ctx, `SELECT object_ref->>'key' FROM audit.signed_segments WHERE tenant_id=$1 AND segment_id=$2`, tenantA, id).Scan(&objectKey); err != nil {
		t.Fatal(err)
	}
	backend.mu.Lock()
	stored := backend.objects[objectKey]
	stored.Body = append(stored.Body, 'x')
	backend.objects[objectKey] = stored
	backend.mu.Unlock()
	if err := service.VerifyRange(ctx, tenantA, firstSeq, firstSeq); err == nil {
		t.Fatal("tampered archive verified successfully")
	}
	backend.mu.Lock()
	stored.Body = stored.Body[:len(stored.Body)-1]
	backend.objects[objectKey] = stored
	backend.mu.Unlock()

	// A failed upload leaves a pending immutable segment which a retry resumes.
	secondSeq := appendEntry(tenantA, time.Now().UTC().Add(-6*time.Minute))
	backend.mu.Lock()
	backend.failPuts = 1
	backend.mu.Unlock()
	if _, sealed, err := service.SealNext(ctx, tenantA); err == nil || sealed {
		t.Fatal("first archive attempt unexpectedly completed")
	}
	var pending int
	if err := admin.QueryRowContext(ctx, `SELECT count(*) FROM audit.signed_segments WHERE tenant_id=$1 AND status='pending_signature'`, tenantA).Scan(&pending); err != nil || pending != 1 {
		t.Fatalf("pending segment count=%d err=%v", pending, err)
	}
	if _, sealed, err := service.SealNext(ctx, tenantA); err != nil || !sealed {
		t.Fatalf("retry pending segment: sealed=%v err=%v", sealed, err)
	}
	if err := service.VerifyRange(ctx, tenantA, firstSeq, secondSeq); err != nil {
		t.Fatalf("verify recovered segment: %v", err)
	}

	// A successful S3 upload followed by a signing outage must reuse the exact
	// immutable object on retry, even when Transit encryption is randomized.
	thirdSeq := appendEntry(tenantA, time.Now().UTC().Add(-6*time.Minute))
	backend.mu.Lock()
	objectsBefore := backend.successfulPuts
	backend.mu.Unlock()
	signer.mu.Lock()
	signer.failSigns = 1
	signer.mu.Unlock()
	if _, sealed, err := service.SealNext(ctx, tenantA); err == nil || sealed {
		t.Fatal("signing outage unexpectedly completed the segment")
	}
	if _, sealed, err := service.SealNext(ctx, tenantA); err != nil || !sealed {
		t.Fatalf("resume archived segment after signing outage: sealed=%v err=%v", sealed, err)
	}
	backend.mu.Lock()
	objectsAfter := backend.successfulPuts
	backend.mu.Unlock()
	if objectsAfter != objectsBefore+2 {
		t.Fatalf("segment retry must upload exactly one encrypted segment and one signed proof: successful uploads before=%d after=%d", objectsBefore, objectsAfter)
	}
	if err := service.VerifyRange(ctx, tenantA, thirdSeq, thirdSeq); err != nil {
		t.Fatalf("verify segment recovered after sign outage: %v", err)
	}
	// The verifier must detect deletion of an entire signed segment, even when
	// both its metadata and records disappear. Restore via INSERT after checking
	// so later concurrency tests continue against the original signed history.
	mutation, err := admin.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer mutation.Close()
	for _, query := range []string{
		`CREATE TEMP TABLE review_removed_segment AS SELECT * FROM audit.signed_segments WHERE tenant_id=$1 AND first_audit_seq=$2`,
		`CREATE TEMP TABLE review_removed_record AS SELECT * FROM audit.records WHERE tenant_id=$1 AND audit_seq=$2`,
	} {
		if _, err := mutation.ExecContext(ctx, query, tenantA, secondSeq); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := mutation.ExecContext(ctx, `SET session_replication_role=replica`); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`DELETE FROM audit.signed_segments WHERE tenant_id=$1 AND first_audit_seq=$2`, `DELETE FROM audit.records WHERE tenant_id=$1 AND audit_seq=$2`} {
		if _, err := mutation.ExecContext(ctx, query, tenantA, secondSeq); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := mutation.ExecContext(ctx, `SET session_replication_role=origin`); err != nil {
		t.Fatal(err)
	}
	if err := service.VerifyRange(ctx, tenantA, firstSeq, thirdSeq); err == nil {
		t.Error("complete signed segment deletion was accepted")
	}
	if _, err := mutation.ExecContext(ctx, `INSERT INTO audit.signed_segments SELECT * FROM review_removed_segment; INSERT INTO audit.records OVERRIDING SYSTEM VALUE SELECT * FROM review_removed_record`); err != nil {
		t.Fatal(err)
	}
	// Exercise real PostgreSQL record mutation, insertion into an inter-tenant
	// sequence gap, reordering, and forged signed metadata, then restore each.
	if _, err := mutation.ExecContext(ctx, `UPDATE audit.records SET subject='tampered' WHERE tenant_id=$1 AND audit_seq=$2`, tenantA, firstSeq); err != nil {
		t.Fatal(err)
	}
	if err := service.VerifyRange(ctx, tenantA, firstSeq, thirdSeq); err == nil {
		t.Fatal("record modification verified")
	}
	if _, err := mutation.ExecContext(ctx, `UPDATE audit.records SET subject='integration-user' WHERE tenant_id=$1 AND audit_seq=$2`, tenantA, firstSeq); err != nil {
		t.Fatal(err)
	}
	if _, err := mutation.ExecContext(ctx, `UPDATE audit.records SET tenant_id=$1 WHERE audit_seq=$2`, tenantA, otherTenantSeq); err != nil {
		t.Fatal(err)
	}
	if err := service.VerifyRange(ctx, tenantA, firstSeq, thirdSeq); err == nil {
		t.Fatal("record inserted into signed range verified")
	}
	if _, err := mutation.ExecContext(ctx, `UPDATE audit.records SET tenant_id=$1 WHERE audit_seq=$2`, tenantB, otherTenantSeq); err != nil {
		t.Fatal(err)
	}
	if _, err := mutation.ExecContext(ctx, `CREATE TEMP TABLE review_swap AS SELECT * FROM audit.records WHERE tenant_id=$1 AND audit_seq IN($2,$3)`, tenantA, firstSeq, secondSeq); err != nil {
		t.Fatal(err)
	}
	if _, err := mutation.ExecContext(ctx, `DELETE FROM audit.records WHERE tenant_id=$1 AND audit_seq IN($2,$3)`, tenantA, firstSeq, secondSeq); err != nil {
		t.Fatal(err)
	}
	if _, err := mutation.ExecContext(ctx, `INSERT INTO audit.records(audit_seq,tenant_id,tenant_seq,record_id,event_type,entity_kind,entity_id,subject,record,canonical_digest,created_at) OVERRIDING SYSTEM VALUE SELECT CASE audit_seq WHEN $1 THEN $2 ELSE $1 END,tenant_id,tenant_seq,record_id,event_type,entity_kind,entity_id,subject,record,canonical_digest,created_at FROM review_swap`, firstSeq, secondSeq); err != nil {
		t.Fatal(err)
	}
	if err := service.VerifyRange(ctx, tenantA, firstSeq, thirdSeq); err == nil {
		t.Fatal("reordered records verified")
	}
	if _, err := mutation.ExecContext(ctx, `DELETE FROM audit.records WHERE tenant_id=$1 AND audit_seq IN($2,$3)`, tenantA, firstSeq, secondSeq); err != nil {
		t.Fatal(err)
	}
	if _, err := mutation.ExecContext(ctx, `INSERT INTO audit.records OVERRIDING SYSTEM VALUE SELECT * FROM review_swap`); err != nil {
		t.Fatal(err)
	}

	var originalRoot, originalSignature string
	if err := mutation.QueryRowContext(ctx, `SELECT merkle_root,signature FROM audit.signed_segments WHERE tenant_id=$1 AND segment_id=$2`, tenantA, id).Scan(&originalRoot, &originalSignature); err != nil {
		t.Fatal(err)
	}
	if _, err := mutation.ExecContext(ctx, `SET session_replication_role=replica`); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"merkle_root", "signature"} {
		forged := "sha256:" + strings.Repeat("b", 64)
		if column == "signature" {
			forged = "vault:v1:" + base64.StdEncoding.EncodeToString(make([]byte, 64))
		}
		if _, err := mutation.ExecContext(ctx, `UPDATE audit.signed_segments SET `+column+`=$1 WHERE tenant_id=$2 AND segment_id=$3`, forged, tenantA, id); err != nil {
			t.Fatal(err)
		}
		if err := service.VerifyRange(ctx, tenantA, firstSeq, thirdSeq); err == nil {
			t.Fatalf("forged %s verified", column)
		}
		if _, err := mutation.ExecContext(ctx, `UPDATE audit.signed_segments SET merkle_root=$1,signature=$2 WHERE tenant_id=$3 AND segment_id=$4`, originalRoot, originalSignature, tenantA, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := mutation.ExecContext(ctx, `SET session_replication_role=origin`); err != nil {
		t.Fatal(err)
	}
	if err := service.VerifyRange(ctx, tenantA, firstSeq, thirdSeq); err != nil {
		t.Fatalf("restored original audit range: %v", err)
	}

	// Concurrent same-tenant append transactions use a shared lock and a
	// database sequence; every committed record keeps a unique monotonic ID.
	const writers = 20
	var wg sync.WaitGroup
	var idsMu sync.Mutex
	ids := make(map[int64]bool, writers)
	errCh := make(chan error, writers)
	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var seq int64
			err := persistence.WithTenantTx(ctx, pool, tenantA, func(tx pgx.Tx) error {
				result, err := audit.Append(ctx, tx, audit.Entry{TenantID: tenantA, RecordID: uuid.New(), EventType: "source.updated", EntityKind: "source", EntityID: uuid.New(), Subject: "integration-user", Payload: map[string]any{"revision": 2}, CreatedAt: created})
				seq = result.Audit
				return err
			})
			if err != nil {
				errCh <- err
				return
			}
			idsMu.Lock()
			ids[seq] = true
			idsMu.Unlock()
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
	if len(ids) != writers {
		t.Fatalf("concurrent append produced %d unique sequence IDs, want %d", len(ids), writers)
	}

	// The API and worker roles can read append-only audit rows but cannot
	// directly rewrite records or promote a segment to signed.
	for _, query := range []string{
		`UPDATE audit.records SET subject='rewritten' WHERE tenant_id=$1`,
		`DELETE FROM audit.records WHERE tenant_id=$1`,
		`UPDATE audit.signed_segments SET status='signed' WHERE tenant_id=$1`,
	} {
		err := persistence.WithTenantTx(ctx, pool, tenantA, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `SAVEPOINT audit_segment_mutation_check`); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, query, tenantA)
			if err == nil {
				return errors.New("runtime role unexpectedly modified append-only audit state")
			}
			_, rollbackErr := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT audit_segment_mutation_check`)
			return rollbackErr
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	bulkTenant := uuid.New()
	if _, err := admin.ExecContext(ctx, `INSERT INTO platform.tenants (tenant_id, slug, display_name) VALUES ($1, $2, $2)`, bulkTenant, "audit-"+bulkTenant.String()[:8]); err != nil {
		t.Fatal(err)
	}
	var firstBulkSeq, lastBulkSeq int64
	if err := persistence.WithTenantTx(ctx, pool, bulkTenant, func(tx pgx.Tx) error {
		for index := 0; index < expectedAuditSegmentLimit+1; index++ {
			result, err := audit.Append(ctx, tx, audit.Entry{TenantID: bulkTenant, RecordID: uuid.New(), EventType: "source.updated", EntityKind: "source", EntityID: uuid.New(), Subject: "integration-user", Payload: map[string]any{"revision": index}, CreatedAt: created})
			if err != nil {
				return err
			}
			if index == 0 {
				firstBulkSeq = result.Audit
			}
			lastBulkSeq = result.Audit
		}
		return nil
	}); err != nil {
		t.Fatalf("append bounded segment fixture: %v", err)
	}
	firstSegment, firstSealed, err := service.SealNext(ctx, bulkTenant)
	if err != nil || !firstSealed {
		t.Fatalf("seal first bounded segment: id=%s sealed=%v err=%v", firstSegment, firstSealed, err)
	}
	secondSegment, secondSealed, err := service.SealNext(ctx, bulkTenant)
	if err != nil || !secondSealed || secondSegment == firstSegment {
		t.Fatalf("seal overflow record: id=%s sealed=%v err=%v", secondSegment, secondSealed, err)
	}
	var largestSegment, segmentCount int
	if err := admin.QueryRowContext(ctx, `SELECT max(record_count), count(*) FROM audit.signed_segments WHERE tenant_id=$1`, bulkTenant).Scan(&largestSegment, &segmentCount); err != nil {
		t.Fatal(err)
	}
	if largestSegment != expectedAuditSegmentLimit || segmentCount != 2 {
		t.Fatalf("bounded segments: max records=%d count=%d, want %d records across 2 segments", largestSegment, segmentCount, expectedAuditSegmentLimit)
	}
	if err := service.VerifyRange(ctx, bulkTenant, firstBulkSeq, lastBulkSeq); err != nil {
		t.Fatalf("verify both bounded segments: %v", err)
	}
	// A recovered backlog must still split at five-minute boundaries.
	windowTenant := uuid.Must(uuid.NewV7())
	if _, err := admin.ExecContext(ctx, `INSERT INTO platform.tenants(tenant_id,slug,display_name) VALUES($1,$2,'window')`, windowTenant, windowTenant.String()); err != nil {
		t.Fatal(err)
	}
	for _, age := range []time.Duration{15 * time.Minute, 8 * time.Minute, 6 * time.Minute} {
		appendEntry(windowTenant, time.Now().UTC().Add(-age))
	}
	for range 5 {
		_, worked, err := service.SealNext(ctx, windowTenant)
		if err != nil {
			t.Fatal(err)
		}
		if !worked {
			break
		}
	}
	var tooWide int
	if err := admin.QueryRowContext(ctx, `SELECT count(*) FROM audit.signed_segments s WHERE tenant_id=$1 AND (SELECT max(created_at)-min(created_at) FROM audit.records r WHERE r.tenant_id=s.tenant_id AND r.audit_seq BETWEEN s.first_audit_seq AND s.last_audit_seq)>interval '5 minutes'`, windowTenant).Scan(&tooWide); err != nil {
		t.Fatal(err)
	}
	if tooWide != 0 {
		t.Errorf("%d recovered segments exceed five-minute window", tooWide)
	}
}

type integrationArchiveBackend struct {
	mu             sync.Mutex
	objects        map[string]archive.StoredObject
	failPuts       int
	successfulPuts int
}

func (b *integrationArchiveBackend) Put(_ context.Context, key string, body []byte, metadata map[string]string) (archive.Version, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failPuts > 0 {
		b.failPuts--
		return archive.Version{}, errors.New("injected isolated archive outage")
	}
	if _, exists := b.objects[key]; exists {
		return archive.Version{}, errors.New("archive key collision")
	}
	b.objects[key] = archive.StoredObject{Body: append([]byte(nil), body...), Metadata: metadata}
	b.successfulPuts++
	return archive.Version{ETag: "test-etag"}, nil
}
func (b *integrationArchiveBackend) Get(_ context.Context, key, _ string) (archive.StoredObject, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	value, ok := b.objects[key]
	if !ok {
		return archive.StoredObject{}, archive.ErrObjectNotFound
	}
	value.Body = append([]byte(nil), value.Body...)
	return value, nil
}
func (b *integrationArchiveBackend) Head(ctx context.Context, key, version string) (archive.StoredObject, error) {
	return b.Get(ctx, key, version)
}
func (b *integrationArchiveBackend) Delete(_ context.Context, key, _ string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.objects, key)
	return nil
}

type integrationProtector struct{}

func (integrationProtector) Seal(_ context.Context, tenant, object uuid.UUID, plain []byte) (protect.Envelope, error) {
	digest := sha256.Sum256(plain)
	return protect.Envelope{Version: "transit-envelope/v1", TenantID: tenant, ObjectID: object, KeyName: "evidence-archive", Ciphertext: base64.StdEncoding.EncodeToString(plain), KeyVersion: "1", PlaintextDigest: "sha256:" + hex.EncodeToString(digest[:])}, nil
}
func (integrationProtector) Open(_ context.Context, tenant, object uuid.UUID, envelope protect.Envelope) ([]byte, error) {
	if envelope.TenantID != tenant || envelope.ObjectID != object || envelope.KeyName != "evidence-archive" {
		return nil, errors.New("test encryption identity mismatch")
	}
	plain, err := base64.StdEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(plain)
	if envelope.PlaintextDigest != "sha256:"+hex.EncodeToString(digest[:]) {
		return nil, errors.New("test archive digest mismatch")
	}
	return plain, nil
}

type integrationSigner struct {
	mu        sync.Mutex
	private   ed25519.PrivateKey
	public    ed25519.PublicKey
	failSigns int
}

func newIntegrationSigner() (integrationSigner, error) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	return integrationSigner{private: private, public: public}, err
}
func (s *integrationSigner) TransitSign(_ context.Context, _ string, message []byte) (openbao.TransitSignature, error) {
	s.mu.Lock()
	if s.failSigns > 0 {
		s.failSigns--
		s.mu.Unlock()
		return openbao.TransitSignature{}, errors.New("injected isolated signing outage")
	}
	s.mu.Unlock()
	signature := ed25519.Sign(s.private, message)
	return openbao.TransitSignature{Signature: "vault:v1:" + base64.StdEncoding.EncodeToString(signature), KeyVersion: "1"}, nil
}
func (s *integrationSigner) TransitVerify(_ context.Context, _ string, message []byte, encoded string) error {
	if !strings.HasPrefix(encoded, "vault:v1:") {
		return errors.New("signature format invalid")
	}
	signature, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(encoded, "vault:v1:"))
	if err != nil || !ed25519.Verify(s.public, message, signature) {
		return errors.New("signature invalid")
	}
	return nil
}
