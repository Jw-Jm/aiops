package integration

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http"
	"net/http/httptest"
	"ops-platform/internal/action"
	"ops-platform/internal/auth"
	"ops-platform/internal/contract"
	platformcrypto "ops-platform/internal/crypto"
	"ops-platform/internal/finding"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/incident"
	"ops-platform/internal/policy"
	"strings"
	"sync"
	"testing"
	"time"
)

// Fault doubles are ONLY used to isolate transaction behavior. They are not
// evidence of Transit, OPA, Keycloak, SSH or Kubernetes runtime acceptance.
type sp07Protector struct {
	mu    sync.Mutex
	plain map[uuid.UUID][]byte
}

func (p *sp07Protector) Seal(_ context.Context, t, id uuid.UUID, b []byte) (platformcrypto.Envelope, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.plain[id] = append([]byte(nil), b...)
	return platformcrypto.Envelope{Version: "test-only", TenantID: t, ObjectID: id, PlaintextDigest: action.Digest(b), Ciphertext: "test-only-ciphertext"}, nil
}
func (p *sp07Protector) Open(_ context.Context, t, id uuid.UUID, e platformcrypto.Envelope) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]byte(nil), p.plain[id]...), nil
}

type sp07Policy struct{ version string }

type sp07WithdrawWriter struct {
	*httptest.ResponseRecorder
	onFirst  func()
	events   int
	deadline time.Time
}

func (w *sp07WithdrawWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(b)
	if strings.Contains(string(b), "data: ") {
		w.events++
		if w.events == 1 {
			w.onFirst()
		}
	}
	return n, err
}
func (w *sp07WithdrawWriter) SetWriteDeadline(d time.Time) error { w.deadline = d; return nil }
func (w *sp07WithdrawWriter) WriteString(s string) (int, error)  { return w.Write([]byte(s)) }

func (p sp07Policy) Evaluate(_ context.Context, in policy.PolicyInput) (policy.PolicyDecision, error) {
	return policy.PolicyDecision{Allow: in.RiskAcknowledged && (!in.ClusterLevel || in.StepUpVerified), Risk: policy.RiskHigh, PolicyVersion: p.version, DecisionID: action.Digest([]byte("test-policy"))}, nil
}

