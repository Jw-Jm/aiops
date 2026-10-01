package integration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/persistence"
)

func TestIdempotencyLedgerSchema(t *testing.T) {
	ctx, db, migrationDir, dbURL := newMigrationDatabase(t)
	if err := goose.UpToContext(ctx, db, migrationDir, 1); err != nil {
		t.Fatalf("apply privileged role bootstrap: %v", err)
	}
	if err := runRemainingMigrationsAsMigrationRole(t, ctx, db, dbURL, migrationDir); err != nil {
		t.Fatalf("apply current schema as migration_role: %v", err)
	}
	var relation sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT to_regclass('platform.idempotency_request_ledger')::text`).Scan(&relation); err != nil {
		t.Fatal(err)
	}
	if !relation.Valid {
		t.Fatal("unified request idempotency ledger does not exist")
	}
}

func TestIdempotencyLedger(t *testing.T) {
	ctx, db, migrationDir, dbURL := newMigrationDatabase(t)
	if err := goose.UpToContext(ctx, db, migrationDir, 1); err != nil {
		t.Fatalf("apply privileged role bootstrap: %v", err)
	}
	if err := runRemainingMigrationsAsMigrationRole(t, ctx, db, dbURL, migrationDir); err != nil {
		t.Fatalf("apply current schema as migration_role: %v", err)
	}
	tenantA := uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987301")
	tenantB := uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987302")
	for _, tenantID := range []uuid.UUID{tenantA, tenantB} {
		if _, err := db.ExecContext(ctx, `INSERT INTO platform.tenants (tenant_id, slug, display_name) VALUES ($1, $2, $2)`, tenantID, tenantID.String()); err != nil {
			t.Fatalf("create tenant: %v", err)
		}
	}

	poolConfig := runtimePoolConfig(t, ctx, db, dbURL, "api_runtime_role")
	poolConfig.MaxConns = 24
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	scope := persistence.Scope{TenantID: tenantA, Subject: "operator-a", Operation: "POST /api/v1/admin/source-registrations"}
	key := "concurrent-write-01"
	requestDigest := testDigest("same canonical request")
	const concurrentRequests = 24
	start := make(chan struct{})
	results := make(chan persistence.Decision, concurrentRequests)
	concurrentErrors := make(chan error, concurrentRequests)
	var wait sync.WaitGroup
	for i := 0; i < concurrentRequests; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			var decision persistence.Decision
			err := persistence.WithTenantTx(ctx, pool, tenantA, func(tx pgx.Tx) error {
				var beginErr error
				decision, beginErr = persistence.Begin(ctx, tx, scope, key, requestDigest)
				return beginErr
			})
			if err != nil {
				concurrentErrors <- err
				return
			}
			results <- decision
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	close(concurrentErrors)
	for err := range concurrentErrors {
		t.Errorf("concurrent Begin: %v", err)
	}
	var proceeding, inProgress int
	var firstLease persistence.Lease
	for decision := range results {
		switch decision.Kind {
		case persistence.DecisionProceed:
			proceeding++
			firstLease = decision.Lease
		case persistence.DecisionInProgress:
			inProgress++
		default:
			t.Errorf("unexpected concurrent decision: %q", decision.Kind)
		}
	}
	if proceeding != 1 || inProgress != concurrentRequests-1 {
		t.Fatalf("same-key concurrent Begin dispatched more than once: proceed=%d in_progress=%d", proceeding, inProgress)
	}

	response := persistence.StoredResponse{Status: 201, ContentType: "application/json", Body: []byte(`{"sourceId":"018f0f2b-91c2-7d42-a8dc-f719c5987399"}`)}
	if err := persistence.WithTenantTx(ctx, pool, tenantA, func(tx pgx.Tx) error {
		return persistence.Complete(ctx, tx, firstLease, response)
	}); err != nil {
		t.Fatalf("complete request: %v", err)
	}
	if err := persistence.WithTenantTx(ctx, pool, tenantA, func(tx pgx.Tx) error {
		decision, err := persistence.Begin(ctx, tx, scope, key, requestDigest)
		if err != nil {
			return err
		}
		if decision.Kind != persistence.DecisionReplay || decision.Response.Status != response.Status || string(decision.Response.Body) != string(response.Body) {
			return fmt.Errorf("completed write did not replay original response: %#v", decision)
		}
		return nil
	}); err != nil {
		t.Fatalf("completed request replay: %v", err)
	}
	if err := persistence.WithTenantTx(ctx, pool, tenantA, func(tx pgx.Tx) error {
		decision, err := persistence.Begin(ctx, tx, scope, key, testDigest("different canonical request"))
		if err != nil {
			return err
		}
		if decision.Kind != persistence.DecisionConflict {
			return fmt.Errorf("same key with different request digest returned %q", decision.Kind)
		}
		return nil
	}); err != nil {
		t.Fatalf("request digest conflict: %v", err)
	}

	for _, isolatedScope := range []persistence.Scope{
		{TenantID: tenantB, Subject: scope.Subject, Operation: scope.Operation},
		{TenantID: tenantA, Subject: "operator-b", Operation: scope.Operation},
	} {
		if err := persistence.WithTenantTx(ctx, pool, isolatedScope.TenantID, func(tx pgx.Tx) error {
			decision, err := persistence.Begin(ctx, tx, isolatedScope, key, requestDigest)
			if err != nil {
				return err
			}
			if decision.Kind != persistence.DecisionProceed {
				return fmt.Errorf("tenant/subject scope was not isolated: %q", decision.Kind)
			}
			return nil
		}); err != nil {
			t.Fatalf("tenant/subject idempotency scope: %v", err)
		}
	}

	recoveryKey := "recover-after-worker-crash"
	var staleLease persistence.Lease
	if err := persistence.WithTenantTx(ctx, pool, tenantA, func(tx pgx.Tx) error {
		decision, err := persistence.Begin(ctx, tx, scope, recoveryKey, requestDigest)
		staleLease = decision.Lease
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE platform.idempotency_request_ledger SET lease_expires_at = clock_timestamp() - interval '1 second' WHERE tenant_id = $1 AND idempotency_key_digest = $2`, tenantA, persistence.HashIdempotencyKey(recoveryKey)); err != nil {
		t.Fatal(err)
	}
	var recoveryLease persistence.Lease
	if err := persistence.WithTenantTx(ctx, pool, tenantA, func(tx pgx.Tx) error {
		decision, err := persistence.Begin(ctx, tx, scope, recoveryKey, requestDigest)
		if err == nil && decision.Kind != persistence.DecisionProceed {
			return fmt.Errorf("expired transactional request lease did not recover: %q", decision.Kind)
		}
		recoveryLease = decision.Lease
		return err
	}); err != nil {
		t.Fatalf("recover expired transactional request: %v", err)
	}
	if recoveryLease.Token == staleLease.Token || recoveryLease.Attempt <= staleLease.Attempt {
		t.Fatalf("recovery reused stale lease: old=%#v new=%#v", staleLease, recoveryLease)
	}
	if err := persistence.WithTenantTx(ctx, pool, tenantA, func(tx pgx.Tx) error {
		if err := persistence.Complete(ctx, tx, staleLease, response); err == nil {
			return fmt.Errorf("stale worker lease completed after recovery")
		}
		return nil
	}); err != nil {
		t.Fatalf("stale lease fencing: %v", err)
	}
	if err := persistence.WithTenantTx(ctx, pool, tenantA, func(tx pgx.Tx) error {
		return persistence.Fail(ctx, tx, recoveryLease, true)
	}); err != nil {
		t.Fatalf("mark recoverable failure: %v", err)
	}

	executionScope := persistence.Scope{TenantID: tenantA, Subject: scope.Subject, Operation: "POST /api/v1/command-executions", NoRedispatch: true}
	executionKey := "execution-once-01"
	var dispatches atomic.Int32
	executed, err := persistence.DispatchExecutionOnce(ctx, pool, executionScope, executionKey, requestDigest, func(context.Context) (persistence.StoredResponse, error) {
		dispatches.Add(1)
		return response, nil
	})
	if err != nil || executed.Kind != persistence.DecisionReplay {
		t.Fatalf("first execution request did not complete one dispatch: decision=%#v err=%v", executed, err)
	}
	replayed, err := persistence.DispatchExecutionOnce(ctx, pool, executionScope, executionKey, requestDigest, func(context.Context) (persistence.StoredResponse, error) {
		dispatches.Add(1)
		return response, nil
	})
	if err != nil || replayed.Kind != persistence.DecisionReplay {
		t.Fatalf("completed execution did not replay: decision=%#v err=%v", replayed, err)
	}
	if dispatches.Load() != 1 {
		t.Fatalf("execution was dispatched %d times after replay", dispatches.Load())
	}

	crashKey := "execution-crash-after-dispatch"
	uncertain, err := persistence.DispatchExecutionOnce(ctx, pool, executionScope, crashKey, requestDigest, func(context.Context) (persistence.StoredResponse, error) {
		dispatches.Add(1)
		return persistence.StoredResponse{}, errors.New("dispatcher stopped after an uncertain external handoff")
	})
	if err == nil || uncertain.Kind != persistence.DecisionInProgress || !uncertain.ExecutionUnknown {
		t.Fatalf("uncertain execution was not fenced as unknown: decision=%#v err=%v", uncertain, err)
	}
	uncertain, err = persistence.DispatchExecutionOnce(ctx, pool, executionScope, crashKey, requestDigest, func(context.Context) (persistence.StoredResponse, error) {
		dispatches.Add(1)
		return response, nil
	})
	if err != nil || uncertain.Kind != persistence.DecisionInProgress || !uncertain.ExecutionUnknown {
		t.Fatalf("unknown execution was redispatched on recovery: decision=%#v err=%v", uncertain, err)
	}
	if dispatches.Load() != 2 {
		t.Fatalf("execution request dispatched %d times across crash recovery; want 2 total independent requests", dispatches.Load())
	}
	if _, err := db.ExecContext(ctx, `UPDATE platform.idempotency_request_ledger SET response_expires_at = clock_timestamp() - interval '91 days' WHERE tenant_id = $1 AND subject = $2 AND operation = $3 AND idempotency_key_digest = $4`, tenantA, scope.Subject, scope.Operation, persistence.HashIdempotencyKey(key)); err != nil {
		t.Fatal(err)
	}
	var removed int
	if err := persistence.WithTenantTx(ctx, pool, tenantA, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT platform.delete_expired_idempotency($1, clock_timestamp(), 100)`, tenantA).Scan(&removed)
	}); err != nil {
		t.Fatalf("cleanup expired idempotency response: %v", err)
	}
	if removed != 1 {
		t.Fatalf("cleanup removed %d expired records, expected 1", removed)
	}
	if _, err := db.ExecContext(ctx, `UPDATE platform.idempotency_request_ledger SET response_expires_at = clock_timestamp() - interval '91 days' WHERE tenant_id = $1 AND subject = $2 AND operation = $3 AND idempotency_key_digest = $4`, tenantA, executionScope.Subject, executionScope.Operation, persistence.HashIdempotencyKey(executionKey)); err != nil {
		t.Fatal(err)
	}
	removed = 0
	if err := persistence.WithTenantTx(ctx, pool, tenantA, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT platform.delete_expired_idempotency($1, clock_timestamp(), 100)`, tenantA).Scan(&removed)
	}); err != nil {
		t.Fatalf("cleanup retained execution-once record: %v", err)
	}
	if removed != 0 {
		t.Fatalf("cleanup removed %d execution-once ledger records; those keys must remain fenced", removed)
	}
}

