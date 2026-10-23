package integration

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"ops-platform/internal/action"
	"ops-platform/internal/auth"
	"ops-platform/internal/finding"
	"ops-platform/internal/identity"
	"ops-platform/internal/incident"
	"ops-platform/internal/resource"
)

// The native Action chain must survive the browser session that admitted it.
// This guarded test runs the real repository paths against a disposable
// PostgreSQL instance and exercises: risk assessment and acknowledgement by a
// native operator, execution creation with a frozen native authority, dispatch and
// claim without any session, output and completion callbacks, native post-check
// re-proof, cancellation and an unknown external result. Revoked, re-versioned,
// tampered and cross-tenant authorities must refuse instead of falling back to the
// legacy binding table.
type nativeActionFixture struct {
	ctx         context.Context
	db          *sql.DB
	apiPool     *pgxpool.Pool
	workerPool  *pgxpool.Pool
	tenantID    uuid.UUID
	clusterID   uuid.UUID
	clusterUID  string
	policyID    string
	target      string
	profile     action.Profile
	incidentID  uuid.UUID
	accountCtx  context.Context
	subject     string
	userID      uuid.UUID
	sessionID   uuid.UUID
	service     action.Service
	worker      action.Service
}

func newNativeActionFixture(t *testing.T) *nativeActionFixture {
	t.Helper()
	ctx, db, workerPool, b := sp05Database(t)
	f := &nativeActionFixture{ctx: ctx, db: db, workerPool: workerPool, tenantID: b.TenantID, clusterID: b.ClusterID, clusterUID: b.ClusterUID}
	var database string
	if err := db.QueryRowContext(ctx, `SELECT current_database()`).Scan(&database); err != nil {
		t.Fatalf("read fixture database: %v", err)
	}
	dsn, err := databaseURLForDatabase(os.Getenv("SP03_TEST_DATABASE_URL"), database)
	if err != nil {
		t.Fatalf("fixture database url: %v", err)
	}
	apiConfig := runtimePoolConfig(t, ctx, db, dsn, "api_runtime_role")
	apiPool, err := pgxpool.NewWithConfig(ctx, apiConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(apiPool.Close)
	f.apiPool = apiPool

	fs := finding.Service{Pool: workerPool}
	findingRecord, _, err := fs.Ingest(ctx, b, sp05Envelope(b, "native-action-event", "native-action-occurrence"))
	if err != nil {
		t.Fatalf("ingest finding: %v", err)
	}
	if err = fs.RelayPass(ctx, b.TenantID, incident.Consume, 50); err != nil {
		t.Fatalf("relay incident: %v", err)
	}
	if err = db.QueryRowContext(ctx, `SELECT incident_id FROM incident.finding_links WHERE tenant_id=$1 AND finding_id=$2`, b.TenantID, findingRecord.FindingID).Scan(&f.incidentID); err != nil {
		t.Fatalf("read incident: %v", err)
	}

	f.policyID = sp06Policy(t, ctx, db, apiPool, b.TenantID)
	// A Pod target so the post-check applicability is a supported kind; the
	// incident ownership check only requires a finding in the same cluster.
	f.target = resource.CanonicalID{Domain: "k8s", Tenant: b.TenantID.String(), Scope: b.ClusterUID, APIGroup: "core", Kind: "Pod", StableID: "native-action-pod"}.String()
	if _, err = db.ExecContext(ctx, `INSERT INTO platform.resource_entities(tenant_id,canonical_id,cluster_id,kind,namespace,name,metadata,observed_at) VALUES($1,$2,$3,'Pod','test-ns','pod-a','{"sourceValues":{"uid":"native-action-pod"}}',clock_timestamp())`, b.TenantID, f.target, b.ClusterID); err != nil {
		t.Fatalf("insert target: %v", err)
	}
	profile := action.Profile{SchemaVersion: "execution-profile/v2", ID: uuid.New(), Version: 1, Name: "native-action-profile", Type: "k8s_namespace", Namespace: "test-ns", AllowedTargets: []string{f.target}, ClusterUID: b.ClusterUID, ToolImageDigest: "localhost/tool@" + action.Digest([]byte("tool")), Tools: []string{"bash"}, NetworkPolicyRef: "test-network", CredentialRef: "openbao://test", TimeoutSeconds: 900, MaxOutputBytes: 10 << 20}
	if profile.Validate() != nil {
		t.Fatal("invalid fixture profile")
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO platform.execution_profile_versions(tenant_id,profile_version_id,logical_name,version_number,content,digest,signature,signer_key_id,status,published_by) VALUES($1,$2,'native-action-profile',1,$3,$4,'x','test','published','test')`, b.TenantID, uuid.New(), action.Canonical(profile), action.Digest(action.Canonical(profile))); err != nil {
		t.Fatalf("insert profile: %v", err)
	}
	f.profile = profile
	// One test-only sealer stands in for the same task OpenBao Transit key used by
	// both the API and the Worker; it is not an OpenBao capability claim.
	protector := &sp07Protector{plain: map[uuid.UUID][]byte{}}
	f.service = action.Service{Pool: apiPool, Policy: sp07Policy{f.policyID}, Protector: protector}
	f.worker = action.Service{Pool: workerPool, Policy: sp07Policy{f.policyID}, Protector: protector}
	f.newNativeAccount(t)
	return f
}

// newNativeAccount creates one real native operator account with a namespace
// scoped grant and a live 'full' session, exactly as the HTTP login would.
func (f *nativeActionFixture) newNativeAccount(t *testing.T) {
	t.Helper()
	f.userID = uuid.Must(uuid.NewV7())
	login := "naction." + uuid.Must(uuid.NewV7()).String()[:12]
	f.subject = "user:" + f.userID.String()
	hash, err := identity.HashPassword("fixture-native-action-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO identity.accounts
		(tenant_id,user_id,login_name,display_name,subject,status,password_state,must_change_password,account_revision,credential_version,authorization_version,session_version)
		VALUES($1,$2,$3,'Native Action Operator',$4,'active','active',false,1,1,1,1)`, f.tenantID, f.userID, login, f.subject); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO identity.credentials(tenant_id,user_id,credential_version,password_hash,status) VALUES($1,$2,1,$3,'active')`, f.tenantID, f.userID, hash); err != nil {
		t.Fatalf("insert credential: %v", err)
	}
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO identity.role_grants(tenant_id,user_id,binding_id,role,scope,authorization_version,status) VALUES($1,$2,$3,'operator',$4,1,'active')`,
		f.tenantID, f.userID, uuid.New(), `{"clusterUids":["`+f.clusterUID+`"],"namespaces":["test-ns"]}`); err != nil {
		t.Fatalf("insert grant: %v", err)
	}
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
	f.sessionID = uuid.Must(uuid.NewV7())
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO identity.sessions
		(tenant_id,session_id,user_id,mode,token_digest,csrf_digest,issued_credential_version,issued_authorization_version,issued_session_version,issued_at,last_seen_at,idle_expires_at,absolute_expires_at)
		VALUES($1,$2,$3,'full',$4,$5,1,1,1,clock_timestamp(),clock_timestamp(),clock_timestamp()+interval '30 minutes',clock_timestamp()+interval '8 hours')`,
		f.tenantID, f.sessionID, f.userID, digest[:], csrfDigest[:]); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	sessions, err := identity.NewSessionRepository(f.apiPool)
	if err != nil {
		t.Fatal(err)
	}
	active, err := sessions.FindActiveSession(f.ctx, digest[:])
	if err != nil {
		t.Fatalf("find fixture session: %v", err)
	}
	if active.Subject != f.subject {
		t.Fatalf("native subject = %q want %q", active.Subject, f.subject)
	}
	f.accountCtx = auth.WithNativeSession(f.ctx, auth.NativeSessionContext{Session: active})
}

func (f *nativeActionFixture) actor() auth.RequestContext {
	return auth.RequestContext{TenantID: f.tenantID, Subject: f.subject, Roles: []auth.Role{auth.Operator}, ClusterScopes: []uuid.UUID{f.clusterID}, TokenExpiresAt: time.Now().Add(time.Hour)}
}

func (f *nativeActionFixture) commandRequest() action.CommandRequest {
	return action.CommandRequest{IncidentID: f.incidentID, ActualCommand: "printf 'native command'", Target: f.target, Shell: "bash", ProfileID: f.profile.ID, ProfileVersion: 1, Options: action.Options{TimeoutSeconds: 900, MaxOutputBytes: 10 << 20, Environment: map[string]string{}}}
}

// prepare runs the real request path with the native session and returns the
// stored execution. The acknowledgement is consumed on success.
func (f *nativeActionFixture) prepare(t *testing.T, key string) action.Execution {
	t.Helper()
	actor := f.actor()
	req := f.commandRequest()
	assessment, err := f.service.AssessRisk(f.accountCtx, actor, req, "assess-"+key)
	if err != nil {
		t.Fatalf("risk assessment: %v", err)
	}
	ack, err := f.service.AcknowledgeRisk(f.accountCtx, actor, assessment.ID, assessment.RequestDigest, "ack-"+key)
	if err != nil {
		t.Fatalf("risk acknowledgement: %v", err)
	}
	e, err := f.service.PrepareCommandExecution(f.accountCtx, actor, req, ack.ID, "prepare-"+key)
	if err != nil {
		t.Fatalf("prepare execution: %v", err)
	}
	return e
}

func (f *nativeActionFixture) executionState(t *testing.T, id uuid.UUID) string {
	t.Helper()
	var state string
	if err := f.db.QueryRowContext(f.ctx, `SELECT state FROM action.executions WHERE tenant_id=$1 AND execution_id=$2`, f.tenantID, id).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

// postCheckReason reads the stored report reason of the latest post-check event.
func (f *nativeActionFixture) postCheckReason(t *testing.T, id uuid.UUID) string {
	t.Helper()
	var payload []byte
	if err := f.db.QueryRowContext(f.ctx, `SELECT payload FROM action.events WHERE tenant_id=$1 AND execution_id=$2 AND event_type='post_check' ORDER BY event_seq DESC LIMIT 1`, f.tenantID, id).Scan(&payload); err != nil {
		t.Fatalf("read post-check event: %v", err)
	}
	var stored struct {
		Report action.PostCheckReport `json:"report"`
	}
	if err := json.Unmarshal(payload, &stored); err != nil {
		t.Fatal(err)
	}
	return stored.Report.Reason
}

func (f *nativeActionFixture) revoke(t *testing.T) {
	t.Helper()
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.role_grants SET status='revoked',revoked_at=clock_timestamp(),revoked_reason='integration revoke',authorization_version=authorization_version+1 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, f.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.accounts SET authorization_version=authorization_version+1 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, f.userID); err != nil {
		t.Fatal(err)
	}
}

func TestNativeActionPostCheckAuthority(t *testing.T) {
	f := newNativeActionFixture(t)

	// The request path (with the native session) persists which identity system
	// admitted the command. The Worker later re-proves that source, not a subject.
	e := f.prepare(t, "chain")
	var authorityRaw []byte
	if err := f.db.QueryRowContext(f.ctx, `SELECT authority FROM action.executions WHERE tenant_id=$1 AND execution_id=$2`, f.tenantID, e.ID).Scan(&authorityRaw); err != nil {
		t.Fatal(err)
	}
	var authority map[string]any
	if err := json.Unmarshal(authorityRaw, &authority); err != nil {
		t.Fatal(err)
	}
	if authority["source"] != "native" || authority["userId"] != f.userID.String() {
		t.Fatalf("persisted authority = %+v", authority)
	}
	if version, ok := authority["authorizationVersion"].(float64); !ok || version != 1 {
		t.Fatalf("persisted authorization version = %v", authority["authorizationVersion"])
	}

	// Dispatch and claim run in the Worker, without the browser session. A native
	// command must not need a legacy role binding to be dispatched.
	d, err := f.worker.BeginDispatch(f.ctx, f.tenantID, e.ID)
	if err != nil {
		t.Fatalf("native dispatch without a session: %v", err)
	}
	if len(d.Token) != 43 {
		t.Fatalf("dispatch token length = %d", len(d.Token))
	}
	claim, err := f.worker.Claim(f.ctx, f.tenantID, e.ID, d.Token)
	if err != nil {
		t.Fatalf("native claim without a session: %v", err)
	}
	if claim.Command != f.commandRequest().ActualCommand {
		t.Fatalf("claimed command = %q", claim.Command)
	}
	if err = f.worker.AppendOutput(f.ctx, f.tenantID, e.ID, d.Token, action.OutputChunk{ExecutionID: e.ID, Seq: 1, Stream: "stdout", Bytes: []byte("native command")}); err != nil {
		t.Fatalf("append output: %v", err)
	}
	if err = f.worker.Finish(f.ctx, f.tenantID, e.ID, d.Token, 0, 1, false); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if state := f.executionState(t, e.ID); state != "succeeded" {
		t.Fatalf("execution state = %q", state)
	}

	// The post-check collection scope is rebuilt from the frozen native authority
	// through the live grant rows.
	scope, err := f.worker.ScopeForExecution(f.ctx, e)
	if err != nil {
		t.Fatalf("native post-check scope: %v", err)
	}
	if !scope.Allows(f.target, "test-ns") {
		t.Fatalf("native post-check scope does not cover the target: %+v", scope)
	}

	// Recording the check re-proves the native authority inside the transaction.
	// Incomplete facts are the expected verdict here; the reason must not be an
	// authorization failure.
	facts := action.PostCheckFacts{Authorized: true, SourceAvailable: true, Complete: false, CheckedAt: time.Now().UTC(), EvidenceRefs: []string{}}
	if err = f.worker.RecordPostCheck(f.ctx, f.tenantID, e.ID, facts); err != nil {
		t.Fatalf("record native post-check: %v", err)
	}
	if reason := f.postCheckReason(t, e.ID); reason != "facts_incomplete" {
		t.Fatalf("native post-check reason = %q want facts_incomplete", reason)
	}

	// A session read of the completed post-check still works for the native owner.
	view, err := f.service.GetPostCheck(f.accountCtx, f.actor(), e.ID)
	if err != nil {
		t.Fatalf("native post-check view: %v", err)
	}
	if view.Status != "completed" || view.Report == nil || view.Report.Verdict != "inconclusive" {
		t.Fatalf("native post-check view = %+v", view)
	}
}

func TestNativeActionDispatchRefusesLostAuthority(t *testing.T) {
	f := newNativeActionFixture(t)

	// Prepared before the revocation, dispatched after it: the frozen version no
	// longer matches the live account/grant rows, so the attempt fails closed and
	// no dispatch token is minted.
	revokedExecution := f.prepare(t, "revoked")
	f.revoke(t)
	if _, err := f.worker.BeginDispatch(f.ctx, f.tenantID, revokedExecution.ID); !errors.Is(err, action.ErrDenied) {
		t.Fatalf("dispatch after revocation = %v want ErrDenied", err)
	}
	if state := f.executionState(t, revokedExecution.ID); state != "failed" {
		t.Fatalf("revoked dispatch state = %q want failed", state)
	}
	var attempts int
	if err := f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM action.attempts WHERE tenant_id=$1 AND execution_id=$2`, f.tenantID, revokedExecution.ID).Scan(&attempts); err != nil || attempts != 0 {
		t.Fatalf("revoked dispatch attempts = %d (%v)", attempts, err)
	}
	// The post-check cannot borrow authority either.
	if _, err := f.worker.ScopeForExecution(f.ctx, revokedExecution); !errors.Is(err, action.ErrDenied) {
		t.Fatalf("post-check scope after revocation = %v want ErrDenied", err)
	}

	// A mixed or unknown authority encoding is refused rather than classified as
	// legacy. This is the column a Worker reads, so it must never downgrade.
	fresh := newNativeActionFixture(t)
	pending := fresh.prepare(t, "tampered")
	if _, err := fresh.db.ExecContext(fresh.ctx, `UPDATE action.executions SET authority='{"source":"native","userId":"00000000-0000-0000-0000-000000000000","authorizationVersion":0}' WHERE tenant_id=$1 AND execution_id=$2`, fresh.tenantID, pending.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fresh.worker.BeginDispatch(fresh.ctx, fresh.tenantID, pending.ID); !errors.Is(err, action.ErrDenied) {
		t.Fatalf("tampered authority dispatch = %v want ErrDenied", err)
	}
	if _, err := fresh.db.ExecContext(fresh.ctx, `UPDATE action.executions SET authority='{"source":"native","userId":"`+fresh.userID.String()+`","authorizationVersion":1}' WHERE tenant_id=$1 AND execution_id=$2`, fresh.tenantID, pending.ID); err != nil {
		t.Fatal(err)
	}
	// Another tenant cannot dispatch or read the same execution.
	other := uuid.Must(uuid.NewV7())
	if _, err := fresh.worker.BeginDispatch(fresh.ctx, other, pending.ID); err == nil {
		t.Fatal("cross-tenant dispatch succeeded")
	}
}

