package integration

// P0-04 task-owned business fixture for the V1.2 replication work.
//
// This file is deliberately not a live-source acceptance test. It seeds an
// isolated, task-owned PostgreSQL database with repeatable business data and
// then verifies the data through the existing tenant, source, graph and
// assistant authorization paths. It refuses to run against the shared
// development database or any non-loopback endpoint.
//
// Secrets are read from process-owned env files at run time. The files are
// KEY=URL lines and must not be sourced by a shell (the URL can contain '&').
// Nothing in this file prints, stores or returns a password or a full DSN.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"ops-platform/internal/analytics"
	"ops-platform/internal/assistant"
	"ops-platform/internal/auth"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/investigation"
	"ops-platform/internal/persistence"
	"ops-platform/internal/resource"
	"ops-platform/internal/resourcestore"
	"ops-platform/internal/source"
	"ops-platform/internal/tenant"
)

const (
	uiV12FixtureTask = "ui-v12-p0-04-r2"

	uiV12FixtureDatabase = "ui_v12_development"
	uiV12FixtureDBHost   = "127.0.0.1"
	uiV12FixtureDBPort   = "15449"
	uiV12DatabaseOwner   = "ui_v12_owner"
	uiV12SchemaVersion   = 43
	uiV12DatabaseComment = "ops.task=ui-v12-20261021-r2; isolated synthetic fixtures only"

	uiV12DefaultEnvDir = "/data/ops-ui-v12-20261021-r2/private"

	uiV12BootstrapSlug    = "ui-v12-r2-bootstrap"
	uiV12BootstrapName    = "UI v1.2 R2 task bootstrap"
	uiV12FixtureSlug      = "ui-v12-p0-04-r2"
	uiV12FixtureTenantNam = "UI v1.2 P0-04 R2 fixture"
	uiV12AdminSubject     = "ui-v12-r2-bootstrap-admin"
	uiV12OperatorASubject = "ui-v12-r2-operator-a"
	uiV12OperatorBSubject = "ui-v12-r2-operator-b"

	uiV12ClusterAUID = "0c1a0000-0000-4000-8000-0000000000a1"
	uiV12ClusterBUID = "0c1a0000-0000-4000-8000-0000000000b1"

	uiV12NSAProdUID   = "0a110000-0000-4000-8000-0000000000a1"
	uiV12NSACanaryUID = "0a110000-0000-4000-8000-0000000000a2"
	uiV12NSBProdUID   = "0a110000-0000-4000-8000-0000000000b1"

	uiV12PodAProdUID   = "90d00000-0000-4000-8000-0000000000a1"
	uiV12PodACanaryUID = "90d00000-0000-4000-8000-0000000000a2"
	uiV12PodBProdUID   = "90d00000-0000-4000-8000-0000000000b1"
)

var (
	uiV12BootstrapTenantID     = uuid.MustParse("7e1a0000-0000-4000-8000-000000000001")
	uiV12BootstrapBindID       = uuid.MustParse("7e1a0000-0000-4000-8000-000000000002")
	uiV12LegacyConversationAID = uuid.MustParse("c0a00000-0000-4000-8000-0000000000a1")
	uiV12LegacyConversationBID = uuid.MustParse("c0a00000-0000-4000-8000-0000000000b1")
	uiV12FixtureObservedAt     = time.Date(2026, 10, 21, 3, 0, 0, 0, time.UTC)
)

type uiV12DSN struct {
	File string
	Raw  string
	User string
	Role string
}

type uiV12GuardInfo struct {
	Database           string `json:"database"`
	ClientEndpoint     string `json:"clientEndpoint"`
	ServerAddress      string `json:"serverAddress"`
	ServerPort         int    `json:"serverPort"`
	DatabaseOwner      string `json:"databaseOwner"`
	SchemaVersion      int    `json:"schemaVersion"`
	DatabaseTaskMarker bool   `json:"databaseTaskMarker"`
	APISession         string `json:"apiSessionUser"`
	WorkerSession      string `json:"workerSessionUser"`
	MigrSession        string `json:"migrationSessionUser"`
	Task               string `json:"task"`
	EnvDir             string `json:"envDir"`
}

type uiV12Probe struct {
	Session string
	Address string
	Port    int
}

type uiV12Guard struct {
	API       *pgxpool.Pool
	Worker    *pgxpool.Pool
	Migration *pgxpool.Pool
	Info      uiV12GuardInfo
}

type uiV12Check struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail,omitempty"`
}

type uiV12Report struct {
	SchemaVersion string         `json:"schemaVersion"`
	Task          string         `json:"task"`
	Scope         string         `json:"scope"`
	Status        string         `json:"status"`
	Error         string         `json:"error,omitempty"`
	GeneratedAt   time.Time      `json:"generatedAt"`
	Guard         uiV12GuardInfo `json:"guard"`
	Fixture       map[string]any `json:"fixture,omitempty"`
	Counts        map[string]int `json:"counts,omitempty"`
	Checks        []uiV12Check   `json:"checks"`
	Unmet         []string       `json:"unmet"`
}

func (r *uiV12Report) check(name string, ok bool, format string, args ...any) bool {
	detail := fmt.Sprintf(format, args...)
	r.Checks = append(r.Checks, uiV12Check{Name: name, Passed: ok, Detail: detail})
	return ok
}

// TestUIV12FixtureDSNGuard is a pure unit test: it proves the guard rejects
// shared/non-task endpoints without opening a database connection.
func TestUIV12FixtureDSNGuard(t *testing.T) {
	base := uiV12TestDSN(t, "ui_v12_api", "p&ss", "127.0.0.1", "15449", "/ui_v12_development",
		"api_runtime_role")
	if _, err := uiV12ValidateDSN(base, "ui_v12_api", "api_runtime_role"); err != nil {
		t.Fatalf("valid task DSN rejected: %v", err)
	}
	rejected := map[string]string{
		"shared database":   uiV12TestDSN(t, "ui_v12_api", "pw", "127.0.0.1", "15449", "/ops_platform", "api_runtime_role"),
		"shared port":       uiV12TestDSN(t, "ui_v12_api", "pw", "127.0.0.1", "5432", "/ui_v12_development", "api_runtime_role"),
		"non-loopback host": uiV12TestDSN(t, "ui_v12_api", "pw", "192.168.0.63", "15449", "/ui_v12_development", "api_runtime_role"),
		"wrong login":       uiV12TestDSN(t, "ui_v12_worker", "pw", "127.0.0.1", "15449", "/ui_v12_development", "api_runtime_role"),
		"wrong duty role":   uiV12TestDSN(t, "ui_v12_api", "pw", "127.0.0.1", "15449", "/ui_v12_development", "schema_owner"),
		"http scheme":       "http://ui_v12_api:pw@127.0.0.1:15449/ui_v12_development",
	}
	for name, raw := range rejected {
		if _, err := uiV12ValidateDSN(raw, "ui_v12_api", "api_runtime_role"); err == nil {
			t.Errorf("%s DSN was accepted", name)
		}
	}
	extra := uiV12TestDSN(t, "ui_v12_api", "pw", "127.0.0.1", "15449", "/ui_v12_development", "api_runtime_role")
	extra += "&hostaddr=10.0.0.1"
	if _, err := uiV12ValidateDSN(extra, "ui_v12_api", "api_runtime_role"); err == nil {
		t.Fatal("extra connection override was accepted")
	}

	parsed, err := uiV12ParseEnvDatabaseURL("DATABASE_URL=postgres://ui_v12_api:p&w@127.0.0.1:15449/ui_v12_development?sslmode=disable\n")
	if err != nil || !strings.Contains(parsed, "p&w") {
		t.Fatalf("env file URL was split on '&': %q %v", parsed, err)
	}

	if !uiV12DatabaseCommentIsTask(uiV12DatabaseComment) {
		t.Fatal("exact task database comment rejected")
	}
	for _, comment := range []string{
		"",
		"shared development database",
		"ui-v12-r2",
		"ops.task=ui-v12-20261021-r20; isolated synthetic fixtures only",
		"ops.task=ui-v12-20261020-r2; isolated synthetic fixtures only",
		"ops.task=ui-v12-20261021-r2; isolated synthetic fixtures only; ops.task=ui-v12-20261021-r2-other",
		"OPS.TASK=UI-V12-20261021-R2",
		"ops.task=ui-v12-20261021-r2; isolated synthetic fixtures only ",
	} {
		if uiV12DatabaseCommentIsTask(comment) {
			t.Errorf("non-exact database comment accepted: %q", comment)
		}
	}
}

