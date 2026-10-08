package integration

import (
	"context"
	"database/sql"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"ops-platform/internal/app"
)

// Fixture setup uses the isolated administrator; the business pool connects
// with an actual API-only or Worker-only LOGIN and production privilege checks.
func runtimePoolConfig(t *testing.T, ctx context.Context, admin *sql.DB, dsn, role string) *pgxpool.Config {
	t.Helper()
	if role != "api_runtime_role" && role != "worker_runtime_role" {
		t.Fatal("unsupported runtime role")
	}
	login := "review_runtime_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	password := strings.ReplaceAll(uuid.NewString()+uuid.NewString(), "-", "")
	if _, err := admin.ExecContext(ctx, `CREATE ROLE "`+login+`" LOGIN PASSWORD '`+password+`'; GRANT `+role+` TO "`+login+`"`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.ExecContext(context.Background(), `DROP ROLE "`+login+`"`); err != nil {
			t.Errorf("remove isolated runtime LOGIN: %v", err)
		}
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword(login, password)
	checked, err := app.OpenRuntimePool(ctx, u.String(), role)
	if err != nil {
		t.Fatal(err)
	}
	config := checked.Config()
	checked.Close()
	return config
}
