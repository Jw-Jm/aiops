package investigation

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/graph"
	"ops-platform/internal/persistence"
	"slices"
)

type AssistantTask struct {
	TaskKind              string      `json:"taskKind"`
	AuthorizationDomain   string      `json:"authorizationDomain"`
	ConversationID        uuid.UUID   `json:"conversationId"`
	TurnID                uuid.UUID   `json:"turnId"`
	InputDigest           string      `json:"inputDigest"`
	ContextDigest         string      `json:"contextDigest"`
	GrantSnapshot         graph.Scope `json:"grantSnapshot"`
	GrantSnapshotDigest   string      `json:"grantSnapshotDigest"`
	DatasetCatalogVersion string      `json:"datasetCatalogVersion"`
	DatasetCatalogDigest  string      `json:"datasetCatalogDigest"`
	CatalogVersion        string      `json:"catalogVersion"`
	PromptVersion         string      `json:"promptVersion"`
	ModelProfileVersion   string      `json:"modelProfileVersion"`
	ResultSchemaVersion   string      `json:"resultSchemaVersion"`
}

func (a *AssistantTask) Valid(j Job) bool {
	if a == nil || a.ConversationID == uuid.Nil || a.TurnID == uuid.Nil || a.InputDigest == "" || a.ContextDigest == "" || a.CatalogVersion != "assistant-tools/v1" || a.PromptVersion != "assistant/v1" || a.ModelProfileVersion == "" || a.DatasetCatalogVersion != "analysis-catalog/v1" || a.DatasetCatalogDigest == "" || a.ResultSchemaVersion != "assistant-answer/v1" || a.GrantSnapshotDigest != graph.ScopeDigest(a.GrantSnapshot) {
		return false
	}
	if a.AuthorizationDomain == "audit" {
		return a.TaskKind == "data_query" && j.IncidentID == uuid.Nil && j.TriggerRevision == 0 && j.Scope.Cluster == "" && a.GrantSnapshot.Tenant == j.TenantID.String() && graph.ScopeDigest(j.Scope) == a.GrantSnapshotDigest && !j.Scope.ClusterScoped && len(j.Scope.Resources) == 0 && len(j.Scope.Namespaces) == 0
	}
	if a.AuthorizationDomain != "operational" || !ScopeSubset(a.GrantSnapshot, j.Scope) {
		return false
	}
	return (a.TaskKind == "incident_followup" && j.IncidentID != uuid.Nil && j.TriggerRevision > 0) || (slices.Contains([]string{"data_query", "resource_troubleshooting"}, a.TaskKind) && j.IncidentID == uuid.Nil && j.TriggerRevision == 0)
}
func ScopeSubset(grant, scope graph.Scope) bool { return graph.ContainsScope(grant, scope) }

