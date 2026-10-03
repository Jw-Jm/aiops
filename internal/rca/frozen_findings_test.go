package rca

import (
	"encoding/json"
	"testing"
)

func TestInputDigestFreezesFindingRevisions(t *testing.T) {
	a, b := Input{}, Input{}
	if err := json.Unmarshal([]byte(`{"schemaVersion":"rca-input/v2","findingRevisions":[{"findingId":"finding-a","aggregateRevision":1,"occurrenceId":"occurrence-a","digest":"digest-a"}]}`), &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"schemaVersion":"rca-input/v2","findingRevisions":[{"findingId":"finding-a","aggregateRevision":2,"occurrenceId":"occurrence-a","digest":"digest-b"}]}`), &b); err != nil {
		t.Fatal(err)
	}
	if InputDigest(a, Result{}) == InputDigest(b, Result{}) {
		t.Fatal("changed Finding revisions vanished from immutable RCA input")
	}
}
