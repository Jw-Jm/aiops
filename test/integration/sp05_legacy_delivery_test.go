package integration

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/finding"
	"ops-platform/internal/incident"
	"testing"
)

func TestSP05LegacyOutboxIsPreservedAndExplicitlyQuarantined(t *testing.T) {
	ctx, db, pool, b := sp05Database(t)
	fs := finding.Service{Pool: pool}
	f, _, err := fs.Ingest(ctx, b, sp05Envelope(b, "current-v2", "current-v2"))
	if err != nil {
		t.Fatal(err)
	}
	if err = fs.RelayPass(ctx, b.TenantID, incident.Consume, 10); err != nil {
		t.Fatal(err)
	}
	event := uuid.New()
	original := `{"schemaVersion":"finding/v1","legacyEvidence":"preserve original payload"}`
	if _, err = db.ExecContext(ctx, `INSERT INTO finding.outbox(tenant_id,outbox_id,finding_id,event_type,schema_version,payload) VALUES($1,$2,$3,'finding.changed','finding/v1',$4)`, b.TenantID, event, f.FindingID, original); err != nil {
		t.Fatal(err)
	}
	d, err := fs.Claim(ctx, b.TenantID)
	if err != nil {
		t.Fatalf("historical delivery silently unclaimed: %v", err)
	}
	called := false
	if err = fs.Deliver(ctx, d, func(ctx context.Context, tx pgx.Tx, d finding.Delivery) error { called = true; return nil }); !errors.Is(err, finding.ErrInvalid) || called {
		t.Fatalf("legacy event became current proven correlation: err=%v called=%t", err, called)
	}
	var state, code, raw string
	if err = db.QueryRowContext(ctx, `SELECT state,last_error_code,payload->>'legacyEvidence' FROM finding.outbox WHERE tenant_id=$1 AND outbox_id=$2`, b.TenantID, event).Scan(&state, &code, &raw); err != nil || state != "deadletter" || code != "UNSUPPORTED_HISTORICAL_SCHEMA" || raw != "preserve original payload" {
		t.Fatalf("legacy proof falsely delivered/lost: %s %s %s %v", state, code, raw, err)
	}
	if _, _, err = fs.Ingest(ctx, b, sp05Envelope(b, "next-v2", "next-v2")); err != nil {
		t.Fatal(err)
	}
	if err = fs.RelayPass(ctx, b.TenantID, incident.Consume, 10); err != nil {
		t.Fatal(err)
	}
	var pending int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM finding.outbox WHERE tenant_id=$1 AND state IN ('pending','claimed')`, b.TenantID).Scan(&pending); err != nil || pending != 0 {
		t.Fatal("historical quarantine blocked current deliveries")
	}
}
