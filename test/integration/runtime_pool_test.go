package integration

import (
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"ops-platform/internal/app"
)

func TestRuntimeDatabaseLoginCannotRegainOwnerOrOtherProcess(t *testing.T) {
	ctx, db, dir, dsn := newMigrationDatabase(t)
	if err := goose.UpContext(ctx, db, dir); err != nil {
		t.Fatal(err)
	}
	if pool, err := app.OpenRuntimePool(ctx, dsn, "api_runtime_role"); err == nil {
		pool.Close()
		t.Fatal("superuser login accepted after SET ROLE")
	}
	name := "sp03_runtime_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := db.ExecContext(ctx, `CREATE ROLE "`+name+`" LOGIN; GRANT api_runtime_role TO "`+name+`"`); err != nil {
		t.Fatal(err)
	}
	defer db.ExecContext(ctx, `DROP ROLE "`+name+`"`)
	u, _ := url.Parse(dsn)
	u.User = url.User(name)
	pool, err := app.OpenRuntimePool(ctx, u.String(), "api_runtime_role")
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()
	if _, err := db.ExecContext(ctx, `GRANT worker_runtime_role TO "`+name+`"`); err != nil {
		t.Fatal(err)
	}
	if pool, err := app.OpenRuntimePool(ctx, u.String(), "api_runtime_role"); err == nil {
		pool.Close()
		t.Fatal("shared API/worker login accepted")
	}
}
