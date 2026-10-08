package integration

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"ops-platform/internal/bootstrap"
)

func TestPreSP07DatabaseBootstrapCLIProvidesThreeRestrictedLOGINs(t *testing.T) {
	if os.Getenv("SP03_TEST_DATABASE_URL") == "" {
		t.Fatal("actual isolated PostgreSQL required")
	}
	ctx, db, dir, dsn := newMigrationDatabase(t)
	if err := goose.UpToContext(ctx, db, dir, 1); err != nil {
		t.Fatal(err)
	}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	private := t.TempDir()
	binary := filepath.Join(private, "opsctl")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/opsctl")
	build.Dir = filepath.Join("..", "..")
	if raw, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build formal CLI: %v: %s", err, raw)
	}
	config := bootstrap.DatabaseLogins{SchemaVersion: 1, Migration: bootstrap.DatabaseLogin{Name: "m_" + suffix, Password: uuid.NewString() + uuid.NewString()}, API: bootstrap.DatabaseLogin{Name: "a_" + suffix, Password: uuid.NewString() + uuid.NewString()}, Worker: bootstrap.DatabaseLogin{Name: "w_" + suffix, Password: uuid.NewString() + uuid.NewString()}}
	input := filepath.Join(private, "logins.json")
	raw, _ := json.Marshal(config)
	if err := os.WriteFile(input, raw, 0600); err != nil {
		t.Fatal(err)
	}
	invoke := func() ([]byte, error) {
		cmd := exec.CommandContext(ctx, binary, "bootstrap", "database-logins", "--secrets-file", input)
		cmd.Env = append(os.Environ(), "OPS_BOOTSTRAP_DATABASE_URL="+dsn)
		return cmd.CombinedOutput()
	}
	out, err := invoke()
	if err != nil {
		t.Fatalf("formal bootstrap failed (private diagnostics retained by test): %v", err)
	}
	for _, login := range []bootstrap.DatabaseLogin{config.Migration, config.API, config.Worker} {
		if bytes.Contains(out, []byte(login.Password)) {
			t.Fatal("CLI leaked a password")
		}
	}
	if _, err := invoke(); err == nil {
		t.Fatal("repeated role bootstrap accepted")
	}
	for _, duty := range []struct {
		Login bootstrap.DatabaseLogin
		Role  string
	}{{config.Migration, "migration_role"}, {config.API, "api_runtime_role"}, {config.Worker, "worker_runtime_role"}} {
		cfg, err := pgx.ParseConfig(dsn)
		if err != nil {
			t.Fatal(err)
		}
		cfg.User = duty.Login.Name
		cfg.Password = duty.Login.Password
		conn, err := pgx.ConnectConfig(ctx, cfg)
		if err != nil {
			t.Fatal("native SCRAM authentication failed")
		}
		var elevated, inherit bool
		var memberships int
		err = conn.QueryRow(ctx, `SELECT rolsuper OR rolbypassrls OR rolcreaterole OR rolcreatedb OR rolreplication,rolinherit,(SELECT count(*) FROM pg_auth_members WHERE member=pg_roles.oid) FROM pg_roles WHERE rolname=session_user`).Scan(&elevated, &inherit, &memberships)
		if err != nil || elevated || inherit || memberships != 1 {
			t.Fatalf("LOGIN privileges elevated=%v inherit=%v memberships=%d err=%v", elevated, inherit, memberships, err)
		}
		if _, err = conn.Exec(ctx, "SET ROLE "+pgx.Identifier{duty.Role}.Sanitize()); err != nil {
			t.Fatal("designated duty unavailable")
		}
		other := "migration_role"
		if duty.Role == "migration_role" {
			other = "api_runtime_role"
		}
		if _, err = conn.Exec(ctx, "SET ROLE "+pgx.Identifier{other}.Sanitize()); err == nil {
			t.Fatal("LOGIN acquired another process duty")
		}
		conn.Close(ctx)
		cfg.Password = "incorrect-private-credential"
		wrong, err := pgx.ConnectConfig(ctx, cfg)
		if err == nil {
			wrong.Close(ctx)
			t.Fatal("wrong SCRAM password accepted")
		}
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.User = config.Migration.Name
	cfg.Password = config.Migration.Password
	cfg.RuntimeParams["role"] = "migration_role"
	migrator := stdlib.OpenDB(*cfg)
	defer migrator.Close()
	if err := goose.UpContext(ctx, migrator, dir); err != nil {
		t.Fatal("restricted LOGIN could not complete forward migrations")
	}
	t.Cleanup(func() {
		for _, login := range []bootstrap.DatabaseLogin{config.Migration, config.API, config.Worker} {
			_, _ = db.ExecContext(ctx, "DROP OWNED BY "+pgx.Identifier{login.Name}.Sanitize())
			_, _ = db.ExecContext(ctx, "DROP ROLE "+pgx.Identifier{login.Name}.Sanitize())
		}
	})
	t.Log("formal opsctl, native SCRAM success/wrong credential rejection, three separated restricted LOGIN duties, repeat refusal, forward migration verified")
}
