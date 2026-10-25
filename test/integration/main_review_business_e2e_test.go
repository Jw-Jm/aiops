package integration

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/analytics"
	"ops-platform/internal/app"
	"ops-platform/internal/archive"
	"ops-platform/internal/assistant"
	"ops-platform/internal/auth"
	protect "ops-platform/internal/crypto"
	"ops-platform/internal/datascope"
	"ops-platform/internal/evidence"
	"ops-platform/internal/finding"
	"ops-platform/internal/graph"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/incident"
	kube "ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/investigation"
	"ops-platform/internal/resource"
	"ops-platform/internal/resourcestore"
)

// Real production inspection, archive protocol, Finding transport and Incident
// recovery against PostgreSQL; Node samples and Transit/S3 transport are controlled.
func TestMainReviewNodeUnknownIncidentRecovery(t *testing.T) {
	ctx, db, pool, b := sp05Database(t)
	store, err := archive.NewStore(&sp04FaultBackend{objects: map[string]archive.StoredObject{}}, 128<<10)
	if err != nil {
		t.Fatal(err)
	}
	protector, err := protect.NewTransitProtector(sp04FaultTransit{}, "evidence-archive")
	if err != nil {
		t.Fatal(err)
	}
	archives := &evidence.ArchiveService{Pool: pool, Store: store, Protector: protector, BackendLogicalID: "main-review-archive"}
	cluster := app.SP04Cluster{Tenant: b.TenantID.String(), ClusterUID: b.ClusterUID, SourceID: b.SourceID.String(), SourceRevision: 1, BackendLogicalID: "sp05-native"}
	repository := resourcestore.Repository{Pool: pool, ExpectedRevision: 1, BackendLogicalID: cluster.BackendLogicalID}
	previous := map[string]string{}
	observed := time.Now().UTC().Add(-time.Minute)
	apply := func(status string, version int) {
		t.Helper()
		at := observed.Add(time.Duration(version) * time.Second)
		object := unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Node", "metadata": map[string]any{"uid": "node-unknown", "name": "node-unknown", "resourceVersion": strconv.Itoa(version), "creationTimestamp": at.Add(-time.Hour).Format(time.RFC3339), "annotations": map[string]any{"ops.internal/observed-at": at.Format(time.RFC3339Nano)}}, "status": map[string]any{"conditions": []any{map[string]any{"type": "Ready", "status": status, "lastTransitionTime": at.Format(time.RFC3339Nano)}}}}}
		snapshot := kube.Snapshot{GVR: kube.GVR{Version: "v1", Resource: "nodes", Kind: "Node"}, Objects: []unstructured.Unstructured{object}, State: kube.GVRState{LastListCompletedAt: at, LastConnectivityProbeAt: at, WatchConnected: true, WatchContinuous: true}}
		var err error
		previous, err = repository.SyncSnapshot(ctx, cluster.Tenant, cluster.SourceID, cluster.ClusterUID, "Node", "core", snapshot, snapshot, previous)
		if err != nil {
			t.Fatal(err)
		}
		if err := app.InspectSP05Snapshot(ctx, cluster, snapshot, archives); err != nil {
			t.Fatal(err)
		}
		if err := (finding.Service{Pool: pool}).RelayPass(ctx, b.TenantID, incident.Consume, 50); err != nil {
			t.Fatal(err)
		}
	}
	apply("Unknown", 1)
	var incidentID, state string
	if err := db.QueryRowContext(ctx, `SELECT i.incident_id,i.state FROM incident.records i JOIN incident.finding_links l USING(tenant_id,incident_id) JOIN finding.records f USING(tenant_id,finding_id) WHERE i.tenant_id=$1 AND f.payload->>'normalizedSymptom'='NodeReadyUnknown'`, b.TenantID).Scan(&incidentID, &state); err != nil || state != "open" {
		t.Fatalf("Unknown did not open Incident: %s %v", state, err)
	}
	apply("True", 2)
	if err := db.QueryRowContext(ctx, `SELECT lifecycle_state FROM finding.records WHERE tenant_id=$1 AND payload->>'normalizedSymptom'='NodeReadyUnknown'`, b.TenantID).Scan(&state); err != nil || state != "resolved" {
		t.Fatalf("Unknown Finding not resolved: %s %v", state, err)
	}
	service := incident.Service{Pool: pool}
	if err := service.RecoveryPass(ctx, b.TenantID, func(string) bool { return true }); err != nil {
		t.Fatal(err)
	}
	readState := func() string {
		t.Helper()
		var s string
		if err := db.QueryRowContext(ctx, `SELECT state FROM incident.records WHERE tenant_id=$1 AND incident_id=$2`, b.TenantID, incidentID).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	if readState() == "resolved" {
		t.Fatal("settle window bypassed")
	}
	// Advance only the persisted settle clock, never rewrite the observed Finding.
	if _, err := db.ExecContext(ctx, `UPDATE incident.records SET recovery_known_at=clock_timestamp()-interval '301 seconds' WHERE tenant_id=$1 AND incident_id=$2`, b.TenantID, incidentID); err != nil {
		t.Fatal(err)
	}
	if err := service.RecoveryPass(ctx, b.TenantID, func(string) bool { return false }); err != nil {
		t.Fatal(err)
	}
	if readState() == "resolved" {
		t.Fatal("unavailable graph counted healthy")
	}
	if err := service.RecoveryPass(ctx, b.TenantID, func(string) bool { return true }); err != nil {
		t.Fatal(err)
	}
	if readState() != "resolved" {
		t.Fatal("healthy Unknown Incident did not settle")
	}
	apply("True", 3)
	if readState() != "resolved" {
		t.Fatal("healthy replay reopened Incident")
	}
}

// MR-018: execute the fixed metric adapter, commit the query Step, evaluate its
// evidence, persist the answer and read the evaluator's Step through the HTTP API.
func TestMainReviewMetricJudgementSourceStepHTTP(t *testing.T) {
	ctx, db, worker, b, repo, jobs := assistantExtendedFixture(t)
	now := time.Now().UTC().Truncate(time.Second)
	target := resource.CanonicalID{Domain: "k8s", Tenant: b.TenantID.String(), Scope: b.ClusterUID, APIGroup: "core", Kind: "Pod", StableID: "metric-pod"}.String()
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.resource_entities(tenant_id,canonical_id,cluster_id,kind,namespace,name,metadata,observed_at) VALUES($1,$2,$3,'Pod','apps','metric-pod','{}',$4)`, b.TenantID, target, b.ClusterID, now); err != nil {
		t.Fatal(err)
	}
	metricServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			t.Errorf("unexpected metric path %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		_ = r.ParseForm()
		json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": []any{map[string]any{"metric": map[string]string{}, "value": []any{now.Unix(), "6"}}}}})
	}))
	defer metricServer.Close()
	binding := evidence.Binding{Tenant: b.TenantID.String(), SourceID: uuid.NewString(), SourceType: "victoriametrics", Revision: 1, BackendLogicalID: "metric-fixture", Endpoint: metricServer.URL, ScopeMapping: datascope.Mapping{RequiredLabels: map[string]string{"tenant": b.TenantID.String()}, Scopes: map[string][]string{"cluster": {b.ClusterUID}, "namespace": {"apps"}}}}
	mapping, _ := json.Marshal(binding.ScopeMapping)
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.source_registrations(tenant_id,source_id,cluster_id,source_type,instance_key,auth_ref,backend_logical_id,data_scope_mapping) VALUES($1,$2,$3,'victoriametrics','metric-fixture','openbao://test/metrics',$4,$5)`, b.TenantID, binding.SourceID, b.ClusterID, binding.BackendLogicalID, mapping); err != nil {
		t.Fatal(err)
	}
	grant, err := (graph.Authorization{Pool: worker}).Effective(ctx, b.TenantID.String(), "assistant-owner", b.ClusterUID)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := analytics.RestrictScope(grant, analytics.Selection{Namespaces: []string{"apps"}})
	if err != nil {
		t.Fatal(err)
	}
	scope.Resources = []string{target}
	rc := assistant.ResourceContext{SchemaVersion: "resource-investigation-context/v1", ContextID: uuid.New(), TenantID: b.TenantID.String(), OwnerSubject: "assistant-owner", ClusterUID: b.ClusterUID, Resources: []assistant.Candidate{{CanonicalID: target, Kind: "Pod", Name: "metric-pod", Namespace: "apps", ObservedAt: now}}, Symptom: "restarts", TimeRange: assistant.TimeRange{From: now.Add(-time.Minute), To: now, Timezone: "UTC"}, ResolvedAt: now, ObservationRevision: graph.Revision{OwnerEpoch: 1, GraphGeneration: 1}, GrantSnapshotDigest: graph.ScopeDigest(grant), ExecutionScope: scope, ExecutionScopeDigest: graph.ScopeDigest(scope)}
	rc.ContextDigest = graph.ScopeDigest(rc)
	conversation, err := repo.Create(ctx, assistant.Conversation{TenantID: b.TenantID, OwnerSubject: rc.OwnerSubject, Title: "Metric judgement", Mode: "troubleshoot", AuthorizationDomain: "operational", ClusterUID: b.ClusterUID, Binding: assistant.ConversationBinding{Timezone: "UTC", ResourceContext: &rc, Selection: analytics.Selection{Namespaces: []string{"apps"}}}})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := repo.Submit(ctx, b.TenantID, conversation.ConversationID, rc.OwnerSubject, "metric-judgement", assistant.SubmitMessage{Message: "检查重启", ExpectedConversationRevision: conversation.Revision})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := jobs.Claim(ctx, b.TenantID, "metric-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	job, err := jobs.Get(ctx, b.TenantID, turn.JobID)
	if err != nil {
		t.Fatal(err)
	}
	window := analytics.QueryTimeRange{From: rc.TimeRange.From, To: rc.TimeRange.To, Timezone: "UTC"}
	proof := analytics.Qualification{DatasetID: "metrics.pod-restarts", Operation: "aggregate", SourceRevision: 1, BindingDigest: evidence.BindingScopeDigest(binding), EvidenceDigest: graph.ScopeDigest("controlled-metric-proof"), CollectorVersion: "fixture/v1", VerifiedUntil: now.Add(time.Hour), CoverageFrom: now.Add(-time.Hour), CoverageTo: now, MaxWindowSeconds: 3600}
	adapter, err := analytics.NewVictoria(binding, metricServer.Client(), (evidence.Repository{Pool: worker}).CheckBinding, []analytics.Qualification{proof})
	if err != nil {
		t.Fatal(err)
	}
	intent := analytics.QueryIntent{SchemaVersion: "query-intent/v1", DatasetID: proof.DatasetID, Operation: proof.Operation, Metrics: []string{"restart_increase"}, Dimensions: []string{}, Filters: []analytics.Filter{{Field: "namespace", Operator: "eq", Value: "apps"}}, Sort: []analytics.Sort{}, Limit: 1, TimeRange: &window}
	plan, err := adapter.Compile(intent, analytics.Binding{Subject: job.Subject, Grant: job.Scope, Selection: analytics.Selection{Namespaces: []string{"apps"}, CanonicalIDs: []string{target}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := adapter.Execute(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	stored := investigation.BuildStoredQueryResult(result)
	metricStep := uuid.New()
	commit := func(step uuid.UUID, name string, args, raw []byte) {
		t.Helper()
		call := investigation.Call{StepID: step, ContextID: uuid.New(), JTI: uuid.NewString(), Name: name, Arguments: args, ArgsDigest: investigation.ArgumentsDigest(args), Reserve: investigation.Usage{ToolCalls: 1, ResultBytes: 65536, RawQueries: 1}}
		if _, err := jobs.BeginCall(ctx, lease, call); err != nil {
			t.Fatalf("begin %s: %v", name, err)
		}
		if _, err := jobs.CompleteToolStep(ctx, lease, step, raw, investigation.Usage{ToolCalls: 1, ResultBytes: int64(len(raw)), RawQueries: 1}); err != nil {
			t.Fatalf("complete %s: %v; jobScope=%+v queryScope=%+v catalogMatch=%t", name, err, job.Scope, plan.ExecutionScope(), result.CatalogDigest == job.Assistant.DatasetCatalogDigest)
		}
	}
	args, _ := json.Marshal(map[string]any{"intent": intent, "canonicalId": target, "windowRole": "fault"})
	raw, _ := json.Marshal(map[string]any{"schemaVersion": "tool-response/v3", "tool": "execute_analysis_query", "state": "succeeded", "evidenceRefs": []string{}, "data": map[string]any{"canonicalId": target, "queryResultRefs": []uuid.UUID{stored.QueryResultID}, "storedQueryResult": stored, "scope": plan.ExecutionScope()}})
	commit(metricStep, "execute_analysis_query", args, raw)
	toolsAPI := httpapi.AssistantTools{Legacy: httpapi.InvestigationTools{SP04: &httpapi.SP04Handlers{Pool: repo.Pool}}}
	args, _ = json.Marshal(map[string]any{"stepIds": []uuid.UUID{metricStep}})
	evaluation, err := toolsAPI.Invoke(ctx, job, "evaluate_fault_chain", args)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(evaluation)
	var wire struct {
		Data struct {
			Context       investigation.FaultChainContext `json:"context"`
			ContentDigest string                          `json:"contentDigest"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, assessment := range wire.Data.Context.ResourceAssessments {
		for _, id := range assessment.Supports {
			if id == stored.QueryResultID {
				t.Fatal("judgement names QueryResult instead of Step")
			}
			if id == metricStep {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("metric judgement has no committed Step support")
	}
	evalStep := uuid.New()
	commit(evalStep, "evaluate_fault_chain", args, raw)
	var answer investigation.AssistantAnswer
	if err := json.Unmarshal(assistantEmptyAnswer(), &answer); err != nil {
		t.Fatal(err)
	}
	answer.Partial = true
	answer.FaultChainContextRef = &investigation.FaultChainContextRef{StepID: evalStep, ContentDigest: wire.Data.ContentDigest}
	raw, _ = json.Marshal(answer)
	if err := jobs.Complete(ctx, lease, raw); err != nil {
		t.Fatal(err)
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	handler := &httpapi.AssistantHandlers{Repository: repo, CursorKey: key}
	actor := auth.RequestContext{TenantID: b.TenantID, Subject: rc.OwnerSubject, Roles: []auth.Role{auth.Operator}, RequestID: uuid.NewString()}
	for _, tc := range []struct {
		id      uuid.UUID
		subject string
		want    int
	}{{metricStep, rc.OwnerSubject, 200}, {stored.QueryResultID, rc.OwnerSubject, 403}, {uuid.New(), rc.OwnerSubject, 403}, {metricStep, "other-owner", 404}} {
		request := httptest.NewRequest("GET", "/api/v1/assistant/turns/"+turn.TurnID.String()+"/source-steps/"+tc.id.String(), nil)
		actor.Subject = tc.subject
		request = request.WithContext(auth.WithRequestContext(ctx, actor))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != tc.want {
			t.Fatalf("source Step HTTP status=%d want %d: %s", response.Code, tc.want, response.Body.String())
		}
		if tc.want == 200 && !json.Valid(response.Body.Bytes()) {
			t.Fatal("invalid source Step response")
		}
	}
}
