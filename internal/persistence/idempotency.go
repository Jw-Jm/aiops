package persistence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	idempotencyLeaseDuration   = 30 * time.Second
	idempotencyResponseMaxAge  = 90 * 24 * time.Hour
	idempotencyResponseMaxSize = 64 * 1024
)

var (
	ErrInvalidIdempotencyRequest = errors.New("invalid idempotency request")
	ErrIdempotencyLeaseLost      = errors.New("idempotency lease is no longer owned by this request")
)

type Digest string

type Scope struct {
	TenantID     uuid.UUID
	Subject      string
	Operation    string
	NoRedispatch bool
}

type DecisionKind string

const (
	DecisionProceed    DecisionKind = "proceed"
	DecisionReplay     DecisionKind = "replay"
	DecisionCompleted  DecisionKind = "completed"
	DecisionInProgress DecisionKind = "in_progress"
	DecisionConflict   DecisionKind = "conflict"
)

type Decision struct {
	Kind             DecisionKind
	Lease            Lease
	Response         StoredResponse
	ExecutionUnknown bool
	Terminal         bool
}

type Lease struct {
	TenantID      uuid.UUID
	Subject       string
	Operation     string
	KeyDigest     string
	Token         uuid.UUID
	Attempt       int
	ExecutionOnce bool
}

type StoredResponse struct {
	Status      int
	ContentType string
	Headers     map[string]string
	Body        []byte
}

