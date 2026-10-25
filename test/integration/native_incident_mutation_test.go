package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"ops-platform/internal/auth"
	"ops-platform/internal/graph"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/incident"
	"ops-platform/internal/persistence"
	"ops-platform/internal/resource"
)

// incidentMutationClient drives the real SP05 mutation handler: path rewriting,
// the shared idempotency transaction, the HTTP-time authorization and the business
// write transaction. The native session context is the one the middleware would
// have produced for this account; no browser is involved.
func (f *nativeJobFixture) incidentMutationClient(t *testing.T, account nativeJobAccount) func(method, path string, body any, key string) (int, []byte) {
	t.Helper()
	handler := &httpapi.SP05Handlers{Pool: f.apiPool, Enabled: true}
	ctx := auth.WithRequestContext(account.ctx, auth.RequestContext{
		TenantID: f.tenantID, Subject: account.subject,
		Roles: []auth.Role{auth.Operator}, RequestID: "native-incident-mutation",
	})
	return func(method, path string, body any, key string) (int, []byte) {
		var payload []byte
		if body != nil {
			payload, _ = json.Marshal(body)
		}
		request := httptest.NewRequest(method, path, bytes.NewReader(payload))
		request.Header.Set("Content-Type", "application/json")
		if key != "" {
			request.Header.Set("Idempotency-Key", key)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request.WithContext(ctx))
		return recorder.Code, recorder.Body.Bytes()
	}
}

func (f *nativeJobFixture) incidentRevision(t *testing.T, id uuid.UUID) int64 {
	t.Helper()
	var revision int64
	if err := f.db.QueryRowContext(f.ctx, `SELECT revision FROM incident.records WHERE tenant_id=$1 AND incident_id=$2`, f.tenantID, id).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	return revision
}

// A native operator with no legacy mirror confirms, mitigates (with retained
// manual Evidence), suppresses and closes Incidents through the real HTTP write
// path. Every replay returns the frozen response and applies the mutation once;
// audit and timeline rows name the native subject.
func TestNativeIncidentMutationAuthority(t *testing.T) {
	f := newNativeJobFixture(t)
	seed := f.newIncident(t)
	account := f.newAccount(t, `"apps"`)
	f.clearLegacyBindings(t)
	call := f.incidentMutationClient(t, account)
	path := "/api/v1/incidents/" + seed.IncidentID.String() + ":transition"

	// Acknowledge, then replay the exact same idempotency key.
	ackKey := uuid.NewString()
	ack := map[string]any{"expectedRevision": seed.Revision, "state": "acknowledged", "reason": "native acknowledge"}
	status, first := call("POST", path, ack, ackKey)
	if status != 200 {
		t.Fatalf("native acknowledge = %d %s", status, first)
	}
	status, replayed := call("POST", path, ack, ackKey)
	if status != 200 || !bytes.Equal(first, replayed) {
		t.Fatalf("acknowledge replay = %d %s", status, replayed)
	}
	if got := f.incidentRevision(t, seed.IncidentID); got != seed.Revision+1 {
		t.Fatalf("acknowledge revision = %d want %d", got, seed.Revision+1)
	}

	// Mitigation retaining one manual Evidence row exercises the same authority for
	// the Evidence resource (source revision, namespace and resource identity).
	evidenceID := f.newEvidence(t, seed, false, "resource_state")
	mitigate := map[string]any{"expectedRevision": seed.Revision + 1, "state": "mitigating", "reason": "native mitigate", "evidenceRefs": []string{evidenceID.String()}}
	status, body := call("POST", path, mitigate, uuid.NewString())
	if status != 200 {
		t.Fatalf("native mitigate = %d %s", status, body)
	}
	var retained int
	if err := f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM platform.evidence_retention_references WHERE tenant_id=$1 AND reference_kind='incident' AND reference_id=$2 AND evidence_id=$3`, f.tenantID, seed.IncidentID, evidenceID).Scan(&retained); err != nil || retained != 1 {
		t.Fatalf("manual evidence retention = %d (%v)", retained, err)
	}

	// The linked Findings resolve, so an expired suppression may close the Incident.
	f.resolveFindings(t, seed)
	until := time.Now().UTC().Add(2 * time.Second)
	suppress := map[string]any{"expectedRevision": seed.Revision + 2, "state": "suppressed", "reason": "native suppress", "suppressedUntil": until}
	status, body = call("POST", path, suppress, uuid.NewString())
	if status != 200 {
		t.Fatalf("native suppress = %d %s", status, body)
	}
	time.Sleep(2100 * time.Millisecond)
	closeBody := map[string]any{"expectedRevision": seed.Revision + 3, "state": "closed", "reason": "native close"}
	status, body = call("POST", path, closeBody, uuid.NewString())
	if status != 200 {
		t.Fatalf("native close = %d %s", status, body)
	}
	if got := f.incidentRevision(t, seed.IncidentID); got != seed.Revision+4 {
		t.Fatalf("final revision = %d want %d", got, seed.Revision+4)
	}

	// Audit and timeline are written under the native subject.
	var audits, timelineRows int
	if err := f.db.QueryRowContext(f.ctx, `SELECT (SELECT count(*) FROM audit.records WHERE tenant_id=$1 AND entity_kind='incident' AND entity_id=$2 AND event_type='incident.override' AND subject=$3), (SELECT count(*) FROM incident.timeline WHERE tenant_id=$1 AND incident_id=$2 AND kind='manual_override')`, f.tenantID, seed.IncidentID, account.subject).Scan(&audits, &timelineRows); err != nil {
		t.Fatal(err)
	}
	if audits != 4 || timelineRows != 4 {
		t.Fatalf("audit=%d timeline=%d want 4/4", audits, timelineRows)
	}
	var bindings int
	if err := f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM platform.role_bindings WHERE tenant_id=$1 AND subject=$2`, f.tenantID, account.subject).Scan(&bindings); err != nil || bindings != 0 {
		t.Fatalf("native subject has a legacy binding: %d (%v)", bindings, err)
	}
}

