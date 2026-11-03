package integration

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"ops-platform/internal/analytics"
	"ops-platform/internal/datascope"
	"ops-platform/internal/graph"
	"ops-platform/internal/observationquery"
	"ops-platform/internal/resource"
	"testing"
	"time"
)

// Lifecycle/authority fixture on real PostgreSQL. No synthetic rows are claimed
// to be qualified live logging-source or browser acceptance evidence.
func TestNativeObservationQueryLifecycle(t *testing.T) {
	f := newNativeJobFixture(t)
	account := f.newAccount(t, `"apps"`)
	scope, err := (graph.Authorization{Pool: f.apiPool}).Effective(account.ctx, f.tenantID.String(), account.subject, f.clusterUID)
	if err != nil {
		t.Fatal(err)
	}
	claims := graph.Claims{Source: graph.SourceNativeInteractive, Native: &graph.NativeProof{SessionID: account.sessionID.String()}, Subject: account.subject, Scope: scope}
	sid := uuid.New()
	mapping, _ := json.Marshal(datascope.Mapping{Scopes: map[string][]string{"cluster": {f.clusterUID}, "namespace": {"apps"}}})
	if _, err = f.db.ExecContext(f.ctx, `INSERT INTO platform.source_registrations(tenant_id,source_id,cluster_id,source_type,instance_key,auth_ref,allowed_schemas,backend_logical_id,data_scope_mapping) VALUES($1,$2,$3,'victorialogs',$4,'openbao://controlled/observation',ARRAY['finding-envelope/v2'],$4,$5)`, f.tenantID, sid, f.clusterID, sid.String(), mapping); err != nil {
		t.Fatal(err)
	}
	ns, nsuid := "apps", "namespace-fixture"
	now := time.Now().UTC().Truncate(time.Second)
	request := observationquery.Request{SchemaVersion: observationquery.Schema, QueryKind: "logs", TemplateID: "logs.records", TemplateVersion: "logs.records/v1", Resource: observationquery.Resource{CanonicalID: resource.CanonicalID{Domain: "k8s", Tenant: f.tenantID.String(), Scope: f.clusterUID, APIGroup: "core", Kind: "Pod", StableID: "pod-fixture"}.String(), UID: "pod-fixture", ClusterUID: f.clusterUID, NamespaceScope: observationquery.NamespaceScope{Kind: "namespaced", Namespace: &ns, NamespaceUID: &nsuid}}, Source: observationquery.Source{RegistrationID: sid, RegistrationRevision: 1, CredentialRevision: 1, QualificationDigest: graph.ScopeDigest("controlled qualification"), CollectorVersion: "controlled/v1"}, Window: observationquery.Window{From: now.Add(-time.Minute), To: now, Timezone: "UTC", Bounds: "[from,to)"}, Budget: observationquery.Budget{MaxRows: 20, MaxBytes: 131072, MaxDurationSeconds: 8, MaxSourceCalls: 2}}
	raw, _ := json.Marshal(request)
	if _, err := observationquery.Decode(raw, f.tenantID.String()); err != nil {
		t.Fatal(err)
	}
	repo := observationquery.Repository{Pool: f.workerPool}
	key := uuid.New()
	accepted, err := repo.Create(f.ctx, claims, request, key)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := repo.Create(f.ctx, claims, request, key)
	if err != nil || replay.QueryID != accepted.QueryID {
		t.Fatalf("idempotent replay: %+v %v", replay, err)
	}
	if _, err = repo.Create(f.ctx, claims, request, uuid.New()); !errors.Is(err, observationquery.ErrConflict) {
		t.Fatalf("duplicate active query: %v", err)
	}
	running, err := repo.Claim(f.ctx, f.tenantID, f.clusterUID)
	if err != nil || running.State != "running" {
		t.Fatalf("claim: %+v %v", running, err)
	}
	if _, err = repo.Cancel(f.ctx, claims, accepted.QueryID, accepted.Revision); !errors.Is(err, observationquery.ErrConflict) {
		t.Fatalf("stale cancel accepted: %v", err)
	}
	cancel, err := repo.Cancel(f.ctx, claims, accepted.QueryID, running.Revision)
	if err != nil || cancel.State != "cancel_requested" || cancel.Termination != nil {
		t.Fatalf("cancel pretended to stop: %+v %v", cancel, err)
	}
	if err = repo.Finish(f.ctx, running, claims, json.RawMessage(`{"resultId":"`+uuid.NewString()+`","resultDigest":"`+graph.ScopeDigest("late")+`"}`), nil); err != nil {
		t.Fatal(err)
	}
	stopped, err := repo.Claim(f.ctx, f.tenantID, f.clusterUID)
	if err != nil || stopped.State != "terminated" || stopped.Termination.SourceStopped || stopped.ResultRef != nil {
		t.Fatalf("late publication/cancellation receipt: %+v %v", stopped, err)
	}
	next, err := repo.Create(f.ctx, claims, request, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	running, err = repo.Claim(f.ctx, f.tenantID, f.clusterUID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := observationquery.BuildResult(running, analytics.QueryResult{Exact: true, Status: "complete", Rows: []analytics.Row{{Value: 2}}}, analytics.QueryResult{Samples: []analytics.LogSample{{Timestamp: now, Message: "controlled sample", Level: "info"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = repo.Finish(f.ctx, running, claims, result, nil); err != nil {
		t.Fatal(err)
	}
	read, err := repo.Read(f.ctx, claims, next.QueryID)
	if err != nil || read.State != "completed" || read.ResultRef == nil {
		t.Fatalf("persistent result: %+v %v", read, err)
	}
	if _, err = f.db.ExecContext(f.ctx, `UPDATE investigation.observation_queries SET result='{}' WHERE tenant_id=$1 AND query_id=$2`, f.tenantID, next.QueryID); err == nil {
		t.Fatal("published result mutated")
	}
	if _, err = f.db.ExecContext(f.ctx, `UPDATE platform.source_registrations SET status='disabled' WHERE tenant_id=$1 AND source_id=$2`, f.tenantID, sid); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Read(f.ctx, claims, next.QueryID); !errors.Is(err, observationquery.ErrUnavailable) {
		t.Fatalf("disabled source disclosed result: %v", err)
	}
	if _, err = f.db.ExecContext(f.ctx, `UPDATE platform.source_registrations SET status='active' WHERE tenant_id=$1 AND source_id=$2`, f.tenantID, sid); err != nil {
		t.Fatal(err)
	}
	recovery, err := repo.Create(f.ctx, claims, request, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Claim(f.ctx, f.tenantID, f.clusterUID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.ExecContext(f.ctx, `UPDATE investigation.observation_queries SET lease_until=clock_timestamp()-interval '1 second' WHERE tenant_id=$1 AND query_id=$2`, f.tenantID, recovery.QueryID); err != nil {
		t.Fatal(err)
	}
	failed, err := repo.Claim(f.ctx, f.tenantID, f.clusterUID)
	if err != nil || failed.State != "failed" || failed.Failure.Code != "WORKER_INTERRUPTED" {
		t.Fatalf("restart recovery: %+v %v", failed, err)
	}

	// A historical row lacks the new view.request projection; the immutable
	// request column remains authoritative for budget and template readback.
	expired := read.View
	expired.QueryID = uuid.New()
	expired.CreatedAt = now.Add(-31 * 24 * time.Hour)
	expired.UpdatedAt = expired.CreatedAt
	expired.ExpiresAt = now.Add(-time.Minute)
	expiredView, _ := json.Marshal(expired)
	var legacy map[string]any
	_ = json.Unmarshal(expiredView, &legacy)
	delete(legacy, "request")
	expiredView, _ = json.Marshal(legacy)
	scopeJSON, _ := json.Marshal(scope)
	if _, err = f.db.ExecContext(f.ctx, `INSERT INTO investigation.observation_queries(tenant_id,query_id,owner_subject,request_key,cluster_uid,session_id,request,scope,view,result,state,revision,created_at,updated_at,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'completed',$11,$12,$12,$13)`, f.tenantID, expired.QueryID, account.subject, uuid.New(), f.clusterUID, account.sessionID, raw, scopeJSON, expiredView, result, expired.Revision, expired.CreatedAt, expired.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	projected, err := repo.Read(f.ctx, claims, expired.QueryID)
	if err != nil || projected.State != "expired" || projected.Result != nil || projected.ResultRef != nil || projected.Failure.Code != "RESULT_EXPIRED" || projected.QueryRequest == nil || projected.QueryRequest.Budget != request.Budget || projected.QueryRequest.TemplateVersion != request.TemplateVersion {
		t.Fatalf("expired/readback projection: %+v %v", projected.View, err)
	}
	sourceDispatched := false
	expiryService := observationquery.Service{Repository: repo, Resources: func(context.Context, graph.Scope) ([]observationquery.Resource, error) {
		sourceDispatched = true
		return nil, observationquery.ErrUnavailable
	}}
	if _, err := expiryService.Read(f.ctx, claims, expired.QueryID); err != nil || sourceDispatched {
		t.Fatalf("expired read dispatched source IO: %v %v", sourceDispatched, err)
	}
	first, err := repo.IDs(f.ctx, claims, uuid.Nil, 1)
	if err != nil || len(first) != 2 {
		t.Fatalf("first history page plus lookahead: %v %v", first, err)
	}
	second, err := repo.IDs(f.ctx, claims, first[0], 1)
	if err != nil || len(second) != 2 || second[0] != first[1] {
		t.Fatalf("history cursor skipped/duplicated: %v %v", second, err)
	}
	for _, id := range append(first, second...) {
		if id == expired.QueryID {
			t.Fatal("expired result entered history")
		}
	}
	if _, err := repo.IDs(f.ctx, claims, uuid.New(), 1); !errors.Is(err, observationquery.ErrInvalid) {
		t.Fatalf("unowned cursor accepted: %v", err)
	}
	narrowed := claims
	narrowed.Scope.Cluster = "another-cluster"
	if _, err := repo.IDs(f.ctx, narrowed, first[0], 1); !errors.Is(err, observationquery.ErrInvalid) {
		t.Fatalf("cross-cluster cursor accepted: %v", err)
	}

	for _, state := range []string{"accepted", "running"} {
		id, token := uuid.New(), uuid.New()
		_, err = f.db.ExecContext(f.ctx, `INSERT INTO investigation.observation_queries(tenant_id,query_id,owner_subject,request_key,cluster_uid,session_id,request,scope,view,result,state,revision,created_at,updated_at,expires_at,lease_token,lease_until)
        SELECT tenant_id,$3::uuid,owner_subject,$4::uuid,cluster_uid,session_id,request,scope,view||jsonb_build_object('queryId',$3::uuid::text,'state',$5::text,'resultRef',NULL,'failure',NULL),NULL,$5,revision,created_at,updated_at,expires_at,$6,clock_timestamp()+interval '15 seconds' FROM investigation.observation_queries WHERE tenant_id=$1 AND query_id=$2`, f.tenantID, expired.QueryID, id, uuid.New(), state, token)
		if err != nil {
			t.Fatal(err)
		}
		if state == "accepted" {
			if err = expiryService.RunOne(f.ctx, f.tenantID, f.clusterUID); err != nil || sourceDispatched {
				t.Fatalf("expired queue dispatched: %v %v", sourceDispatched, err)
			}
		} else {
			lease := observationquery.Record{View: observationquery.View{TenantID: f.tenantID, QueryID: id}, LeaseToken: token}
			stopped, e := repo.Cancelled(f.ctx, lease)
			if e != nil || !stopped {
				t.Fatalf("expired execution not stopped: %v %v", stopped, e)
			}
			if err = repo.Finish(f.ctx, lease, claims, result, nil); err != nil {
				t.Fatal(err)
			}
		}
		var final string
		var hidden bool
		if err = f.db.QueryRowContext(f.ctx, `SELECT state,result IS NULL FROM investigation.observation_queries WHERE tenant_id=$1 AND query_id=$2`, f.tenantID, id).Scan(&final, &hidden); err != nil || final != "expired" || !hidden {
			t.Fatalf("expired %s published a result: %s %v %v", state, final, hidden, err)
		}
	}
	pending, err := repo.Create(f.ctx, claims, request, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.ExecContext(f.ctx, `UPDATE identity.role_grants SET status='revoked',revoked_at=clock_timestamp(),revoked_reason='observation fixture' WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, account.userID); err != nil {
		t.Fatal(err)
	}
	dispatched := false
	service := observationquery.Service{Repository: repo, Resources: func(context.Context, graph.Scope) ([]observationquery.Resource, error) {
		dispatched = true
		return nil, observationquery.ErrUnavailable
	}}
	if err = service.RunOne(f.ctx, f.tenantID, f.clusterUID); err != nil {
		t.Fatal(err)
	}
	var state string
	if err = f.db.QueryRowContext(f.ctx, `SELECT state FROM investigation.observation_queries WHERE tenant_id=$1 AND query_id=$2`, f.tenantID, pending.QueryID).Scan(&state); err != nil || state != "denied" || dispatched {
		t.Fatalf("revoked query dispatched source IO: state=%s dispatched=%v err=%v", state, dispatched, err)
	}
	if _, err = repo.Read(f.ctx, claims, next.QueryID); !errors.Is(err, observationquery.ErrDenied) {
		t.Fatalf("revoked session disclosed result: %v", err)
	}
	if _, err := repo.Read(f.ctx, claims, expired.QueryID); !errors.Is(err, observationquery.ErrDenied) {
		t.Fatalf("expired state bypassed revoked authorization: %v", err)
	}
	var jobs int
	if err = f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM investigation.jobs WHERE tenant_id=$1`, f.tenantID).Scan(&jobs); err != nil || jobs != 0 {
		t.Fatalf("manual query fabricated AI Job: %d %v", jobs, err)
	}
}

func TestNativeObservationSourceAuthorization(t *testing.T) {
	f := newNativeJobFixture(t)
	account := f.newAccount(t, `"apps"`)
	scope, err := (graph.Authorization{Pool: f.apiPool}).Effective(account.ctx, f.tenantID.String(), account.subject, f.clusterUID)
	if err != nil {
		t.Fatal(err)
	}
	claims := graph.Claims{Source: graph.SourceNativeInteractive, Native: &graph.NativeProof{SessionID: account.sessionID.String()}, Subject: account.subject, Scope: scope}
	sid := uuid.New()
	mapping, _ := json.Marshal(datascope.Mapping{Scopes: map[string][]string{"cluster": {f.clusterUID}, "namespace": {"apps"}}})
	if _, err = f.db.ExecContext(f.ctx, `INSERT INTO platform.source_registrations(tenant_id,source_id,cluster_id,source_type,instance_key,auth_ref,allowed_schemas,backend_logical_id,data_scope_mapping) VALUES($1,$2,$3,'kubernetes',$4,'openbao://controlled/native-logs',ARRAY['finding-envelope/v2'],$4,$5)`, f.tenantID, sid, f.clusterID, sid.String(), mapping); err != nil {
		t.Fatal(err)
	}
	ns, nsuid := "apps", "namespace-fixture"
	now := time.Now().UTC().Truncate(time.Second)
	request := observationquery.Request{SchemaVersion: observationquery.Schema, QueryKind: "logs", TemplateID: "logs.records", TemplateVersion: "logs.records/v1", Resource: observationquery.Resource{CanonicalID: resource.CanonicalID{Domain: "k8s", Tenant: f.tenantID.String(), Scope: f.clusterUID, APIGroup: "core", Kind: "Pod", StableID: "pod-fixture"}.String(), UID: "pod-fixture", ClusterUID: f.clusterUID, NamespaceScope: observationquery.NamespaceScope{Kind: "namespaced", Namespace: &ns, NamespaceUID: &nsuid}}, Source: observationquery.Source{RegistrationID: sid, RegistrationRevision: 1, CredentialRevision: 1, QualificationDigest: graph.ScopeDigest("controlled native identity"), CollectorVersion: observationquery.NativeLogVersion}, Window: observationquery.Window{From: now.Add(-time.Minute), To: now, Timezone: "UTC", Bounds: "[from,to)"}, Budget: observationquery.Budget{MaxRows: 20, MaxBytes: 131072, MaxDurationSeconds: 8, MaxSourceCalls: 2}}
	repo := observationquery.Repository{Pool: f.workerPool}
	accepted, err := repo.Create(f.ctx, claims, request, uuid.New())
	if err != nil {
		t.Fatal("registered native source rejected", err)
	}
	if _, err = repo.Read(f.ctx, claims, accepted.QueryID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.ExecContext(f.ctx, `UPDATE platform.source_registrations SET data_scope_mapping=jsonb_set(data_scope_mapping,'{nativeTenant}',to_jsonb($3::text)) WHERE tenant_id=$1 AND source_id=$2`, f.tenantID, sid, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Read(f.ctx, claims, accepted.QueryID); !errors.Is(err, observationquery.ErrUnavailable) {
		t.Fatal("foreign native tenant accepted", err)
	}
	if _, err = f.db.ExecContext(f.ctx, `UPDATE platform.source_registrations SET data_scope_mapping=$3,credential_revision=2 WHERE tenant_id=$1 AND source_id=$2`, f.tenantID, sid, mapping); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Read(f.ctx, claims, accepted.QueryID); !errors.Is(err, observationquery.ErrUnavailable) {
		t.Fatal("old credential binding accepted", err)
	}
}
