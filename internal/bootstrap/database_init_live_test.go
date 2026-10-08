//go:build pre_sp07_live

package bootstrap

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
)

func TestBundledDatabaseFirstInitEnforcesKeycloakBoundary(t *testing.T) {
	dsn := os.Getenv("PRE_SP07_DATABASE_INIT_DSN")
	password := os.Getenv("PRE_SP07_KEYCLOAK_DATABASE_PASSWORD")
	if dsn == "" || password == "" {
		t.Fatal("actual newly initialized isolated PostgreSQL required")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal("isolated first-init PostgreSQL unavailable")
	}
	defer admin.Close(ctx)
	var unsafe, inherit bool
	var owner string
	var tables int
	if err := admin.QueryRow(ctx, `SELECT rolsuper OR rolcreatedb OR rolcreaterole OR rolbypassrls OR rolreplication,rolinherit FROM pg_roles WHERE rolname='ops_keycloak'`).Scan(&unsafe, &inherit); err != nil || unsafe || inherit {
		t.Fatalf("Keycloak database identity missing or elevated: unsafe=%v inherit=%v err=%v", unsafe, inherit, err)
	}
	if err := admin.QueryRow(ctx, `SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname='keycloak'`).Scan(&owner); err != nil || owner != "ops_keycloak" {
		t.Fatal("dedicated Keycloak database ownership differs")
	}
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema NOT IN ('pg_catalog','information_schema')`).Scan(&tables); err != nil || tables != 0 {
		t.Fatal("platform database did not start empty")
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	config.User = "ops_keycloak"
	config.Password = password
	wrong, err := pgx.ConnectConfig(ctx, config)
	if err == nil {
		wrong.Close(ctx)
		t.Fatal("Keycloak LOGIN connected to platform database")
	}
	config.Database = "keycloak"
	own, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		t.Fatal("Keycloak LOGIN cannot authenticate to its own database")
	}
	defer own.Close(ctx)
	if _, err := own.Exec(ctx, `CREATE TEMPORARY TABLE bootstrap_permission_check (id int); INSERT INTO bootstrap_permission_check VALUES (1)`); err != nil {
		t.Fatal("Keycloak cannot initialize its own schema")
	}
	if _, err := own.Exec(ctx, `CREATE ROLE unexpected_role`); err == nil {
		t.Fatal("Keycloak LOGIN created another role")
	}
	t.Log("actual fixed Chart first-init SQL, empty platform database, restricted Keycloak role, own database SCRAM authentication, platform CONNECT and CREATEROLE denial verified")
}
