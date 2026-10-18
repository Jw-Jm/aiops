package action

import "ops-platform/internal/resource"

type PostCheckSupport struct {
	Status     string `json:"status"`
	TargetKind string `json:"targetKind,omitempty"`
	Recipe     string `json:"recipe,omitempty"`
	Reason     string `json:"reason"`
}

// Applicability is a declared adapter boundary, never proof that the current
// source, Recipe activation or archived observation has qualified.
func PostCheckApplicability(target string) PostCheckSupport {
	id, err := resource.ParseCanonicalID(target)
	out := PostCheckSupport{Status: "unsupported", Reason: "no_trusted_post_check"}
	if err != nil {
		return out
	}
	out.TargetKind = id.Kind
	if id.Domain != "k8s" || id.APIGroup != "core" {
		return out
	}
	switch id.Kind {
	case "Node":
		out.Recipe = "node-failure"
	case "PersistentVolumeClaim":
		out.Recipe = "pvc-csi-failure"
	default:
		return out
	}
	out.Status, out.Reason = "requires_qualified_source", "native_recipe_and_evidence_required"
	return out
}
