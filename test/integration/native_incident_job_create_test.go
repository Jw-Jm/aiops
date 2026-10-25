package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"ops-platform/internal/auth"
	"ops-platform/internal/graph"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/investigation"
	"ops-platform/internal/investigation/tools"
)

func (f *nativeJobFixture) createRequest(seed nativeIncidentSeed, subject, digest string) investigation.CreateInvestigationRequest {
	return investigation.CreateInvestigationRequest{
		IdempotencyKey: "native-create-" + digest[:8], RequestDigest: digest,
		TenantID: f.tenantID, Subject: subject, IncidentID: seed.IncidentID,
		TriggerRevision: seed.Revision, TriggerKind: "manual", PolicyVersion: f.policy,
		ToolCatalogDigest: "sha256:" + digest, Budget: investigation.DefaultBudget(),
	}
}

func (f *nativeJobFixture) countJobs(t *testing.T, subject string) (jobs, queued, audited int) {
	t.Helper()
	if err := f.db.QueryRowContext(f.ctx, `SELECT (SELECT count(*) FROM investigation.jobs WHERE tenant_id=$1 AND requesting_subject=$2), (SELECT count(*) FROM investigation.worker_queue q JOIN investigation.jobs j USING(tenant_id,job_id) WHERE j.tenant_id=$1 AND j.requesting_subject=$2), (SELECT count(*) FROM audit.records WHERE tenant_id=$1 AND event_type LIKE 'investigation.%' AND subject=$2)`, f.tenantID, subject).Scan(&jobs, &queued, &audited); err != nil {
		t.Fatal(err)
	}
	return jobs, queued, audited
}

// The HTTP request path proves the browser session twice: once before the
// handler runs and again inside the transaction that writes the Job. A session
// revoked, a password reset or a credential/session version rotation between the
// two must deny admission and leave no Job, queue row or audit record behind.
func TestNativeIncidentJobCreateReprovesSessionInTransaction(t *testing.T) {
	f := newNativeJobFixture(t)
	seed := f.newIncident(t)
	f.clearLegacyBindings(t)
	defs, err := tools.CatalogFor("investigation-job/v2", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	digest := tools.Digest(defs)

	cases := []struct {
		name   string
		change string
	}{
		{name: "session revoked", change: `UPDATE identity.sessions SET revoked_at=clock_timestamp(),revoked_reason='integration' WHERE tenant_id=$1 AND user_id=$2`},
		{name: "credential rotated", change: `UPDATE identity.accounts SET credential_version=credential_version+1 WHERE tenant_id=$1 AND user_id=$2`},
		{name: "session version rotated", change: `UPDATE identity.accounts SET session_version=session_version+1 WHERE tenant_id=$1 AND user_id=$2`},
		{name: "account disabled", change: `UPDATE identity.accounts SET status='disabled',authorization_version=authorization_version+1 WHERE tenant_id=$1 AND user_id=$2`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			account := f.newAccount(t, `"apps"`)
			// The interactive pre-check passes: this is the scope the HTTP handler
			// would freeze into the request.
			scope, err := (graph.Authorization{Pool: f.apiPool}).Effective(account.ctx, f.tenantID.String(), account.subject, f.clusterUID)
			if err != nil {
				t.Fatalf("pre-check scope: %v", err)
			}
			if _, err := f.db.ExecContext(f.ctx, tc.change, f.tenantID, account.userID); err != nil {
				t.Fatal(err)
			}
			// The mutation is only admitted if the write transaction re-proves the
			// session itself, not just the live account/grant versions.
			request := f.createRequest(seed, account.subject, digest)
			request.Scope = scope
			if _, err = (investigation.Repository{Pool: f.apiPool}).CreateJob(account.ctx, request); !errors.Is(err, investigation.ErrDenied) {
				t.Fatalf("%s create = %v", tc.name, err)
			}
			if jobs, queued, audited := f.countJobs(t, account.subject); jobs != 0 || queued != 0 || audited != 0 {
				t.Fatalf("%s left jobs=%d queue=%d audit=%d", tc.name, jobs, queued, audited)
			}
		})
	}

	// Control: a live session still creates a native Job with persisted provenance.
	live := f.newAccount(t, `"apps"`)
	scope, err := (graph.Authorization{Pool: f.apiPool}).Effective(live.ctx, f.tenantID.String(), live.subject, f.clusterUID)
	if err != nil {
		t.Fatal(err)
	}
	request := f.createRequest(seed, live.subject, digest)
	request.Scope = scope
	job, err := (investigation.Repository{Pool: f.apiPool}).CreateJob(live.ctx, request)
	if err != nil {
		t.Fatalf("live create: %v", err)
	}
	if source, userID, version := f.jobAuthority(t, job.JobID); source != "native" || userID != live.userID || version != 1 {
		t.Fatalf("live provenance = %s/%s/%d", source, userID, version)
	}
}

