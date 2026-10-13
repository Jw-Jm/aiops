package investigation

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/analytics"
	"ops-platform/internal/contract"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/resource"
)

type FactRef struct {
	QueryResultID uuid.UUID `json:"queryResultId"`
	FactID        string    `json:"factId"`
}
type QueryFact struct {
	SchemaVersion string  `json:"schemaVersion"`
	FactID        string  `json:"factId"`
	Row           int     `json:"row"`
	Column        string  `json:"column"`
	Value         float64 `json:"value"`
	Unit          string  `json:"unit"`
	Exact         bool    `json:"exact"`
}
type StoredQueryResult struct {
	QueryResultID uuid.UUID             `json:"queryResultId"`
	Result        analytics.QueryResult `json:"result"`
	Facts         []QueryFact           `json:"facts"`
	ContentDigest string                `json:"contentDigest"`
}
type Hypothesis struct {
	Key             string   `json:"key"`
	Summary         string   `json:"summary"`
	Supports        []string `json:"supports"`
	Conflicts       []string `json:"conflicts"`
	MissingEvidence []string `json:"missingEvidence"`
	NextChecks      []string `json:"nextChecks"`
}
type Clarification struct {
	ClarificationID uuid.UUID `json:"clarificationId"`
	Question        string    `json:"question"`
	MissingFields   []string  `json:"missingFields"`
	Candidates      []string  `json:"candidates"`
}
type AssistantAnswer struct {
	FormalRefs      []FormalReference `json:"formalRefs,omitempty"`
	FaultChain      []ChainLink       `json:"faultChain,omitempty"`
	SchemaVersion   string            `json:"schemaVersion"`
	AnswerKind      string            `json:"answerKind"`
	Summary         string            `json:"summary"`
	FactRefs        []FactRef         `json:"factRefs"`
	QueryResultRefs []uuid.UUID       `json:"queryResultRefs"`
	EvidenceRefs    []string          `json:"evidenceRefs"`
	EntityRefs      []string          `json:"entityRefs"`
	MissingEvidence []string          `json:"missingEvidence"`
	ScopeSummary    string            `json:"scopeSummary"`
	AsOf            time.Time         `json:"asOf"`
	Partial         bool              `json:"partial"`
	DegradedSources []string          `json:"degradedSources"`
	Warnings        []string          `json:"warnings"`
	Hypotheses      []Hypothesis      `json:"hypotheses"`
	NextChecks      []string          `json:"nextChecks"`
	Clarification   *Clarification    `json:"clarification,omitempty"`
}

