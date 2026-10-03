package incident

import (
	"encoding/json"
	"ops-platform/internal/evidence"
)

func decodeEvidence(raw []byte) (evidence.Evidence, error) {
	var e evidence.Evidence
	err := json.Unmarshal(raw, &e)
	return e, err
}
