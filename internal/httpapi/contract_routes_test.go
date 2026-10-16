package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPublicContractRoutesReachDomainHandler(t *testing.T) {
	calls := 0
	router, err := newContractRouter(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(http.StatusNoContent) }))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/api/v1/capabilities", 204},
		{"GET", "/api/v1/incidents/018f0f2b-91c2-7d42-a8dc-f719c5987302/rca/revisions", 204},
		{"POST", "/api/v1/command-executions", 204},
		{"POST", "/api/v2/findings:ingest", 204},
		{"GET", "/api/v1/does-not-exist", 404},
		{"DELETE", "/api/v1/capabilities", 405},
	} {
		before := calls
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(entry.method, entry.path, nil))
		if w.Code != entry.status {
			t.Fatalf("%s %s: %d %s", entry.method, entry.path, w.Code, w.Body.String())
		}
		if (calls > before) != (entry.status == 204) {
			t.Fatal("undeclared operation reached domain handler")
		}
	}
}
