package integration

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"ops-platform/internal/action"
	"ops-platform/internal/auth"
	"ops-platform/internal/configregistry"
	"ops-platform/internal/finding"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/identity"
	"ops-platform/internal/incident"
	"ops-platform/internal/observability"
	"ops-platform/internal/resource"
)

// nativeCommandHTTPP fixture wires the real native surface: the session
// authenticator with its cookie, the MFA and step-up handlers, and the action
// handlers. The browser part of the chain (cookie, origin, CSRF and the step-up
// challenge header) is the production one; the asynchronous part is executed by a
// Worker service that has no browser session at all.
type nativeCommandHTTPFixture struct {
	ctx        context.Context
	db         *sql.DB
	apiPool    *pgxpool.Pool
	workerPool *pgxpool.Pool
	tenantID   uuid.UUID
	clusterID  uuid.UUID
	clusterUID string
	policyID   string
	policyKey  ed25519.PrivateKey
	target     string
	profile    action.Profile
	incidentID uuid.UUID
	login      string
	password   string
	secret     string
	userID     uuid.UUID
	subject    string
	session    mfaSession
	mfa        mfaClient
	actions    mfaClient
	worker     action.Service
	protector  *sp07Protector
}

func newNativeCommandHTTPFixture(t *testing.T) *nativeCommandHTTPFixture {
	t.Helper()
	ctx, db, workerPool, b := sp05Database(t)
	f := &nativeCommandHTTPFixture{ctx: ctx, db: db, workerPool: workerPool, tenantID: b.TenantID, clusterID: b.ClusterID, clusterUID: b.ClusterUID}
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
	f.policyKey = ed25519.NewKeyFromSeed(make([]byte, 32))
	f.policyID = sp06Policy(t, ctx, db, apiPool, b.TenantID, f.policyKey)
	// The policy publication writes a legacy administrator binding for the
	// fixture author. The native operator must stand on the native grant alone,
	// so the fixture author binding is removed before the assertion below.
	if _, err = db.ExecContext(ctx, `DELETE FROM platform.role_bindings WHERE tenant_id=$1`, b.TenantID); err != nil {
		t.Fatalf("remove the fixture author legacy binding: %v", err)
	}

	fs := finding.Service{Pool: workerPool}
	record, _, err := fs.Ingest(ctx, b, sp05Envelope(b, "native-http-event", "native-http-occurrence"))
	if err != nil {
		t.Fatalf("ingest finding: %v", err)
	}
	if err = fs.RelayPass(ctx, b.TenantID, incident.Consume, 50); err != nil {
		t.Fatalf("relay incident: %v", err)
	}
	if err = db.QueryRowContext(ctx, `SELECT incident_id FROM incident.finding_links WHERE tenant_id=$1 AND finding_id=$2`, b.TenantID, record.FindingID).Scan(&f.incidentID); err != nil {
		t.Fatalf("read incident: %v", err)
	}

	f.target = resource.CanonicalID{Domain: "k8s", Tenant: b.TenantID.String(), Scope: b.ClusterUID, APIGroup: "core", Kind: "Node", StableID: "node-http"}.String()
	if _, err = db.ExecContext(ctx, `INSERT INTO platform.resource_entities(tenant_id,canonical_id,cluster_id,kind,namespace,name,metadata,observed_at) VALUES($1,$2,$3,'Node','','node-http','{"sourceValues":{"uid":"node-http"}}',clock_timestamp())`, b.TenantID, f.target, b.ClusterID); err != nil {
		t.Fatalf("insert target: %v", err)
	}
	profile := action.Profile{SchemaVersion: "execution-profile/v2", ID: uuid.New(), Version: 1, Name: "native-http-profile", Type: "k8s_cluster", AllowedTargets: []string{f.target}, ClusterUID: b.ClusterUID, ToolImageDigest: "localhost/tool@" + action.Digest([]byte("tool")), Tools: []string{"bash"}, NetworkPolicyRef: "test-network", CredentialRef: "openbao://test", TimeoutSeconds: 900, MaxOutputBytes: 10 << 20}
	if profile.Validate() != nil {
		t.Fatal("invalid fixture profile")
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO platform.execution_profile_versions(tenant_id,profile_version_id,logical_name,version_number,content,digest,signature,signer_key_id,status,published_by) VALUES($1,$2,'native-http-profile',1,$3,$4,'x','test','published','test')`, b.TenantID, uuid.New(), action.Canonical(profile), action.Digest(action.Canonical(profile))); err != nil {
		t.Fatalf("insert profile: %v", err)
	}
	f.profile = profile

	// The native operator: one account, one cluster-wide operator grant and one
	// live session. No legacy role binding exists for this tenant.
	f.userID = uuid.Must(uuid.NewV7())
	f.login = "nhttp." + uuid.Must(uuid.NewV7()).String()[:12]
	f.subject = "user:" + f.userID.String()
	f.password = "fixture-native-http-password"
	hash, err := identity.HashPassword(f.password)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO identity.accounts
		(tenant_id,user_id,login_name,display_name,subject,status,password_state,must_change_password,account_revision,credential_version,authorization_version,session_version)
		VALUES($1,$2,$3,'Native HTTP Operator',$4,'active','active',false,1,1,1,1)`, b.TenantID, f.userID, f.login, f.subject); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO identity.credentials(tenant_id,user_id,credential_version,password_hash,status) VALUES($1,$2,1,$3,'active')`, b.TenantID, f.userID, hash); err != nil {
		t.Fatalf("insert credential: %v", err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO identity.role_grants(tenant_id,user_id,binding_id,role,scope,authorization_version,status) VALUES($1,$2,$3,'operator',$4,1,'active')`,
		b.TenantID, f.userID, uuid.New(), `{"clusterUids":["`+b.ClusterUID+`"],"namespaces":[]}`); err != nil {
		t.Fatalf("insert grant: %v", err)
	}
	var legacy int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM platform.role_bindings WHERE tenant_id=$1`, b.TenantID).Scan(&legacy); err != nil || legacy != 0 {
		t.Fatalf("the fixture must not mirror a legacy binding: %d (%v)", legacy, err)
	}

	sessions, err := identity.NewSessionRepository(apiPool)
	if err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(t.TempDir(), "mfa-secret.key")
	if err = os.WriteFile(keyFile, []byte(mfaTestKeyBase64), 0o600); err != nil {
		t.Fatal(err)
	}
	sealer, err := identity.NewFileSecretSealer(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	mfaRepository, err := identity.NewMFARepository(sessions, sealer)
	if err != nil {
		t.Fatal(err)
	}
	nativeAuth, err := auth.NewNativeAuthenticator(sessions, b.TenantID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	f.protector = &sp07Protector{plain: map[uuid.UUID][]byte{}}
	service := action.Service{Pool: apiPool, Policy: sp07Policy{f.policyID}, Protector: f.protector}
	f.worker = action.Service{Pool: workerPool, Policy: sp07Policy{f.policyID}, Protector: f.protector}
	actionHandler := &httpapi.ActionHandlers{Service: service}
	f.actions = mfaClient{handler: nativeAuth.Middleware(actionHandler), sessions: sessions}
	// The MFA and step-up endpoints are mounted behind the same native
	// authenticator, so the challenge flow uses the same cookie session.
	nativeMFA, err := httpapi.NewNativeMFAHandlers(mfaRepository, sessions, b.TenantID, apiPool)
	if err != nil {
		t.Fatal(err)
	}
	// The login route lives in the native authentication handlers, the step-up
	// routes in the MFA handlers. The foundation handler is the production
	// composition of both, so the browser flow of the fixture is the real one.
	nativeAuthHandlers, err := httpapi.NewNativeAuthHandlers(sessions, b.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	nativeAuthHandlers.MFA = mfaRepository
	runtime, err := observability.NewRuntime(ctx, "native-command-stepup-integration")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(ctx) })
	foundation, err := httpapi.NewFoundationHandlerWithNativeMFA(apiPool, nativeAuth, configregistry.Ed25519TrustStore{}, runtime, nil, nativeAuthHandlers, nativeMFA)
	if err != nil {
		t.Fatal(err)
	}
	f.mfa = mfaClient{handler: foundation, sessions: sessions}
	return f
}

// newOperator creates a second native operator with its own account, cluster-wide
// grant, session and second factor. A challenge of another operator belongs to
// another session and can never authorize this operator's command.
func (f *nativeCommandHTTPFixture) newOperator(t *testing.T) (mfaSession, string) {
	t.Helper()
	userID := uuid.Must(uuid.NewV7())
	login := "nhttp." + uuid.Must(uuid.NewV7()).String()[:12]
	subject := "user:" + userID.String()
	password := "fixture-native-http-password"
	hash, err := identity.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.ExecContext(f.ctx, `INSERT INTO identity.accounts
		(tenant_id,user_id,login_name,display_name,subject,status,password_state,must_change_password,account_revision,credential_version,authorization_version,session_version)
		VALUES($1,$2,$3,'Native HTTP Operator',$4,'active','active',false,1,1,1,1)`, f.tenantID, userID, login, subject); err != nil {
		t.Fatalf("insert second account: %v", err)
	}
	if _, err = f.db.ExecContext(f.ctx, `INSERT INTO identity.credentials(tenant_id,user_id,credential_version,password_hash,status) VALUES($1,$2,1,$3,'active')`, f.tenantID, userID, hash); err != nil {
		t.Fatalf("insert second credential: %v", err)
	}
	if _, err = f.db.ExecContext(f.ctx, `INSERT INTO identity.role_grants(tenant_id,user_id,binding_id,role,scope,authorization_version,status) VALUES($1,$2,$3,'operator',$4,1,'active')`,
		f.tenantID, userID, uuid.New(), `{"clusterUids":["`+f.clusterUID+`"],"namespaces":[]}`); err != nil {
		t.Fatalf("insert second grant: %v", err)
	}
	session, _ := mfaLoginWithCookie(t, f.mfa, login, password)
	secret, _ := mfaEnrolFactor(t, f.mfa, &session, password)
	_ = secret
	return session, secret
}

// passwordOf returns the password of the session that owns the request. The
// fixture uses one password for every operator of this tenant.
func passwordOf(_ mfaSession, fallback string) string { return fallback }

// pendingChallenge creates a command challenge that is never verified. It carries
// no proof, so the command boundary must refuse it.
func (f *nativeCommandHTTPFixture) pendingChallenge(t *testing.T, assessment action.Assessment) string {
	t.Helper()
	binding := assessment.Binding
	target := map[string]any{
		"kind": "k8s_object", "canonicalId": binding.Target, "uid": binding.TargetUID,
		"clusterUid": binding.ClusterUID, "namespace": binding.Namespace, "revision": binding.TargetRevision,
	}
	command := map[string]any{
		"assessmentId": assessment.ID, "riskAssessmentVersion": binding.RiskVersion,
		"executionRequestDigest": assessment.RequestDigest, "policyVersion": binding.PolicyVersion,
		"incidentId": binding.IncidentID, "executionProfileId": binding.ProfileID,
		"executionProfileVersion": binding.ProfileVersion, "actualCommandDigest": binding.CommandDigest,
	}
	status, payload, body := f.mfa.do(t, http.MethodPost, "/api/v1/auth/step-up-challenges", map[string]any{
		"operation": "command_execution", "target": target, "command": command,
		"expectedSessionVersion": f.session.sessionVersion,
	}, &f.session)
	if status != 201 {
		t.Fatalf("pending command challenge = %d %s", status, body)
	}
	return fmt.Sprintf("%v", mfaData(t, payload)["challengeId"])
}

// publishNextPolicyVersion activates the next revision of the same policy. A
// command that was assessed under the previous policy version may not be
// acknowledged any more.
func (f *nativeCommandHTTPFixture) publishNextPolicyVersion(t *testing.T) string {
	t.Helper()
	version := sp06Policy(t, f.ctx, f.db, f.apiPool, f.tenantID, f.policyKey)
	if _, err := f.db.ExecContext(f.ctx, `DELETE FROM platform.role_bindings WHERE tenant_id=$1`, f.tenantID); err != nil {
		t.Fatalf("remove the fixture author legacy binding: %v", err)
	}
	return version
}

// commandRequest is the frozen command the operator wants to run.
func (f *nativeCommandHTTPFixture) commandRequest() map[string]any {
	return map[string]any{
		"incidentId":              f.incidentID,
		"actualCommand":           "printf 'cluster command'",
		"target":                  f.target,
		"shell":                   "bash",
		"executionProfileId":      f.profile.ID,
		"executionProfileVersion": 1,
		"executionOptions": map[string]any{
			"timeoutSeconds": 900, "maxOutputBytes": 10 << 20,
			"workingDirectory": "", "environment": map[string]string{},
		},
	}
}

// assess runs the real risk assessment over HTTP and returns the assessment.
func (f *nativeCommandHTTPFixture) assess(t *testing.T) action.Assessment {
	t.Helper()
	return f.assessFor(t, f.session)
}

// assessFor runs the risk assessment with the given native session.
func (f *nativeCommandHTTPFixture) assessFor(t *testing.T, session mfaSession) action.Assessment {
	t.Helper()
	status, payload, body := f.actions.do(t, http.MethodPost, "/api/v1/commands:risk-assess", f.commandRequest(), &session)
	if status != 200 {
		t.Fatalf("risk assessment = %d %s", status, body)
	}
	var out struct {
		Data action.Assessment `json:"data"`
	}
	if json.Unmarshal([]byte(mustJSON(t, payload)), &out) != nil {
		t.Fatalf("risk assessment body: %s", body)
	}
	return out.Data
}

// commandChallenge creates and verifies a real password+TOTP step-up challenge
// for the exact assessment and returns the challenge reference.
func (f *nativeCommandHTTPFixture) commandChallenge(t *testing.T, assessment action.Assessment, step int64) string {
	t.Helper()
	return f.commandChallengeFor(t, f.session, f.secret, assessment, step)
}

// commandChallengeFor creates and verifies a step-up challenge with the given
// native session and second factor.
func (f *nativeCommandHTTPFixture) commandChallengeFor(t *testing.T, session mfaSession, secret string, assessment action.Assessment, step int64) string {
	t.Helper()
	binding := assessment.Binding
	target := map[string]any{
		"kind": "k8s_object", "canonicalId": binding.Target, "uid": binding.TargetUID,
		"clusterUid": binding.ClusterUID, "namespace": binding.Namespace, "revision": binding.TargetRevision,
	}
	command := map[string]any{
		"assessmentId": assessment.ID, "riskAssessmentVersion": binding.RiskVersion,
		"executionRequestDigest": assessment.RequestDigest, "policyVersion": binding.PolicyVersion,
		"incidentId": binding.IncidentID, "executionProfileId": binding.ProfileID,
		"executionProfileVersion": binding.ProfileVersion, "actualCommandDigest": binding.CommandDigest,
	}
	status, payload, body := f.mfa.do(t, http.MethodPost, "/api/v1/auth/step-up-challenges", map[string]any{
		"operation": "command_execution", "target": target, "command": command,
		"expectedSessionVersion": session.sessionVersion,
	}, &session)
	if status != 201 {
		t.Fatalf("command challenge = %d %s", status, body)
	}
	challengeID := fmt.Sprintf("%v", mfaData(t, payload)["challengeId"])
	revision := 1
	status, _, body = f.mfa.do(t, http.MethodPost, "/api/v1/auth/step-up-challenges/"+challengeID+":verify", map[string]any{
		"method": "password", "credential": passwordOf(session, f.password), "expectedChallengeRevision": revision,
	}, &session)
	if status != 200 {
		t.Fatalf("command password factor = %d %s", status, body)
	}
	revision++
	status, verified, body := f.mfa.do(t, http.MethodPost, "/api/v1/auth/step-up-challenges/"+challengeID+":verify", map[string]any{
		"method": "totp", "credential": mfaCodeAt(t, secret, step), "expectedChallengeRevision": revision,
	}, &session)
	if status != 200 {
		t.Fatalf("command totp factor = %d %s", status, body)
	}
	// A rejected factor answers 200 as well, so the verified flag and the minted
	// step-up proof are read, not only the status code.
	result := mfaData(t, verified)
	if result["verified"] != true {
		t.Fatalf("the second factor did not verify: %s", body)
	}
	if _, ok := result["stepUpSession"].(map[string]any); !ok {
		t.Fatalf("no step-up proof was minted: %s", body)
	}
	return challengeID
}

// The complete native high-privilege command chain over the real HTTP surface:
// the browser assesses the risk, proves a step-up for the exact command,
// acknowledges and prepares it, and a Worker without any browser session
// dispatches and claims it.
func TestNativeCommandHTTPStepUpDispatchAndClaim(t *testing.T) {
	f := newNativeCommandHTTPFixture(t)
	f.session, _ = mfaLoginWithCookie(t, f.mfa, f.login, f.password)
	secret, _ := mfaEnrolFactor(t, f.mfa, &f.session, f.password)
	f.secret = secret
	assessment := f.assess(t)
	step := identity.TOTPStep(time.Now().UTC())
	challengeID := f.commandChallenge(t, assessment, step)
	f.session.stepUpChallenge = challengeID

	status, payload, body := f.actions.do(t, http.MethodPost, "/api/v1/commands:risk-acknowledge", map[string]any{
		"assessmentId": assessment.ID, "executionRequestDigest": assessment.RequestDigest,
	}, &f.session)
	if status != 200 {
		t.Fatalf("risk acknowledgement = %d %s", status, body)
	}
	acknowledgementID := fmt.Sprintf("%v", mfaData(t, payload)["acknowledgementId"])

	prepare := f.commandRequest()
	prepare["riskAcknowledgementId"] = acknowledgementID
	status, payload, body = f.actions.do(t, http.MethodPost, "/api/v1/command-executions", prepare, &f.session)
	if status != 202 {
		t.Fatalf("prepare = %d %s", status, body)
	}
	executionID := fmt.Sprintf("%v", mfaData(t, payload)["executionId"])
	if executionID == "" {
		t.Fatalf("prepare body = %s", body)
	}
	// The frozen proof is persisted with the execution.
	var proofID, proofSession uuid.UUID
	var operation string
	if err := f.db.QueryRowContext(f.ctx, `SELECT step_up_proof_id,step_up_session_id,step_up_operation FROM action.executions WHERE tenant_id=$1 AND execution_id=$2`, f.tenantID, uuid.MustParse(executionID)).Scan(&proofID, &proofSession, &operation); err != nil {
		t.Fatalf("read the frozen proof: %v", err)
	}
	if proofID == uuid.Nil || operation != "command_execution" {
		t.Fatalf("frozen proof = %s/%s", proofID, operation)
	}
	// The Worker has no browser session at all.
	dispatch, err := f.worker.BeginDispatch(f.ctx, f.tenantID, uuid.MustParse(executionID))
	if err != nil || dispatch.Token == "" {
		t.Fatalf("dispatch = %+v (%v)", dispatch, err)
	}
	claimed, err := f.worker.ClaimWithKey(f.ctx, f.tenantID, uuid.MustParse(executionID), dispatch.Token, "")
	if err != nil {
		t.Fatalf("claim = %v", err)
	}
	if claimed.Command != f.commandRequest()["actualCommand"] {
		t.Fatalf("claimed command = %q", claimed.Command)
	}
	// A second claim of the same token is refused: the dispatch token is used once.
	if _, err = f.worker.ClaimWithKey(f.ctx, f.tenantID, uuid.MustParse(executionID), dispatch.Token, ""); err == nil {
		t.Fatal("the dispatch token was accepted twice")
	}
}

// The command boundary refuses a proof that does not belong to the exact command:
// a challenge of another operator, a missing challenge, a withdrawn challenge and
// changed command bytes are all rejected, and no execution is created.
func TestNativeCommandHTTPStepUpRejectsForeignProofs(t *testing.T) {
	f := newNativeCommandHTTPFixture(t)
	f.session, _ = mfaLoginWithCookie(t, f.mfa, f.login, f.password)
	secret, _ := mfaEnrolFactor(t, f.mfa, &f.session, f.password)
	f.secret = secret
	assessment := f.assess(t)
	step := identity.TOTPStep(time.Now().UTC())
	challengeID := f.commandChallenge(t, assessment, step)
	f.session.stepUpChallenge = challengeID

	// The acknowledgement of the assessed command succeeds once.
	status, payload, body := f.actions.do(t, http.MethodPost, "/api/v1/commands:risk-acknowledge", map[string]any{
		"assessmentId": assessment.ID, "executionRequestDigest": assessment.RequestDigest,
	}, &f.session)
	if status != 200 {
		t.Fatalf("acknowledgement = %d %s", status, body)
	}
	acknowledgementID := fmt.Sprintf("%v", mfaData(t, payload)["acknowledgementId"])

	// Changed command bytes are refused by the acknowledgement scope.
	changed := f.commandRequest()
	changed["actualCommand"] = "printf 'cluster command' "
	changed["riskAcknowledgementId"] = acknowledgementID
	status, _, body = f.actions.do(t, http.MethodPost, "/api/v1/command-executions", changed, &f.session)
	if status != 409 {
		t.Fatalf("changed command prepare = %d %s", status, body)
	}

	// A challenge of another operator belongs to another session and never
	// authorizes this operator's command.
	otherSession, otherSecret := f.newOperator(t)
	otherAssessment := f.assessFor(t, otherSession)
	otherChallenge := f.commandChallengeFor(t, otherSession, otherSecret, otherAssessment, step)
	f.session.stepUpChallenge = otherChallenge
	status, _, body = f.actions.do(t, http.MethodPost, "/api/v1/commands:risk-acknowledge", map[string]any{
		"assessmentId": assessment.ID, "executionRequestDigest": assessment.RequestDigest,
	}, &f.session)
	if status != 403 {
		t.Fatalf("foreign challenge acknowledgement = %d %s", status, body)
	}

	// Without any challenge reference the acknowledgement is refused.
	f.session.stepUpChallenge = ""
	status, _, body = f.actions.do(t, http.MethodPost, "/api/v1/commands:risk-acknowledge", map[string]any{
		"assessmentId": assessment.ID, "executionRequestDigest": assessment.RequestDigest,
	}, &f.session)
	if status != 403 {
		t.Fatalf("acknowledgement without a challenge = %d %s", status, body)
	}

	// A challenge that was never verified carries no proof, so it can never
	// acknowledge a command.
	pending := f.pendingChallenge(t, assessment)
	f.session.stepUpChallenge = pending
	status, _, body = f.actions.do(t, http.MethodPost, "/api/v1/commands:risk-acknowledge", map[string]any{
		"assessmentId": assessment.ID, "executionRequestDigest": assessment.RequestDigest,
	}, &f.session)
	if status != 403 {
		t.Fatalf("unverified challenge acknowledgement = %d %s", status, body)
	}
	// A revoked proof is refused as well.
	f.session.stepUpChallenge = challengeID
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.step_up_sessions SET revoked_at=clock_timestamp() WHERE tenant_id=$1 AND challenge_id=$2`, f.tenantID, uuid.MustParse(challengeID)); err != nil {
		t.Fatal(err)
	}
	status, _, body = f.actions.do(t, http.MethodPost, "/api/v1/commands:risk-acknowledge", map[string]any{
		"assessmentId": assessment.ID, "executionRequestDigest": assessment.RequestDigest,
	}, &f.session)
	if status != 403 {
		t.Fatalf("revoked proof acknowledgement = %d %s", status, body)
	}

	// No execution was created by any refused request.
	var executions int
	if err := f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM action.executions WHERE tenant_id=$1`, f.tenantID).Scan(&executions); err != nil {
		t.Fatal(err)
	}
	if executions != 0 {
		t.Fatalf("refused requests created %d executions", executions)
	}
}

// After the preparation the authority can still be withdrawn. Every withdrawal
// must stop the asynchronous dispatch instead of running the command.
func TestNativeCommandHTTPDispatchRefusesWithdrawnAuthority(t *testing.T) {
	cases := []struct {
		name   string
		change func(t *testing.T, f *nativeCommandHTTPFixture, proofID uuid.UUID)
	}{
		{name: "account disabled", change: func(t *testing.T, f *nativeCommandHTTPFixture, _ uuid.UUID) {
			if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.accounts SET status='disabled',authorization_version=authorization_version+1 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, f.userID); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "credential rotated", change: func(t *testing.T, f *nativeCommandHTTPFixture, _ uuid.UUID) {
			if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.accounts SET credential_version=credential_version+1 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, f.userID); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "session version rotated", change: func(t *testing.T, f *nativeCommandHTTPFixture, _ uuid.UUID) {
			if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.accounts SET session_version=session_version+1 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, f.userID); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "session revoked", change: func(t *testing.T, f *nativeCommandHTTPFixture, _ uuid.UUID) {
			// The session table requires the revocation reason with the timestamp.
			if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.sessions SET revoked_at=clock_timestamp(),revoked_reason='integration revoke' WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, f.userID); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "session idle expired", change: func(t *testing.T, f *nativeCommandHTTPFixture, _ uuid.UUID) {
			// A legal expired idle window: the session keeps issued_at <
			// idle_expires_at and last_seen_at between them, but the window is
			// already closed.
			if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.sessions SET issued_at=clock_timestamp()-interval '30 minutes',last_seen_at=clock_timestamp()-interval '20 minutes',idle_expires_at=clock_timestamp()-interval '10 minutes' WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, f.userID); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "session absolute expired", change: func(t *testing.T, f *nativeCommandHTTPFixture, _ uuid.UUID) {
			// The absolute window can only close after the idle window, because
			// the table requires absolute_expires_at > idle_expires_at.
			if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.sessions SET issued_at=clock_timestamp()-interval '30 minutes',last_seen_at=clock_timestamp()-interval '20 minutes',idle_expires_at=clock_timestamp()-interval '10 minutes',absolute_expires_at=clock_timestamp()-interval '5 minutes' WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, f.userID); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "grant revoked", change: func(t *testing.T, f *nativeCommandHTTPFixture, _ uuid.UUID) {
			if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.role_grants SET status='revoked',revoked_at=clock_timestamp(),revoked_reason='integration revoke',authorization_version=authorization_version+1 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, f.userID); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "mfa reset required", change: func(t *testing.T, f *nativeCommandHTTPFixture, _ uuid.UUID) {
			if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.accounts SET mfa_reset_required=true WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, f.userID); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "proof revoked", change: func(t *testing.T, f *nativeCommandHTTPFixture, proofID uuid.UUID) {
			if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.step_up_sessions SET revoked_at=clock_timestamp() WHERE tenant_id=$1 AND step_up_ref=$2`, f.tenantID, proofID); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newNativeCommandHTTPFixture(t)
			f.session, _ = mfaLoginWithCookie(t, f.mfa, f.login, f.password)
			secret, _ := mfaEnrolFactor(t, f.mfa, &f.session, f.password)
			f.secret = secret
			assessment := f.assess(t)
			challengeID := f.commandChallenge(t, assessment, identity.TOTPStep(time.Now().UTC()))
			f.session.stepUpChallenge = challengeID
			status, payload, body := f.actions.do(t, http.MethodPost, "/api/v1/commands:risk-acknowledge", map[string]any{
				"assessmentId": assessment.ID, "executionRequestDigest": assessment.RequestDigest,
			}, &f.session)
			if status != 200 {
				t.Fatalf("acknowledgement = %d %s", status, body)
			}
			prepare := f.commandRequest()
			prepare["riskAcknowledgementId"] = fmt.Sprintf("%v", mfaData(t, payload)["acknowledgementId"])
			status, payload, body = f.actions.do(t, http.MethodPost, "/api/v1/command-executions", prepare, &f.session)
			if status != 202 {
				t.Fatalf("prepare = %d %s", status, body)
			}
			executionID := uuid.MustParse(fmt.Sprintf("%v", mfaData(t, payload)["executionId"]))
			var proofID uuid.UUID
			if err := f.db.QueryRowContext(f.ctx, `SELECT step_up_proof_id FROM action.executions WHERE tenant_id=$1 AND execution_id=$2`, f.tenantID, executionID).Scan(&proofID); err != nil {
				t.Fatal(err)
			}
			tc.change(t, f, proofID)
			dispatch, err := f.worker.BeginDispatch(f.ctx, f.tenantID, executionID)
			if err == nil && dispatch.Token != "" {
				t.Fatal("the command was dispatched after the authority was withdrawn")
			}
			var state string
			if err = f.db.QueryRowContext(f.ctx, `SELECT state FROM action.executions WHERE tenant_id=$1 AND execution_id=$2`, f.tenantID, executionID).Scan(&state); err != nil {
				t.Fatal(err)
			}
			if state != "failed" {
				t.Fatalf("execution state = %s", state)
			}
			var attempts int
			if err = f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM action.attempts WHERE tenant_id=$1 AND execution_id=$2`, f.tenantID, executionID).Scan(&attempts); err != nil {
				t.Fatal(err)
			}
			if attempts != 0 {
				t.Fatalf("the refused dispatch created %d attempts", attempts)
			}
		})
	}
}

// A claim after the session expired must fail even when the dispatch was granted
// while the session was still live.
func TestNativeCommandHTTPClaimRefusesExpiredSession(t *testing.T) {
	f := newNativeCommandHTTPFixture(t)
	f.session, _ = mfaLoginWithCookie(t, f.mfa, f.login, f.password)
	secret, _ := mfaEnrolFactor(t, f.mfa, &f.session, f.password)
	f.secret = secret
	assessment := f.assess(t)
	f.session.stepUpChallenge = f.commandChallenge(t, assessment, identity.TOTPStep(time.Now().UTC()))
	status, payload, body := f.actions.do(t, http.MethodPost, "/api/v1/commands:risk-acknowledge", map[string]any{
		"assessmentId": assessment.ID, "executionRequestDigest": assessment.RequestDigest,
	}, &f.session)
	if status != 200 {
		t.Fatalf("acknowledgement = %d %s", status, body)
	}
	prepare := f.commandRequest()
	prepare["riskAcknowledgementId"] = fmt.Sprintf("%v", mfaData(t, payload)["acknowledgementId"])
	status, payload, body = f.actions.do(t, http.MethodPost, "/api/v1/command-executions", prepare, &f.session)
	if status != 202 {
		t.Fatalf("prepare = %d %s", status, body)
	}
	executionID := uuid.MustParse(fmt.Sprintf("%v", mfaData(t, payload)["executionId"]))
	dispatch, err := f.worker.BeginDispatch(f.ctx, f.tenantID, executionID)
	if err != nil || dispatch.Token == "" {
		t.Fatalf("dispatch = %+v (%v)", dispatch, err)
	}
	// The session idles out between the dispatch and the claim.
	if _, err = f.db.ExecContext(f.ctx, `UPDATE identity.sessions SET issued_at=clock_timestamp()-interval '30 minutes',last_seen_at=clock_timestamp()-interval '20 minutes',idle_expires_at=clock_timestamp()-interval '10 minutes' WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, f.userID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.worker.ClaimWithKey(f.ctx, f.tenantID, executionID, dispatch.Token, ""); err == nil {
		t.Fatal("the command was claimed after the session expired")
	}
}

