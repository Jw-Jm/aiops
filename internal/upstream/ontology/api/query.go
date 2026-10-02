package api

type QueryService interface {
	GetDiagnosticSubgraph(entry EntryRef, policy ExpansionPolicy) (DiagnosticSubgraph, error)
	GetDiagnosticSubgraphByPod(namespace, name string, policy ExpansionPolicy) (DiagnosticSubgraph, error)
}
