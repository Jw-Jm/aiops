package evidence

const KubernetesMetricsCollector = "victoriametrics-kubernetes/v1"

func (b Binding) KubernetesMetrics() bool {
	return b.SourceType == "victoriametrics" && b.ScopeMapping.RequiredLabels["collector"] == KubernetesMetricsCollector
}
