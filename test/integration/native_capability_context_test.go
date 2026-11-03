package integration

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"ops-platform/internal/graph"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/investigation"
)

// Exercise the interactive capability transport without creating an AI Job or
// inventing a source qualification. The worker returns an empty capability set
// only after the signed native session has been re-proven against real PG.
func TestNativeInteractiveCapabilityContext(t *testing.T) {
	f := newNativeJobFixture(t)
	account := f.newAccount(t, `"apps"`)
	scope, err := (graph.Authorization{Pool: f.apiPool}).Effective(account.ctx, f.tenantID.String(), account.subject, f.clusterUID)
	if err != nil {
		t.Fatal(err)
	}
	public, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	worker := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 65537))
		claims, err := graph.VerifyContext(public, r.Header.Get("X-Graph-Context"), "platform-analysis-query", raw)
		if err != nil || claims.Source != graph.SourceNativeInteractive || claims.Native == nil || claims.Native.SessionID != account.sessionID.String() {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		current, err := (graph.Authorization{Pool: f.workerPool}).EffectiveClaim(r.Context(), claims)
		if err != nil || !claims.AllowsCurrent(current) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"sourceCapabilities": []any{}, "networkCapabilities": []any{}, "scopeDigest": graph.ScopeDigest(scope)})
	}))
	defer worker.Close()
	if _, err = f.db.ExecContext(f.ctx, `INSERT INTO platform.graph_ownership
		(tenant_id,cluster_uid,owner_epoch,owner_instance,owner_endpoint,lease_uid,route_expires_at)
		VALUES($1,$2,1,'r23-capability',$3,$4,clock_timestamp()+interval '5 minutes')`,
		f.tenantID, f.clusterUID, worker.URL, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	h := httpapi.SP04Handlers{Pool: f.apiPool, SigningKey: key, Client: worker.Client(), EndpointAllowed: func(endpoint string) bool { return endpoint == worker.URL }}
	job := investigation.Job{TenantID: f.tenantID, Subject: account.subject, Scope: scope,
		Assistant: &investigation.AssistantTask{GrantSnapshotDigest: graph.ScopeDigest(scope)}}
	if result, err := h.AssistantCapabilityDetails(account.ctx, job); err != nil || result == nil || result.ScopeDigest != graph.ScopeDigest(scope) {
		t.Fatalf("interactive capabilities = %+v (%v)", result, err)
	}
	for _, source := range []string{investigation.AuditSourceNative, "unknown"} {
		job.Assistant.AuthorizationSource = source
		if _, err := h.AssistantCapabilityDetails(account.ctx, job); err == nil {
			t.Fatalf("source %q without a persisted Job was downgraded", source)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("invalid authority reached the worker: calls=%d", calls.Load())
	}
	job.Assistant.AuthorizationSource = ""
	if _, err = f.db.ExecContext(f.ctx, `UPDATE identity.role_grants SET status='revoked', revoked_at=clock_timestamp(), revoked_reason='r23' WHERE tenant_id=$1 AND user_id=$2 AND role='operator'`, f.tenantID, account.userID); err != nil {
		t.Fatal(err)
	}
	if _, err = h.AssistantCapabilityDetails(account.ctx, job); err == nil {
		t.Fatal("revoked native capability request succeeded")
	}
}
