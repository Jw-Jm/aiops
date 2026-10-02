package observability

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSemanticCompletenessDistinguishesSuccessfulHTTPResponses(t *testing.T) {
	m := NewMetrics()
	m.ObserveHTTP("POST", 200, time.Millisecond)
	m.ObserveQuery("evidence", false, "fresh")
	m.ObserveHTTP("POST", 200, time.Millisecond)
	m.ObserveQuery("evidence", true, "unavailable")
	r := httptest.NewRecorder()
	m.Handler().ServeHTTP(r, httptest.NewRequest("GET", "/metrics", nil))
	for _, line := range []string{`platform_query_completeness_total{area="evidence",completeness="complete",freshness="fresh"} 1`, `platform_query_completeness_total{area="evidence",completeness="partial",freshness="unavailable"} 1`, `platform_http_requests_total{method="POST",status_class="2xx"} 2`} {
		if !strings.Contains(r.Body.String(), line) {
			t.Fatalf("semantic result not observed: %s", r.Body.String())
		}
	}
}
