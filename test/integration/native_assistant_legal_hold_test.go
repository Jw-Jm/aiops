package integration

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"ops-platform/internal/analytics"
	"ops-platform/internal/assistant"
	"ops-platform/internal/auth"
	"ops-platform/internal/identity"
	"ops-platform/internal/investigation"
)

// A native platform administrator must be able to set and release an
// assistant legal hold against a real PostgreSQL instance. The fence is the
// live native session, the account's own subject and the current
// platform_admin grant: no legacy platform.role_bindings row is involved and
// none is created. Every refusal must leave the hold, the messages, the query
// results and the audit trail untouched.

type nativeHoldAccount struct {
	userID    uuid.UUID
	subject   string
	sessionID uuid.UUID
	ctx       context.Context
}

// newNativeHoldAccount creates a real native account, grant and live session
// in the given tenant and returns the request context the API would carry.
func newNativeHoldAccount(t *testing.T, ctx context.Context, db *sql.DB, apiPool *pgxpool.Pool, tenant uuid.UUID, role, scope string) nativeHoldAccount {
	t.Helper()
	userID := uuid.Must(uuid.NewV7())
	login := "nhold." + uuid.Must(uuid.NewV7()).String()[:12]
	passwordHash, err := identity.HashPassword("fixture-hold-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO identity.accounts
		(tenant_id,user_id,login_name,display_name,subject,status,password_state,must_change_password,account_revision,credential_version,authorization_version,session_version)
		VALUES($1,$2,$3,'Native Hold',$4,'active','active',false,1,1,1,1)`, tenant, userID, login, "user:"+userID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO identity.credentials(tenant_id,user_id,credential_version,password_hash,status) VALUES($1,$2,1,$3,'active')`, tenant, userID, passwordHash); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO identity.role_grants(tenant_id,user_id,binding_id,role,scope,authorization_version,status) VALUES($1,$2,$3,$4,$5,1,'active')`,
		tenant, userID, uuid.New(), role, scope); err != nil {
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
	if _, err := db.ExecContext(ctx, `INSERT INTO identity.sessions
		(tenant_id,session_id,user_id,mode,token_digest,csrf_digest,issued_credential_version,issued_authorization_version,issued_session_version,issued_at,last_seen_at,idle_expires_at,absolute_expires_at)
		VALUES($1,$2,$3,'full',$4,$5,1,1,1,clock_timestamp(),clock_timestamp(),clock_timestamp()+interval '30 minutes',clock_timestamp()+interval '8 hours')`,
		tenant, uuid.Must(uuid.NewV7()), userID, digest[:], csrfDigest[:]); err != nil {
		t.Fatal(err)
	}
	sessions, err := identity.NewSessionRepository(apiPool)
	if err != nil {
		t.Fatal(err)
	}
	active, err := sessions.FindActiveSession(ctx, digest[:])
	if err != nil {
		t.Fatalf("find fixture session: %v", err)
	}
	return nativeHoldAccount{userID: userID, subject: active.Subject, sessionID: active.SessionID, ctx: auth.WithNativeSession(ctx, auth.NativeSessionContext{Session: active})}
}

// nativeHoldTurn creates one assistant turn with a message and a query result,
// owned by the legacy operator fixture.
func nativeHoldTurn(t *testing.T, ctx context.Context, db *sql.DB, repo assistant.Repository, tenant uuid.UUID, clusterUID, title string) assistant.Turn {
	t.Helper()
	conversation, err := repo.Create(ctx, assistant.Conversation{
		TenantID: tenant, OwnerSubject: "assistant-owner", Title: title, Mode: "data_query",
		AuthorizationDomain: "operational", ClusterUID: clusterUID,
		Binding: assistant.ConversationBinding{Timezone: "UTC", Selection: analytics.Selection{Namespaces: []string{"apps"}}},
	})
	if err != nil {
		t.Fatalf("create conversation: %v", err)
	}
	turn, err := repo.Submit(ctx, tenant, conversation.ConversationID, conversation.OwnerSubject, "native-hold-"+uuid.Must(uuid.NewV7()).String(), assistant.SubmitMessage{Message: "保留证据", ExpectedConversationRevision: 1})
	if err != nil {
		t.Fatalf("submit turn: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO investigation.assistant_query_results(tenant_id,query_result_id,turn_id,owner_subject,plan_digest,result,scope) VALUES($1,$2,$3,'assistant-owner','sha256:fixture','{}'::jsonb,'{}'::jsonb)`, tenant, uuid.New(), turn.TurnID); err != nil {
		t.Fatalf("insert query result: %v", err)
	}
	return turn
}