func TestIdempotencyHTTPMiddleware(t *testing.T) {
	ctx, db, migrationDir, dbURL := newMigrationDatabase(t)
	if err := goose.UpToContext(ctx, db, migrationDir, 1); err != nil {
		t.Fatalf("apply privileged role bootstrap: %v", err)
	}
	if err := runRemainingMigrationsAsMigrationRole(t, ctx, db, dbURL, migrationDir); err != nil {
		t.Fatalf("apply current schema as migration_role: %v", err)
	}
	tenantID := uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987310")
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.tenants (tenant_id, slug, display_name) VALUES ($1, $2, $2)`, tenantID, tenantID.String()); err != nil {
		t.Fatal(err)
	}
	poolConfig := runtimePoolConfig(t, ctx, db, dbURL, "api_runtime_role")

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	var calls atomic.Int32
	var authorized atomic.Bool
	authorized.Store(true)
	resolver := func(*http.Request) (persistence.Scope, error) {
		return persistence.Scope{TenantID: tenantID, Subject: "operator-a", Operation: "create-source-registration"}, nil
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := httpapi.TransactionFromContext(r.Context()); !ok {
			http.Error(w, "tenant transaction missing", http.StatusInternalServerError)
			return
		}
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Location", "/api/v1/source-registrations/018f0f2b-91c2-7d42-a8dc-f719c5987399")
		w.Header().Set("Set-Cookie", "session=must-not-be-replayed")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"created":true}`)
	})
	middleware := httpapi.IdempotencyMiddleware{
		Pool: pool, Resolve: resolver,
		Authorize: func(*http.Request, persistence.Scope) error {
			if !authorized.Load() {
				return errors.New("route authorization denied")
			}
			return nil
		},
	}
	wrapped := middleware.Wrap(handler)
	request := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/source-registrations?zone=a", strings.NewReader(body))
		req.Header.Set("Idempotency-Key", "middleware-request-01")
		req.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		wrapped.ServeHTTP(recorder, req)
		return recorder
	}
	first := request(`{"source":"one"}`)
	authorized.Store(false)
	second := request(`{"source":"one"}`)
	if first.Code != http.StatusCreated || second.Code != http.StatusForbidden || calls.Load() != 1 {
		t.Fatalf("replay bypassed current route authorization: first=%d second=%d handler_calls=%d", first.Code, second.Code, calls.Load())
	}
	authorized.Store(true)
	second = request(`{"source":"one"}`)
	if second.Code != http.StatusCreated || first.Body.String() != second.Body.String() || calls.Load() != 1 {
		t.Fatalf("idempotent response was not replayed: first=%d %s second=%d %s handler_calls=%d", first.Code, first.Body.String(), second.Code, second.Body.String(), calls.Load())
	}
	if second.Header().Get("Location") == "" || second.Header().Get("Set-Cookie") != "" {
		t.Fatalf("response replay did not preserve only safe headers: location=%q set-cookie=%q", second.Header().Get("Location"), second.Header().Get("Set-Cookie"))
	}
	conflict := request(`{"source":"two"}`)
	if conflict.Code != http.StatusConflict || calls.Load() != 1 || !strings.Contains(conflict.Body.String(), "IDEMPOTENCY_CONFLICT") {
		t.Fatalf("same key with a different body was not rejected before dispatch: status=%d calls=%d body=%s", conflict.Code, calls.Load(), conflict.Body.String())
	}
	// Model another request holding a live lease without redispatching business
	// work. The retry response must guide the client and retain its error code.
	if _, err := db.ExecContext(ctx, `UPDATE platform.idempotency_request_ledger
		SET state='in_progress', lease_token=$2, lease_expires_at=clock_timestamp()+interval '30 seconds',
		response_status=NULL, response_content_type=NULL, response_headers='{}', response_body=NULL, response_expires_at=NULL
		WHERE tenant_id=$1 AND subject='operator-a' AND operation='create-source-registration'`, tenantID, uuid.New()); err != nil {
		t.Fatal(err)
	}
	inProgress := request(`{"source":"one"}`)
	if inProgress.Code != http.StatusConflict || inProgress.Header().Get("Retry-After") != "1" || calls.Load() != 1 || !strings.Contains(inProgress.Body.String(), "IDEMPOTENCY_IN_PROGRESS") {
		t.Fatalf("in-progress retry lost its delay or redispatched work: status=%d headers=%v body=%s", inProgress.Code, inProgress.Header(), inProgress.Body.String())
	}

	draftID := uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987320")
	var draftCalls atomic.Int32
	draftHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tx, ok := httpapi.TransactionFromContext(r.Context())
		if !ok {
			http.Error(w, "tenant transaction missing", http.StatusInternalServerError)
			return
		}
		if _, err := tx.Exec(r.Context(), `INSERT INTO platform.registry_drafts (tenant_id, draft_id, kind, logical_name, content, updated_by) VALUES ($1, $2, 'tool', 'middleware-test', '{}'::jsonb, 'operator-a')`, tenantID, draftID); err != nil {
			http.Error(w, "registry write failed", http.StatusInternalServerError)
			return
		}
		if draftCalls.Add(1) == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"failed":true}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"draftCreated":true}`)
	})
	draftMiddleware := httpapi.IdempotencyMiddleware{
		Pool: pool,
		Resolve: func(*http.Request) (persistence.Scope, error) {
			return persistence.Scope{TenantID: tenantID, Subject: "operator-a", Operation: "create-registry-draft"}, nil
		},
		Authorize: func(*http.Request, persistence.Scope) error { return nil },
	}
	draftWrapped := draftMiddleware.Wrap(draftHandler)
	requestDraft := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/tool-drafts", strings.NewReader(`{"kind":"tool"}`))
		req.Header.Set("Idempotency-Key", "middleware-retry-01")
		req.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		draftWrapped.ServeHTTP(recorder, req)
		return recorder
	}
	failed := requestDraft()
	var draftCount int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM platform.registry_drafts WHERE tenant_id = $1 AND draft_id = $2`, tenantID, draftID).Scan(&draftCount); err != nil {
		t.Fatal(err)
	}
	if failed.Code != http.StatusInternalServerError || draftCount != 0 {
		t.Fatalf("failed write was not rolled back to its savepoint: status=%d row_count=%d", failed.Code, draftCount)
	}
	retried := requestDraft()
	replayedDraft := requestDraft()
	if retried.Code != http.StatusCreated || replayedDraft.Code != http.StatusCreated || retried.Body.String() != replayedDraft.Body.String() || draftCalls.Load() != 2 {
		t.Fatalf("retry did not commit once and replay: retry=%d %s replay=%d %s handler_calls=%d", retried.Code, retried.Body.String(), replayedDraft.Code, replayedDraft.Body.String(), draftCalls.Load())
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM platform.registry_drafts WHERE tenant_id = $1 AND draft_id = $2`, tenantID, draftID).Scan(&draftCount); err != nil {
		t.Fatal(err)
	}
	if draftCount != 1 {
		t.Fatalf("idempotent retry committed %d business rows, expected one", draftCount)
	}
}

func testDigest(value string) persistence.Digest {
	digest := sha256.Sum256([]byte(value))
	return persistence.Digest("sha256:" + hex.EncodeToString(digest[:]))
}
