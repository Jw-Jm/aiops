package identity

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// TestStepUpBudgetDenialIsBoundedAndRetryable keeps the HTTP-visible contract of
// a locked account budget: a retryable sentinel, a cooldown derived from the
// database row, and a hard upper bound so a broken row cannot turn into a
// forever-retry header.
func TestStepUpBudgetDenialIsBoundedAndRetryable(t *testing.T) {
	var typed *StepUpBudgetError
	err := budgetDenial(nil)
	if !errors.Is(err, ErrMFAStepUpBudget) {
		t.Fatalf("budget denial does not match the sentinel: %v", err)
	}
	if !errors.As(err, &typed) {
		t.Fatalf("budget denial has no typed cooldown: %v", err)
	}
	if typed.RetryAfter != StepUpBudgetCooldown {
		t.Fatalf("default cooldown = %s, want %s", typed.RetryAfter, StepUpBudgetCooldown)
	}

	future := time.Now().Add(4 * time.Minute)
	err = budgetDenial(&future)
	if !errors.As(err, &typed) {
		t.Fatalf("budget denial has no typed cooldown: %v", err)
	}
	if typed.RetryAfter > 4*time.Minute || typed.RetryAfter < 3*time.Minute {
		t.Fatalf("cooldown = %s, want about four minutes", typed.RetryAfter)
	}

	stale := time.Now().Add(-time.Hour)
	err = budgetDenial(&stale)
	if !errors.As(err, &typed) {
		t.Fatalf("budget denial has no typed cooldown: %v", err)
	}
	if typed.RetryAfter != time.Second {
		t.Fatalf("stale cooldown = %s, want the one second floor", typed.RetryAfter)
	}

	remote := time.Now().Add(48 * time.Hour)
	err = budgetDenial(&remote)
	if !errors.As(err, &typed) {
		t.Fatalf("budget denial has no typed cooldown: %v", err)
	}
	if typed.RetryAfter != StepUpBudgetCooldown {
		t.Fatalf("remote cooldown = %s, want the %s policy cap", typed.RetryAfter, StepUpBudgetCooldown)
	}
}

// TestMFADBErrorsKeepBudgetCredentialAndResetGapsSeparate proves the SQLSTATE
// mapping that the HTTP layer relies on: a locked budget must never be reported
// as a wrong verification code, and a reset without a preservable password must
// never be reported as a successful reset.
func TestMFADBErrorsKeepBudgetCredentialAndResetGapsSeparate(t *testing.T) {
	if err := mapMFADBError("fail", &pgconn.PgError{Code: "42900"}); !errors.Is(err, ErrMFAStepUpBudget) {
		t.Fatalf("42900 mapped to %v, want ErrMFAStepUpBudget", err)
	}
	if err := mapMFADBError("fail", &pgconn.PgError{Code: "55000"}); !errors.Is(err, ErrMFAResetCredentialUnavailable) {
		t.Fatalf("55000 mapped to %v, want ErrMFAResetCredentialUnavailable", err)
	}
	if err := mapMFADBError("fail", &pgconn.PgError{Code: "28000"}); !errors.Is(err, ErrMFAChallengeInvalid) {
		t.Fatalf("28000 mapped to %v, want ErrMFAChallengeInvalid", err)
	}
	if err := mapMFADBError("fail", &pgconn.PgError{Code: "40001"}); !errors.Is(err, ErrSessionVersion) {
		t.Fatalf("40001 mapped to %v, want ErrSessionVersion", err)
	}
	verification := mapMFADBError("fail", &pgconn.PgError{Code: "22023"})
	if errors.Is(verification, ErrMFAStepUpBudget) || errors.Is(verification, ErrMFAResetCredentialUnavailable) {
		t.Fatalf("invalid request leaked into a budget/reset result: %v", verification)
	}
}
