package investigation

import (
	"encoding/json"
	"errors"
	"io"
)

type ModelFailure struct{ Code string }

func (e ModelFailure) Error() string { return e.Code }

// Only fixed diagnostic categories cross the authenticated workload boundary.
func DecodeInvestigatorFailure(body io.Reader) error {
	var envelope struct {
		ErrorCode string `json:"errorCode"`
	}
	if json.NewDecoder(io.LimitReader(body, 4097)).Decode(&envelope) != nil {
		return errors.New("INVESTIGATOR_UNAVAILABLE")
	}
	switch envelope.ErrorCode {
	case "MODEL_UNAVAILABLE", "MODEL_TIMEOUT", "MODEL_AUTHENTICATION_FAILED",
		"MODEL_CONTEXT_LIMIT", "MODEL_RATE_LIMITED", "MODEL_REQUEST_REJECTED", "MODEL_FAILURE",
		"MODEL_FAILURE_SETTLEMENT_REJECTED", "MODEL_RESULT_SETTLEMENT_REJECTED":
		return ModelFailure{Code: envelope.ErrorCode}
	default:
		return errors.New("INVESTIGATOR_UNAVAILABLE")
	}
}
