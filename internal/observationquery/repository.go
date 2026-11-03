package observationquery

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"ops-platform/internal/graph"
	"ops-platform/internal/persistence"
	"time"
)

type Repository struct{ Pool persistence.TxBeginner }

// AuthorizeTx pins session, independent operator grants and source revisions
// until the enclosing read/write commits. No legacy-role fallback is allowed.
func AuthorizeTx(ctx context.Context, tx pgx.Tx, claims graph.Claims, request Request) (Reauthorization, error) {
	out := Reauthorization{CheckedAt: time.Now().UTC(), SourceRegistrationRevision: request.Source.RegistrationRevision, Result: "allowed"}
	if claims.Scope.Cluster != request.Resource.ClusterUID {
		return out, ErrDenied
	}
	resolved, err := graph.ResolveNativeClaimTx(ctx, tx, claims)
	if err != nil {
		return out, ErrDenied
	}
	current, err := graph.EffectivePersistedNativeTx(ctx, tx, claims.Scope.Tenant, claims.Subject, request.Resource.ClusterUID, resolved.UserID, resolved.AuthorizationVersion)
	if err != nil || !current.Allows(request.Resource.CanonicalID, *request.Resource.NamespaceScope.Namespace) {
		return out, ErrDenied
	}
	out.AuthorizationVersion = resolved.AuthorizationVersion
	var sourceCurrent bool
	err = tx.QueryRow(ctx, `SELECT platform.observation_lock_source($1,$2,$3,$4,$5,$6)`, claims.Scope.Tenant, request.Source.RegistrationID, request.Source.RegistrationRevision, request.Source.CredentialRevision, request.Resource.ClusterUID, *request.Resource.NamespaceScope.Namespace).Scan(&sourceCurrent)
	if err != nil {
		return out, err
	}
	if !sourceCurrent {
		return out, ErrUnavailable
	}
	return out, nil
}
func (r Repository) Create(ctx context.Context, claims graph.Claims, request Request, key uuid.UUID) (View, error) {
	var out View
	tenant, err := uuid.Parse(claims.Scope.Tenant)
	if err != nil {
		return out, ErrDenied
	}
	session, err := uuid.Parse(claims.Native.SessionID)
	if err != nil {
		return out, ErrDenied
	}
	err = persistence.WithTenantTx(ctx, r.Pool, tenant, func(tx pgx.Tx) error {
		checked, err := AuthorizeTx(ctx, tx, claims, request)
		if err != nil {
			return err
		}
		var previousRequest, previousView []byte
		previousErr := tx.QueryRow(ctx, `SELECT request,view FROM investigation.observation_queries WHERE tenant_id=$1 AND owner_subject=$2 AND request_key=$3`, tenant, claims.Subject, key).Scan(&previousRequest, &previousView)
		if previousErr == nil {
			var previous Request
			if json.Unmarshal(previousRequest, &previous) != nil || graph.ScopeDigest(previous) != graph.ScopeDigest(request) {
				return ErrConflict
			}
			if err := json.Unmarshal(previousView, &out); err != nil {
				return err
			}
			out.QueryRequest = &previous
			return nil
		}
		if !errors.Is(previousErr, pgx.ErrNoRows) {
			return previousErr
		}
		now := time.Now().UTC()
		out = View{QueryRequest: &request, Filters: request.Filters, QueryID: uuid.New(), Revision: 1, SchemaVersion: Schema, State: "accepted", OwnerSubject: claims.Subject, TenantID: tenant, Resource: request.Resource, Source: request.Source, Window: request.Window, Coverage: request.Coverage(), CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(30 * 24 * time.Hour), Reauthorization: checked}
		body, _ := json.Marshal(request)
		scope, _ := json.Marshal(claims.Scope)
		view, _ := json.Marshal(out)
		_, err = tx.Exec(ctx, `INSERT INTO investigation.observation_queries(tenant_id,query_id,owner_subject,cluster_uid,session_id,request,scope,view,state,created_at,updated_at,expires_at,request_key) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'accepted',$9,$9,$10,$11)`, tenant, out.QueryID, claims.Subject, request.Resource.ClusterUID, session, body, scope, view, now, out.ExpiresAt, key)
		var pgerr *pgconn.PgError
		if errors.As(err, &pgerr) && pgerr.Code == "23505" {
			return ErrConflict
		}
		return err
	})
	return out, err
}
func scanRecord(row pgx.Row) (Record, error) {
	var out Record
	var request, scope, view []byte
	err := row.Scan(&request, &scope, &view, &out.SessionID, &out.LeaseToken, &out.Result)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if json.Unmarshal(request, &out.Request) != nil || json.Unmarshal(scope, &out.Scope) != nil || json.Unmarshal(view, &out.View) != nil {
		return out, ErrUnavailable
	}
	out.QueryRequest = &out.Request
	return out, nil
}

