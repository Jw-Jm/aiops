package integration

import (
	"github.com/pressly/goose/v3"
	"testing"
)

// The bootstrap identity owns version 1; the restricted migration role must
// carry an existing version-63 database through every new forward migration.
func TestMainReviewMigration63To69(t *testing.T) {
	ctx, db, dir, dsn := newMigrationDatabase(t)
	if err := goose.UpToContext(ctx, db, dir, 1); err != nil {
		t.Fatal(err)
	}
	var database string
	if err := db.QueryRowContext(ctx, `SELECT current_database()`).Scan(&database); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `GRANT CREATE ON DATABASE "`+database+`" TO schema_owner`); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpToContext(ctx, db, dir, 63); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM goose_db_version WHERE is_applied AND version_id BETWEEN 1 AND 63`).Scan(&before); err != nil || before != 63 {
		t.Fatalf("baseline has %d migrations: %v", before, err)
	}
	if err := runRemainingMigrationsAsMigrationRole(t, ctx, db, dsn, dir); err != nil {
		t.Fatal(err)
	}
	migrations, err := goose.CollectMigrations(dir, 0, goose.MaxVersion)
	if err != nil || len(migrations) == 0 {
		t.Fatalf("collect delivered migrations: %v", err)
	}
	want := migrations[len(migrations)-1].Version
	var latest, count int64
	if err := db.QueryRowContext(ctx, `SELECT max(version_id),count(*) FROM goose_db_version WHERE is_applied AND version_id>0`).Scan(&latest, &count); err != nil || latest != want || count != want {
		t.Fatalf("forward result %d/%d, want %d: %v", latest, count, want, err)
	}
}