// TestUIV12FixtureSeedAndVerify writes only to the task-owned database named in
// the remote env files. It is skipped unless the operator explicitly opts in.
func TestUIV12FixtureSeedAndVerify(t *testing.T) {
	if os.Getenv("OPS_UI_V12_FIXTURE_RUN") != "1" {
		t.Skip("set OPS_UI_V12_FIXTURE_RUN=1 and OPS_UI_V12_FIXTURE_TASK=ui-v12-p0-04-r2 on the task-owned remote host")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()

	report := &uiV12Report{
		SchemaVersion: "ui-v12-fixture/v1",
		Task:          uiV12FixtureTask,
		Scope:         "isolated business fixture only; not a live source, native account or G0/P0-04 acceptance",
		GeneratedAt:   time.Now().UTC(),
		Unmet: []string{
			"native user, login-credential and login-session tables are absent; native login and explicit historical subject mapping remain P2-01/P2-02 dependencies",
			"no adapter qualification proof is fabricated; unqualified sources are rejected, and a qualified live source belongs to P8-02",
			"this fixture does not authenticate a real API/Worker process, Kubernetes source, model or browser",
			"G0 and P0-04 are not closed by this test; it prepares and verifies only the task-owned business fixture",
		},
	}
	evidencePath := os.Getenv("OPS_UI_V12_FIXTURE_EVIDENCE")
	defer func() {
		if report.Status == "" {
			report.Status = "incomplete"
		}
		if err := uiV12WriteReport(report, evidencePath); err != nil {
			t.Errorf("write fixture evidence: %v", err)
			return
		}
		t.Logf("fixture evidence written to %s (status=%s checks=%d)", evidencePath, report.Status, len(report.Checks))
	}()
	if evidencePath == "" {
		report.Status = "evidence_path_required"
		report.Error = "OPS_UI_V12_FIXTURE_EVIDENCE must name a new, unique evidence file"
		t.Fatal(report.Error)
	}

	envDir := os.Getenv("OPS_UI_V12_ENV_DIR")
	if envDir == "" {
		envDir = uiV12DefaultEnvDir
	}
	guard, err := uiV12OpenGuard(ctx, envDir)
	if err != nil {
		report.Status = "guard_failed"
		report.Error = err.Error()
		t.Fatalf("fixture guard: %v", err)
	}
	defer func() {
		guard.API.Close()
		guard.Worker.Close()
		guard.Migration.Close()
	}()
	report.Guard = guard.Info

	beforeTenants, err := uiV12TenantSnapshot(ctx, guard.API)
	if err != nil {
		report.Status = "snapshot_failed"
		report.Error = err.Error()
		t.Fatalf("tenant snapshot: %v", err)
	}

	state, err := uiV12Seed(ctx, guard)
	if err != nil {
		report.Status = "seed_failed"
		report.Error = err.Error()
		t.Fatalf("seed fixture: %v", err)
	}
	first, err := uiV12Counts(ctx, guard.API, state.TenantID)
	if err != nil {
		report.Status = "count_failed"
		report.Error = err.Error()
		t.Fatalf("first counts: %v", err)
	}

	again, err := uiV12Seed(ctx, guard)
	if err != nil {
		report.Status = "second_seed_failed"
		report.Error = err.Error()
		t.Fatalf("second seed: %v", err)
	}
	second, err := uiV12Counts(ctx, guard.API, state.TenantID)
	if err != nil {
		report.Status = "second_count_failed"
		report.Error = err.Error()
		t.Fatalf("second counts: %v", err)
	}
	afterTenants, err := uiV12TenantSnapshot(ctx, guard.API)
	if err != nil {
		report.Status = "snapshot_failed"
		report.Error = err.Error()
		t.Fatalf("tenant snapshot after seed: %v", err)
	}

	report.Fixture = uiV12FixtureEvidence(state)
	report.Counts = map[string]int{
		"clusters":            first.Clusters,
		"sources":             first.Sources,
		"bindings":            first.Bindings,
		"entities":            first.Entities,
		"aliases":             first.Aliases,
		"conversations":       first.Conversations,
		"qualificationProofs": first.Verifications,
		"auditRecords":        first.AuditRecords,
		"sourceRevisions":     first.SourceRevisions,
		"clusterRevisions":    first.ClusterRevisions,
	}

	uiV12VerifyStableIDs(report, state, again)
	uiV12VerifyCounts(report, first, second)
	uiV12VerifyNoDuplicates(ctx, report, guard.API, state.TenantID)
	uiV12VerifyTenantNonInterference(report, beforeTenants, afterTenants)
	uiV12VerifyResources(ctx, report, guard.API, state)
	uiV12VerifyAuthorization(ctx, report, guard.API, state)
	uiV12VerifySourceStates(ctx, report, guard.API, state)
	uiV12VerifyHistory(ctx, report, guard.API, state)
	uiV12VerifySubjectDependency(ctx, report, guard.API, state.TenantID)

	for _, c := range report.Checks {
		if !c.Passed {
			report.Status = "checks_failed"
			report.Error = "one or more fixture checks failed"
			break
		}
	}
	if report.Status == "" {
		report.Status = "fixture_verified_not_accepted"
	}
	failed := 0
	for _, c := range report.Checks {
		if !c.Passed {
			failed++
		}
	}
	if failed > 0 {
		t.Fatalf("%d fixture checks failed; see evidence output", failed)
	}
}

//---------------------------------------------------------------------------
// Env-file and guard handling
//---------------------------------------------------------------------------

func uiV12OpenGuard(ctx context.Context, envDir string) (*uiV12Guard, error) {
	if os.Getenv("OPS_UI_V12_FIXTURE_TASK") != uiV12FixtureTask {
		return nil, fmt.Errorf("OPS_UI_V12_FIXTURE_TASK must be %q for the task-owned fixture", uiV12FixtureTask)
	}
	files := []struct {
		name, user, role string
	}{
		{"api.env", "ui_v12_api", "api_runtime_role"},
		{"worker.env", "ui_v12_worker", "worker_runtime_role"},
		{"migration.env", "ui_v12_migration", "migration_role"},
	}
	dsns := make([]uiV12DSN, 0, len(files))
	for _, f := range files {
		raw, err := uiV12ReadEnvDatabaseURL(envDir + "/" + f.name)
		if err != nil {
			return nil, err
		}
		dsn, err := uiV12ValidateDSN(raw, f.user, f.role)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.name, err)
		}
		dsn.File = f.name
		dsns = append(dsns, dsn)
	}
	open := func(dsn uiV12DSN) (*pgxpool.Pool, error) {
		cfg, err := pgxpool.ParseConfig(dsn.Raw)
		if err != nil {
			return nil, fmt.Errorf("%s: parse connection", dsn.File)
		}
		cfg.MaxConns = 3
		pool, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			return nil, fmt.Errorf("%s: open connection", dsn.File)
		}
		if err := pool.Ping(ctx); err != nil {
			pool.Close()
			return nil, fmt.Errorf("%s: ping task database: %w", dsn.File, err)
		}
		return pool, nil
	}
	g := &uiV12Guard{}
	var err error
	if g.API, err = open(dsns[0]); err != nil {
		return nil, err
	}
	if g.Worker, err = open(dsns[1]); err != nil {
		g.API.Close()
		return nil, err
	}
	if g.Migration, err = open(dsns[2]); err != nil {
		g.API.Close()
		g.Worker.Close()
		return nil, err
	}
	closeAll := func() {
		g.API.Close()
		g.Worker.Close()
		g.Migration.Close()
	}
	apiProbe, err := uiV12ProbePool(ctx, g.API, "ui_v12_api", "api_runtime_role")
	if err != nil {
		closeAll()
		return nil, err
	}
	workerProbe, err := uiV12ProbePool(ctx, g.Worker, "ui_v12_worker", "worker_runtime_role")
	if err != nil {
		closeAll()
		return nil, err
	}
	migrProbe, err := uiV12ProbePool(ctx, g.Migration, "ui_v12_migration", "migration_role")
	if err != nil {
		closeAll()
		return nil, err
	}
	if apiProbe.Session == workerProbe.Session || apiProbe.Session == migrProbe.Session || workerProbe.Session == migrProbe.Session {
		closeAll()
		return nil, errors.New("task LOGIN identities are not independent")
	}
	if apiProbe.Address != workerProbe.Address || apiProbe.Port != workerProbe.Port ||
		apiProbe.Address != migrProbe.Address || apiProbe.Port != migrProbe.Port {
		closeAll()
		return nil, fmt.Errorf("task connections reached different servers: api=%s:%d worker=%s:%d migration=%s:%d",
			apiProbe.Address, apiProbe.Port, workerProbe.Address, workerProbe.Port, migrProbe.Address, migrProbe.Port)
	}
	var owner, db, comment string
	if err := g.API.QueryRow(ctx,
		`SELECT pg_get_userbyid(d.datdba), d.datname, COALESCE(shobj_description(d.oid, 'pg_database'), '')
		   FROM pg_database d WHERE d.datname = current_database()`).Scan(&owner, &db, &comment); err != nil {
		closeAll()
		return nil, fmt.Errorf("read task database ownership: %w", err)
	}
	if owner != uiV12DatabaseOwner {
		closeAll()
		return nil, fmt.Errorf("database %q owner is %q, expected task owner %q", db, owner, uiV12DatabaseOwner)
	}
	if !uiV12DatabaseCommentIsTask(comment) {
		closeAll()
		return nil, fmt.Errorf("database %q has no task ownership comment for %q; refusing shared or unowned database", db, uiV12FixtureTask)
	}
	var version int64
	if err := g.Migration.QueryRow(ctx, `SELECT COALESCE(max(version_id), 0) FROM public.goose_db_version`).Scan(&version); err != nil {
		closeAll()
		return nil, fmt.Errorf("read task schema version: %w", err)
	}
	if version != uiV12SchemaVersion {
		closeAll()
		return nil, fmt.Errorf("task database schema version is %d, expected %d", version, uiV12SchemaVersion)
	}
	g.Info = uiV12GuardInfo{
		Database: db, ClientEndpoint: uiV12FixtureDBHost + ":" + uiV12FixtureDBPort,
		ServerAddress: apiProbe.Address, ServerPort: apiProbe.Port, DatabaseOwner: owner,
		SchemaVersion: int(version), DatabaseTaskMarker: true,
		APISession: apiProbe.Session, WorkerSession: workerProbe.Session, MigrSession: migrProbe.Session,
		Task: uiV12FixtureTask, EnvDir: envDir,
	}
	return g, nil
}

