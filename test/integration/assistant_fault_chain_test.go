package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"ops-platform/internal/analytics"
	"ops-platform/internal/assistant"
	"ops-platform/internal/auth"
	"ops-platform/internal/graph"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/investigation"
	"ops-platform/internal/modelprofile"
	"ops-platform/internal/persistence"
	"ops-platform/internal/resource"
	"ops-platform/internal/source"
)

func TestAssistantActualFaultChainWithoutModel(t *testing.T) {
	if os.Getenv("OPS_FAULT_CHAIN_RUNTIME") != "1" || os.Getenv("OPS_ASSISTANT_DEEPFLOW_GRAPH_ONLY") != "1" {
		t.Skip("isolated fault-chain proof and native runtime required")
	}
	assistantRealRuntime(t, true, func(ctx context.Context, db *sql.DB, pool *pgxpool.Pool, b source.BoundSourceContext, repo assistant.Repository, profile modelprofile.Profile, _ *assistantEvaluationTrace, sp04 *httpapi.SP04Handlers) {
		const namespace = "ops-assistant-deepflow-20261013"
		scopes, _ := json.Marshal([]map[string]string{{"clusterId": b.ClusterID.String(), "namespace": namespace}})
		if _, err := db.ExecContext(ctx, `INSERT INTO platform.role_bindings(tenant_id,binding_id,subject,role_name,cluster_scopes,namespace_scopes) VALUES($1,$2,'fault-chain-operator','operator',$3,$4)`, b.TenantID, uuid.New(), `["`+b.ClusterID.String()+`"]`, scopes); err != nil {
			t.Fatal(err)
		}
		actor := auth.RequestContext{TenantID: b.TenantID, Subject: "fault-chain-operator", Roles: []auth.Role{auth.Operator}, ClusterScopes: []uuid.UUID{b.ClusterID}, NamespaceScopes: []auth.NamespaceScope{{ClusterID: b.ClusterID, Namespace: namespace}}, RequestID: uuid.NewString()}
		requestCtx := auth.WithRequestContext(ctx, actor)
		grant, err := (graph.Authorization{Pool: pool}).Effective(ctx, b.TenantID.String(), actor.Subject, b.ClusterUID)
		if err != nil {
			t.Fatal(err)
		}
		var pods struct {
			Items []struct{ Metadata struct{ UID string } }
		}
		raw := sp04Kubectl(t, ctx, nil, "-n", namespace, "get", "pods", "-l", "app=fault-chain-client,ops.platform.io/test-release=fault-chain-20261014", "-o", "json")
		if json.Unmarshal(raw, &pods) != nil || len(pods.Items) != 1 {
			t.Fatal("owned native client missing")
		}
		target := resource.CanonicalID{Domain: "k8s", Tenant: b.TenantID.String(), Scope: b.ClusterUID, APIGroup: "core", Kind: "Pod", StableID: pods.Items[0].Metadata.UID}.String()
		var observation graph.Result
		for attempt := 0; attempt < 60; attempt++ {
			result, e := (httpapi.InvestigationTools{SP04: sp04, SP05: sp04.SP05}).Invoke(ctx, investigation.Job{TenantID: b.TenantID, JobID: uuid.New(), Subject: actor.Subject, Scope: grant}, "get_resource_context", json.RawMessage(`{"canonicalId":"`+target+`"}`))
			if e == nil {
				raw, _ = json.Marshal(result.Data)
				if json.Unmarshal(raw, &observation) == nil && len(observation.Nodes) == 1 {
					break
				}
			}
			time.Sleep(time.Second)
		}
		if len(observation.Nodes) != 1 {
			t.Fatal("native graph identity unavailable")
		}
		var proof struct {
			Window struct {
				From int64 `json:"fromUnix"`
				To   int64 `json:"toUnix"`
			} `json:"window"`
		}
		raw, err = os.ReadFile(os.Getenv("OPS_FAULT_CHAIN_PROOF"))
		if err != nil || json.Unmarshal(raw, &proof) != nil {
			t.Fatal("source proof unavailable")
		}
		window := analytics.QueryTimeRange{From: time.Unix(proof.Window.From, 0).UTC(), To: time.Unix(proof.Window.To, 0).UTC(), Timezone: "Asia/Shanghai"}
		scope := analytics.CloneScope(grant)
		scope.Resources = []string{target}
		scope.Namespaces = []string{namespace}
		scope.ClusterScoped = false
		entity := observation.Nodes[0]
		rc := assistant.ResourceContext{SchemaVersion: "resource-investigation-context/v1", ContextID: uuid.New(), TenantID: b.TenantID.String(), OwnerSubject: actor.Subject, ClusterUID: b.ClusterUID, Resources: []assistant.Candidate{{CanonicalID: target, Kind: entity.Kind, Name: entity.Name, Namespace: entity.Namespace, ObservedAt: entity.UpdatedAt}}, Symptom: "比较响应异常和依赖证据", TimeRange: assistant.TimeRange{From: window.From, To: window.To, Timezone: window.Timezone}, ResolvedAt: observation.CollectedAt, ObservationRevision: observation.GraphRevision, GrantSnapshotDigest: graph.ScopeDigest(grant), ExecutionScope: scope, ExecutionScopeDigest: graph.ScopeDigest(scope)}
		rc.ContextDigest = graph.ScopeDigest(rc)
		conversation, err := repo.Create(requestCtx, assistant.Conversation{TenantID: b.TenantID, OwnerSubject: actor.Subject, Title: "不发模型请求的故障链调查", Mode: "troubleshoot", AuthorizationDomain: "operational", ClusterUID: b.ClusterUID, Binding: assistant.ConversationBinding{Timezone: window.Timezone, Selection: analytics.Selection{Namespaces: []string{namespace}}, ResourceContext: &rc}})
		if err != nil {
			t.Fatal(err)
		}
		if err := persistence.WithTenantTx(requestCtx, repo.Pool, b.TenantID, func(tx pgx.Tx) error {
			var count int
			err := tx.QueryRow(ctx, `SELECT count(*) FROM platform.source_registrations s JOIN platform.cluster_registrations c USING(tenant_id,cluster_id) WHERE s.tenant_id=$1 AND s.source_type='deepflow' AND s.status='active' AND c.cluster_uid=$2 AND c.status='active'`, b.TenantID, b.ClusterUID).Scan(&count)
			t.Log("API role source selection count", count)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		request := assistant.SubmitMessage{Message: "收集故障链证据", ExpectedConversationRevision: conversation.Revision, FaultChainRequest: &investigation.FaultChainRequest{SchemaVersion: "fault-chain-request/v1", TargetCanonicalID: target, Fault: window, MaxEdges: 4}}
		turn, err := repo.Submit(requestCtx, b.TenantID, conversation.ConversationID, actor.Subject, "fault-chain-explicit", request)
		if err != nil {
			t.Fatal(err)
		}
		duplicate, err := repo.Submit(requestCtx, b.TenantID, conversation.ConversationID, actor.Subject, "fault-chain-explicit", request)
		if err != nil || duplicate.TurnID != turn.TurnID {
			t.Fatal("idempotency failed", err)
		}
		var current assistant.Turn
		for attempt := 0; attempt < 90; attempt++ {
			current, err = repo.Turn(requestCtx, b.TenantID, turn.TurnID, actor.Subject)
			if err != nil {
				t.Fatal(err)
			}
			if current.State != "queued" && current.State != "running" {
				break
			}
			time.Sleep(time.Second)
		}
		if current.State != "partial" && current.State != "completed" {
			t.Fatalf("collection state=%s code=%s", current.State, current.ErrorCode)
		}
		context, err := repo.FaultChainContext(requestCtx, b.TenantID, turn.TurnID, actor.Subject)
		if err != nil || len(context.Edges) == 0 || len(context.QueryResultRefs) == 0 || len(context.EvidenceRefs) == 0 {
			t.Fatalf("actual context edges=%d queries=%d evidence=%d err=%v", len(context.Edges), len(context.QueryResultRefs), len(context.EvidenceRefs), err)
		}
		validated := map[string]bool{}
		for _, id := range context.QueryResultRefs {
			stored, err := repo.QueryResult(requestCtx, b.TenantID, id, actor.Subject)
			if err != nil || !stored.Result.Exact || stored.Result.Status != "complete" {
				t.Fatal("qualified stored result unavailable", err)
			}
			for _, stat := range stored.Result.Statistics {
				if stat.Key == "current.responses" && stat.Value != 1000 {
					t.Fatal("HTTP population differs from independent oracle", stat.Value)
				}
			}
			for _, row := range stored.Result.Rows {
				if row.Dimension != "current" {
					continue
				}
				if stored.Result.DatasetID == "network.http-5xx-rate" && row.Value != 5 || stored.Result.DatasetID == "network.http-5xx" && row.Value != 50 {
					t.Fatal("HTTP observation differs from independent oracle", stored.Result.DatasetID, row.Value)
				}
			}
			validated[stored.Result.DatasetID] = true
		}
		if !validated["network.http-5xx-rate"] || !validated["network.http-5xx"] || !validated["network.http-response-time"] {
			t.Fatal("required HTTP datasets not collected")
		}
		var calls int
		if db.QueryRowContext(ctx, `SELECT count(*) FROM investigation.steps WHERE tenant_id=$1 AND job_id=$2 AND tool_name='model'`, b.TenantID, turn.JobID).Scan(&calls) != nil || calls != 0 {
			t.Fatal("model admission occurred")
		}
		if profile.BaseURL != "http://127.0.0.1:1/v1" {
			t.Fatal("offline model fixture not configured")
		}
		if _, err = repo.FaultChainContext(requestCtx, b.TenantID, turn.TurnID, "different-operator"); err == nil {
			t.Fatal("cross-owner context disclosed")
		}
		if _, err = db.ExecContext(ctx, `DELETE FROM platform.role_bindings WHERE tenant_id=$1 AND subject=$2`, b.TenantID, actor.Subject); err != nil {
			t.Fatal(err)
		}
		if _, err = repo.FaultChainContext(requestCtx, b.TenantID, turn.TurnID, actor.Subject); err == nil {
			t.Fatal("revoked grant exposed historical context")
		}
		if _, err = repo.QueryResult(requestCtx, b.TenantID, context.QueryResultRefs[0], actor.Subject); err == nil {
			t.Fatal("revoked grant exposed stored query")
		}
		result, _ := json.MarshalIndent(map[string]any{"passed": true, "authorizationRevocation": true, "schemaVersion": "fault-chain-runtime-acceptance/v1", "modelEndpointOffline": true, "modelKeyMissing": true, "modelCalls": calls, "nativePodUid": pods.Items[0].Metadata.UID, "turnId": turn.TurnID, "context": context, "ancillaryFixtures": []string{"tenant/role/PKI bootstrap", "TLS/IAM archive backend"}, "modelQualityAcceptance": false}, "", "  ")
		if os.WriteFile(os.Getenv("OPS_FAULT_CHAIN_RUNTIME_REPORT"), result, 0600) != nil {
			t.Fatal("report path required")
		}
	})
}