// nativeHoldState reads the persisted hold, the message flag, the query result
// flag and the audit trail of one turn.
func nativeHoldState(t *testing.T, db *sql.DB, tenant, turn uuid.UUID, subject string) (int, bool, int, int, int) {
	t.Helper()
	var holds int
	var held bool
	if err := db.QueryRow(`SELECT count(*),COALESCE(bool_or(held),false) FROM investigation.assistant_legal_holds WHERE tenant_id=$1 AND turn_id=$2`, tenant, turn).Scan(&holds, &held); err != nil {
		t.Fatal(err)
	}
	var messages int
	if err := db.QueryRow(`SELECT count(*) FROM investigation.assistant_messages WHERE tenant_id=$1 AND turn_id=$2 AND legal_hold`, tenant, turn).Scan(&messages); err != nil {
		t.Fatal(err)
	}
	var results int
	if err := db.QueryRow(`SELECT count(*) FROM investigation.assistant_query_results WHERE tenant_id=$1 AND turn_id=$2 AND legal_hold`, tenant, turn).Scan(&results); err != nil {
		t.Fatal(err)
	}
	var audited int
	if err := db.QueryRow(`SELECT count(*) FROM audit.records WHERE tenant_id=$1 AND event_type='assistant.legal_hold.changed' AND subject=$2`, tenant, subject).Scan(&audited); err != nil {
		t.Fatal(err)
	}
	return holds, held, messages, results, audited
}

