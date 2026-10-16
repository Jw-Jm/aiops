package integration

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/analytics"
	"ops-platform/internal/assistant"
	"ops-platform/internal/finding"
	"ops-platform/internal/graph"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/investigation"
	"ops-platform/internal/persistence"
	"ops-platform/internal/resource"
	"ops-platform/internal/resourcestore"
)

// This proves real migrations/runtime roles and semantics with labelled source
// fixtures. Actual Kubernetes observations use the separate live test below.
func TestAssistantOperationalResourceHistory(t *testing.T) {
	if os.Getenv("OPS_ASSISTANT_RUNTIME_ACCEPTANCE") != "1" {
		t.Skip("isolated remote runtime required")
	}
	for _, kind := range []string{"Pod", "Node", "PersistentVolumeClaim"} {
		t.Run(kind, func(t *testing.T) {
			ctx, db, worker, b, repo, jobs := assistantExtendedFixture(t)
			now := time.Now().UTC().Truncate(time.Second)
			start := now.Add(-time.Minute)
			namespace := "apps"
			if kind == "Node" {
				namespace = ""
			}
			uid := uuid.NewString()
			target := resource.CanonicalID{Domain: "k8s", Tenant: b.TenantID.String(), Scope: b.ClusterUID, APIGroup: "core", Kind: kind, StableID: uid}.String()
			store := resourcestore.Repository{Pool: worker, ExpectedRevision: 1, BackendLogicalID: "sp05-native"}
			previous := map[string]string{}
			for i, at := range []time.Time{start.Add(-time.Second), start, now} {
				object := unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": kind, "metadata": map[string]any{"uid": uid, "name": "operational-fixture", "namespace": namespace, "resourceVersion": string(rune('1' + i))}, "spec": map[string]any{"replicas": int64(i)}, "status": map[string]any{"phase": "Pending", "conditions": []any{map[string]any{"type": "Ready", "status": "False"}}}}}
				object, err := kubernetes.Project(object, at)
				if err != nil {
					t.Fatal(err)
				}
				state := kubernetes.GVRState{LastListCompletedAt: start.Add(-2 * time.Second), LastWatchEventAt: at, WatchConnected: true, WatchContinuous: true}
				// The fixture's visible projection equals its complete native list.
				snapshot := kubernetes.Snapshot{Objects: []unstructured.Unstructured{object}, State: state}
				previous, err = store.SyncSnapshot(ctx, b.TenantID.String(), b.SourceID.String(), b.ClusterUID, kind, "core", snapshot, snapshot, previous)
				if err != nil {
					t.Fatal(err)
				}
			}
			grant, err := (graph.Authorization{Pool: worker}).Effective(ctx, b.TenantID.String(), "assistant-owner", b.ClusterUID)
			if err != nil {
				t.Fatal(err)
			}
			scope, err := analytics.RestrictScope(grant, analytics.Selection{Namespaces: []string{"apps"}, IncludeClusterScoped: true})
			if err != nil {
				t.Fatal(err)
			}
			scope.Resources = []string{target}
			scope.ClusterScoped = kind == "Node"
			if kind == "Node" {
				scope.Namespaces = []string{}
			}
			rc := assistant.ResourceContext{SchemaVersion: "resource-investigation-context/v1", ContextID: uuid.New(), TenantID: b.TenantID.String(), OwnerSubject: "assistant-owner", ClusterUID: b.ClusterUID, Resources: []assistant.Candidate{{CanonicalID: target, Kind: kind, Name: "operational-fixture", Namespace: namespace, ObservedAt: now}}, Symptom: "labelled operational history fixture", TimeRange: assistant.TimeRange{From: start, To: now, Timezone: "UTC"}, ResolvedAt: now, ObservationRevision: graph.Revision{OwnerEpoch: 1, GraphGeneration: 1}, GrantSnapshotDigest: graph.ScopeDigest(grant), ExecutionScope: scope, ExecutionScopeDigest: graph.ScopeDigest(scope)}
			rc.ContextDigest = graph.ScopeDigest(rc)
			conversation, err := repo.Create(ctx, assistant.Conversation{TenantID: b.TenantID, OwnerSubject: rc.OwnerSubject, Title: "Operational history fixture", Mode: "troubleshoot", AuthorizationDomain: "operational", ClusterUID: b.ClusterUID, Binding: assistant.ConversationBinding{Timezone: "UTC", ResourceContext: &rc, Selection: analytics.Selection{Namespaces: []string{"apps"}, IncludeClusterScoped: true}}})
			if err != nil {
				t.Fatal(err)
			}
			repo.FaultChainEnabled = true
			request := investigation.FaultChainRequest{SchemaVersion: "fault-chain-request/v2", TargetCanonicalID: target, Fault: analytics.QueryTimeRange{From: start, To: now, Timezone: "UTC"}, MaxEdges: 4}
			turn, err := repo.Submit(ctx, b.TenantID, conversation.ConversationID, rc.OwnerSubject, "history", assistant.SubmitMessage{Message: "收集运行数据", ExpectedConversationRevision: conversation.Revision, FaultChainRequest: &request})
			if err != nil {
				t.Fatal(err)
			}
			j, err := jobs.Get(ctx, b.TenantID, turn.JobID)
			if err != nil {
				t.Fatal(err)
			}
			// Old worker sessions cannot select unclaimed v2 jobs through the queue.
			if err := persistence.WithTenantTx(ctx, worker, b.TenantID, func(tx pgx.Tx) error {
				var n int
				if err := tx.QueryRow(ctx, `SELECT count(*) FROM investigation.worker_queue WHERE tenant_id=$1 AND job_id=$2`, b.TenantID, j.JobID).Scan(&n); err != nil {
					return err
				}
				if n != 0 {
					t.Fatal("old worker sees v2 job")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			lease, err := jobs.Claim(ctx, b.TenantID, "fixture", time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			adapter := httpapi.AssistantTools{Legacy: httpapi.InvestigationTools{SP04: &httpapi.SP04Handlers{Pool: repo.Pool}}}
			// Existing immutable Finding outbox is the window authority, even after the
			// mutable aggregate's latest observedAt moves beyond this window.
			event := sp05Envelope(b, uuid.NewString(), uuid.NewString())
			event.ResourceCanonicalID = target
			event.Namespace = namespace
			event.StartsAt = start
			event.ObservedAt = start
			if _, _, err := (finding.Service{Pool: worker}).Ingest(ctx, b, event); err != nil {
				t.Fatal(err)
			}
			event.EventID = uuid.NewString()
			event.IdempotencyKey = event.EventID
			event.SourceSequence++
			event.ObservedAt = now
			event.State = "resolved"
			if _, _, err := (finding.Service{Pool: worker}).Ingest(ctx, b, event); err != nil {
				t.Fatal(err)
			}
			steps := []uuid.UUID{}
			for _, tool := range []string{"resource_history", "resource_changes", "resource_findings"} {
				args, _ := json.Marshal(map[string]any{"canonicalId": target, "windowRole": "fault"})
				step := uuid.New()
				call := investigation.Call{StepID: step, ContextID: uuid.New(), JTI: uuid.NewString(), Name: tool, Arguments: args, ArgsDigest: investigation.ArgumentsDigest(args), Reserve: investigation.Usage{ToolCalls: 1, ResultBytes: 65536}}
				if _, err := jobs.BeginCall(ctx, lease, call); err != nil {
					t.Fatal(err)
				}
				result, err := adapter.Invoke(ctx, j, tool, args)
				if err != nil {
					t.Fatal(tool, err)
				}
				raw, _ := json.Marshal(result)
				if tool == "resource_history" {
					var h investigation.ResourceHistory
					rawData, _ := json.Marshal(result.Data)
					json.Unmarshal(rawData, &h)
					if len(h.Records) != 1 || !h.Records[0].ObservedAt.Equal(start) {
						t.Fatalf("half-open history: %+v", h)
					}
				}
				if tool == "resource_changes" && !strings.Contains(string(raw), "observed_field_change") {
					t.Fatal("missing actual field change", string(raw))
				}
				if tool == "resource_findings" && (!strings.Contains(string(raw), "firing") || strings.Contains(string(raw), `"state":"resolved"`)) {
					t.Fatal("latest aggregate used as historical state", string(raw))
				}
				if _, err := jobs.CompleteToolStep(ctx, lease, step, raw, investigation.Usage{ToolCalls: 1, ResultBytes: int64(len(raw))}); err != nil {
					t.Fatal(err)
				}
				steps = append(steps, step)
			}
			args, _ := json.Marshal(map[string]any{"stepIds": steps})
			evaluation, err := adapter.Invoke(ctx, j, "evaluate_fault_chain", args)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(evaluation.Data)
			var result struct {
				Context investigation.FaultChainContext `json:"context"`
			}
			json.Unmarshal(raw, &result)
			if len(result.Context.SourceSteps) != 3 || len(result.Context.ResourceAssessments) == 0 {
				t.Fatal("missing reference closure", string(raw))
			}
			if _, err := db.ExecContext(ctx, `INSERT INTO platform.resource_observation_coverage(tenant_id,source_id,source_revision,gvr,observed_at,watch_connected,watch_continuous,has_error) VALUES($1,$2,1,'fixture/v1/watch-gap',$3,false,false,true)`, b.TenantID, b.SourceID, start.Add(-500*time.Millisecond)); err != nil {
				t.Fatal(err)
			}
			if err := persistence.WithTenantTx(ctx, worker, b.TenantID, func(tx pgx.Tx) error {
				changes, err := investigation.ReadResourceChangesTx(ctx, tx, j, target, request.Fault)
				if err == nil && (len(changes.Changes) != 1 || changes.Changes[0].Kind != "history_gap") {
					t.Fatal("watch gap promoted a field change", changes)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if kind == "Node" {
				if err := jobs.ExhaustBudget(ctx, lease); err != nil {
					t.Fatal("budget with committed history", err)
				}
			} else if err := jobs.Stop(ctx, b.TenantID, j.JobID, rc.OwnerSubject, false); err != nil {
				t.Fatal("cancel with committed history", err)
			}
			if _, err := repo.ResourceSourceStep(ctx, b.TenantID, turn.TurnID, steps[0], rc.OwnerSubject); err != nil {
				t.Fatal("cancel lost history closure", err)
			}
			if _, err := db.ExecContext(ctx, `UPDATE platform.resource_observations SET retain_until=clock_timestamp()-interval '1 day' WHERE tenant_id=$1`, b.TenantID); err != nil {
				t.Fatal(err)
			}
			if err := persistence.WithTenantTx(ctx, worker, b.TenantID, func(tx pgx.Tx) error {
				var deleted int
				return tx.QueryRow(ctx, `SELECT platform.resource_history_cleanup($1,100)`, b.TenantID).Scan(&deleted)
			}); err != nil {
				t.Fatal("retention function", err)
			}
			var retained int
			if err := db.QueryRowContext(ctx, `SELECT count(*) FROM platform.resource_observations WHERE tenant_id=$1`, b.TenantID).Scan(&retained); err != nil || retained != 2 {
				t.Fatal("source reference closure removed", retained, err)
			}
			if _, err := db.ExecContext(ctx, `UPDATE platform.source_registrations SET status='disabled' WHERE tenant_id=$1 AND source_id=$2`, b.TenantID, b.SourceID); err != nil {
				t.Fatal(err)
			}
			if _, err := repo.ResourceSourceStep(ctx, b.TenantID, turn.TurnID, steps[0], rc.OwnerSubject); err == nil {
				t.Fatal("revoked source remained readable")
			}
		})
	}
}

// Actual Kubernetes objects pass through the production projector, identity
// store and history writer. No current object is backdated into an old window.
func TestAssistantOperationalActualKubernetesObservations(t *testing.T) {
	if os.Getenv("OPS_ASSISTANT_RUNTIME_ACCEPTANCE") != "1" {
		t.Skip("isolated remote Kubernetes required")
	}
	ctx := t.Context()
	namespace := "ops-assistant-deepflow-20261013"
	var pods struct {
		Items []unstructured.Unstructured `json:"items"`
	}
	if err := json.Unmarshal(sp04Kubectl(t, ctx, nil, "-n", namespace, "get", "pods", "-l", "app=fault-chain-client,ops.platform.io/test-release=fault-chain-20261014", "-o", "json"), &pods); err != nil || len(pods.Items) != 1 {
		t.Fatal("actual owned Pod required", err)
	}
	cluster := unstructured.Unstructured{}
	if err := json.Unmarshal(sp04Kubectl(t, ctx, nil, "get", "namespace", "kube-system", "-o", "json"), &cluster); err != nil {
		t.Fatal(err)
	}
	ctx, db, worker, b := sp05Database(t, sp05Seed{Tenant: uuid.New(), Source: uuid.New(), ClusterUID: string(cluster.GetUID()), Namespace: namespace, Backend: "sp05-native"})
	nodeName, _, _ := unstructured.NestedString(pods.Items[0].Object, "spec", "nodeName")
	node := unstructured.Unstructured{}
	if err := json.Unmarshal(sp04Kubectl(t, ctx, nil, "get", "node", nodeName, "-o", "json"), &node); err != nil {
		t.Fatal(err)
	}
	pvcName := "operational-history-" + uuid.NewString()[:8]
	pvc := map[string]any{"apiVersion": "v1", "kind": "PersistentVolumeClaim", "metadata": map[string]any{"name": pvcName, "namespace": namespace, "labels": map[string]any{"ops.platform.io/test-release": "operational-history-v1"}}, "spec": map[string]any{"storageClassName": "", "accessModes": []any{"ReadWriteOnce"}, "resources": map[string]any{"requests": map[string]any{"storage": "1Mi"}}}}
	body, _ := json.Marshal(pvc)
	sp04Kubectl(t, ctx, body, "apply", "-f", "-")
	defer sp04Kubectl(t, ctx, nil, "-n", namespace, "delete", "pvc", pvcName, "--wait=false")
	claim := unstructured.Unstructured{}
	if err := json.Unmarshal(sp04Kubectl(t, ctx, nil, "-n", namespace, "get", "pvc", pvcName, "-o", "json"), &claim); err != nil {
		t.Fatal(err)
	}
	store := resourcestore.Repository{Pool: worker, ExpectedRevision: 1, BackendLogicalID: "sp05-native"}
	for _, o := range []unstructured.Unstructured{node, pods.Items[0], claim} {
		at := time.Now().UTC()
		projected, err := kubernetes.Project(o, at)
		if err != nil {
			t.Fatal(err)
		}
		gvr := kubernetes.GVR{Version: "v1", Resource: map[string]string{"Node": "nodes", "Pod": "pods", "PersistentVolumeClaim": "persistentvolumeclaims"}[o.GetKind()]}
		state := kubernetes.GVRState{LastListCompletedAt: at}
		if err := store.ObserveCoverage(ctx, b.TenantID.String(), b.SourceID.String(), gvr, state); err != nil {
			t.Fatal(err)
		}
		// The fixture's visible projection equals its complete native list.
		snapshot := kubernetes.Snapshot{GVR: gvr, Objects: []unstructured.Unstructured{projected}, State: state}
		if _, err := store.SyncSnapshot(ctx, b.TenantID.String(), b.SourceID.String(), b.ClusterUID, o.GetKind(), "core", snapshot, snapshot, nil); err != nil {
			t.Fatal(err)
		}
		id := resource.CanonicalID{Domain: "k8s", Tenant: b.TenantID.String(), Scope: b.ClusterUID, APIGroup: "core", Kind: o.GetKind(), StableID: string(o.GetUID())}.String()
		var count int
		var coverage string
		if err := db.QueryRowContext(ctx, `SELECT count(*),min(coverage) FROM platform.resource_observations WHERE tenant_id=$1 AND canonical_id=$2 AND observed_at=$3`, b.TenantID, id, at).Scan(&count, &coverage); err != nil || count != 1 || coverage != "initial_list" {
			t.Fatal("actual history receipt", o.GetKind(), count, coverage, err)
		}
		t.Log("actual resource history", o.GetKind(), id, at, coverage)
	}
}