func TestNativeActionCancelAndUnknownRecovery(t *testing.T) {
	f := newNativeActionFixture(t)
	actor := f.actor()

	// Cancellation of a prepared native execution is a terminal, recorded state.
	cancelled := f.prepare(t, "cancel")
	out, err := f.service.CancelExecution(f.accountCtx, actor, cancelled.ID, "cancel-native")
	if err != nil {
		t.Fatalf("native cancel: %v", err)
	}
	if out.State != "cancelled" {
		t.Fatalf("cancel state = %q", out.State)
	}
	view, err := f.service.GetPostCheck(f.accountCtx, actor, cancelled.ID)
	if err != nil || view.Status != "not_applicable" {
		t.Fatalf("cancelled post-check status = %q (%v)", view.Status, err)
	}
	if _, err = f.service.CancelExecution(f.accountCtx, actor, cancelled.ID, "cancel-native-again"); !errors.Is(err, action.ErrConflict) {
		t.Fatalf("second cancel = %v want ErrConflict", err)
	}

	// An external result that cannot be proven is marked unknown. A late callback
	// must not silently turn it into a success, and the post-check refuses it.
	unknown := f.prepare(t, "unknown")
	d, err := f.worker.BeginDispatch(f.ctx, f.tenantID, unknown.ID)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if _, err = f.worker.Claim(f.ctx, f.tenantID, unknown.ID, d.Token); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err = f.worker.MarkUnknown(f.ctx, f.tenantID, unknown.ID, "runner_result_not_proven"); err != nil {
		t.Fatalf("mark unknown: %v", err)
	}
	if err = f.worker.Finish(f.ctx, f.tenantID, unknown.ID, d.Token, 0, 0, false); err != nil {
		t.Fatalf("late callback: %v", err)
	}
	if state := f.executionState(t, unknown.ID); state != "execution_unknown" {
		t.Fatalf("late callback state = %q want execution_unknown", state)
	}
	unknownView, err := f.service.GetPostCheck(f.accountCtx, actor, unknown.ID)
	if err != nil || unknownView.Status != "execution_unknown" {
		t.Fatalf("unknown post-check status = %q (%v)", unknownView.Status, err)
	}
	if err = f.worker.RecordPostCheck(f.ctx, f.tenantID, unknown.ID, action.PostCheckFacts{Authorized: true, CheckedAt: time.Now().UTC()}); !errors.Is(err, action.ErrConflict) {
		t.Fatalf("post-check on unknown result = %v want ErrConflict", err)
	}
}