func uiV12ProbePool(ctx context.Context, pool *pgxpool.Pool, wantSession, wantRole string) (uiV12Probe, error) {
	var out uiV12Probe
	var current, database, address string
	var port int
	if err := pool.QueryRow(ctx,
		`SELECT session_user, current_user, current_database(), COALESCE(host(inet_server_addr()), ''), COALESCE(inet_server_port(), 0)`).
		Scan(&out.Session, &current, &database, &address, &port); err != nil {
		return out, fmt.Errorf("%s probe: %w", wantSession, err)
	}
	out.Address, out.Port = address, port
	if out.Session != wantSession || current != wantRole {
		return out, fmt.Errorf("connection identity is session=%q current=%q, expected session=%q current=%q", out.Session, current, wantSession, wantRole)
	}
	if database != uiV12FixtureDatabase {
		return out, fmt.Errorf("connected to database=%q, expected task database %q", database, uiV12FixtureDatabase)
	}
	// The client DSN is strictly 127.0.0.1:15449. Inside the container the
	// PostgreSQL server keeps its native 5432 port and its container address;
	// neither is an error and neither may be "fixed" by changing the runtime.
	if port != 5432 {
		return out, fmt.Errorf("server-side PostgreSQL port is %d, expected native container port 5432 (client mapping %s is checked separately)", port, uiV12FixtureDBPort)
	}
	if address == "" {
		return out, errors.New("server-side PostgreSQL address is unavailable")
	}
	for _, role := range []string{out.Session, current} {
		var inherit, bypass, super bool
		if err := pool.QueryRow(ctx, `SELECT rolinherit, rolbypassrls, rolsuper FROM pg_roles WHERE rolname = $1`, role).
			Scan(&inherit, &bypass, &super); err != nil {
			return out, fmt.Errorf("read role attributes for %q: %w", role, err)
		}
		if inherit || bypass || super {
			return out, fmt.Errorf("role %q must be NOINHERIT/NOBYPASSRLS/NOSUPERUSER", role)
		}
	}
	return out, nil
}

func uiV12ValidateDSN(raw, wantUser, wantRole string) (uiV12DSN, error) {
	if raw == "" || strings.TrimSpace(raw) != raw || strings.ContainsAny(raw, "\r\n\x00") {
		return uiV12DSN{}, errors.New("connection URL is empty or contains control characters")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return uiV12DSN{}, errors.New("connection URL is invalid")
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		return uiV12DSN{}, errors.New("connection URL must use postgres:// or postgresql://")
	}
	if parsed.User == nil {
		return uiV12DSN{}, errors.New("connection URL has no login")
	}
	user := parsed.User.Username()
	password, hasPassword := parsed.User.Password()
	if user != wantUser || !hasPassword || password == "" {
		return uiV12DSN{}, fmt.Errorf("connection URL must use the task login %q with a password", wantUser)
	}
	if parsed.Hostname() != uiV12FixtureDBHost || parsed.Port() != uiV12FixtureDBPort {
		return uiV12DSN{}, fmt.Errorf("connection URL must use %s:%s, got %s:%s", uiV12FixtureDBHost, uiV12FixtureDBPort, parsed.Hostname(), parsed.Port())
	}
	if parsed.Path != "/"+uiV12FixtureDatabase || parsed.RawPath != "" {
		return uiV12DSN{}, fmt.Errorf("connection URL must target database %q", uiV12FixtureDatabase)
	}
	if parsed.Fragment != "" {
		return uiV12DSN{}, errors.New("connection URL must not contain a fragment")
	}
	query := parsed.Query()
	for key, values := range query {
		if key != "sslmode" && key != "options" {
			return uiV12DSN{}, fmt.Errorf("connection URL must not override %q", key)
		}
		if len(values) != 1 {
			return uiV12DSN{}, fmt.Errorf("connection URL repeats %q", key)
		}
	}
	if value := query.Get("sslmode"); value != "" && value != "disable" {
		return uiV12DSN{}, errors.New("task loopback connection must use sslmode=disable")
	}
	if query.Get("options") != "-c role="+wantRole {
		return uiV12DSN{}, fmt.Errorf("connection URL must switch only to duty role %q", wantRole)
	}
	return uiV12DSN{Raw: raw, User: user, Role: wantRole}, nil
}

// uiV12DatabaseCommentIsTask requires the exact operator-written database
// comment. This is a known environment fact, not a format heuristic: a comment
// for r20, another date, or another task appended to the same text must not be
// accepted as ownership of this database.
func uiV12DatabaseCommentIsTask(comment string) bool {
	return comment == uiV12DatabaseComment
}

func uiV12ReadEnvDatabaseURL(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read task env file %s: %w", path, err)
	}
	value, err := uiV12ParseEnvDatabaseURL(string(raw))
	if err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	return value, nil
}

// uiV12ParseEnvDatabaseURL parses a KEY=URL file without shell evaluation. The
// value is everything after the first '=', so '&' in a password is preserved.
func uiV12ParseEnvDatabaseURL(content string) (string, error) {
	var value string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, rest, ok := strings.Cut(line, "=")
		if !ok {
			return "", errors.New("env file line is not KEY=VALUE")
		}
		if strings.TrimSpace(key) != "DATABASE_URL" {
			continue
		}
		if value != "" {
			return "", errors.New("env file contains more than one DATABASE_URL")
		}
		// Keep the entire value after the first '='; never shell-evaluate and
		// never trim an embedded '&' or other URL characters.
		value = uiV12Unquote(rest)
	}
	if value == "" {
		return "", errors.New("env file has no DATABASE_URL")
	}
	return value, nil
}

func uiV12Unquote(value string) string {
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}

func uiV12TestDSN(t *testing.T, user, password, host, port, path, role string) string {
	t.Helper()
	raw := "postgres://" + url.UserPassword(user, password).String() + "@" + host + ":" + port + path
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	parsed.RawQuery = url.Values{
		"sslmode": {"disable"},
		"options": {"-c role=" + role},
	}.Encode()
	return parsed.String()
}

//---------------------------------------------------------------------------
// Fixture seeding
//---------------------------------------------------------------------------

type uiV12CountSet struct {
	Clusters         int
	Sources          int
	Bindings         int
	Entities         int
	Aliases          int
	Conversations    int
	Verifications    int
	AuditRecords     int
	SourceRevisions  int
	ClusterRevisions int
}

type uiV12State struct {
	TenantID      uuid.UUID
	Admin         auth.RequestContext
	ClusterA      source.ClusterRegistration
	ClusterB      source.ClusterRegistration
	SourceA       source.SourceRegistration
	SourceB       source.SourceRegistration
	SourceRevoked source.SourceRegistration
	BindingA      tenant.RoleBinding
	BindingB      tenant.RoleBinding
	Resources     map[string]resource.Resolution
	Conversations map[string]uuid.UUID
	LegacyRepairs []uiV12LegacyRepair
}

type uiV12LegacyRepair struct {
	ConversationID  string `json:"conversationId"`
	OwnerSubject    string `json:"ownerSubject"`
	RevisionBefore  int64  `json:"revisionBefore"`
	RevisionAfter   int64  `json:"revisionAfter"`
	RebindRevision  int64  `json:"rebindRevision"`
	Turns           int    `json:"turns"`
	Messages        int    `json:"messages"`
	Events          int    `json:"events"`
	BindingRequests int    `json:"bindingRequests"`
	BindingRepaired bool   `json:"bindingRepaired"`
}

func uiV12Seed(ctx context.Context, g *uiV12Guard) (*uiV12State, error) {
	if err := uiV12EnsureBootstrapTenant(ctx, g.Migration); err != nil {
		return nil, err
	}
	tenantService, err := tenant.NewService(g.API)
	if err != nil {
		return nil, err
	}
	sourceService, err := source.NewService(g.API, source.CredentialVerifierFunc(
		func(context.Context, string, source.SourceIdentity, source.FindingEnvelope) error {
			return source.ErrUnauthorized
		}))
	if err != nil {
		return nil, err
	}
	bootstrapActor := auth.RequestContext{
		RequestID: uiV12FixtureTask,
		TenantID:  uiV12BootstrapTenantID,
		Subject:   uiV12AdminSubject,
		Roles:     []auth.Role{auth.PlatformAdmin},
	}
	tenantID, err := uiV12EnsureFixtureTenant(ctx, g.API, tenantService, bootstrapActor)
	if err != nil {
		return nil, err
	}
	state := &uiV12State{
		TenantID: tenantID,
		Admin: auth.RequestContext{
			RequestID: uiV12FixtureTask,
			TenantID:  tenantID,
			Subject:   uiV12AdminSubject,
			Roles:     []auth.Role{auth.PlatformAdmin},
		},
	}
	if state.ClusterA, err = uiV12EnsureCluster(ctx, g.API, sourceService, state.Admin, source.ClusterCommand{
		ClusterUID:     uiV12ClusterAUID,
		DisplayName:    "UI v1.2 R2 cluster A",
		APIEndpointRef: "openbao://ops-ui-v12-p0-04-r2/cluster-a",
		Distribution:   "k3s",
		ActualVersions: map[string]string{"kubernetes": "v1.36.3"},
		Capabilities:   map[string]bool{"network": true, "logs": true},
	}); err != nil {
		return nil, err
	}
	if state.ClusterB, err = uiV12EnsureCluster(ctx, g.API, sourceService, state.Admin, source.ClusterCommand{
		ClusterUID:     uiV12ClusterBUID,
		DisplayName:    "UI v1.2 R2 cluster B",
		APIEndpointRef: "openbao://ops-ui-v12-p0-04-r2/cluster-b",
		Distribution:   "k3s",
		ActualVersions: map[string]string{"kubernetes": "v1.36.3"},
		Capabilities:   map[string]bool{"network": true, "logs": true},
	}); err != nil {
		return nil, err
	}
	if state.BindingA, err = uiV12EnsureBinding(ctx, g.API, tenantService, state.Admin, tenant.CreateRoleBindingInput{
		Subject:       uiV12OperatorASubject,
		Role:          auth.Operator,
		ClusterScopes: []uuid.UUID{state.ClusterA.ClusterID},
		NamespaceScopes: []auth.NamespaceScope{
			{ClusterID: state.ClusterA.ClusterID, Namespace: "prod"},
			{ClusterID: state.ClusterA.ClusterID, Namespace: "canary"},
		},
	}); err != nil {
		return nil, err
	}
	if state.BindingB, err = uiV12EnsureBinding(ctx, g.API, tenantService, state.Admin, tenant.CreateRoleBindingInput{
		Subject:       uiV12OperatorBSubject,
		Role:          auth.Operator,
		ClusterScopes: []uuid.UUID{state.ClusterB.ClusterID},
		NamespaceScopes: []auth.NamespaceScope{
			{ClusterID: state.ClusterB.ClusterID, Namespace: "prod"},
		},
	}); err != nil {
		return nil, err
	}
	if state.SourceA, err = uiV12EnsureSource(ctx, g.API, sourceService, state.Admin, uiV12SourceACommand(state.ClusterA)); err != nil {
		return nil, err
	}
	if state.SourceA, err = uiV12EnsureSourceRevision(ctx, g.API, sourceService, state.Admin, state.SourceA); err != nil {
		return nil, err
	}
	if state.SourceB, err = uiV12EnsureSource(ctx, g.API, sourceService, state.Admin, uiV12SourceBCommand(state.ClusterB)); err != nil {
		return nil, err
	}
	if state.SourceRevoked, err = uiV12EnsureSource(ctx, g.API, sourceService, state.Admin, uiV12RevokedSourceCommand(state.ClusterB)); err != nil {
		return nil, err
	}
	if state.SourceRevoked, err = uiV12EnsureRevoked(ctx, g.API, sourceService, state.Admin, state.SourceRevoked); err != nil {
		return nil, err
	}
	if state.Resources, err = uiV12ResolveResources(ctx, g.Worker, state); err != nil {
		return nil, err
	}
	if state.Conversations, err = uiV12EnsureConversations(ctx, g.API, state); err != nil {
		return nil, err
	}
	return state, nil
}

