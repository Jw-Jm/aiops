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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"

	"ops-platform/internal/analytics"
	"ops-platform/internal/assistant"
	"ops-platform/internal/auth"
	"ops-platform/internal/identity"
	"ops-platform/internal/investigation"
)

// A business Job admitted by a native operator must be re-proved by the Worker
// without a browser session. This guarded test runs the real repository paths
// (Submit, Claim, GetAssistantInput) against a disposable PostgreSQL instance
// and proves: the identity source/user/version are persisted and frozen, the
// live Worker re-proof accepts them, and revocation, re-grant, disabled account,
// tampered binding and cross-tenant claims all refuse and fail the job.
type nativeJobFixture struct {
	ctx        context.Context
	db         *sql.DB
	apiPool    *pgxpool.Pool
	workerPool *pgxpool.Pool
	repo       assistant.Repository
	jobs       investigation.Repository
	tenantID   uuid.UUID
	clusterID  uuid.UUID
	clusterUID string
	policy     string
}

type nativeJobAccount struct {
	userID    uuid.UUID
	subject   string
	sessionID uuid.UUID
	ctx       context.Context
}

func newNativeJobFixture(t *testing.T) *nativeJobFixture {
	t.Helper()
	f := &nativeJobFixture{clusterUID: "native-job-cluster"}
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
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.tenants(tenant_id,slug,display_name) VALUES($1,'njob','Native Job')`, f.tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.cluster_registrations(tenant_id,cluster_id,cluster_uid,display_name) VALUES($1,$2,$3,'Native Job')`, f.tenantID, f.clusterID, f.clusterUID); err != nil {
		t.Fatal(err)
	}
	apiPool, err := pgxpool.NewWithConfig(ctx, runtimePoolConfig(t, ctx, db, dsn, "api_runtime_role"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(apiPool.Close)
	f.apiPool = apiPool
	workerPool, err := pgxpool.NewWithConfig(ctx, runtimePoolConfig(t, ctx, db, dsn, "worker_runtime_role"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(workerPool.Close)
	f.workerPool = workerPool
	f.policy = sp06Policy(t, ctx, db, apiPool, f.tenantID)
	f.jobs = investigation.Repository{Pool: workerPool, AssistantEnabled: true}
	f.repo = assistant.Repository{Pool: apiPool, Jobs: investigation.Repository{Pool: workerPool, AssistantEnabled: true}, Admit: func(context.Context, assistant.Conversation, string) (assistant.Admission, error) {
		return assistant.Admission{PolicyVersion: f.policy, ModelProfileVersion: "native-job-profile/v1", Budget: investigation.DefaultBudget()}, nil
	}}
	if _, err := db.ExecContext(ctx, `INSERT INTO investigation.assistant_worker_capabilities(tenant_id,worker_id,model_profile_version,catalog_version,expires_at) VALUES($1,'native-job-worker','native-job-profile/v1','assistant-tools/v2',clock_timestamp()+interval '30 minutes')`, f.tenantID); err != nil {
		t.Fatal(err)
	}
	return f
}

// newAccount creates a real native operator account, grant and live session.
func (f *nativeJobFixture) newAccount(t *testing.T, namespaces string) nativeJobAccount {
	t.Helper()
	return f.newAccountWithRole(t, "operator", `{"clusterUids":["`+f.clusterUID+`"],"namespaces":[`+namespaces+`]}`)
}

// newAccountWithRole creates a real native account with one grant and a session.
func (f *nativeJobFixture) newAccountWithRole(t *testing.T, role, scope string) nativeJobAccount {
	t.Helper()
	userID := uuid.Must(uuid.NewV7())
	login := "njob." + uuid.Must(uuid.NewV7()).String()[:12]
	passwordHash, err := identity.HashPassword("fixture-operational-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO identity.accounts
		(tenant_id,user_id,login_name,display_name,subject,status,password_state,must_change_password,account_revision,credential_version,authorization_version,session_version)
		VALUES($1,$2,$3,'Operational Operator',$4,'active','active',false,1,1,1,1)`, f.tenantID, userID, login, "user:"+userID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO identity.credentials(tenant_id,user_id,credential_version,password_hash,status) VALUES($1,$2,1,$3,'active')`, f.tenantID, userID, passwordHash); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO identity.role_grants(tenant_id,user_id,binding_id,role,scope,authorization_version,status) VALUES($1,$2,$3,$4,$5,1,'active')`,
		f.tenantID, userID, uuid.New(), role, scope); err != nil {
		t.Fatal(err)
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
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO identity.sessions
		(tenant_id,session_id,user_id,mode,token_digest,csrf_digest,issued_credential_version,issued_authorization_version,issued_session_version,issued_at,last_seen_at,idle_expires_at,absolute_expires_at)
		VALUES($1,$2,$3,'full',$4,$5,1,1,1,clock_timestamp(),clock_timestamp(),clock_timestamp()+interval '30 minutes',clock_timestamp()+interval '8 hours')`,
		f.tenantID, uuid.Must(uuid.NewV7()), userID, digest[:], csrfDigest[:]); err != nil {
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
	return nativeJobAccount{userID: userID, subject: active.Subject, sessionID: active.SessionID, ctx: auth.WithNativeSession(f.ctx, auth.NativeSessionContext{Session: active})}
}

// submit creates a conversation and one queued business turn for the account.
func (f *nativeJobFixture) submit(t *testing.T, account nativeJobAccount, title string) (uuid.UUID, assistant.Turn) {
	t.Helper()
	conversation, err := f.repo.Create(account.ctx, assistant.Conversation{
		TenantID: f.tenantID, OwnerSubject: account.subject, Title: title, Mode: "data_query",
		AuthorizationDomain: "operational", ClusterUID: f.clusterUID,
		Binding: assistant.ConversationBinding{Timezone: "UTC", Selection: analytics.Selection{Namespaces: []string{"apps"}}},
	})
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	turn, err := f.repo.Submit(account.ctx, f.tenantID, conversation.ConversationID, account.subject, "turn-"+uuid.Must(uuid.NewV7()).String(), assistant.SubmitMessage{Message: "查看服务依赖与最近变更", ExpectedConversationRevision: 1})
	if err != nil {
		t.Fatalf("submit business turn: %v", err)
	}
	return conversation.ConversationID, turn
}

// quarantine keeps an already claimed fixture job out of the queue so a later
// scenario cannot accidentally claim it after its lease expires.
func (f *nativeJobFixture) quarantine(t *testing.T, jobID uuid.UUID) {
	t.Helper()
	if _, err := f.db.ExecContext(f.ctx, `UPDATE investigation.worker_queue SET next_attempt_at=clock_timestamp()+interval '2 hours', lease_expires_at=clock_timestamp()+interval '2 hours' WHERE tenant_id=$1 AND job_id=$2`, f.tenantID, jobID); err != nil {
		t.Fatal(err)
	}
}

func (f *nativeJobFixture) jobState(t *testing.T, jobID uuid.UUID) (string, string) {
	t.Helper()
	var state, code string
	if err := f.db.QueryRowContext(f.ctx, `SELECT state,error_code FROM investigation.jobs WHERE tenant_id=$1 AND job_id=$2`, f.tenantID, jobID).Scan(&state, &code); err != nil {
		t.Fatal(err)
	}
	return state, code
}

func (f *nativeJobFixture) binding(t *testing.T, jobID uuid.UUID) map[string]any {
	t.Helper()
	var rawBinding []byte
	if err := f.db.QueryRowContext(f.ctx, `SELECT assistant_binding FROM investigation.jobs WHERE tenant_id=$1 AND job_id=$2`, f.tenantID, jobID).Scan(&rawBinding); err != nil {
		t.Fatal(err)
	}
	out := map[string]any{}
	if err := json.Unmarshal(rawBinding, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestNativeOperationalJobWorkerAuthority(t *testing.T) {
	f := newNativeJobFixture(t)
	account := f.newAccount(t, `"apps"`)
	_, turn := f.submit(t, account, "原生业务任务")

	binding := f.binding(t, turn.JobID)
	if binding["authorizationSource"] != "native" || binding["authorizationUserId"] != account.userID.String() {
		t.Fatalf("frozen binding did not persist the native authority: %+v", binding)
	}
	if version, ok := binding["authorizationVersion"].(float64); !ok || version != 1 {
		t.Fatalf("frozen binding authorization version = %v", binding["authorizationVersion"])
	}

	// The Worker has no browser session: claim and context read must succeed from
	// the persisted account/version and the live grant rows only.
	workerCtx := context.Background()
	{
		var ver int
		var bid uuid.UUID
		var sc []byte
		if err := f.workerPool.QueryRow(workerCtx, `SELECT authorization_version,binding_id,scope FROM platform.native_operator_scope_shared($1,$2)`, f.tenantID, account.userID).Scan(&ver, &bid, &sc); err != nil {
			t.Logf("diagnostic native_operator_scope: %v", err)
		} else {
			t.Logf("diagnostic native_operator_scope version=%d binding=%s scope=%s", ver, bid, sc)
		}
	}
	lease, err := f.jobs.Claim(workerCtx, f.tenantID, "native-worker", 5*time.Second)
	if err != nil {
		t.Fatalf("worker claim without a browser session: %v", err)
	}
	if lease.JobID != turn.JobID {
		t.Fatalf("worker claimed %s want %s", lease.JobID, turn.JobID)
	}
	input, err := f.jobs.GetAssistantInput(workerCtx, lease)
	if err != nil || len(input) == 0 {
		t.Fatalf("worker read of the admitted input: %v", err)
	}

	// A restarted Worker (new lease after expiry) takes over the same job while
	// the persisted authority is still valid.
	time.Sleep(7 * time.Second)
	second, err := f.jobs.Claim(workerCtx, f.tenantID, "native-worker-restarted", 30*time.Second)
	if err != nil {
		t.Fatalf("worker takeover after restart: %v", err)
	}
	if second.JobID != turn.JobID || second.Generation <= lease.Generation {
		t.Fatalf("takeover lease = %+v (first %+v)", second, lease)
	}
	f.quarantine(t, turn.JobID)

	// Revocation: the account version moves and the grant is revoked, exactly as
	// the administrative path does. The queued job must not be revived.
	revoked := f.newAccount(t, `"apps"`)
	_, revokedTurn := f.submit(t, revoked, "撤权任务")
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.role_grants SET status='revoked',revoked_at=clock_timestamp(),revoked_reason='integration revoke',authorization_version=authorization_version+1 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, revoked.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.accounts SET authorization_version=authorization_version+1 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, revoked.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.jobs.Claim(workerCtx, f.tenantID, "native-worker", 30*time.Second); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("revoked authority claim error = %v", err)
	}
	if state, code := f.jobState(t, revokedTurn.JobID); state != "failed" || code != "AUTHORIZATION_REVOKED" {
		t.Fatalf("revoked job state = %s/%s", state, code)
	}
	// The cancelled turn can no longer be stopped through the API either.
	if err := f.repo.Cancel(revoked.ctx, f.tenantID, revokedTurn.TurnID, revoked.subject, "cancel-"+uuid.Must(uuid.NewV7()).String()[:8]); err == nil {
		t.Fatal("cancel accepted a job whose authority was revoked")
	}

	// Re-grant: a new grant at a higher version must not resurrect the old job.
	regranted := f.newAccount(t, `"apps"`)
	_, regrantedTurn := f.submit(t, regranted, "重授任务")
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.role_grants SET status='revoked',revoked_at=clock_timestamp(),revoked_reason='integration regrant' WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, regranted.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.accounts SET authorization_version=authorization_version+1 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, regranted.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO identity.role_grants(tenant_id,user_id,binding_id,role,scope,authorization_version,status) VALUES($1,$2,$3,'operator',$4,2,'active')`,
		f.tenantID, regranted.userID, uuid.New(), `{"clusterUids":["`+f.clusterUID+`"],"namespaces":["apps"]}`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.jobs.Claim(workerCtx, f.tenantID, "native-worker", 30*time.Second); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("re-granted authority claim error = %v", err)
	}
	if state, code := f.jobState(t, regrantedTurn.JobID); state != "failed" || code != "AUTHORIZATION_REVOKED" {
		t.Fatalf("re-granted job state = %s/%s", state, code)
	}

	// Disabled account: no claim, even though the grant rows still exist.
	disabled := f.newAccount(t, `"apps"`)
	_, disabledTurn := f.submit(t, disabled, "停用任务")
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.accounts SET status='disabled',authorization_version=authorization_version+1 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, disabled.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.role_grants SET authorization_version=authorization_version+1 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, disabled.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.jobs.Claim(workerCtx, f.tenantID, "native-worker", 30*time.Second); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("disabled account claim error = %v", err)
	}
	if state, code := f.jobState(t, disabledTurn.JobID); state != "failed" || code != "AUTHORIZATION_REVOKED" {
		t.Fatalf("disabled job state = %s/%s", state, code)
	}

	// Tampered binding: the persisted user id no longer matches any grant, so the
	// rebuilt scope cannot reproduce the frozen digest.
	tampered := f.newAccount(t, `"apps"`)
	_, tamperedTurn := f.submit(t, tampered, "篡改任务")
	foreignUser := uuid.Must(uuid.NewV7())
	if _, err := f.db.ExecContext(f.ctx, `UPDATE investigation.jobs SET assistant_binding=jsonb_set(assistant_binding,'{authorizationUserId}',to_jsonb($3::text)) WHERE tenant_id=$1 AND job_id=$2`, f.tenantID, tamperedTurn.JobID, foreignUser.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.jobs.Claim(workerCtx, f.tenantID, "native-worker", 30*time.Second); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("tampered binding claim error = %v", err)
	}

	// Tampered frozen scope: the digest no longer matches the scope.
	scopeTampered := f.newAccount(t, `"apps"`)
	_, scopeTurn := f.submit(t, scopeTampered, "篡改范围任务")
	if _, err := f.db.ExecContext(f.ctx, `UPDATE investigation.jobs SET effective_scope=jsonb_set(effective_scope,'{namespaces}',to_jsonb(ARRAY['private']::text[])) WHERE tenant_id=$1 AND job_id=$2`, f.tenantID, scopeTurn.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.jobs.Claim(workerCtx, f.tenantID, "native-worker", 30*time.Second); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("tampered scope claim error = %v", err)
	}

	// Cross-tenant claim never sees the job.
	otherTenant := uuid.Must(uuid.NewV7())
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO platform.tenants(tenant_id,slug,display_name) VALUES($1,'njob-b','Native Job B')`, otherTenant); err != nil {
		t.Fatal(err)
	}
	if _, err := f.jobs.Claim(workerCtx, otherTenant, "native-worker", 30*time.Second); !errors.Is(err, investigation.ErrNoJob) {
		t.Fatalf("cross-tenant claim error = %v", err)
	}

	// A narrowed grant (private namespace only) refuses the frozen apps scope.
	narrowed := f.newAccount(t, `"apps"`)
	_, narrowedTurn := f.submit(t, narrowed, "收窄任务")
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.role_grants SET scope=$3 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, narrowed.userID, `{"clusterUids":["`+f.clusterUID+`"],"namespaces":["private"]}`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.jobs.Claim(workerCtx, f.tenantID, "native-worker", 30*time.Second); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("narrowed grant claim error = %v", err)
	}
	if state, code := f.jobState(t, narrowedTurn.JobID); state != "failed" || code != "AUTHORIZATION_REVOKED" {
		t.Fatalf("narrowed job state = %s/%s", state, code)
	}
}

// loadJob rebuilds the persisted Job exactly as the Worker reads it, so the
// recheck below is the production code path, not a reimplementation.
func (f *nativeJobFixture) loadJob(t *testing.T, jobID uuid.UUID) investigation.Job {
	t.Helper()
	var schemaVersion, subject, policy, scopeDigest string
	var scopeRaw, bindingRaw, budgetRaw, consumedRaw []byte
	var expires time.Time
	var incident uuid.NullUUID
	var trigger sql.NullInt64
	if err := f.db.QueryRowContext(f.ctx, `SELECT schema_version,requesting_subject,policy_version,effective_scope,effective_scope_digest,assistant_binding,budget,budget_consumed,expires_at,incident_id,trigger_revision FROM investigation.jobs WHERE tenant_id=$1 AND job_id=$2`, f.tenantID, jobID).
		Scan(&schemaVersion, &subject, &policy, &scopeRaw, &scopeDigest, &bindingRaw, &budgetRaw, &consumedRaw, &expires, &incident, &trigger); err != nil {
		t.Fatal(err)
	}
	j := investigation.Job{SchemaVersion: schemaVersion, TenantID: f.tenantID, JobID: jobID, Subject: subject, PolicyVersion: policy, EffectiveScopeDigest: scopeDigest, ExpiresAt: expires, TriggerKind: "manual"}
	if json.Unmarshal(scopeRaw, &j.Scope) != nil || json.Unmarshal(bindingRaw, &j.Assistant) != nil || json.Unmarshal(budgetRaw, &j.Budget) != nil || json.Unmarshal(consumedRaw, &j.Consumed) != nil {
		t.Fatal("decode persisted job")
	}
	if incident.Valid {
		j.IncidentID = incident.UUID
	}
	if trigger.Valid {
		j.TriggerRevision = trigger.Int64
	}
	return j
}

// isLockUnavailable reports the lock_timeout error a blocked writer receives.
func isLockUnavailable(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "55P03"
}

// The Worker authority recheck must hold the account/grant rows until its
// business transaction commits, so an administrative revocation cannot slip in
// between the check and the commit. The waiting writer proves the lock with a
// bounded lock_timeout instead of sleeping.
func TestNativeJobAuthorityLocksSerializeRevocation(t *testing.T) {
	f := newNativeJobFixture(t)
	account := f.newAccount(t, `"apps"`)
	_, turn := f.submit(t, account, "并发撤权任务")

	workerTx, err := f.workerPool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = workerTx.Rollback(context.Background()) }()
	if _, err := workerTx.Exec(f.ctx, `SELECT set_config('app.tenant_id',$1,true)`, f.tenantID.String()); err != nil {
		t.Fatal(err)
	}
	if err := investigation.CheckAssistantJobTx(context.Background(), workerTx, f.loadJob(t, turn.JobID)); err != nil {
		t.Fatalf("worker authority recheck: %v", err)
	}
	adminConn, err := f.db.Conn(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer adminConn.Close()
	if _, err := adminConn.ExecContext(f.ctx, `SET lock_timeout='750ms'`); err != nil {
		t.Fatal(err)
	}
	if _, err := adminConn.ExecContext(f.ctx, `UPDATE identity.accounts SET authorization_version=authorization_version+1,session_version=session_version+1 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, account.userID); !isLockUnavailable(err) {
		t.Fatalf("revocation did not wait for the worker authority lock: %v", err)
	}
	if _, err := adminConn.ExecContext(f.ctx, `UPDATE identity.role_grants SET scope=jsonb_set(scope,'{namespaces}',to_jsonb(ARRAY['private']::text[])) WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, account.userID); !isLockUnavailable(err) {
		t.Fatalf("grant narrowing did not wait for the worker authority lock: %v", err)
	}
	if err := workerTx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	// After the Worker transaction commits the same administrative write proceeds.
	if _, err := adminConn.ExecContext(f.ctx, `UPDATE identity.role_grants SET status='revoked',revoked_at=clock_timestamp(),revoked_reason='concurrent revocation',authorization_version=authorization_version+1 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, account.userID); err != nil {
		t.Fatalf("revocation after worker commit: %v", err)
	}
	if _, err := f.jobs.Claim(context.Background(), f.tenantID, "native-worker", 30*time.Second); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("claim after committed revocation error = %v", err)
	}
	if state, code := f.jobState(t, turn.JobID); state != "failed" || code != "AUTHORIZATION_REVOKED" {
		t.Fatalf("revoked job state = %s/%s", state, code)
	}
}

// The reverse order is equally important: an adopted revocation that commits
// before the Worker recheck must refuse the job, and the old unlocked projection
// must no longer be executable by a runtime duty.
func TestNativeJobAuthorityRevocationBeforeWorkerRecheck(t *testing.T) {
	f := newNativeJobFixture(t)
	account := f.newAccount(t, `"apps"`)
	_, turn := f.submit(t, account, "先撤权后复检")
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.accounts SET status='disabled',authorization_version=authorization_version+1,session_version=session_version+1 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, account.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.role_grants SET authorization_version=authorization_version+1 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, account.userID); err != nil {
		t.Fatal(err)
	}
	workerTx, err := f.workerPool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = workerTx.Rollback(context.Background()) }()
	if _, err := workerTx.Exec(f.ctx, `SELECT set_config('app.tenant_id',$1,true)`, f.tenantID.String()); err != nil {
		t.Fatal(err)
	}
	if err := investigation.CheckAssistantJobTx(context.Background(), workerTx, f.loadJob(t, turn.JobID)); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("recheck after committed disable error = %v", err)
	}
	_ = workerTx.Rollback(f.ctx)
	var unlocked int
	if err := f.workerPool.QueryRow(f.ctx, `SELECT count(*) FROM investigation.job_native_operator_scope($1,$2)`, f.tenantID, account.userID).Scan(&unlocked); err == nil {
		t.Fatal("worker could still call the unlocked authority projection")
	}
	if _, err := f.jobs.Claim(context.Background(), f.tenantID, "native-worker", 30*time.Second); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("claim after committed disable error = %v", err)
	}
}

// submitAudit creates an audit-domain conversation and one queued turn for a
// native platform administrator. Audit jobs carry the native authority too.
func (f *nativeJobFixture) submitAudit(t *testing.T, account nativeJobAccount, title string) (uuid.UUID, assistant.Turn) {
	t.Helper()
	conversation, err := f.repo.Create(account.ctx, assistant.Conversation{
		TenantID: f.tenantID, OwnerSubject: account.subject, Title: title, Mode: "data_query",
		AuthorizationDomain: "audit", Binding: assistant.ConversationBinding{Timezone: "UTC"},
	})
	if err != nil {
		t.Fatalf("create audit conversation: %v", err)
	}
	turn, err := f.repo.Submit(account.ctx, f.tenantID, conversation.ConversationID, account.subject, "audit-"+uuid.Must(uuid.NewV7()).String(), assistant.SubmitMessage{Message: "查询审计事件", ExpectedConversationRevision: 1})
	if err != nil {
		t.Fatalf("submit audit turn: %v", err)
	}
	return conversation.ConversationID, turn
}

// Audit jobs re-prove platform_admin authority through platform.native_authority_shared.
func TestNativeAuditJobWorkerAuthority(t *testing.T) {
	f := newNativeJobFixture(t)
	admin := f.newAccountWithRole(t, "platform_admin", `{}`)
	_, first := f.submitAudit(t, admin, "原生审计任务")
	binding := f.binding(t, first.JobID)
	if binding["authorizationSource"] != "native" || binding["authorizationUserId"] != admin.userID.String() {
		t.Fatalf("audit binding did not persist native authority: %+v", binding)
	}
	workerCtx := context.Background()
	lease, err := f.jobs.Claim(workerCtx, f.tenantID, "native-worker", 30*time.Second)
	if err != nil {
		t.Fatalf("audit worker claim: %v", err)
	}
	if lease.JobID != first.JobID {
		t.Fatalf("audit worker claimed %s want %s", lease.JobID, first.JobID)
	}
	input, err := f.jobs.GetAssistantInput(workerCtx, lease)
	if err != nil || len(input) == 0 {
		t.Fatalf("audit worker input read: %v", err)
	}

	// A second audit job queued before the administrative revocation must not be
	// revived by the Worker after the revocation commits.
	_, second := f.submitAudit(t, admin, "撤权后的审计任务")
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.role_grants SET status='revoked',revoked_at=clock_timestamp(),revoked_reason='audit revocation',authorization_version=authorization_version+1 WHERE tenant_id=$1 AND user_id=$2 AND role='platform_admin'`, f.tenantID, admin.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.accounts SET authorization_version=authorization_version+1,session_version=session_version+1 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, admin.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.jobs.Claim(workerCtx, f.tenantID, "native-worker", 30*time.Second); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("audit claim after revocation error = %v", err)
	}
	if state, code := f.jobState(t, second.JobID); state != "failed" || code != "AUTHORIZATION_REVOKED" {
		t.Fatalf("revoked audit job state = %s/%s", state, code)
	}
}

// Two same-account native API transactions hold FOR SHARE (as
// identity.authorize_native_session_role does) and then re-prove authority in the
// same transaction. With SHARE-compatible projections both must succeed; the
// previous FOR UPDATE projections deadlocked on the lock upgrade.
func TestNativeAuthoritySharedLockNoDeadlock(t *testing.T) {
	f := newNativeJobFixture(t)
	account := f.newAccount(t, `"apps"`)
	_, turn := f.submit(t, account, "同账号并发任务")
	job := f.loadJob(t, turn.JobID)

	// The real API entry point holds FOR SHARE on the account/session/grant rows
	// before the assistant read runs. Two same-account API transactions must both
	// reach the shared-lock projection and commit; the previous FOR UPDATE
	// projection deadlocked on the lock upgrade.
	poolConfig := f.apiPool.Config()
	poolConfig.MaxConns = 4
	pool, err := pgxpool.NewWithConfig(f.ctx, poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ready := make(chan struct{}, 2)
	release := make(chan struct{})
	errs := make(chan error, 2)
	for index := 0; index < 2; index++ {
		go func(index int) {
			tx, err := pool.Begin(f.ctx)
			if err != nil {
				errs <- err
				return
			}
			defer func() { _ = tx.Rollback(context.Background()) }()
			if _, err := tx.Exec(f.ctx, `SELECT set_config('app.tenant_id',$1,true)`, f.tenantID.String()); err != nil {
				errs <- err
				return
			}
			var authorized bool
			if err := tx.QueryRow(f.ctx, `SELECT identity.authorize_native_session_role($1,$2,$3,'operator',1,1,1)`, f.tenantID, account.userID, account.sessionID).Scan(&authorized); err != nil || !authorized {
				errs <- errors.New("native session role was not authorized for the concurrent request")
				return
			}
			ready <- struct{}{}
			<-release
			if err := investigation.CheckAssistantJobTx(context.Background(), tx, job); err != nil {
				errs <- err
				return
			}
			errs <- tx.Commit(f.ctx)
		}(index)
	}
	for index := 0; index < 2; index++ {
		select {
		case <-ready:
		case err := <-errs:
			t.Fatalf("concurrent API transaction failed before the barrier: %v", err)
		case <-time.After(15 * time.Second):
			select {
			case err := <-errs:
				t.Fatalf("concurrent API transaction failed: %v", err)
			default:
			}
			t.Fatal("both transactions did not acquire the shared locks")
		}
	}
	close(release)
	for index := 0; index < 2; index++ {
		if err := <-errs; err != nil {
			t.Fatalf("same-account concurrent recheck[%d]: %v", index, err)
		}
	}
	// The job is still claimable afterwards: the concurrent reads held no write lock.
	if _, err := f.jobs.Claim(context.Background(), f.tenantID, "native-worker", 30*time.Second); err != nil {
		t.Fatalf("claim after concurrent shared-lock reads: %v", err)
	}
}
