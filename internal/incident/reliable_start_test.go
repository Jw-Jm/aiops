package incident

import (
	"ops-platform/internal/finding"
	"testing"
	"time"
)

func TestUnreliableFindingCannotSupplyDurationStart(t *testing.T) {
	at := time.Now().UTC()
	f := finding.Finding{Envelope: finding.Envelope{StartsAt: at}}
	if reliableStart(f) != nil {
		t.Fatal("unreliable clock became a duration sample")
	}
	f.TimeReliable = true
	if got := reliableStart(f); got == nil || !got.Equal(at) {
		t.Fatal("reliable clock lost")
	}
	f.StartsAt = time.Time{}
	if reliableStart(f) != nil {
		t.Fatal("missing event time accepted")
	}
}
