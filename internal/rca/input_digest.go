package rca

import (
	"encoding/json"
	"ops-platform/internal/evidence"
	"ops-platform/internal/finding"
	"slices"
	"strings"
	"time"
)

// Delivery/evaluation time is not an idempotency input. The result's expiry,
// partial and degraded state is, so an actual post-check change appends a new
// revision while a crash/retry with identical facts reuses the old revision.
func InputDigest(input Input, result Result) string {
	input.EvaluatedAt = time.Time{}
	input.From = time.Time{}
	input.To = time.Time{}
	input.Graph.CollectedAt = time.Time{}
	result.Impact.CollectedAt = time.Time{}
	input.Evidence = append([]evidence.Evidence(nil), input.Evidence...)
	for n := range input.Evidence {
		input.Evidence[n].ArchiveRef = nil
		input.Evidence[n].Data = nil
		input.Evidence[n].FactSlice = nil
	}
	slices.SortFunc(input.Evidence, func(a, b evidence.Evidence) int { return strings.Compare(a.EvidenceID, b.EvidenceID) })
	return finding.Hash(struct {
		Input  Input
		Result Result
	}{input, result})
}
func sameMetadata(actual, supplied evidence.Evidence) bool {
	for _, e := range []*evidence.Evidence{&actual, &supplied} {
		e.ArchiveRef = nil
		e.ReplayState = ""
		e.FactSlice = nil
		e.Data = nil
	}
	a, _ := json.Marshal(actual)
	b, _ := json.Marshal(supplied)
	return string(a) == string(b)
}
