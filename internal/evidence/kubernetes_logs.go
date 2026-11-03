package evidence

// The persisted collector marker fixes the upstream metadata format in the source binding.
const KubernetesLogCollector = "vlagent-kubernetes/v1"

func (b Binding) KubernetesLogArchive() bool {
	return b.SourceType == "victorialogs" && b.ScopeMapping.RequiredLabels["collector"] == KubernetesLogCollector
}
func (b Binding) LogField(field string) string {
	if b.KubernetesLogArchive() {
		switch field {
		case "namespace":
			return "kubernetes.pod_namespace"
		case "uid":
			return "kubernetes.pod_uid"
		}
	}
	return field
}
