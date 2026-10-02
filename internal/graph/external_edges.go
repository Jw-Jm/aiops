package graph

import (
	"errors"
	"ops-platform/internal/resource"
	"slices"
	"time"
)

func (g *Graph) AddExternal(epoch int64, edges []resource.Relation) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.addExternalLocked(epoch, edges)
}
func (g *Graph) addExternalLocked(epoch int64, edges []resource.Relation) error {
	if epoch != g.epoch || !time.Now().Before(g.deadline) {
		return ErrStale
	}
	if len(edges) > 400 {
		return errors.New("external edge budget exhausted")
	}
	now := time.Now()
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
	now := time.Now()
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
