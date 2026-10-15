package integration

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"ops-platform/internal/analytics"
	"ops-platform/internal/app"
	"ops-platform/internal/assistant"
	"ops-platform/internal/auth"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/integrations/openbao"
	"ops-platform/internal/investigation"
	"ops-platform/internal/investigation/tools"
	"ops-platform/internal/modelprofile"
	"ops-platform/internal/persistence"
	"ops-platform/internal/resource"
	"ops-platform/internal/source"
	"slices"
)

func TestAssistantActualDeepFlowSignedMCP(t *testing.T) {
	if os.Getenv("OPS_ASSISTANT_DEEPFLOW_GRAPH_ONLY") != "1" || os.Getenv("OPS_ASSISTANT_MANUAL_MCP") != "1" {
		t.Skip("isolated actual signed DeepFlow MCP runtime required")
	}
	assistantRealRuntime(t, true, func(ctx context.Context, db *sql.DB, pool *pgxpool.Pool, b source.BoundSourceContext, repo assistant.Repository, profile modelprofile.Profile, _ *assistantEvaluationTrace, sp04 *httpapi.SP04Handlers) {
		const namespace = "ops-assistant-deepflow-20261013"
		scopes, _ := json.Marshal([]map[string]string{{"clusterId": b.ClusterID.String(), "namespace": namespace}})
		if _, err := db.ExecContext(ctx, `INSERT INTO platform.role_bindings(tenant_id,binding_id,subject,role_name,cluster_scopes,namespace_scopes) VALUES($1,$2,'native-mcp-operator','operator',$3,$4)`, b.TenantID, uuid.New(), `["`+b.ClusterID.String()+`"]`, scopes); err != nil {
			t.Fatal(err)
		}
		actor := auth.RequestContext{TenantID: b.TenantID, Subject: "native-mcp-operator", Roles: []auth.Role{auth.Operator}, ClusterScopes: []uuid.UUID{b.ClusterID}, NamespaceScopes: []auth.NamespaceScope{{ClusterID: b.ClusterID, Namespace: namespace}}, RequestID: uuid.NewString()}
		requestCtx := auth.WithRequestContext(ctx, actor)
		var list struct {
			Items []struct{ Metadata struct{ UID string } }
		}
		raw := sp04Kubectl(t, ctx, nil, "-n", namespace, "get", "pods", "-l", "ops.platform.io/canary-version=deployment-v1", "-o", "json")
		if json.Unmarshal(raw, &list) != nil || len(list.Items) != 2 {
			t.Fatal("actual two endpoint canaries unavailable")
		}
		targets := []string{}
		for _, p := range list.Items {
			targets = append(targets, resource.CanonicalID{Domain: "k8s", Tenant: b.TenantID.String(), Scope: b.ClusterUID, APIGroup: "core", Kind: "Pod", StableID: p.Metadata.UID}.String())
		}
		grant, err := (graph.Authorization{Pool: pool}).Effective(ctx, b.TenantID.String(), actor.Subject, b.ClusterUID)
		if err != nil {
			t.Fatal(err)
		}
		readyCtx, readyCancel := context.WithTimeout(ctx, 90*time.Second)
		ready := false
		for readyCtx.Err() == nil {
			details, e := sp04.AssistantCapabilityDetails(readyCtx, investigation.Job{TenantID: b.TenantID, Subject: actor.Subject, Scope: grant, Assistant: &investigation.AssistantTask{GrantSnapshotDigest: graph.ScopeDigest(grant)}})
			if e == nil {
				for _, capability := range details.Network {
					if capability["network"] == "live" && capability["l7"] == "live" && capability["trace"] == "CAPABILITY_DISABLED" {
						ready = true
					}
				}
			}
			if ready {
				break
			}
			time.Sleep(time.Second)
		}
		readyCancel()
		if !ready {
			t.Fatal("actual scoped L4/L7 capabilities did not qualify")
		}
		nativeResult, err := (httpapi.InvestigationTools{SP04: sp04, SP05: sp04.SP05}).Invoke(ctx, investigation.Job{TenantID: b.TenantID, JobID: uuid.New(), Subject: actor.Subject, Scope: grant}, "get_resource_context", json.RawMessage(`{"canonicalId":"`+targets[0]+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		raw, _ = json.Marshal(nativeResult.Data)
		var observation graph.Result
		if json.Unmarshal(raw, &observation) != nil || len(observation.Nodes) != 1 {
			t.Fatal("actual resolved graph identity unavailable")
		}
		entity := observation.Nodes[0]
		var actualNS string
		if e := db.QueryRowContext(ctx, `SELECT namespace FROM platform.resource_entities WHERE tenant_id=$1 AND canonical_id=$2 AND deleted_at IS NULL`, b.TenantID, entity.CanonicalID).Scan(&actualNS); e != nil || actualNS != namespace {
			t.Fatalf("actual persisted identity missing: %s namespace=%s err=%v", entity.CanonicalID, actualNS, e)
		}
		scope := analytics.CloneScope(grant)
		scope.Resources = []string{entity.CanonicalID}
		scope.Namespaces = []string{namespace}
		scope.ClusterScoped = false
		now := time.Now().UTC()
		resourceContext := assistant.ResourceContext{SchemaVersion: "resource-investigation-context/v1", ContextID: uuid.New(), TenantID: b.TenantID.String(), OwnerSubject: actor.Subject, ClusterUID: b.ClusterUID, Resources: []assistant.Candidate{{CanonicalID: entity.CanonicalID, Kind: entity.Kind, Name: entity.Name, Namespace: entity.Namespace, ObservedAt: entity.UpdatedAt}}, Symptom: "读取网络和应用层请求观测", TimeRange: assistant.TimeRange{From: now.Add(-20 * time.Second), To: now, Timezone: "Asia/Shanghai"}, ResolvedAt: observation.CollectedAt, ObservationRevision: observation.GraphRevision, GrantSnapshotDigest: graph.ScopeDigest(grant), ExecutionScope: scope, ExecutionScopeDigest: graph.ScopeDigest(scope)}
		resourceContext.ContextDigest = graph.ScopeDigest(resourceContext)

		if e := persistence.WithTenantTx(requestCtx, repo.Pool, b.TenantID, func(tx pgx.Tx) error {
			current, e := graph.EffectiveTx(requestCtx, tx, b.TenantID.String(), actor.Subject, b.ClusterUID, true)
			if e != nil {
				return fmt.Errorf("API role current fenced grant: %w", e)
			}
			selected, e := analytics.RestrictScope(current, analytics.Selection{Namespaces: []string{namespace}})
			if e != nil {
				return fmt.Errorf("API role selection: %w", e)
			}
			if !graph.ContainsScope(current, selected) {
				return fmt.Errorf("API role scope containment")
			}
			var ns string
			if e = tx.QueryRow(requestCtx, `SELECT namespace FROM platform.resource_entities WHERE tenant_id=$1 AND canonical_id=$2 AND deleted_at IS NULL`, b.TenantID, entity.CanonicalID).Scan(&ns); e != nil {
				return fmt.Errorf("API role native target read: %w", e)
			}
			if !selected.Allows(entity.CanonicalID, ns) {
				return fmt.Errorf("API role selected target denied")
			}
			return nil
		}); e != nil {
			t.Fatal(e)
		}
		conversation, err := repo.Create(requestCtx, assistant.Conversation{TenantID: b.TenantID, OwnerSubject: actor.Subject, Title: "实际网络工具机制", Mode: "troubleshoot", AuthorizationDomain: "operational", ClusterUID: b.ClusterUID, Binding: assistant.ConversationBinding{Timezone: "Asia/Shanghai", Selection: analytics.Selection{Namespaces: []string{namespace}}, ResourceContext: &resourceContext}})
		if err != nil {
			t.Fatal(err)
		}
		turn, err := repo.Submit(requestCtx, b.TenantID, conversation.ConversationID, actor.Subject, "native-mcp-deepflow", assistant.SubmitMessage{Message: "读取当前已确认目标的网络观测。", ExpectedConversationRevision: conversation.Revision})
		if err != nil {
			t.Fatal(err)
		}
		workerJobs := repo.Jobs
		workerJobs.Pool = pool
		lease, err := workerJobs.Claim(ctx, b.TenantID, "manual-native-mcp", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		job, err := repo.Jobs.Get(ctx, b.TenantID, turn.JobID)
		if err != nil {
			t.Fatal(err)
		}
		defs, err := tools.ForJob(job)
		if err != nil {
			t.Fatal(err)
		}
		raw, err = os.ReadFile(os.Getenv("SP06_RUNTIME_FILE"))
		var config app.SP06Config
		if err != nil || json.Unmarshal(raw, &config) != nil {
			t.Fatal("actual runtime transport configuration unavailable")
		}
		ca, err := os.ReadFile(config.CAFile)
		if err != nil {
			t.Fatal(err)
		}
		roots := x509.NewCertPool()
		roots.AppendCertsFromPEM(ca)
		directory := filepath.Dir(config.CertificateFile)
		cert, err := tls.LoadX509KeyPair(filepath.Join(directory, "ops-investigator.pem"), filepath.Join(directory, "ops-investigator.key"))
		if err != nil {
			t.Fatal(err)
		}
		baoCA, err := os.ReadFile(os.Getenv("SP03_TEST_OPENBAO_CA_FILE"))
		if err != nil {
			t.Fatal(err)
		}
		bao, err := openbao.NewClient(openbao.ClientConfig{Address: os.Getenv("SP03_TEST_OPENBAO_URL"), CACertBundle: baoCA, Token: os.Getenv("SP03_TEST_OPENBAO_TOKEN")})
		if err != nil {
			t.Fatal(err)
		}
		signer := investigation.ContextSigner{Transit: bao, Key: "investigation-signing"}
		jwt, err := signer.IssueRegistered(ctx, workerJobs, job, lease, "platform-mcp-gateway", "spiffe://ops.local/ns/sp06-test/sa/ops-investigator", tools.Names(defs))
		if err != nil {
			t.Fatal(err)
		}
		transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, Certificates: []tls.Certificate{cert}, ServerName: "ops-api.sp06-test.svc.cluster.local", MinVersion: tls.VersionTLS13}}
		defer transport.CloseIdleConnections()
		client := sdk.NewClient(&sdk.Implementation{Name: "actual-deepflow-acceptance", Version: "1"}, nil)
		session, err := client.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: "https://" + config.ListenAddress + "/mcp", HTTPClient: &http.Client{Transport: sp06BearerTransport{transport, jwt}}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		catalog, err := session.ListTools(ctx, nil)
		if err != nil || len(catalog.Tools) == 0 {
			t.Fatal("actual standard MCP catalog handshake", err)
		}
		outcomes := []map[string]any{}
		for _, operation := range []string{"get network health", "get l7 context"} {
			args := map[string]any{"operation": operation, "resourceCanonicalId": targets[0], "timeRange": map[string]string{"from": time.Now().UTC().Add(-20 * time.Second).Format(time.RFC3339), "to": time.Now().UTC().Format(time.RFC3339)}}
			raw, _ := json.Marshal(args)
			allocation, err := workerJobs.AllocateTool(ctx, lease, "query_deepflow", raw)
			if err != nil {
				t.Fatal(err)
			}
			call := &sdk.CallToolParams{Name: "query_deepflow", Arguments: args}
			call.SetMeta(map[string]any{"ops/stepId": allocation.StepID.String(), "ops/jti": allocation.JTI})
			result, err := session.CallTool(ctx, call)
			if err != nil || result == nil {
				t.Fatal("actual signed network invocation", operation, err)
			}
			encoded, _ := json.Marshal(result.StructuredContent)
			var output tools.Result
			if json.Unmarshal(encoded, &output) != nil || (output.State != "succeeded" && output.State != "partial") || len(output.EvidenceRefs) == 0 {
				diagnostic, _ := json.Marshal(map[string]any{"resourceCanonicalId": targets[0], "type": "deepflow", "timeRange": args["timeRange"], "queryTemplate": map[string]string{"get network health": "GetResourceNetworkHealth", "get l7 context": "GetL7Context"}[operation], "budget": map[string]int{"maxBytes": 32768, "timeoutMs": 2500}, "limit": 100})
				req := httptest.NewRequest("POST", "/api/v1/evidence:query", bytes.NewReader(diagnostic)).WithContext(requestCtx)
				req.Header.Set("Idempotency-Key", uuid.NewString())
				recorder := httptest.NewRecorder()
				sp04.ServeHTTP(recorder, req)
				var category struct {
					Code string `json:"code"`
				}
				_ = json.Unmarshal(recorder.Body.Bytes(), &category)
				t.Fatalf("actual tool output %s state=%s refs=%d error=%s; current direct API status=%d code=%s", operation, output.State, len(output.EvidenceRefs), output.ErrorCode, recorder.Code, category.Code)
			}
			again, err := session.CallTool(ctx, call)
			if err != nil || again == nil {
				t.Fatal("committed MCP replay", err)
			}
			replay, _ := json.Marshal(again.StructuredContent)
			var denied tools.Result
			if !again.IsError || json.Unmarshal(replay, &denied) != nil || denied.State != "failed" || len(denied.EvidenceRefs) != 0 {
				t.Fatal("wire replay accepted")
			}
			reused, e := workerJobs.AllocateTool(ctx, lease, "query_deepflow", raw)
			if e != nil || !reused.Committed || reused.StepID != allocation.StepID {
				t.Fatal("committed allocation not reused", e)
			}
			outcomes = append(outcomes, map[string]any{"operation": operation, "state": output.State, "evidenceRefs": output.EvidenceRefs, "stepId": allocation.StepID})
		}
		var count int
		if db.QueryRowContext(ctx, `SELECT count(*) FROM investigation.steps WHERE tenant_id=$1 AND job_id=$2 AND tool_name='query_deepflow' AND state='succeeded'`, b.TenantID, job.JobID).Scan(&count) != nil || count != 2 {
			t.Fatal("MCP replay created duplicate committed steps")
		}
		if err = repo.Cancel(requestCtx, b.TenantID, turn.TurnID, actor.Subject, "manual-mcp-finished"); err != nil {
			t.Fatal(err)
		}
		var incidents int
		if db.QueryRowContext(ctx, `SELECT count(*) FROM incident.records WHERE tenant_id=$1`, b.TenantID).Scan(&incidents) != nil || incidents != 0 {
			t.Fatal("no-Incident invocation created Incident")
		}
		report := map[string]any{"schemaVersion": "assistant-deepflow-mcp/v1", "passed": true, "fixtureOnly": false, "modelQualityAcceptance": false, "transport": "actual official SDK, mTLS, OpenBao signed registered context", "profileReference": profile.Reference(), "outcomes": outcomes, "committedSteps": count, "incidentCount": 0, "wireReplayRejected": true, "committedAllocationReuse": true, "productionAdmission": false, "dependencyFixtures": []string{"isolated tenant/role/PKI setup", "TLS/IAM Archive"}}
		raw, _ = json.MarshalIndent(report, "", "  ")
		path := os.Getenv("OPS_ASSISTANT_DEEPFLOW_MCP_REPORT")
		if path == "" || os.WriteFile(path, raw, 0600) != nil {
			t.Fatal("owned MCP report unavailable")
		}
	})
}

func TestAssistantActualNetworkOperationsSignedMCP(t *testing.T) {
	if os.Getenv("OPS_FAULT_CHAIN_RUNTIME") != "1" || os.Getenv("OPS_ASSISTANT_MANUAL_MCP") != "1" {
		t.Skip("isolated actual signed DeepFlow MCP runtime required")
	}
	assistantRealRuntime(t, true, func(ctx context.Context, db *sql.DB, pool *pgxpool.Pool, b source.BoundSourceContext, repo assistant.Repository, profile modelprofile.Profile, _ *assistantEvaluationTrace, sp04 *httpapi.SP04Handlers) {
		const namespace = "ops-assistant-deepflow-20261013"
		scopes, _ := json.Marshal([]map[string]string{{"clusterId": b.ClusterID.String(), "namespace": namespace}})
		if _, err := db.ExecContext(ctx, `INSERT INTO platform.role_bindings(tenant_id,binding_id,subject,role_name,cluster_scopes,namespace_scopes) VALUES($1,$2,'native-mcp-operator','operator',$3,$4)`, b.TenantID, uuid.New(), `["`+b.ClusterID.String()+`"]`, scopes); err != nil {
			t.Fatal(err)
		}
		actor := auth.RequestContext{TenantID: b.TenantID, Subject: "native-mcp-operator", Roles: []auth.Role{auth.Operator}, ClusterScopes: []uuid.UUID{b.ClusterID}, NamespaceScopes: []auth.NamespaceScope{{ClusterID: b.ClusterID, Namespace: namespace}}, RequestID: uuid.NewString()}
		requestCtx := auth.WithRequestContext(ctx, actor)
		var raw []byte
		targets := []string{}
		for _, label := range []string{"fault-chain-client", "fault-chain-server"} {
			var list struct {
				Items []struct{ Metadata struct{ UID string } }
			}
			raw := sp04Kubectl(t, ctx, nil, "-n", namespace, "get", "pods", "-l", "app="+label+",ops.platform.io/test-release=fault-chain-20261014", "-o", "json")
			if json.Unmarshal(raw, &list) != nil || len(list.Items) != 1 {
				t.Fatal("owned native pair required")
			}
			targets = append(targets, resource.CanonicalID{Domain: "k8s", Tenant: b.TenantID.String(), Scope: b.ClusterUID, APIGroup: "core", Kind: "Pod", StableID: list.Items[0].Metadata.UID}.String())
		}
		grant, err := (graph.Authorization{Pool: pool}).Effective(ctx, b.TenantID.String(), actor.Subject, b.ClusterUID)
		if err != nil {
			t.Fatal(err)
		}
		readyCtx, readyCancel := context.WithTimeout(ctx, 90*time.Second)
		ready := false
		for readyCtx.Err() == nil {
			details, e := sp04.AssistantCapabilityDetails(readyCtx, investigation.Job{TenantID: b.TenantID, Subject: actor.Subject, Scope: grant, Assistant: &investigation.AssistantTask{GrantSnapshotDigest: graph.ScopeDigest(grant)}})
			if e == nil {
				for _, capability := range details.Network {
					if capability["network"] == "live" && capability["l7"] == "live" && capability["trace"] == "CAPABILITY_DISABLED" {
						ready = true
					}
				}
			}
			if ready {
				break
			}
			time.Sleep(time.Second)
		}
		readyCancel()
		if !ready {
			t.Fatal("actual scoped L4/L7 capabilities did not qualify")
		}
		nativeResult, err := (httpapi.InvestigationTools{SP04: sp04, SP05: sp04.SP05}).Invoke(ctx, investigation.Job{TenantID: b.TenantID, JobID: uuid.New(), Subject: actor.Subject, Scope: grant}, "get_resource_context", json.RawMessage(`{"canonicalId":"`+targets[0]+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		raw, _ = json.Marshal(nativeResult.Data)
		var observation graph.Result
		if json.Unmarshal(raw, &observation) != nil || len(observation.Nodes) != 1 {
			t.Fatal("actual resolved graph identity unavailable")
		}
		entity := observation.Nodes[0]
		var actualNS string
		if e := db.QueryRowContext(ctx, `SELECT namespace FROM platform.resource_entities WHERE tenant_id=$1 AND canonical_id=$2 AND deleted_at IS NULL`, b.TenantID, entity.CanonicalID).Scan(&actualNS); e != nil || actualNS != namespace {
			t.Fatalf("actual persisted identity missing: %s namespace=%s err=%v", entity.CanonicalID, actualNS, e)
		}
		scope := analytics.CloneScope(grant)
		scope.Resources = []string{entity.CanonicalID}
		scope.Namespaces = []string{namespace}
		scope.ClusterScoped = false
		now := time.Now().UTC()
		resourceContext := assistant.ResourceContext{SchemaVersion: "resource-investigation-context/v1", ContextID: uuid.New(), TenantID: b.TenantID.String(), OwnerSubject: actor.Subject, ClusterUID: b.ClusterUID, Resources: []assistant.Candidate{{CanonicalID: entity.CanonicalID, Kind: entity.Kind, Name: entity.Name, Namespace: entity.Namespace, ObservedAt: entity.UpdatedAt}}, Symptom: "读取网络和应用层请求观测", TimeRange: assistant.TimeRange{From: now.Add(-20 * time.Second), To: now, Timezone: "Asia/Shanghai"}, ResolvedAt: observation.CollectedAt, ObservationRevision: observation.GraphRevision, GrantSnapshotDigest: graph.ScopeDigest(grant), ExecutionScope: scope, ExecutionScopeDigest: graph.ScopeDigest(scope)}
		resourceContext.ContextDigest = graph.ScopeDigest(resourceContext)

		proof, err := os.ReadFile(os.Getenv("OPS_FAULT_CHAIN_WINDOWS_PROOF"))
		var windows struct {
			Golden []struct {
				From int64 `json:"fromUnix"`
				To   int64 `json:"toUnix"`
			} `json:"golden"`
		}
		if err != nil || json.Unmarshal(proof, &windows) != nil || len(windows.Golden) != 3 {
			t.Fatal("three current qualified windows required")
		}
		window := func(i int) *analytics.QueryTimeRange {
			return &analytics.QueryTimeRange{From: time.Unix(windows.Golden[i].From, 0).UTC(), To: time.Unix(windows.Golden[i].To, 0).UTC(), Timezone: "Asia/Shanghai"}
		}
		results := []investigation.StoredQueryResult{}
		costs := []int64{}
		for _, operation := range []string{"trend", "compare_percent", "zero-baseline"} {
			request := investigation.FaultChainRequest{SchemaVersion: "fault-chain-request/v1", TargetCanonicalID: targets[0], Fault: *window(1), Baseline: window(0), Recovery: window(2), MaxEdges: 1}
			intent := analytics.QueryIntent{SchemaVersion: "query-intent/v1", DatasetID: "network.http-summary", Operation: "compare_percent", Metrics: []string{"http_5xx_percent"}, Dimensions: []string{"comparison"}, Filters: []analytics.Filter{}, Sort: []analytics.Sort{}, Limit: 3, TimeRange: window(1), ComparisonTimeRange: window(0), NetworkSelection: &analytics.NetworkSelection{TargetCanonicalID: targets[0], PeerCanonicalID: targets[1], Direction: "outgoing"}}
			if operation == "trend" {
				request.Fault = analytics.QueryTimeRange{From: window(0).From, To: window(2).To, Timezone: "Asia/Shanghai"}
				request.Baseline = nil
				request.Recovery = nil
				intent.TimeRange = &request.Fault
				intent.ComparisonTimeRange = nil
				intent.Operation = "trend"
				intent.Dimensions = []string{"time"}
				intent.StepSeconds = 60
			}
			if operation == "zero-baseline" {
				intent.DatasetID = "network.tcp-summary"
				intent.Metrics = []string{"connection_failure_observation_count"}
			}
			conversation, err := repo.Create(requestCtx, assistant.Conversation{TenantID: b.TenantID, OwnerSubject: actor.Subject, Title: "实际网络工具机制", Mode: "troubleshoot", AuthorizationDomain: "operational", ClusterUID: b.ClusterUID, Binding: assistant.ConversationBinding{Timezone: "Asia/Shanghai", Selection: analytics.Selection{Namespaces: []string{namespace}}, ResourceContext: &resourceContext}})
			if err != nil {
				t.Fatal(err)
			}
			turn, err := repo.Submit(requestCtx, b.TenantID, conversation.ConversationID, actor.Subject, operation, assistant.SubmitMessage{Message: "冻结的真实网络操作验收。", FaultChainRequest: &request, ExpectedConversationRevision: conversation.Revision})
			if err != nil {
				t.Fatal(err)
			}
			workerJobs := repo.Jobs
			workerJobs.Pool = pool
			lease, err := workerJobs.Claim(ctx, b.TenantID, "manual-native-mcp", time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			job, err := repo.Jobs.Get(ctx, b.TenantID, turn.JobID)
			if err != nil {
				t.Fatal(err)
			}
			defs, err := tools.ForJob(job)
			if err != nil {
				t.Fatal(err)
			}
			raw, err = os.ReadFile(os.Getenv("SP06_RUNTIME_FILE"))
			var config app.SP06Config
			if err != nil || json.Unmarshal(raw, &config) != nil {
				t.Fatal("actual runtime transport configuration unavailable")
			}
			ca, err := os.ReadFile(config.CAFile)
			if err != nil {
				t.Fatal(err)
			}
			roots := x509.NewCertPool()
			roots.AppendCertsFromPEM(ca)
			directory := filepath.Dir(config.CertificateFile)
			cert, err := tls.LoadX509KeyPair(filepath.Join(directory, "ops-investigator.pem"), filepath.Join(directory, "ops-investigator.key"))
			if err != nil {
				t.Fatal(err)
			}
			baoCA, err := os.ReadFile(os.Getenv("SP03_TEST_OPENBAO_CA_FILE"))
			if err != nil {
				t.Fatal(err)
			}
			bao, err := openbao.NewClient(openbao.ClientConfig{Address: os.Getenv("SP03_TEST_OPENBAO_URL"), CACertBundle: baoCA, Token: os.Getenv("SP03_TEST_OPENBAO_TOKEN")})
			if err != nil {
				t.Fatal(err)
			}
			signer := investigation.ContextSigner{Transit: bao, Key: "investigation-signing"}
			jwt, err := signer.IssueRegistered(ctx, workerJobs, job, lease, "platform-mcp-gateway", "spiffe://ops.local/ns/sp06-test/sa/ops-investigator", tools.Names(defs))
			if err != nil {
				t.Fatal(err)
			}
			transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, Certificates: []tls.Certificate{cert}, ServerName: "ops-api.sp06-test.svc.cluster.local", MinVersion: tls.VersionTLS13}}
			defer transport.CloseIdleConnections()
			client := sdk.NewClient(&sdk.Implementation{Name: "actual-deepflow-acceptance", Version: "1"}, nil)
			session, err := client.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: "https://" + config.ListenAddress + "/mcp", HTTPClient: &http.Client{Transport: sp06BearerTransport{transport, jwt}}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			catalog, err := session.ListTools(ctx, nil)
			if err != nil || len(catalog.Tools) == 0 {
				t.Fatal("actual standard MCP catalog handshake", err)
			}
			args := map[string]any{"intent": intent}
			encoded, _ := json.Marshal(args)
			allocation, err := workerJobs.AllocateTool(ctx, lease, "execute_analysis_query", encoded)
			if err != nil {
				t.Fatal(err)
			}
			call := &sdk.CallToolParams{Name: "execute_analysis_query", Arguments: args}
			call.SetMeta(map[string]any{"ops/stepId": allocation.StepID.String(), "ops/jti": allocation.JTI})
			response, err := session.CallTool(ctx, call)
			if err != nil || response == nil {
				t.Fatal("signed real network transport", operation, err)
			}
			if response.IsError {
				encoded, _ := json.Marshal(response.StructuredContent)
				var failure tools.Result
				json.Unmarshal(encoded, &failure)
				t.Fatalf("signed real network operation=%s state=%s code=%s", operation, failure.State, failure.ErrorCode)
			}
			encoded, _ = json.Marshal(response.StructuredContent)
			var wire struct {
				Data struct {
					Stored investigation.StoredQueryResult `json:"storedQueryResult"`
				} `json:"data"`
			}
			if json.Unmarshal(encoded, &wire) != nil || wire.Data.Stored.QueryResultID == uuid.Nil {
				t.Fatal("signed result missing", operation)
			}
			stored, err := repo.QueryResult(requestCtx, b.TenantID, wire.Data.Stored.QueryResultID, actor.Subject)
			if err != nil || stored.ContentDigest != wire.Data.Stored.ContentDigest || len(stored.Facts) == 0 {
				t.Fatal("authorized Fact readback changed", err)
			}
			result := stored.Result
			switch operation {
			case "trend":
				if !result.Exact || len(result.Rows) != 3 {
					t.Fatal("trend incomplete")
				}
				for i, want := range []float64{1, 5, 0} {
					if result.Rows[i].Value != want || result.Rows[i].Dimension != window(i).From.Format(time.RFC3339) {
						t.Fatal("trend oracle mismatch", i)
					}
				}
			case "compare_percent":
				if !result.Exact || len(result.Rows) != 1 || result.Rows[0].Dimension != "increase" || result.Rows[0].Value != 400 {
					t.Fatal("relative oracle mismatch")
				}
			case "zero-baseline":
				if result.Exact || result.Status != "no_data" || len(result.Rows) != 0 || !slices.Contains(result.Warnings, "percentage_requires_nonzero_baseline") {
					t.Fatal("zero baseline invented percentage")
				}
			}
			cost := analytics.NetworkRawQueryCost(intent)
			if int64(len(result.DerivationEvidenceRefs)) != cost {
				t.Fatal("hidden raw subquery")
			}
			for _, ref := range result.DerivationEvidenceRefs {
				parent, err := (evidence.Repository{Pool: repo.Pool}).Get(requestCtx, b.TenantID, uuid.MustParse(ref), grant)
				if err != nil || parent.ReplayState != "archived_verified" || parent.ArchiveRef == nil || parent.ArchiveRef.PlaintextDigest != parent.ContentDigest {
					t.Fatal("parent archive unavailable", err)
				}
			}
			if _, err := repo.QueryResult(requestCtx, b.TenantID, stored.QueryResultID, "other-owner"); err == nil {
				t.Fatal("cross-owner read allowed")
			}
			results = append(results, stored)
			costs = append(costs, cost)
			if err := repo.Cancel(requestCtx, b.TenantID, turn.TurnID, actor.Subject, "operation-acceptance-finished"); err != nil {
				t.Fatal(err)
			}
			session.Close()
		}
		encoded, _ := json.MarshalIndent(map[string]any{"passed": true, "schemaVersion": "fault-chain-signed-network-operations/v1", "transport": "official MCP SDK / mTLS / registered OpenBao context / signed Worker RPC", "rawQueryCosts": costs, "results": results, "ancillaryFixtures": []string{"tenant/role/PKI bootstrap", "TLS/IAM archive backend"}, "modelQualityAcceptance": false}, "", "  ")
		if err := os.WriteFile(os.Getenv("OPS_FAULT_CHAIN_SIGNED_OPERATIONS_REPORT"), encoded, 0600); err != nil {
			t.Fatal(err)
		}
	})
}
