package incident

import (
	"errors"
	"ops-platform/internal/finding"
	"ops-platform/internal/upstream/keepmodel"
	"time"
)

var ErrRevision = errors.New("REVISION_CONFLICT")
var ErrTransition = errors.New("INVALID_STATE_TRANSITION")

const PolicyVersion = "correlation/nonvirtual/v1"
const CorrelationWindow = 10 * time.Minute
const ReopenWindow = 30 * time.Minute
const SettleWindow = 5 * time.Minute

// The licensed Community active/closed model is consumed at this narrow boundary.
// Platform-specific suppression, mitigation and CAS are not supplied by Keep.
func Active(state string) bool {
	return keepmodel.Active(map[string]string{"open": "firing", "acknowledged": "acknowledged"}[state]) || state == "mitigating" || state == "suppressed"
}
func Fingerprint(f finding.Finding, policy string) string {
	return finding.Hash([]string{f.TenantID, f.ClusterUID, f.ResourceCanonicalID, f.RuleFamily, f.NormalizedSymptom, policy})
}
func CanTransition(from, to string) bool {
	whitelist := map[string][]string{"open": {"acknowledged", "mitigating", "suppressed", "resolved"}, "acknowledged": {"mitigating", "suppressed", "resolved"}, "mitigating": {"suppressed", "resolved"}, "suppressed": {"open", "closed"}, "resolved": {"open", "closed"}, "closed": {}}
	for _, s := range whitelist[from] {
		if s == to {
			return true
		}
	}
	return false
}
func RecoveryReady(now, known time.Time, allResolved, sourcesHealthy bool) bool {
	return !known.IsZero() && now.Sub(known) >= SettleWindow && allResolved && sourcesHealthy
}

type Incident struct {
	SchemaVersion       string     `json:"schemaVersion"`
	TenantID            string     `json:"tenantId"`
	IncidentID          string     `json:"incidentId"`
	State               string     `json:"state"`
	Revision            int64      `json:"revision"`
	Fingerprint         string     `json:"fingerprint"`
	PolicyVersion       string     `json:"policyVersion"`
	ClusterUID          string     `json:"clusterUid"`
	ResourceCanonicalID string     `json:"resourceCanonicalId"`
	Namespace           string     `json:"namespace"`
	ResolvedAt          *time.Time `json:"resolvedAt"`
	RecoveryKnownAt     *time.Time `json:"recoveryKnownAt"`
	CurrentRCARevision  int64      `json:"currentRcaRevision"`
	SuppressedUntil     *time.Time `json:"suppressedUntil"`
	StartedAt           time.Time  `json:"startedAt"`
	LastObservedAt      time.Time  `json:"lastObservedAt"`
	CreatedAt           time.Time  `json:"createdAt"`
	UpdatedAt           time.Time  `json:"updatedAt"`
}