// uiV12EnsureBootstrapTenant is the only schema_owner write in this fixture.
// The product has no native first-administrator bootstrap yet (P2-01/P2-02),
// so a fixed, task-scoped tenant and its platform_admin binding are inserted
// directly. No business data is written through schema_owner.
func uiV12EnsureBootstrapTenant(ctx context.Context, migration *pgxpool.Pool) error {
	tx, err := migration.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE schema_owner`); err != nil {
		return fmt.Errorf("assume schema owner for fixture bootstrap: %w", err)
	}
	var existingID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT tenant_id FROM platform.tenants WHERE tenant_id = $1`, uiV12BootstrapTenantID).Scan(&existingID)
	switch {
	case err == nil:
		var slug, name, status string
		if err := tx.QueryRow(ctx,
			`SELECT slug, display_name, status FROM platform.tenants WHERE tenant_id = $1`,
			uiV12BootstrapTenantID).Scan(&slug, &name, &status); err != nil {
			return fmt.Errorf("read bootstrap task tenant: %w", err)
		}
		if slug != uiV12BootstrapSlug || name != uiV12BootstrapName || status != "active" {
			return fmt.Errorf("bootstrap tenant %s ownership mismatch: slug=%q name=%q status=%q; refusing to reuse",
				uiV12BootstrapTenantID, slug, name, status)
		}
	case errors.Is(err, pgx.ErrNoRows):
		var other uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT tenant_id FROM platform.tenants WHERE slug = $1`, uiV12BootstrapSlug).Scan(&other); err == nil {
			return fmt.Errorf("bootstrap slug %q belongs to another tenant %s; refusing to write", uiV12BootstrapSlug, other)
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("check bootstrap slug ownership: %w", err)
		}
		// No ON CONFLICT here: an unexpected duplicate must fail instead of
		// silently adopting a row created by another task.
		if _, err := tx.Exec(ctx,
			`INSERT INTO platform.tenants (tenant_id, slug, display_name) VALUES ($1, $2, $3)`,
			uiV12BootstrapTenantID, uiV12BootstrapSlug, uiV12BootstrapName); err != nil {
			return fmt.Errorf("bootstrap task tenant: %w", err)
		}
	default:
		return fmt.Errorf("read bootstrap task tenant identity: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, uiV12BootstrapTenantID.String()); err != nil {
		return err
	}
	type bindingRow struct {
		id     uuid.UUID
		status string
	}
	bindings := []bindingRow{}
	rows, err := tx.Query(ctx,
		`SELECT binding_id, status FROM platform.role_bindings
		  WHERE tenant_id = $1 AND subject = $2 AND role_name = 'platform_admin'`,
		uiV12BootstrapTenantID, uiV12AdminSubject)
	if err != nil {
		return err
	}
	for rows.Next() {
		var item bindingRow
		if err := rows.Scan(&item.id, &item.status); err != nil {
			rows.Close()
			return err
		}
		bindings = append(bindings, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	switch len(bindings) {
	case 0:
		if _, err := tx.Exec(ctx,
			`INSERT INTO platform.role_bindings (tenant_id, binding_id, subject, role_name, cluster_scopes, namespace_scopes)
			 VALUES ($1, $2, $3, 'platform_admin', '[]'::jsonb, '[]'::jsonb)`,
			uiV12BootstrapTenantID, uiV12BootstrapBindID, uiV12AdminSubject); err != nil {
			return fmt.Errorf("bootstrap platform administrator: %w", err)
		}
	case 1:
		if bindings[0].id != uiV12BootstrapBindID || bindings[0].status != "active" {
			return fmt.Errorf("bootstrap administrator binding ownership mismatch: id=%s status=%s; refusing to reuse",
				bindings[0].id, bindings[0].status)
		}
	default:
		return fmt.Errorf("bootstrap administrator has %d platform_admin bindings; refusing ambiguous ownership", len(bindings))
	}
	return tx.Commit(ctx)
}

func uiV12EnsureFixtureTenant(ctx context.Context, pool *pgxpool.Pool, service *tenant.Service, actor auth.RequestContext) (uuid.UUID, error) {
	var id uuid.UUID
	err := pool.QueryRow(ctx, `SELECT tenant_id FROM platform.tenants WHERE slug = $1`, uiV12FixtureSlug).Scan(&id)
	if err == nil {
		var status, name string
		if err := pool.QueryRow(ctx, `SELECT status, display_name FROM platform.tenants WHERE tenant_id = $1`, id).Scan(&status, &name); err != nil {
			return uuid.Nil, err
		}
		if status != "active" || name != uiV12FixtureTenantNam {
			return uuid.Nil, fmt.Errorf("fixture tenant %s ownership mismatch: status=%q name=%q; refusing to reuse", id, status, name)
		}
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("read fixture tenant: %w", err)
	}
	var created tenant.Tenant
	err = persistence.WithTenantTx(ctx, pool, actor.TenantID, func(tx pgx.Tx) error {
		var err error
		created, err = service.CreateTenant(ctx, tx, actor, tenant.CreateTenantInput{
			Slug:        uiV12FixtureSlug,
			DisplayName: uiV12FixtureTenantNam,
		})
		return err
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("provision fixture tenant: %w", err)
	}
	return created.ID, nil
}

func uiV12EnsureCluster(ctx context.Context, pool *pgxpool.Pool, service *source.Service, actor auth.RequestContext, command source.ClusterCommand) (source.ClusterRegistration, error) {
	var out source.ClusterRegistration
	err := persistence.WithTenantTx(ctx, pool, actor.TenantID, func(tx pgx.Tx) error {
		var err error
		out, err = service.RegisterCluster(ctx, tx, actor, command)
		return err
	})
	if err != nil {
		return out, fmt.Errorf("register fixture cluster %q: %w", command.ClusterUID, err)
	}
	return out, nil
}

func uiV12EnsureBinding(ctx context.Context, pool *pgxpool.Pool, service *tenant.Service, actor auth.RequestContext, input tenant.CreateRoleBindingInput) (tenant.RoleBinding, error) {
	list, err := service.ListCurrentRoleBindings(ctx, actor)
	if err != nil {
		return tenant.RoleBinding{}, err
	}
	for _, existing := range list {
		if existing.Subject != input.Subject || existing.Role != input.Role || existing.Status != "active" {
			continue
		}
		if uiV12SameBinding(existing, input) {
			return existing, nil
		}
		return tenant.RoleBinding{}, fmt.Errorf("fixture binding for %q has different scopes; refusing to overwrite", input.Subject)
	}
	var created tenant.RoleBinding
	err = persistence.WithTenantTx(ctx, pool, actor.TenantID, func(tx pgx.Tx) error {
		var err error
		created, err = service.CreateRoleBinding(ctx, tx, actor, input)
		return err
	})
	if err != nil {
		return tenant.RoleBinding{}, fmt.Errorf("create fixture binding for %q: %w", input.Subject, err)
	}
	return created, nil
}

func uiV12SameBinding(existing tenant.RoleBinding, input tenant.CreateRoleBindingInput) bool {
	if len(existing.ClusterScopes) != len(input.ClusterScopes) || len(existing.NamespaceScopes) != len(input.NamespaceScopes) {
		return false
	}
	clusters := map[uuid.UUID]bool{}
	for _, id := range existing.ClusterScopes {
		clusters[id] = true
	}
	for _, id := range input.ClusterScopes {
		if !clusters[id] {
			return false
		}
	}
	namespaces := map[auth.NamespaceScope]bool{}
	for _, scope := range existing.NamespaceScopes {
		namespaces[scope] = true
	}
	for _, scope := range input.NamespaceScopes {
		if !namespaces[scope] {
			return false
		}
	}
	return true
}

func uiV12SourceACommand(cluster source.ClusterRegistration) source.RegisterCommand {
	return source.RegisterCommand{
		SourceType:       "kubernetes",
		InstanceKey:      "ui-v12-r2-a-native",
		ClusterID:        &cluster.ClusterID,
		AuthRef:          "openbao://ops-ui-v12-p0-04-r2/cluster-a",
		AllowedSchemas:   []string{"finding-envelope/v2"},
		BackendLogicalID: "ui-v12-r2-a-backend",
		DataScopeMapping: source.DataScopeMapping{
			Scopes:         map[string][]string{"cluster": {cluster.ClusterUID}, "namespace": {"prod", "canary"}},
			RequiredLabels: map[string]string{"tenant": cluster.TenantID.String()},
		},
	}
}

func uiV12SourceBCommand(cluster source.ClusterRegistration) source.RegisterCommand {
	return source.RegisterCommand{
		SourceType:       "kubernetes",
		InstanceKey:      "ui-v12-r2-b-native",
		ClusterID:        &cluster.ClusterID,
		AuthRef:          "openbao://ops-ui-v12-p0-04-r2/cluster-b",
		AllowedSchemas:   []string{"finding-envelope/v2"},
		BackendLogicalID: "ui-v12-r2-b-backend",
		DataScopeMapping: source.DataScopeMapping{
			Scopes:         map[string][]string{"cluster": {cluster.ClusterUID}, "namespace": {"prod"}},
			RequiredLabels: map[string]string{"tenant": cluster.TenantID.String()},
		},
	}
}

func uiV12RevokedSourceCommand(cluster source.ClusterRegistration) source.RegisterCommand {
	return source.RegisterCommand{
		SourceType:       "kubernetes",
		InstanceKey:      "ui-v12-r2-b-revoked",
		ClusterID:        &cluster.ClusterID,
		AuthRef:          "openbao://ops-ui-v12-p0-04-r2/cluster-b-revoked",
		AllowedSchemas:   []string{"finding-envelope/v2"},
		BackendLogicalID: "ui-v12-r2-b-revoked-backend",
		DataScopeMapping: source.DataScopeMapping{
			Scopes:         map[string][]string{"cluster": {cluster.ClusterUID}, "namespace": {"prod"}},
			RequiredLabels: map[string]string{"tenant": cluster.TenantID.String()},
		},
	}
}

func uiV12EnsureSource(ctx context.Context, pool *pgxpool.Pool, service *source.Service, actor auth.RequestContext, command source.RegisterCommand) (source.SourceRegistration, error) {
	list, err := service.ListSources(ctx, actor)
	if err != nil {
		return source.SourceRegistration{}, err
	}
	for _, existing := range list {
		if existing.SourceType != command.SourceType || existing.InstanceKey != command.InstanceKey {
			continue
		}
		if existing.ClusterID != *command.ClusterID || existing.BackendLogicalID != command.BackendLogicalID {
			return source.SourceRegistration{}, fmt.Errorf("source %q identity changed; refusing to overwrite", command.InstanceKey)
		}
		return existing, nil
	}
	var created source.SourceRegistration
	err = persistence.WithTenantTx(ctx, pool, actor.TenantID, func(tx pgx.Tx) error {
		var err error
		created, err = service.Register(ctx, tx, actor, command)
		return err
	})
	if err != nil {
		return source.SourceRegistration{}, fmt.Errorf("register fixture source %q: %w", command.InstanceKey, err)
	}
	return created, nil
}

// uiV12EnsureSourceRevision advances the primary source exactly once. The
// older revision remains a real rejection case for callers that pin it.
func uiV12EnsureSourceRevision(ctx context.Context, pool *pgxpool.Pool, service *source.Service, actor auth.RequestContext, current source.SourceRegistration) (source.SourceRegistration, error) {
	if current.Revision == 2 && len(current.AllowedSchemas) == 2 &&
		strings.Contains(strings.Join(current.AllowedSchemas, ","), "finding-envelope/v1") &&
		strings.Contains(strings.Join(current.AllowedSchemas, ","), "finding-envelope/v2") {
		return current, nil
	}
	if current.Revision != 1 {
		return current, fmt.Errorf("fixture source revision is %d, expected 1 or 2", current.Revision)
	}
	var updated source.SourceRegistration
	err := persistence.WithTenantTx(ctx, pool, actor.TenantID, func(tx pgx.Tx) error {
		var err error
		updated, err = service.UpdateRegistration(ctx, tx, actor, current.SourceID, source.SourceUpdateCommand{
			ExpectedRevision: current.Revision,
			AllowedSchemas:   []string{"finding-envelope/v1", "finding-envelope/v2"},
		})
		return err
	})
	if err != nil {
		return current, fmt.Errorf("advance fixture source revision: %w", err)
	}
	return updated, nil
}

func uiV12EnsureRevoked(ctx context.Context, pool *pgxpool.Pool, service *source.Service, actor auth.RequestContext, current source.SourceRegistration) (source.SourceRegistration, error) {
	if current.Status == "disabled" {
		return current, nil
	}
	var updated source.SourceRegistration
	err := persistence.WithTenantTx(ctx, pool, actor.TenantID, func(tx pgx.Tx) error {
		var err error
		updated, err = service.UpdateRegistration(ctx, tx, actor, current.SourceID, source.SourceUpdateCommand{
			ExpectedRevision: current.Revision,
			Status:           "disabled",
		})
		return err
	})
	if err != nil {
		return current, fmt.Errorf("revoke fixture source: %w", err)
	}
	return updated, nil
}

func uiV12ResolveResources(ctx context.Context, pool *pgxpool.Pool, state *uiV12State) (map[string]resource.Resolution, error) {
	type object struct {
		key, clusterUID, group, kind, namespace, name, uid string
		source                                             source.SourceRegistration
	}
	objects := []object{
		{"a/namespace/prod", uiV12ClusterAUID, "core", "Namespace", "", "prod", uiV12NSAProdUID, state.SourceA},
		{"a/namespace/canary", uiV12ClusterAUID, "core", "Namespace", "", "canary", uiV12NSACanaryUID, state.SourceA},
		{"a/pod/prod", uiV12ClusterAUID, "core", "Pod", "prod", "api-0", uiV12PodAProdUID, state.SourceA},
		{"a/pod/canary", uiV12ClusterAUID, "core", "Pod", "canary", "api-0", uiV12PodACanaryUID, state.SourceA},
		{"b/namespace/prod", uiV12ClusterBUID, "core", "Namespace", "", "prod", uiV12NSBProdUID, state.SourceB},
		{"b/pod/prod", uiV12ClusterBUID, "core", "Pod", "prod", "api-0", uiV12PodBProdUID, state.SourceB},
	}
	out := map[string]resource.Resolution{}
	for _, o := range objects {
		resolution, err := (resourcestore.Repository{
			Pool:             pool,
			ExpectedRevision: o.source.Revision,
			BackendLogicalID: o.source.BackendLogicalID,
		}).Resolve(ctx, resource.SourceRef{
			Domain:               "k8s",
			Tenant:               state.TenantID.String(),
			Scope:                o.clusterUID,
			APIGroup:             o.group,
			Kind:                 o.kind,
			UID:                  o.uid,
			Name:                 o.name,
			Namespace:            o.namespace,
			SourceRegistrationID: o.source.SourceID.String(),
			ObservedAt:           uiV12FixtureObservedAt,
		})
		if err != nil {
			return nil, fmt.Errorf("resolve %s: %w", o.key, err)
		}
		if resolution.Status != resource.Matched {
			return nil, fmt.Errorf("resolve %s returned %s", o.key, resolution.Status)
		}
		out[o.key] = resolution
	}
	return out, nil
}

func uiV12EnsureConversations(ctx context.Context, pool *pgxpool.Pool, state *uiV12State) (map[string]uuid.UUID, error) {
	repo := assistant.Repository{Pool: pool}
	type entry struct {
		subject   string
		legacyID  uuid.UUID
		cluster   string
		title     string
		createKey string
	}
	entries := []entry{
		{uiV12OperatorASubject, uiV12LegacyConversationAID, uiV12ClusterAUID, "UI v1.2 R2 operator A history", "ui-v12-r2-history-owner-a"},
		{uiV12OperatorBSubject, uiV12LegacyConversationBID, uiV12ClusterBUID, "UI v1.2 R2 operator B history", "ui-v12-r2-history-owner-b"},
	}
	out := map[string]uuid.UUID{}
	for _, e := range entries {
		legacyID, found, err := uiV12ConvergeLegacyConversation(ctx, pool, repo, state, e.subject, e.legacyID, e.cluster)
		if err != nil {
			return nil, err
		}
		if found {
			out[e.subject] = legacyID
			continue
		}
		created, err := repo.Create(ctx, assistant.Conversation{
			TenantID:            state.TenantID,
			OwnerSubject:        e.subject,
			Title:               e.title,
			Mode:                "data_query",
			AuthorizationDomain: "operational",
			ClusterUID:          e.cluster,
			Binding:             assistant.ConversationBinding{Timezone: "Asia/Shanghai"},
		}, assistant.CreationKey{
			Key:           e.createKey,
			RequestDigest: "ui-v12-r2-history/v1",
		})
		if err != nil {
			return nil, fmt.Errorf("create %s history via assistant.Repository.Create: %w", e.subject, err)
		}
		if created.ConversationID == uuid.Nil {
			return nil, fmt.Errorf("assistant.Repository.Create returned no conversation for %s", e.subject)
		}
		out[e.subject] = created.ConversationID
	}
	return out, nil
}

// uiV12ConvergeLegacyConversation handles the two empty conversations written
// by the first fixture run. Rebind deliberately refuses an unauthorized stored
// binding, so the only safe repair is a bounded binding correction on an empty,
// task-owned conversation followed by the existing Rebind path. No row is
// deleted or recreated, and any turn/message or foreign identity aborts.
func uiV12ConvergeLegacyConversation(ctx context.Context, pool *pgxpool.Pool, repo assistant.Repository, state *uiV12State, subject string, id uuid.UUID, cluster string) (uuid.UUID, bool, error) {
	var (
		owner, domain, clusterUID string
		archived                  *time.Time
		bindingRaw                []byte
		revision, revisionAfter   int64
		turns, messages           int
		events, bindingRequests   int
		repaired, found           bool
	)
	err := persistence.WithTenantTx(ctx, pool, state.TenantID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx,
			`SELECT owner_subject, authorization_domain, cluster_uid, revision, archived_at, binding
			   FROM investigation.assistant_conversations
			  WHERE tenant_id = $1 AND conversation_id = $2`,
			state.TenantID, id).
			Scan(&owner, &domain, &clusterUID, &revision, &archived, &bindingRaw)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read legacy conversation %s: %w", id, err)
		}
		found = true
		if owner != subject || domain != "operational" || clusterUID != cluster || archived != nil {
			return fmt.Errorf("legacy conversation %s ownership mismatch: owner=%q domain=%q cluster=%q archived=%v",
				id, owner, domain, clusterUID, archived != nil)
		}
		for _, item := range []struct {
			query string
			into  *int
		}{
			{`SELECT count(*) FROM investigation.assistant_turns WHERE tenant_id=$1 AND conversation_id=$2`, &turns},
			{`SELECT count(*) FROM investigation.assistant_messages WHERE tenant_id=$1 AND conversation_id=$2`, &messages},
			{`SELECT count(*) FROM investigation.assistant_events WHERE tenant_id=$1 AND conversation_id=$2`, &events},
			{`SELECT count(*) FROM investigation.assistant_binding_requests WHERE tenant_id=$1 AND conversation_id=$2`, &bindingRequests},
		} {
			if err := tx.QueryRow(ctx, item.query, state.TenantID, id).Scan(item.into); err != nil {
				return err
			}
		}
		if turns != 0 || messages != 0 {
			return fmt.Errorf("legacy conversation %s has turns=%d messages=%d; refusing repair", id, turns, messages)
		}
		var binding assistant.ConversationBinding
		if err := json.Unmarshal(bindingRaw, &binding); err != nil {
			return fmt.Errorf("decode legacy conversation %s binding: %w", id, err)
		}
		grant, err := graph.EffectiveTx(ctx, tx, state.TenantID.String(), subject, cluster, true)
		if err != nil {
			return fmt.Errorf("load current grant for legacy conversation %s: %w", id, err)
		}
		scope, err := analytics.RestrictScope(grant, binding.Selection)
		if err != nil {
			return fmt.Errorf("restrict current grant for legacy conversation %s: %w", id, err)
		}
		revisionAfter = revision
		valid := graph.ScopeDigest(binding.GrantSnapshot) == graph.ScopeDigest(grant) &&
			investigation.ScopeSubset(grant, binding.ExecutionScope)
		if valid {
			return nil
		}
		if events != 0 || bindingRequests != 0 {
			return fmt.Errorf("legacy conversation %s binding is invalid but has events=%d bindingRequests=%d; refusing repair",
				id, events, bindingRequests)
		}
		binding.GrantSnapshot = grant
		binding.ExecutionScope = scope
		fixed, err := json.Marshal(binding)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx,
			`UPDATE investigation.assistant_conversations
			    SET binding = $3::jsonb, revision = revision + 1, updated_at = clock_timestamp()
			  WHERE tenant_id = $1 AND conversation_id = $2 AND revision = $4`,
			state.TenantID, id, fixed, revision)
		if err != nil {
			return fmt.Errorf("repair legacy conversation %s binding: %w", id, err)
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf("legacy conversation %s changed during repair", id)
		}
		revisionAfter = revision + 1
		repaired = true
		return nil
	})
	if err != nil {
		return uuid.Nil, false, err
	}
	if !found {
		return uuid.Nil, false, nil
	}
	digest := graph.ScopeDigest(map[string]string{
		"fixture": "ui-v12-r2-history/v1", "conversation": id.String(), "owner": subject,
	})
	conv, err := repo.Rebind(ctx, state.TenantID, id, subject, "ui-v12-r2-history-rebind-"+subject,
		revisionAfter, analytics.Selection{}, nil, nil, digest)
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("formal Rebind of legacy conversation %s: %w", id, err)
	}
	if conv.ConversationID != id {
		return uuid.Nil, false, fmt.Errorf("formal Rebind returned conversation %s, expected %s", conv.ConversationID, id)
	}
	state.LegacyRepairs = append(state.LegacyRepairs, uiV12LegacyRepair{
		ConversationID: id.String(), OwnerSubject: subject,
		RevisionBefore: revision, RevisionAfter: revisionAfter, RebindRevision: conv.Revision,
		Turns: turns, Messages: messages, Events: events, BindingRequests: bindingRequests,
		BindingRepaired: repaired,
	})
	return id, true, nil
}

//---------------------------------------------------------------------------
// Verification
//---------------------------------------------------------------------------

func uiV12Counts(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID) (uiV12CountSet, error) {
	var out uiV12CountSet
	err := persistence.WithTenantTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
		for _, item := range []struct {
			query string
			into  *int
		}{
			{`SELECT count(*) FROM platform.cluster_registrations WHERE tenant_id = $1`, &out.Clusters},
			{`SELECT count(*) FROM platform.source_registrations WHERE tenant_id = $1`, &out.Sources},
			{`SELECT count(*) FROM platform.role_bindings WHERE tenant_id = $1`, &out.Bindings},
			{`SELECT count(*) FROM platform.resource_entities WHERE tenant_id = $1`, &out.Entities},
			{`SELECT count(*) FROM platform.resource_aliases WHERE tenant_id = $1`, &out.Aliases},
			{`SELECT count(*) FROM investigation.assistant_conversations WHERE tenant_id = $1`, &out.Conversations},
			{`SELECT count(*) FROM platform.adapter_scope_verifications WHERE tenant_id = $1`, &out.Verifications},
			{`SELECT count(*) FROM audit.records WHERE tenant_id = $1`, &out.AuditRecords},
			{`SELECT count(*) FROM platform.source_registration_revisions WHERE tenant_id = $1`, &out.SourceRevisions},
			{`SELECT count(*) FROM platform.cluster_registration_revisions WHERE tenant_id = $1`, &out.ClusterRevisions},
		} {
			if err := tx.QueryRow(ctx, item.query, tenantID).Scan(item.into); err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}

func uiV12TenantSnapshot(ctx context.Context, pool *pgxpool.Pool) (map[string]uuid.UUID, error) {
	rows, err := pool.Query(ctx, `SELECT slug, tenant_id FROM platform.tenants ORDER BY slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]uuid.UUID{}
	for rows.Next() {
		var slug string
		var id uuid.UUID
		if err := rows.Scan(&slug, &id); err != nil {
			return nil, err
		}
		out[slug] = id
	}
	return out, rows.Err()
}