func TestSP07CommandTransactionAndClaim(t *testing.T) {
	ctx, db, worker, b := sp05Database(t)
	var dsn string
	// The fixture supplies a least-privilege API login independently of Worker.
	dsn = worker.Config().ConnString()
	apiConfig := runtimePoolConfig(t, ctx, db, dsn, "api_runtime_role")
	pool, err := pgxpool.NewWithConfig(ctx, apiConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	fs := finding.Service{Pool: worker}
	f, _, err := fs.Ingest(ctx, b, sp05Envelope(b, "sp07-event", "sp07-occurrence"))
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
	actor := auth.RequestContext{TenantID: b.TenantID, Subject: "sp07-operator", Roles: []auth.Role{auth.Operator}, ClusterScopes: []uuid.UUID{b.ClusterID}, TokenExpiresAt: time.Now().Add(time.Hour)}
	if _, err = db.ExecContext(ctx, `INSERT INTO platform.role_bindings(tenant_id,binding_id,subject,role_name,cluster_scopes,namespace_scopes) VALUES($1,$2,$3,'operator',$4,$5)`, b.TenantID, uuid.New(), actor.Subject, `["`+b.ClusterID.String()+`"]`, `[{"clusterId":"`+b.ClusterID.String()+`","namespace":"test-ns"}]`); err != nil {
		t.Fatal(err)
	}
	policyVersion := sp06Policy(t, ctx, db, worker, b.TenantID)
	target := sp05Envelope(b, "unused", "unused").ResourceCanonicalID
	if _, err = db.ExecContext(ctx, `INSERT INTO platform.resource_entities(tenant_id,canonical_id,cluster_id,kind,namespace,name,metadata,observed_at) VALUES($1,$2,$3,'Node','test-ns','node-a','{"sourceValues":{"uid":"node-a"}}',clock_timestamp())`, b.TenantID, target, b.ClusterID); err != nil {
		t.Fatal(err)
	}
	profile := action.Profile{SchemaVersion: "execution-profile/v2", ID: uuid.New(), Version: 1, Name: "test-profile", Type: "k8s_namespace", Namespace: "test-ns", AllowedTargets: []string{target}, ClusterUID: b.ClusterUID, ToolImageDigest: "localhost/tool@sha256:" + string(make([]byte, 0)), Tools: []string{"bash"}, NetworkPolicyRef: "test-network", CredentialRef: "openbao://test", TimeoutSeconds: 900, MaxOutputBytes: 10 << 20, Principal: "", HostOnboardingRef: ""}
	profile.ToolImageDigest = "localhost/tool@" + action.Digest([]byte("tool"))
	if profile.Validate() != nil {
		t.Fatal("invalid fixture profile")
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO platform.execution_profile_versions(tenant_id,profile_version_id,logical_name,version_number,content,digest,signature,signer_key_id,status,published_by) VALUES($1,$2,'test-profile',1,$3,$4,'x','test','published','test')`, b.TenantID, uuid.New(), action.Canonical(profile), action.Digest(action.Canonical(profile))); err != nil {
		t.Fatal(err)
	}
	service := action.Service{Pool: pool, Policy: sp07Policy{policyVersion}, Protector: &sp07Protector{plain: map[uuid.UUID][]byte{}}}
	req := action.CommandRequest{IncidentID: incidentID, ActualCommand: "printf 'operator command'", Target: target, Shell: "bash", ProfileID: profile.ID, ProfileVersion: 1, Options: action.Options{TimeoutSeconds: 900, MaxOutputBytes: 10 << 20, Environment: map[string]string{}}}
	assessment, err := service.AssessRisk(ctx, actor, req, "risk-1")
	if err != nil {
		t.Fatal("assess", err)
	}
	if _, err = service.AcknowledgeRisk(ctx, actor, assessment.ID, action.Digest([]byte("wrong")), "bad-ack"); !errors.Is(err, action.ErrAcknowledgement) {
		t.Fatalf("digest substitution %v", err)
	}
	ack, err := service.AcknowledgeRisk(ctx, actor, assessment.ID, assessment.RequestDigest, "ack-1")
	if err != nil {
		t.Fatal("ack", err)
	}
	otherAssessment, err := service.AssessRisk(ctx, actor, req, "same-binding-new-assessment")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.AcknowledgeRisk(ctx, actor, otherAssessment.ID, otherAssessment.RequestDigest, "ack-1"); err == nil {
		t.Fatal("changed assessment reused acknowledgement idempotency key")
	}
	changed := req
	changed.ActualCommand += " "
	if _, err = service.PrepareCommandExecution(ctx, actor, changed, ack.ID, "bad-command"); !errors.Is(err, action.ErrAcknowledgement) {
		t.Fatalf("changed bytes %v", err)
	}
	e, err := service.PrepareCommandExecution(ctx, actor, req, ack.ID, "execute-1")
	if err != nil {
		t.Fatal("prepare", err)
	}
	if err := contract.Validate("https://ops.local/schemas/command-execution/v1", action.Canonical(e.Public())); err != nil {
		t.Fatal("public execution contract", err)
	}
	replay, err := service.PrepareCommandExecution(ctx, actor, req, ack.ID, "execute-1")
	if err != nil || replay.ID != e.ID {
		t.Fatalf("replay %v", err)
	}
	if _, err = service.PrepareCommandExecution(ctx, actor, req, ack.ID, "execute-2"); !errors.Is(err, action.ErrAcknowledgement) {
		t.Fatalf("ack reuse %v", err)
	}
	var raw string
	if err = db.QueryRowContext(ctx, `SELECT command_envelope::text FROM action.executions WHERE execution_id=$1`, e.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if json.Unmarshal([]byte(raw), &envelope) != nil || envelope["ciphertext"] != "test-only-ciphertext" {
		t.Fatal("envelope absent")
	}
	workerService := service
	workerService.Pool = worker
	d, err := workerService.BeginDispatch(ctx, b.TenantID, e.ID)
	if err != nil {
		t.Fatal("dispatch", err)
	}
	var wg sync.WaitGroup
	success := make(chan bool, 2)
	for range 2 {
		wg.Go(func() {
			c, err := service.Claim(ctx, b.TenantID, e.ID, d.Token)
			success <- err == nil && c.Command == req.ActualCommand
		})
	}
	wg.Wait()
	close(success)
	n := 0
	for ok := range success {
		if ok {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("claims=%d", n)
	}
	if _, err = service.BeginDispatch(ctx, b.TenantID, e.ID); !errors.Is(err, action.ErrConflict) {
		t.Fatal("dispatch repeated", err)
	}
	chunk := action.OutputChunk{ExecutionID: e.ID, Seq: 1, Stream: "stdout", Bytes: []byte("one")}
	if err = service.AppendOutput(ctx, b.TenantID, e.ID, d.Token, chunk); err != nil {
		t.Fatal("output", err)
	}
	if err = service.AppendOutput(ctx, b.TenantID, e.ID, d.Token, chunk); err != nil {
		t.Fatal("duplicate", err)
	}
	chunk.Bytes = []byte("tamper")
	if err = service.AppendOutput(ctx, b.TenantID, e.ID, d.Token, chunk); !errors.Is(err, action.ErrConflict) {
		t.Fatal("conflicting chunk", err)
	}
	if err = service.Finish(ctx, b.TenantID, e.ID, d.Token, 0, 1, false); err != nil {
		t.Fatal("finish", err)
	}
	got, err := service.Get(ctx, actor, e.ID)
	if err != nil || got.State != "succeeded" || got.PostCheck != "inconclusive" {
		t.Fatalf("exit success implies remediation %+v %v", got, err)
	}
	streamCtx, stopStream := context.WithTimeout(ctx, 2*time.Second)
	writer := &sp07WithdrawWriter{ResponseRecorder: httptest.NewRecorder(), onFirst: func() {
		if _, err := db.ExecContext(ctx, `UPDATE platform.role_bindings SET status='disabled' WHERE tenant_id=$1 AND subject=$2`, actor.TenantID, actor.Subject); err != nil {
			t.Fatal(err)
		}
	}}
	streamRequest := httptest.NewRequest(http.MethodGet, "/api/v1/command-executions/"+e.ID.String()+"/events", nil).WithContext(auth.WithRequestContext(streamCtx, actor))
	(&httpapi.ActionHandlers{Service: service}).ServeHTTP(writer, streamRequest)
	stopStream()
	if writer.events != 1 {
		t.Errorf("SSE disclosed %d events after first-event withdrawal; want one", writer.events)
	}
	if writer.deadline.IsZero() || writer.deadline.After(actor.TokenExpiresAt) {
		t.Error("SSE write was not bounded by verified token expiry")
	}
	if _, err := db.ExecContext(ctx, `UPDATE platform.role_bindings SET status='active' WHERE tenant_id=$1 AND subject=$2`, actor.TenantID, actor.Subject); err != nil {
		t.Fatal(err)
	}
	// Retention clock fault: old terminal data remains until archive verification.
	if _, err = db.ExecContext(ctx, `UPDATE action.executions SET completed_at=clock_timestamp()-interval '25 hours',archived_at=clock_timestamp()-interval '25 hours' WHERE execution_id=$1`, e.ID); err != nil {
		t.Fatal(err)
	}
	cleanup := action.OutputArchive{Service: workerService}
	if err = cleanup.Cleanup(ctx, b.TenantID); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Events(ctx, actor, e.ID, 0); err != nil {
		t.Fatal("unverified archive deleted output", err)
	}
	// This marker simulates a verified archive only to test cleanup eligibility;
	// native ciphertext/digest verification is separately exercised in deployment.
	if _, err = db.ExecContext(ctx, `INSERT INTO action.output_archives(tenant_id,execution_id,retain_until,plaintext_digest,object_ref,verified_at) VALUES($1,$2,clock_timestamp()+interval '365 days',$3,'{"retention-fixture":true}',clock_timestamp())`, b.TenantID, e.ID, action.Digest([]byte("fixture"))); err != nil {
		t.Fatal(err)
	}
	if err = cleanup.Cleanup(ctx, b.TenantID); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Events(ctx, actor, e.ID, 0); !errors.Is(err, action.ErrCursorExpired) {
		t.Fatalf("expired output cursor: %v", err)
	}
	expiredCursor := httptest.NewRecorder()
	(&httpapi.ActionHandlers{Service: service}).ServeHTTP(expiredCursor, httptest.NewRequest(http.MethodGet, "/api/v1/command-executions/"+e.ID.String()+"/events", nil).WithContext(auth.WithRequestContext(ctx, actor)))
	if expiredCursor.Code != 410 {
		t.Fatalf("formal expired output cursor status=%d", expiredCursor.Code)
	}

	// Two competing requests consume one acknowledgement only once.
	prepareAck := func(label string) action.Acknowledgement {
		t.Helper()
		assessment, err := service.AssessRisk(ctx, actor, req, "risk-"+label)
		if err != nil {
			t.Fatal(err)
		}
		ack, err := service.AcknowledgeRisk(ctx, actor, assessment.ID, assessment.RequestDigest, "ack-"+label)
		if err != nil {
			t.Fatal(err)
		}
		return ack
	}
	legacy := req
	legacy.IncidentID = uuid.Nil
	legacy.ProfileVersion = 0
	legacy.Options = action.Options{}
	legacy.OmitIncident = true
	legacy.OmitVersion = true
	legacy.OmitOptions = true
	if replay, err := service.PrepareCommandExecution(ctx, actor, legacy, ack.ID, "execute-1"); err != nil || replay.ID != e.ID {
		t.Fatalf("legacy confirmed replay %v", err)
	}
	otherAck := prepareAck("idempotency-conflict")
	if _, err := service.PrepareCommandExecution(ctx, actor, req, otherAck.ID, "execute-1"); err == nil {
		t.Fatal("changed acknowledgement reused idempotency key")
	}
	raceAck := prepareAck("competing")
	executions := make(chan action.Execution, 2)
	for _, key := range []string{"competing-a", "competing-b"} {
		wg.Go(func() {
			e, err := service.PrepareCommandExecution(ctx, actor, req, raceAck.ID, key)
			if err == nil {
				executions <- e
			}
		})
	}
	wg.Wait()
	close(executions)
	var uncertain action.Execution
	winners := 0
	for e := range executions {
		uncertain = e
		winners++
	}
	if winners != 1 {
		t.Fatalf("ack consumption winners=%d", winners)
	}
	expired := prepareAck("expired")
	if _, err := db.ExecContext(ctx, `UPDATE action.risk_acknowledgements SET confirmed_at=clock_timestamp()-interval '6 minutes',expires_at=clock_timestamp()-interval '1 second' WHERE acknowledgement_id=$1`, expired.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.PrepareCommandExecution(ctx, actor, req, expired.ID, "expired-execution"); !errors.Is(err, action.ErrAcknowledgement) {
		t.Fatalf("expired acknowledgement: %v", err)
	}
	dispatch, err := workerService.BeginDispatch(ctx, b.TenantID, uncertain.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Claim(ctx, b.TenantID, uncertain.ID, dispatch.Token); err != nil {
		t.Fatal(err)
	}
	partial := action.OutputChunk{ExecutionID: uncertain.ID, Seq: 1, Stream: "stderr", Bytes: []byte("partial before lost callback")}
	if err = service.AppendOutput(ctx, b.TenantID, uncertain.ID, dispatch.Token, partial); err != nil {
		t.Fatal(err)
	}
	if err = service.MarkUnknown(ctx, b.TenantID, uncertain.ID, "callback_lost"); err != nil {
		t.Fatal(err)
	}
	partial.Seq = 2
	if err = service.AppendOutput(ctx, b.TenantID, uncertain.ID, dispatch.Token, partial); !errors.Is(err, action.ErrConflict) {
		t.Fatalf("unknown output not frozen: %v", err)
	}
	if err = service.Finish(ctx, b.TenantID, uncertain.ID, dispatch.Token, 0, 1, false); err != nil {
		t.Fatal(err)
	}
	var beforeEvents, afterEvents int64
	if err = db.QueryRowContext(ctx, `SELECT event_seq FROM action.executions WHERE execution_id=$1`, uncertain.ID).Scan(&beforeEvents); err != nil {
		t.Fatal(err)
	}
	if err = service.Finish(ctx, b.TenantID, uncertain.ID, dispatch.Token, 0, 1, false); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT event_seq FROM action.executions WHERE execution_id=$1`, uncertain.ID).Scan(&afterEvents); err != nil || beforeEvents != afterEvents {
		t.Fatalf("unknown completion replay changed events: %v", err)
	}
	result, err := service.Get(ctx, actor, uncertain.ID)
	if err != nil || result.State != "execution_unknown" || result.CompletedAt == nil || result.OutputBytes != int64(len(partial.Bytes)) {
		t.Fatalf("late callback changed uncertainty: %+v %v", result, err)
	}
	if _, err = service.BeginDispatch(ctx, b.TenantID, uncertain.ID); !errors.Is(err, action.ErrConflict) {
		t.Fatalf("uncertain command redispatched: %v", err)
	}
	// Database-time faults exercise the real high-privilege transaction guards
	// through both least-privilege roles; they do not emulate Keycloak acceptance.
	highProfile := profile
	highProfile.ID, highProfile.Name, highProfile.Type, highProfile.Namespace = uuid.New(), "test-cluster", "k8s_cluster", ""
	if _, err = db.ExecContext(ctx, `INSERT INTO platform.execution_profile_versions(tenant_id,profile_version_id,logical_name,version_number,content,digest,signature,signer_key_id,status,published_by) VALUES($1,$2,'test-cluster',1,$3,$4,'x','test','published','test')`, b.TenantID, uuid.New(), action.Canonical(highProfile), action.Digest(action.Canonical(highProfile))); err != nil {
		t.Fatal(err)
	}
	actor.KeycloakSID, actor.ACR, actor.AuthTime = "owned-session", auth.StepUpACRLevel2, time.Now().UTC().Truncate(time.Second)
	highRequest := req
	highRequest.ProfileID = highProfile.ID
	highAssessment, err := service.AssessRisk(ctx, actor, highRequest, "high-risk")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.AcknowledgeRisk(ctx, actor, highAssessment.ID, highAssessment.RequestDigest, "high-no-session"); !errors.Is(err, auth.ErrStepUpInvalid) {
		t.Fatalf("missing high session: %v", err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO platform.step_up_sessions(tenant_id,session_id,subject,keycloak_sid,acr,auth_time,created_at,last_used_at) VALUES($1,$2,$3,$4,$5,$6,$6,$6)`, actor.TenantID, uuid.New(), actor.Subject, actor.KeycloakSID, actor.ACR, actor.AuthTime); err != nil {
		t.Fatal(err)
	}
	for _, fault := range []string{"absolute", "idle", "revoked"} {
		statement := `UPDATE platform.step_up_sessions SET created_at=clock_timestamp()-interval '61 minutes' WHERE tenant_id=$1 AND subject=$2`
		if fault == "idle" {
			statement = `UPDATE platform.step_up_sessions SET last_used_at=clock_timestamp()-interval '61 minutes' WHERE tenant_id=$1 AND subject=$2`
		}
		if fault == "revoked" {
			statement = `UPDATE platform.step_up_sessions SET revoked_at=clock_timestamp() WHERE tenant_id=$1 AND subject=$2`
		}
		if _, err = db.ExecContext(ctx, statement, actor.TenantID, actor.Subject); err != nil {
			t.Fatal(err)
		}
		if _, err = service.AcknowledgeRisk(ctx, actor, highAssessment.ID, highAssessment.RequestDigest, "high-"+fault); !errors.Is(err, auth.ErrStepUpInvalid) {
			t.Fatalf("%s: %v", fault, err)
		}
		if _, err = db.ExecContext(ctx, `UPDATE platform.step_up_sessions SET created_at=$3,last_used_at=$3,revoked_at=NULL WHERE tenant_id=$1 AND subject=$2`, actor.TenantID, actor.Subject, actor.AuthTime); err != nil {
			t.Fatal(err)
		}
	}
	highAck, err := service.AcknowledgeRisk(ctx, actor, highAssessment.ID, highAssessment.RequestDigest, "high-ack")
	if err != nil {
		t.Fatal(err)
	}
	highExecution, err := service.PrepareCommandExecution(ctx, actor, highRequest, highAck.ID, "high-prepare")
	if err != nil {
		t.Fatal(err)
	}
	highDispatch, err := workerService.BeginDispatch(ctx, b.TenantID, highExecution.ID)
	if err != nil {
		t.Fatal("high worker dispatch/touch", err)
	}
	if _, err = service.Claim(ctx, b.TenantID, highExecution.ID, highDispatch.Token); err != nil {
		t.Fatal("high claim/touch", err)
	}
	if err = service.MarkUnknown(ctx, b.TenantID, highExecution.ID, "owned_callback_fault"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE platform.role_bindings SET status='disabled' WHERE tenant_id=$1 AND subject=$2`, actor.TenantID, actor.Subject); err != nil {
		t.Fatal(err)
	}
	if _, err = service.PrepareCommandExecution(ctx, actor, req, ack.ID, "execute-1"); err == nil {
		t.Fatal("revoked replay succeeded")
	}
}
