package integration

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"ops-platform/internal/app"
	"ops-platform/internal/finding"
	"ops-platform/internal/incident"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestSP05OutboxChildProcessClaim(t *testing.T) {
	if os.Getenv("OPS_SP05_CRASH_CHILD") != "1" {
		return
	}
	ctx := context.Background()
	pool, err := app.OpenRuntimePool(ctx, os.Getenv("OPS_SP05_CRASH_DSN"), "worker_runtime_role")
	if err != nil {
		t.Fatal("child runtime connection unavailable")
	}
	defer pool.Close()
	tenant, err := uuid.Parse(os.Getenv("OPS_SP05_CRASH_TENANT"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := (finding.Service{Pool: pool}).Claim(ctx, tenant)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(d)
	if err := os.WriteFile(os.Getenv("OPS_SP05_CRASH_MARKER"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	select {}
}

func TestSP05SIGKILLClaimLeaseRecoverExactlyOnce(t *testing.T) {
	ctx, db, pool, b := sp05Database(t)
	s := finding.Service{Pool: pool}
	f, _, err := s.Ingest(ctx, b, sp05Envelope(b, "process-crash", "process-occurrence"))
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "claimed.json")
	cmd := exec.Command(os.Args[0], "-test.run=^TestSP05OutboxChildProcessClaim$", "-test.timeout=1m")
	cmd.Env = append(os.Environ(), "OPS_SP05_CRASH_CHILD=1", "OPS_SP05_CRASH_DSN="+pool.Config().ConnConfig.ConnString(), "OPS_SP05_CRASH_TENANT="+b.TenantID.String(), "OPS_SP05_CRASH_MARKER="+marker)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	var raw []byte
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		raw, err = os.ReadFile(marker)
		if err == nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal("child did not commit its claim")
	}
	var claimed finding.Delivery
	if json.Unmarshal(raw, &claimed) != nil {
		t.Fatal("child claim marker")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	var stopped *exec.ExitError
	if err := cmd.Wait(); !errors.As(err, &stopped) || !stopped.ProcessState.Exited() && stopped.ProcessState.String() != "signal: killed" {
		t.Fatalf("expected killed subprocess: %v", err)
	}
	if err := s.RelayPass(ctx, b.TenantID, incident.Consume, 10); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM incident.finding_links WHERE tenant_id=$1 AND finding_id=$2`, b.TenantID, f.FindingID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("live claim lease stolen: count=%d err=%v", count, err)
	}
	// This waits for the actual production lease; no shortened test threshold.
	time.Sleep(31 * time.Second)
	if err := s.RelayPass(ctx, b.TenantID, incident.Consume, 10); err != nil {
		t.Fatal(err)
	}
	if err := s.RelayPass(ctx, b.TenantID, incident.Consume, 10); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`SELECT count(*) FROM incident.finding_links WHERE tenant_id=$1 AND finding_id=$2`, `SELECT count(*) FROM incident.inbox WHERE tenant_id=$1 AND finding_id=$2`} {
		if err := db.QueryRowContext(ctx, query, b.TenantID, f.FindingID).Scan(&count); err != nil || count != 1 {
			t.Fatalf("crash replay not exactly once: count=%d err=%v", count, err)
		}
	}
	if err := s.Deliver(ctx, claimed, incident.Consume); err == nil {
		t.Fatal("pre-crash token committed after recovery")
	}
	t.Log("real child SIGKILL after committed outbox claim; unchanged 30-second lease recovered exactly once; stale claim token rejected")
}
