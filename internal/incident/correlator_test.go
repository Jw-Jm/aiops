package incident

import (
	"ops-platform/internal/finding"
	"testing"
	"time"
)

func TestCorrelationFingerprintResourcePolicyIsolation(t *testing.T) {
	f := finding.Finding{Envelope: finding.Envelope{TenantID: "t", ClusterUID: "c", ResourceCanonicalID: "a", RuleFamily: "node", NormalizedSymptom: "NotReady"}}
	a := Fingerprint(f, "v1")
	f.ResourceCanonicalID = "b"
	if Fingerprint(f, "v1") == a {
		t.Fatal("different resources merged")
	}
	f.ResourceCanonicalID = "a"
	if Fingerprint(f, "v2") == a {
		t.Fatal("policy not versioned")
	}
}
func TestExplicitTransitionsRecoveryAndClosed(t *testing.T) {
	for _, p := range [][2]string{{"open", "resolved"}, {"acknowledged", "resolved"}, {"mitigating", "resolved"}, {"open", "suppressed"}, {"suppressed", "open"}, {"resolved", "open"}, {"resolved", "closed"}} {
		if !CanTransition(p[0], p[1]) {
			t.Fatal(p)
		}
	}
	for _, p := range [][2]string{{"closed", "open"}, {"suppressed", "resolved"}, {"resolved", "mitigating"}, {"open", "closed"}} {
		if CanTransition(p[0], p[1]) {
			t.Fatal(p)
		}
	}
	now := time.Now()
	if RecoveryReady(now, now.Add(-4*time.Minute), true, true) || RecoveryReady(now, now.Add(-6*time.Minute), true, false) || !RecoveryReady(now, now.Add(-6*time.Minute), true, true) {
		t.Fatal("settle/degradation gate")
	}
}
