package integration

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/analytics"
	"ops-platform/internal/assistant"
	"ops-platform/internal/auth"
	"ops-platform/internal/datascope"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/identity"
	"ops-platform/internal/investigation"
	"ops-platform/internal/rca"
	"ops-platform/internal/resource"
)

// Persisted query/RCA/plan contents are controlled old-history fixtures. The
// mapped ownership, native cookie, authorization and HTTP readers are production.
func TestNativeMappedHistoricalQueryAndFormalReferences(t *testing.T) {
	f := newMappedHistoryFixture(t)
	incidentID, planID, kubeID, logsID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	target := resource.CanonicalID{Domain: "k8s", Tenant: f.tenantID.String(), Scope: f.clusterUID, APIGroup: "core", Kind: "Pod", StableID: "history-ref-pod"}.String()
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO platform.resource_entities(tenant_id,canonical_id,cluster_id,kind,namespace,name,metadata,observed_at) VALUES($1,$2,$3,'Pod','apps','history-ref-pod','{}',clock_timestamp())`, f.tenantID, target, f.clusterID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO incident.records(tenant_id,incident_id,state,cluster_uid,namespace,current_rca_revision,resource_canonical_id) VALUES($1,$2,'open',$3,'apps',2,$4)`, f.tenantID, incidentID, f.clusterUID, target); err != nil {
		t.Fatal(err)
	}
	mapping := datascope.Mapping{Scopes: map[string][]string{"cluster": {f.clusterUID}, "namespace": {"apps"}}}
	mappingRaw, _ := json.Marshal(mapping)
	for _, source := range []struct {
		id   uuid.UUID
		kind string
	}{{kubeID, "kubernetes"}, {logsID, "victorialogs"}} {
		if _, err := f.db.ExecContext(f.ctx, `INSERT INTO platform.source_registrations(tenant_id,source_id,cluster_id,source_type,instance_key,auth_ref,allowed_schemas,backend_logical_id,data_scope_mapping) VALUES($1,$2,$3,$4,$5,'openbao://controlled/history',ARRAY['finding-envelope/v2'],$5,$6)`, f.tenantID, source.id, f.clusterID, source.kind, source.id.String(), mappingRaw); err != nil {
			t.Fatal(err)
		}
	}
	kubeBinding := evidence.Binding{Revision: 1, SourceType: "kubernetes", BackendLogicalID: kubeID.String(), ScopeMapping: mapping}
	logsBinding := evidence.Binding{Revision: 1, SourceType: "victorialogs", BackendLogicalID: logsID.String(), ScopeMapping: mapping}
	for version := int64(1); version <= 2; version++ {
		revision := rca.Revision{SchemaVersion: "rca-revision/v1", TenantID: f.tenantID.String(), IncidentID: incidentID.String(), Revision: version, Source: "manual", Actor: f.legacy, CreatedAt: time.Now().UTC(), InputManifest: rca.Input{GraphSources: []graph.SourceAuthority{{SourceRegistrationID: kubeID.String(), Revision: 1, ScopeDigest: evidence.BindingScopeDigest(kubeBinding)}}}}
		raw, _ := json.Marshal(revision)
		if _, err := f.db.ExecContext(f.ctx, `INSERT INTO incident.rca_revisions(tenant_id,incident_id,revision,evaluation_key,input_digest,base_incident_revision,recipe_digest,actor,source,provenance,input_manifest,result,superseded) VALUES($1,$2,$3,$4,$5,1,$5,$6,'manual','{}','{}',$7,false)`, f.tenantID, incidentID, version, uuid.NewString(), graph.ScopeDigest("controlled"), f.legacy, raw); err != nil {
			t.Fatal(err)
		}
	}
	planRaw, _ := json.Marshal(map[string]any{"rcaRevision": 1, "targetCanonicalId": target, "reason": "controlled historical plan", "suggestedCommand": "must-not-be-disclosed", "executionProfileId": "must-not-be-disclosed"})
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO action.plans(tenant_id,action_plan_id,incident_id,content,state) VALUES($1,$2,$3,$4,'suggested')`, f.tenantID, planID, incidentID, planRaw); err != nil {
		t.Fatal(err)
	}
	c, err := f.repo.Create(f.ctx, assistant.Conversation{TenantID: f.tenantID, OwnerSubject: f.legacy, Title: "controlled historical references", Mode: "troubleshoot", AuthorizationDomain: "operational", ClusterUID: f.clusterUID, Binding: assistant.ConversationBinding{Timezone: "UTC", IncidentID: incidentID, Selection: analytics.Selection{Namespaces: []string{"apps"}}}})
	if err != nil {
		t.Fatal(err)
	}
	f.convID = c.ConversationID
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO investigation.assistant_worker_capabilities(tenant_id,worker_id,model_profile_version,catalog_version,expires_at) VALUES($1,'history-ref-fixture','hist-profile/v1','assistant-tools/v2',clock_timestamp()+interval '10 minutes')`, f.tenantID); err != nil {
		t.Fatal(err)
	}
	turn, err := f.repo.Submit(f.ctx, f.tenantID, c.ConversationID, f.legacy, "history-refs", assistant.SubmitMessage{Message: "controlled historical reference request", ExpectedConversationRevision: c.Revision})
	if err != nil {
		t.Fatal(err)
	}
	f.turnID, f.jobID = turn.TurnID, turn.JobID
	job, err := f.repo.Jobs.Get(f.ctx, f.tenantID, turn.JobID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	stored := investigation.BuildStoredQueryResult(analytics.QueryResult{SchemaVersion: "query-result/v1", DatasetID: "logs.records", PlanDigest: graph.ScopeDigest("controlled-log-plan"), CatalogDigest: job.Assistant.DatasetCatalogDigest, ScopeDigest: graph.ScopeDigest(job.Scope), Status: "complete", Exact: true, Rows: []analytics.Row{{Value: 3}}, Unit: "logs", SnapshotReadAt: now, Freshness: "fresh", Warnings: []string{}, DegradedSources: []string{}, TimeRange: &analytics.QueryTimeRange{From: now.Add(-time.Minute), To: now, Timezone: "UTC"}, SourceManifest: &analytics.SourceManifest{SourceID: logsID.String(), Revision: 1, BindingDigest: evidence.BindingScopeDigest(logsBinding), QualificationDigest: graph.ScopeDigest("controlled qualification"), QueryHash: graph.ScopeDigest("controlled source query"), TemplateVersion: "resource-logs/v1", CoverageFrom: now.Add(-time.Minute), CoverageTo: now}})
	rawResult, _ := json.Marshal(stored)
	rawScope, _ := json.Marshal(job.Scope)
	rawManifest, _ := json.Marshal(stored.Result.SourceManifest)
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO investigation.assistant_query_results(tenant_id,query_result_id,turn_id,owner_subject,plan_digest,result,scope,source_manifest) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, f.tenantID, stored.QueryResultID, turn.TurnID, f.legacy, stored.Result.PlanDigest, rawResult, rawScope, rawManifest); err != nil {
		t.Fatal(err)
	}
	var rcaRaw []byte
	if err := f.db.QueryRowContext(f.ctx, `SELECT result FROM incident.rca_revisions WHERE tenant_id=$1 AND incident_id=$2 AND revision=1`, f.tenantID, incidentID).Scan(&rcaRaw); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRowContext(f.ctx, `SELECT content FROM action.plans WHERE tenant_id=$1 AND action_plan_id=$2`, f.tenantID, planID).Scan(&planRaw); err != nil {
		t.Fatal(err)
	}
	refs := []investigation.FormalReference{{Kind: "rca", IncidentID: incidentID, Revision: 1, ContentDigest: graph.ScopeDigest(json.RawMessage(rcaRaw))}, {Kind: "action_plan", IncidentID: incidentID, Revision: 1, ActionPlanID: &planID, ContentDigest: graph.ScopeDigest(json.RawMessage(planRaw))}}
	answer, _ := json.Marshal(investigation.AssistantAnswer{SchemaVersion: "assistant-answer/v1", AnswerKind: "data", Summary: "controlled old references only", QueryResultRefs: []uuid.UUID{stored.QueryResultID}, FormalRefs: refs, FactRefs: []investigation.FactRef{}, EvidenceRefs: []string{}, EntityRefs: []string{}, MissingEvidence: []string{}, Hypotheses: []investigation.Hypothesis{}, NextChecks: []string{}, AsOf: now})
	if _, err := f.db.ExecContext(f.ctx, `UPDATE investigation.jobs SET state='succeeded',result=$3 WHERE tenant_id=$1 AND job_id=$2`, f.tenantID, turn.JobID, answer); err != nil {
		t.Fatal(err)
	}
	frozen := f.frozenJob(t)
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
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/auth/login", login.Login)
	mux.HandleFunc("/api/v1/auth/session", login.Session)
	mux.Handle("/api/v1/assistant/", &httpapi.AssistantHandlers{Repository: f.repo, CursorKey: key})
	client := mfaClient{handler: authenticator.Middleware(mux), sessions: sessions}
	var loginName string
	if err := f.db.QueryRowContext(f.ctx, `SELECT login_name FROM identity.accounts WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, f.userID).Scan(&loginName); err != nil {
		t.Fatal(err)
	}
	mapped, _ := mfaLoginWithCookie(t, client, loginName, "fixture-historical-password")
	queryPath := "/api/v1/assistant/query-results/" + stored.QueryResultID.String()
	formalPath := "/api/v1/assistant/turns/" + turn.TurnID.String() + "/formal-references?index="
	endpoints := []string{queryPath, formalPath + "0", formalPath + "1"}
	for _, endpoint := range endpoints {
		status, _, body := client.do(t, http.MethodGet, endpoint, nil, &mapped)
		if status != 200 || strings.Contains(body, "must-not-be-disclosed") {
			t.Fatalf("mapped reference read %s=%d %s", endpoint, status, body)
		}
	}
	if got, err := f.repo.QueryResult(f.nativeCtx, f.tenantID, stored.QueryResultID, f.subject); err != nil || got.ContentDigest != stored.ContentDigest || got.Facts[0].Value != 3 {
		t.Fatalf("frozen query content changed: %v", err)
	}
	for _, namespace := range []string{"outside", "apps"} {
		scope := `{"clusterUids":["` + f.clusterUID + `"],"namespaces":["` + namespace + `"]}`
		if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.role_grants SET scope=$3 WHERE tenant_id=$1 AND user_id=$2 AND role='operator'`, f.tenantID, f.userID, scope); err != nil {
			t.Fatal(err)
		}
		for _, endpoint := range endpoints {
			status, _, _ := client.do(t, http.MethodGet, endpoint, nil, &mapped)
			if namespace == "outside" && status != 403 && status != 404 || namespace == "apps" && status != 200 {
				t.Fatalf("scope %s reference %s=%d", namespace, endpoint, status)
			}
		}
	}
	// A changed/disabled source invalidates its frozen provenance, not its stored bytes.
	for _, change := range []string{"revision=2", "status='disabled'"} {
		if _, err := f.db.ExecContext(f.ctx, `UPDATE platform.source_registrations SET `+change+` WHERE tenant_id=$1 AND source_id=$2`, f.tenantID, logsID); err != nil {
			t.Fatal(err)
		}
		if status, _, _ := client.do(t, http.MethodGet, queryPath, nil, &mapped); status != 403 {
			t.Fatalf("invalid log source reference=%d", status)
		}
		if _, err := f.db.ExecContext(f.ctx, `UPDATE platform.source_registrations SET revision=1,status='active' WHERE tenant_id=$1 AND source_id=$2`, f.tenantID, logsID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.db.ExecContext(f.ctx, `UPDATE platform.source_registrations SET status='disabled' WHERE tenant_id=$1 AND source_id=$2`, f.tenantID, kubeID); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := client.do(t, http.MethodGet, formalPath+"0", nil, &mapped); status != 403 {
		t.Fatalf("invalid RCA source read=%d", status)
	}
	if _, err := f.db.ExecContext(f.ctx, `UPDATE platform.source_registrations SET status='active' WHERE tenant_id=$1 AND source_id=$2`, f.tenantID, kubeID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(f.ctx, `UPDATE action.plans SET revision=2 WHERE tenant_id=$1 AND action_plan_id=$2`, f.tenantID, planID); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := client.do(t, http.MethodGet, formalPath+"1", nil, &mapped); status != 403 {
		t.Fatalf("changed plan substituted latest=%d", status)
	}
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.role_grants SET status='revoked',revoked_at=clock_timestamp(),revoked_reason='reference scope test' WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, f.userID); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range endpoints {
		if status, _, _ := client.do(t, http.MethodGet, endpoint, nil, &mapped); status != 403 {
			t.Fatalf("revoked historical ref %s=%d", endpoint, status)
		}
	}
	if !reflect.DeepEqual(frozen, f.frozenJob(t)) {
		t.Fatal("reference reads changed frozen owner/scope/input")
	}
}
