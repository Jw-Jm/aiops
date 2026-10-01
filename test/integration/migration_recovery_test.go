package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
)

func TestFailedForwardMigrationRollsBackAndCanRecover(t *testing.T) {
	ctx, db, dir, dsn := newMigrationDatabase(t)
	if err := goose.UpToContext(ctx, db, dir, 1); err != nil {
		t.Fatal(err)
	}
	if err := runRemainingMigrationsAsMigrationRole(t, ctx, db, dsn, dir); err != nil {
		t.Fatal(err)
	}
	var version int64
	if err := db.QueryRowContext(ctx, `SELECT max(version_id) FROM public.goose_db_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	migrations, err := goose.CollectMigrations(dir, 0, goose.MaxVersion)
	if err != nil || len(migrations) == 0 {
		t.Fatalf("collect delivered migrations: %v", err)
	}
	if latest := migrations[len(migrations)-1].Version; version != latest {
		t.Fatalf("database has not applied the latest delivered migration: got %d want %d", version, latest)
	}
	scratch := t.TempDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(scratch, entry.Name()), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	candidate := filepath.Join(scratch, fmt.Sprintf("%05d_review_failure.sql", version+1))
	source := "-- +goose Up\nSET ROLE schema_owner;\nCREATE TABLE platform.review_failure(value integer);\nSELECT 1/0;\nRESET ROLE;\n"
	if err := os.WriteFile(candidate, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runRemainingMigrationsAsMigrationRole(t, ctx, db, dsn, scratch); err == nil {
		t.Fatal("invalid migration succeeded")
	}
	var exists bool
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('platform.review_failure') IS NOT NULL`).Scan(&exists); err != nil || exists {
		t.Fatalf("failed migration left schema side effects: %v %v", exists, err)
	}
	var after int64
	if err := db.QueryRowContext(ctx, `SELECT max(version_id) FROM public.goose_db_version`).Scan(&after); err != nil || after != version {
		t.Fatal("failed migration changed applied version")
	}
	source = "-- +goose Up\nSET ROLE schema_owner;\nCREATE TABLE platform.review_failure(value integer);\nRESET ROLE;\n"
	if err := os.WriteFile(candidate, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runRemainingMigrationsAsMigrationRole(t, ctx, db, dsn, scratch); err != nil {
		t.Fatal(err)
	}
	t.Log("deliberate SQL division-by-zero rolled back DDL/version; corrected unapplied forward migration succeeded as migration_role")
}
