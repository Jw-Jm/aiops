package persistence

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestHashIdempotencyKeyStoresOnlyDigest(t *testing.T) {
	key := "client-request-42"
	first := HashIdempotencyKey(key)
	if first != HashIdempotencyKey(key) {
		t.Fatal("idempotency key digest is not stable")
	}
	if strings.Contains(first, key) || !validDigest(first) {
		t.Fatalf("idempotency key was not reduced to a SHA-256 digest: %q", first)
	}
}

func TestIdempotencyRequestValidation(t *testing.T) {
	scope := Scope{
		TenantID:  uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987301"),
		Subject:   "operator-a",
		Operation: "POST /api/v1/source-registrations",
	}
	digest := Digest(HashIdempotencyKey("canonical-request"))
	for _, key := range []string{"", " has-space", "has-space ", "contains\nnewline", strings.Repeat("x", 513)} {
		if err := validateIdempotencyRequest(scope, key, digest); !errors.Is(err, ErrInvalidIdempotencyRequest) {
			t.Errorf("key %q returned %v, want invalid request", key, err)
		}
	}
	if err := validateIdempotencyRequest(scope, "valid-key_42", digest); err != nil {
		t.Fatalf("valid idempotency request rejected: %v", err)
	}
}

func TestReplayHeadersRejectCredentials(t *testing.T) {
	for _, headers := range []map[string]string{
		{"Set-Cookie": "session=secret"},
		{"Authorization": "Bearer token"},
		{"Location": "/safe\r\nSet-Cookie: bad=true"},
	} {
		if _, err := marshalReplayHeaders(headers); !errors.Is(err, ErrInvalidIdempotencyRequest) {
			t.Errorf("unsafe replay headers %v returned %v", headers, err)
		}
	}
	if _, err := marshalReplayHeaders(map[string]string{"Location": "/safe", "ETag": "\"revision-1\""}); err != nil {
		t.Fatalf("safe replay headers rejected: %v", err)
	}
}
