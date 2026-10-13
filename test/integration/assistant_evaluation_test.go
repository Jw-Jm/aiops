package integration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"ops-platform/internal/analytics"
	"ops-platform/internal/assistant"
	"ops-platform/internal/auth"
	"ops-platform/internal/graph"
	"ops-platform/internal/modelprofile"
	"ops-platform/internal/resource"
	"ops-platform/internal/source"
)

type assistantEvaluationCase struct {
	ID           string   `json:"id"`
	Group        string   `json:"group"`
	Namespace    string   `json:"namespace"`
	Timezone     string   `json:"timezone"`
	Messages     []string `json:"messages"`
	Dataset      string   `json:"dataset"`
	Metric       string   `json:"metric"`
	Dimension    string   `json:"dimension"`
	Unit         string   `json:"unit"`
	Task         string   `json:"task"`
	Kind         string   `json:"kind"`
	Health       string   `json:"health"`
	Domain       string   `json:"domain"`
	Adapter      string   `json:"adapter"`
	Availability string   `json:"availability"`
	Fixture      struct {
		Count         int `json:"count"`
		ExcludedCount int `json:"excludedCount"`
	} `json:"fixture"`
}

// Evaluation completion and quality admission are separate. Failed turns are
// durable observations, not t.Skip or passing quality cases. The offline scorer
// exits nonzero unless all five cases, machine checks and manual review pass.
func TestAssistantChineseEvaluation(t *testing.T) {
	if os.Getenv("OPS_ASSISTANT_EVALUATION") != "1" {
		t.Skip("explicit isolated Chinese evaluation required")
	}
	suitePath := os.Getenv("OPS_ASSISTANT_EVALUATION_SUITE")
	if suitePath == "" {
		suitePath = "../fixtures/assistant/evaluation/acceptance-v1.json"
	}
	raw, err := os.ReadFile(suitePath)
	if err != nil {
		t.Fatal(err)
	}
	var suite struct {
		Version string                    `json:"version"`
		Cases   []assistantEvaluationCase `json:"cases"`
	}
	if json.Unmarshal(raw, &suite) != nil || len(suite.Cases) != 5 {
		t.Fatal("frozen five-case suite required")
	}
	sum := sha256.Sum256(raw)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	outPath := os.Getenv("OPS_ASSISTANT_EVALUATION_OUTPUT")
	if outPath == "" || !filepath.IsAbs(outPath) {
		t.Fatal("absolute owned output path required")
	}
	out, err := os.OpenFile(outPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	encoder := json.NewEncoder(out)
	repeats := 1
	if v := os.Getenv("OPS_ASSISTANT_EVALUATION_REPEATS"); v != "" {
		repeats, err = strconv.Atoi(v)
		if err != nil || repeats != 1 {
			t.Fatal("five-case run requires one repeat")
		}
	}
	assistantRealRuntime(t, true, func(ctx context.Context, db *sql.DB, pool *pgxpool.Pool, b source.BoundSourceContext, repo assistant.Repository, profile modelprofile.Profile, trace *assistantEvaluationTrace) {
		namespaces := []map[string]string{}
		for _, c := range suite.Cases {
			for r := 1; r <= repeats; r++ {
				namespaces = append(namespaces, map[string]string{"clusterId": b.ClusterID.String(), "namespace": fmt.Sprintf("%s-r%d", c.Namespace, r)})
			}
		}
		nsRaw, _ := json.Marshal(namespaces)
		if _, err := db.ExecContext(ctx, `INSERT INTO platform.role_bindings(tenant_id,binding_id,subject,role_name,cluster_scopes,namespace_scopes) VALUES($1,$2,'assistant-eval-operator','operator',$3,$4)`, b.TenantID, uuid.New(), `["`+b.ClusterID.String()+`"]`, nsRaw); err != nil {
			t.Fatal(err)
		}
		for repeat := 1; repeat <= repeats; repeat++ {
			for _, c := range suite.Cases {
				c.Namespace = fmt.Sprintf("%s-r%d", c.Namespace, repeat)
				start := time.Now()
				observation := map[string]any{"schemaVersion": "assistant-evaluation-observation/v1", "suiteVersion": suite.Version, "suiteDigest": digest, "caseId": c.ID, "repeat": repeat, "fixtureOnly": true, "profileReference": profile.Reference(), "modelID": profile.ModelID, "runState": "completed", "turns": []any{}}
				if c.Adapter == "symptom" {
					parsed, err := assistant.ParseSymptom(c.Messages[0])
					observation["symptomIntent"] = parsed
					if err != nil {
						observation["runState"] = "fixture_error"
					}
				} else {
					actor := auth.RequestContext{TenantID: b.TenantID, Subject: "assistant-eval-operator", Roles: []auth.Role{auth.Operator}, ClusterScopes: []uuid.UUID{b.ClusterID}, RequestID: uuid.NewString()}
					domain := c.Domain
					if domain == "" {
						domain = "operational"
					}
					if domain == "audit" {
						actor.Subject = "sp06-admin"
						actor.Roles = []auth.Role{auth.PlatformAdmin}
					}
					requestCtx := auth.WithRequestContext(ctx, actor)
					conversation := assistant.Conversation{TenantID: b.TenantID, OwnerSubject: actor.Subject, Title: c.ID, Mode: "data_query", AuthorizationDomain: domain, ClusterUID: b.ClusterUID, Binding: assistant.ConversationBinding{Timezone: c.Timezone, Selection: analytics.Selection{Namespaces: []string{c.Namespace}}}}
					if domain == "audit" {
						conversation.ClusterUID = ""
						conversation.Binding.Selection = analytics.Selection{}
					}
					anchor := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Second)
					count := c.Fixture.Count
					assistantEvaluationSeed(t, ctx, db, b, c, anchor, actor.Subject)
					if c.Task != "" {
						conversation.Mode = "troubleshoot"
						if c.Kind == "Node" {
							conversation.Binding.Selection = analytics.Selection{IncludeClusterScoped: true}
						}
						id := resource.CanonicalID{Domain: "k8s", Tenant: b.TenantID.String(), Scope: b.ClusterUID, APIGroup: "core", Kind: c.Kind, StableID: c.ID + "-" + c.Namespace}.String()
						if c.Task == "incident_followup" {
							incidentID := uuid.New()
							conversation.Binding.IncidentID = incidentID
							if _, err := db.ExecContext(ctx, `INSERT INTO incident.records(tenant_id,incident_id,state,revision,cluster_uid,namespace,resource_canonical_id) VALUES($1,$2,'open',1,$3,$4,$5)`, b.TenantID, incidentID, b.ClusterUID, entityNamespace(c), id); err != nil {
								t.Fatal(err)
							}
						} else {
							grant, err := (graph.Authorization{Pool: pool}).Effective(ctx, b.TenantID.String(), actor.Subject, b.ClusterUID)
							if err != nil {
								t.Fatal(err)
							}
							scope := analytics.CloneScope(grant)
							scope.Namespaces = []string{c.Namespace}
							scope.Resources = []string{id}
							scope.ClusterScoped = c.Kind == "Node"
							if c.Kind == "Node" {
								scope.Namespaces = []string{}
							}
							rc := assistant.ResourceContext{SchemaVersion: "resource-investigation-context/v1", ContextID: uuid.New(), TenantID: b.TenantID.String(), OwnerSubject: actor.Subject, ClusterUID: b.ClusterUID, Resources: []assistant.Candidate{{CanonicalID: id, Kind: c.Kind, Name: "eval-target", Namespace: entityNamespace(c), ObservedAt: anchor}}, Symptom: c.Messages[0], TimeRange: assistant.TimeRange{From: anchor.Add(-10 * time.Minute), To: anchor, Timezone: c.Timezone}, ResolvedAt: anchor, ObservationRevision: graph.Revision{OwnerEpoch: 1, GraphGeneration: 1}, GrantSnapshotDigest: graph.ScopeDigest(grant), ExecutionScope: scope, ExecutionScopeDigest: graph.ScopeDigest(scope)}
							rc.ContextDigest = graph.ScopeDigest(rc)
							conversation.Binding.ResourceContext = &rc
						}
					}
					var incidentBefore int
					if err := db.QueryRowContext(ctx, `SELECT count(*) FROM incident.records WHERE tenant_id=$1`, b.TenantID).Scan(&incidentBefore); err != nil {
						t.Fatal(err)
					}
					observation["incidentCountBefore"] = incidentBefore
					created, err := repo.Create(requestCtx, conversation)
					if err != nil {
						observation["runState"] = "admission_error"
						observation["errorCode"] = err.Error()
					} else {
						currentRevision := created.Revision
						turns := []any{}
						var parent *uuid.UUID
						for index, message := range c.Messages {
							turn, err := repo.Submit(requestCtx, b.TenantID, created.ConversationID, actor.Subject, fmt.Sprintf("%s-%d-%d", c.ID, repeat, index), assistant.SubmitMessage{Message: message, ExpectedConversationRevision: currentRevision, ParentTurnID: parent})
							if err != nil {
								observation["runState"] = "submission_error"
								observation["errorCode"] = err.Error()
								break
							}
							currentRevision = turn.ConversationRevision
							deadline := time.Now().Add(150 * time.Second)
							for time.Now().Before(deadline) && ctx.Err() == nil {
								turn, err = repo.Turn(requestCtx, b.TenantID, turn.TurnID, actor.Subject)
								if err != nil || (turn.State != "queued" && turn.State != "running") {
									break
								}
								time.Sleep(250 * time.Millisecond)
							}
							if turn.State == "queued" || turn.State == "running" {
								_ = repo.Cancel(requestCtx, b.TenantID, turn.TurnID, actor.Subject, c.ID+"-evaluation-deadline")
								observation["runState"] = "timeout"
							}
							if err != nil {
								observation["runState"] = "read_error"
							}
							var input assistant.TurnInput
							var inputRaw []byte
							if err := db.QueryRowContext(ctx, `SELECT input FROM investigation.assistant_turns WHERE tenant_id=$1 AND turn_id=$2`, b.TenantID, turn.TurnID).Scan(&inputRaw); err != nil || json.Unmarshal(inputRaw, &input) != nil {
								t.Fatal("frozen input unavailable", err)
							}
							steps := []map[string]any{}
							rows, err := db.QueryContext(ctx, `SELECT step_id,tool_name,state,args_digest,result,error_code FROM investigation.steps WHERE tenant_id=$1 AND job_id=$2 AND tool_name<>'model' ORDER BY started_at,step_id`, b.TenantID, turn.JobID)
							if err != nil {
								t.Fatal(err)
							}
							for rows.Next() {
								var id, name, state, argsDigest, code string
								var result []byte
								if err := rows.Scan(&id, &name, &state, &argsDigest, &result, &code); err != nil {
									t.Fatal(err)
								}
								step := map[string]any{"stepId": id, "toolName": name, "state": state, "errorCode": code}
								if name != "get_assistant_context" && len(result) > 0 {
									step["result"] = json.RawMessage(result)
								}
								trace.mu.Lock()
								if intent, ok := trace.intents[id]; ok && trace.digests[id] == argsDigest {
									step["intent"] = intent
								}
								trace.mu.Unlock()
								steps = append(steps, step)
							}
							if err := rows.Err(); err != nil {
								t.Fatal(err)
							}
							rows.Close()
							item := map[string]any{"turnId": turn.TurnID, "jobId": turn.JobID, "state": turn.State, "errorCode": turn.ErrorCode, "requestedAt": input.RequestedAt, "requestedTimeRange": input.RequestedTimeRange, "steps": steps, "goldenCount": count, "expectedNamespace": c.Namespace, "fixtureObservedAt": anchor, "referenceResolution": input.ReferenceResolution, "bindingTimeRange": input.Binding.ResourceContext}
							bound := []string{}
							if input.Binding.ResourceContext != nil {
								for _, r := range input.Binding.ResourceContext.Resources {
									bound = append(bound, r.CanonicalID)
								}
							}
							item["boundEntityIds"] = bound
							if len(turn.Answer) > 0 {
								item["answer"] = json.RawMessage(turn.Answer)
							}
							turns = append(turns, item)
							parent = &turn.TurnID
							if observation["runState"] != "completed" {
								break
							}
						}
						observation["turns"] = turns
						var incidentAfter int
						if err := db.QueryRowContext(ctx, `SELECT count(*) FROM incident.records WHERE tenant_id=$1`, b.TenantID).Scan(&incidentAfter); err != nil {
							t.Fatal(err)
						}
						observation["incidentCountAfter"] = incidentAfter
					}
				}
				observation["elapsedMilliseconds"] = time.Since(start).Milliseconds()
				if err := encoder.Encode(observation); err != nil {
					t.Fatal(err)
				}
				if err := out.Sync(); err != nil {
					t.Fatal(err)
				}
				t.Logf("evaluation repeat=%d case=%s group=%s runState=%s elapsed=%s", repeat, c.ID, c.Group, observation["runState"], time.Since(start).Round(time.Millisecond))
			}
		}
	})
}

