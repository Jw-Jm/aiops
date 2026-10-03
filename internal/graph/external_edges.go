package graph

import (
	"encoding/json"
	"errors"
	"ops-platform/internal/resource"
	"slices"
	"strings"
	"time"
)

func (g *Graph) AddExternal(epoch int64, edges []resource.Relation) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.addExternalLocked(epoch, edges)
}
func (g *Graph) addExternalLocked(epoch int64, edges []resource.Relation) error {
	if epoch != g.epoch || !g.now().Before(g.deadline) {
		return ErrStale
	}
	if len(edges) > 400 {
		return errors.New("external edge budget exhausted")
	}
	now := g.now()
	valid := []resource.Relation{}
	for _, e := range edges {
		from, err := resource.ParseCanonicalID(e.From)
		if err != nil {
			return err
		}
		to, err := resource.ParseCanonicalID(e.To)
		if err != nil {
			return err
		}
		if from.Tenant != g.tenant || to.Tenant != g.tenant || from.Scope != g.cluster || to.Scope != g.cluster || e.ObservedAt.IsZero() || e.ObservedAt.After(now) || e.TTLSeconds < 1 || e.TTLSeconds > 3600 {
			return ErrScope
		}
		if !now.Before(e.ObservedAt.Add(time.Duration(e.TTLSeconds) * time.Second)) {
			continue
		}
		valid = append(valid, e)
	}
	// Delivery order and exact duplicate facts cannot invalidate a current
	// frozen proof. Genuine provenance, observation, TTL or endpoint changes do.
	normalize := func(edges []resource.Relation) ([]string, error) {
		keys := make([]string, 0, len(edges))
		for _, e := range edges {
			raw, err := json.Marshal(e)
			if err != nil {
				return nil, ErrScope
			}
			keys = append(keys, string(raw))
		}
		slices.Sort(keys)
		return slices.Compact(keys), nil
	}
	oldKeys, err := normalize(g.overlays)
	if err != nil {
		return err
	}
	newKeys, err := normalize(valid)
	if err != nil {
		return err
	}
	if slices.Equal(oldKeys, newKeys) {
		return nil
	}
	slices.SortFunc(valid, func(a, b resource.Relation) int {
		left, _ := json.Marshal(a)
		right, _ := json.Marshal(b)
		return strings.Compare(string(left), string(right))
	})
	valid = slices.CompactFunc(valid, func(a, b resource.Relation) bool {
		left, _ := json.Marshal(a)
		right, _ := json.Marshal(b)
		return string(left) == string(right)
	})
	g.overlays = slices.Clone(valid)
	g.bumpGeneration()
	return nil
}

func (g *Graph) bumpGeneration() {
	if g.current != nil {
		next := *g.current
		next.revision.GraphGeneration++
		g.current = &next
	}
}
func (g *Graph) expireExternal(now time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	valid := make([]resource.Relation, 0, len(g.overlays))
	for _, e := range g.overlays {
		if now.Before(e.ObservedAt.Add(time.Duration(e.TTLSeconds) * time.Second)) {
			valid = append(valid, e)
		}
	}
	if len(valid) != len(g.overlays) {
		g.overlays = valid
		g.bumpGeneration()
	}
}

// ReplaceExternalSource preserves independently observed overlays from other
// sources. All edges retain their source observation time; reads never renew TTL.
func (g *Graph) ReplaceExternalSource(epoch int64, source string, edges []resource.Relation) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if source == "" {
		return ErrScope
	}
	merged := []resource.Relation{}
	for _, e := range g.overlays {
		if e.Provenance.SourceRegistrationID != source {
			merged = append(merged, e)
		}
	}
	for _, e := range edges {
		if e.Provenance.SourceRegistrationID != source {
			return ErrScope
		}
		merged = append(merged, e)
	}
	return g.addExternalLocked(epoch, merged)
}

// ObserveExternalSource combines distinct observations without renewing old
// facts. Only a strictly newer source timestamp replaces the same relation.
func (g *Graph) ObserveExternalSource(epoch int64, source string, edges []resource.Relation) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if source == "" {
		return ErrScope
	}
	now := g.now()
	merged := map[string]resource.Relation{}
	key := func(e resource.Relation) string {
		return e.Provenance.SourceRegistrationID + "|" + e.From + "|" + e.Kind + "|" + e.To
	}
	for _, e := range g.overlays {
		if now.Before(e.ObservedAt.Add(time.Duration(e.TTLSeconds) * time.Second)) {
			merged[key(e)] = e
		}
	}
	for _, e := range edges {
		if e.Provenance.SourceRegistrationID != source {
			return ErrScope
		}
		k := key(e)
		old, exists := merged[k]
		if !exists || e.ObservedAt.After(old.ObservedAt) {
			merged[k] = e
		}
	}
	combined := []resource.Relation{}
	for _, e := range merged {
		combined = append(combined, e)
	}
	return g.addExternalLocked(epoch, combined)
}
