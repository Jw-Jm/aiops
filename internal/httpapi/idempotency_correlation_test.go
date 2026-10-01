package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"ops-platform/internal/auth"
)

func TestIdempotencyErrorsRetainVerifiedRequestID(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/tenants", nil)
	request.Header.Set("X-Request-ID", "header-id")
	request = request.WithContext(auth.WithRequestContext(request.Context(), auth.RequestContext{RequestID: "verified-id"}))
	response := httptest.NewRecorder()
	IdempotencyMiddleware{}.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("misconfigured middleware reached business handler")
	})).ServeHTTP(response, request)
	var envelope struct {
		RequestID string `json:"requestId"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.RequestID != "verified-id" {
		t.Fatalf("error lost verified request correlation: %q", envelope.RequestID)
	}
}