// Query result IDs are platform generated. Facts locate immutable, server computed
// cells; a model cannot supply a replacement value or an arbitrary calculation.
func BuildStoredQueryResult(result analytics.QueryResult) StoredQueryResult {
	out := StoredQueryResult{QueryResultID: uuid.Must(uuid.NewV7()), Result: result, Facts: []QueryFact{}}
	for row, value := range result.Rows {
		out.Facts = append(out.Facts, QueryFact{"query-fact/v1", graph.ScopeDigest(struct {
			Plan string
			Row  int
		}{result.PlanDigest, row}), row, "value", value.Value, result.Unit, result.Exact})
	}
	out.ContentDigest = graph.ScopeDigest(out)
	return out
}
func validateStoredQueryResult(out StoredQueryResult) error {
	digest := out.ContentDigest
	out.ContentDigest = ""
	if digest == "" || graph.ScopeDigest(out) != digest || out.QueryResultID == uuid.Nil || len(out.Facts) != len(out.Result.Rows) {
		return ErrInvalid
	}
	if contract.Validate("https://ops.local/schemas/query-result/v1", raw(out.Result)) != nil {
		return ErrInvalid
	}
	for n, f := range out.Facts {
		if f.SchemaVersion != "query-fact/v1" || f.Row != n || f.Column != "value" || f.Value != out.Result.Rows[n].Value || f.Unit != out.Result.Unit || f.Exact != out.Result.Exact || f.FactID != graph.ScopeDigest(struct {
			Plan string
			Row  int
		}{out.Result.PlanDigest, n}) {
			return ErrInvalid
		}
	}
	return nil
}
func loadQueryResultTx(ctx context.Context, tx pgx.Tx, j Job, id uuid.UUID) (StoredQueryResult, error) {
	var out StoredQueryResult
	var b, scopeRaw []byte
	var owner string
	var conversation uuid.UUID
	err := tx.QueryRow(ctx, `SELECT q.result,q.scope,q.owner_subject,t.conversation_id FROM investigation.assistant_query_results q JOIN investigation.assistant_turns t USING(tenant_id,turn_id) WHERE q.tenant_id=$1 AND q.query_result_id=$2 AND (q.retain_until>clock_timestamp() OR q.legal_hold)`, j.TenantID, id).Scan(&b, &scopeRaw, &owner, &conversation)
	if err != nil {
		return out, ErrDenied
	}
	var scope graph.Scope
	if json.Unmarshal(b, &out) != nil || json.Unmarshal(scopeRaw, &scope) != nil || validateStoredQueryResult(out) != nil {
		return out, ErrInvalid
	}
	if j.Assistant == nil || owner != j.Subject || conversation != j.Assistant.ConversationID || !ScopeSubset(j.Scope, scope) || out.Result.ScopeDigest != graph.ScopeDigest(scope) {
		return out, ErrDenied
	}
	if out.Result.DatasetID == "resources.current" {
		// A changed graph owner invalidates replay, even if the subject still has rights.
		var epoch int64
		if tx.QueryRow(ctx, `SELECT owner_epoch FROM platform.graph_ownership WHERE tenant_id=$1 AND cluster_uid=$2 AND route_expires_at>clock_timestamp()`, j.TenantID, j.Scope.Cluster).Scan(&epoch) != nil || epoch != out.Result.GraphRevision.OwnerEpoch {
			return out, ErrDenied
		}
	}
	if manifest := out.Result.SourceManifest; manifest != nil {
		var source evidence.Binding
		var mapping []byte
		if tx.QueryRow(ctx, `SELECT source_type,revision,backend_logical_id,data_scope_mapping FROM platform.source_registrations WHERE tenant_id=$1 AND source_id=$2 AND status='active'`, j.TenantID, manifest.SourceID).Scan(&source.SourceType, &source.Revision, &source.BackendLogicalID, &mapping) != nil || json.Unmarshal(mapping, &source.ScopeMapping) != nil || source.Revision != manifest.Revision || evidence.BindingScopeDigest(source) != manifest.BindingDigest {
			return out, ErrDenied
		}
	}
	return out, nil
}

