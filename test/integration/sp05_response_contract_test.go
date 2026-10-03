package integration

import (
	"net/url"
	"ops-platform/internal/contract"
	"strings"
	"testing"
)

func assertSP05PublicResponseContract(t *testing.T, path string, status int, raw []byte) {
	t.Helper()
	u, err := url.ParseRequestURI(path)
	if err != nil {
		t.Fatal(err)
	}
	p := u.Path
	schema := ""
	switch {
	case p == "/api/v2/findings:ingest":
		schema = "finding-ingestion-success"
	case p == "/api/v1/findings":
		schema = "finding-page"
	case strings.HasPrefix(p, "/api/v1/findings/"):
		schema = "finding-success"
	case p == "/api/v1/incidents:merge":
		schema = "incident-success"
	case p == "/api/v1/incidents":
		schema = "incident-page"
	case strings.HasPrefix(p, "/api/v1/incidents/"):
		switch {
		case strings.Contains(p, "/rca/revisions/"):
			schema = "rca-revision-success"
		case strings.HasSuffix(p, "/rca/revisions"):
			schema = "rca-revision-page"
		case strings.HasSuffix(p, "/rca"):
			schema = "current-rca-success"
		case strings.HasSuffix(p, "/timeline"):
			schema = "incident-timeline-page"
		case strings.HasSuffix(p, "/evidence"):
			schema = "incident-evidence-page"
		default:
			schema = "incident-success"
		}
	}
	if schema == "" {
		return
	}
	if status >= 400 {
		if err := contract.Validate("https://ops.local/schemas/error-envelope/v2", raw); err != nil {
			if legacy := contract.Validate("https://ops.local/schemas/error-envelope/v1", raw); legacy != nil {
				t.Fatalf("actual SP05 error response violates both declared versions: %v", err)
			}
		}
		return
	}
	if status != 200 {
		t.Fatalf("unexpected SP05 success status %d", status)
	}
	if err := contract.Validate("https://ops.local/schemas/"+schema+"/v2", raw); err != nil {
		t.Fatalf("actual SP05 public response %s: %v", p, err)
	}
}