// Merge and split are deliberate operator decisions: both Incidents, their linked
// Findings and the replayed response are re-authorized in the write transaction.
func TestNativeIncidentNativeMergeSplit(t *testing.T) {
	f := newNativeJobFixture(t)
	first := f.newIncident(t)
	second := f.newIncidentFamily(t, "node", "MemoryPressure", "worker-1")
	account := f.newAccount(t, `"apps"`)
	f.clearLegacyBindings(t)
	call := f.incidentMutationClient(t, account)

	merge := map[string]any{"sourceIncidentId": second.IncidentID.String(), "targetIncidentId": first.IncidentID.String(), "expectedRevision": second.Revision, "targetRevision": first.Revision, "reason": "native merge"}
	mergeKey := uuid.NewString()
	status, merged := call("POST", "/api/v1/incidents:merge", merge, mergeKey)
	if status != 200 {
		t.Fatalf("native merge = %d %s", status, merged)
	}
	status, replay := call("POST", "/api/v1/incidents:merge", merge, mergeKey)
	if status != 200 || !bytes.Equal(merged, replay) {
		t.Fatalf("merge replay = %d %s", status, replay)
	}
	if got := f.incidentRevision(t, first.IncidentID); got != first.Revision+1 {
		t.Fatalf("merge applied %d times", got-first.Revision)
	}
	if got := f.incidentRevision(t, second.IncidentID); got != second.Revision+1 {
		t.Fatalf("merged source revision = %d", got)
	}

	third := f.newIncidentFamily(t, "disk", "DiskPressure", "worker-2")
	split := map[string]any{"expectedRevision": third.Revision, "findingIds": []string{third.Findings[0].String()}, "reason": "native split"}
	splitPath := "/api/v1/incidents/" + third.IncidentID.String() + ":split"
	splitKey := uuid.NewString()
	status, splitted := call("POST", splitPath, split, splitKey)
	if status != 200 {
		t.Fatalf("native split = %d %s", status, splitted)
	}
	status, replay = call("POST", splitPath, split, splitKey)
	if status != 200 || !bytes.Equal(splitted, replay) {
		t.Fatalf("split replay = %d %s", status, replay)
	}
	if got := f.incidentRevision(t, third.IncidentID); got != third.Revision+1 {
		t.Fatalf("split applied %d times", got-third.Revision)
	}
}

