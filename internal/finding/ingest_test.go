package finding

import (
	"testing"
	"time"
)

func TestOccurrenceReducerTerminalOrdering(t *testing.T) {
	now := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	firing := Envelope{State: "firing", SourceSequence: 2, ObservedAt: now, TimeReliable: true}
	resolved := firing
	resolved.State = "resolved"
	resolved.SourceSequence = 3
	for _, c := range []struct {
		name     string
		previous *Finding
		input    Envelope
		change   bool
		state    string
	}{
		{"first", nil, firing, true, "firing"},
		{"resolved-first", nil, resolved, true, "resolved"},
		{"resolve", &Finding{State: "firing", SourceSequence: 2, ObservedAt: now}, resolved, true, "resolved"},
		{"late-firing", &Finding{State: "resolved", SourceSequence: 3, ObservedAt: now}, firing, false, "resolved"},
		{"higher-sequence-cannot-revive", &Finding{State: "resolved", SourceSequence: 1, ObservedAt: now}, firing, false, "resolved"},
		{"old-update", &Finding{State: "firing", SourceSequence: 5, ObservedAt: now}, firing, false, "firing"},
	} {
		t.Run(c.name, func(t *testing.T) {
			state, changed := Reduce(c.previous, c.input)
			if state != c.state || changed != c.change {
				t.Fatalf("got %s/%t want %s/%t", state, changed, c.state, c.change)
			}
		})
	}
}
func TestFingerprintAndSemanticDigest(t *testing.T) {
	e := Envelope{SchemaVersion: "finding-envelope/v2", EventID: "a", IdempotencyKey: "k", RuleID: "node-ready/v1", RuleFamily: "node", NormalizedSymptom: "NotReady", ResourceCanonicalID: "node-a", OccurrenceID: "o", State: "firing", ObservedAt: time.Now().UTC(), TimeReliable: true}
	a := Fingerprint("t", "s", "c", e)
	e.EventID = "b"
	e.IdempotencyKey = "another"
	e.PayloadDigest = "untrusted"
	b := Fingerprint("t", "s", "c", e)
	if a != b || a == Fingerprint("t", "other", "c", e) || a == Fingerprint("t", "s", "other", e) {
		t.Fatal("fingerprint binding drift")
	}
	d := SemanticDigest(e)
	e.EventID = "c"
	e.IdempotencyKey = "retry"
	e.PayloadDigest = ""
	if SemanticDigest(e) != d {
		t.Fatal("transport metadata changes semantics")
	}
	e.State = "resolved"
	if SemanticDigest(e) == d {
		t.Fatal("status digest collision")
	}
}

func TestSemanticDigestCanonicalizesEquivalentEventClockOffsets(t *testing.T) {
	e := Envelope{StartsAt: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC), ObservedAt: time.Date(2026, 10, 2, 0, 0, 1, 0, time.UTC), Payload: []byte(`{"ready":false}`)}
	first := SemanticDigest(e)
	e.StartsAt = e.StartsAt.In(time.FixedZone("CST", 8*3600))
	e.ObservedAt = e.ObservedAt.In(time.FixedZone("CST", 8*3600))
	if first != SemanticDigest(e) {
		t.Fatal("equivalent timestamp offsets changed semantic digest")
	}
}