func uiV12VerifyStableIDs(report *uiV12Report, first, second *uiV12State) {
	report.check("idempotent_tenant", first.TenantID == second.TenantID, "tenant=%s", first.TenantID)
	report.check("idempotent_clusters",
		first.ClusterA.ClusterID == second.ClusterA.ClusterID && first.ClusterB.ClusterID == second.ClusterB.ClusterID,
		"a=%s b=%s", first.ClusterA.ClusterID, first.ClusterB.ClusterID)
	report.check("idempotent_sources",
		first.SourceA.SourceID == second.SourceA.SourceID &&
			first.SourceB.SourceID == second.SourceB.SourceID &&
			first.SourceRevoked.SourceID == second.SourceRevoked.SourceID &&
			first.SourceA.Revision == second.SourceA.Revision &&
			first.SourceB.Revision == second.SourceB.Revision &&
			first.SourceRevoked.Revision == second.SourceRevoked.Revision &&
			first.SourceRevoked.Status == second.SourceRevoked.Status,
		"a=%s/%d b=%s/%d revoked=%s/%d/%s",
		first.SourceA.SourceID, first.SourceA.Revision,
		first.SourceB.SourceID, first.SourceB.Revision,
		first.SourceRevoked.SourceID, first.SourceRevoked.Revision, first.SourceRevoked.Status)
	report.check("idempotent_bindings",
		first.BindingA.ID == second.BindingA.ID && first.BindingB.ID == second.BindingB.ID,
		"a=%s b=%s", first.BindingA.ID, first.BindingB.ID)
	report.check("idempotent_conversations",
		first.Conversations[uiV12OperatorASubject] == second.Conversations[uiV12OperatorASubject] &&
			first.Conversations[uiV12OperatorBSubject] == second.Conversations[uiV12OperatorBSubject],
		"a=%s b=%s", first.Conversations[uiV12OperatorASubject], first.Conversations[uiV12OperatorBSubject])
	report.check("idempotent_resources",
		len(first.Resources) == len(second.Resources) && len(first.Resources) == 6,
		"first=%d second=%d", len(first.Resources), len(second.Resources))
}

