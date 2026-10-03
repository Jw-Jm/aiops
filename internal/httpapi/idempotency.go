package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	generated "ops-platform/gen/api"
	"ops-platform/internal/auth"
	"ops-platform/internal/bundle"
	"ops-platform/internal/persistence"
)

const (
	maxIdempotentRequestBody  = 1 << 20
	maxIdempotentResponseBody = 64 << 10
)

type transactionContextKey struct{}

// TransactionFromContext returns the tenant-bound transaction installed around a write handler.
func TransactionFromContext(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(transactionContextKey{}).(pgx.Tx)
	return tx, ok
}

// IdempotencyMiddleware binds each mutating request to an authenticated tenant and subject, then
// commits the ledger result in the same transaction as the handler's database changes.
type IdempotencyMiddleware struct {
	Pool        persistence.TxBeginner
	Resolve     func(*http.Request) (persistence.Scope, error)
	Authorize   func(*http.Request, persistence.Scope) error
	AuthorizeTx func(*http.Request, pgx.Tx, persistence.Scope) error
}

func (m IdempotencyMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := auth.RequestIDFromHeader(r.Header.Get("X-Request-ID"))
		if verified, ok := auth.RequestID(r.Context()); ok {
			requestID = verified
		}
		w.Header().Set("X-Request-ID", requestID)
		if m.Pool == nil || m.Resolve == nil || m.Authorize == nil || next == nil {
			writeIdempotencyError(w, http.StatusInternalServerError, "INTERNAL", "idempotency middleware is not configured", false, requestID)
			return
		}
		keyValues := r.Header.Values("Idempotency-Key")
		if len(keyValues) != 1 || !validIdempotencyKey(keyValues[0]) {
			writeIdempotencyError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "exactly one valid Idempotency-Key header is required", false, requestID)
			return
		}
		scope, err := m.Resolve(r)
		if err != nil || scope.TenantID == uuid.Nil || scope.Subject == "" || scope.Operation == "" {
			writeIdempotencyError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "authenticated tenant and subject are required", false, requestID)
			return
		}
		if err := m.Authorize(r, scope); err != nil {
			writeIdempotencyError(w, http.StatusForbidden, "FORBIDDEN", "request is not authorized", false, requestID)
			return
		}
		if scope.NoRedispatch {
			writeIdempotencyError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "execution requests require the single-dispatch execution boundary", false, requestID)
			return
		}
		body, err := readIdempotentRequestBody(r)
		if err != nil {
			status, message := http.StatusBadRequest, "request body could not be read"
			if errors.Is(err, errIdempotencyBodyTooLarge) {
				status, message = http.StatusRequestEntityTooLarge, "request body exceeds the idempotency limit"
			}
			writeIdempotencyError(w, status, "INVALID_ARGUMENT", message, false, requestID)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		digest, err := canonicalRequestDigest(scope, r, body)
		if err != nil {
			writeIdempotencyError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "request query is invalid", false, requestID)
			return
		}
		var output *capturedResponse
		var decision persistence.Decision
		err = persistence.WithTenantTx(r.Context(), m.Pool, scope.TenantID, func(tx pgx.Tx) error {
			if m.AuthorizeTx != nil {
				if err := m.AuthorizeTx(r, tx, scope); err != nil {
					output = idempotencyErrorResponse(http.StatusForbidden, "FORBIDDEN", "request is not authorized", false, requestID)
					return nil
				}
			}
			var beginErr error
			decision, beginErr = persistence.Begin(r.Context(), tx, scope, keyValues[0], digest)
			if beginErr != nil {
				return beginErr
			}
			switch decision.Kind {
			case persistence.DecisionReplay:
				output = capturedFromStored(decision.Response)
				return nil
			case persistence.DecisionConflict:
				output = idempotencyErrorResponse(http.StatusConflict, "IDEMPOTENCY_CONFLICT", "the key was already used for a different request", false, requestID)
				return nil
			case persistence.DecisionInProgress:
				if decision.ExecutionUnknown {
					output = idempotencyErrorResponse(http.StatusConflict, "EXECUTION_UNKNOWN", "the execution result is uncertain and will not be dispatched again", false, requestID)
				} else if decision.Terminal {
					output = idempotencyErrorResponse(http.StatusConflict, "IDEMPOTENCY_CONFLICT", "the request key has a terminal result", false, requestID)
				} else {
					output = idempotencyErrorResponse(http.StatusConflict, "IDEMPOTENCY_IN_PROGRESS", "a request with this key is still in progress", true, requestID)
					output.header.Set("Retry-After", "1")
				}
				return nil
			case persistence.DecisionProceed:
			default:
				return fmt.Errorf("unknown idempotency decision %q", decision.Kind)
			}

			if _, err := tx.Exec(r.Context(), `SAVEPOINT idempotency_business`); err != nil {
				return fmt.Errorf("start idempotent business savepoint: %w", err)
			}
			capture := newCapturedResponse()
			handlerCtx := context.WithValue(r.Context(), transactionContextKey{}, tx)
			next.ServeHTTP(capture, r.WithContext(handlerCtx))
			capture.finish()
			if capture.overflow || capture.status >= http.StatusInternalServerError {
				if _, err := tx.Exec(r.Context(), `ROLLBACK TO SAVEPOINT idempotency_business`); err != nil {
					return fmt.Errorf("rollback failed idempotent write: %w", err)
				}
				if _, err := tx.Exec(r.Context(), `RELEASE SAVEPOINT idempotency_business`); err != nil {
					return fmt.Errorf("release idempotent business savepoint: %w", err)
				}
				if err := persistence.Fail(r.Context(), tx, decision.Lease, true); err != nil {
					return err
				}
				if capture.overflow {
					output = idempotencyErrorResponse(http.StatusInternalServerError, "INTERNAL", "response exceeds the idempotency limit", true, requestID)
				} else {
					output = capture
				}
				return nil
			}
			if _, err := tx.Exec(r.Context(), `RELEASE SAVEPOINT idempotency_business`); err != nil {
				return fmt.Errorf("release idempotent business savepoint: %w", err)
			}
			stored, err := capture.storedResponse()
			if err != nil {
				return err
			}
			if err := persistence.Complete(r.Context(), tx, decision.Lease, stored); err != nil {
				return err
			}
			output = capture
			return nil
		})
		if err != nil {
			output = idempotencyErrorResponse(http.StatusInternalServerError, "INTERNAL", "the idempotent request could not be committed", true, requestID)
		}
		if output == nil {
			output = idempotencyErrorResponse(http.StatusInternalServerError, "INTERNAL", "the idempotent request produced no response", true, requestID)
		}
		output.writeTo(w)
	})
}

func readIdempotentRequestBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxIdempotentRequestBody+1))
	_ = r.Body.Close()
	if err != nil {
		return nil, err
	}
	if len(body) > maxIdempotentRequestBody {
		return nil, errIdempotencyBodyTooLarge
	}
	return body, nil
}

var errIdempotencyBodyTooLarge = errors.New("request body exceeds configured limit")

func canonicalRequestDigest(scope persistence.Scope, r *http.Request, body []byte) (persistence.Digest, error) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return "", err
	}
	canonicalQuery := query.Encode()
	contentType := r.Header.Get("Content-Type")
	if len(body) > 0 {
		mediaType, _, parseErr := mime.ParseMediaType(contentType)
		if parseErr != nil {
			return "", parseErr
		}
		if mediaType == "application/json" || strings.HasSuffix(mediaType, "+json") {
			body, err = bundle.CanonicalizeJSON(body)
			if err != nil {
				return "", err
			}
		}
		contentType = mediaType
	}
	bodyHash := sha256.Sum256(body)
	canonical := strings.Join([]string{
		strings.ToUpper(r.Method), scope.Operation, r.URL.EscapedPath(), canonicalQuery,
		contentType, r.Header.Get("Accept"), hex.EncodeToString(bodyHash[:]),
	}, "\n")
	digest := sha256.Sum256([]byte(canonical))
	return persistence.Digest("sha256:" + hex.EncodeToString(digest[:])), nil
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

type capturedResponse struct {
	header   http.Header
	status   int
	body     bytes.Buffer
	overflow bool
}

func newCapturedResponse() *capturedResponse {
	return &capturedResponse{header: make(http.Header)}
}

func (c *capturedResponse) Header() http.Header { return c.header }

func (c *capturedResponse) WriteHeader(status int) {
	if c.status == 0 {
		c.status = status
	}
}

func (c *capturedResponse) Write(body []byte) (int, error) {
	if c.status == 0 {
		c.status = http.StatusOK
	}
	remaining := maxIdempotentResponseBody - c.body.Len()
	if remaining <= 0 || len(body) > remaining {
		c.overflow = true
		if remaining > 0 {
			_, _ = c.body.Write(body[:remaining])
		}
		return len(body), nil
	}
	return c.body.Write(body)
}

func (c *capturedResponse) finish() {
	if c.status == 0 {
		c.status = http.StatusOK
	}
}

func (c *capturedResponse) storedResponse() (persistence.StoredResponse, error) {
	contentType := c.header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "application/json" {
		return persistence.StoredResponse{}, errors.New("idempotent writes must return application/json")
	}
	var headers = make(map[string]string)
	for _, name := range []string{"Cache-Control", "Content-Language", "ETag", "Location"} {
		if value := c.header.Get(name); value != "" {
			headers[name] = value
		}
	}
	return persistence.StoredResponse{Status: c.status, ContentType: "application/json", Headers: headers, Body: bytes.Clone(c.body.Bytes())}, nil
}

func (c *capturedResponse) writeTo(w http.ResponseWriter) {
	for name, values := range c.header {
		for _, value := range values {
			w.Header().Add(name, value)
		}
	}
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(c.status)
	if c.status != http.StatusNoContent && c.status != http.StatusResetContent && c.status != http.StatusNotModified {
		_, _ = w.Write(c.body.Bytes())
	}
}

func capturedFromStored(response persistence.StoredResponse) *capturedResponse {
	capture := newCapturedResponse()
	capture.status = response.Status
	capture.header.Set("Content-Type", response.ContentType)
	for name, value := range response.Headers {
		capture.header.Set(name, value)
	}
	_, _ = capture.body.Write(response.Body)
	return capture
}

func idempotencyErrorResponse(status int, code, message string, retryable bool, requestID string) *capturedResponse {
	body, _ := json.Marshal(generated.ErrorEnvelope{
		Code: generated.ErrorEnvelopeCode(code), Message: message, RequestId: auth.RequestIDFromHeader(requestID), Retryable: retryable,
	})
	capture := newCapturedResponse()
	capture.status = status
	capture.header.Set("Content-Type", "application/json")
	_, _ = capture.body.Write(body)
	return capture
}

func writeIdempotencyError(w http.ResponseWriter, status int, code, message string, retryable bool, requestID string) {
	idempotencyErrorResponse(status, code, message, retryable, requestID).writeTo(w)
}
