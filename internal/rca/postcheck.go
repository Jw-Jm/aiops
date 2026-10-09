package rca

import (
	"encoding/json"
	"ops-platform/internal/evidence"
	"time"
)

// PostCheckResource reuses the signed Recipe's reviewed native primary health
// predicates. It makes no causal/accuracy claim and never uses command exit code.
func PostCheckResource(r Recipe, e evidence.Evidence, completed time.Time) (string, error) {
	baseline, err := BuiltinVersion(r.Name, r.Version)
	if err != nil || !sameJSON(r, baseline) || r.PostCheck != "fresh-required-evidence/v1" {
		return "inconclusive", ErrRecipe
	}
	if e.SourceSystem != "kubernetes" || e.QueryTemplateVersion != "kubernetes-projection/v1" || e.ReplayState != "archived_verified" || !e.TimeReliable || !e.ObservedFrom.After(completed) || !e.ObservedTo.After(completed) || e.ContentDigest != evidence.Digest(e.Data) {
		return "inconclusive", nil
	}
	var raw struct {
		Kind   string `json:"kind"`
		Status struct {
			Phase      string                          `json:"phase"`
			Conditions []struct{ Type, Status string } `json:"conditions"`
		} `json:"status"`
	}
	if json.Unmarshal(e.Data, &raw) != nil {
		return "inconclusive", nil
	}
	switch r.Name {
	case "pvc-csi-failure":
		if raw.Kind != "PersistentVolumeClaim" {
			return "inconclusive", nil
		}
		switch raw.Status.Phase {
		case "Bound":
			return "resolved", nil
		case "Pending", "Lost":
			return "not_resolved", nil
		}
	case "node-failure":
		if raw.Kind != "Node" {
			return "inconclusive", nil
		}
		for _, c := range raw.Status.Conditions {
			if c.Type == "Ready" {
				if c.Status == "True" {
					return "resolved", nil
				}
				if c.Status == "False" || c.Status == "Unknown" {
					return "not_resolved", nil
				}
			}
		}
	}
	return "inconclusive", nil
}
