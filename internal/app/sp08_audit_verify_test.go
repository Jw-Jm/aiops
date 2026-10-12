package app

import (
	"crypto/ed25519"
	"net/http"
	"net/http/httptest"
	"ops-platform/internal/auth"
	"strings"
	"testing"
)

func TestAuditWorkloadVerifierRequiresMutualTLSBeforeReadingContextOrDatabase(t *testing.T) {
	for _, method := range []string{"GET", "POST"} {
		r := httptest.NewRequest(method, "/internal/v1/audit:verify", strings.NewReader(`{"tenantId":"forged"}`))
		w := httptest.NewRecorder()
		serveWorkerAudit(w, r, auth.WorkloadTrust{}, ed25519.PublicKey{}, nil, "test-ns", nil)
		if w.Code != http.StatusForbidden {
			t.Fatalf("untrusted workload reached verifier: %d", w.Code)
		}
	}
}