// Every authorization shape the shared boundary can see must be refused without a
// business write: forgotten namespace/cluster/tenant, a narrowed grant, a disabled
// account, revocation (including replay of a previously accepted request) and a
// concurrent administrative write waiting for the decision locks.
func TestNativeIncidentMutationDenied(t *testing.T) {
	f := newNativeJobFixture(t)
	seed := f.newIncident(t)
	account := f.newAccount(t, `"apps"`)
	f.clearLegacyBindings(t)
	call := f.incidentMutationClient(t, account)
	path := "/api/v1/incidents/" + seed.IncidentID.String() + ":transition"

	// The frozen target must be inside the presented scope, whatever the caller
	// claims. These cases exercise the shared boundary directly so each mismatch is
	// attributed, not merely reported as a generic 403.
	scopeCases := []struct {
		name  string
		scope graph.Scope
	}{
		{"foreign tenant", graph.Scope{Tenant: uuid.Must(uuid.NewV7()).String(), Cluster: f.clusterUID, Namespaces: []string{"apps"}}},
		{"foreign cluster", graph.Scope{Tenant: f.tenantID.String(), Cluster: "other-cluster", Namespaces: []string{"apps"}}},
		{"foreign namespace", graph.Scope{Tenant: f.tenantID.String(), Cluster: f.clusterUID, Namespaces: []string{"private"}}},
		{"foreign resource", graph.Scope{Tenant: f.tenantID.String(), Cluster: f.clusterUID, Namespaces: []string{"apps"}, Resources: []string{resource.CanonicalID{Domain: "k8s", Tenant: f.tenantID.String(), Scope: f.clusterUID, APIGroup: "core", Kind: "Pod", StableID: "other"}.String()}}},
	}
	for _, tc := range scopeCases {
		err := persistence.WithTenantTx(f.ctx, f.apiPool, f.tenantID, func(tx pgx.Tx) error {
			item, err := incident.Load(f.ctx, tx, f.tenantID, seed.IncidentID.String(), false)
			if err != nil {
				return err
			}
			return incident.Authorize(f.ctx, tx, tc.scope, account.subject, item)
		})
		if !errors.Is(err, graph.ErrScope) {
			t.Fatalf("%s authorization = %v", tc.name, err)
		}
	}

	// A native operator whose grant only covers another namespace cannot mutate
	// this Incident through the real HTTP path.
	narrowed := f.newAccount(t, `"private"`)
	narrowedCall := f.incidentMutationClient(t, narrowed)
	status, body := narrowedCall("POST", path, map[string]any{"expectedRevision": seed.Revision, "state": "acknowledged", "reason": "must be denied"}, uuid.NewString())
	if status != 403 {
		t.Fatalf("narrowed grant transition = %d %s", status, body)
	}

	// Accepted request, then revocation: the same idempotency key must be refused
	// by current authority instead of replaying the stored response.
	accepted := map[string]any{"expectedRevision": seed.Revision, "state": "acknowledged", "reason": "native accepted before revocation"}
	key := uuid.NewString()
	if status, body = call("POST", path, accepted, key); status != 200 {
		t.Fatalf("accepted transition = %d %s", status, body)
	}
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.role_grants SET status='revoked',revoked_at=clock_timestamp(),revoked_reason='mutation replay',authorization_version=authorization_version+1 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, account.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.accounts SET authorization_version=authorization_version+1,session_version=session_version+1 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, account.userID); err != nil {
		t.Fatal(err)
	}
	if status, body = call("POST", path, accepted, key); status != 403 {
		t.Fatalf("revoked replay = %d %s", status, body)
	}

	// Disabled account: no further mutation is authorized.
	disabled := f.newAccount(t, `"apps"`)
	disabledCall := f.incidentMutationClient(t, disabled)
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.accounts SET status='disabled',authorization_version=authorization_version+1,session_version=session_version+1 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, disabled.userID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.role_grants SET authorization_version=authorization_version+1 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, disabled.userID); err != nil {
		t.Fatal(err)
	}
	status, body = disabledCall("POST", path, map[string]any{"expectedRevision": f.incidentRevision(t, seed.IncidentID), "state": "suppressed", "reason": "disabled must be denied", "suppressedUntil": time.Now().UTC().Add(time.Hour)}, uuid.NewString())
	if status != 403 {
		t.Fatalf("disabled transition = %d %s", status, body)
	}
}

