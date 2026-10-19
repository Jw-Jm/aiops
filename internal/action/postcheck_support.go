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
	if id.Domain != "k8s" {
		return out
	}
	switch id.APIGroup + "/" + id.Kind {
	case "core/Node":
		out.Recipe = "node-failure"
	case "core/PersistentVolumeClaim":
		out.Recipe = "pvc-csi-failure"
	case "core/Pod":
		out.Recipe = "pod-health"
	case "apps/Deployment":
		out.Recipe = "deployment-health"
	case "core/Service", "discovery.k8s.io/EndpointSlice":
		out.Recipe = "service-endpoints-health"
	default:
		return out
	}
	out.Status, out.Reason = "requires_qualified_source", "native_recipe_and_evidence_required"
	return out
}