// Independent fixture arithmetic: three in-scope records and two out-of-scope
// decoys. No analytic compiler/executor is used to calculate the golden value.
func assistantEvaluationSeed(t *testing.T, ctx context.Context, db *sql.DB, b source.BoundSourceContext, c assistantEvaluationCase, anchor time.Time, subject string) {
	t.Helper()
	kind := c.Kind
	if kind == "" {
		kind = "Pod"
	}
	id := resource.CanonicalID{Domain: "k8s", Tenant: b.TenantID.String(), Scope: b.ClusterUID, APIGroup: "core", Kind: kind, StableID: c.ID + "-" + c.Namespace}.String()
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.resource_entities(tenant_id,canonical_id,cluster_id,kind,namespace,name,metadata,observed_at) VALUES($1,$2,$3,$4,$5,'eval-target','{}',$6)`, b.TenantID, id, b.ClusterID, kind, entityNamespace(c), anchor); err != nil {
		t.Fatal(err)
	}
	if c.Dataset == "incidents.records" {
		for _, fixture := range []struct {
			namespace string
			count     int
		}{{c.Namespace, c.Fixture.Count}, {"outside-eval", c.Fixture.ExcludedCount}} {
			for i := 0; i < fixture.count; i++ {
				if _, err := db.ExecContext(ctx, `INSERT INTO incident.records(tenant_id,incident_id,state,cluster_uid,namespace,created_at) VALUES($1,$2,'resolved',$3,$4,$5)`, b.TenantID, uuid.New(), b.ClusterUID, fixture.namespace, anchor); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

func entityNamespace(c assistantEvaluationCase) string {
	if c.Kind == "Node" {
		return ""
	}
	return c.Namespace
}
