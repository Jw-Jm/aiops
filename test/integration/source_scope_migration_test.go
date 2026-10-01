package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"ops-platform/internal/auth"
	"ops-platform/internal/source"
)

func TestSourceScopeMigrationPreservesLegacyIdentityWithoutQueryGrants(t *testing.T) {
	ctx, db, directory, dsn := newMigrationDatabase(t)
	if err := goose.UpToContext(ctx, db, directory, 1); err != nil {
		t.Fatal(err)
	}
	legacyDirectory := t.TempDir()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() >= "00017" || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(legacyDirectory, entry.Name()), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := runRemainingMigrationsAsMigrationRole(t, ctx, db, dsn, legacyDirectory); err != nil {
		t.Fatal(err)
	}
	tenantID, sourceID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.tenants(tenant_id,slug,display_name) VALUES($1,'legacy-scope','Legacy Scope')`, tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.role_bindings(tenant_id,binding_id,subject,role_name) VALUES($1,$2,'scope-admin','platform_admin')`, tenantID, uuid.Must(uuid.NewV7())); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.source_registrations(tenant_id,source_id,source_type,instance_key,auth_ref,allowed_schemas) VALUES($1,$2,'victoriametrics','legacy-metrics','openbao://kv/source/legacy','{finding-envelope/v1}')`, tenantID, sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.source_registration_revisions(tenant_id,source_id,revision,auth_ref,credential_revision,status,scope,actor_subject) VALUES($1,$2,1,'openbao://kv/source/legacy',1,'active','{"source_type":"victoriametrics","instance_key":"legacy-metrics","allowed_schemas":["finding-envelope/v1"]}','scope-admin')`, tenantID, sourceID); err != nil {
		t.Fatal(err)
	}
	if err := runRemainingMigrationsAsMigrationRole(t, ctx, db, dsn, directory); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, runtimePoolConfig(t, ctx, db, dsn, "api_runtime_role"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	service, err := source.NewService(pool, nil)
	if err != nil {
		t.Fatal(err)
	}
	actor := auth.RequestContext{TenantID: tenantID, Subject: "scope-admin", Roles: []auth.Role{auth.PlatformAdmin}}
	legacy, err := service.ListSources(ctx, actor)
	if err != nil || len(legacy) != 1 || legacy[0].SourceID != sourceID || legacy[0].Revision != 1 || legacy[0].BackendLogicalID != "" || len(legacy[0].DataScopeMapping.Scopes) != 0 {
		t.Fatalf("migration invented scope or changed identity: sources=%#v err=%v", legacy, err)
	}
	backend := "backend-legacy"
	mapping := source.DataScopeMapping{NativeTenant: "account-a", Scopes: map[string][]string{"namespace": {"prod"}}}
	completed, err := updateSourceInTenant(ctx, pool, service, actor, sourceID, source.SourceUpdateCommand{ExpectedRevision: 1, BackendLogicalID: &backend, DataScopeMapping: &mapping})
	if err != nil || completed.Revision != 2 {
		t.Fatalf("audited scope completion failed: %v", err)
	}
	restored, err := rollbackSourceInTenant(ctx, pool, service, actor, sourceID, source.SourceRegistrationRollbackCommand{ExpectedRevision: 2, TargetRevision: 1})
	if err != nil || restored.BackendLogicalID != backend || restored.SourceID != sourceID || len(restored.DataScopeMapping.Scopes) != 0 {
		t.Fatalf("legacy rollback erased stable backend or invented query scope: source=%#v err=%v", restored, err)
	}
}
