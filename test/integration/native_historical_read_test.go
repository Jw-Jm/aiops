package integration

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"

	"ops-platform/internal/analytics"
	"ops-platform/internal/assistant"
	"ops-platform/internal/auth"
	"ops-platform/internal/bundle"
	"ops-platform/internal/graph"
	"ops-platform/internal/identity"
	"ops-platform/internal/investigation"
	"ops-platform/internal/resource"
)

// These guarded integration tests prove the historical ownership read against a
// real PostgreSQL instance (SP03_TEST_DATABASE_URL, disposable databases only).
// They never mint authority: the native accounts, their operator grants and
// their sessions are real rows, and the reviewed mapping/inventory fixture
// simulates the controlled migration duty that is the only producer of
// mappings. The conversation and turn are written by the ordinary repository
// path; only the committed tool Step and the model answer are fixture rows, so
// the read has to authorize them itself.
type mappedHistoryFixture struct {
	ctx        context.Context
	db         *sql.DB
	apiPool    *pgxpool.Pool
	repo       assistant.Repository
	tenantID   uuid.UUID
	clusterID  uuid.UUID
	clusterUID string
	legacy     string
	userID     uuid.UUID
	subject    string
	sessionID  uuid.UUID
	nativeCtx  context.Context
	convID     uuid.UUID
	turnID     uuid.UUID
	jobID      uuid.UUID
	chain      investigation.FaultChainContext
	chainRef   investigation.FaultChainContextRef
}

const historicalLegacyOwner = "legacy-hist-owner"

// A denied historical read surfaces as ErrDenied or as "no rows" because the
// allowlist filters the lookup itself; both are refusals, never a partial view.
func historicalDenied(err error) bool {
	return errors.Is(err, investigation.ErrDenied) || errors.Is(err, pgx.ErrNoRows)
}

func newMappedHistoryFixture(t *testing.T) *mappedHistoryFixture {
	t.Helper()
	f := &mappedHistoryFixture{clusterUID: "hist-cluster", legacy: historicalLegacyOwner}
	ctx, db, dir, dsn := newMigrationDatabaseWithTimeout(t, 10*time.Minute)
	f.ctx, f.db = ctx, db
	var database string
	if err := db.QueryRowContext(ctx, `SELECT current_database()`).Scan(&database); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `GRANT CREATE ON DATABASE "`+database+`" TO schema_owner`); err != nil {
		t.Fatalf("grant schema duty on the disposable database: %v", err)
	}
	goose.SetDialect("postgres")
	if err := goose.UpToContext(ctx, db, dir, 1); err != nil {
		t.Fatal(err)
	}
	if err := runRemainingMigrationsAsMigrationRole(t, ctx, db, dsn, dir); err != nil {
		t.Fatalf("apply remaining migrations: %v", err)
	}

	f.tenantID, f.clusterID = uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.tenants(tenant_id,slug,display_name) VALUES($1,'hist','Historical')`, f.tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.cluster_registrations(tenant_id,cluster_id,cluster_uid,display_name) VALUES($1,$2,$3,'Historical')`, f.tenantID, f.clusterID, f.clusterUID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.role_bindings(tenant_id,binding_id,subject,role_name,cluster_scopes,namespace_scopes) VALUES($1,$2,$3,'operator',$4,$5)`,
		f.tenantID, uuid.New(), f.legacy, `["`+f.clusterID.String()+`"]`, `[{"clusterId":"`+f.clusterID.String()+`","namespace":"apps"}]`); err != nil {
		t.Fatal(err)
	}
	apiPool, err := pgxpool.NewWithConfig(ctx, runtimePoolConfig(t, ctx, db, dsn, "api_runtime_role"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(apiPool.Close)
	f.apiPool = apiPool
	policy := sp06Policy(t, ctx, db, apiPool, f.tenantID)
	f.repo = assistant.Repository{Pool: apiPool, Jobs: investigation.Repository{Pool: apiPool, AssistantEnabled: true}, Admit: func(context.Context, assistant.Conversation, string) (assistant.Admission, error) {
		return assistant.Admission{PolicyVersion: policy, ModelProfileVersion: "hist-profile/v1", Budget: investigation.DefaultBudget()}, nil
	}}
	legacyConv, err := f.repo.Create(ctx, assistant.Conversation{
		TenantID: f.tenantID, OwnerSubject: f.legacy, Title: "历史归属会话", Mode: "data_query",
		AuthorizationDomain: "operational", ClusterUID: f.clusterUID,
		Binding: assistant.ConversationBinding{Timezone: "UTC", Selection: analytics.Selection{Namespaces: []string{"apps"}}},
	})
	if err != nil {
		t.Fatalf("legacy owner conversation: %v", err)
	}
	f.convID = legacyConv.ConversationID

	f.userID = f.createNativeAccount(t)
	if _, err := db.ExecContext(ctx, `INSERT INTO identity.role_grants(tenant_id,user_id,binding_id,role,scope,authorization_version,status) VALUES($1,$2,$3,'operator',$4,1,'active')`,
		f.tenantID, f.userID, uuid.New(), `{"clusterUids":["`+f.clusterUID+`"],"namespaces":["apps"]}`); err != nil {
		t.Fatal(err)
	}
	// The reviewed inventory plus the explicit mapping is the controlled duty's
	// product. It authorises reading only; no role is copied.
	inventory := identity.SubjectInventory{
		SchemaVersion: "native-subject-inventory/v1", TenantID: f.tenantID, ReviewedBy: "integration-review",
		Entries: []identity.SubjectInventoryEntry{{LegacySubject: f.legacy, UserID: f.userID}},
	}
	rawInventory, err := json.Marshal(inventory)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := bundle.CanonicalizeJSON(rawInventory)
	if err != nil {
		t.Fatal(err)
	}
	evidence := sha256.Sum256(canonical)
	if _, err := db.ExecContext(ctx, `INSERT INTO identity.subject_mapping_inventories(tenant_id,evidence_digest,document,imported_by) VALUES($1,$2,$3,'integration-duty')`, f.tenantID, evidence[:], canonical); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO identity.subject_mappings(tenant_id,mapping_id,legacy_subject,user_id,basis,evidence_digest,inventory_digest) VALUES($1,$2,$3,$4,'verified_inventory',$5,$5)`,
		f.tenantID, uuid.Must(uuid.NewV7()), f.legacy, f.userID, evidence[:]); err != nil {
		t.Fatal(err)
	}
	// A real login session for the mapped account, created after the mapping
	// exactly like the controlled duty's session revocation would require.
	f.subject, f.sessionID, f.nativeCtx = f.createSession(t, f.userID)
	return f
}

