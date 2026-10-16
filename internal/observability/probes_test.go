package observability

import (
	"net/http/httptest"
	"testing"
)

func TestWorkerReadyOnlyAfterInitialization(t *testing.T) {
	r := &Runtime{Metrics: NewMetrics()}
	h := r.probeHandler()
	check := func(path string, want int) {
		t.Helper()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != want {
			t.Fatal(path, w.Code)
		}
	}
	check("/healthz", 200)
	check("/readyz", 503)
	r.Ready.Store(true)
	check("/readyz", 200)
	r.Ready.Store(false)
	check("/readyz", 503)
}
