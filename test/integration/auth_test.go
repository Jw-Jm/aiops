package integration

import (
	"context"
	"encoding/json"
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

func TestRoleBindingsAndStepUpSessions(t *testing.T) {
	ctx, db, migrationDir, dbURL := newMigrationDatabase(t)
	if err := goose.UpToContext(ctx, db, migrationDir, 1); err != nil {
		t.Fatalf("apply privileged role bootstrap: %v", err)
	}
	if err := runRemainingMigrationsAsMigrationRole(t, ctx, db, dbURL, migrationDir); err != nil {
		t.Fatalf("apply current schema as migration_role: %v", err)
	}
	tenantA := uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987351")
	tenantB := uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987352")
	clusterA := uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987353")
	for _, tenantID := range []uuid.UUID{tenantA, tenantB} {
		if _, err := db.ExecContext(ctx, "INSERT INTO platform.tenants (tenant_id, slug, display_name) VALUES ($1, $2, $2)", tenantID, tenantID.String()); err != nil {
			t.Fatalf("create tenant: %v", err)
		}
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO platform.cluster_registrations (tenant_id, cluster_id, cluster_uid, display_name) VALUES ($1, $2, 'cluster-uid-a', 'cluster-a')", tenantA, clusterA); err != nil {
		t.Fatalf("create cluster scope: %v", err)
	}
	clusterScopes, _ := json.Marshal([]string{clusterA.String()})
	namespaceScopes, _ := json.Marshal([]auth.NamespaceScope{{ClusterID: clusterA, Namespace: "production"}})
	_, err := db.ExecContext(ctx, "INSERT INTO platform.role_bindings (tenant_id, binding_id, subject, role_name, cluster_scopes, namespace_scopes) VALUES "+
		"($1, $2, 'operator-a', 'operator', $3::jsonb, $4::jsonb), "+
		"($1, $5, 'admin-a', 'platform_admin', '[]'::jsonb, '[]'::jsonb), "+
		"($1, $6, 'disabled-a', 'operator', '[]'::jsonb, '[]'::jsonb)",
		tenantA, uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987354"), string(clusterScopes), string(namespaceScopes),
		uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987355"),
		uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987356"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE platform.role_bindings SET status = 'disabled' WHERE tenant_id = $1 AND subject = 'disabled-a'", tenantA); err != nil {
		t.Fatal(err)
	}

	poolConfig, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		t.Fatal(err)
	}
	poolConfig.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE api_runtime_role")
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	source, err := auth.NewPostgreSQLRoleBindingSource(pool)
	if err != nil {
		t.Fatal(err)
	}
	operatorBindings, err := source.LoadRoleBindings(ctx, tenantA, "operator-a")
	if err != nil || len(operatorBindings) != 1 || operatorBindings[0].Role != auth.Operator ||
		len(operatorBindings[0].ClusterScopes) != 1 || operatorBindings[0].ClusterScopes[0] != clusterA ||
		len(operatorBindings[0].NamespaceScopes) != 1 || operatorBindings[0].NamespaceScopes[0].Namespace != "production" {
		t.Fatalf("operator binding scopes are not loaded from the selected tenant: bindings=%#v err=%v", operatorBindings, err)
	}
	adminBindings, err := source.LoadRoleBindings(ctx, tenantA, "admin-a")
	if err != nil || len(adminBindings) != 1 || adminBindings[0].Role != auth.PlatformAdmin {
		t.Fatalf("platform_admin inherited an operator binding: bindings=%#v err=%v", adminBindings, err)
	}
	otherTenantBindings, err := source.LoadRoleBindings(ctx, tenantB, "operator-a")
	if err != nil || len(otherTenantBindings) != 0 {
		t.Fatalf("role binding crossed tenant boundary: bindings=%#v err=%v", otherTenantBindings, err)
	}
	disabledBindings, err := source.LoadRoleBindings(ctx, tenantA, "disabled-a")
	if err != nil || len(disabledBindings) != 0 {
		t.Fatalf("disabled role binding remained active: bindings=%#v err=%v", disabledBindings, err)
	}

	request := auth.RequestContext{
		RequestID: "step-up-test", TenantID: tenantA, Subject: "operator-a", KeycloakSID: "sid-step-up-a",
		ACR: "urn:ops:loa:2", AuthTime: time.Now().UTC().Truncate(time.Second),
	}
	sessionID := uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987357")
	var session auth.StepUpSession
	err = persistence.WithTenantTx(ctx, pool, tenantA, func(tx pgx.Tx) error {
		var recordErr error
		session, recordErr = auth.RecordStepUpSession(ctx, tx, request, sessionID, []string{"urn:ops:loa:2"})
		if recordErr != nil {
			return recordErr
		}
		session, recordErr = auth.TouchStepUpSession(ctx, tx, session, request)
		return recordErr
	})
	if err != nil {
		t.Fatalf("persist and atomically touch step-up session: %v", err)
	}
	if err := auth.ValidateStepUpForContext(session, request, session.LastUsedAt); err != nil {
		t.Fatalf("fresh persisted step-up session was rejected: %v", err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE platform.step_up_sessions SET created_at = clock_timestamp() - interval '61 minutes' WHERE tenant_id = $1 AND session_id = $2", tenantA, sessionID); err != nil {
		t.Fatal(err)
	}
	err = persistence.WithTenantTx(ctx, pool, tenantA, func(tx pgx.Tx) error {
		_, err := auth.TouchStepUpSession(ctx, tx, session, request)
		return err
	})
	if !errors.Is(err, auth.ErrStepUpInvalid) {
		t.Fatalf("absolutely expired step-up session returned %v", err)
	}
	var auditCount int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM audit.records WHERE tenant_id = $1 AND entity_kind = 'step_up_session'", tenantA).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 2 {
		t.Fatalf("step-up create/use audit count=%d, want 2", auditCount)
	}
}