// The real InvestigationHandlers POST is the only production create path. It must
// persist the native provenance, replay the identical request without a second
// Job/queue/audit row, and refuse a new request once the session is revoked.
func TestNativeIncidentJobHTTPCreatePersistsProvenance(t *testing.T) {
	f := newNativeJobFixture(t)
	seed := f.newIncident(t)
	account := f.newAccount(t, `"apps"`)
	f.clearLegacyBindings(t)
	handler := &httpapi.InvestigationHandlers{
		Repository: investigation.Repository{Pool: f.apiPool},
		PolicyVersion: func(ctx context.Context, actor auth.RequestContext, cluster string) (string, error) {
			return f.policy, nil
		},
		Budget: investigation.DefaultBudget(),
	}
	ctx := auth.WithRequestContext(account.ctx, auth.RequestContext{
		TenantID: f.tenantID, Subject: account.subject,
		Roles: []auth.Role{auth.Operator}, RequestID: "native-incident-create",
	})
	call := func(key string, body map[string]any) (int, []byte) {
		payload, _ := json.Marshal(body)
		request := httptest.NewRequest("POST", "/api/v2/incidents/"+seed.IncidentID.String()+"/investigations", bytes.NewReader(payload))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", key)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request.WithContext(ctx))
		return recorder.Code, recorder.Body.Bytes()
	}
	body := map[string]any{"triggerRevision": seed.Revision, "triggerKind": "manual"}
	key := uuid.NewString()
	status, payload := call(key, body)
	if status != 202 {
		t.Fatalf("http create = %d %s", status, payload)
	}
	var created struct {
		Data investigation.Job `json:"data"`
	}
	if json.Unmarshal(payload, &created) != nil || created.Data.JobID == uuid.Nil {
		t.Fatalf("http create body = %s", payload)
	}
	if source, userID, version := f.jobAuthority(t, created.Data.JobID); source != "native" || userID != account.userID || version != 1 {
		t.Fatalf("http provenance = %s/%s/%d", source, userID, version)
	}
	if jobs, queued, audited := f.countJobs(t, account.subject); jobs != 1 || queued != 1 || audited != 1 {
		t.Fatalf("http create rows jobs=%d queue=%d audit=%d", jobs, queued, audited)
	}

	// The identical request replays the same Job and writes nothing new.
	status, replayed := call(key, body)
	if status != 202 {
		t.Fatalf("http replay = %d %s", status, replayed)
	}
	var again struct {
		Data investigation.Job `json:"data"`
	}
	if json.Unmarshal(replayed, &again) != nil || again.Data.JobID != created.Data.JobID {
		t.Fatalf("http replay job = %s", replayed)
	}
	if jobs, queued, audited := f.countJobs(t, account.subject); jobs != 1 || queued != 1 || audited != 1 {
		t.Fatalf("http replay rows jobs=%d queue=%d audit=%d", jobs, queued, audited)
	}

	// After the session is revoked the same browser can neither create a new Job
	// nor replay the accepted request.
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.sessions SET revoked_at=clock_timestamp(),revoked_reason='integration' WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, account.userID); err != nil {
		t.Fatal(err)
	}
	if status, payload = call(uuid.NewString(), body); status != 403 {
		t.Fatalf("revoked create = %d %s", status, payload)
	}
	if status, payload = call(key, body); status != 403 {
		t.Fatalf("revoked replay = %d %s", status, payload)
	}
	if jobs, queued, audited := f.countJobs(t, account.subject); jobs != 1 || queued != 1 || audited != 1 {
		t.Fatalf("revoked rows jobs=%d queue=%d audit=%d", jobs, queued, audited)
	}
}

