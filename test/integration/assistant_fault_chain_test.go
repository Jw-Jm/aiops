package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"ops-platform/internal/analytics"
	"ops-platform/internal/assistant"
	"ops-platform/internal/auth"
	"ops-platform/internal/evidence"
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
	assistantRealRuntime(t, true, func(ctx context.Context, db *sql.DB, pool *pgxpool.Pool, b source.BoundSourceContext, repo assistant.Repository, profile modelprofile.Profile, trace *assistantEvaluationTrace, sp04 *httpapi.SP04Handlers) {
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
		appLabel := "fault-chain-client"
		multi := os.Getenv("OPS_FAULT_CHAIN_MULTI") == "1"
		if multi {
			appLabel = "fault-chain-server"
		}
		raw := sp04Kubectl(t, ctx, nil, "-n", namespace, "get", "pods", "-l", "app="+appLabel+",ops.platform.io/test-release=fault-chain-20261014", "-o", "json")
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
			Status         string `json:"status"`
			IdentityStable bool   `json:"identityStable"`
			Scenario       string `json:"scenario"`
			Callers        []struct {
				UID       string `json:"uid"`
				Responses int    `json:"responses"`
				Errors    int    `json:"errors"`
			} `json:"callers"`
			Window struct {
				From int64 `json:"fromUnix"`
				To   int64 `json:"toUnix"`
			} `json:"window"`
		}
		raw, err = os.ReadFile(os.Getenv("OPS_FAULT_CHAIN_PROOF"))
		if err != nil || json.Unmarshal(raw, &proof) != nil || proof.Status != "PASS" {
			t.Fatal("source proof unavailable")
		}
		expectedByPeer := map[string]float64{}
		if multi {
			if !proof.IdentityStable || len(proof.Callers) != 2 || (proof.Scenario != "mixed" && proof.Scenario != "both-abnormal") {
				t.Fatal("two-caller scenario proof required")
			}
			for _, caller := range proof.Callers {
				if caller.UID == "" || caller.Responses != 1000 {
					t.Fatal("invalid independent caller population")
				}
				id := resource.CanonicalID{Domain: "k8s", Tenant: b.TenantID.String(), Scope: b.ClusterUID, APIGroup: "core", Kind: "Pod", StableID: caller.UID}.String()
				expectedByPeer[id] = float64(caller.Errors)
			}
			if len(expectedByPeer) != 2 {
				t.Fatal("duplicate caller identity")
			}
		}
		window := analytics.QueryTimeRange{From: time.Unix(proof.Window.From, 0).UTC(), To: time.Unix(proof.Window.To, 0).UTC(), Timezone: "Asia/Shanghai"}
		var baseline, recovery *analytics.QueryTimeRange
		comparison := os.Getenv("OPS_FAULT_CHAIN_WINDOWS_PROOF") != ""
		if comparison {
			raw, e := os.ReadFile(os.Getenv("OPS_FAULT_CHAIN_WINDOWS_PROOF"))
			var p struct {
				Golden []struct {
					From int64 `json:"fromUnix"`
					To   int64 `json:"toUnix"`
				} `json:"golden"`
			}
			if e != nil || json.Unmarshal(raw, &p) != nil || len(p.Golden) != 3 {
				t.Fatal("window proof required")
			}
			w := func(i int) *analytics.QueryTimeRange {
				return &analytics.QueryTimeRange{From: time.Unix(p.Golden[i].From, 0).UTC(), To: time.Unix(p.Golden[i].To, 0).UTC(), Timezone: "Asia/Shanghai"}
			}
			baseline, recovery = w(0), w(2)
			window = *w(1)
		}

		scope := analytics.CloneScope(grant)
		scope.Resources = []string{target}
		scope.Namespaces = []string{namespace}
		// ResourceContext describes the Pod anchor; the explicit conversation
		// selection separately admits cluster-scoped shared Node reads.
		scope.ClusterScoped = false
		entity := observation.Nodes[0]
		rc := assistant.ResourceContext{SchemaVersion: "resource-investigation-context/v1", ContextID: uuid.New(), TenantID: b.TenantID.String(), OwnerSubject: actor.Subject, ClusterUID: b.ClusterUID, Resources: []assistant.Candidate{{CanonicalID: target, Kind: entity.Kind, Name: entity.Name, Namespace: entity.Namespace, ObservedAt: entity.UpdatedAt}}, Symptom: "比较响应异常和依赖证据", TimeRange: assistant.TimeRange{From: window.From, To: window.To, Timezone: window.Timezone}, ResolvedAt: observation.CollectedAt, ObservationRevision: observation.GraphRevision, GrantSnapshotDigest: graph.ScopeDigest(grant), ExecutionScope: scope, ExecutionScopeDigest: graph.ScopeDigest(scope)}
		rc.ContextDigest = graph.ScopeDigest(rc)
		conversation, err := repo.Create(requestCtx, assistant.Conversation{TenantID: b.TenantID, OwnerSubject: actor.Subject, Title: "不发模型请求的故障链调查", Mode: "troubleshoot", AuthorizationDomain: "operational", ClusterUID: b.ClusterUID, Binding: assistant.ConversationBinding{Timezone: window.Timezone, Selection: analytics.Selection{Namespaces: []string{namespace}, IncludeClusterScoped: multi}, ResourceContext: &rc}})
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
		request := assistant.SubmitMessage{Message: "收集故障链证据", ExpectedConversationRevision: conversation.Revision, FaultChainRequest: &investigation.FaultChainRequest{SchemaVersion: "fault-chain-request/v1", TargetCanonicalID: target, Fault: window, Baseline: baseline, Recovery: recovery, MaxEdges: 4}}
		if comparison {
			// The independent window oracle covers one directed edge; retain
			// both protocol/recovery checks within the original query budget.
			request.FaultChainRequest.MaxEdges = 1
		}
		if os.Getenv("OPS_ASSISTANT_OPERATIONAL_SOURCES") == "1" {
			request.FaultChainRequest.SchemaVersion = "fault-chain-request/v2"
		}
		turn, err := repo.Submit(requestCtx, b.TenantID, conversation.ConversationID, actor.Subject, "fault-chain-explicit", request)
		if err != nil {
			t.Fatal(err)
		}
		duplicate, err := repo.Submit(requestCtx, b.TenantID, conversation.ConversationID, actor.Subject, "fault-chain-explicit", request)
		if err != nil || duplicate.TurnID != turn.TurnID {
			t.Fatal("idempotency failed", err)
		}
		var killedLease investigation.Lease
		if trace != nil && trace.recoveryPaused != nil {
			select {
			case <-trace.recoveryPaused:
			case <-time.After(45 * time.Second):
				t.Fatal("fixed collector did not reach recovery barrier")
			}
			var committed int
			if db.QueryRowContext(ctx, `SELECT count(*) FROM investigation.steps WHERE tenant_id=$1 AND job_id=$2 AND tool_name='get_assistant_context' AND state='succeeded'`, b.TenantID, turn.JobID).Scan(&committed) != nil || committed != 1 {
				t.Fatal("recovery barrier preceded committed context")
			}
			killedLease.TenantID = b.TenantID
			killedLease.JobID = turn.JobID
			if db.QueryRowContext(ctx, `SELECT lease_token,fencing_epoch FROM investigation.worker_queue WHERE tenant_id=$1 AND job_id=$2`, b.TenantID, turn.JobID).Scan(&killedLease.Token, &killedLease.Generation) != nil {
				t.Fatal("dispatcher lease unavailable")
			}
			trace.restartProcesses()
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
		if killedLease.Generation > 0 {
			var generation int64
			var reads int
			if db.QueryRowContext(ctx, `SELECT fencing_epoch FROM investigation.worker_queue WHERE tenant_id=$1 AND job_id=$2`, b.TenantID, turn.JobID).Scan(&generation) != nil || generation <= killedLease.Generation {
				t.Fatal("dispatcher did not take over real expired lease")
			}
			if db.QueryRowContext(ctx, `SELECT count(*) FROM investigation.steps WHERE tenant_id=$1 AND job_id=$2 AND tool_name='get_assistant_context' AND state='succeeded'`, b.TenantID, turn.JobID).Scan(&reads) != nil || reads != 1 {
				t.Fatal("committed context repeated after process kill")
			}
			if err := repo.Jobs.Complete(ctx, killedLease, assistantEmptyAnswer()); !errors.Is(err, investigation.ErrLease) {
				t.Fatal("old dispatcher accepted a late answer", err)
			}
			t.Log("actual dispatcher and Python SIGKILL; real 45-second lease takeover; context reused once; old generation rejected", generation)
		}
		context, err := repo.FaultChainContext(requestCtx, b.TenantID, turn.TurnID, actor.Subject)
		if err != nil || len(context.Edges) == 0 || len(context.QueryResultRefs) == 0 || len(context.EvidenceRefs) == 0 {
			t.Fatalf("actual context edges=%d queries=%d evidence=%d err=%v", len(context.Edges), len(context.QueryResultRefs), len(context.EvidenceRefs), err)
		}
		if os.Getenv("OPS_ASSISTANT_OPERATIONAL_SOURCES") == "1" && len(context.SourceSteps) == 0 {
			t.Fatal("operational sources did not join committed context")
		}
		if comparison && len(context.QueryResultRefs) != 4 {
			if killedLease.Generation == 0 {
				t.Fatal("both protocol comparisons for fault and recovery required", len(context.QueryResultRefs))
			}
			var partial investigation.AssistantAnswer
			if json.Unmarshal(current.Answer, &partial) != nil || !partial.Partial || len(partial.MissingEvidence) == 0 {
				t.Fatal("interrupted recovery omitted explicit partial checks")
			}
			var failed int
			if db.QueryRowContext(ctx, `SELECT count(*) FROM investigation.steps WHERE tenant_id=$1 AND job_id=$2 AND tool_name='execute_analysis_query' AND state='failed' AND error_code='SOURCE_DEGRADED'`, b.TenantID, turn.JobID).Scan(&failed) != nil || failed == 0 {
				t.Fatal("recovery lost comparisons without recorded source failure")
			}
			t.Log("source failure after takeover preserved committed statistics with explicit partial gaps", len(context.QueryResultRefs), failed)
		}
		rules := map[string]bool{}
		if comparison {
			for _, edge := range context.Edges {
				if edge.TemporalAssessment != "unreliable" {
					t.Fatal("unverified source clock became propagation order")
				}
				for _, observation := range edge.Observations {
					if len(observation.Supports) > 0 {
						rules[observation.RuleID] = true
					}
					if len(observation.FactRefs) == 0 {
						t.Fatal("rule lost committed statistics or evidence references")
					}
				}
			}
		}

		validated := map[string]bool{}
		validatedWindows := map[string]bool{}
		for _, id := range context.QueryResultRefs {
			stored, err := repo.QueryResult(requestCtx, b.TenantID, id, actor.Subject)
			if err != nil {
				t.Fatal("stored result unavailable", err)
			}
			if stored.Result.NetworkContext == nil && os.Getenv("OPS_ASSISTANT_OPERATIONAL_SOURCES") == "1" && (stored.Result.Status != "complete" || !stored.Result.Exact) {
				if stored.Result.Status == "no_data" && len(stored.Facts) > 0 {
					t.Fatal("missing source produced numeric facts")
				}
				t.Log("operational query remains an explicit gap", stored.Result.DatasetID, stored.Result.Status)
				continue
			}
			for _, ref := range stored.Result.DerivationEvidenceRefs {
				var parent evidence.Evidence
				for attempt := 0; attempt < 30; attempt++ {
					parent, err = (evidence.Repository{Pool: repo.Pool}).Get(requestCtx, b.TenantID, uuid.MustParse(ref), grant)
					if err != nil {
						t.Fatal(err)
					}
					if parent.ReplayState == "archived_verified" {
						break
					}
					time.Sleep(100 * time.Millisecond)
				}
				if parent.ArchiveRef == nil || parent.ArchiveRef.PlaintextDigest != parent.ContentDigest || parent.ArchiveRef.Object.VersionID == "" {
					t.Fatal("unverified parent archive", ref, parent.ReplayState)
				}
			}
			if n := stored.Result.NetworkContext; n != nil && n.Protocol == "http" && ((!multi && n.Selection.Direction == "incoming") || (multi && n.Selection.Direction == "outgoing")) {
				if stored.Result.Exact || stored.Result.Status != "no_data" || len(stored.Result.Rows) != 0 {
					t.Fatal("reverse population must remain explicitly empty")
				}
				found := false
				for _, stat := range stored.Result.Statistics {
					if stat.Key == "current.responses" {
						found = true
						if stat.Value != 0 {
							t.Fatal("unexpected reverse HTTP population")
						}
					}
				}
				if !found {
					t.Fatal("reverse population denominator missing")
				}
				continue
			}
			if !stored.Result.Exact || stored.Result.Status != "complete" {
				t.Fatal("qualified network result unavailable")
			}

			isRecovery := stored.Result.TimeRange.From.Equal(recoveryFrom(recovery))
			expectedErrors := float64(50)
			if isRecovery {
				expectedErrors = 0
			}
			if multi {
				n := stored.Result.NetworkContext
				if n == nil || n.Selection.TargetCanonicalID != target || n.Selection.Direction != "incoming" || n.TimeReliable {
					t.Fatal("multi-caller frozen selection or time gate lost")
				}
				var ok bool
				expectedErrors, ok = expectedByPeer[n.Selection.PeerCanonicalID]
				if !ok {
					t.Fatal("result is outside independent caller identities")
				}
				if !stored.Result.TimeRange.From.Equal(window.From) || !stored.Result.TimeRange.To.Equal(window.To) {
					t.Fatal("multi-caller window changed")
				}
			}
			for _, stat := range stored.Result.Statistics {
				if stat.Key == "current.responses" && stat.Value != 1000 || stat.Key == "current.5xx" && stat.Value != expectedErrors {
					t.Fatal("HTTP population differs from independent oracle", stat.Value)
				}
			}
			for _, row := range stored.Result.Rows {
				if row.Dimension != "current" {
					continue
				}
				if stored.Result.DatasetID == "network.http-5xx-rate" && row.Value != expectedErrors/10 || stored.Result.DatasetID == "network.http-5xx" && row.Value != expectedErrors {
					t.Fatal("HTTP observation differs from independent oracle", stored.Result.DatasetID, row.Value)
				}
			}
			if comparison {
				expected := []float64{1, 5, 4}
				unit := "percentage_points"
				if stored.Result.DatasetID == "network.http-summary" && isRecovery {
					expected = []float64{5, 0, 5}
				}
				if stored.Result.DatasetID == "network.tcp-summary" {
					expected = []float64{0, 100, 100}
					unit = "observations"
					if isRecovery {
						expected = []float64{100, 0, 100}
					}
				}
				if len(stored.Result.Rows) != 3 || stored.Facts[2].Unit != unit {
					t.Fatal("comparison rows or difference unit invalid")
				}
				for i, row := range stored.Result.Rows {
					if row.Value != expected[i] {
						t.Fatal("comparison differs from independent oracle", stored.Result.DatasetID, row.Value, expected[i])
					}
				}
			}
			validated[stored.Result.DatasetID] = true
			phase := "fault"
			if isRecovery {
				phase = "recovery"
			}
			if comparison && !isRecovery && !stored.Result.TimeRange.From.Equal(window.From) {
				t.Fatal("result used an unfrozen window")
			}
			validatedWindows[stored.Result.DatasetID+"/"+phase] = true
		}
		if comparison && killedLease.Generation == 0 && (!validated["network.http-summary"] || !validated["network.tcp-summary"]) {
			t.Fatal("qualified protocol snapshots not collected")
		}
		if comparison {
			expectedRules := map[string]bool{
				"R1/http-response-increase":         validatedWindows["network.http-summary/fault"],
				"R1/http-latency-increase":          validatedWindows["network.http-summary/fault"],
				"R2/transport-observation-increase": validatedWindows["network.tcp-summary/fault"],
				"R5/recovery-observation":           validatedWindows["network.http-summary/recovery"] || validatedWindows["network.tcp-summary/recovery"],
			}
			for rule, expected := range expectedRules {
				if rules[rule] != expected {
					t.Fatal("rule support differs from committed protocol/window facts", rule, rules[rule], expected)
				}
			}
		}
		sharedNativeChecks := []map[string]any{}
		if multi {
			observations := map[string]bool{}
			peers := map[string]bool{}
			for _, edge := range context.Edges {
				for _, ob := range edge.Observations {
					observations[ob.RuleID] = true
				}
			}
			for _, id := range context.QueryResultRefs {
				q, e := repo.QueryResult(requestCtx, b.TenantID, id, actor.Subject)
				if e != nil {
					t.Fatal(e)
				}
				peers[q.Result.NetworkContext.Selection.PeerCanonicalID] = true
			}
			if len(peers) != 2 || !observations["R1/observed-http-5xx"] || observations["R6/no-observed-http-5xx"] != (proof.Scenario == "mixed") || observations["R3/shared-resource-observation"] {
				t.Fatal("two callers' competing explanations or source gate lost", len(peers), observations)
			}
			rows, err := db.QueryContext(ctx, `SELECT step_id,tool_name,result FROM investigation.steps WHERE tenant_id=$1 AND job_id=$2 AND state='succeeded' AND tool_name IN ('get_dependencies','get_resource_health') ORDER BY started_at`, b.TenantID, turn.JobID)
			if err != nil {
				t.Fatal(err)
			}
			sharedPaths := map[string]map[string]bool{}
			healthy := map[string]bool{}
			for rows.Next() {
				var step uuid.UUID
				var tool string
				var raw []byte
				if err := rows.Scan(&step, &tool, &raw); err != nil {
					t.Fatal(err)
				}
				var result struct {
					Data graph.Result `json:"data"`
				}
				if json.Unmarshal(raw, &result) != nil {
					t.Fatal("invalid committed native result")
				}
				snapshot := result.Data
				if snapshot.Partial || snapshot.Freshness != "fresh" || len(snapshot.ObservationSources) == 0 {
					t.Fatal("native shared checks were not qualified", tool)
				}
				health := []investigation.FaultChainResourceHealth{}
				if tool == "get_resource_health" {
					health = investigation.CommittedFaultChainResourceHealth(step, snapshot)
				}
				for _, h := range health {
					healthy[h.CanonicalID] = h.ConditionStatus == "True"
				}
				for _, relation := range snapshot.Edges {
					if relation.Kind != "scheduled_on" {
						continue
					}
					if _, selected := expectedByPeer[relation.From]; !selected {
						continue
					}
					if sharedPaths[relation.To] == nil {
						sharedPaths[relation.To] = map[string]bool{}
					}
					sharedPaths[relation.To][relation.From] = true
				}
				sharedNativeChecks = append(sharedNativeChecks, map[string]any{"stepId": step, "tool": tool, "collectedAt": snapshot.CollectedAt, "sources": snapshot.ObservationSources, "relations": snapshot.Edges, "health": health})
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			sharedHealthyNode := false
			for node, callers := range sharedPaths {
				if len(callers) == 2 && healthy[node] {
					sharedHealthyNode = true
				}
			}
			if !sharedHealthyNode || !healthy[target] {
				t.Fatal("actual shared Node paths or common downstream health not collected")
			}
			for _, edge := range context.Edges {
				if !slices.Contains(edge.MissingEvidence, "shared_resource_cause_not_checked") {
					t.Fatal("unqualified common-resource candidate lost its explicit gap")
				}
			}
			t.Log("two actual callers; scenario", proof.Scenario, "R3 remains unavailable for unqualified source time")
		}
		if !comparison && (!validated["network.http-5xx-rate"] || !validated["network.http-response-time"]) {
			t.Fatal("required HTTP datasets not collected")
		}
		var calls int
		if db.QueryRowContext(ctx, `SELECT count(*) FROM investigation.steps WHERE tenant_id=$1 AND job_id=$2 AND tool_name='model'`, b.TenantID, turn.JobID).Scan(&calls) != nil || calls != 0 {
			t.Fatal("model admission occurred")
		}
		if os.Getenv("OPS_FAULT_CHAIN_MODEL") != "1" && profile.BaseURL != "http://127.0.0.1:1/v1" {
			t.Fatal("offline model fixture not configured")
		}
		modelReceipt := map[string]any{"attempted": false}
		if os.Getenv("OPS_FAULT_CHAIN_MODEL") == "1" {
			latest, err := repo.Get(requestCtx, b.TenantID, conversation.ConversationID, actor.Subject)
			if err != nil {
				t.Fatal(err)
			}
			follow, err := repo.Submit(requestCtx, b.TenantID, conversation.ConversationID, actor.Subject, "actual-model-explanation", assistant.SubmitMessage{Message: "先调用 get_assistant_context，读取 historicalFaultChainContext 和 historicalFaultChainQueryResults。用中文解释已提交的统计、支持、冲突与缺口，只引用已提交的 Fact/Evidence，不重新查询或修改窗口，不确认根因。", ExpectedConversationRevision: latest.Revision})
			if err != nil {
				t.Fatal(err)
			}
			var explanation assistant.Turn
			for attempt := 0; attempt < 150; attempt++ {
				explanation, err = repo.Turn(requestCtx, b.TenantID, follow.TurnID, actor.Subject)
				if err != nil {
					t.Fatal(err)
				}
				if explanation.State != "running" && explanation.State != "queued" {
					break
				}
				time.Sleep(time.Second)
			}
			var modelCalls, contextReads int
			if db.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE tool_name='model'),count(*) FILTER(WHERE tool_name='get_assistant_context' AND state='succeeded') FROM investigation.steps WHERE tenant_id=$1 AND job_id=$2`, b.TenantID, follow.JobID).Scan(&modelCalls, &contextReads) != nil {
				t.Fatal("model ledger unavailable")
			}
			historical, err := repo.FaultChainContext(requestCtx, b.TenantID, turn.TurnID, actor.Subject)
			if err != nil || graph.ScopeDigest(historical) != graph.ScopeDigest(context) {
				t.Fatal("model explanation lost committed evidence", err)
			}
			modelReceipt = map[string]any{"attempted": true, "turnId": follow.TurnID, "state": explanation.State, "errorCode": explanation.ErrorCode, "modelCalls": modelCalls, "contextReads": contextReads, "evidencePreserved": true, "answer": json.RawMessage(explanation.Answer), "mechanismPassed": modelCalls > 0 && contextReads > 0, "chineseQualityAcceptance": false}
			t.Log("actual model explanation outcome", explanation.State, explanation.ErrorCode, "modelCalls", modelCalls, "contextReads", contextReads, "evidence preserved")
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
		result, _ := json.MarshalIndent(map[string]any{"passed": true, "authorizationRevocation": true, "processRecovery": killedLease.Generation > 0, "killedGeneration": killedLease.Generation, "validatedWindows": validatedWindows, "schemaVersion": "fault-chain-runtime-acceptance/v1", "modelExplanation": modelReceipt, "modelEndpointOffline": os.Getenv("OPS_FAULT_CHAIN_MODEL") != "1", "modelKeyMissing": os.Getenv("OPS_FAULT_CHAIN_MODEL") != "1", "modelCalls": calls, "sharedNativeChecks": sharedNativeChecks, "multiCallerScenario": proof.Scenario, "nativePodUid": pods.Items[0].Metadata.UID, "turnId": turn.TurnID, "context": context, "ancillaryFixtures": []string{"tenant/role/PKI bootstrap", "TLS/IAM archive backend"}, "modelQualityAcceptance": false}, "", "  ")
		if os.WriteFile(os.Getenv("OPS_FAULT_CHAIN_RUNTIME_REPORT"), result, 0600) != nil {
			t.Fatal("report path required")
		}
	})
}

func recoveryFrom(w *analytics.QueryTimeRange) time.Time {
	if w == nil {
		return time.Time{}
	}
	return w.From
}
