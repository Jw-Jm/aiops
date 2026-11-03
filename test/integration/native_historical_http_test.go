package integration

import (
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/auth"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/identity"
)

// Real PostgreSQL and production cookie middleware/handlers; the legacy rows
// are controlled migration fixtures, not migrated production business history.
func TestNativeMappedHistoricalHTTPRead(t *testing.T) {
	f := newMappedHistoryFixture(t)
	f.submitTurn(t)
	frozen := f.frozenJob(t)
	sessions, err := identity.NewSessionRepository(f.apiPool)
	if err != nil {
		t.Fatal(err)
	}
	login, err := httpapi.NewNativeAuthHandlers(sessions, f.tenantID)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := auth.NewNativeAuthenticator(sessions, f.tenantID, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	history := &httpapi.AssistantHandlers{Repository: f.repo, CursorKey: key}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/auth/login", login.Login)
	mux.HandleFunc("/api/v1/auth/session", login.Session)
	mux.Handle("/api/v1/assistant/", history)
	client := mfaClient{handler: authenticator.Middleware(mux), sessions: sessions}
	var loginName string
	if err := f.db.QueryRowContext(f.ctx, `SELECT login_name FROM identity.accounts WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, f.userID).Scan(&loginName); err != nil {
		t.Fatal(err)
	}
	mapped, _ := mfaLoginWithCookie(t, client, loginName, "fixture-historical-password")
	path := "/api/v1/assistant/conversations/" + f.convID.String()
	for _, endpoint := range []string{path, path + "/messages", "/api/v1/assistant/turns/" + f.turnID.String()} {
		if status, _, _ := client.do(t, http.MethodGet, endpoint, nil, &mapped); status != 200 {
			t.Fatalf("mapped HTTP read %s = %d, want 200", endpoint, status)
		}
	}
	if status, _, _ := client.do(t, http.MethodPost, path+"/messages", map[string]any{"message": "must not acquire a new Worker grant", "expectedConversationRevision": 1}, &mapped); status != 403 && status != 404 {
		t.Fatalf("mapping conferred submit authority: HTTP %d", status)
	}
	// A matching login name and a current operator grant never imply ownership.
	other := f.createNativeAccount(t)
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.accounts SET login_name=$3 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, other, f.legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(f.ctx, `INSERT INTO identity.role_grants(tenant_id,user_id,binding_id,role,scope,authorization_version,status) VALUES($1,$2,$3,'operator',$4,1,'active')`, f.tenantID, other, uuid.New(), `{"clusterUids":["`+f.clusterUID+`"],"namespaces":["apps"]}`); err != nil {
		t.Fatal(err)
	}
	sameName, _ := mfaLoginWithCookie(t, client, f.legacy, "fixture-historical-password")
	for _, endpoint := range []string{path, path + "/messages", "/api/v1/assistant/turns/" + f.turnID.String()} {
		if status, _, _ := client.do(t, http.MethodGet, endpoint, nil, &sameName); status != 403 && status != 404 {
			t.Fatalf("same-name unmapped account read %s: %d", endpoint, status)
		}
	}
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.role_grants SET status='revoked',revoked_at=clock_timestamp(),revoked_reason='HTTP fixture revocation' WHERE tenant_id=$1 AND user_id=$2 AND role='operator'`, f.tenantID, f.userID); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{path, path + "/messages", "/api/v1/assistant/turns/" + f.turnID.String()} {
		if status, _, _ := client.do(t, http.MethodGet, endpoint, nil, &mapped); status != 401 && status != 403 && status != 404 {
			t.Fatalf("revoked account still read %s: %d", endpoint, status)
		}
	}
	if !reflect.DeepEqual(frozen, f.frozenJob(t)) {
		t.Fatal("HTTP reads changed frozen historical authority or input")
	}
	var jobs int
	if err := f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM investigation.jobs WHERE tenant_id=$1`, f.tenantID).Scan(&jobs); err != nil || jobs != 1 {
		t.Fatalf("historical reads/submission changed job count: %d err=%v", jobs, err)
	}
}