func TestNativeAdminAssistantLegalHoldPersistsAndAudits(t *testing.T) {
	ctx, db, worker, b, repo, _ := assistantExtendedFixture(t)
	api, err := pgxpool.NewWithConfig(ctx, runtimePoolConfig(t, ctx, db, worker.Config().ConnString(), "api_runtime_role"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(api.Close)
	turn := nativeHoldTurn(t, ctx, db, repo, b.TenantID, b.ClusterUID, "原生保留")
	admin := newNativeHoldAccount(t, ctx, db, api, b.TenantID, "platform_admin", `{"clusterUids":[],"namespaces":[]}`)

	if err := repo.SetLegalHold(admin.ctx, b.TenantID, turn.TurnID, admin.subject, true, "原生管理员保留"); err != nil {
		t.Fatalf("native administrator set legal hold: %v", err)
	}
	holds, held, messages, results, audited := nativeHoldState(t, db, b.TenantID, turn.TurnID, admin.subject)
	if holds != 1 || !held || messages == 0 || results != 1 || audited != 1 {
		t.Fatalf("hold state holds=%d held=%v messages=%d results=%d audited=%d", holds, held, messages, results, audited)
	}
	var reason, subject string
	if err := db.QueryRow(`SELECT reason,subject FROM investigation.assistant_legal_holds WHERE tenant_id=$1 AND turn_id=$2`, b.TenantID, turn.TurnID).Scan(&reason, &subject); err != nil {
		t.Fatal(err)
	}
	if reason != "原生管理员保留" || subject != admin.subject {
		t.Fatalf("hold row reason=%q subject=%q", reason, subject)
	}
	// No legacy binding was created for the native administrator.
	var legacy int
	if err := db.QueryRow(`SELECT count(*) FROM platform.role_bindings WHERE tenant_id=$1 AND subject=$2`, b.TenantID, admin.subject).Scan(&legacy); err != nil {
		t.Fatal(err)
	}
	if legacy != 0 {
		t.Fatalf("native hold created %d legacy bindings", legacy)
	}

	// Releasing the hold is the same native path.
	if err := repo.SetLegalHold(admin.ctx, b.TenantID, turn.TurnID, admin.subject, false, "原生管理员解除"); err != nil {
		t.Fatalf("native administrator release legal hold: %v", err)
	}
	holds, held, messages, results, audited = nativeHoldState(t, db, b.TenantID, turn.TurnID, admin.subject)
	if holds != 1 || held || messages != 0 || results != 0 || audited != 2 {
		t.Fatalf("released hold state holds=%d held=%v messages=%d results=%d audited=%d", holds, held, messages, results, audited)
	}
}

func TestNativeAssistantLegalHoldRefusesWithoutAuthority(t *testing.T) {
	ctx, db, worker, b, repo, _ := assistantExtendedFixture(t)
	api, err := pgxpool.NewWithConfig(ctx, runtimePoolConfig(t, ctx, db, worker.Config().ConnString(), "api_runtime_role"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(api.Close)
	turn := nativeHoldTurn(t, ctx, db, repo, b.TenantID, b.ClusterUID, "原生保留拒绝")

	// A native operator is not an audit-domain administrator.
	operator := newNativeHoldAccount(t, ctx, db, api, b.TenantID, "operator", `{"clusterUids":["`+b.ClusterUID+`"],"namespaces":["apps"]}`)
	if err := repo.SetLegalHold(operator.ctx, b.TenantID, turn.TurnID, operator.subject, true, "operator must not hold"); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("operator hold error = %v", err)
	}

	// A disabled account loses the authority even with a live session.
	disabled := newNativeHoldAccount(t, ctx, db, api, b.TenantID, "platform_admin", `{"clusterUids":[],"namespaces":[]}`)
	if _, err := db.ExecContext(ctx, `UPDATE identity.accounts SET status='disabled' WHERE tenant_id=$1 AND user_id=$2`, b.TenantID, disabled.userID); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetLegalHold(disabled.ctx, b.TenantID, turn.TurnID, disabled.subject, true, "disabled account"); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("disabled account hold error = %v", err)
	}

	// A moved authorization version invalidates the live session.
	moved := newNativeHoldAccount(t, ctx, db, api, b.TenantID, "platform_admin", `{"clusterUids":[],"namespaces":[]}`)
	if _, err := db.ExecContext(ctx, `UPDATE identity.accounts SET authorization_version=authorization_version+1 WHERE tenant_id=$1 AND user_id=$2`, b.TenantID, moved.userID); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetLegalHold(moved.ctx, b.TenantID, turn.TurnID, moved.subject, true, "stale version"); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("stale authorization version hold error = %v", err)
	}

	// A revoked grant is refused as well.
	revoked := newNativeHoldAccount(t, ctx, db, api, b.TenantID, "platform_admin", `{"clusterUids":[],"namespaces":[]}`)
	if _, err := db.ExecContext(ctx, `UPDATE identity.role_grants SET status='revoked',revoked_at=clock_timestamp(),revoked_reason='integration revoke' WHERE tenant_id=$1 AND user_id=$2`, b.TenantID, revoked.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE identity.accounts SET authorization_version=authorization_version+1 WHERE tenant_id=$1 AND user_id=$2`, b.TenantID, revoked.userID); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetLegalHold(revoked.ctx, b.TenantID, turn.TurnID, revoked.subject, true, "revoked grant"); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("revoked grant hold error = %v", err)
	}

	// A forged subject never establishes an identity.
	admin := newNativeHoldAccount(t, ctx, db, api, b.TenantID, "platform_admin", `{"clusterUids":[],"namespaces":[]}`)
	forged := "user:" + uuid.New().String()
	if err := repo.SetLegalHold(admin.ctx, b.TenantID, turn.TurnID, forged, true, "forged subject"); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("forged subject hold error = %v", err)
	}

	// Without the native session the request is no native administrator.
	if err := repo.SetLegalHold(ctx, b.TenantID, turn.TurnID, admin.subject, true, "no session"); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("session-less hold error = %v", err)
	}

	// A native administrator of another tenant cannot hold this turn.
	otherTenant := uuid.Must(uuid.NewV7())
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.tenants(tenant_id,slug,display_name) VALUES($1,'nhold-other','Native Hold Other')`, otherTenant); err != nil {
		t.Fatal(err)
	}
	other := newNativeHoldAccount(t, ctx, db, api, otherTenant, "platform_admin", `{"clusterUids":[],"namespaces":[]}`)
	if err := repo.SetLegalHold(other.ctx, otherTenant, turn.TurnID, other.subject, true, "cross tenant"); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("cross tenant hold error = %v", err)
	}
	if err := repo.SetLegalHold(other.ctx, b.TenantID, turn.TurnID, other.subject, true, "cross tenant"); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("cross tenant subject hold error = %v", err)
	}

	// Nothing was written by any of the refused attempts.
	holds, held, messages, results, audited := nativeHoldState(t, db, b.TenantID, turn.TurnID, admin.subject)
	if holds != 0 || held || messages != 0 || results != 0 || audited != 0 {
		t.Fatalf("refused hold wrote state holds=%d held=%v messages=%d results=%d audited=%d", holds, held, messages, results, audited)
	}
}

