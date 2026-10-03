// SPDX-License-Identifier: Apache-2.0
// SP05 export adapter for the locked kubernetes-ontology diagnostic service.
// All scoring, tie-breaking and generic Helm ownership conflict detection remain
// in the unchanged upstream implementation. This file adds no algorithm.
package diagnostic

import "ops-platform/internal/upstream/ontology/api"

func RankEvidence(nodes []api.DiagnosticNode, edges []api.DiagnosticEdge) []api.RankedEvidence {
	return rankEvidence(nodes, edges)
}
func EvidenceConflicts(nodes []api.DiagnosticNode, edges []api.DiagnosticEdge) []api.DiagnosticConflict {
	return analyzeHelmContext(nodes, edges).Conflicts
}
