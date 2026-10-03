package k8sgpt

import (
	"encoding/json"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/finding"
	official "ops-platform/internal/inspection/kubernetes"
	"ops-platform/internal/resource"
	"time"
)

// Candidates consumes the unchanged no-LLM Analyzer verdict and exact native
// UIDs read through this invocation's broker. Official Condition/Event symptoms
// take priority. No diagnostic free text or Sensitive map becomes a fact or a
// confirmation predicate; Analyzer-specific symptoms remain separate hypotheses.
func Candidates(out Output, objects []unstructured.Unstructured, tenant, cluster string, observed time.Time) ([]finding.FindingCandidate, error) {
	if observed.IsZero() {
		return nil, ErrDrift
	}
	native := map[string]unstructured.Unstructured{}
	for _, o := range objects {
		name := o.GetName()
		if o.GetKind() != "Node" {
			name = o.GetNamespace() + "/" + name
		}
		key := o.GetKind() + "|" + name
		if _, exists := native[key]; exists || o.GetUID() == "" {
			return nil, ErrDrift
		}
		native[key] = o
	}
	faults := map[string]bool{}
	for _, r := range out.Results {
		key := r.Kind + "|" + r.Name
		if _, exists := native[key]; !exists || faults[key] {
			return nil, ErrDrift
		}
		faults[key] = true
	}
	candidates := []finding.FindingCandidate{}
	for _, o := range objects {
		name := o.GetName()
		if o.GetKind() != "Node" {
			name = o.GetNamespace() + "/" + name
		}
		firing := faults[o.GetKind()+"|"+name]
		if firing && len(official.Inspect(o)) > 0 {
			continue
		}
		if _, ok := o.Object["status"].(map[string]any); !ok {
			continue
		}
		symptom := map[string]string{"Pod": "PodRuntimeFault", "Node": "NodeAnalyzerFault", "PersistentVolumeClaim": "StorageAnalyzerFault"}[o.GetKind()]
		if symptom == "" {
			return nil, ErrDrift
		}
		state := "resolved"
		if firing {
			state = "firing"
		}
		data, _ := json.Marshal(map[string]any{"nativeKind": o.GetKind(), "nativeUID": string(o.GetUID()), "analyzer": "k8sgpt/v0.3.41/f071b32aa85d77b197cd2d0f9868294f7b55c5eb", "symptom": symptom, "fault": firing})
		id := resource.CanonicalID{Domain: "k8s", Tenant: tenant, Scope: cluster, APIGroup: "core", Kind: o.GetKind(), StableID: string(o.GetUID())}
		candidates = append(candidates, finding.FindingCandidate{ResourceCanonicalID: id.String(), Namespace: o.GetNamespace(), RuleID: "k8sgpt/" + symptom + "/v1", RuleFamily: "kubernetes-analyzer", NormalizedSymptom: symptom, State: state, NativeIdentity: string(o.GetUID()) + "/" + o.GetResourceVersion() + "/" + finding.Hash(data), IndependenceGroup: string(o.GetUID()), ObservedAt: observed, TimeReliable: true, QueryTemplateVersion: "sp05-k8sgpt/v1", Data: data})
	}
	return candidates, nil
}
