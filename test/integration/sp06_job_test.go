package integration

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"ops-platform/internal/auth"
	"ops-platform/internal/configregistry"
	"ops-platform/internal/finding"
	"ops-platform/internal/graph"
	"ops-platform/internal/incident"
	"ops-platform/internal/investigation"
	"ops-platform/internal/policy"
	"os"
	"sync"
	"testing"
	"time"
)

func TestSP06JobLeaseReplayAndAtomicBudget(t *testing.T) {
	if os.Getenv("SP03_TEST_DATABASE_URL") == "" {
		t.Fatal("SP06 requires an actual isolated PostgreSQL instance")
	}
	ctx, db, pool, b := sp05Database(t)
	fs := finding.Service{Pool: pool}
	f, _, err := fs.Ingest(ctx, b, sp05Envelope(b, "sp06-event", "sp06-occurrence"))
	if err != nil {
		t.Fatal(err)
	}
	if err = fs.RelayPass(ctx, b.TenantID, incident.Consume, 50); err != nil {
		t.Fatal(err)
	}
	var incidentID uuid.UUID
	if err = db.QueryRowContext(ctx, `SELECT incident_id FROM incident.finding_links WHERE finding_id=$1`, f.FindingID).Scan(&incidentID); err != nil {
		t.Fatal(err)
	}
	var revision int64
	if err = db.QueryRowContext(ctx, `SELECT revision FROM incident.records WHERE incident_id=$1`, incidentID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	clusters := `["` + b.ClusterID.String() + `"]`
	if _, err = db.ExecContext(ctx, `INSERT INTO platform.role_bindings(tenant_id,binding_id,subject,role_name,cluster_scopes) VALUES($1,$2,'sp06-operator','operator',$3)`, b.TenantID, uuid.New(), clusters); err != nil {
		t.Fatal(err)
	}
	scope, err := (graph.Authorization{Pool: pool}).Effective(ctx, b.TenantID.String(), "sp06-operator", b.ClusterUID)
	if err != nil {
		t.Fatal(err)
	}
	repo := investigation.Repository{Pool: pool}
	req := investigation.CreateInvestigationRequest{TenantID: b.TenantID, IncidentID: incidentID, Subject: "sp06-operator", TriggerRevision: revision, TriggerKind: "manual", PolicyVersion: sp06Policy(t, ctx, db, pool, b.TenantID), Scope: scope, ToolCatalogDigest: "catalog/v1", Budget: investigation.DefaultBudget()}
	req.Budget.ToolCalls = 2
	j, err := repo.CreateJob(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := repo.CreateJob(ctx, req)
	if err != nil || j.JobID != duplicate.JobID {
		t.Fatalf("duplicate job %v", err)
	}
	var wg sync.WaitGroup
	claims := make(chan investigation.Lease, 2)
	errs := make(chan error, 2)
	for range 2 {
		wg.Go(func() { l, e := repo.Claim(ctx, b.TenantID, "worker", time.Minute); claims <- l; errs <- e })
	}
	wg.Wait()
	close(claims)
	close(errs)
	count := 0
	for e := range errs {
		if e == nil {
			count++
		} else if !errors.Is(e, investigation.ErrNoJob) {
			t.Fatal(e)
		}
	}
	if count != 1 {
		t.Fatalf("double claim %d", count)
	}
	var lease investigation.Lease
	for l := range claims {
		if l.Token != uuid.Nil {
			lease = l
		}
	}
	call := investigation.Call{StepID: uuid.New(), ContextID: uuid.New(), JTI: uuid.NewString(), Name: "get_incident_context", ArgsDigest: "sha256:args", Reserve: investigation.Usage{ToolCalls: 1, ResultBytes: 1024}}
	if _, err = repo.BeginCall(ctx, lease, call); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.BeginCall(ctx, lease, call); !errors.Is(err, investigation.ErrReplay) {
		t.Fatalf("nonce replay %v", err)
	}
	if err = repo.CompleteStep(ctx, lease, call.StepID, []byte(`{"partial":false}`), investigation.Usage{ToolCalls: 1, ResultBytes: 17}); err != nil {
		t.Fatal(err)
	}
	call.ContextID = uuid.New()
	call.JTI = uuid.NewString()
	if _, err = repo.BeginCall(ctx, lease, call); !errors.Is(err, investigation.ErrCommitted) {
		t.Fatalf("successful step repeated %v", err)
	}
	old := lease
	if _, err = db.ExecContext(ctx, `UPDATE investigation.worker_queue SET lease_expires_at=clock_timestamp()-interval '1 second' WHERE job_id=$1`, j.JobID); err != nil {
		t.Fatal(err)
	}
	lease, err = repo.Claim(ctx, b.TenantID, "takeover", time.Minute)
	if err != nil || lease.Generation != old.Generation+1 {
		t.Fatalf("takeover %v", err)
	}
	if err = repo.Heartbeat(ctx, old, time.Minute); !errors.Is(err, investigation.ErrLease) {
		t.Fatalf("old lease %v", err)
	}
	call.StepID = uuid.New()
	call.ContextID = uuid.New()
	call.JTI = uuid.NewString()
	if _, err = repo.BeginCall(ctx, lease, call); err != nil {
		t.Fatal(err)
	}
	call.StepID = uuid.New()
	call.JTI = uuid.NewString()
	if _, err = repo.BeginCall(ctx, lease, call); !errors.Is(err, investigation.ErrBudget) {
		t.Fatalf("reservation exceeded budget %v", err)
	}
	if err = repo.Cancel(ctx, lease); !errors.Is(err, investigation.ErrLease) {
		t.Fatalf("terminal job accepted old cancel fence: %v", err)
	}
	terminal, err := repo.Get(ctx, b.TenantID, j.JobID)
	if err != nil || terminal.State != "partial" || terminal.ErrorCode != "BUDGET_EXHAUSTED" || terminal.Reserved != (investigation.Usage{}) {
		t.Fatalf("budget did not terminate and settle job: state=%s err=%v", terminal.State, err)
	}
	if err = repo.Heartbeat(ctx, lease, time.Minute); !errors.Is(err, investigation.ErrLease) {
		t.Fatalf("cancelled heartbeat %v", err)
	}
}

func sp06Policy(t *testing.T, ctx context.Context, db *sql.DB, pool *pgxpool.Pool, tenant uuid.UUID, keys ...ed25519.PrivateKey) string {
	apiPool, err := pgxpool.NewWithConfig(ctx, runtimePoolConfig(t, ctx, db, pool.Config().ConnConfig.ConnString(), "api_runtime_role"))
	if err != nil {
		t.Fatal(err)
	}
	defer apiPool.Close()
	if _, err = db.ExecContext(ctx, `INSERT INTO platform.role_bindings(tenant_id,binding_id,subject,role_name) VALUES($1,$2,'sp06-admin','platform_admin')`, tenant, uuid.New()); err != nil {
		t.Fatal(err)
	}
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	if len(keys) == 1 {
		priv = keys[0]
		pub = priv.Public().(ed25519.PublicKey)
	}
	trust := configregistry.Ed25519TrustStore{Keys: map[string]ed25519.PublicKey{"integration-key": pub}}
	compiler, _ := policy.NewBundleCompiler(trust)
	service, _ := configregistry.NewService(apiPool, trust, compiler)
	actor := auth.RequestContext{TenantID: tenant, Subject: "sp06-admin", Roles: []auth.Role{auth.PlatformAdmin}}
	draft, err := createRegistryDraft(ctx, apiPool, service, actor, configregistry.DraftCommand{Kind: configregistry.KindPolicy, LogicalName: "sp06-read-only", Content: policyRegistryContent(t, "sp06-read-only", "sp06")})
	if err != nil {
		t.Fatal(err)
	}
	version, err := signAndPublish(ctx, apiPool, service, actor, draft, 1, priv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = activateRegistryVersion(ctx, apiPool, service, actor, version, configregistry.KindPolicy, "sp06-read-only", configregistry.Scope{Type: configregistry.ScopeTenant}, 0); err != nil {
		t.Fatal(err)
	}
	return version.VersionID.String()
}

func sp06JobFixture(t *testing.T) (context.Context, *sql.DB, investigation.Repository, investigation.CreateInvestigationRequest) {
	if os.Getenv("SP03_TEST_DATABASE_URL") == "" {
		t.Fatal("SP06 requires an actual isolated PostgreSQL instance")
	}
	ctx, db, pool, b := sp05Database(t)
	fs := finding.Service{Pool: pool}
	f, _, err := fs.Ingest(ctx, b, sp05Envelope(b, "sp06-event", "sp06-occurrence"))
	if err != nil {
		t.Fatal(err)
	}
	if err = fs.RelayPass(ctx, b.TenantID, incident.Consume, 50); err != nil {
		t.Fatal(err)
	}
	var incidentID uuid.UUID
	if err = db.QueryRowContext(ctx, `SELECT incident_id FROM incident.finding_links WHERE finding_id=$1`, f.FindingID).Scan(&incidentID); err != nil {
		t.Fatal(err)
	}
	var revision int64
	if err = db.QueryRowContext(ctx, `SELECT revision FROM incident.records WHERE incident_id=$1`, incidentID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	clusters := `["` + b.ClusterID.String() + `"]`
	if _, err = db.ExecContext(ctx, `INSERT INTO platform.role_bindings(tenant_id,binding_id,subject,role_name,cluster_scopes) VALUES($1,$2,'sp06-operator','operator',$3)`, b.TenantID, uuid.New(), clusters); err != nil {
		t.Fatal(err)
	}
	scope, err := (graph.Authorization{Pool: pool}).Effective(ctx, b.TenantID.String(), "sp06-operator", b.ClusterUID)
	if err != nil {
		t.Fatal(err)
	}
	repo := investigation.Repository{Pool: pool}
	req := investigation.CreateInvestigationRequest{TenantID: b.TenantID, IncidentID: incidentID, Subject: "sp06-operator", TriggerRevision: revision, TriggerKind: "manual", PolicyVersion: sp06Policy(t, ctx, db, pool, b.TenantID), Scope: scope, ToolCatalogDigest: "catalog/v1", Budget: investigation.DefaultBudget()}
	return ctx, db, repo, req
}
