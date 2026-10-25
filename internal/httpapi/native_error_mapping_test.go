package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ops-platform/internal/identity"
)

func decodeErrorEnvelope(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	decoded := map[string]any{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("error body is not JSON: %s", recorder.Body.String())
	}
	return decoded
}

// TestMFAStepUpBudgetMapsToRetryableRateLimit keeps the account budget distinct
// from a rejected credential and surfaces the server cooldown to the client.
func TestMFAStepUpBudgetMapsToRetryableRateLimit(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeMFAError(recorder, &identity.StepUpBudgetError{RetryAfter: 90 * time.Second}, "req-1")
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", recorder.Code)
	}
	if retry := recorder.Header().Get("Retry-After"); retry != "90" {
		t.Fatalf("Retry-After = %q, want 90", retry)
	}
	body := decodeErrorEnvelope(t, recorder)
	if body["code"] != "RATE_LIMITED" {
		t.Fatalf("code = %v, want RATE_LIMITED", body["code"])
	}
	if body["retryable"] != true {
		t.Fatalf("budget answer must be retryable: %v", body)
	}
	if body["code"] == "UNAUTHENTICATED" {
		t.Fatalf("a locked budget was reported as a credential failure: %v", body)
	}
}

// TestPasswordWorkCongestionIsNotACredentialFailure covers every HTTP surface
// that spends password work: login/change through the session writer, the MFA
// password step and administrator create/reset.
func TestPasswordWorkCongestionIsNotACredentialFailure(t *testing.T) {
	for name, handler := range map[string]func(*httptest.ResponseRecorder, string){
		"mfa": func(recorder *httptest.ResponseRecorder, requestID string) {
			writeMFAError(recorder, identity.ErrPasswordWorkBusy, requestID)
		},
		"session": func(recorder *httptest.ResponseRecorder, requestID string) {
			writeNativeSessionError(recorder, identity.ErrPasswordWorkBusy, requestID)
		},
	} {
		recorder := httptest.NewRecorder()
		handler(recorder, "req-"+name)
		if recorder.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s status = %d, want 503", name, recorder.Code)
		}
		if retry := recorder.Header().Get("Retry-After"); retry != "1" {
			t.Fatalf("%s Retry-After = %q, want 1", name, retry)
		}
		body := decodeErrorEnvelope(t, recorder)
		if body["code"] != "UNAVAILABLE" || body["retryable"] != true {
			t.Fatalf("%s body = %v, want UNAVAILABLE/retryable", name, body)
		}
	}

	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users", nil)
	recorder := httptest.NewRecorder()
	writeNativeAdminError(recorder, request, identity.ErrPasswordWorkBusy)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("admin status = %d, want 503", recorder.Code)
	}
	if body := decodeErrorEnvelope(t, recorder); body["retryable"] != true {
		t.Fatalf("admin body = %v, want retryable", body)
	}
}

// TestMFAResetWithoutPreservablePasswordIsAConflict proves the reset refuses a
// broken account instead of implying the password survived.
func TestMFAResetWithoutPreservablePasswordIsAConflict(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeMFAError(recorder, identity.ErrMFAResetCredentialUnavailable, "req-2")
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", recorder.Code)
	}
	if body := decodeErrorEnvelope(t, recorder); body["code"] != "CONFLICT" {
		t.Fatalf("body = %v, want CONFLICT", body)
	}
}

// TestChallengeRemainingUsesTheTighterBudget keeps remainingAttempts meaningful
// after failures were spread over more than one challenge.
func TestChallengeRemainingUsesTheTighterBudget(t *testing.T) {
	budgetTwo := 2
	challenge := identity.StepUpChallenge{
		AttemptLimit: 5, Attempts: 3, State: "pending",
		RequiredMethods:  []string{"password", "totp"},
		CompletedMethods: []string{"password"},
		BudgetRemaining:  &budgetTwo,
		Target:           json.RawMessage(`{"kind":"user","revision":1}`),
	}
	view := challengeView(challenge)
	if view["remainingAttempts"] != 2 {
		t.Fatalf("challenge view remainingAttempts = %v, want 2", view["remainingAttempts"])
	}
	result := challengeResult(challenge, "password", nil, "invalid_credential")
	if result["remainingAttempts"] != 2 {
		t.Fatalf("verify result remainingAttempts = %v, want 2", result["remainingAttempts"])
	}

	revoked := challengeView(identity.StepUpChallenge{
		AttemptLimit: 5, Attempts: 5, State: "revoked",
		RequiredMethods:  []string{"password", "totp"},
		CompletedMethods: []string{},
		BudgetRemaining:  &budgetTwo,
		Target:           json.RawMessage(`{"kind":"user","revision":1}`),
	})
	if revoked["remainingAttempts"] != 0 {
		t.Fatalf("revoked challenge remainingAttempts = %v, want 0", revoked["remainingAttempts"])
	}

	five := 5
	roomy := challengeView(identity.StepUpChallenge{
		AttemptLimit: 5, Attempts: 0, State: "pending",
		RequiredMethods:  []string{"password", "totp"},
		CompletedMethods: []string{},
		BudgetRemaining:  &five,
		Target:           json.RawMessage(`{"kind":"user","revision":1}`),
	})
	if roomy["remainingAttempts"] != 5 {
		t.Fatalf("fresh challenge remainingAttempts = %v, want 5", roomy["remainingAttempts"])
	}
}
