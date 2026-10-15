package httpapi

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/analytics"
	"ops-platform/internal/assistant"
	"ops-platform/internal/evidence"
	"ops-platform/internal/finding"
	"ops-platform/internal/graph"
	"ops-platform/internal/investigation"
	"ops-platform/internal/investigation/tools"
	"ops-platform/internal/persistence"
)

func resourceWindow(input assistant.TurnInput, j investigation.Job, role string) (analytics.QueryTimeRange, error) {
	r, err := faultChainEvaluationRequest(input, j)
	if err != nil {
		return analytics.QueryTimeRange{}, err
	}
	var w *analytics.QueryTimeRange
	switch role {
	case "fault":
		w = &r.Fault
	case "baseline":
		w = r.Baseline
	case "recovery":
		w = r.Recovery
	}
	if w == nil {
		return analytics.QueryTimeRange{}, investigation.ErrInvalid
	}
	return *w, nil
}

func (a AssistantTools) resourceHistory(ctx context.Context, j investigation.Job, name string, args json.RawMessage, input assistant.TurnInput) (tools.Result, error) {
	out := tools.Result{SchemaVersion: "tool-response/v3", Tool: name, State: "succeeded", EvidenceRefs: []string{}, DegradedSources: []string{}}
	var p struct {
		CanonicalID string `json:"canonicalId"`
		WindowRole  string `json:"windowRole"`
	}
	if json.Unmarshal(args, &p) != nil {
		return out, investigation.ErrInvalid
	}
	var window *analytics.QueryTimeRange
	if p.WindowRole != "" {
		w, err := resourceWindow(input, j, p.WindowRole)
		if err != nil {
			return out, err
		}
		window = &w
	}
	err := persistence.WithTenantTx(ctx, a.Legacy.SP04.Pool, j.TenantID, func(tx pgx.Tx) error {
		if investigation.CheckAssistantJobTx(ctx, tx, j) != nil {
			return investigation.ErrDenied
		}
		switch name {
		case "resource_history":
			if window == nil {
				return investigation.ErrInvalid
			}
			h, err := investigation.ReadResourceHistoryTx(ctx, tx, j, p.CanonicalID, *window)
			out.Data = h
			out.Partial = true
			return err
		case "resource_changes":
			if window == nil {
				return investigation.ErrInvalid
			}
			h, err := investigation.ReadResourceChangesTx(ctx, tx, j, p.CanonicalID, *window)
			if err != nil {
				return err
			}
			rows, err := tx.Query(ctx, `SELECT evidence_id FROM platform.evidence_metadata WHERE tenant_id=$1 AND canonical_id=$2 AND metadata->>'sourceSystem'='kubernetes' AND metadata->>'type' IN('change','change_event') AND (metadata->>'observedFrom')::timestamptz >= $3 AND (metadata->>'observedTo')::timestamptz <= $4 ORDER BY created_at,evidence_id LIMIT 80`, j.TenantID, p.CanonicalID, window.From, window.To)
			if err != nil {
				return err
			}
			ids := []uuid.UUID{}
			for rows.Next() {
				var id uuid.UUID
				if err := rows.Scan(&id); err != nil {
					rows.Close()
					return err
				}
				ids = append(ids, id)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			for _, id := range ids {
				e, err := evidence.GetTx(ctx, tx, j.TenantID, id, j.Scope)
				if err != nil || !slices.Contains(j.Budget.AllowedDataClasses, e.DataClass) {
					return investigation.ErrDenied
				}
				out.EvidenceRefs = append(out.EvidenceRefs, id.String())
			}
			out.Data = h
			out.Partial = true
			return nil
		case "resource_findings":
			records := []map[string]any{}
			sources := []graph.SourceAuthority{}
			// Outbox revisions are immutable occurrence facts. The latest aggregate is
			// used only by the legacy unwindowed read, never to reconstruct past state.
			query := `SELECT payload FROM finding.records WHERE tenant_id=$1 AND resource_canonical_id=$2 ORDER BY observed_at DESC,finding_id LIMIT 51`
			params := []any{j.TenantID, p.CanonicalID}
			if window != nil {
				query = `SELECT o.payload FROM finding.outbox o JOIN finding.records f USING(tenant_id,finding_id) WHERE o.tenant_id=$1 AND f.resource_canonical_id=$2 AND (o.payload->>'observedAt')::timestamptz >= $3 AND (o.payload->>'observedAt')::timestamptz < $4 ORDER BY (o.payload->>'observedAt')::timestamptz,o.outbox_id LIMIT 51`
				params = append(params, window.From, window.To)
			}
			rows, err := tx.Query(ctx, query, params...)
			if err != nil {
				return err
			}
			payloads := [][]byte{}
			for rows.Next() {
				var raw []byte
				if err := rows.Scan(&raw); err != nil {
					rows.Close()
					return err
				}
				payloads = append(payloads, raw)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			truncated := len(payloads) > 50
			if truncated {
				payloads = payloads[:50]
			}
			for _, raw := range payloads {
				var f finding.Finding
				if json.Unmarshal(raw, &f) != nil || f.ResourceCanonicalID != p.CanonicalID || !j.Scope.Allows(f.ResourceCanonicalID, f.Namespace) {
					return investigation.ErrDenied
				}
				var active bool
				if tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform.source_registrations WHERE tenant_id=$1 AND source_id=$2 AND revision=$3 AND status='active')`, j.TenantID, f.SourceRegistrationID, f.SourceRevision).Scan(&active) != nil || !active {
					return investigation.ErrDenied
				}
				b := evidence.Binding{Tenant: j.TenantID.String(), SourceID: f.SourceRegistrationID, Revision: f.SourceRevision}
				var mapping []byte
				if tx.QueryRow(ctx, `SELECT source_type,backend_logical_id,data_scope_mapping FROM platform.source_registrations WHERE tenant_id=$1 AND source_id=$2 AND revision=$3 AND status='active'`, j.TenantID, f.SourceRegistrationID, f.SourceRevision).Scan(&b.SourceType, &b.BackendLogicalID, &mapping) != nil || json.Unmarshal(mapping, &b.ScopeMapping) != nil {
					return investigation.ErrDenied
				}
				authority := graph.SourceAuthority{SourceRegistrationID: b.SourceID, Revision: b.Revision, ScopeDigest: evidence.BindingScopeDigest(b)}
				if !slices.Contains(sources, authority) {
					sources = append(sources, authority)
				}
				for _, ref := range f.EvidenceRefs {
					id, err := uuid.Parse(ref)
					if err != nil {
						return investigation.ErrInvalid
					}
					e, err := evidence.GetTx(ctx, tx, j.TenantID, id, j.Scope)
					if err != nil || !slices.Contains(j.Budget.AllowedDataClasses, e.DataClass) {
						return investigation.ErrDenied
					}
					out.EvidenceRefs = append(out.EvidenceRefs, ref)
				}
				records = append(records, map[string]any{"findingId": f.FindingID, "occurrenceId": f.OccurrenceID, "aggregateRevision": f.AggregateRevision, "state": f.State, "severity": f.Severity, "ruleId": f.RuleID, "startsAt": f.StartsAt, "observedAt": f.ObservedAt, "timeReliable": f.TimeReliable, "sourceId": f.SourceRegistrationID, "sourceRevision": f.SourceRevision, "evidenceRefs": f.EvidenceRefs, "digest": f.Digest})
			}
			out.Data = map[string]any{"canonicalId": p.CanonicalID, "findings": records, "sources": sources, "limit": 50, "truncated": truncated, "window": window, "missingEvidence": []string{"historical_finding_revisions_before_outbox_unavailable"}}
			out.Partial = truncated || window != nil
		}
		return nil
	})
	slices.Sort(out.EvidenceRefs)
	out.EvidenceRefs = slices.Compact(out.EvidenceRefs)
	return out, err
}
