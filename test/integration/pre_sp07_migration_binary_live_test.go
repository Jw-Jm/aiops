//go:build pre_sp07_live

package integration

import (
	"bytes"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"ops-platform/internal/bootstrap"
)

// The Linux binary must have been rebuilt from the reviewed source archive in
// a network=none container with an empty module cache. This gate uses an owned
// PostgreSQL network namespace and a new isolated database, never business seeds.
func TestPreSP07OfflineMigrationBinaryAndFormalLOGINBootstrap(t *testing.T) {
	if os.Getenv("SP03_TEST_DATABASE_URL") == "" {
		t.Fatal("actual isolated PostgreSQL required")
	}
	binary, container := os.Getenv("PRE_SP07_MIGRATION_BINARY"), os.Getenv("PRE_SP07_POSTGRES_CONTAINER")
	if !filepath.IsAbs(binary) || container == "" {
		t.Fatal("actual offline-rebuilt Linux binary and owned PostgreSQL container required")
	}
	inspect, err := exec.Command("docker", "inspect", container).Output()
	var live []struct {
		ID     string                             `json:"Id"`
		Config struct{ Labels map[string]string } `json:"Config"`
		State  struct{ Running bool }             `json:"State"`
	}
	if err != nil || json.Unmarshal(inspect, &live) != nil || len(live) != 1 || live[0].ID != container || !live[0].State.Running || live[0].Config.Labels["ops.platform.owner"] != "pre-sp07-20261004" || live[0].Config.Labels["ops.platform.purpose"] != "history-restore" {
		t.Fatal("PostgreSQL container ownership/running identity differs")
	}
	ctx, db, dir, dsn := newMigrationDatabase(t)
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal("isolated PostgreSQL URL invalid")
	}
	parsed.Host = "127.0.0.1:5432"
	private := t.TempDir()
	runMigration := func(databaseURL string, target string) ([]byte, error) {
		envFile := filepath.Join(private, "migration.env")
		if strings.ContainsAny(databaseURL, "\r\n") {
			t.Fatal("invalid private database URL")
		}
		if err := os.WriteFile(envFile, []byte("SP03_MIGRATION_DATABASE_URL="+databaseURL+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		migrations, err := filepath.Abs(dir)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "--pull=never", "--read-only", "--network=container:"+container, "--env-file", envFile,
			"--mount", "type=bind,source="+binary+",target=/db-migrate,readonly", "--mount", "type=bind,source="+migrations+",target=/migrations,readonly",
			"--entrypoint", "/db-migrate", "docker.io/library/postgres@sha256:75731e2765e7d0c8bb7dea960ef3bdcde68d16314991ab2057a2a74ea0fff257", "-dir", "/migrations", "-to", target)
		return cmd.CombinedOutput()
	}
	if out, err := runMigration(parsed.String(), "1"); err != nil {
		t.Fatalf("offline binary role bootstrap failed: %v: %s", err, out)
	}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	config := bootstrap.DatabaseLogins{SchemaVersion: 1, Migration: bootstrap.DatabaseLogin{Name: "m_" + suffix, Password: uuid.NewString() + uuid.NewString()}, API: bootstrap.DatabaseLogin{Name: "a_" + suffix, Password: uuid.NewString() + uuid.NewString()}, Worker: bootstrap.DatabaseLogin{Name: "w_" + suffix, Password: uuid.NewString() + uuid.NewString()}}
	input := filepath.Join(private, "logins.json")
	raw, _ := json.Marshal(config)
	if err := os.WriteFile(input, raw, 0600); err != nil {
		t.Fatal(err)
	}
	opsctl := filepath.Join(private, "opsctl")
	build := exec.CommandContext(ctx, "go", "build", "-o", opsctl, "./cmd/opsctl")
	build.Dir = "../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("formal CLI build: %v: %s", err, out)
	}
	cmd := exec.CommandContext(ctx, opsctl, "bootstrap", "database-logins", "--secrets-file", input)
	cmd.Env = append(os.Environ(), "OPS_BOOTSTRAP_DATABASE_URL="+dsn)
	if _, err := cmd.CombinedOutput(); err != nil {
		t.Fatal("formal LOGIN bootstrap failed")
	}
	parsed.User = url.UserPassword(config.Migration.Name, config.Migration.Password)
	q := parsed.Query()
	q.Set("options", "-c role=migration_role")
	parsed.RawQuery = strings.ReplaceAll(q.Encode(), "+", "%20")
	for _, target := range []string{"0", "0", "1"} {
		out, err := runMigration(parsed.String(), target)
		if err != nil {
			t.Fatalf("restricted Linux migration/retry target %s failed: %v: %s", target, err, out)
		}
		if bytes.Contains(out, []byte(config.Migration.Password)) {
			t.Fatal("migration leaked credentials")
		}
		var version int
		if err := db.QueryRowContext(ctx, "SELECT max(version_id) FROM public.goose_db_version WHERE is_applied").Scan(&version); err != nil || version != 35 {
			t.Fatalf("forward-only migration state: version=%d err=%v", version, err)
		}
	}
	if out, err := runMigration(parsed.String(), "-1"); err == nil || !bytes.Contains(out, []byte("target must be non-negative")) {
		t.Fatal("Linux migration binary accepted negative target")
	}
	t.Cleanup(func() {
		for _, login := range []bootstrap.DatabaseLogin{config.Migration, config.API, config.Worker} {
			_, _ = db.ExecContext(ctx, `DROP OWNED BY "`+login.Name+`"`)
			_, _ = db.ExecContext(ctx, `DROP ROLE "`+login.Name+`"`)
		}
	})
	t.Log("exact offline Linux binary: privileged role bootstrap, formal separated LOGIN bootstrap, restricted forward 1->35, retry/no rollback, negative target refusal")
}
