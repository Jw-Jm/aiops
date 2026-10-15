package investigation

import (
	"ops-platform/internal/graph"
	"testing"
	"time"
)

func TestResourceChangesVersionIsNotFieldOrEventTime(t *testing.T) {
	before := ResourceObservation{CanonicalID: "uid-1", ResourceVersion: "1", Source: graph.SourceAuthority{SourceRegistrationID: "s", Revision: 1, ScopeDigest: "scope"}, ObservedAt: time.Now(), Projection: map[string]any{"spec": map[string]any{"replicas": int64(1)}}}
	after := before
	after.ResourceVersion = "999999"
	after.ObservedAt = before.ObservedAt.Add(time.Second)
	after.Coverage = "continuous_watch"
	if c := CompareResourceObservations(before, after); c.Kind != "object_version_only" || c.TimeReliable || len(c.Fields) != 0 {
		t.Fatal(c)
	}
	after.Projection = map[string]any{"spec": map[string]any{"replicas": int64(2)}}
	if c := CompareResourceObservations(before, after); c.Kind != "observed_field_change" || c.TimeReliable {
		t.Fatal(c)
	}
	for _, gap := range []string{"initial_list", "coverage_gap"} {
		after.Coverage = gap
		if c := CompareResourceObservations(before, after); c.Kind != "history_gap" {
			t.Fatal(c)
		}
	}
	after.Coverage = "continuous_watch"
	after.CanonicalID = "replacement-uid"
	if c := CompareResourceObservations(before, after); c.Kind != "history_gap" {
		t.Fatal(c)
	}
}
