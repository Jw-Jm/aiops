package incident

import (
	"testing"
	"time"
)

func TestFormalInitialPolicyWindows(t *testing.T) {
	if CorrelationWindow != 10*time.Minute {
		t.Errorf("formal correlation default=%s want 10m", CorrelationWindow)
	}
	if ReopenWindow != 30*time.Minute {
		t.Errorf("formal reopen default=%s want 30m", ReopenWindow)
	}
}
