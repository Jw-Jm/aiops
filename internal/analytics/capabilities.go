package analytics

import (
	"context"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/resource"
	"regexp"
	"slices"
	"sort"
	"time"
)

type SourceCapability struct {
	SourceID         string    `json:"sourceId"`
	SourceType       string    `json:"sourceType"`
	Revision         int64     `json:"revision"`
	DatasetID        string    `json:"datasetId"`
	Operation        string    `json:"operation"`
	CollectorVersion string    `json:"collectorVersion"`
	ProofDigest      string    `json:"proofDigest"`
	CoverageFrom     time.Time `json:"coverageFrom"`
	CoverageTo       time.Time `json:"coverageTo"`
	VerifiedUntil    time.Time `json:"verifiedUntil"`
	MaxWindowSeconds int64     `json:"maxWindowSeconds"`
	Status           string    `json:"status"`
}

// Only a live authorized worker may disclose qualification metadata. Neither
// static dataset membership nor an old Evidence adapter implies availability.
func (v *Victoria) Capabilities(ctx context.Context, scope graph.Scope) ([]SourceCapability, error) {
	out := []SourceCapability{}
	if scope.Tenant != v.binding.Tenant || v.binding.ScopeMapping.RequiredLabels["tenant"] != scope.Tenant || v.binding.ScopeMapping.NativeTenant != "" || !slices.Contains(v.binding.ScopeMapping.Scopes["cluster"], scope.Cluster) || v.authorize(ctx, v.binding) != nil {
		return out, ErrDenied
	}
	for _, ns := range scope.Namespaces {
		if !slices.Contains(v.binding.ScopeMapping.Scopes["namespace"], ns) {
			return out, nil
		}
	}
	uids := []string{}
	for _, id := range scope.Resources {
		parsed, err := resource.ParseCanonicalID(id)
		if err != nil || parsed.Domain != "k8s" || !slices.Contains([]string{"Pod", "Node"}, parsed.Kind) || parsed.Tenant != scope.Tenant || parsed.Scope != scope.Cluster {
			return out, nil
		}
		uids = append(uids, parsed.StableID)
	}
	catalog, err := Catalog()
	if err != nil {
		return out, err
	}
	for _, q := range v.currentQualifications() {
		if !qualifiedUIDsAllow(q.ResourceUIDs, uids) {
			continue
		}
		if q.SourceRevision != v.binding.Revision || q.BindingDigest != evidence.BindingScopeDigest(v.binding) || !q.VerifiedUntil.After(time.Now()) || !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(q.EvidenceDigest) || q.CollectorVersion == "" || q.MaxWindowSeconds < 1 || !q.CoverageTo.After(q.CoverageFrom) {
			continue
		}
		operation := q.Operation
		if operation == "" {
			operation = "aggregate"
		}
		for _, d := range catalog.Datasets {
			if d.ID != q.DatasetID || d.Source != v.binding.SourceType || !slices.Contains(d.Operations, operation) {
				continue
			}
			resourceKind := "Pod"
			if d.ID == "metrics.node-ready" {
				resourceKind = "Node"
			}
			compatible := true
			for _, id := range scope.Resources {
				parsed, _ := resource.ParseCanonicalID(id)
				if parsed.Kind != resourceKind {
					compatible = false
				}
			}
			if !compatible {
				continue
			}
			if d.ID == "metrics.node-ready" {
				if !scope.ClusterScoped {
					continue
				}
			} else if len(scope.Namespaces) == 0 {
				continue
			}
			status := "qualification_configured"
			if q.Continuous {
				status = "continuous_verified"
			}
			out = append(out, SourceCapability{v.binding.SourceID, v.binding.SourceType, v.binding.Revision, q.DatasetID, operation, q.CollectorVersion, q.Digest(), q.CoverageFrom, q.CoverageTo, q.VerifiedUntil, q.MaxWindowSeconds, status})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].DatasetID == out[j].DatasetID {
			return out[i].Operation < out[j].Operation
		}
		return out[i].DatasetID < out[j].DatasetID
	})
	return out, nil
}

// A proof for one captured UID cannot qualify an unobserved or recreated Pod.
func qualifiedUIDsAllow(qualified, selected []string) bool {
	if len(qualified) == 0 {
		return true
	}
	if len(selected) == 0 {
		return false
	}
	for _, uid := range selected {
		if !slices.Contains(qualified, uid) {
			return false
		}
	}
	return true
}