// HashIdempotencyKey ensures the caller-supplied key is never stored in the ledger.
func HashIdempotencyKey(key string) string {
	digest := sha256.Sum256([]byte(key))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func Begin(ctx context.Context, tx pgx.Tx, scope Scope, key string, requestDigest Digest) (Decision, error) {
	if err := validateIdempotencyRequest(scope, key, requestDigest); err != nil {
		return Decision{}, err
	}
	keyDigest := HashIdempotencyKey(key)
	requestHash := string(requestDigest)
	leaseToken := uuid.Must(uuid.NewV7())
	var ledgerID uuid.UUID
	var attempt int
	err := tx.QueryRow(ctx, `
		INSERT INTO platform.idempotency_request_ledger
			(tenant_id, ledger_id, subject, operation, idempotency_key_digest, request_digest,
			 execution_once, state, lease_token, lease_expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'in_progress', $8, clock_timestamp() + $9::interval)
		ON CONFLICT (tenant_id, subject, operation, idempotency_key_digest) DO NOTHING
		RETURNING ledger_id, attempt`,
		scope.TenantID, uuid.Must(uuid.NewV7()), scope.Subject, scope.Operation, keyDigest,
		requestHash, scope.NoRedispatch, leaseToken, idempotencyLeaseDuration.String(),
	).Scan(&ledgerID, &attempt)
	if err == nil {
		return Decision{Kind: DecisionProceed, Lease: leaseFor(scope, keyDigest, leaseToken, attempt)}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Decision{}, fmt.Errorf("insert idempotency ledger row: %w", err)
	}

	var stored struct {
		ledgerID      uuid.UUID
		requestDigest string
		executionOnce bool
		state         string
		leaseActive   bool
		attempt       int
		status        *int
		contentType   *string
		headers       []byte
		body          []byte
	}
	err = tx.QueryRow(ctx, `
		SELECT ledger_id, request_digest, execution_once, state,
	       COALESCE(lease_expires_at > clock_timestamp(), false),
		       attempt, response_status, response_content_type, response_headers, response_body
		FROM platform.idempotency_request_ledger
		WHERE tenant_id = $1 AND subject = $2 AND operation = $3 AND idempotency_key_digest = $4
		FOR UPDATE`, scope.TenantID, scope.Subject, scope.Operation, keyDigest).Scan(
		&stored.ledgerID, &stored.requestDigest, &stored.executionOnce, &stored.state,
		&stored.leaseActive, &stored.attempt, &stored.status,
		&stored.contentType, &stored.headers, &stored.body,
	)
	if err != nil {
		return Decision{}, fmt.Errorf("lock idempotency ledger row: %w", err)
	}
	if stored.requestDigest != requestHash || stored.executionOnce != scope.NoRedispatch {
		return Decision{Kind: DecisionConflict}, nil
	}

	switch stored.state {
	case "completed":
		response := StoredResponse{Status: *stored.status, ContentType: *stored.contentType, Body: stored.body}
		if err := json.Unmarshal(stored.headers, &response.Headers); err != nil {
			return Decision{}, fmt.Errorf("decode stored idempotency response headers: %w", err)
		}
		return Decision{Kind: DecisionReplay, Response: response}, nil
	case "in_progress":
		if stored.leaseActive {
			return Decision{Kind: DecisionInProgress}, nil
		}
		if stored.executionOnce {
			_, err = tx.Exec(ctx, `UPDATE platform.idempotency_request_ledger
				SET state = 'execution_unknown', lease_token = NULL, lease_expires_at = NULL, updated_at = clock_timestamp()
				WHERE tenant_id = $1 AND ledger_id = $2`, scope.TenantID, stored.ledgerID)
			if err != nil {
				return Decision{}, fmt.Errorf("mark crashed execution unknown: %w", err)
			}
			return Decision{Kind: DecisionInProgress, ExecutionUnknown: true, Terminal: true}, nil
		}
		return acquireIdempotencyLease(ctx, tx, scope, keyDigest, stored.ledgerID, stored.attempt+1)
	case "retryable":
		if stored.executionOnce {
			return Decision{Kind: DecisionInProgress, ExecutionUnknown: true, Terminal: true}, nil
		}
		return acquireIdempotencyLease(ctx, tx, scope, keyDigest, stored.ledgerID, stored.attempt+1)
	case "failed", "execution_unknown":
		return Decision{Kind: DecisionInProgress, ExecutionUnknown: stored.state == "execution_unknown", Terminal: true}, nil
	default:
		return Decision{}, fmt.Errorf("unknown idempotency state %q", stored.state)
	}
}

func Complete(ctx context.Context, tx pgx.Tx, lease Lease, response StoredResponse) error {
	if err := validateLease(lease); err != nil {
		return err
	}
	validEmptyResponse := (response.Status == http.StatusNoContent || response.Status == http.StatusResetContent || response.Status == http.StatusNotModified) && len(response.Body) == 0
	if response.Status < 100 || response.Status > 599 || response.ContentType != "application/json" || len(response.Body) > idempotencyResponseMaxSize || (!validEmptyResponse && !json.Valid(response.Body)) {
		return fmt.Errorf("%w: response must be bounded JSON with a valid status", ErrInvalidIdempotencyRequest)
	}
	headers, err := marshalReplayHeaders(response.Headers)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE platform.idempotency_request_ledger
		SET state = 'completed', lease_token = NULL, lease_expires_at = NULL,
		    response_status = $1, response_content_type = $2, response_headers = $3,
		    response_body = $4, response_expires_at = clock_timestamp() + $5::interval,
		    updated_at = clock_timestamp()
		WHERE tenant_id = $6 AND subject = $7 AND operation = $8 AND idempotency_key_digest = $9
		  AND state = 'in_progress' AND lease_token = $10 AND lease_expires_at > clock_timestamp()`,
		response.Status, response.ContentType, headers, response.Body, idempotencyResponseMaxAge.String(),
		lease.TenantID, lease.Subject, lease.Operation, lease.KeyDigest, lease.Token)
	if err != nil {
		return fmt.Errorf("complete idempotency request: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrIdempotencyLeaseLost
	}
	return nil
}

func Fail(ctx context.Context, tx pgx.Tx, lease Lease, retryable bool) error {
	if err := validateLease(lease); err != nil {
		return err
	}
	state := "failed"
	if retryable && !lease.ExecutionOnce {
		state = "retryable"
	} else if lease.ExecutionOnce {
		state = "execution_unknown"
	}
	tag, err := tx.Exec(ctx, `
		UPDATE platform.idempotency_request_ledger
		SET state = $1, lease_token = NULL, lease_expires_at = NULL, updated_at = clock_timestamp()
		WHERE tenant_id = $2 AND subject = $3 AND operation = $4 AND idempotency_key_digest = $5
		  AND state = 'in_progress' AND lease_token = $6 AND lease_expires_at > clock_timestamp()`,
		state, lease.TenantID, lease.Subject, lease.Operation, lease.KeyDigest, lease.Token)
	if err != nil {
		return fmt.Errorf("fail idempotency request: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return ErrIdempotencyLeaseLost
	}
	return nil
}

// DispatchExecutionOnce commits its claim before invoking an external dispatcher. An uncertain
// dispatcher result is terminal for this key; recovery reports execution_unknown and never calls
// the dispatcher again.
func DispatchExecutionOnce(ctx context.Context, pool TxBeginner, scope Scope, key string, requestDigest Digest, dispatch func(context.Context) (StoredResponse, error)) (Decision, error) {
	if !scope.NoRedispatch || dispatch == nil {
		return Decision{}, ErrInvalidIdempotencyRequest
	}
	var admission Decision
	if err := WithTenantTx(ctx, pool, scope.TenantID, func(tx pgx.Tx) error {
		var err error
		admission, err = Begin(ctx, tx, scope, key, requestDigest)
		return err
	}); err != nil {
		return Decision{}, err
	}
	if admission.Kind != DecisionProceed {
		return admission, nil
	}

	response, dispatchErr := dispatch(ctx)
	if dispatchErr != nil {
		unknown := Decision{Kind: DecisionInProgress, ExecutionUnknown: true, Terminal: true}
		failErr := WithTenantTx(ctx, pool, scope.TenantID, func(tx pgx.Tx) error {
			return Fail(ctx, tx, admission.Lease, false)
		})
		if failErr != nil {
			return unknown, errors.Join(dispatchErr, fmt.Errorf("record uncertain execution: %w", failErr))
		}
		return unknown, dispatchErr
	}
	if err := WithTenantTx(ctx, pool, scope.TenantID, func(tx pgx.Tx) error {
		return Complete(ctx, tx, admission.Lease, response)
	}); err != nil {
		unknown := Decision{Kind: DecisionInProgress, ExecutionUnknown: true, Terminal: true}
		return unknown, fmt.Errorf("persist execution response after dispatch: %w", err)
	}
	return Decision{Kind: DecisionCompleted, Response: response}, nil
}

func validateIdempotencyRequest(scope Scope, key string, digest Digest) error {
	if scope.TenantID == uuid.Nil || strings.TrimSpace(scope.Subject) == "" || len(scope.Subject) > 512 ||
		strings.TrimSpace(scope.Operation) == "" || len(scope.Operation) > 512 || !validIdempotencyKey(key) || !validDigest(string(digest)) {
		return ErrInvalidIdempotencyRequest
	}
	return nil
}

func validDigest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil && len(decoded) == sha256.Size
}

func validIdempotencyKey(key string) bool {
	if len(key) == 0 || len(key) > 512 || strings.TrimSpace(key) != key {
		return false
	}
	for _, value := range []byte(key) {
		if value < 0x21 || value > 0x7e {
			return false
		}
	}
	return true
}

func acquireIdempotencyLease(ctx context.Context, tx pgx.Tx, scope Scope, keyDigest string, ledgerID uuid.UUID, attempt int) (Decision, error) {
	token := uuid.Must(uuid.NewV7())
	tag, err := tx.Exec(ctx, `
		UPDATE platform.idempotency_request_ledger
		SET state = 'in_progress', lease_token = $1, lease_expires_at = clock_timestamp() + $2::interval,
		    attempt = $3, updated_at = clock_timestamp()
		WHERE tenant_id = $4 AND ledger_id = $5`, token, idempotencyLeaseDuration.String(), attempt, scope.TenantID, ledgerID)
	if err != nil {
		return Decision{}, fmt.Errorf("acquire idempotency lease: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return Decision{}, ErrIdempotencyLeaseLost
	}
	return Decision{Kind: DecisionProceed, Lease: leaseFor(scope, keyDigest, token, attempt)}, nil
}

func leaseFor(scope Scope, keyDigest string, token uuid.UUID, attempt int) Lease {
	return Lease{TenantID: scope.TenantID, Subject: scope.Subject, Operation: scope.Operation,
		KeyDigest: keyDigest, Token: token, Attempt: attempt, ExecutionOnce: scope.NoRedispatch}
}

func validateLease(lease Lease) error {
	if lease.TenantID == uuid.Nil || lease.Subject == "" || lease.Operation == "" ||
		!validDigest(lease.KeyDigest) || lease.Token == uuid.Nil || lease.Attempt < 1 {
		return ErrInvalidIdempotencyRequest
	}
	return nil
}

func marshalReplayHeaders(values map[string]string) ([]byte, error) {
	allowed := map[string]struct{}{"cache-control": {}, "content-language": {}, "etag": {}, "location": {}}
	filtered := make(map[string]string, len(values))
	for name, value := range values {
		lower := strings.ToLower(strings.TrimSpace(name))
		if _, ok := allowed[lower]; !ok || strings.ContainsAny(value, "\r\n") || len(value) > 2048 {
			return nil, fmt.Errorf("%w: response contains a non-replayable header", ErrInvalidIdempotencyRequest)
		}
		filtered[http.CanonicalHeaderKey(lower)] = value
	}
	return json.Marshal(filtered)
}
