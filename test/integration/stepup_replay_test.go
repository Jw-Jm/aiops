package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"ops-platform/internal/auth"
	"ops-platform/internal/persistence"
)

func TestRevokedStepUpCannotBeRecreatedByReplayingSameJWT(t *testing.T) {
	ctx, db, dir, dsn := newMigrationDatabase(t)
	if err := goose.UpContext(ctx, db, dir); err != nil {
		t.Fatal(err)
	}
	tenant := uuid.Must(uuid.NewV7())
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.tenants(tenant_id,slug,display_name) VALUES($1,$2,'review')`, tenant, tenant.String()); err != nil {
		t.Fatal(err)
	}
	cfg, _ := pgxpool.ParseConfig(dsn)
	cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		_, err := c.Exec(ctx, `SET ROLE api_runtime_role`)
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	request := auth.RequestContext{TenantID: tenant, Subject: "review", KeycloakSID: "same-sid", ACR: auth.StepUpACRLevel2, AuthTime: time.Now().UTC().Add(-2 * time.Second).Truncate(time.Second)}
	record := func() error {
		return persistence.WithTenantTx(ctx, pool, tenant, func(tx pgx.Tx) error {
			_, err := auth.RecordStepUpSession(ctx, tx, request, uuid.Must(uuid.NewV7()), []string{auth.StepUpACRLevel2})
			return err
		})
	}
	if err := record(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE platform.step_up_sessions SET revoked_at=clock_timestamp() WHERE tenant_id=$1`, tenant); err != nil {
		t.Fatal(err)
	}
	if err := record(); !errors.Is(err, auth.ErrStepUpInvalid) {
		t.Fatalf("replayed revoked proof returned %v", err)
	}
	request.AuthTime = request.AuthTime.Add(time.Second)
	if err := record(); err != nil {
		t.Fatalf("fresh reauthentication rejected: %v", err)
	}
}
