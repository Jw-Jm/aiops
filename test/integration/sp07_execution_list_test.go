package integration

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"ops-platform/internal/action"
	"ops-platform/internal/auth"
	"ops-platform/internal/finding"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/incident"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestSP07ExecutionListFiltersByAuthorization proves the global execution
// listing is keyset-paginated, state-filtered, and hides rows outside the
// caller's effective scope instead of leaking them.
func TestSP07ExecutionListFiltersByAuthorization(t *testing.T) {
	ctx, db, worker, b := sp05Database(t)
	apiConfig := runtimePoolConfig(t, ctx, db, worker.Config().ConnString(), "api_runtime_role")
	pool, err := pgxpool.NewWithConfig(ctx, apiConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	fs := finding.Service{Pool: worker}
	f, _, err := fs.Ingest(ctx, b, sp05Envelope(b, "sp07-list-event", "sp07-list-occurrence"))
	if err != nil {
		t.Fatal(err)
	}
	if err = fs.RelayPass(ctx, b.TenantID, incident.Consume, 50); err != nil {
		t.Fatal(err)
	}
	var incidentID uuid.UUID
	if err = db.QueryRowContext(ctx, `SELECT incident_id FROM incident.finding_links WHERE finding_id=$1`, f.FindingID).Scan(&incidentID); err != nil {
		t.Fatal(err)
	}
	actor := auth.RequestContext{TenantID: b.TenantID, Subject: "sp07-list-operator", Roles: []auth.Role{auth.Operator}, ClusterScopes: []uuid.UUID{b.ClusterID}, TokenExpiresAt: time.Now().Add(time.Hour)}
	if _, err = db.ExecContext(ctx, `INSERT INTO platform.role_bindings(tenant_id,binding_id,subject,role_name,cluster_scopes,namespace_scopes) VALUES($1,$2,$3,'operator',$4,$5)`, b.TenantID, uuid.New(), actor.Subject, `["`+b.ClusterID.String()+`"]`, `[{"clusterId":"`+b.ClusterID.String()+`","namespace":"test-ns"}]`); err != nil {
		t.Fatal(err)
	}
	policyVersion := sp06Policy(t, ctx, db, worker, b.TenantID)
	target := sp05Envelope(b, "unused-list", "unused-list").ResourceCanonicalID
	if _, err = db.ExecContext(ctx, `INSERT INTO platform.resource_entities(tenant_id,canonical_id,cluster_id,kind,namespace,name,metadata,observed_at) VALUES($1,$2,$3,'Node','test-ns','node-list','{"sourceValues":{"uid":"node-list"}}',clock_timestamp())`, b.TenantID, target, b.ClusterID); err != nil {
		t.Fatal(err)
	}
	profile := action.Profile{SchemaVersion: "execution-profile/v2", ID: uuid.New(), Version: 1, Name: "list-profile", Type: "k8s_namespace", Namespace: "test-ns", AllowedTargets: []string{target}, ClusterUID: b.ClusterUID, Tools: []string{"bash"}, NetworkPolicyRef: "test-network", CredentialRef: "openbao://test", TimeoutSeconds: 900, MaxOutputBytes: 10 << 20}
	profile.ToolImageDigest = "localhost/tool@" + action.Digest([]byte("tool"))
	if profile.Validate() != nil {
		t.Fatal("invalid fixture profile")
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO platform.execution_profile_versions(tenant_id,profile_version_id,logical_name,version_number,content,digest,signature,signer_key_id,status,published_by) VALUES($1,$2,'list-profile',1,$3,$4,'x','test','published','test')`, b.TenantID, uuid.New(), action.Canonical(profile), action.Digest(action.Canonical(profile))); err != nil {
		t.Fatal(err)
	}
	service := action.Service{Pool: pool, Policy: sp07Policy{policyVersion}, Protector: &sp07Protector{plain: map[uuid.UUID][]byte{}}}
	req := action.CommandRequest{IncidentID: incidentID, ActualCommand: "printf 'list page'", Target: target, Shell: "bash", ProfileID: profile.ID, ProfileVersion: 1, Options: action.Options{TimeoutSeconds: 900, MaxOutputBytes: 10 << 20, Environment: map[string]string{}}}
	first, err := service.AssessRisk(ctx, actor, req, "list-risk-1")
	if err != nil {
		t.Fatal(err)
	}
	ackOne, err := service.AcknowledgeRisk(ctx, actor, first.ID, first.RequestDigest, "list-ack-1")
	if err != nil {
		t.Fatal(err)
	}
	older, err := service.PrepareCommandExecution(ctx, actor, req, ackOne.ID, "list-execute-1")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.AssessRisk(ctx, actor, req, "list-risk-2")
	if err != nil {
		t.Fatal(err)
	}
	ackTwo, err := service.AcknowledgeRisk(ctx, actor, second.ID, second.RequestDigest, "list-ack-2")
	if err != nil {
		t.Fatal(err)
	}
	newer, err := service.PrepareCommandExecution(ctx, actor, req, ackTwo.ID, "list-execute-2")
	if err != nil {
		t.Fatal(err)
	}

	page, next, err := service.ListExecutions(ctx, actor, nil, "", 1)
	if err != nil {
		t.Fatalf("list: %T %[1]v", err)
	}
	if len(page) != 1 || page[0].ID != newer.ID || next == nil {
		t.Fatalf("first page %v next %v", page, next)
	}
	rest, next, err := service.ListExecutions(ctx, actor, next, "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 1 || rest[0].ID != older.ID || next != nil {
		t.Fatalf("second page %v next %v", rest, next)
	}
	prepared, _, err := service.ListExecutions(ctx, actor, nil, "prepared", 10)
	if err != nil || len(prepared) != 2 {
		t.Fatalf("state filter %v %v", prepared, err)
	}
	succeeded, _, err := service.ListExecutions(ctx, actor, nil, "succeeded", 10)
	if err != nil || len(succeeded) != 0 {
		t.Fatalf("terminal filter %v %v", succeeded, err)
	}
	stranger := auth.RequestContext{TenantID: b.TenantID, Subject: "sp07-list-stranger", Roles: []auth.Role{auth.Operator}, TokenExpiresAt: time.Now().Add(time.Hour)}
	hidden, next, err := service.ListExecutions(ctx, stranger, nil, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hidden) != 0 || next != nil {
		t.Fatalf("unscoped subject must not see executions: %v %v", hidden, next)
	}

	handler := &httpapi.ActionHandlers{Service: service, CursorKey: mustListKey(t)}
	respond := func(path string, subject auth.RequestContext) (int, map[string]any) {
		t.Helper()
		recorder := httptest.NewRecorder()
		reqHTTP := httptest.NewRequest(http.MethodGet, path, nil).WithContext(auth.WithRequestContext(context.Background(), subject))
		handler.ServeHTTP(recorder, reqHTTP)
		var body map[string]any
		_ = json.Unmarshal(recorder.Body.Bytes(), &body)
		return recorder.Code, body
	}
	status, body := respond("/api/v1/command-executions?limit=1", actor)
	if status != 200 {
		t.Fatalf("list status %d body %v", status, body)
	}
	data, _ := body["data"].([]any)
	meta, _ := body["meta"].(map[string]any)
	cursor, _ := meta["nextCursor"].(string)
	if len(data) != 1 || cursor == "" {
		t.Fatalf("http first page %v meta %v", data, meta)
	}
	status, body = respond("/api/v1/command-executions?limit=1&cursor="+cursor, actor)
	if status != 200 {
		t.Fatalf("cursor page status %d body %v", status, body)
	}
	data, _ = body["data"].([]any)
	meta, _ = body["meta"].(map[string]any)
	if len(data) != 1 || meta["nextCursor"] != nil {
		t.Fatalf("http second page %v meta %v", data, meta)
	}
	if status, _ = respond("/api/v1/command-executions?state=bogus", actor); status != 400 {
		t.Fatalf("bogus state %d", status)
	}
	if status, _ = respond("/api/v1/command-executions?cursor=tampered.signature", actor); status != 403 {
		t.Fatalf("tampered cursor %d", status)
	}
	if status, _ = respond("/api/v1/command-executions?limit=1&extra=1", actor); status != 400 {
		t.Fatalf("unknown parameter %d", status)
	}
}

func mustListKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}
