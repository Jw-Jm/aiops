package investigation

import (
	"errors"
	"strings"
	"testing"
)

func TestSP08ModelFailureCategory(t *testing.T) {
	for _, code := range []string{"MODEL_UNAVAILABLE", "MODEL_TIMEOUT", "MODEL_RATE_LIMITED", "MODEL_FAILURE_SETTLEMENT_REJECTED"} {
		err := DecodeInvestigatorFailure(strings.NewReader(`{"errorCode":"` + code + `","detail":"password=private"}`))
		var failure ModelFailure
		if !errors.As(err, &failure) || failure.Code != code || err.Error() != code {
			t.Fatalf("category not preserved: %v", err)
		}
	}
	for _, body := range []string{`{"errorCode":"password=private"}`, `not json password=private`, `{"errorCode":"` + strings.Repeat("x", 5000) + `"}`} {
		err := DecodeInvestigatorFailure(strings.NewReader(body))
		if err.Error() != "INVESTIGATOR_UNAVAILABLE" {
			t.Fatalf("unsafe category: %v", err)
		}
	}
}
