package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"
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
	"ops-platform/internal/integrations/s3"
	"ops-platform/internal/persistence"
)

func TestRealOpenBaoTransitAndSeaweedS3AuditArchive(t *testing.T) {
	baoAddress := os.Getenv("SP03_TEST_OPENBAO_URL")
	baoToken := os.Getenv("SP03_TEST_OPENBAO_TOKEN")
	baoCAPath := os.Getenv("SP03_TEST_OPENBAO_CA_FILE")
	s3Endpoint := os.Getenv("SP03_TEST_S3_ENDPOINT")
	s3Access := os.Getenv("SP03_TEST_S3_ACCESS_KEY")
	s3Secret := os.Getenv("SP03_TEST_S3_SECRET_KEY")
	if baoAddress == "" || baoToken == "" || baoCAPath == "" || s3Endpoint == "" || s3Access == "" || s3Secret == "" {
		t.Skip("isolated OpenBao and SeaweedFS test endpoints and credentials are required")
	}
	caBundle, err := os.ReadFile(baoCAPath)
	if err != nil {
		t.Fatal("read isolated OpenBao development CA")
	}
	bao, err := openbao.NewClient(openbao.ClientConfig{Address: baoAddress, ServerName: "localhost", CACertBundle: caBundle, Token: baoToken, ServiceDomain: "ops-system.svc.cluster.local"})
	if err != nil {
		t.Fatalf("configure isolated OpenBao client: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := bao.ConfigureTransit(ctx); err != nil {
		t.Fatalf("configure isolated OpenBao Transit engines and keys: %v", err)
	}
	protector, err := protect.NewTransitProtector(bao, "evidence-archive")
	if err != nil {
		t.Fatal(err)
	}
	tenantID, objectID := uuid.New(), uuid.New()
	sealed, err := protector.Seal(ctx, tenantID, objectID, []byte("SP-03 isolated Transit round-trip"))
	if err != nil {
		t.Fatal("encrypt through isolated OpenBao Transit")
	}
	plain, err := protector.Open(ctx, tenantID, objectID, sealed)
	if err != nil || string(plain) != "SP-03 isolated Transit round-trip" {
		t.Fatalf("Transit decrypt round-trip failed: err=%v", err)
	}
	if err := bao.TransitRotate(ctx, "evidence-archive"); err != nil {
		t.Fatalf("rotate isolated Transit evidence key: %v", err)
	}
	plain, err = protector.Open(ctx, tenantID, objectID, sealed)
	if err != nil || string(plain) != "SP-03 isolated Transit round-trip" {
		t.Fatalf("decrypt with previous Transit key version failed: err=%v", err)
	}
	signInput := []byte(`{"purpose":"sp03-audit-test"}`)
	signature, err := bao.TransitSign(ctx, "audit-signing", signInput)
	if err != nil {
		t.Fatal("sign through isolated OpenBao Transit")
	}
	if err := bao.TransitVerify(ctx, "audit-signing", signInput, signature.Signature); err != nil {
		t.Fatalf("verify isolated Transit signature: %v", err)
	}
	if err := bao.TransitVerify(ctx, "audit-signing", []byte(`{"purpose":"tampered"}`), signature.Signature); err == nil {
		t.Fatal("isolated Transit accepted a changed signed message")
	}

	bucketName := "sp03-" + uuid.NewString()[:8]
	s3Client, err := s3.NewClient(s3.Config{Endpoint: s3Endpoint, Region: "us-east-1", Bucket: bucketName, AccessKey: s3Access, SecretKey: s3Secret, MaxObjectBytes: 2 << 20})
	if err != nil {
		t.Fatal("configure isolated SeaweedFS S3 client")
	}
	if err := s3Client.CreateBucket(ctx); err != nil {
		t.Fatalf("create isolated S3 bucket with versioning and Object Lock: %v", err)
	}
	store, err := archive.NewStore(s3Client, 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	archiveTenant, archiveID := uuid.New(), uuid.New()
	retention := time.Now().UTC().Add(30 * 24 * time.Hour).Truncate(time.Second)
	descriptor := archive.ObjectDescriptor{TenantID: archiveTenant, ObjectID: archiveID, Category: "evidence", ContentType: "application/octet-stream", RetainUntil: retention}
	ref, err := store.Put(ctx, descriptor, strings.NewReader("opaque-encrypted-payload"))
	if err != nil {
		t.Fatalf("put isolated SeaweedFS object with compliance retention: %v", err)
	}
	if _, err := store.Put(ctx, descriptor, strings.NewReader("opaque-encrypted-payload")); err != nil {
		t.Fatalf("idempotent retry after an uncertain S3 response: %v", err)
	}
	if _, found, err := store.Find(ctx, archiveTenant, archiveID, "evidence"); err != nil || !found {
		t.Fatalf("find persisted S3 object: found=%v err=%v", found, err)
	}
	if got, err := store.Get(ctx, archiveTenant, ref); err != nil || string(got) != "opaque-encrypted-payload" {
		t.Fatalf("read isolated S3 object: err=%v", err)
	}
	if err := store.Delete(ctx, archiveTenant, ref); !errors.Is(err, archive.ErrRetentionActive) {
		t.Fatalf("application retention guard: %v", err)
	}
	if err := s3Client.Delete(ctx, ref.Key, ref.VersionID); err == nil {
		t.Fatal("SeaweedFS deleted a compliance-locked object version")
	}

	// Exercise the complete append -> PostgreSQL pending segment -> OpenBao
	// Transit encryption/signature -> SeaweedFS Object Lock -> verification path.
	dbctx, admin, migrationDir, dbURL := newMigrationDatabase(t)
	if err := goose.UpToContext(dbctx, admin, migrationDir, 1); err != nil {
		t.Fatal(err)
	}
	if err := runRemainingMigrationsAsMigrationRole(t, dbctx, admin, dbURL, migrationDir); err != nil {
		t.Fatalf("migrate isolated database: %v", err)
	}
	segmentTenant := uuid.New()
	if _, err := admin.ExecContext(dbctx, `INSERT INTO platform.tenants (tenant_id, slug, display_name) VALUES ($1, $2, $2)`, segmentTenant, "audit-"+segmentTenant.String()[:8]); err != nil {
		t.Fatal(err)
	}
	poolConfig, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		t.Fatal(err)
	}
	poolConfig.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, `SET ROLE worker_runtime_role`)
		return err
	}
	pool, err := pgxpool.NewWithConfig(dbctx, poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var sequence audit.Sequence
	if err := persistence.WithTenantTx(dbctx, pool, segmentTenant, func(tx pgx.Tx) error {
		var err error
		sequence, err = audit.Append(dbctx, tx, audit.Entry{TenantID: segmentTenant, RecordID: uuid.New(), EventType: "source.registered", EntityKind: "source", EntityID: uuid.New(), Subject: "isolated-test", Payload: map[string]any{"action": "registered", "authRef": "bao://test/source"}, CreatedAt: time.Now().UTC().Add(-6 * time.Minute)})
		return err
	}); err != nil {
		t.Fatalf("append audit row in isolated PostgreSQL: %v", err)
	}
	segmentStore, err := audit.NewSegmentService(pool, store, protector, bao, "audit-signing", 90*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	segmentID, sealedSegment, err := segmentStore.SealNext(dbctx, segmentTenant)
	if err != nil || !sealedSegment {
		t.Fatalf("seal real audit archive segment %s: sealed=%v err=%v", segmentID, sealedSegment, err)
	}
	if err := segmentStore.VerifyRange(dbctx, segmentTenant, sequence.Audit, sequence.Audit); err != nil {
		t.Fatalf("verify real audit archive segment: %v", err)
	}
	unrelatedTenant := uuid.New()
	if _, err := admin.ExecContext(dbctx, `INSERT INTO platform.tenants (tenant_id, slug, display_name) VALUES ($1, $2, $2)`, unrelatedTenant, "audit-"+unrelatedTenant.String()[:8]); err != nil {
		t.Fatal(err)
	}
	var unrelatedSequence audit.Sequence
	if err := persistence.WithTenantTx(dbctx, pool, unrelatedTenant, func(tx pgx.Tx) error {
		var err error
		unrelatedSequence, err = audit.Append(dbctx, tx, audit.Entry{TenantID: unrelatedTenant, RecordID: uuid.New(), EventType: "source.registered", EntityKind: "source", EntityID: uuid.New(), Subject: "isolated-test", Payload: map[string]any{"action": "registered"}, CreatedAt: time.Now().UTC().Add(-6 * time.Minute)})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := segmentStore.VerifyRange(dbctx, unrelatedTenant, unrelatedSequence.Audit, unrelatedSequence.Audit); err == nil {
		t.Fatal("unrelated tenant verified a record outside its signed archive")
	}
	if err := bao.TransitRotate(dbctx, "evidence-archive"); err != nil {
		t.Fatalf("rotate evidence key after archival: %v", err)
	}
	if err := segmentStore.VerifyRange(dbctx, segmentTenant, sequence.Audit, sequence.Audit); err != nil {
		t.Fatalf("verify archive after Transit key rotation: %v", err)
	}
	objectDigest := sha256.Sum256([]byte("opaque-encrypted-payload"))
	t.Logf("isolated S3 bucket=%s object=%s digest=sha256:%s; audit tenant=%s segment=%s seq=%d verified", bucketName, ref.ObjectID, hex.EncodeToString(objectDigest[:]), segmentTenant, segmentID, sequence.Audit)
}
