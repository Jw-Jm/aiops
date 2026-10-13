package investigation

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"time"
)

// Ambiguous pronouns are answered by the platform under the worker lease, with
// no model call or data tool reads. The ordinary persisted answer path is used.
func (r Repository) PlatformClarification(ctx context.Context, l Lease) ([]byte, error) {
	var answer []byte
	err := r.withLease(ctx, l, func(tx pgx.Tx, j Job) error {
		if j.Assistant == nil {
			return nil
		}
		var input []byte
		if tx.QueryRow(ctx, `SELECT input FROM investigation.assistant_turns WHERE tenant_id=$1 AND turn_id=$2 AND input_digest=$3`, j.TenantID, j.Assistant.TurnID, j.Assistant.InputDigest).Scan(&input) != nil {
			return ErrDenied
		}
		var parsed struct {
			Reference *struct {
				Status     string   `json:"status"`
				Candidates []string `json:"candidates"`
			} `json:"referenceResolution"`
		}
		if json.Unmarshal(input, &parsed) != nil {
			return ErrInvalid
		}
		if parsed.Reference == nil || parsed.Reference.Status != "clarification" {
			return nil
		}
		if ValidateAssistantHistoryTx(ctx, tx, j, input) != nil {
			return ErrDenied
		}
		a := AssistantAnswer{SchemaVersion: "assistant-answer/v1", AnswerKind: "clarification", Summary: "资源指代不明确，请确认本轮目标。", FactRefs: []FactRef{}, QueryResultRefs: []uuid.UUID{}, EvidenceRefs: []string{}, EntityRefs: []string{}, MissingEvidence: []string{}, ScopeSummary: j.Assistant.AuthorizationDomain + ":" + j.Scope.Cluster, AsOf: time.Now().UTC(), DegradedSources: []string{}, Warnings: []string{}, Hypotheses: []Hypothesis{}, NextChecks: []string{}, Clarification: &Clarification{ClarificationID: j.Assistant.TurnID, Question: "“它/该资源”具体指哪个资源？请选择已验证身份，或明确填写资源名称和类型。", MissingFields: []string{"referentCanonicalId"}, Candidates: parsed.Reference.Candidates}}
		if err := chargeRead(ctx, tx, j, "assistant-clarification", int64(len(input))); err != nil {
			return err
		}
		answer = raw(a)
		return nil
	})
	return answer, err
}