// AuditScopeTx is independent of Graph/operator scope and locks current admin grants.
func AuditScopeTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, subject string) (graph.Scope, error) {
	out := graph.Scope{Tenant: tenant.String(), Namespaces: []string{}, Resources: []string{}}
	var locked bool
	if err := tx.QueryRow(ctx, `SELECT platform.assistant_lock_audit_authority($1,$2)`, tenant, subject).Scan(&locked); err != nil || !locked {
		return out, ErrDenied
	}
	var revision int64
	if err := tx.QueryRow(ctx, `SELECT revision FROM platform.tenants WHERE tenant_id=$1 AND status='active'`, tenant).Scan(&revision); err != nil {
		return out, ErrDenied
	}
	rows, err := tx.Query(ctx, `SELECT binding_id,revision FROM platform.role_bindings WHERE tenant_id=$1 AND subject=$2 AND role_name='platform_admin' AND status='active' ORDER BY binding_id`, tenant, subject)
	if err != nil {
		return out, err
	}
	versions := []any{revision}
	defer rows.Close()
	for rows.Next() {
		var id uuid.UUID
		var rev int64
		if err := rows.Scan(&id, &rev); err != nil {
			return out, err
		}
		versions = append(versions, id.String(), rev)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	if len(versions) == 1 {
		return out, ErrDenied
	}
	out.AuthorizationRevision = graph.ScopeDigest(versions)
	return out, nil
}
func AuditScope(ctx context.Context, pool persistence.TxBeginner, tenant uuid.UUID, subject string) (graph.Scope, error) {
	var out graph.Scope
	err := persistence.WithTenantTx(ctx, pool, tenant, func(tx pgx.Tx) error { var err error; out, err = AuditScopeTx(ctx, tx, tenant, subject); return err })
	return out, err
}
func currentAssistant(ctx context.Context, tx pgx.Tx, j Job) error {
	if !j.Assistant.Valid(j) || graph.ScopeDigest(j.Scope) != j.EffectiveScopeDigest {
		return ErrDenied
	}
	var grant graph.Scope
	var err error
	if j.Assistant.AuthorizationDomain == "audit" {
		grant, err = AuditScopeTx(ctx, tx, j.TenantID, j.Subject)
	} else {
		grant, err = graph.EffectiveTx(ctx, tx, j.TenantID.String(), j.Subject, j.Scope.Cluster, true)
	}
	if err != nil || graph.ScopeDigest(grant) != j.Assistant.GrantSnapshotDigest {
		return ErrDenied
	}
	var owner, digest, contextDigest string
	if err := tx.QueryRow(ctx, `SELECT c.owner_subject,t.input_digest,t.context_digest FROM investigation.assistant_turns t JOIN investigation.assistant_conversations c USING(tenant_id,conversation_id) WHERE t.tenant_id=$1 AND t.turn_id=$2 AND t.job_id=$3 AND c.conversation_id=$4`, j.TenantID, j.Assistant.TurnID, j.JobID, j.Assistant.ConversationID).Scan(&owner, &digest, &contextDigest); err != nil || owner != j.Subject || digest != j.Assistant.InputDigest || contextDigest != j.Assistant.ContextDigest {
		return ErrDenied
	}
	vid, err := uuid.Parse(j.PolicyVersion)
	if err != nil {
		return ErrDenied
	}
	// The existing definer fences tenant policy for audit with an empty cluster;
	// it grants no operator or cluster authority.
	var valid bool
	if tx.QueryRow(ctx, `SELECT platform.sp06_lock_policy($1,$2,$3,$4)`, j.TenantID, vid, j.Scope.Cluster, j.Scope.Namespaces).Scan(&valid) != nil || !valid {
		return ErrDenied
	}
	return nil
}

// CreateAssistantJobTx shares the caller's short transaction with Message/Turn.
// Deferrable FKs permit the Job and Turn to be inserted atomically in either order.
func CreateAssistantJobTx(ctx context.Context, tx pgx.Tx, j Job) error {
	if j.SchemaVersion != "investigation-job/v3" || !j.Assistant.Valid(j) || !j.Budget.Valid() || j.JobID == uuid.Nil {
		return ErrInvalid
	}
	var incident, revision any
	if j.IncidentID != uuid.Nil {
		incident = j.IncidentID
		revision = j.TriggerRevision
	}
	_, err := tx.Exec(ctx, `INSERT INTO investigation.jobs(tenant_id,job_id,incident_id,trigger_revision,state,schema_version,requesting_subject,trigger_kind,policy_version,effective_scope,effective_scope_digest,tool_catalog_digest,budget,budget_reserved,budget_consumed,expires_at,assistant_binding) VALUES($1,$2,$3,$4,'queued','investigation-job/v3',$5,'manual',$6,$7,$8,$9,$10,$11,$11,$12,$13)`, j.TenantID, j.JobID, incident, revision, j.Subject, j.PolicyVersion, raw(j.Scope), j.EffectiveScopeDigest, j.ToolCatalogDigest, raw(j.Budget), raw(Usage{}), j.ExpiresAt, raw(j.Assistant))
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO investigation.worker_queue(tenant_id,job_id) VALUES($1,$2)`, j.TenantID, j.JobID)
	return err
}
func (r Repository) GetAssistantInput(ctx context.Context, l Lease) (json.RawMessage, error) {
	var out json.RawMessage
	err := r.withLease(ctx, l, func(tx pgx.Tx, j Job) error {
		if j.Assistant == nil {
			return ErrInvalid
		}
		if err := tx.QueryRow(ctx, `SELECT input FROM investigation.assistant_turns WHERE tenant_id=$1 AND turn_id=$2 AND input_digest=$3`, j.TenantID, j.Assistant.TurnID, j.Assistant.InputDigest).Scan(&out); err != nil {
			return err
		}
		if err := ValidateAssistantHistoryTx(ctx, tx, j, out); err != nil {
			return err
		}
		return chargeRead(ctx, tx, j, "assistant-context", int64(len(out)))
	})
	return out, err
}
func (r Repository) completeAssistant(ctx context.Context, l Lease, b []byte) error {
	return r.withLease(ctx, l, func(tx pgx.Tx, j Job) error {
		if j.Assistant == nil || j.Reserved != (Usage{}) {
			return ErrLease
		}
		validated, err := ValidateAssistantAnswerTx(ctx, tx, j, b)
		if err != nil {
			return err
		}
		var answer struct {
			Partial bool `json:"partial"`
		}
		json.Unmarshal(validated, &answer)
		state := "succeeded"
		if answer.Partial {
			state = "partial"
		}
		if _, err := tx.Exec(ctx, `UPDATE investigation.jobs SET state=$3,result=$4 WHERE tenant_id=$1 AND job_id=$2`, j.TenantID, j.JobID, state, validated); err != nil {
			return err
		}
		return event(ctx, tx, j, "result", map[string]any{"state": state, "result": json.RawMessage(validated)})
	})
}

func CheckAssistantJobTx(ctx context.Context, tx pgx.Tx, j Job) error {
	return currentAssistant(ctx, tx, j)
}
func AuthorizeAssistantResultTx(ctx context.Context, tx pgx.Tx, j Job, result []byte) error {
	return authorizeResult(ctx, tx, j, result)
}

// AssistantReadJobTx restores the exact admitted policy and data ceiling for
// history reads. A conversation scope alone cannot stand in for a Job budget.
func AssistantReadJobTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, subject string, conversation, turn uuid.UUID, scope graph.Scope) (Job, error) {
	var id uuid.UUID
	if tx.QueryRow(ctx, `SELECT job_id FROM investigation.assistant_turns WHERE tenant_id=$1 AND conversation_id=$2 AND turn_id=$3`, tenant, conversation, turn).Scan(&id) != nil {
		return Job{}, ErrDenied
	}
	j, err := load(ctx, tx, tenant, id, false)
	if err != nil || j.Assistant == nil || j.Subject != subject || j.Assistant.ConversationID != conversation || graph.ScopeDigest(j.Scope) != graph.ScopeDigest(scope) {
		return Job{}, ErrDenied
	}
	if err = currentAssistant(ctx, tx, j); err != nil {
		return Job{}, err
	}
	return j, nil
}

func AssistantAnswerRetainedTx(ctx context.Context, tx pgx.Tx, tenant, conversation, turn uuid.UUID) error {
	var retained bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM investigation.assistant_messages WHERE tenant_id=$1 AND conversation_id=$2 AND turn_id=$3 AND role='assistant' AND (retain_until>clock_timestamp() OR legal_hold))`, tenant, conversation, turn).Scan(&retained); err != nil {
		return err
	}
	if !retained {
		return ErrResultExpired
	}
	return nil
}
