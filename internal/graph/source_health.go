package graph

import "slices"

func (g *Graph) SetSourceDegraded(source, reason string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if reason == "" {
		if _, ok := g.degraded[source]; ok {
			delete(g.degraded, source)
			g.bumpGeneration()
		}
		return
	}
	if g.degraded[source] != reason {
		g.degraded[source] = reason
		g.bumpGeneration()
	}
}
func (g *Graph) applySourceHealth(out *Result) {
	ids := []string{}
	for source := range g.degraded {
		ids = append(ids, source)
	}
	slices.Sort(ids)
	for _, source := range ids {
		out.Partial = true
		out.DegradedSources = append(out.DegradedSources, source)
		out.Warnings = append(out.Warnings, g.degraded[source])
	}
}