// A v2 Job written before the provenance columns existed reads back with an empty
// source. Replaying the same identity must return that Job instead of a bogus
// conflict, and the historical encoding must stay untouched.
func TestLegacyIncidentJobIdempotentReplayAcrossHistoricalEncoding(t *testing.T) {
	f := newNativeJobFixture(t)
	seed := f.newIncident(t)
	var clusterID uuid.UUID
	if err := f.db.QueryRowContext(f.ctx, `SELECT cluster_id FROM platform.cluster_registrations WHERE tenant_id=$1 AND cluster_uid=$2`, f.tenantID, f.clusterUID).Scan(&clusterID); err != nil {
		t.Fatal(err)
	}
	namespaces := `[{"clusterId":"` + clusterID.String() + `","namespace":"apps"}]`
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO platform.role_bindings(tenant_id,binding_id,subject,role_name,cluster_scopes,namespace_scopes) VALUES($1,$2,'legacy-incident-operator','operator','[]',$3)`, f.tenantID, uuid.New(), namespaces); err != nil {
		t.Fatal(err)
	}
	legacyCtx := auth.WithRequestContext(f.ctx, auth.RequestContext{TenantID: f.tenantID, Subject: "legacy-incident-operator", Roles: []auth.Role{auth.Operator}})
	scope, err := (graph.Authorization{Pool: f.apiPool}).Effective(legacyCtx, f.tenantID.String(), "legacy-incident-operator", f.clusterUID)
	if err != nil {
		t.Fatalf("legacy scope: %v", err)
	}
	defs, err := tools.CatalogFor("investigation-job/v2", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	digest := tools.Digest(defs)
	request := f.createRequest(seed, "legacy-incident-operator", digest)
	request.Scope = scope
	job, err := (investigation.Repository{Pool: f.apiPool}).CreateJob(legacyCtx, request)
	if err != nil {
		t.Fatalf("legacy create: %v", err)
	}
	if source, _, _ := f.jobAuthority(t, job.JobID); source != "legacy" {
		t.Fatalf("legacy provenance = %s", source)
	}
	// Simulate a row written before migration 68: no provenance at all.
	if _, err := f.db.ExecContext(f.ctx, `UPDATE investigation.jobs SET authorization_source=NULL,authorization_user_id=NULL,authorization_version=NULL WHERE tenant_id=$1 AND job_id=$2`, f.tenantID, job.JobID); err != nil {
		t.Fatal(err)
	}
	replayed, err := (investigation.Repository{Pool: f.apiPool}).CreateJob(legacyCtx, request)
	if err != nil {
		t.Fatalf("historical replay: %v", err)
	}
	if replayed.JobID != job.JobID {
		t.Fatalf("historical replay job = %s want %s", replayed.JobID, job.JobID)
	}
	if source, userID, version := f.jobAuthority(t, job.JobID); source != "" || userID != uuid.Nil || version != 0 {
		t.Fatalf("historical encoding was rewritten: %s/%s/%d", source, userID, version)
	}
	if jobs, _, _ := f.countJobs(t, "legacy-incident-operator"); jobs != 1 {
		t.Fatalf("historical replay created %d jobs", jobs)
	}
	// A native authority never borrows a historical legacy Job.
	nativeAccount := f.newAccount(t, `"apps"`)
	nativeScope, err := (graph.Authorization{Pool: f.apiPool}).Effective(nativeAccount.ctx, f.tenantID.String(), nativeAccount.subject, f.clusterUID)
	if err != nil {
		t.Fatal(err)
	}
	nativeRequest := f.createRequest(seed, nativeAccount.subject, digest)
	nativeRequest.Scope = nativeScope
	nativeJob, err := (investigation.Repository{Pool: f.apiPool}).CreateJob(nativeAccount.ctx, nativeRequest)
	if err != nil {
		t.Fatalf("native create after historical row: %v", err)
	}
	if source, userID, version := f.jobAuthority(t, nativeJob.JobID); source != "native" || userID != nativeAccount.userID || version != 1 {
		t.Fatalf("native provenance = %s/%s/%d", source, userID, version)
	}
	if nativeJob.JobID == job.JobID {
		t.Fatal("native create reused the historical legacy Job")
	}
}