const recordColumns = `request,scope,view,session_id,COALESCE(lease_token,'00000000-0000-0000-0000-000000000000'::uuid),result`

// Read runs the current session/source check in the same transaction as the
// result read; a caller cannot use a historical session reference to read it.
func (r Repository) Read(ctx context.Context, claims graph.Claims, id uuid.UUID) (Record, error) {
	var out Record
	err := persistence.WithTenantTx(ctx, r.Pool, uuid.MustParse(claims.Scope.Tenant), func(tx pgx.Tx) error {
		var err error
		out, err = scanRecord(tx.QueryRow(ctx, `SELECT `+recordColumns+` FROM investigation.observation_queries WHERE tenant_id=$1 AND query_id=$2 AND owner_subject=$3`, claims.Scope.Tenant, id, claims.Subject))
		if err != nil {
			return err
		}
		out.Reauthorization, err = AuthorizeTx(ctx, tx, claims, out.Request)
		if err != nil {
			return err
		}
		if !out.ExpiresAt.After(time.Now()) {
			// Read-only projection preserves the immutable stored result.
			expireRecord(&out)
		}
		return nil
	})
	return out, err
}
func (r Repository) IDs(ctx context.Context, claims graph.Claims, cursor uuid.UUID, limit int) ([]uuid.UUID, error) {
	out := []uuid.UUID{}
	err := persistence.WithTenantTx(ctx, r.Pool, uuid.MustParse(claims.Scope.Tenant), func(tx pgx.Tx) error {
		if cursor != uuid.Nil {
			var owned bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM investigation.observation_queries WHERE tenant_id=$1 AND owner_subject=$2 AND cluster_uid=$3 AND query_id=$4)`, claims.Scope.Tenant, claims.Subject, claims.Scope.Cluster, cursor).Scan(&owned); err != nil {
				return err
			}
			if !owned {
				return ErrInvalid
			}
		}
		rows, err := tx.Query(ctx, `SELECT query_id FROM investigation.observation_queries WHERE tenant_id=$1 AND owner_subject=$2 AND cluster_uid=$3 AND expires_at>clock_timestamp() AND ($4::uuid='00000000-0000-0000-0000-000000000000'::uuid OR (created_at,query_id)<(SELECT created_at,query_id FROM investigation.observation_queries WHERE tenant_id=$1 AND owner_subject=$2 AND cluster_uid=$3 AND query_id=$4)) ORDER BY created_at DESC,query_id DESC LIMIT $5`, claims.Scope.Tenant, claims.Subject, claims.Scope.Cluster, cursor, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				return err
			}
			out = append(out, id)
		}
		return rows.Err()
	})
	return out, err
}
func save(ctx context.Context, tx pgx.Tx, rec Record) (bool, error) {
	view, _ := json.Marshal(rec.View)
	tag, err := tx.Exec(ctx, `UPDATE investigation.observation_queries SET view=$3,state=$4,revision=$5,updated_at=$6,result=COALESCE($7,result) WHERE tenant_id=$1 AND query_id=$2 AND revision=$8`, rec.TenantID, rec.QueryID, view, rec.State, rec.Revision, rec.UpdatedAt, rec.Result, rec.Revision-1)
	return tag.RowsAffected() == 1, err
}
func (r Repository) Cancel(ctx context.Context, claims graph.Claims, id uuid.UUID, revision int64) (View, error) {
	var out Record
	err := persistence.WithTenantTx(ctx, r.Pool, uuid.MustParse(claims.Scope.Tenant), func(tx pgx.Tx) error {
		var err error
		out, err = scanRecord(tx.QueryRow(ctx, `SELECT `+recordColumns+` FROM investigation.observation_queries WHERE tenant_id=$1 AND query_id=$2 AND owner_subject=$3 FOR UPDATE`, claims.Scope.Tenant, id, claims.Subject))
		if err != nil {
			return err
		}
		out.Reauthorization, err = AuthorizeTx(ctx, tx, claims, out.Request)
		if err != nil {
			return err
		}
		if out.Revision != revision {
			return ErrConflict
		}
		if out.State != "accepted" && out.State != "running" {
			return ErrConflict
		}
		now := time.Now().UTC()
		out.State = "cancel_requested"
		out.CancelRequestedAt = &now
		out.Revision++
		out.UpdatedAt = now
		_, err = save(ctx, tx, out)
		return err
	})
	return out.View, err
}

// Claim commits a bounded lease before any source call. A crashed attempt is
// failed visibly; it is never silently retried as a second source operation.
func (r Repository) Claim(ctx context.Context, tenant uuid.UUID, cluster string) (Record, error) {
	var out Record
	err := persistence.WithTenantTx(ctx, r.Pool, tenant, func(tx pgx.Tx) error {
		var err error
		out, err = scanRecord(tx.QueryRow(ctx, `SELECT `+recordColumns+` FROM investigation.observation_queries WHERE tenant_id=$1 AND cluster_uid=$2 AND (state='accepted' OR state='cancel_requested' OR(state='running' AND lease_until<clock_timestamp())) ORDER BY created_at LIMIT 1 FOR UPDATE SKIP LOCKED`, tenant, cluster))
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		oldState := out.State
		out.Revision++
		out.UpdatedAt = now
		if !out.ExpiresAt.After(now) {
			expireRecord(&out)
		} else if oldState == "cancel_requested" {
			out.State = "terminated"
			out.Termination = &Termination{RequestedAt: *out.CancelRequestedAt, DeliveredAt: now, SourceStopped: out.LeaseToken == uuid.Nil}
			out.Termination.ReceiptDigest = graph.ScopeDigest(out.Termination)
		} else if oldState == "running" {
			out.State = "failed"
			out.Failure = &Failure{Code: "WORKER_INTERRUPTED", Retryable: true, Message: "查询工作进程中断；可发起新查询。"}
		} else {
			out.State = "running"
			out.LeaseToken = uuid.New()
		}
		view, _ := json.Marshal(out.View)
		_, err = tx.Exec(ctx, `UPDATE investigation.observation_queries SET state=$3,revision=$4,view=$5,updated_at=$6,lease_token=$7,lease_until=$6::timestamptz+interval '15 seconds' WHERE tenant_id=$1 AND query_id=$2`, tenant, out.QueryID, out.State, out.Revision, view, now, out.LeaseToken)
		return err
	})
	return out, err
}
func (r Repository) Cancelled(ctx context.Context, rec Record) (bool, error) {
	var cancelled bool
	err := persistence.WithTenantTx(ctx, r.Pool, rec.TenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT state<>'running' OR lease_token<>$3 OR expires_at<=clock_timestamp() FROM investigation.observation_queries WHERE tenant_id=$1 AND query_id=$2`, rec.TenantID, rec.QueryID, rec.LeaseToken).Scan(&cancelled)
	})
	return cancelled, err
}
func (r Repository) Finish(ctx context.Context, rec Record, claims graph.Claims, result json.RawMessage, runError error) error {
	return persistence.WithTenantTx(ctx, r.Pool, rec.TenantID, func(tx pgx.Tx) error {
		current, err := scanRecord(tx.QueryRow(ctx, `SELECT `+recordColumns+` FROM investigation.observation_queries WHERE tenant_id=$1 AND query_id=$2 FOR UPDATE`, rec.TenantID, rec.QueryID))
		if err != nil {
			return err
		}
		if current.State != "running" || current.LeaseToken != rec.LeaseToken {
			return nil
		}
		current.Revision++
		current.UpdatedAt = time.Now().UTC()
		checked, err := AuthorizeTx(ctx, tx, claims, current.Request)
		if err == nil {
			current.Reauthorization = checked
		}
		if err != nil {
			current.State = "denied"
			current.Failure = &Failure{Code: "AUTHORITY_CHANGED", Message: "当前权限或来源版本已变化。"}
		} else if !current.ExpiresAt.After(current.UpdatedAt) {
			expireRecord(&current)
		} else if runError != nil {
			current.State = "failed"
			current.Failure = &Failure{Code: "SOURCE_UNAVAILABLE", Retryable: true, Message: "来源查询未成功；没有发布结果。"}
		} else {
			var body struct {
				ResultID     uuid.UUID `json:"resultId"`
				ResultDigest string    `json:"resultDigest"`
			}
			if json.Unmarshal(result, &body) != nil {
				return ErrInvalid
			}
			current.State = "completed"
			current.Result = result
			current.ResultRef = &ResultRef{body.ResultID, body.ResultDigest, current.ExpiresAt}
		}
		_, err = save(ctx, tx, current)
		return err
	})
}

func expireRecord(record *Record) {
	record.State, record.ResultRef, record.Result = "expired", nil, nil
	record.CancelRequestedAt, record.Termination = nil, nil
	record.Failure = &Failure{Code: "RESULT_EXPIRED", Message: "查询结果已过期，请重新查询。", Retryable: false}
}