func (f *mappedHistoryFixture) createNativeAccount(t *testing.T) uuid.UUID {
	t.Helper()
	userID := uuid.Must(uuid.NewV7())
	login := "hist." + uuid.Must(uuid.NewV7()).String()[:12]
	passwordHash, err := identity.HashPassword("fixture-historical-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO identity.accounts
		(tenant_id,user_id,login_name,display_name,subject,status,password_state,must_change_password,account_revision,credential_version,authorization_version,session_version)
		VALUES($1,$2,$3,'Historical Reader',$4,'active','active',false,1,1,1,1)`, f.tenantID, userID, login, "user:"+userID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO identity.credentials(tenant_id,user_id,credential_version,password_hash,status) VALUES($1,$2,1,$3,'active')`, f.tenantID, userID, passwordHash); err != nil {
		t.Fatal(err)
	}
	return userID
}

func (f *mappedHistoryFixture) createSession(t *testing.T, userID uuid.UUID) (string, uuid.UUID, context.Context) {
	t.Helper()
	token := make([]byte, 32)
	csrf := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		t.Fatal(err)
	}
	if _, err := rand.Read(csrf); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(token)
	csrfDigest := sha256.Sum256(csrf)
	sessionID := uuid.Must(uuid.NewV7())
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO identity.sessions
		(tenant_id,session_id,user_id,mode,token_digest,csrf_digest,issued_credential_version,issued_authorization_version,issued_session_version,issued_at,last_seen_at,idle_expires_at,absolute_expires_at)
		VALUES($1,$2,$3,'full',$4,$5,1,1,1,clock_timestamp(),clock_timestamp(),clock_timestamp()+interval '30 minutes',clock_timestamp()+interval '8 hours')`,
		f.tenantID, sessionID, userID, digest[:], csrfDigest[:]); err != nil {
		t.Fatal(err)
	}
	sessions, err := identity.NewSessionRepository(f.apiPool)
	if err != nil {
		t.Fatal(err)
	}
	active, err := sessions.FindActiveSession(f.ctx, digest[:])
	if err != nil {
		t.Fatalf("find fixture session: %v", err)
	}
	return active.Subject, sessionID, auth.WithNativeSession(f.ctx, auth.NativeSessionContext{Session: active})
}

// submitTurn writes a real queued turn through the repository, then commits one
// evaluate_fault_chain Step and the model answer exactly as the Worker ledger
// would, so the read path authorizes real persisted evidence.
func (f *mappedHistoryFixture) submitTurn(t *testing.T) {
	t.Helper()
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO investigation.assistant_worker_capabilities(tenant_id,worker_id,model_profile_version,catalog_version,expires_at) VALUES($1,'hist-fixture','hist-profile/v1','assistant-tools/v2',clock_timestamp()+interval '10 minutes')`, f.tenantID); err != nil {
		t.Fatal(err)
	}
	turn, err := f.repo.Submit(f.ctx, f.tenantID, f.convID, f.legacy, "hist-turn-key", assistant.SubmitMessage{Message: "查看服务依赖故障链", ExpectedConversationRevision: 1})
	if err != nil {
		t.Fatalf("submit historical turn: %v", err)
	}
	f.turnID, f.jobID = turn.TurnID, turn.JobID

	from := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Second)
	request := investigation.FaultChainRequest{
		SchemaVersion:     "fault-chain-request/v1",
		TargetCanonicalID: resource.CanonicalID{Domain: "k8s", Tenant: f.tenantID.String(), Scope: f.clusterUID, APIGroup: "core", Kind: "Pod", StableID: "apps/web-0"}.String(),
		Fault:             analytics.QueryTimeRange{From: from, To: from.Add(5 * time.Minute), Timezone: "UTC"},
		MaxEdges:          4,
	}
	chain, err := investigation.EvaluateFaultChain(request, nil, nil, from.Add(6*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	f.chain = chain
	stepID := uuid.Must(uuid.NewV7())
	chainResult, err := json.Marshal(map[string]any{"tool": "evaluate_fault_chain", "data": map[string]any{"context": chain}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO investigation.steps(tenant_id,job_id,step_id,tool_name,args_digest,state,lease_token,lease_generation,result) VALUES($1,$2,$3,'evaluate_fault_chain',$4,'succeeded',$5,1,$6)`,
		f.tenantID, f.jobID, stepID, graph.ScopeDigest(request), uuid.New(), chainResult); err != nil {
		t.Fatal(err)
	}
	f.chainRef = investigation.FaultChainContextRef{StepID: stepID, ContentDigest: graph.ScopeDigest(chain)}

	answer, err := json.Marshal(map[string]any{
		"schemaVersion":        "assistant-answer/v1",
		"answerKind":           "data",
		"summary":              "仅依据已提交的故障链上下文说明依赖关系，未确认根因。",
		"faultChainContextRef": f.chainRef,
		"factRefs":             []any{},
		"queryResultRefs":      []any{},
		"evidenceRefs":         []any{},
		"entityRefs":           []any{},
		"missingEvidence":      []string{"committed_scoped_resource_relations"},
		"hypotheses":           []any{},
		"nextChecks":           []string{"核对两端应用日志"},
	})
	if err != nil {
		t.Fatal(err)
	}
	eventPayload, err := json.Marshal(map[string]any{"state": "succeeded", "result": json.RawMessage(answer)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO investigation.events(tenant_id,job_id,event_seq,event_type,payload) VALUES($1,$2,1,'result',$3)`, f.tenantID, f.jobID, eventPayload); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(f.ctx, `UPDATE investigation.jobs SET state='succeeded',result=$3 WHERE tenant_id=$1 AND job_id=$2`, f.tenantID, f.jobID, answer); err != nil {
		t.Fatal(err)
	}
	var state string
	var stored []byte
	if err := f.db.QueryRowContext(f.ctx, `SELECT state,answer FROM investigation.assistant_turns WHERE tenant_id=$1 AND turn_id=$2`, f.tenantID, f.turnID).Scan(&state, &stored); err != nil {
		t.Fatal(err)
	}
	if state != "completed" || len(stored) == 0 {
		t.Fatalf("committed turn state=%q answerBytes=%d", state, len(stored))
	}
}

func (f *mappedHistoryFixture) frozenJob(t *testing.T) map[string]any {
	t.Helper()
	var subject, scopeDigest, binding string
	var scope []byte
	if err := f.db.QueryRowContext(f.ctx, `SELECT requesting_subject,effective_scope_digest,effective_scope::text,assistant_binding::text FROM investigation.jobs WHERE tenant_id=$1 AND job_id=$2`, f.tenantID, f.jobID).Scan(&subject, &scopeDigest, &scope, &binding); err != nil {
		t.Fatal(err)
	}
	var turnDigest, turnContext string
	if err := f.db.QueryRowContext(f.ctx, `SELECT input_digest,context_digest FROM investigation.assistant_turns WHERE tenant_id=$1 AND turn_id=$2`, f.tenantID, f.turnID).Scan(&turnDigest, &turnContext); err != nil {
		t.Fatal(err)
	}
	return map[string]any{"subject": subject, "scopeDigest": scopeDigest, "scope": string(scope), "binding": binding, "inputDigest": turnDigest, "contextDigest": turnContext}
}

func TestNativeMappedHistoricalOwnerRead(t *testing.T) {
	f := newMappedHistoryFixture(t)

	if page, err := f.repo.ListPage(f.nativeCtx, f.tenantID, f.subject, time.Time{}, uuid.Nil, 10); err != nil {
		t.Fatalf("list: %v", err)
	} else if len(page.Items) != 1 || page.Items[0].ConversationID != f.convID || page.Items[0].OwnerSubject != f.legacy {
		t.Fatalf("mapped list = %+v", page.Items)
	}
	read, err := f.repo.GetReadable(f.nativeCtx, f.tenantID, f.convID, f.subject)
	if err != nil {
		t.Fatalf("mapped detail read: %v", err)
	}
	if read.OwnerSubject != f.legacy || read.ClusterUID != f.clusterUID {
		t.Fatalf("mapped read changed frozen evidence: %+v", read)
	}
	// The write-capable path still refuses the historical owner.
	if _, err := f.repo.Get(f.nativeCtx, f.tenantID, f.convID, f.subject); err == nil {
		t.Fatal("strict read path served a mapped historical conversation")
	}
	var audited int
	if err := f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM audit.records WHERE tenant_id=$1 AND event_type='assistant.history.mapped_owner.read'`, f.tenantID).Scan(&audited); err != nil {
		t.Fatal(err)
	}
	if audited < 1 {
		t.Fatal("mapped historical read left no access audit record")
	}
	var direct int
	if err := f.apiPool.QueryRow(f.ctx, `SELECT count(*) FROM identity.subject_mappings`).Scan(&direct); err == nil {
		t.Fatal("api runtime role read identity.subject_mappings directly")
	}

	// An account without a mapping still sees nothing.
	otherUser := f.createNativeAccount(t)
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO identity.role_grants(tenant_id,user_id,binding_id,role,scope,authorization_version,status) VALUES($1,$2,$3,'operator',$4,1,'active')`,
		f.tenantID, otherUser, uuid.New(), `{"clusterUids":["`+f.clusterUID+`"],"namespaces":["apps"]}`); err != nil {
		t.Fatal(err)
	}
	otherSubject, _, otherCtx := f.createSession(t, otherUser)
	if page, err := f.repo.ListPage(otherCtx, f.tenantID, otherSubject, time.Time{}, uuid.Nil, 10); err != nil || len(page.Items) != 0 {
		t.Fatalf("unmapped account list = %+v, %v", page.Items, err)
	}
	if _, err := f.repo.GetReadable(otherCtx, f.tenantID, f.convID, otherSubject); !historicalDenied(err) {
		t.Fatalf("unmapped account detail error = %v", err)
	}

	// Revoking the current operator grant takes the historical read away again.
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.role_grants SET status='revoked',revoked_at=clock_timestamp(),revoked_reason='integration revocation' WHERE tenant_id=$1 AND user_id=$2 AND role='operator'`, f.tenantID, f.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.GetReadable(f.nativeCtx, f.tenantID, f.convID, f.subject); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("revoked operator still read the historical conversation: %v", err)
	}
}

func TestNativeMappedHistoricalTurnRead(t *testing.T) {
	f := newMappedHistoryFixture(t)
	f.submitTurn(t)
	before := f.frozenJob(t)

	chain, err := f.repo.FaultChainContext(f.nativeCtx, f.tenantID, f.turnID, f.subject)
	if err != nil {
		t.Fatalf("mapped fault chain read: %v", err)
	}
	if chain.Status != f.chain.Status || graph.ScopeDigest(chain) != f.chainRef.ContentDigest || f.chainRef.StepID == uuid.Nil {
		t.Fatalf("mapped fault chain content = %+v", chain)
	}
	turn, err := f.repo.TurnReadable(f.nativeCtx, f.tenantID, f.turnID, f.subject)
	if err != nil {
		t.Fatalf("mapped turn read: %v", err)
	}
	if turn.TurnID != f.turnID || turn.JobID != f.jobID || len(turn.Answer) == 0 || turn.State != "completed" {
		t.Fatalf("mapped turn = %+v", turn)
	}
	messages, err := f.repo.Messages(f.nativeCtx, f.tenantID, f.convID, f.subject)
	if err != nil {
		t.Fatalf("mapped messages: %v", err)
	}
	roles := map[string]int{}
	for _, m := range messages {
		roles[m.Role]++
	}
	if roles["user"] != 1 || roles["assistant"] != 1 {
		t.Fatalf("mapped messages = %+v", messages)
	}
	_, events, err := f.repo.Events(f.nativeCtx, f.tenantID, f.convID, f.subject, 0)
	if err != nil {
		t.Fatalf("mapped events: %v", err)
	}
	found := false
	for _, e := range events {
		if e.Type == "answer.ready" {
			found = true
		}
	}
	if !found {
		t.Fatalf("mapped events missing answer.ready: %+v", events)
	}

	after := f.frozenJob(t)
	for key, value := range before {
		if after[key] != value {
			t.Fatalf("mapped read rewrote frozen job field %s: %q -> %q", key, value, after[key])
		}
	}

	// The strict write/cancel path never serves the historical turn, and a mapped
	// reading account cannot append or cancel under the frozen owner.
	if _, err := f.repo.Turn(f.nativeCtx, f.tenantID, f.turnID, f.subject); err == nil {
		t.Fatal("strict turn path served a mapped historical turn")
	}
	if _, err := f.repo.Submit(f.nativeCtx, f.tenantID, f.convID, f.subject, "mapped-write-key", assistant.SubmitMessage{Message: "追加一轮", ExpectedConversationRevision: 1}); !historicalDenied(err) {
		t.Fatalf("mapped account wrote into the historical conversation: %v", err)
	}
	if _, err := f.repo.FaultChainContext(f.ctx, f.tenantID, f.turnID, "another-legacy-subject"); !historicalDenied(err) {
		t.Fatalf("foreign legacy subject fault chain error = %v", err)
	}
	// A process context without a live native session (the Worker shape) cannot
	// use the mapped read even though the mapping and the job exist.
	if _, err := f.repo.FaultChainContext(f.ctx, f.tenantID, f.turnID, f.subject); !historicalDenied(err) {
		t.Fatalf("sessionless mapped fault chain error = %v", err)
	}
	// The frozen owner itself keeps its owner-exact read (no mapping needed).
	if _, err := f.repo.FaultChainContext(f.ctx, f.tenantID, f.turnID, f.legacy); err != nil {
		t.Fatalf("owner-exact fault chain read: %v", err)
	}

	// A different tenant sees nothing.
	foreignTenant := uuid.Must(uuid.NewV7())
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO platform.tenants(tenant_id,slug,display_name) VALUES($1,'hist-b','Historical B')`, foreignTenant); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.FaultChainContext(f.nativeCtx, foreignTenant, f.turnID, f.subject); err == nil {
		t.Fatal("cross-tenant fault chain read succeeded")
	}

	// A current grant that no longer covers the frozen namespace is refused even
	// though the mapping still exists. A session snapshot is re-read on every
	// request, so the narrowed grant is exercised through a fresh session.
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.role_grants SET scope=$3 WHERE tenant_id=$1 AND user_id=$2 AND role='operator'`, f.tenantID, f.userID, `{"clusterUids":["`+f.clusterUID+`"],"namespaces":["private"]}`); err != nil {
		t.Fatal(err)
	}
	_, _, narrowCtx := f.createSession(t, f.userID)
	if _, err := f.repo.FaultChainContext(narrowCtx, f.tenantID, f.turnID, f.subject); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("out-of-scope grant fault chain error = %v", err)
	}
	if _, err := f.repo.TurnReadable(narrowCtx, f.tenantID, f.turnID, f.subject); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("out-of-scope grant turn error = %v", err)
	}

	// Revocation removes the mapped read entirely.
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.role_grants SET status='revoked',revoked_at=clock_timestamp(),revoked_reason='integration revocation' WHERE tenant_id=$1 AND user_id=$2 AND role='operator'`, f.tenantID, f.userID); err != nil {
		t.Fatal(err)
	}
	_, _, revokedCtx := f.createSession(t, f.userID)
	if _, err := f.repo.FaultChainContext(revokedCtx, f.tenantID, f.turnID, f.subject); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("revoked operator fault chain error = %v", err)
	}
	if _, err := f.repo.Messages(revokedCtx, f.tenantID, f.convID, f.subject); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("revoked operator messages error = %v", err)
	}
}
