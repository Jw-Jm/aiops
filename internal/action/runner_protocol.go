package action

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/auth"
	platformcrypto "ops-platform/internal/crypto"
	"ops-platform/internal/persistence"
	"time"
)

type Dispatch struct {
	TenantID    uuid.UUID `json:"tenantId"`
	ExecutionID uuid.UUID `json:"executionId"`
	Token       string    `json:"claimToken"`
	Profile     Profile   `json:"profile"`
	Binding     Binding   `json:"binding"`
}
type ClaimedCommand struct {
	Credentials    RunnerCredentials `json:"credentials"`
	ExecutionID    uuid.UUID         `json:"executionId"`
	Command        string            `json:"command"`
	TimeoutSeconds int               `json:"timeoutSeconds"`
	MaxOutputBytes int64             `json:"maxOutputBytes"`
}

// BeginDispatch commits one attempt before external side effects. Worker takeover
// can reconcile this attempt but can never mint another execution authority.
func (s Service) BeginDispatch(ctx context.Context, tenant, id uuid.UUID) (Dispatch, error) {
	out := Dispatch{TenantID: tenant, ExecutionID: id}
	err := persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
		e, err := executionTx(ctx, tx, tenant, id)
		if err != nil {
			return err
		}
		if e.State != "prepared" {
			return ErrConflict
		}
		var raw []byte
		var deadline time.Time
		if err = tx.QueryRow(ctx, `SELECT actor_context,dispatch_deadline FROM action.executions WHERE tenant_id=$1 AND execution_id=$2`, tenant, id).Scan(&raw, &deadline); err != nil {
			return err
		}
		var a auth.RequestContext
		if json.Unmarshal(raw, &a) != nil {
			return ErrDenied
		}
		p, _, err := profileTx(ctx, tx, tenant, e.Binding.ProfileID, e.Binding.ProfileVersion)
		denied := err != nil || !time.Now().Before(deadline) || !time.Now().Before(a.TokenExpiresAt)
		if !denied {
			_, err = s.authorize(ctx, tx, a, e.Binding, p, true)
			denied = err != nil
		}
		if denied {
			if _, err = tx.Exec(ctx, `UPDATE action.executions SET state='failed',completed_at=clock_timestamp() WHERE tenant_id=$1 AND execution_id=$2`, tenant, id); err != nil {
				return err
			}
			out.Token = ""
			if err = appendAudit(ctx, tx, workerActor(tenant), id, "command.dispatch_denied", map[string]any{"sent": false}); err != nil {
				return err
			}
			return event(ctx, tx, tenant, id, "state", map[string]any{"state": "failed", "reason": "dispatch_authority_expired_or_revoked", "sent": false})
		}
		token := make([]byte, 32)
		if _, err = rand.Read(token); err != nil {
			return err
		}
		out.Token = base64.RawURLEncoding.EncodeToString(token)
		out.Profile = p
		out.Binding = e.Binding
		_, err = tx.Exec(ctx, `INSERT INTO action.attempts(tenant_id,execution_id,attempt_no,dispatch_token_digest) VALUES($1,$2,1,$3)`, tenant, id, Digest([]byte(out.Token)))
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE action.executions SET state='dispatching',dispatch_token_digest=$3 WHERE tenant_id=$1 AND execution_id=$2`, tenant, id, Digest([]byte(out.Token)))
		if err != nil {
			return err
		}
		if err = appendAudit(ctx, tx, a, id, "command.dispatching", map[string]any{"requestDigest": e.RequestDigest, "attemptNo": 1}); err != nil {
			return err
		}
		return event(ctx, tx, tenant, id, "state", map[string]any{"state": "dispatching"})
	})
	if err == nil && out.Token == "" {
		return out, ErrDenied
	}
	return out, err
}
func (s Service) Claim(ctx context.Context, tenant, id uuid.UUID, token string) (ClaimedCommand, error) {
	return s.ClaimWithKey(ctx, tenant, id, token, "")
}
func (s Service) ClaimWithKey(ctx context.Context, tenant, id uuid.UUID, token, publicKey string) (ClaimedCommand, error) {
	var out ClaimedCommand
	if len(token) != 43 || s.Protector == nil {
		return out, ErrDenied
	}
	err := persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
		e, err := executionTx(ctx, tx, tenant, id)
		if err != nil {
			return err
		}
		if e.State != "dispatching" {
			return ErrConflict
		}
		var envelopeRaw, actorRaw []byte
		var active bool
		err = tx.QueryRow(ctx, `SELECT command_envelope,actor_context,claimed_at IS NULL AND dispatch_token_digest=$3 AND dispatch_deadline>clock_timestamp() FROM action.executions WHERE tenant_id=$1 AND execution_id=$2`, tenant, id, Digest([]byte(token))).Scan(&envelopeRaw, &actorRaw, &active)
		if err != nil {
			return err
		}
		if !active {
			return ErrDenied
		}
		var envelope platformcrypto.Envelope
		var a auth.RequestContext
		if json.Unmarshal(envelopeRaw, &envelope) != nil || json.Unmarshal(actorRaw, &a) != nil {
			return ErrDenied
		}
		if !time.Now().Before(a.TokenExpiresAt) {
			return ErrDenied
		}
		p, _, err := profileTx(ctx, tx, tenant, e.Binding.ProfileID, e.Binding.ProfileVersion)
		if err != nil {
			return err
		}
		if _, err = s.authorize(ctx, tx, a, e.Binding, p, true); err != nil {
			return err
		}
		plain, err := s.Protector.Open(ctx, tenant, id, envelope)
		if err != nil {
			return err
		}
		defer clear(plain)
		if Digest(plain) != e.Binding.CommandDigest || ValidateCommand(string(plain), e.Binding.Shell) != nil {
			return ErrDenied
		}
		_, err = tx.Exec(ctx, `UPDATE action.executions SET state='running',claimed_at=clock_timestamp() WHERE tenant_id=$1 AND execution_id=$2`, tenant, id)
		if err != nil {
			return err
		}
		out = ClaimedCommand{ExecutionID: id, Command: string(plain), TimeoutSeconds: e.Binding.Options.TimeoutSeconds, MaxOutputBytes: e.Binding.Options.MaxOutputBytes}
		if s.Credentials != nil {
			out.Credentials, err = s.Credentials.Issue(ctx, p, e.Binding, publicKey)
			if err != nil {
				return err
			}
		}
		if err = appendAudit(ctx, tx, a, id, "command.claimed", map[string]any{"requestDigest": e.RequestDigest}); err != nil {
			return err
		}
		return event(ctx, tx, tenant, id, "state", map[string]any{"state": "running"})
	})
	return out, err
}
func (s Service) MarkUnknown(ctx context.Context, tenant, id uuid.UUID, reason string) error {
	return persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE action.executions SET state='execution_unknown',completed_at=clock_timestamp() WHERE tenant_id=$1 AND execution_id=$2 AND state IN('dispatching','running')`, tenant, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return nil
		}
		if err = appendAudit(ctx, tx, workerActor(tenant), id, "command.execution_unknown", map[string]any{"reason": reason, "automaticRetry": false}); err != nil {
			return err
		}
		return event(ctx, tx, tenant, id, "state", map[string]any{"state": "execution_unknown", "reason": reason, "automaticRetry": false})
	})
}
func (s Service) RecordRunnerRef(ctx context.Context, d Dispatch, ref string) error {
	return persistence.WithTenantTx(ctx, s.Pool, d.TenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE action.executions SET runner_ref=$4 WHERE tenant_id=$1 AND execution_id=$2 AND dispatch_token_digest=$3 AND runner_ref IS NULL`, d.TenantID, d.ExecutionID, Digest([]byte(d.Token)), ref)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrConflict
		}
		return nil
	})
}