func uiV12VerifyCounts(report *uiV12Report, first, second uiV12CountSet) {
	report.check("counts_stable", first == second, "first=%+v second=%+v", first, second)
	report.check("side_effects_stable",
		first.AuditRecords == second.AuditRecords &&
			first.SourceRevisions == second.SourceRevisions &&
			first.ClusterRevisions == second.ClusterRevisions,
		"audit=%d/%d sourceRevisions=%d/%d clusterRevisions=%d/%d",
		first.AuditRecords, second.AuditRecords,
		first.SourceRevisions, second.SourceRevisions,
		first.ClusterRevisions, second.ClusterRevisions)
	report.check("expected_counts",
		first.Clusters == 2 && first.Sources == 3 && first.Bindings == 3 &&
			first.Entities == 6 && first.Aliases == 6 && first.Conversations == 2 && first.Verifications == 0 &&
			first.AuditRecords > 0 && first.SourceRevisions == 5 && first.ClusterRevisions == 2,
		"%+v", first)
}

func uiV12VerifyNoDuplicates(ctx context.Context, report *uiV12Report, pool *pgxpool.Pool, tenantID uuid.UUID) {
	var bindings, sources int
	err := persistence.WithTenantTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM (
			   SELECT subject, role_name FROM platform.role_bindings
			   WHERE tenant_id = $1 GROUP BY subject, role_name HAVING count(*) > 1
			 ) d`, tenantID).Scan(&bindings); err != nil {
			return err
		}
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM (
			   SELECT source_type, instance_key FROM platform.source_registrations
			   WHERE tenant_id = $1 GROUP BY source_type, instance_key HAVING count(*) > 1
			 ) d`, tenantID).Scan(&sources)
	})
	report.check("no_duplicate_bindings", err == nil && bindings == 0, "duplicates=%d err=%v", bindings, err)
	report.check("no_duplicate_sources", err == nil && sources == 0, "duplicates=%d err=%v", sources, err)
}

