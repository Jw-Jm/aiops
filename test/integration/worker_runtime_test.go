package integration

import (
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
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
	var archiveCA []byte
	if path := os.Getenv("SP03_TEST_S3_CA_FILE"); path != "" {
		archiveCA, err = os.ReadFile(path)
		if err != nil {
			t.Fatal("read independent test archive CA")
		}
	}
	backend, err := s3.NewClient(s3.Config{CACertBundle: archiveCA, Endpoint: os.Getenv("SP03_TEST_S3_ENDPOINT"), Bucket: bucket, AccessKey: os.Getenv("SP03_TEST_S3_ACCESS_KEY"), SecretKey: os.Getenv("SP03_TEST_S3_SECRET_KEY")})
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.CreateBucket(ctx); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"OPENBAO_ADDR": os.Getenv("SP03_TEST_WORKLOAD_OPENBAO_URL"), "OPENBAO_CA_FILE": os.Getenv("SP03_TEST_WORKLOAD_OPENBAO_CA_FILE"), "OPENBAO_SERVICE_DOMAIN": reviewWorkloadNamespace + ".svc.cluster.local", "OPENBAO_PROJECTED_TOKEN_FILE": filepath.Join(os.Getenv("SP03_TEST_KUBERNETES_TOKEN_DIR"), "ops-worker"), "S3_CA_FILE": os.Getenv("SP03_TEST_S3_CA_FILE"), "S3_BUCKET": bucket, "S3_ACCESS_KEY": os.Getenv("SP03_TEST_S3_ACCESS_KEY"), "S3_SECRET_KEY": os.Getenv("SP03_TEST_S3_SECRET_KEY")} {
		t.Setenv(key, value)
	}
	// Exercise the delivered command in separate OS processes. The child gets
	// only its runtime credentials, never bootstrap/root test tokens.
	binary := filepath.Join(t.TempDir(), "platform-worker")
	build := exec.Command("go", "build", "-trimpath", "-o", binary, "./cmd/platform-worker")
	build.Dir = filepath.Dir(dir)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build locked worker command: %v: %s", err, output)
	}
	var childLogs []string
	start := func() (*exec.Cmd, chan error) {
		profilePath := isolatedRuntimeProfile(t, "", os.Getenv("OPENBAO_ADDR"), os.Getenv("S3_ENDPOINT"))
		t.Logf("start worker with archive endpoint=%s bucket=%s", os.Getenv("S3_ENDPOINT"), os.Getenv("S3_BUCKET"))
		command := exec.Command(binary)
		command.Env = []string{"DATABASE_URL=" + u.String(), "OIDC_ISSUER_URL=isolated-not-used-by-audit", "PLATFORM_PROFILE=" + profilePath, "PLATFORM_METRICS_ADDR=127.0.0.1:0"}
		for _, key := range []string{"OPENBAO_ADDR", "OPENBAO_CA_FILE", "OPENBAO_SERVICE_DOMAIN", "OPENBAO_PROJECTED_TOKEN_FILE", "S3_CA_FILE", "S3_BUCKET", "S3_ACCESS_KEY", "S3_SECRET_KEY", "S3_ENDPOINT"} {
			command.Env = append(command.Env, key+"="+os.Getenv(key))
		}
		logFile, err := os.CreateTemp(t.TempDir(), "worker-*.log")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = logFile.Close() })
		childLogs = append(childLogs, logFile.Name())
		command.Stdout, command.Stderr = logFile, logFile
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = command.Process.Kill() })
		done := make(chan error, 1)
		go func() { done <- command.Wait() }()
		return command, done
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
		for _, path := range childLogs {
			output, _ := os.ReadFile(path)
			t.Logf("worker process stdout: %s", output)
		}
		var pending, signed int
		_ = db.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE status='pending_signature'), count(*) FILTER (WHERE status='signed') FROM audit.signed_segments`).Scan(&pending, &signed)
		t.Fatalf("worker condition did not reach %d; pending=%d signed=%d", want, pending, signed)
	}
	t.Setenv("S3_ENDPOINT", "http://127.0.0.1:1")
	command, done := start()
	firstPID := command.Process.Pid
	wait(`SELECT CASE WHEN count(*) >= 1 THEN 1 ELSE 0 END FROM audit.signed_segments WHERE status='pending_signature'`, 1, done)
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	err = <-done
	var exit *exec.ExitError
	if !errors.As(err, &exit) || !exit.ProcessState.Sys().(syscall.WaitStatus).Signaled() || exit.ProcessState.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Fatalf("first worker was not killed by SIGKILL: %v", err)
	}
	t.Setenv("S3_ENDPOINT", os.Getenv("SP03_TEST_S3_ENDPOINT"))
	command, done = start()
	wait(`SELECT count(*) FROM audit.signed_segments WHERE status='signed'`, 2, done)
	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
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
	t.Logf("actual cmd/platform-worker OS processes used projected-token login and worker-only DB login=%s; first_pid=%d exit_signal=SIGKILL second_pid=%d exit_code=0; restart resumed tenants=%s,%s after S3 refusal; pending=0 max_proof_delay_seconds=%.3f bucket=%s", name, firstPID, command.Process.Pid, tenants[0], tenants[1], delay, bucket)
}