// ValidateAssistantAnswerTx accepts only references from committed tool results.
// It has no incident mutation, RCA confirmation or execution authority.
func ValidateAssistantAnswerTx(ctx context.Context, tx pgx.Tx, j Job, b []byte) ([]byte, error) {
	var a AssistantAnswer
	if len(b) > 64<<10 || contract.Validate("https://ops.local/schemas/assistant-answer/v1", b) != nil || strictJSON(b, &a) != nil || j.Assistant == nil {
		return nil, ErrInvalid
	}
	if (j.Assistant.TaskKind == "data_query" && a.AnswerKind == "diagnosis") || (j.Assistant.TaskKind != "data_query" && a.AnswerKind == "data") {
		return nil, ErrInvalid
	}
	availableEvidence := map[string]bool{}
	availableFormal := map[string]bool{}
	availableRelations := map[string]resource.Relation{}
	availableQueries := map[uuid.UUID]bool{}
	availableEntities := map[string]bool{}
	freshReferences := map[string]bool{}
	var historicalAsOf time.Time
	rows, err := tx.Query(ctx, `SELECT tool_name,result FROM investigation.steps WHERE tenant_id=$1 AND job_id=$2 AND state='succeeded' AND tool_name<>'model'`, j.TenantID, j.JobID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var rawResult []byte
		var toolName string
		if rows.Scan(&toolName, &rawResult) != nil {
			rows.Close()
			return nil, ErrInvalid
		}
		var v struct {
			Tool         string   `json:"tool"`
			EvidenceRefs []string `json:"evidenceRefs"`
			Data         struct {
				QueryResultRefs []uuid.UUID `json:"queryResultRefs"`
				History         []struct {
					Role    string          `json:"role"`
					Content json.RawMessage `json:"content"`
				} `json:"history"`
			} `json:"data"`
		}
		if json.Unmarshal(rawResult, &v) != nil {
			rows.Close()
			return nil, ErrInvalid
		}
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		if v.Tool != toolName || json.Unmarshal(rawResult, &envelope) != nil {
			rows.Close()
			return nil, ErrInvalid
		}
		for digest, relation := range committedAssistantRelations(toolName, envelope.Data) {
			availableRelations[digest] = relation
			freshReferences[digest] = true
		}
		if toolName == "get_formal_references" {
			var formal struct {
				Refs []FormalReference `json:"formalReferences"`
			}
			if json.Unmarshal(envelope.Data, &formal) != nil {
				rows.Close()
				return nil, ErrInvalid
			}
			for _, ref := range formal.Refs {
				availableFormal[graph.ScopeDigest(ref)] = true
				freshReferences[graph.ScopeDigest(ref)] = true
			}
		}
		for _, id := range committedAssistantEntities(toolName, envelope.Data) {
			availableEntities[id] = true
			if toolName != "get_assistant_context" {
				freshReferences[id] = true
			}
		}
		for _, ref := range v.EvidenceRefs {
			availableEvidence[ref] = true
			freshReferences[ref] = true
		}
		for _, ref := range v.Data.QueryResultRefs {
			availableQueries[ref] = true
			freshReferences[ref.String()] = true
		}
		if v.Tool == "get_assistant_context" {
			for _, m := range v.Data.History {
				if m.Role != "assistant" {
					continue
				}
				var prior AssistantAnswer
				if json.Unmarshal(m.Content, &prior) != nil {
					rows.Close()
					return nil, ErrInvalid
				}
				if prior.AsOf.After(historicalAsOf) {
					historicalAsOf = prior.AsOf
				}
				for _, ref := range prior.FormalRefs {
					availableFormal[graph.ScopeDigest(ref)] = true
				}
				for _, link := range prior.FaultChain {
					availableRelations[link.RelationDigest] = resource.Relation{From: link.From, To: link.To, Kind: link.Kind}
				}
				for _, ref := range prior.QueryResultRefs {
					availableQueries[ref] = true
				}
				for _, ref := range prior.EvidenceRefs {
					availableEvidence[ref] = true
				}
				for _, ref := range prior.EntityRefs {
					availableEntities[ref] = true
				}
			}
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, ref := range a.EvidenceRefs {
		id, err := uuid.Parse(ref)
		if err != nil || !availableEvidence[ref] || seen[ref] {
			return nil, ErrDenied
		}
		seen[ref] = true
		ev, err := evidence.GetTx(ctx, tx, j.TenantID, id, j.Scope)
		if err != nil || !slices.Contains(j.Budget.AllowedDataClasses, ev.DataClass) || (!ev.SourceRetentionUntil.After(time.Now()) && ev.ReplayState != "archived_verified") {
			return nil, ErrDenied
		}
		if !ev.TimeReliable {
			a.Partial = true
			a.Warnings = append(a.Warnings, "unreliable-event-time")
		}
		if err := evidence.Protect(ctx, tx, j.TenantID, id, j.JobID, "investigation", time.Now().Add(365*24*time.Hour), true); err != nil {
			return nil, err
		}
	}
	queries := map[uuid.UUID]StoredQueryResult{}
	for _, ref := range a.QueryResultRefs {
		if !availableQueries[ref] {
			return nil, ErrDenied
		}
		if _, exists := queries[ref]; exists {
			return nil, ErrInvalid
		}
		q, err := loadQueryResultTx(ctx, tx, j, ref)
		if err != nil {
			return nil, err
		}
		queries[ref] = q
		if !q.Result.Exact {
			a.Partial = true
		}
	}
	facts := map[FactRef]bool{}
	for _, ref := range a.FactRefs {
		q, ok := queries[ref.QueryResultID]
		if !ok || facts[ref] {
			return nil, ErrDenied
		}
		found := false
		for _, f := range q.Facts {
			if f.FactID == ref.FactID {
				found = true
			}
		}
		if !found {
			return nil, ErrDenied
		}
		facts[ref] = true
	}
	if a.AnswerKind == "data" && len(facts) == 0 {
		return nil, ErrDenied
	}
	for _, id := range a.EntityRefs {
		var ns string
		if !availableEntities[id] || tx.QueryRow(ctx, `SELECT namespace FROM platform.resource_entities WHERE tenant_id=$1 AND canonical_id=$2 AND deleted_at IS NULL`, j.TenantID, id).Scan(&ns) != nil || !j.Scope.Allows(id, ns) {
			return nil, ErrDenied
		}
	}
	for _, h := range a.Hypotheses {
		if validateSupportConflict(h.Supports, h.Conflicts, seen) != nil {
			return nil, ErrDenied
		}
	}
	if validateFaultChain(a, availableRelations, seen) != nil {
		return nil, ErrDenied
	}
	for _, ref := range a.FormalRefs {
		if !availableFormal[graph.ScopeDigest(ref)] || AuthorizeFormalReferenceTx(ctx, tx, j, ref) != nil {
			return nil, ErrDenied
		}
	}
	if a.AnswerKind == "clarification" {
		if a.Clarification == nil {
			return nil, ErrInvalid
		}
		a.Clarification.ClarificationID = j.Assistant.TurnID
	} else {
		// Strict provider schemas make optional objects nullable. Only a
		// clarification answer owns a clarification handle; discard unused
		// model-supplied options rather than exposing an invalid follow-up.
		a.Clarification = nil
	}
	a.AsOf = assistantObservationTime(a, freshReferences, historicalAsOf, time.Now().UTC())
	a.ScopeSummary = j.Assistant.AuthorizationDomain + ":" + j.Scope.Cluster
	validated := raw(a)
	if contract.Validate("https://ops.local/schemas/assistant-answer/v1", validated) != nil {
		return nil, ErrInvalid
	}
	return validated, nil
}

// persistAssistantQueryTx is called by tool settlement under the current fence.
// Failed, timed out or cancelled tools never leave a reusable result behind.
func persistAssistantQueryTx(ctx context.Context, tx pgx.Tx, j Job, stepID uuid.UUID, result []byte) error {
	var envelope struct {
		Tool string `json:"tool"`
		Data struct {
			Stored          *StoredQueryResult `json:"storedQueryResult"`
			QueryResultRefs []uuid.UUID        `json:"queryResultRefs"`
			Scope           *graph.Scope       `json:"scope"`
		} `json:"data"`
	}
	if json.Unmarshal(result, &envelope) != nil {
		return ErrInvalid
	}
	if envelope.Data.Stored == nil {
		if len(envelope.Data.QueryResultRefs) > 0 {
			return authorizeResult(ctx, tx, j, result)
		}
		return nil
	}
	out := envelope.Data.Stored
	var stepName string
	if tx.QueryRow(ctx, `SELECT tool_name FROM investigation.steps WHERE tenant_id=$1 AND job_id=$2 AND step_id=$3`, j.TenantID, j.JobID, stepID).Scan(&stepName) != nil || stepName != "execute_analysis_query" {
		return ErrDenied
	}
	if j.Assistant == nil || envelope.Tool != "execute_analysis_query" || envelope.Data.Scope == nil || validateStoredQueryResult(*out) != nil || !ScopeSubset(j.Scope, *envelope.Data.Scope) || out.Result.ScopeDigest != graph.ScopeDigest(*envelope.Data.Scope) || out.Result.CatalogDigest != j.Assistant.DatasetCatalogDigest || len(envelope.Data.QueryResultRefs) != 1 || envelope.Data.QueryResultRefs[0] != out.QueryResultID {
		return ErrDenied
	}
	manifest := raw(map[string]any{"graphRevision": out.Result.GraphRevision, "controlPlaneSnapshot": out.Result.ControlPlaneSnapshot, "sourceManifest": out.Result.SourceManifest})
	_, err := tx.Exec(ctx, `INSERT INTO investigation.assistant_query_results(tenant_id,query_result_id,turn_id,owner_subject,plan_digest,result,scope,source_manifest) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(tenant_id,query_result_id) DO NOTHING`, j.TenantID, out.QueryResultID, j.Assistant.TurnID, j.Subject, out.Result.PlanDigest, raw(out), raw(envelope.Data.Scope), manifest)
	if err != nil {
		return err
	}
	stored, err := loadQueryResultTx(ctx, tx, j, out.QueryResultID)
	if err != nil || stored.ContentDigest != out.ContentDigest || stored.Result.PlanDigest != out.Result.PlanDigest {
		return ErrDenied
	}
	return authorizeResult(ctx, tx, j, result)
}
func ReadAssistantQueryByPlanTx(ctx context.Context, tx pgx.Tx, j Job, digest string) (StoredQueryResult, error) {
	if j.Assistant == nil {
		return StoredQueryResult{}, ErrDenied
	}
	var id uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT query_result_id FROM investigation.assistant_query_results WHERE tenant_id=$1 AND turn_id=$2 AND plan_digest=$3 AND owner_subject=$4`, j.TenantID, j.Assistant.TurnID, digest, j.Subject).Scan(&id); err != nil {
		return StoredQueryResult{}, err
	}
	return loadQueryResultTx(ctx, tx, j, id)
}
func ReadAssistantQueryResultTx(ctx context.Context, tx pgx.Tx, j Job, id uuid.UUID) (StoredQueryResult, error) {
	return loadQueryResultTx(ctx, tx, j, id)
}

// Input history is untrusted text, but its references are platform manifests.
// Both Job API and MCP revalidate them before prompt disclosure or recovery.
func ValidateAssistantHistoryTx(ctx context.Context, tx pgx.Tx, j Job, input []byte) error {
	var v struct {
		History []struct {
			TurnID  uuid.UUID       `json:"turnId"`
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"history"`
	}
	if json.Unmarshal(input, &v) != nil || len(v.History) > 10 {
		return ErrInvalid
	}
	for _, message := range v.History {
		if j.Assistant == nil {
			return ErrDenied
		}
		priorJob, err := AssistantReadJobTx(ctx, tx, j.TenantID, j.Subject, j.Assistant.ConversationID, message.TurnID, j.Scope)
		if err != nil {
			return err
		}
		if message.Role == "assistant" {
			if j.Assistant == nil {
				return ErrDenied
			}
			if err := AssistantAnswerRetainedTx(ctx, tx, j.TenantID, j.Assistant.ConversationID, message.TurnID); err != nil {
				return err
			}
			if err := authorizeResult(ctx, tx, priorJob, message.Content); err != nil {
				return err
			}
		} else if message.Role != "user" {
			return ErrInvalid
		}
	}
	return nil
}

// Context loading does not refresh observations from prior answers. Only a
// committed data/graph/formal read supplying a cited reference may do that.
func assistantObservationTime(a AssistantAnswer, fresh map[string]bool, historical, now time.Time) time.Time {
	references := []string{}
	references = append(references, a.EvidenceRefs...)
	references = append(references, a.EntityRefs...)
	for _, ref := range a.QueryResultRefs {
		references = append(references, ref.String())
	}
	for _, ref := range a.FormalRefs {
		references = append(references, graph.ScopeDigest(ref))
	}
	for _, link := range a.FaultChain {
		references = append(references, link.RelationDigest)
	}
	if len(references) > 0 && !historical.IsZero() {
		for _, ref := range references {
			if fresh[ref] {
				return now
			}
		}
		return historical
	}
	return now
}