func uiV12VerifyTenantNonInterference(report *uiV12Report, before, after map[string]uuid.UUID) {
	changed := []string{}
	for slug, id := range before {
		if after[slug] != id {
			changed = append(changed, slug)
		}
	}
	sort.Strings(changed)
	report.check("existing_tenants_unchanged", len(changed) == 0, "changed=%v", changed)
	added := []string{}
	unexpected := []string{}
	for slug := range after {
		if _, ok := before[slug]; !ok {
			added = append(added, slug)
			if slug != uiV12BootstrapSlug && slug != uiV12FixtureSlug {
				unexpected = append(unexpected, slug)
			}
		}
	}
	sort.Strings(added)
	sort.Strings(unexpected)
	report.check("only_task_tenants_added", len(added) <= 2 && len(unexpected) == 0, "added=%v unexpected=%v", added, unexpected)
}

func uiV12VerifyResources(ctx context.Context, report *uiV12Report, pool *pgxpool.Pool, state *uiV12State) {
	podAProd, okA := state.Resources["a/pod/prod"]
	report.check("resource_a_pod_matched", okA, "status=%s", podAProd.Status)
	podACanary := state.Resources["a/pod/canary"]
	podBProd := state.Resources["b/pod/prod"]
	nsAProd := state.Resources["a/namespace/prod"]
	nsACanary := state.Resources["a/namespace/canary"]
	nsBProd := state.Resources["b/namespace/prod"]

	report.check("same_pod_name_differs_by_cluster",
		podAProd.CanonicalID.String() != podBProd.CanonicalID.String() &&
			podAProd.CanonicalID.Scope == uiV12ClusterAUID && podBProd.CanonicalID.Scope == uiV12ClusterBUID,
		"a=%s b=%s", podAProd.CanonicalID.String(), podBProd.CanonicalID.String())
	report.check("same_pod_name_differs_by_namespace",
		podAProd.CanonicalID.String() != podACanary.CanonicalID.String() &&
			podAProd.CanonicalID.StableID == uiV12PodAProdUID &&
			podACanary.CanonicalID.StableID == uiV12PodACanaryUID,
		"prod=%s canary=%s", podAProd.CanonicalID.String(), podACanary.CanonicalID.String())
	report.check("same_namespace_name_differs_by_cluster",
		nsAProd.CanonicalID.String() != nsBProd.CanonicalID.String() &&
			nsAProd.CanonicalID.StableID == uiV12NSAProdUID &&
			nsBProd.CanonicalID.StableID == uiV12NSBProdUID &&
			nsACanary.CanonicalID.StableID == uiV12NSACanaryUID,
		"a-prod=%s canary=%s b-prod=%s", nsAProd.CanonicalID.StableID, nsACanary.CanonicalID.StableID, nsBProd.CanonicalID.StableID)

	var pods, linkedPods, distinctNamespaces int
	namespaces := map[string]string{}
	err := persistence.WithTenantTx(ctx, pool, state.TenantID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM platform.resource_entities WHERE tenant_id = $1 AND kind = 'Pod' AND name = 'api-0'`,
			state.TenantID).Scan(&pods); err != nil {
			return err
		}
		rows, err := tx.Query(ctx,
			`SELECT canonical_id, namespace FROM platform.resource_entities
			 WHERE tenant_id = $1 AND kind = 'Pod' AND name = 'api-0' ORDER BY canonical_id`, state.TenantID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var canonical, namespace string
			if err := rows.Scan(&canonical, &namespace); err != nil {
				rows.Close()
				return err
			}
			parsed, err := resource.ParseCanonicalID(canonical)
			if err != nil {
				rows.Close()
				return err
			}
			namespaces[parsed.Scope+"/"+namespace] = canonical
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		return tx.QueryRow(ctx,
			`SELECT count(*), count(DISTINCT n.canonical_id)
			   FROM platform.resource_entities p
			   JOIN platform.resource_entities n
			     ON n.tenant_id = p.tenant_id AND n.cluster_id = p.cluster_id
			    AND n.kind = 'Namespace' AND n.name = p.namespace
			  WHERE p.tenant_id = $1 AND p.kind = 'Pod' AND p.name = 'api-0'`,
			state.TenantID).Scan(&linkedPods, &distinctNamespaces)
	})
	report.check("persisted_three_same_named_pods", err == nil && pods == 3, "pods=%d err=%v", pods, err)
	report.check("persisted_pod_namespace_links",
		err == nil && linkedPods == 3 && distinctNamespaces == 3,
		"links=%d distinctNamespaces=%d", linkedPods, distinctNamespaces)
	report.check("persisted_pod_scopes",
		err == nil && len(namespaces) == 3 &&
			namespaces[uiV12ClusterAUID+"/prod"] != "" &&
			namespaces[uiV12ClusterAUID+"/canary"] != "" &&
			namespaces[uiV12ClusterBUID+"/prod"] != "",
		"scopes=%v", namespaces)
}

func uiV12VerifyAuthorization(ctx context.Context, report *uiV12Report, pool *pgxpool.Pool, state *uiV12State) {
	admin := state.Admin
	authz := graph.Authorization{Pool: pool}
	_, adminErr := authz.Effective(ctx, state.TenantID.String(), admin.Subject, uiV12ClusterAUID)
	report.check("platform_admin_has_no_operator_scope", errors.Is(adminErr, graph.ErrScope), "err=%v", adminErr)

	scopeA, errA := authz.Effective(ctx, state.TenantID.String(), uiV12OperatorASubject, uiV12ClusterAUID)
	scopeB, errB := authz.Effective(ctx, state.TenantID.String(), uiV12OperatorBSubject, uiV12ClusterBUID)
	report.check("operator_a_scope_cluster_a", errA == nil && scopeA.Cluster == uiV12ClusterAUID && scopeA.ClusterScoped, "err=%v scope=%+v", errA, scopeA)
	report.check("operator_b_scope_cluster_b", errB == nil && scopeB.Cluster == uiV12ClusterBUID && scopeB.ClusterScoped, "err=%v scope=%+v", errB, scopeB)

	_, crossA := authz.Effective(ctx, state.TenantID.String(), uiV12OperatorASubject, uiV12ClusterBUID)
	_, crossB := authz.Effective(ctx, state.TenantID.String(), uiV12OperatorBSubject, uiV12ClusterAUID)
	report.check("operator_cluster_isolation", errors.Is(crossA, graph.ErrScope) && errors.Is(crossB, graph.ErrScope), "a-on-b=%v b-on-a=%v", crossA, crossB)

	podA := state.Resources["a/pod/prod"].CanonicalID.String()
	podB := state.Resources["b/pod/prod"].CanonicalID.String()
	report.check("operator_scope_allows_own_resources",
		scopeA.Allows(podA, "prod") && scopeB.Allows(podB, "prod"),
		"a=%t b=%t", scopeA.Allows(podA, "prod"), scopeB.Allows(podB, "prod"))
	hasNamespace := func(values []string, want string) bool {
		for _, value := range values {
			if value == want {
				return true
			}
		}
		return false
	}
	report.check("operator_namespace_grants_explicit",
		hasNamespace(scopeA.Namespaces, "prod") && hasNamespace(scopeA.Namespaces, "canary") &&
			hasNamespace(scopeB.Namespaces, "prod") && !hasNamespace(scopeB.Namespaces, "canary"),
		"a=%v b=%v", scopeA.Namespaces, scopeB.Namespaces)
	report.check("operator_scope_rejects_other_cluster",
		!scopeA.Allows(podB, "prod") && !scopeB.Allows(podA, "prod"),
		"a-on-b=%t b-on-a=%t", scopeA.Allows(podB, "prod"), scopeB.Allows(podA, "prod"))
	report.check("operator_scope_rejects_other_namespace",
		!scopeB.Allows(podB, "canary") && !scopeA.Allows(podA, "other"),
		"b-canary=%t a-other=%t", scopeB.Allows(podB, "canary"), scopeA.Allows(podA, "other"))

	tenantService, err := tenant.NewService(pool)
	if err != nil {
		report.check("tenant_service_available", false, "err=%v", err)
		return
	}
	bindings, err := tenantService.ListCurrentRoleBindings(ctx, admin)
	if err != nil {
		report.check("list_fixture_bindings", false, "err=%v", err)
		return
	}
	adminGrant, operatorA, operatorB := 0, false, false
	for _, binding := range bindings {
		if binding.Status != "active" {
			continue
		}
		switch {
		case binding.Subject == admin.Subject && binding.Role == auth.PlatformAdmin:
			adminGrant++
		case binding.Subject == uiV12OperatorASubject && binding.Role == auth.Operator:
			operatorA = true
		case binding.Subject == uiV12OperatorBSubject && binding.Role == auth.Operator:
			operatorB = true
		}
	}
	report.check("operators_have_no_admin_binding", adminGrant == 1 && operatorA && operatorB, "admin=%d a=%t b=%t", adminGrant, operatorA, operatorB)

	sourceService, err := source.NewService(pool, source.CredentialVerifierFunc(
		func(context.Context, string, source.SourceIdentity, source.FindingEnvelope) error {
			return source.ErrUnauthorized
		}))
	if err != nil {
		report.check("source_service_available", false, "err=%v", err)
		return
	}
	for _, subject := range []string{uiV12OperatorASubject, uiV12OperatorBSubject} {
		_, err := sourceService.ListSources(ctx, auth.RequestContext{
			RequestID: uiV12FixtureTask, TenantID: state.TenantID, Subject: subject,
			Roles: []auth.Role{auth.Operator},
		})
		report.check("operator_rejected_by_admin_endpoint/"+subject, errors.Is(err, source.ErrUnauthorized), "err=%v", err)
	}
}

func uiV12VerifySourceStates(ctx context.Context, report *uiV12Report, pool *pgxpool.Pool, state *uiV12State) {
	svc, err := source.NewService(pool, source.CredentialVerifierFunc(
		func(context.Context, string, source.SourceIdentity, source.FindingEnvelope) error {
			return source.ErrUnauthorized
		}))
	if err != nil {
		report.check("source_service_for_states", false, "err=%v", err)
		return
	}
	list, err := svc.ListSources(ctx, state.Admin)
	if err != nil {
		report.check("list_fixture_sources", false, "err=%v", err)
		return
	}
	byKey := map[string]source.SourceRegistration{}
	for _, item := range list {
		byKey[item.InstanceKey] = item
	}
	a, b, revoked := byKey["ui-v12-r2-a-native"], byKey["ui-v12-r2-b-native"], byKey["ui-v12-r2-b-revoked"]
	report.check("source_revision_advanced", a.Revision == 2 && a.Status == "active", "revision=%d status=%s", a.Revision, a.Status)
	report.check("source_active_native", b.Revision == 1 && b.Status == "active", "revision=%d status=%s", b.Revision, b.Status)
	report.check("source_revoked_state", revoked.Status == "disabled" && revoked.Revision == 2, "revision=%d status=%s", revoked.Revision, revoked.Status)

	repo := evidence.Repository{Pool: pool}
	validate := func(item source.SourceRegistration, revision int64) evidence.Binding {
		return evidence.Binding{
			SourceType: item.SourceType, Tenant: state.TenantID.String(), SourceID: item.SourceID.String(),
			Revision: revision, BackendLogicalID: item.BackendLogicalID, ScopeMapping: item.DataScopeMapping,
		}
	}
	active := validate(b, b.Revision)
	report.check("active_source_binding_matches", repo.CheckBinding(ctx, active) == nil, "binding accepted")
	report.check("unqualified_source_rejected",
		errors.Is(repo.Authorize(ctx, active), evidence.ErrScopeUnverified), "no fabricated qualification proof")
	stale := validate(a, 1)
	report.check("stale_source_revision_rejected",
		repo.CheckBinding(ctx, stale) != nil && errors.Is(repo.Authorize(ctx, stale), evidence.ErrScopeUnverified),
		"expected revision 1, current 2")
	off := validate(revoked, revoked.Revision)
	report.check("revoked_source_rejected",
		repo.CheckBinding(ctx, off) != nil && errors.Is(repo.Authorize(ctx, off), evidence.ErrScopeUnverified), "status disabled")
}

func uiV12VerifyHistory(ctx context.Context, report *uiV12Report, pool *pgxpool.Pool, state *uiV12State) {
	repo := assistant.Repository{Pool: pool}
	listA, errA := repo.List(ctx, state.TenantID, uiV12OperatorASubject, time.Time{}, uuid.Nil, 50)
	listB, errB := repo.List(ctx, state.TenantID, uiV12OperatorBSubject, time.Time{}, uuid.Nil, 50)
	listLogin, errLogin := repo.List(ctx, state.TenantID, "ui_v12_api", time.Time{}, uuid.Nil, 50)
	conversationA := state.Conversations[uiV12OperatorASubject]
	conversationB := state.Conversations[uiV12OperatorBSubject]
	report.check("history_owner_a",
		errA == nil && len(listA) == 1 && listA[0].ConversationID == conversationA,
		"expected=%s count=%d err=%v", conversationA, len(listA), errA)
	report.check("history_owner_b",
		errB == nil && len(listB) == 1 && listB[0].ConversationID == conversationB,
		"expected=%s count=%d err=%v", conversationB, len(listB), errB)
	report.check("login_name_has_no_history",
		errLogin == nil && len(listLogin) == 0, "count=%d err=%v", len(listLogin), errLogin)

	_, crossErr := repo.Get(ctx, state.TenantID, conversationB, uiV12OperatorASubject)
	report.check("history_cross_owner_rejected", crossErr != nil, "err=%v", crossErr)
	got, ownErr := repo.Get(ctx, state.TenantID, conversationA, uiV12OperatorASubject)
	report.check("history_owner_read", ownErr == nil && got.OwnerSubject == uiV12OperatorASubject, "err=%v", ownErr)

	var owners int
	err := persistence.WithTenantTx(ctx, pool, state.TenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM investigation.assistant_conversations
			 WHERE tenant_id = $1 AND owner_subject IN ($2, $3)`,
			state.TenantID, uiV12OperatorASubject, uiV12OperatorBSubject).Scan(&owners)
	})
	report.check("history_owner_rows_preserved", err == nil && owners == 2, "owners=%d err=%v", owners, err)
}

