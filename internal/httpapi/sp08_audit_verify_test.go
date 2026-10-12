package httpapi

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"github.com/google/uuid"
	"io"
	"net/http"
	"net/http/httptest"
	"ops-platform/internal/auth"
	"ops-platform/internal/graph"
	"testing"
)

func TestAuditVerifierBindsSubjectTenantRangeAndSeparateDigestSemantics(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	tenant := uuid.New()
	rows := []map[string]any{{"auditSeq": "9007199254740995"}, {"auditSeq": "9007199254740993"}}
	a := auth.RequestContext{TenantID: tenant, Subject: "admin-subject"}
	reply := "verified"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		claims, err := graph.VerifyContext(pub, r.Header.Get("X-Graph-Context"), "platform-audit-verify", body)
		if err != nil || claims.Subject != a.Subject || claims.Scope.Tenant != tenant.String() {
			t.Error("trusted context did not bind administrator and tenant")
		}
		var data map[string]string
		if json.Unmarshal(body, &data) != nil || data["first"] != "9007199254740993" || data["last"] != "9007199254740995" {
			t.Error("audit sequence lost integer precision")
		}
		json.NewEncoder(w).Encode(map[string]string{"status": reply, "reason": "fixture verification"})
	}))
	defer server.Close()
	h := SP04Handlers{Client: server.Client(), SigningKey: key, AuditEndpoint: server.URL, EndpointAllowed: func(e string) bool { return e == server.URL }}
	status, _ := h.verifyAuditRange(context.Background(), a, rows)
	if status != "verified" {
		t.Fatalf("verification=%s", status)
	}
	reply = "failed"
	status, _ = h.verifyAuditRange(context.Background(), a, rows)
	if status != "failed" {
		t.Fatal("failed archive must not be shown as verified")
	}
	h.EndpointAllowed = func(string) bool { return false }
	status, _ = h.verifyAuditRange(context.Background(), a, rows)
	if status != "unavailable" {
		t.Fatal("unregistered endpoint must not be contacted")
	}
}
func TestAuditBooleanAndTimeFiltersRemainStrict(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/v1/audit-records?from=2026-10-11T03:00:00Z&to=2026-10-11T00:00:00Z", nil)
	if _, err := auditFilter(r); err == nil {
		t.Fatal("reversed times accepted")
	}
	r = httptest.NewRequest("GET", "/api/v1/audit-records?subject=a&subject=b", nil)
	if _, err := auditFilter(r); err == nil {
		t.Fatal("duplicate filters accepted")
	}
}