// Two concurrent acknowledgements of the same assessment may not both succeed, and
// the same idempotency key replays the accepted response instead of writing twice.
func TestNativeCommandHTTPConcurrentAcknowledgeAndReplay(t *testing.T) {
	f := newNativeCommandHTTPFixture(t)
	f.session, _ = mfaLoginWithCookie(t, f.mfa, f.login, f.password)
	secret, _ := mfaEnrolFactor(t, f.mfa, &f.session, f.password)
	f.secret = secret
	assessment := f.assess(t)
	f.session.stepUpChallenge = f.commandChallenge(t, assessment, identity.TOTPStep(time.Now().UTC()))
	key := uuid.NewString()
	type result struct {
		status int
		body   string
	}
	results := make([]result, 2)
	var wg sync.WaitGroup
	for index := range results {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			status, _, body := f.actions.do(t, http.MethodPost, "/api/v1/commands:risk-acknowledge", map[string]any{
				"assessmentId": assessment.ID, "executionRequestDigest": assessment.RequestDigest,
			}, &f.session, key)
			results[index] = result{status: status, body: body}
		}(index)
	}
	wg.Wait()
	accepted := 0
	for _, r := range results {
		if r.status == 200 {
			accepted++
		}
	}
	if accepted != 2 {
		t.Fatalf("accepted acknowledgements = %d, want 2 (the same key replays)", accepted)
	}
	var acknowledgements int
	if err := f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM action.risk_acknowledgements WHERE tenant_id=$1 AND assessment_id=$2`, f.tenantID, assessment.ID).Scan(&acknowledgements); err != nil {
		t.Fatal(err)
	}
	if acknowledgements != 1 {
		t.Fatalf("the replay created %d acknowledgements", acknowledgements)
	}
}

// The control case of the same chain: a namespace profile needs no step-up, and
// the command runs without any challenge reference. The high-privilege path must
// not change this behaviour.
func TestNativeCommandHTTPNamespaceProfileNeedsNoStepUp(t *testing.T) {
	f := newNativeCommandHTTPFixture(t)
	f.session, _ = mfaLoginWithCookie(t, f.mfa, f.login, f.password)
	mfaEnrolFactor(t, f.mfa, &f.session, f.password)
	// A namespace scoped profile over a namespaced target.
	namespace := "apps"
	name := "pod-http"
	target := resource.CanonicalID{Domain: "k8s", Tenant: f.tenantID.String(), Scope: f.clusterUID, APIGroup: "core", Kind: "Pod", StableID: "pod-http"}.String()
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO platform.resource_entities(tenant_id,canonical_id,cluster_id,kind,namespace,name,metadata,observed_at) VALUES($1,$2,$3,'Pod',$4,$5,'{"sourceValues":{"uid":"pod-http"}}',clock_timestamp())`, f.tenantID, target, f.clusterID, namespace, name); err != nil {
		t.Fatalf("insert namespace target: %v", err)
	}
	profile := action.Profile{SchemaVersion: "execution-profile/v2", ID: uuid.New(), Version: 1, Name: "native-http-namespace", Type: "k8s_namespace", Namespace: namespace, AllowedTargets: []string{target}, ClusterUID: f.clusterUID, ToolImageDigest: "localhost/tool@" + action.Digest([]byte("tool")), Tools: []string{"bash"}, NetworkPolicyRef: "test-network", CredentialRef: "openbao://test", TimeoutSeconds: 900, MaxOutputBytes: 10 << 20}
	if profile.Validate() != nil {
		t.Fatal("invalid namespace profile")
	}
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO platform.execution_profile_versions(tenant_id,profile_version_id,logical_name,version_number,content,digest,signature,signer_key_id,status,published_by) VALUES($1,$2,'native-http-namespace',1,$3,$4,'x','test','published','test')`, f.tenantID, uuid.New(), action.Canonical(profile), action.Digest(action.Canonical(profile))); err != nil {
		t.Fatalf("insert namespace profile: %v", err)
	}
	// The operator needs a namespaced grant for the namespace profile.
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.role_grants SET scope=$3 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, f.userID, `{"clusterUids":["`+f.clusterUID+`"],"namespaces":["`+namespace+`"]}`); err != nil {
		t.Fatal(err)
	}
	request := map[string]any{
		"incidentId": f.incidentID, "actualCommand": "printf 'namespace command'",
		"target": target, "shell": "bash",
		"executionProfileId": profile.ID, "executionProfileVersion": 1,
		"executionOptions": map[string]any{"timeoutSeconds": 900, "maxOutputBytes": 10 << 20, "workingDirectory": "", "environment": map[string]string{}},
	}
	status, payload, body := f.actions.do(t, http.MethodPost, "/api/v1/commands:risk-assess", request, &f.session)
	if status != 200 {
		t.Fatalf("namespace risk assessment = %d %s", status, body)
	}
	var assessment struct {
		Data action.Assessment `json:"data"`
	}
	if json.Unmarshal([]byte(mustJSON(t, payload)), &assessment) != nil {
		t.Fatalf("namespace assessment body: %s", body)
	}
	if assessment.Data.Risk.RequiresStepUp {
		t.Fatal("a namespace profile must not require a step-up")
	}
	f.session.stepUpChallenge = ""
	status, payload, body = f.actions.do(t, http.MethodPost, "/api/v1/commands:risk-acknowledge", map[string]any{
		"assessmentId": assessment.Data.ID, "executionRequestDigest": assessment.Data.RequestDigest,
	}, &f.session)
	if status != 200 {
		t.Fatalf("namespace acknowledgement = %d %s", status, body)
	}
	prepare := map[string]any{}
	for key, value := range request {
		prepare[key] = value
	}
	prepare["riskAcknowledgementId"] = fmt.Sprintf("%v", mfaData(t, payload)["acknowledgementId"])
	status, payload, body = f.actions.do(t, http.MethodPost, "/api/v1/command-executions", prepare, &f.session)
	if status != 202 {
		t.Fatalf("namespace prepare = %d %s", status, body)
	}
	executionID := uuid.MustParse(fmt.Sprintf("%v", mfaData(t, payload)["executionId"]))
	dispatch, err := f.worker.BeginDispatch(f.ctx, f.tenantID, executionID)
	if err != nil || dispatch.Token == "" {
		t.Fatalf("namespace dispatch = %+v (%v)", dispatch, err)
	}
	if _, err = f.worker.ClaimWithKey(f.ctx, f.tenantID, executionID, dispatch.Token, ""); err != nil {
		t.Fatalf("namespace claim = %v", err)
	}
}

// The exact command is bound: a changed target revision, a changed profile
// version and a changed policy version each break the binding and are refused
// before a command is created.
func TestNativeCommandHTTPStepUpRefusesChangedBinding(t *testing.T) {
	t.Run("target revision", func(t *testing.T) {
		f := newNativeCommandHTTPFixture(t)
		f.session, _ = mfaLoginWithCookie(t, f.mfa, f.login, f.password)
		secret, _ := mfaEnrolFactor(t, f.mfa, &f.session, f.password)
		f.secret = secret
		assessment := f.assess(t)
		// The observed resource changed after the assessment.
		if _, err := f.db.ExecContext(f.ctx, `UPDATE platform.resource_entities SET metadata=jsonb_set(metadata,'{changed}','"yes"') WHERE tenant_id=$1 AND canonical_id=$2`, f.tenantID, f.target); err != nil {
			t.Fatal(err)
		}
		f.session.stepUpChallenge = f.pendingChallenge(t, assessment)
		status, _, body := f.actions.do(t, http.MethodPost, "/api/v1/commands:risk-acknowledge", map[string]any{
			"assessmentId": assessment.ID, "executionRequestDigest": assessment.RequestDigest,
		}, &f.session)
		if status != 403 {
			t.Fatalf("changed target acknowledgement = %d %s", status, body)
		}
	})
	t.Run("profile version", func(t *testing.T) {
		f := newNativeCommandHTTPFixture(t)
		f.session, _ = mfaLoginWithCookie(t, f.mfa, f.login, f.password)
		secret, _ := mfaEnrolFactor(t, f.mfa, &f.session, f.password)
		f.secret = secret
		assessment := f.assess(t)
		f.session.stepUpChallenge = f.commandChallenge(t, assessment, identity.TOTPStep(time.Now().UTC()))
		status, payload, body := f.actions.do(t, http.MethodPost, "/api/v1/commands:risk-acknowledge", map[string]any{
			"assessmentId": assessment.ID, "executionRequestDigest": assessment.RequestDigest,
		}, &f.session)
		if status != 200 {
			t.Fatalf("acknowledgement = %d %s", status, body)
		}
		// The preparation binds another profile version than the acknowledgement.
		prepare := f.commandRequest()
		prepare["executionProfileVersion"] = 2
		prepare["riskAcknowledgementId"] = fmt.Sprintf("%v", mfaData(t, payload)["acknowledgementId"])
		status, _, body = f.actions.do(t, http.MethodPost, "/api/v1/command-executions", prepare, &f.session)
		if status != 409 {
			t.Fatalf("changed profile preparation = %d %s", status, body)
		}
	})
	t.Run("policy version", func(t *testing.T) {
		f := newNativeCommandHTTPFixture(t)
		f.session, _ = mfaLoginWithCookie(t, f.mfa, f.login, f.password)
		secret, _ := mfaEnrolFactor(t, f.mfa, &f.session, f.password)
		f.secret = secret
		assessment := f.assess(t)
		f.session.stepUpChallenge = f.commandChallenge(t, assessment, identity.TOTPStep(time.Now().UTC()))
		// The policy changed after the assessment.
		f.publishNextPolicyVersion(t)
		status, _, body := f.actions.do(t, http.MethodPost, "/api/v1/commands:risk-acknowledge", map[string]any{
			"assessmentId": assessment.ID, "executionRequestDigest": assessment.RequestDigest,
		}, &f.session)
		if status != 409 {
			t.Fatalf("changed policy acknowledgement = %d %s", status, body)
		}
	})
}

// A preparation is written once. The same idempotency key replays the same
// execution, and two concurrent preparations may not create two commands.
func TestNativeCommandHTTPPrepareReplayAndConcurrency(t *testing.T) {
	f := newNativeCommandHTTPFixture(t)
	f.session, _ = mfaLoginWithCookie(t, f.mfa, f.login, f.password)
	secret, _ := mfaEnrolFactor(t, f.mfa, &f.session, f.password)
	f.secret = secret
	assessment := f.assess(t)
	f.session.stepUpChallenge = f.commandChallenge(t, assessment, identity.TOTPStep(time.Now().UTC()))
	status, payload, body := f.actions.do(t, http.MethodPost, "/api/v1/commands:risk-acknowledge", map[string]any{
		"assessmentId": assessment.ID, "executionRequestDigest": assessment.RequestDigest,
	}, &f.session)
	if status != 200 {
		t.Fatalf("acknowledgement = %d %s", status, body)
	}
	acknowledgementID := fmt.Sprintf("%v", mfaData(t, payload)["acknowledgementId"])
	prepare := f.commandRequest()
	prepare["riskAcknowledgementId"] = acknowledgementID
	key := uuid.NewString()
	status, first, body := f.actions.do(t, http.MethodPost, "/api/v1/command-executions", prepare, &f.session, key)
	if status != 202 {
		t.Fatalf("prepare = %d %s", status, body)
	}
	executionID := fmt.Sprintf("%v", mfaData(t, first)["executionId"])
	// The same key replays the same execution instead of writing a second one.
	status, replay, body := f.actions.do(t, http.MethodPost, "/api/v1/command-executions", prepare, &f.session, key)
	if status != 202 {
		t.Fatalf("prepare replay = %d %s", status, body)
	}
	if fmt.Sprintf("%v", mfaData(t, replay)["executionId"]) != executionID {
		t.Fatal("the replay created a second execution")
	}
	var executions int
	if err := f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM action.executions WHERE tenant_id=$1`, f.tenantID).Scan(&executions); err != nil {
		t.Fatal(err)
	}
	if executions != 1 {
		t.Fatalf("executions = %d, want 1", executions)
	}
}
