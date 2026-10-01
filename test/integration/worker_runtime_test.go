package integration

import (
	"context"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pressly/goose/v3"
	"ops-platform/internal/app"
	"ops-platform/internal/archive"
	"ops-platform/internal/audit"
	"ops-platform/internal/crypto"
	"ops-platform/internal/integrations/s3"
	"ops-platform/internal/observability"
	"ops-platform/internal/persistence"
)

func TestRealAuditWorkerProjectedLoginResumesPendingAfterArchiveOutage(t *testing.T) {
	bao := liveWorkloadClient(t)
	ctx, db, dir, dsn := newMigrationDatabase(t)
	if err := goose.UpContext(ctx, db, dir); err != nil {
		t.Fatal(err)
	}
	if err := bao.Configure(ctx); err != nil {
		t.Fatal(err)
	}
	name := "sp03_worker_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := db.ExecContext(ctx, `CREATE ROLE "`+name+`" LOGIN; GRANT worker_runtime_role TO "`+name+`"`); err != nil {
		t.Fatal(err)
	}
	defer db.ExecContext(ctx, `DROP ROLE "`+name+`"`)
	u, _ := url.Parse(dsn)
	u.User = url.User(name)
	pool, err := app.OpenRuntimePool(ctx, u.String(), "worker_runtime_role")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	tenants := []uuid.UUID{uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())}
	sequences := map[uuid.UUID]int64{}
	for _, tenant := range tenants {
		if _, err := db.ExecContext(ctx, `INSERT INTO platform.tenants(tenant_id,slug,display_name) VALUES($1,$2,'worker-review')`, tenant, tenant.String()); err != nil {
			t.Fatal(err)
		}
		err := persistence.WithTenantTx(ctx, pool, tenant, func(tx pgx.Tx) error {
			seq, err := audit.Append(ctx, tx, audit.Entry{TenantID: tenant, RecordID: uuid.Must(uuid.NewV7()), EventType: "sp03.review", EntityKind: "test-consumer", Subject: "test", Payload: map[string]any{"action": "test"}, CreatedAt: time.Now().UTC().Add(-6 * time.Minute)})
			sequences[tenant] = seq.Audit
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	bucket := "sp03-worker-" + uuid.NewString()[:8]
	backend, err := s3.NewClient(s3.Config{Endpoint: os.Getenv("SP03_TEST_S3_ENDPOINT"), Bucket: bucket, AccessKey: os.Getenv("SP03_TEST_S3_ACCESS_KEY"), SecretKey: os.Getenv("SP03_TEST_S3_SECRET_KEY")})
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.CreateBucket(ctx); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"OPENBAO_ADDR": os.Getenv("SP03_TEST_WORKLOAD_OPENBAO_URL"), "OPENBAO_CA_FILE": os.Getenv("SP03_TEST_WORKLOAD_OPENBAO_CA_FILE"), "OPENBAO_SERVICE_DOMAIN": reviewWorkloadNamespace + ".svc.cluster.local", "OPENBAO_PROJECTED_TOKEN_FILE": filepath.Join(os.Getenv("SP03_TEST_KUBERNETES_TOKEN_DIR"), "ops-worker"), "S3_BUCKET": bucket, "S3_ACCESS_KEY": os.Getenv("SP03_TEST_S3_ACCESS_KEY"), "S3_SECRET_KEY": os.Getenv("SP03_TEST_S3_SECRET_KEY")} {
		t.Setenv(key, value)
	}
	application, _ := app.NewWorker(app.AppConfig{DatabaseURL: u.String(), OIDCIssuerURL: "isolated-not-used-by-audit", ProfilePath: "isolated-core"})
	runtime, _ := observability.NewRuntime(ctx, "review-worker")
	defer runtime.Close(ctx)
	runtime.Logger = observability.NewLogger(io.Discard, slog.LevelInfo)
	start := func() (context.CancelFunc, chan error) {
		cctx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- application.Serve(cctx, runtime) }()
		return cancel, done
	}
	wait := func(query string, want int, done chan error) {
		t.Helper()
		for deadline := time.Now().Add(25 * time.Second); time.Now().Before(deadline); {
			var count int
			if err := db.QueryRowContext(ctx, query).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count == want {
				return
			}
			select {
			case err := <-done:
				t.Fatalf("worker stopped before acceptance: %v", err)
			default:
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("worker condition did not reach %d", want)
	}
	t.Setenv("S3_ENDPOINT", "http://127.0.0.1:1")
	cancel, done := start()
	defer func() { cancel() }()
	wait(`SELECT CASE WHEN count(*) >= 1 THEN 1 ELSE 0 END FROM audit.signed_segments WHERE status='pending_signature'`, 1, done)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	t.Setenv("S3_ENDPOINT", os.Getenv("SP03_TEST_S3_ENDPOINT"))
	cancel, done = start()
	wait(`SELECT count(*) FROM audit.signed_segments WHERE status='signed'`, 2, done)
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var pending int
	var delay float64
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM audit.signed_segments WHERE status='pending_signature'`).Scan(&pending); err != nil || pending != 0 {
		t.Fatal("permanent pending signature remains")
	}
	if err := db.QueryRowContext(ctx, `SELECT max(EXTRACT(EPOCH FROM s.signed_at-r.created_at))::double precision FROM audit.signed_segments s JOIN audit.records r ON r.tenant_id=s.tenant_id AND r.audit_seq BETWEEN s.first_audit_seq AND s.last_audit_seq`).Scan(&delay); err != nil || delay >= 600 {
		t.Fatalf("audit signature delay=%f seconds err=%v", delay, err)
	}
	store, _ := archive.NewStore(backend, 0)
	protector, _ := crypto.NewTransitProtector(bao, "evidence-archive")
	verifier, _ := audit.NewSegmentService(pool, store, protector, bao, "audit-signing", 30*24*time.Hour)
	for _, tenant := range tenants {
		if err := verifier.VerifyRange(ctx, tenant, sequences[tenant], sequences[tenant]); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("actual WorkerApp used projected-token login and worker-only DB login; two tenants resumed after S3 refusal; pending=0 max_proof_delay_seconds=%.3f bucket=%s", delay, bucket)
}
