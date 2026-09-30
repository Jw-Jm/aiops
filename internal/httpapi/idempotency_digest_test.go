package httpapi

import (
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"ops-platform/internal/persistence"
)

func TestIdempotencyDigestCanonicalizesJSON(t *testing.T) {
	scope := persistence.Scope{TenantID: uuid.Must(uuid.NewV7()), Subject: "operator", Operation: "create-source-registration"}
	r := httptest.NewRequest("POST", "/api/v1/admin/source-registrations", nil)
	r.Header.Set("Content-Type", "application/json")
	first, err := canonicalRequestDigest(scope, r, []byte(`{"a":1,"b":2}`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := canonicalRequestDigest(scope, r, []byte("{\n  \"b\": 2.0, \"a\": 1\n}"))
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("equivalent JSON retries have different request digests")
	}
	if _, err := canonicalRequestDigest(scope, r, []byte(`{"a":1,"a":2}`)); err == nil {
		t.Fatal("ambiguous duplicate JSON fields accepted")
	}
}