// The native business transaction holds the account/grant rows until it commits,
// so a concurrent administrative revocation waits instead of slipping between the
// check and the business write.
func TestNativeIncidentMutationLocksSerializeRevocation(t *testing.T) {
	f := newNativeJobFixture(t)
	seed := f.newIncident(t)
	account := f.newAccount(t, `"apps"`)
	f.clearLegacyBindings(t)

	scope, err := (graph.Authorization{Pool: f.apiPool}).Effective(account.ctx, f.tenantID.String(), account.subject, f.clusterUID)
	if err != nil {
		t.Fatalf("native scope: %v", err)
	}
	tx, err := f.apiPool.Begin(account.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(f.ctx, `SELECT set_config('app.tenant_id',$1,true)`, f.tenantID.String()); err != nil {
		t.Fatal(err)
	}
	item, err := incident.Load(f.ctx, tx, f.tenantID, seed.IncidentID.String(), false)
	if err != nil {
		t.Fatal(err)
	}
	// The business authorization must be bound to the real native context,
	// otherwise the shared boundary takes the legacy branch and the lock under
	// test is never taken.
	if err := incident.Authorize(account.ctx, tx, scope, account.subject, item); err != nil {
		t.Fatalf("native in-transaction authorization: %v", err)
	}
	adminConn, err := f.db.Conn(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer adminConn.Close()
	if _, err := adminConn.ExecContext(f.ctx, `SET lock_timeout='750ms'`); err != nil {
		t.Fatal(err)
	}
	if _, err := adminConn.ExecContext(f.ctx, `UPDATE identity.role_grants SET status='revoked',revoked_at=clock_timestamp(),revoked_reason='concurrent',authorization_version=authorization_version+1 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, account.userID); !isLockUnavailable(err) {
		t.Fatalf("revocation did not wait for the incident decision lock: %v", err)
	}
	if err := tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := adminConn.ExecContext(f.ctx, `UPDATE identity.role_grants SET status='revoked',revoked_at=clock_timestamp(),revoked_reason='after commit',authorization_version=authorization_version+1 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, account.userID); err != nil {
		t.Fatalf("revocation after commit: %v", err)
	}
	call := f.incidentMutationClient(t, account)
	status, body := call("POST", "/api/v1/incidents/"+seed.IncidentID.String()+":transition", map[string]any{"expectedRevision": seed.Revision, "state": "acknowledged", "reason": "after revocation"}, uuid.NewString())
	if status != 403 {
		t.Fatalf("transition after committed revocation = %d %s", status, body)
	}
}

// Resolving an Incident requires archived, time-reliable Evidence for the same
// resource. The Evidence row is authorized by the same native boundary, and the
// resolved Incident may then be closed.
func TestNativeIncidentResolveWithArchivedEvidence(t *testing.T) {
	f := newNativeJobFixture(t)
	seed := f.newIncidentFamily(t, "job", "BackOff", "job-1")
	account := f.newAccount(t, `"apps"`)
	f.clearLegacyBindings(t)
	call := f.incidentMutationClient(t, account)
	path := "/api/v1/incidents/" + seed.IncidentID.String() + ":transition"
	evidenceID := f.newEvidence(t, seed, true, "change")
	resolve := map[string]any{"expectedRevision": seed.Revision, "state": "resolved", "reason": "native resolve with archived evidence", "evidenceRefs": []string{evidenceID.String()}}
	status, body := call("POST", path, resolve, uuid.NewString())
	if status != 200 {
		t.Fatalf("native resolve = %d %s", status, body)
	}
	closed := map[string]any{"expectedRevision": seed.Revision + 1, "state": "closed", "reason": "native close resolved"}
	status, body = call("POST", path, closed, uuid.NewString())
	if status != 200 {
		t.Fatalf("native close of resolved = %d %s", status, body)
	}
	if got := f.incidentRevision(t, seed.IncidentID); got != seed.Revision+2 {
		t.Fatalf("resolved close revision = %d", got)
	}

	// Resolving with Evidence of another resource is refused before the write.
	other := f.newIncidentFamily(t, "cronjob", "Missed", "cron-1")
	foreign := f.newEvidence(t, seed, true, "change")
	badPath := "/api/v1/incidents/" + other.IncidentID.String() + ":transition"
	bad := map[string]any{"expectedRevision": other.Revision, "state": "resolved", "reason": "foreign evidence", "evidenceRefs": []string{foreign.String()}}
	status, body = call("POST", badPath, bad, uuid.NewString())
	if status != 400 && status != 403 {
		t.Fatalf("foreign evidence resolve = %d %s", status, body)
	}
	if got := f.incidentRevision(t, other.IncidentID); got != other.Revision {
		t.Fatalf("foreign evidence wrote revision %d", got)
	}
}