func uiV12VerifySubjectDependency(ctx context.Context, report *uiV12Report, pool *pgxpool.Pool, tenantID uuid.UUID) {
	var nativeRaw []byte
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(json_agg(n.nspname || '.' || c.relname ORDER BY n.nspname, c.relname), '[]'::json)
		   FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		  WHERE c.relkind = 'r' AND n.nspname IN ('platform', 'investigation', 'audit', 'action')
		    AND c.relname = ANY (ARRAY['users','user_accounts','user_credentials','principals','login_sessions','user_sessions','native_users','native_sessions'])`).Scan(&nativeRaw); err != nil {
		report.check("native_identity_probe", false, "err=%v", err)
		return
	}
	var nativeTables []string
	if err := json.Unmarshal(nativeRaw, &nativeTables); err != nil {
		report.check("native_identity_probe", false, "decode err=%v", err)
		return
	}
	var stepUp bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('platform.step_up_sessions') IS NOT NULL`).Scan(&stepUp); err != nil {
		report.check("native_identity_probe", false, "step-up probe err=%v", err)
		return
	}
	report.check("native_user_tables_absent", len(nativeTables) == 0,
		"nativeTables=%v stepUpSessionsPresent=%t (step-up sessions are not a native account/login proof)", nativeTables, stepUp)

	var loginBindings int
	err := persistence.WithTenantTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM platform.role_bindings
			 WHERE tenant_id = $1 AND subject IN ('ui_v12_api', 'ui_v12_worker', 'ui_v12_migration')`,
			tenantID).Scan(&loginBindings)
	})
	report.check("no_login_name_binding_inheritance", err == nil && loginBindings == 0, "bindings=%d err=%v", loginBindings, err)
}

func uiV12FixtureEvidence(state *uiV12State) map[string]any {
	out := map[string]any{
		"tenantId": state.TenantID.String(),
		"clusters": []map[string]any{
			{"clusterUid": state.ClusterA.ClusterUID, "clusterId": state.ClusterA.ClusterID.String(), "sourceId": state.SourceA.SourceID.String()},
			{"clusterUid": state.ClusterB.ClusterUID, "clusterId": state.ClusterB.ClusterID.String(), "sourceId": state.SourceB.SourceID.String()},
		},
		"sources": []map[string]any{
			{"instanceKey": state.SourceA.InstanceKey, "sourceId": state.SourceA.SourceID.String(), "revision": state.SourceA.Revision, "status": state.SourceA.Status},
			{"instanceKey": state.SourceB.InstanceKey, "sourceId": state.SourceB.SourceID.String(), "revision": state.SourceB.Revision, "status": state.SourceB.Status},
			{"instanceKey": state.SourceRevoked.InstanceKey, "sourceId": state.SourceRevoked.SourceID.String(), "revision": state.SourceRevoked.Revision, "status": state.SourceRevoked.Status},
		},
		"bindings": []map[string]any{
			{"subject": uiV12OperatorASubject, "role": "operator", "clusterUid": state.ClusterA.ClusterUID},
			{"subject": uiV12OperatorBSubject, "role": "operator", "clusterUid": state.ClusterB.ClusterUID},
			{"subject": uiV12AdminSubject, "role": "platform_admin", "clusterUid": ""},
		},
		"resources": map[string]string{},
		"history": map[string]string{
			uiV12OperatorASubject: state.Conversations[uiV12OperatorASubject].String(),
			uiV12OperatorBSubject: state.Conversations[uiV12OperatorBSubject].String(),
		},
		"legacyRepairs": state.LegacyRepairs,
	}
	resources := out["resources"].(map[string]string)
	for key, resolution := range state.Resources {
		resources[key] = resolution.CanonicalID.String()
	}
	return out
}

func uiV12WriteReport(report *uiV12Report, path string) error {
	if path == "" {
		return errors.New("OPS_UI_V12_FIXTURE_EVIDENCE must name a new, unique evidence file")
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode fixture evidence: %w", err)
	}
	// O_EXCL is deliberate: each run gets a new evidence file and can never
	// overwrite an earlier run's result. A write failure fails the test.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create exclusive evidence %s: %w", path, err)
	}
	if _, err := file.Write(append(raw, '\n')); err != nil {
		_ = file.Close()
		return fmt.Errorf("write evidence %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close evidence %s: %w", path, err)
	}
	return nil
}
