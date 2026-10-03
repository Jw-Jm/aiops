package finding

import (
	"encoding/json"
	"time"
)

// FindingCandidate is the only Inspector output admitted by the Worker. Source
// and tenant are deliberately absent; the immutable collector binding supplies
// both before Archive/ingestion. NativeIdentity contains a native UID/URI and
// resource version or content digest, never a caller-controlled executable.
type FindingCandidate struct {
	ResourceCanonicalID  string
	Namespace            string
	RuleID               string
	RuleFamily           string
	NormalizedSymptom    string
	State                string
	NativeIdentity       string
	IndependenceGroup    string
	ObservedAt           time.Time
	TimeReliable         bool
	QueryTemplateVersion string
	Data                 json.RawMessage
	EvidenceRefs         []string
}
