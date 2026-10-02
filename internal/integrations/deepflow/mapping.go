package deepflow

import (
	"context"
	"errors"
	"ops-platform/internal/resource"
)

// FrozenMappings contains exact Canonical IDs with UID provenance from the
// frozen Server/Querier Contract. It is accepted only in fixture_only mode;
// this is deliberately not a name-based live identity discovery algorithm.
type FrozenMappings map[string]Endpoint

func (m FrozenMappings) ByCanonical(_ context.Context, id string) (Endpoint, error) {
	e, ok := m[id]
	canonical, err := resource.ParseCanonicalID(id)
	if !ok || err != nil || canonical.Domain != "k8s" || canonical.Kind != "Pod" || e.CanonicalID != id {
		return e, errors.New("endpoint unresolved")
	}
	return e, nil
}
func (m FrozenMappings) ByBackend(_ context.Context, id uint64) (Endpoint, error) {
	var found Endpoint
	count := 0
	for _, e := range m {
		if e.PodID == id {
			count++
			found = e
		}
	}
	if count != 1 {
		return Endpoint{}, errors.New("endpoint ambiguous or unresolved")
	}
	return found, nil
}
