package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"ops-platform/internal/auth"
	"ops-platform/internal/persistence"
	"ops-platform/internal/tenant"
)

func TestTenantProvisioningAndRoleBindingRevisions(t *testing.T) {
	ctx, db, migrationDir, dbURL := newMigrationDatabase(t)
	if err := goose.UpToContext(ctx, db, migrationDir, 1); err != nil {
		t.Fatalf("apply privileged role bootstrap: %v", err)
	}
	if err := runRemainingMigrationsAsMigrationRole(t, ctx, db, dbURL, migrationDir); err != nil {
		t.Fatalf("apply current schema as migration_role: %v", err)
	}

	const actorTenant = "018f0f2b-91c2-7d42-a8dc-f719c5987301"
	const actorBinding = "018f0f2b-91c2-7d42-a8dc-f719c5987302"
	const clusterID = "018f0f2b-91c2-7d42-a8dc-f719c5987303"
	actorTenantID, actorBindingID, clusterUUID := uuid.MustParse(actorTenant), uuid.MustParse(actorBinding), uuid.MustParse(clusterID)
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.tenants (tenant_id, slug, display_name) VALUES ($1, 'ops-admin', 'Operations')`, actorTenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.role_bindings (tenant_id, binding_id, subject, role_name) VALUES ($1, $2, 'admin-subject', 'platform_admin')`, actorTenantID, actorBindingID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.cluster_registrations (tenant_id, cluster_id, cluster_uid, display_name) VALUES ($1, $2, 'integration-cluster', 'Integration Cluster')`, actorTenantID, clusterUUID); err != nil {
		t.Fatal(err)
	}

	poolConfig, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		t.Fatal(err)
	}
	poolConfig.MaxConns = 1
	poolConfig.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, `SET ROLE api_runtime_role`)
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	service, err := tenant.NewService(pool)
	if err != nil {
		t.Fatal(err)
	}
	actor := auth.RequestContext{RequestID: "tenant-integration", TenantID: actorTenantID, Subject: "admin-subject", Roles: []auth.Role{auth.PlatformAdmin}}

	var created tenant.Tenant
	if err := persistence.WithTenantTx(ctx, pool, actorTenantID, func(tx pgx.Tx) error {
		var err error
		created, err = service.CreateTenant(ctx, tx, actor, tenant.CreateTenantInput{Slug: "acme-lab", DisplayName: "Acme Lab"})
		return err
	}); err != nil {
		t.Fatalf("create tenant with initial administrator binding: %v", err)
	}
	if created.ID == uuid.Nil || created.Revision != 1 {
		t.Fatalf("unexpected created tenant: %#v", created)
	}
	var provisionedAdmins int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM platform.role_bindings WHERE tenant_id = $1 AND subject = 'admin-subject' AND role_name = 'platform_admin' AND status = 'active'`, created.ID).Scan(&provisionedAdmins); err != nil {
		t.Fatal(err)
	}
	if provisionedAdmins != 1 {
		t.Fatalf("new tenant has %d initial administrator bindings", provisionedAdmins)
	}

	input := tenant.CreateRoleBindingInput{
		Subject: "operator-before", Role: auth.Operator, ClusterScopes: []uuid.UUID{clusterUUID},
		NamespaceScopes: []auth.NamespaceScope{{ClusterID: clusterUUID, Namespace: "production"}},
	}
	var binding tenant.RoleBinding
	if err := persistence.WithTenantTx(ctx, pool, actorTenantID, func(tx pgx.Tx) error {
		var err error
		binding, err = service.CreateRoleBinding(ctx, tx, actor, input)
		return err
	}); err != nil {
		t.Fatalf("create scoped operator binding: %v", err)
	}
	if binding.Revision != 1 || len(binding.ClusterScopes) != 1 || len(binding.NamespaceScopes) != 1 {
		t.Fatalf("created binding lost its explicit scopes: %#v", binding)
	}

	updatedInput := input
	updatedInput.Subject = "operator-after"
	var updated tenant.RoleBinding
	if err := persistence.WithTenantTx(ctx, pool, actorTenantID, func(tx pgx.Tx) error {
		var err error
		updated, err = service.UpdateRoleBinding(ctx, tx, actor, binding.ID, binding.Revision, updatedInput, "active")
		return err
	}); err != nil {
		t.Fatalf("update role binding at expected revision: %v", err)
	}
	if updated.Revision != 2 || updated.Subject != "operator-after" {
		t.Fatalf("role binding revision did not advance: %#v", updated)
	}
	if err := persistence.WithTenantTx(ctx, pool, actorTenantID, func(tx pgx.Tx) error {
		_, err := service.UpdateRoleBinding(ctx, tx, actor, binding.ID, binding.Revision, updatedInput, "active")
		return err
	}); !errors.Is(err, tenant.ErrRevisionConflict) {
		t.Fatalf("stale role binding update returned %v, want revision conflict", err)
	}

	var beforeClusters json.RawMessage
	if err := db.QueryRowContext(ctx, `SELECT record->'before'->'cluster_scopes' FROM audit.records WHERE tenant_id = $1 AND entity_id = $2 AND event_type = 'role_binding.updated'`, actorTenantID, binding.ID).Scan(&beforeClusters); err != nil {
		t.Fatal(err)
	}
	var decodedClusters []uuid.UUID
	if err := json.Unmarshal(beforeClusters, &decodedClusters); err != nil || len(decodedClusters) != 1 || decodedClusters[0] != clusterUUID {
		t.Fatalf("audit before scope was not stored as structured JSON: raw=%s decoded=%v err=%v", beforeClusters, decodedClusters, err)
	}

	var revised tenant.Tenant
	if err := persistence.WithTenantTx(ctx, pool, actorTenantID, func(tx pgx.Tx) error {
		var err error
		revised, err = service.UpdateCurrentTenant(ctx, tx, actor, created.Revision, "Acme Lab Operations", "active")
		return err
	}); err != nil {
		t.Fatalf("update tenant at expected revision: %v", err)
	}
	if revised.Revision != created.Revision+1 || revised.DisplayName != "Acme Lab Operations" {
		t.Fatalf("tenant revision did not advance: %#v", revised)
	}
	if err := persistence.WithTenantTx(ctx, pool, actorTenantID, func(tx pgx.Tx) error {
		_, err := service.UpdateCurrentTenant(ctx, tx, actor, created.Revision, "stale", "active")
		return err
	}); !errors.Is(err, tenant.ErrRevisionConflict) {
		t.Fatalf("stale tenant update returned %v, want revision conflict", err)
	}

	if err := persistence.WithTenantTx(ctx, pool, actorTenantID, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SAVEPOINT direct_tenant_write`); err != nil {
			return err
		}
		_, directErr := tx.Exec(ctx, `UPDATE platform.tenants SET display_name = 'bypass' WHERE tenant_id = $1`, created.ID)
		if directErr == nil {
			return fmt.Errorf("runtime role directly updated a tenant row")
		}
		_, err := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT direct_tenant_write`)
		return err
	}); err != nil {
		t.Fatalf("runtime tenant write permission check: %v", err)
	}
}
