package integration

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/action"
	"ops-platform/internal/auth"
	"ops-platform/internal/bundle"
	"ops-platform/internal/configregistry"
	"ops-platform/internal/graph"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/identity"
	"ops-platform/internal/investigation"
	"ops-platform/internal/investigation/tools"
	"ops-platform/internal/observability"
)

// The old job/result/evidence are explicit migration fixtures on a disposable
// PostgreSQL database. This is read authorization, not live model/source proof.
func TestNativeMappedIncidentHistoryHTTP(t *testing.T) {
	f := newNativeJobFixture(t)
	seed := f.newIncident(t)
	evidenceID := f.newEvidence(t, seed, false, "kubernetes-event")
	legacy := "legacy-incident-owner"
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO platform.role_bindings(tenant_id,binding_id,subject,role_name,cluster_scopes,namespace_scopes) VALUES($1,$2,$3,'operator',$4,$5)`, f.tenantID, uuid.New(), legacy, `[]`, `[{"clusterId":"`+f.clusterID.String()+`","namespace":"apps"}]`); err != nil {
		t.Fatal(err)
	}
	scope, err := (graph.Authorization{Pool: f.apiPool}).Effective(f.ctx, f.tenantID.String(), legacy, f.clusterUID)
	if err != nil {
		t.Fatal(err)
	}
	defs, err := tools.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	repository := investigation.Repository{Pool: f.apiPool}
	job, err := repository.CreateJob(f.ctx, investigation.CreateInvestigationRequest{TenantID: f.tenantID, IncidentID: seed.IncidentID, Subject: legacy, TriggerRevision: seed.Revision, TriggerKind: "manual", PolicyVersion: f.policy, Scope: scope, ToolCatalogDigest: tools.Digest(defs), Budget: investigation.DefaultBudget()})
	if err != nil {
		t.Fatal(err)
	}
	proposal, _ := json.Marshal(investigation.Proposal{SchemaVersion: "investigation-result/v1", Status: "unresolved", Summary: "controlled historical result", EvidenceRefs: []string{evidenceID.String()}, CandidateUpdates: []investigation.CandidateSuggestion{}, ActionPlans: []json.RawMessage{}, Partial: true, DegradedSources: []string{"fixture"}})
	if _, err := f.db.ExecContext(f.ctx, `UPDATE investigation.jobs SET state='partial',result=$3,event_seq=2 WHERE tenant_id=$1 AND job_id=$2`, f.tenantID, job.JobID, proposal); err != nil {
		t.Fatal(err)
	}
	eventPayload, _ := json.Marshal(map[string]any{"state": "partial", "result": json.RawMessage(proposal)})
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO investigation.events(tenant_id,job_id,event_seq,event_type,payload) VALUES($1,$2,2,'result',$3)`, f.tenantID, job.JobID, eventPayload); err != nil {
		t.Fatal(err)
	}
	stepID := uuid.Must(uuid.NewV7())
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO investigation.steps(tenant_id,job_id,step_id,tool_name,args_digest,state,lease_token,lease_generation,error_code) VALUES($1,$2,$3,'get_recent_changes',$4,'failed',$5,1,'FIXTURE_UNAVAILABLE')`, f.tenantID, job.JobID, stepID, graph.ScopeDigest("fixture"), uuid.New()); err != nil {
		t.Fatal(err)
	}
	// Preserve an old execution and its output through the ordinary service path.
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO platform.resource_entities(tenant_id,canonical_id,cluster_id,kind,namespace,name,metadata,observed_at) VALUES($1,$2,$3,'Pod','apps','api-0','{"sourceValues":{"uid":"api-0"}}',clock_timestamp())`, f.tenantID, seed.Canonical, f.clusterID); err != nil {
		t.Fatal(err)
	}
	profile := action.Profile{SchemaVersion: "execution-profile/v2", ID: uuid.New(), Version: 1, Name: "legacy-history", Type: "k8s_namespace", Namespace: "apps", AllowedTargets: []string{seed.Canonical}, ClusterUID: f.clusterUID, ToolImageDigest: "localhost/tool@" + action.Digest([]byte("tool")), Tools: []string{"bash"}, NetworkPolicyRef: "fixture-network", CredentialRef: "openbao://fixture", TimeoutSeconds: 900, MaxOutputBytes: 10 << 20}
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO platform.execution_profile_versions(tenant_id,profile_version_id,logical_name,version_number,content,digest,signature,signer_key_id,status,published_by) VALUES($1,$2,'legacy-history',1,$3,$4,'x','fixture','published','fixture')`, f.tenantID, uuid.New(), action.Canonical(profile), action.Digest(action.Canonical(profile))); err != nil {
		t.Fatal(err)
	}
	service := action.Service{Pool: f.apiPool, Policy: sp07Policy{f.policy}, Protector: &sp07Protector{plain: map[uuid.UUID][]byte{}}}
	actor := auth.RequestContext{TenantID: f.tenantID, Subject: legacy, Roles: []auth.Role{auth.Operator}, TokenExpiresAt: time.Now().Add(time.Hour)}
	command := action.CommandRequest{IncidentID: seed.IncidentID, ActualCommand: "printf historical", Target: seed.Canonical, Shell: "bash", ProfileID: profile.ID, ProfileVersion: 1, Options: action.Options{TimeoutSeconds: 900, MaxOutputBytes: 10 << 20, Environment: map[string]string{}}}
	assessment, err := service.AssessRisk(f.ctx, actor, command, "history-assess")
	if err != nil {
		t.Fatal(err)
	}
	ack, err := service.AcknowledgeRisk(f.ctx, actor, assessment.ID, assessment.RequestDigest, "history-ack")
	if err != nil {
		t.Fatal(err)
	}
	execution, err := service.PrepareCommandExecution(f.ctx, actor, command, ack.ID, "history-execution")
	if err != nil {
		t.Fatal(err)
	}
	worker := service
	worker.Pool = f.workerPool
	dispatch, err := worker.BeginDispatch(f.ctx, f.tenantID, execution.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worker.Claim(f.ctx, f.tenantID, execution.ID, dispatch.Token); err != nil {
		t.Fatal(err)
	}
	if err := worker.AppendOutput(f.ctx, f.tenantID, execution.ID, dispatch.Token, action.OutputChunk{ExecutionID: execution.ID, Seq: 1, Stream: "stdout", Bytes: []byte("controlled historical output"), ObservedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := worker.Finish(f.ctx, f.tenantID, execution.ID, dispatch.Token, 0, 1, false); err != nil {
		t.Fatal(err)
	}
	frozenExecution, err := service.Get(f.ctx, actor, execution.ID)
	if err != nil {
		t.Fatal(err)
	}
	account := f.newAccount(t, `"apps"`)
	inventory := identity.SubjectInventory{SchemaVersion: "native-subject-inventory/v1", TenantID: f.tenantID, ReviewedBy: "controlled-review", Entries: []identity.SubjectInventoryEntry{{LegacySubject: legacy, UserID: account.userID}}}
	document, _ := json.Marshal(inventory)
	canonical, err := bundle.CanonicalizeJSON(document)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(canonical)
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO identity.subject_mapping_inventories(tenant_id,evidence_digest,document,imported_by) VALUES($1,$2,$3,'controlled-duty')`, f.tenantID, digest[:], canonical); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO identity.subject_mappings(tenant_id,mapping_id,legacy_subject,user_id,basis,evidence_digest,inventory_digest) VALUES($1,$2,$3,$4,'verified_inventory',$5,$5)`, f.tenantID, uuid.New(), legacy, account.userID, digest[:]); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.Events(account.ctx, f.tenantID, job.JobID, account.subject, 0); err != nil {
		grant, grantErr := (graph.Authorization{Pool: f.apiPool}).Effective(account.ctx, f.tenantID.String(), account.subject, f.clusterUID)
		t.Fatalf("mapped repository read: %v frozen=%+v grant=%+v grantErr=%v", err, job.Scope, grant, grantErr)
	}
	sessions, err := identity.NewSessionRepository(f.apiPool)
	if err != nil {
		t.Fatal(err)
	}
	login, err := httpapi.NewNativeAuthHandlers(sessions, f.tenantID)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := auth.NewNativeAuthenticator(sessions, f.tenantID, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := observability.NewRuntime(f.ctx, "historical-read-test")
	if err != nil {
		t.Fatal(err)
	}
	history := &httpapi.InvestigationHandlers{Repository: repository, CursorKey: key}
	foundation, err := httpapi.NewFoundationHandlerWithNativeAuth(f.apiPool, authenticator, configregistry.Ed25519TrustStore{}, runtime, &httpapi.SP04Handlers{Pool: f.apiPool, SigningKey: key, Investigation: history, Action: &httpapi.ActionHandlers{Service: service}}, login)
	if err != nil {
		t.Fatal(err)
	}
	client := mfaClient{handler: foundation, sessions: sessions}
	var loginName string
	if err := f.db.QueryRowContext(f.ctx, `SELECT login_name FROM identity.accounts WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, account.userID).Scan(&loginName); err != nil {
		t.Fatal(err)
	}
	mapped, _ := mfaLoginWithCookie(t, client, loginName, "fixture-operational-password")
	path := "/api/v1/investigations/" + job.JobID.String()
	endpoints := []string{"/api/v1/command-executions/" + execution.ID.String(), "/api/v1/command-executions/" + execution.ID.String() + "/post-check", "/api/v1/command-executions/" + execution.ID.String() + "/events", path, path + "/steps", path + "/events", "/api/v1/incidents/" + seed.IncidentID.String() + "/investigations", "/api/v1/evidence/" + evidenceID.String() + "?clusterUid=" + f.clusterUID}
	before, err := (investigation.Repository{Pool: f.workerPool}).Get(f.ctx, f.tenantID, job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range endpoints {
		request := httptest.NewRequest(http.MethodGet, endpoint, nil)
		request.AddCookie(mapped.cookie)
		response := httptest.NewRecorder()
		ctx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
		foundation.ServeHTTP(response, request.WithContext(ctx))
		cancel()
		if response.Code != 200 {
			t.Fatalf("mapped %s = %d %s", endpoint, response.Code, response.Body.String())
		}
		if strings.Contains(endpoint, "command-executions") && strings.HasSuffix(endpoint, "/events") && !strings.Contains(response.Body.String(), base64.StdEncoding.EncodeToString([]byte("controlled historical output"))) {
			t.Fatal("historical output not retained in authorized SSE")
		}

	}
	if status, _, _ := client.do(t, http.MethodPost, path+":cancel", map[string]any{}, &mapped); status != 403 {
		t.Fatalf("mapped history cancel = %d", status)
	}
	if status, _, _ := client.do(t, http.MethodGet, "/api/v1/audit-records", nil, &mapped); status != 403 {
		t.Fatalf("operator inherited audit role: %d", status)
	}
	var reads int
	if err := f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM audit.records WHERE tenant_id=$1 AND event_type='investigation.history.mapped_owner.read' AND subject=$2`, f.tenantID, account.subject).Scan(&reads); err != nil || reads == 0 {
		t.Fatalf("missing mapped read audit: %d %v", reads, err)
	}
	// A separate administrator grant enables audit reads, but cannot replace operator authority.
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO identity.role_grants(tenant_id,user_id,binding_id,role,scope,authorization_version,status) VALUES($1,$2,$3,'platform_admin','{}',1,'active')`, f.tenantID, account.userID, uuid.New()); err != nil {
		t.Fatal(err)
	}
	mapped, _ = mfaLoginWithCookie(t, client, loginName, "fixture-operational-password")
	if status, payload, body := client.do(t, http.MethodGet, "/api/v1/audit-records?subject="+legacy, nil, &mapped); status != 200 || len(payload["data"].([]any)) == 0 {
		t.Fatalf("old-subject audit read = %d %s", status, body)
	}
	// Revoke after the first persisted event; the second result must not be disclosed.
	streamRequest := httptest.NewRequest(http.MethodGet, path+"/events", nil)
	streamRequest.AddCookie(mapped.cookie)
	streamReply := &sp07WithdrawWriter{ResponseRecorder: httptest.NewRecorder(), onFirst: func() {
		if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.role_grants SET status='revoked',revoked_at=clock_timestamp(),revoked_reason='stream withdrawal' WHERE tenant_id=$1 AND user_id=$2 AND role='operator'`, f.tenantID, account.userID); err != nil {
			t.Fatal(err)
		}
	}}
	foundation.ServeHTTP(streamReply, streamRequest)
	if streamReply.events != 1 || strings.Contains(streamReply.Body.String(), "controlled historical result") {
		t.Fatal("SSE disclosed cached history after revocation")
	}
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.role_grants SET status='active',revoked_at=NULL,revoked_reason=NULL WHERE tenant_id=$1 AND user_id=$2 AND role='operator'`, f.tenantID, account.userID); err != nil {
		t.Fatal(err)
	}
	// Same name plus the same scope is insufficient without the explicit mapping.
	other := f.newAccount(t, `"apps"`)
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.accounts SET login_name=$3 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, other.userID, legacy); err != nil {
		t.Fatal(err)
	}
	unmapped, _ := mfaLoginWithCookie(t, client, legacy, "fixture-operational-password")
	if status, _, _ := client.do(t, http.MethodGet, path, nil, &unmapped); status != 403 {
		t.Fatalf("unmapped same-name read = %d", status)
	}
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.role_grants SET scope=$3 WHERE tenant_id=$1 AND user_id=$2 AND role='operator'`, f.tenantID, account.userID, `{"clusterUids":["`+f.clusterUID+`"],"namespaces":["outside"]}`); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range endpoints {
		if status, _, _ := client.do(t, http.MethodGet, endpoint, nil, &mapped); status != 403 {
			t.Fatalf("out-of-scope %s = %d", endpoint, status)
		}
	}
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.role_grants SET status='revoked',revoked_at=clock_timestamp(),revoked_reason='controlled test' WHERE tenant_id=$1 AND user_id=$2 AND role='operator'`, f.tenantID, account.userID); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range endpoints {
		if status, _, _ := client.do(t, http.MethodGet, endpoint, nil, &mapped); status != 403 {
			t.Fatalf("admin-only %s = %d", endpoint, status)
		}
	}
	after, err := (investigation.Repository{Pool: f.workerPool}).Get(f.ctx, f.tenantID, job.JobID)
	if err != nil {
		t.Fatal(err)
	}
	afterExecution, err := service.Get(f.ctx, actor, execution.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(frozenExecution, afterExecution) {
		t.Fatal("historical reads mutated execution")
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("mapped reads/cancel mutated frozen historical job")
	}
	// Restore only this disposable fixture grant to isolate source qualification.
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.role_grants SET status='active',revoked_at=NULL,revoked_reason=NULL,scope=$3 WHERE tenant_id=$1 AND user_id=$2 AND role='operator'`, f.tenantID, account.userID, `{"clusterUids":["`+f.clusterUID+`"],"namespaces":["apps"]}`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.Events(account.ctx, f.tenantID, job.JobID, account.subject, 0); err != nil {
		t.Fatalf("restored independent scope: %v", err)
	}
	// Current source qualification is checked again when dereferencing old Evidence.
	if _, err := f.db.ExecContext(f.ctx, `UPDATE platform.source_registrations SET status='disabled' WHERE tenant_id=$1 AND source_id=$2`, f.tenantID, seed.SourceID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.Events(account.ctx, f.tenantID, job.JobID, account.subject, 0); err == nil {
		t.Fatal("disabled source still served historical evidence")
	}
}