// The runtime role must not be able to write a hold without the fence.
func TestNativeAssistantLegalHoldKeepsTheRuntimeRoleFenced(t *testing.T) {
	ctx, db, worker, b, repo, _ := assistantExtendedFixture(t)
	api, err := pgxpool.NewWithConfig(ctx, runtimePoolConfig(t, ctx, db, worker.Config().ConnString(), "api_runtime_role"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(api.Close)
	turn := nativeHoldTurn(t, ctx, db, repo, b.TenantID, b.ClusterUID, "原生保留围栏")
	if _, err := db.ExecContext(ctx, `UPDATE investigation.assistant_legal_holds SET held=false WHERE tenant_id=$1`, b.TenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO investigation.assistant_legal_holds(tenant_id,turn_id,held,reason,subject) VALUES($1,$2,true,'forged','forged') ON CONFLICT(tenant_id,turn_id) DO NOTHING`, b.TenantID, turn.TurnID); err != nil {
		t.Fatal(err)
	}
	var held bool
	if err := db.QueryRow(`SELECT held FROM investigation.assistant_legal_holds WHERE tenant_id=$1 AND turn_id=$2`, b.TenantID, turn.TurnID).Scan(&held); err != nil {
		t.Fatal(err)
	}
	if !held {
		t.Fatal("the fixture hold row was not written")
	}
	// The API runtime role may not change the hold row directly.
	if _, err := api.Exec(ctx, `UPDATE investigation.assistant_legal_holds SET held=false WHERE tenant_id=$1 AND turn_id=$2`, b.TenantID, turn.TurnID); err == nil {
		t.Fatal("the API runtime role updated a hold row directly")
	}
	// And the native fence function is not callable with a forged subject.
	admin := newNativeHoldAccount(t, ctx, db, api, b.TenantID, "platform_admin", `{"clusterUids":[],"namespaces":[]}`)
	var allowed bool
	if err := api.QueryRow(ctx, `SELECT investigation.assistant_set_hold_native($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		b.TenantID, turn.TurnID, admin.userID, admin.sessionID, 1, 1, 1, "user:"+uuid.New().String(), true, "forged subject").Scan(&allowed); err != nil {
		t.Fatal(err)
	}
	if allowed {
		t.Fatal("the native fence accepted a forged subject")
	}
}
