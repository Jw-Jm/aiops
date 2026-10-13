package assistant

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/analytics"
	"ops-platform/internal/graph"
	"ops-platform/internal/investigation"
	"ops-platform/internal/persistence"
	"time"
)

// Rebind is explicit, revision checked and idle only. Existing turns retain
// their immutable input, scope, policy and observation windows.
func (r Repository) Rebind(ctx context.Context, tenant, id uuid.UUID, subject, key string, expected int64, selection analytics.Selection, resourceContext *ResourceContext, window *TimeRange, requestDigest string) (Conversation, error) {
	var out Conversation
	if key == "" || len(key) > 128 || expected < 1 || requestDigest == "" {
		return out, investigation.ErrInvalid
	}
	err := persistence.WithTenantTx(ctx, r.Pool, tenant, func(tx pgx.Tx) error {
		var err error
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, tenant.String()+"|assistant-binding|"+subject+"|"+key); err != nil {
			return err
		}
		out, err = loadConversation(ctx, tx, tenant, id, subject, true)
		if err != nil {
			return err
		}
		grant, err := authorizeConversation(ctx, tx, out)
		if err != nil {
			return err
		}
		var digest string
		var priorID uuid.UUID
		err = tx.QueryRow(ctx, `SELECT conversation_id,request_digest FROM investigation.assistant_binding_requests WHERE tenant_id=$1 AND owner_subject=$2 AND idempotency_key=$3`, tenant, subject, key).Scan(&priorID, &digest)
		if err == nil {
			if priorID != id || digest != requestDigest {
				return investigation.ErrConflict
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if out.ArchivedAt != nil {
			return ErrArchived
		}
		if out.Revision != expected {
			return ErrStale
		}
		var busy bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM investigation.assistant_turns WHERE tenant_id=$1 AND conversation_id=$2 AND state IN('queued','running'))`, tenant, id).Scan(&busy); err != nil {
			return err
		}
		if busy {
			return ErrBusy
		}
		if out.AuthorizationDomain == "audit" {
			return investigation.ErrInvalid
		}
		if resourceContext != nil {
			if out.Mode != "troubleshoot" || out.Binding.IncidentID != uuid.Nil || ValidateContext(*resourceContext) != nil || resourceContext.OwnerSubject != subject || resourceContext.TenantID != tenant.String() || resourceContext.ClusterUID != out.ClusterUID || resourceContext.GrantSnapshotDigest != graph.ScopeDigest(grant) {
				return investigation.ErrDenied
			}
			out.Binding.ResourceContext = resourceContext
		}
		if window != nil {
			maxWindow := time.Hour
			if out.Mode == "data_query" {
				maxWindow = 31 * 24 * time.Hour
			}
			if (out.Mode != "data_query" && out.Binding.ResourceContext == nil) || window.From.IsZero() || !window.To.After(window.From) || window.To.Sub(window.From) > maxWindow || window.To.After(time.Now().Add(time.Minute)) || window.Timezone != out.Binding.Timezone {
				return investigation.ErrInvalid
			}
			if out.Mode == "data_query" {
				frozen := *window
				frozen.From = frozen.From.UTC()
				frozen.To = frozen.To.UTC()
				out.Binding.TimeRange = &frozen
			} else {
				rc := *out.Binding.ResourceContext
				rc.TimeRange = *window
				rc.TimeRange.From = rc.TimeRange.From.UTC()
				rc.TimeRange.To = rc.TimeRange.To.UTC()
				rc.ContextDigest = ""
				rc.ContextDigest = graph.ScopeDigest(rc)
				if ValidateContext(rc) != nil {
					return investigation.ErrInvalid
				}
				out.Binding.ResourceContext = &rc
			}
		}
		if out.Binding.ResourceContext != nil {
			selection.CanonicalIDs = nil
			candidate := out.Binding.ResourceContext.Resources[0]
			if len(selection.Namespaces) == 0 && candidate.Namespace != "" {
				selection.Namespaces = []string{candidate.Namespace}
			}
			if candidate.Namespace == "" {
				selection.IncludeClusterScoped = true
			}
		}
		scope, err := analytics.RestrictScope(grant, selection)
		if err != nil {
			return err
		}
		out.Binding.Selection = selection
		out.Binding.ExecutionScope = scope
		if _, err = authorizeConversation(ctx, tx, out); err != nil {
			return err
		}
		if err = tx.QueryRow(ctx, `UPDATE investigation.assistant_conversations SET binding=$3,revision=revision+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND conversation_id=$2 RETURNING revision,updated_at`, tenant, id, raw(out.Binding)).Scan(&out.Revision, &out.UpdatedAt); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `INSERT INTO investigation.assistant_binding_requests(tenant_id,owner_subject,idempotency_key,conversation_id,request_digest,revision) VALUES($1,$2,$3,$4,$5,$6)`, tenant, subject, key, id, requestDigest, out.Revision); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO investigation.assistant_events(tenant_id,conversation_id,type,payload) VALUES($1,$2,'binding.changed',$3)`, tenant, id, raw(map[string]any{"revision": out.Revision, "contextDigest": graph.ScopeDigest(out.Binding)}))
		return err
	})
	return out, err
}
