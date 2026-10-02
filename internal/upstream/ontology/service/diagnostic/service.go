package diagnostic

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"ops-platform/internal/upstream/ontology/api"
	"ops-platform/internal/upstream/ontology/model"
)

type graphReader interface {
 GetNode(model.CanonicalID) (model.Node, bool)
 ListNodes() []model.Node
 Neighbors(model.CanonicalID) []model.Edge
}

type Service struct {
	kernel graphReader
}

const (
	DefaultDiagnosticMaxNodes = 200
	DefaultDiagnosticMaxEdges = 400
	MaxRankedEvidence         = 20
)

func NewService(kernel graphReader) *Service {
	return &Service{kernel: kernel}
}

func DefaultExpansionPolicy() api.ExpansionPolicy {
	return api.ExpansionPolicy{
		MaxDepth:               2,
		StorageMaxDepth:        5,
		MaxNodes:               DefaultDiagnosticMaxNodes,
		MaxEdges:               DefaultDiagnosticMaxEdges,
		TerminalNodeKinds:      DefaultTerminalNodeKinds(),
		IncludeSiblingPods:     true,
		IncludeRBAC:            true,
		IncludeEvents:          true,
		IncludeWebhookEvidence: true,
		IncludeStorage:         true,
		IncludeOCI:             true,
	}
}

func DefaultTerminalNodeKinds() []api.NodeKind {
	return []api.NodeKind{
		api.NodeKindService,
		api.NodeKindConfigMap,
		api.NodeKindSecret,
		api.NodeKindServiceAccount,
		api.NodeKindNode,
		api.NodeKindRoleBinding,
		api.NodeKindClusterRoleBinding,
		api.NodeKindHelmChart,
		api.NodeKindEvent,
		api.NodeKindImage,
		api.NodeKindOCIArtifactMetadata,
		api.NodeKindWebhookConfig,
		api.NodeKindIngress,
		api.NodeKindEndpointSlice,
		api.NodeKindNetworkPolicy,
		api.NodeKindHPA,
		api.NodeKindPodDisruptionBudget,
		api.NodeKindVolumeAttachment,
	}
}

func (s *Service) GetDiagnosticSubgraph(entry api.EntryRef, policy api.ExpansionPolicy) (api.DiagnosticSubgraph, error) {
	return s.GetDiagnosticSubgraphContext(context.Background(), entry, policy)
}

func (s *Service) GetDiagnosticSubgraphContext(ctx context.Context, entry api.EntryRef, policy api.ExpansionPolicy) (api.DiagnosticSubgraph, error) {
	if entry.Kind == "" {
		return api.DiagnosticSubgraph{}, errors.New("entry kind is required")
	}
	if entry.CanonicalID == "" {
		return api.DiagnosticSubgraph{}, errors.New("canonical entry id is required for phase-1 service")
	}
	if policy.MaxDepth <= 0 {
		policy.MaxDepth = 1
	}
	if policy.StorageMaxDepth <= 0 {
		policy.StorageMaxDepth = policy.MaxDepth
	}
	budget := diagnosticBudget(policy)

	rootID := model.CanonicalID(entry.CanonicalID)
	rootNode, ok := s.kernel.GetNode(rootID)
	if !ok {
		return api.DiagnosticSubgraph{}, errors.New("entry node not found")
	}

	nodes := []api.DiagnosticNode{toAPINode(rootNode)}
	edges := make([]api.DiagnosticEdge, 0)
	seenNodes := map[string]struct{}{rootNode.ID.String(): {}}
	seenEdges := map[string]struct{}{}
	truncationReasons := map[string]struct{}{}
	frontier := []model.CanonicalID{rootID}
	maxTraversalDepth := policy.MaxDepth
	if policy.IncludeStorage && policy.StorageMaxDepth > maxTraversalDepth {
		maxTraversalDepth = policy.StorageMaxDepth
	}
	terminalKinds := terminalNodeKindSet(policy)

	for depth := 0; depth < maxTraversalDepth; depth++ {
		if err := ctx.Err(); err != nil {
			return api.DiagnosticSubgraph{}, err
		}
		nextFrontier := make([]model.CanonicalID, 0)
		for _, current := range frontier {
			if err := ctx.Err(); err != nil {
				return api.DiagnosticSubgraph{}, err
			}
			currentNode, currentOK := s.kernel.GetNode(current)
			if currentOK && s.isTerminalNode(currentNode, rootID, rootNode.Kind, terminalKinds, policy.ExpandTerminalNodes) {
				continue
			}
			for _, edge := range s.kernel.Neighbors(current) {
				if err := ctx.Err(); err != nil {
					return api.DiagnosticSubgraph{}, err
				}
				if !s.shouldTraverseFrom(current, currentNode, currentOK, rootNode, edge, depth, policy) {
					continue
				}
				newNodes := make([]model.Node, 0, 2)
				for _, nodeID := range []model.CanonicalID{edge.From, edge.To} {
					if _, seen := seenNodes[nodeID.String()]; seen {
						continue
					}
					node, ok := s.kernel.GetNode(nodeID)
					if !ok {
						continue
					}
					newNodes = append(newNodes, node)
				}
				if len(nodes)+len(newNodes) > budget.MaxNodes {
					truncationReasons["maxNodes"] = struct{}{}
					continue
				}
				if _, seen := seenEdges[edge.Key()]; !seen {
					if len(edges) >= budget.MaxEdges {
						truncationReasons["maxEdges"] = struct{}{}
						continue
					}
					edges = append(edges, toAPIEdge(edge))
					seenEdges[edge.Key()] = struct{}{}
				}
				for _, node := range newNodes {
					nodes = append(nodes, toAPINode(node))
					seenNodes[node.ID.String()] = struct{}{}
					nextFrontier = append(nextFrontier, node.ID)
				}
			}
		}
		frontier = nextFrontier
	}

	now := time.Now().UTC()
	budget.NodeCount = len(nodes)
	budget.EdgeCount = len(edges)
	budget.TruncationReasons = sortedReasons(truncationReasons)
	budget.Truncated = len(budget.TruncationReasons) > 0
	explanations := summarizeEvidence(nodes)
	helmContext := analyzeHelmContext(nodes, edges)
	return api.DiagnosticSubgraph{
		SchemaVersion:   api.DiagnosticSchemaVersionV1Alpha1,
		Entry:           entry,
		Nodes:           nodes,
		Edges:           edges,
		CollectedAt:     &now,
		Explanation:     explanations,
		Warnings:        diagnosticWarnings(budget, helmContext),
		Partial:         budget.Truncated,
		DegradedSources: diagnosticDegradedSources(helmContext),
		Budgets:         budget,
		RankedEvidence:  rankEvidence(nodes, edges),
		Conflicts:       helmContext.Conflicts,
	}, nil
}

func (s *Service) GetDiagnosticSubgraphByPod(namespace, name string, policy api.ExpansionPolicy) (api.DiagnosticSubgraph, error) {
	for _, node := range s.kernel.ListNodes() {
		if node.Kind != model.NodeKindPod || node.Namespace != namespace || node.Name != name {
			continue
		}
		entry := api.EntryRef{
			Kind:        api.NodeKindPod,
			CanonicalID: node.ID.String(),
			Namespace:   namespace,
			Name:        name,
		}
		return s.GetDiagnosticSubgraph(entry, policy)
	}
	return api.DiagnosticSubgraph{}, errors.New("pod entry not found")
}

func (s *Service) FindNode(kind model.NodeKind, namespace, name string) (model.Node, bool, bool) {
	if s == nil || s.kernel == nil || name == "" {
		return model.Node{}, false, false
	}
	var found model.Node
	ok := false
	for _, node := range s.kernel.ListNodes() {
		if node.Kind != kind || node.Name != name {
			continue
		}
		if namespace != "" && node.Namespace != namespace {
			continue
		}
		if ok {
			return model.Node{}, false, true
		}
		found = node
		ok = true
	}
	return found, ok, false
}

func toAPINode(node model.Node) api.DiagnosticNode {
	return api.DiagnosticNode{
		CanonicalID: node.ID.String(),
		Kind:        api.NodeKind(node.Kind),
		SourceKind:  node.SourceKind,
		Name:        node.Name,
		Namespace:   node.Namespace,
		Attributes:  node.Attributes,
	}
}

func toAPIEdge(edge model.Edge) api.DiagnosticEdge {
	return api.DiagnosticEdge{
		From: edge.From.String(),
		To:   edge.To.String(),
		Kind: api.EdgeKind(edge.Kind),
		Provenance: api.EdgeProvenance{
			SourceType: api.EdgeSourceType(edge.Provenance.SourceType),
			State:      api.EdgeState(edge.Provenance.State),
			Resolver:   edge.Provenance.Resolver,
			LastSeenAt: edge.Provenance.LastSeenAt,
			Confidence: edge.Provenance.Confidence,
		},
	}
}

func summarizeEvidence(nodes []api.DiagnosticNode) []string {
	explanations := make([]string, 0)
	for _, node := range nodes {
		if node.Kind != api.NodeKindEvent {
			continue
		}
		reason, _ := node.Attributes["reason"].(string)
		message, _ := node.Attributes["message"].(string)
		if reason == "" && message == "" {
			continue
		}
		if message != "" {
			explanations = append(explanations, "event: "+reason+" - "+message)
		} else {
			explanations = append(explanations, "event: "+reason)
		}
	}
	return explanations
}

func diagnosticBudget(policy api.ExpansionPolicy) api.DiagnosticBudget {
	maxNodes := policy.MaxNodes
	if maxNodes <= 0 {
		maxNodes = DefaultDiagnosticMaxNodes
	}
	if maxNodes < 1 {
		maxNodes = 1
	}
	maxEdges := policy.MaxEdges
	if maxEdges <= 0 {
		maxEdges = DefaultDiagnosticMaxEdges
	}
	return api.DiagnosticBudget{
		MaxDepth:        policy.MaxDepth,
		StorageMaxDepth: policy.StorageMaxDepth,
		MaxNodes:        maxNodes,
		MaxEdges:        maxEdges,
	}
}

func sortedReasons(reasons map[string]struct{}) []string {
	if len(reasons) == 0 {
		return nil
	}
	out := make([]string, 0, len(reasons))
	for reason := range reasons {
		out = append(out, reason)
	}
	sort.Strings(out)
	return out
}

type helmDiagnosticContext struct {
	HasEvidence      bool
	HasLabelEvidence bool
	Conflicts        []api.DiagnosticConflict
}

func analyzeHelmContext(nodes []api.DiagnosticNode, edges []api.DiagnosticEdge) helmDiagnosticContext {
	ctx := helmDiagnosticContext{}
	for _, node := range nodes {
		if node.Kind == api.NodeKindHelmRelease || node.Kind == api.NodeKindHelmChart {
			ctx.HasEvidence = true
		}
	}
	releasesByResource := map[string]map[string]struct{}{}
	edgeKeysByResource := map[string][]string{}
	for _, edge := range edges {
		switch edge.Kind {
		case api.EdgeKindManagedByHelmRelease, api.EdgeKindInstallsChart:
			ctx.HasEvidence = true
			if edge.Provenance.SourceType == api.EdgeSourceTypeLabelEvidence {
				ctx.HasLabelEvidence = true
			}
		}
		if edge.Kind != api.EdgeKindManagedByHelmRelease {
			continue
		}
		if releasesByResource[edge.From] == nil {
			releasesByResource[edge.From] = map[string]struct{}{}
		}
		releasesByResource[edge.From][edge.To] = struct{}{}
		edgeKeysByResource[edge.From] = append(edgeKeysByResource[edge.From], diagnosticEdgeKey(edge))
	}
	for resourceID, releaseIDs := range releasesByResource {
		if len(releaseIDs) < 2 {
			continue
		}
		nodeIDs := []string{resourceID}
		for releaseID := range releaseIDs {
			nodeIDs = append(nodeIDs, releaseID)
		}
		sort.Strings(nodeIDs[1:])
		edgeKeys := append([]string(nil), edgeKeysByResource[resourceID]...)
		sort.Strings(edgeKeys)
		ctx.Conflicts = append(ctx.Conflicts, api.DiagnosticConflict{
			Code:       "helm_ownership_conflict",
			Message:    "resource has multiple Helm release ownership hints; do not select one owner without stronger Helm CLI output or release manifest evidence",
			NodeIDs:    nodeIDs,
			EdgeKeys:   edgeKeys,
			Confidence: "conflicting",
		})
	}
	sort.SliceStable(ctx.Conflicts, func(i, j int) bool {
		if ctx.Conflicts[i].Code != ctx.Conflicts[j].Code {
			return ctx.Conflicts[i].Code < ctx.Conflicts[j].Code
		}
		if len(ctx.Conflicts[i].NodeIDs) == 0 || len(ctx.Conflicts[j].NodeIDs) == 0 {
			return len(ctx.Conflicts[i].NodeIDs) < len(ctx.Conflicts[j].NodeIDs)
		}
		return ctx.Conflicts[i].NodeIDs[0] < ctx.Conflicts[j].NodeIDs[0]
	})
	return ctx
}

func diagnosticWarnings(budget api.DiagnosticBudget, helmContext helmDiagnosticContext) []api.DiagnosticWarning {
	warnings := make([]api.DiagnosticWarning, 0, 3)
	if budget.Truncated {
		warnings = append(warnings, api.DiagnosticWarning{
			Code:       "diagnostic_budget_exceeded",
			Severity:   "warning",
			Message:    fmt.Sprintf("diagnostic graph was truncated by budget: %s", strings.Join(budget.TruncationReasons, ",")),
			Source:     "diagnostic",
			NextAction: "rerun with a narrower namespace/depth or raise --max-nodes/--max-edges",
		})
	}
	if helmContext.HasEvidence {
		warnings = append(warnings, api.DiagnosticWarning{
			Code:       "helm_cli_output_not_observed",
			Severity:   "info",
			Message:    "diagnostic uses current Kubernetes objects and Helm metadata only; Helm template, values, repository, client, hook, and --atomic rollback errors are not observable without user-provided Helm output",
			Source:     "helm",
			NextAction: "paste helm upgrade stderr or run helm status/history when the failure happened before or outside Kubernetes rollout",
		})
	}
	if helmContext.HasLabelEvidence {
		warnings = append(warnings, api.DiagnosticWarning{
			Code:       "helm_manifest_evidence_not_collected",
			Severity:   "info",
			Message:    "Helm ownership is inferred from labels/annotations; exact release manifest membership is not collected in the default diagnostic path",
			Source:     "helm",
			NextAction: "treat Helm ownership as probable evidence unless the user provides Helm status/history or an opt-in release manifest source is available",
		})
	}
	if len(warnings) == 0 {
		return nil
	}
	return warnings
}

func diagnosticDegradedSources(helmContext helmDiagnosticContext) []api.DegradedSource {
	sources := make([]api.DegradedSource, 0, 2)
	if helmContext.HasEvidence {
		sources = append(sources, api.DegradedSource{
			Source:     "helm_cli_output",
			Status:     "not_collected",
			Reason:     "outside_kubernetes_api",
			Message:    "Helm stderr, status, and history are not available from current cluster objects",
			Retryable:  false,
			NextAction: "ask the user to paste helm upgrade output for template, values, repository, client, hook, or rollback-stage failures",
		})
	}
	if helmContext.HasLabelEvidence {
		sources = append(sources, api.DegradedSource{
			Source:     "helm_release_manifest",
			Status:     "not_collected",
			Reason:     "label_evidence_only",
			Message:    "Exact Helm release manifest membership is not collected in the default diagnostic path",
			Retryable:  false,
			NextAction: "use the returned label evidence as probable ownership, not proof of exact manifest membership",
		})
	}
	return sources
}

func rankEvidence(nodes []api.DiagnosticNode, edges []api.DiagnosticEdge) []api.RankedEvidence {
	items := make([]api.RankedEvidence, 0)
	for _, node := range nodes {
		if node.Kind != api.NodeKindEvent {
			continue
		}
		reason, _ := node.Attributes["reason"].(string)
		message, _ := node.Attributes["message"].(string)
		if reason == "" && message == "" {
			continue
		}
		severity, score := classifyEventEvidence(reason, message)
		items = append(items, api.RankedEvidence{
			Source:     "event",
			NodeID:     node.CanonicalID,
			Kind:       string(node.Kind),
			Severity:   severity,
			Reason:     reason,
			Message:    message,
			Confidence: "observed",
			Score:      score,
			Timestamp:  evidenceTimestamp(node.Attributes),
		})
	}
	items = append(items, rankPodRuntimeEvidence(nodes)...)
	items = append(items, rankHelmEvidence(nodes, edges)...)
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Score != items[j].Score {
			return items[i].Score > items[j].Score
		}
		if items[i].Reason != items[j].Reason {
			return items[i].Reason < items[j].Reason
		}
		return items[i].NodeID < items[j].NodeID
	})
	if len(items) > MaxRankedEvidence {
		items = items[:MaxRankedEvidence]
	}
	for i := range items {
		items[i].Rank = i + 1
	}
	return items
}

func rankPodRuntimeEvidence(nodes []api.DiagnosticNode) []api.RankedEvidence {
	items := make([]api.RankedEvidence, 0)
	for _, node := range nodes {
		if node.Kind != api.NodeKindPod {
			continue
		}
		for _, status := range containerStatusMaps(node.Attributes["containerStates"]) {
			name, _ := status["name"].(string)
			waitingReason, _ := status["waitingReason"].(string)
			waitingMessage, _ := status["waitingMessage"].(string)
			terminatedReason, _ := status["terminatedReason"].(string)
			terminatedMessage, _ := status["terminatedMessage"].(string)
			restartCount := numberValue(status["restartCount"])
			switch {
			case waitingReason != "":
				items = append(items, api.RankedEvidence{
					Source:     "pod_status",
					NodeID:     node.CanonicalID,
					Kind:       string(node.Kind),
					Severity:   "warning",
					Reason:     waitingReason,
					Message:    strings.TrimSpace(fmt.Sprintf("container %s is waiting: %s", name, firstNonEmpty(waitingMessage, waitingReason))),
					Confidence: "observed",
					Score:      88,
				})
			case terminatedReason != "":
				items = append(items, api.RankedEvidence{
					Source:     "pod_status",
					NodeID:     node.CanonicalID,
					Kind:       string(node.Kind),
					Severity:   "warning",
					Reason:     terminatedReason,
					Message:    strings.TrimSpace(fmt.Sprintf("container %s terminated: %s", name, firstNonEmpty(terminatedMessage, terminatedReason))),
					Confidence: "observed",
					Score:      82,
				})
			case restartCount > 0:
				items = append(items, api.RankedEvidence{
					Source:     "pod_status",
					NodeID:     node.CanonicalID,
					Kind:       string(node.Kind),
					Severity:   "info",
					Reason:     "ContainerRestarted",
					Message:    fmt.Sprintf("container %s has restarted %d times", name, int(restartCount)),
					Confidence: "observed",
					Score:      65 + restartCount,
				})
			}
		}
	}
	return items
}

func rankHelmEvidence(nodes []api.DiagnosticNode, edges []api.DiagnosticEdge) []api.RankedEvidence {
	nodeByID := make(map[string]api.DiagnosticNode, len(nodes))
	for _, node := range nodes {
		nodeByID[node.CanonicalID] = node
	}
	items := make([]api.RankedEvidence, 0)
	for _, edge := range edges {
		switch edge.Kind {
		case api.EdgeKindManagedByHelmRelease:
			resource := nodeByID[edge.From]
			release := nodeByID[edge.To]
			items = append(items, api.RankedEvidence{
				Source:     "helm",
				EdgeKey:    diagnosticEdgeKey(edge),
				Kind:       string(edge.Kind),
				Severity:   "info",
				Reason:     "HelmOwnershipEvidence",
				Message:    fmt.Sprintf("%s %s is probably managed by Helm release %s", nodeLabel(resource), nodeName(resource), nodeName(release)),
				Confidence: confidenceLabel(edge.Provenance.Confidence),
				Score:      helmEvidenceScore(edge.Provenance.Confidence, 70),
			})
		case api.EdgeKindInstallsChart:
			release := nodeByID[edge.From]
			chart := nodeByID[edge.To]
			items = append(items, api.RankedEvidence{
				Source:     "helm",
				EdgeKey:    diagnosticEdgeKey(edge),
				Kind:       string(edge.Kind),
				Severity:   "info",
				Reason:     "HelmChartEvidence",
				Message:    fmt.Sprintf("Helm release %s installs chart %s", nodeName(release), nodeName(chart)),
				Confidence: confidenceLabel(edge.Provenance.Confidence),
				Score:      helmEvidenceScore(edge.Provenance.Confidence, 60),
			})
		}
	}
	return items
}

func diagnosticEdgeKey(edge api.DiagnosticEdge) string {
	return edge.From + "|" + string(edge.Kind) + "|" + edge.To
}

func nodeLabel(node api.DiagnosticNode) string {
	if node.Kind == "" {
		return "resource"
	}
	return string(node.Kind)
}

func nodeName(node api.DiagnosticNode) string {
	if node.Name == "" {
		return node.CanonicalID
	}
	if node.Namespace == "" {
		return node.Name
	}
	return node.Namespace + "/" + node.Name
}

func confidenceLabel(confidence *float64) string {
	if confidence == nil {
		return "unknown"
	}
	switch {
	case *confidence >= 0.85:
		return "strong"
	case *confidence >= 0.6:
		return "medium"
	default:
		return "weak"
	}
}

func helmEvidenceScore(confidence *float64, base float64) float64 {
	if confidence == nil {
		return base * 0.5
	}
	return base * *confidence
}

func evidenceTimestamp(attributes map[string]any) *time.Time {
	for _, key := range []string{"lastTimestamp", "firstTimestamp"} {
		switch value := attributes[key].(type) {
		case time.Time:
			return &value
		case string:
			parsed, err := time.Parse(time.RFC3339, value)
			if err == nil {
				return &parsed
			}
		}
	}
	return nil
}

func containerStatusMaps(raw any) []map[string]any {
	switch value := raw.(type) {
	case []map[string]any:
		return value
	case []any:
		out := make([]map[string]any, 0, len(value))
		for _, item := range value {
			if mapped, ok := item.(map[string]any); ok {
				out = append(out, mapped)
			}
		}
		return out
	default:
		return nil
	}
}

func numberValue(raw any) float64 {
	switch value := raw.(type) {
	case int:
		return float64(value)
	case int32:
		return float64(value)
	case int64:
		return float64(value)
	case float64:
		return value
	default:
		return 0
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func classifyEventEvidence(reason, message string) (string, float64) {
	text := strings.ToLower(reason + " " + message)
	for _, token := range []string{"fail", "error", "backoff", "forbidden", "denied", "timeout", "unhealthy", "invalid"} {
		if strings.Contains(text, token) {
			return "warning", 90
		}
	}
	if reason != "" || message != "" {
		return "info", 50
	}
	return "unknown", 0
}

func shouldTraverse(kind model.EdgeKind, depth int, policy api.ExpansionPolicy) bool {
	if kind == model.EdgeKindMountsPVC ||
		kind == model.EdgeKindBoundToPV ||
		kind == model.EdgeKindUsesStorageClass ||
		kind == model.EdgeKindProvisionedByCSIDriver ||
		kind == model.EdgeKindImplementedByCSIController ||
		kind == model.EdgeKindImplementedByCSINodeAgent ||
		kind == model.EdgeKindManagedByCSIController ||
		kind == model.EdgeKindServedByCSINodeAgent {
		if !policy.IncludeStorage {
			return false
		}
		return depth < policy.StorageMaxDepth
	}
	return depth < policy.MaxDepth
}

func (s *Service) shouldTraverseFrom(current model.CanonicalID, currentNode model.Node, currentOK bool, rootNode model.Node, edge model.Edge, depth int, policy api.ExpansionPolicy) bool {
	if !shouldTraverse(edge.Kind, depth, policy) {
		return false
	}
	if !currentOK {
		return true
	}
	switch currentNode.Kind {
	case model.NodeKindHelmRelease:
		if edge.From == current && edge.Kind == model.EdgeKindInstallsChart {
			return true
		}
		return rootNode.Kind == model.NodeKindHelmRelease && edge.To == current && edge.Kind == model.EdgeKindManagedByHelmRelease
	case model.NodeKindStorageClass:
		return edge.From == current && edge.Kind == model.EdgeKindProvisionedByCSIDriver
	case model.NodeKindPV:
		if edge.Kind == model.EdgeKindServedByCSINodeAgent && rootNode.Kind == model.NodeKindPod {
			return edge.From == current && s.csiAgentMatchesRootPodNode(edge.To, rootNode)
		}
		return true
	case model.NodeKindCSIDriver:
		if edge.From != current {
			return false
		}
		if edge.Kind == model.EdgeKindImplementedByCSIController {
			return true
		}
		return edge.Kind == model.EdgeKindImplementedByCSINodeAgent && shouldTraverseDriverNodeAgents(rootNode.Kind)
	default:
		return true
	}
}

func (s *Service) csiAgentMatchesRootPodNode(agentID model.CanonicalID, rootNode model.Node) bool {
	rootNodeName, _ := rootNode.Attributes["nodeName"].(string)
	if rootNodeName == "" || s.kernel == nil {
		return false
	}
	agent, ok := s.kernel.GetNode(agentID)
	if !ok {
		return false
	}
	agentNodeName, _ := agent.Attributes["nodeName"].(string)
	return agentNodeName == rootNodeName
}

func shouldTraverseDriverNodeAgents(rootKind model.NodeKind) bool {
	switch rootKind {
	case model.NodeKindPod, model.NodeKindWorkload, model.NodeKindPVC, model.NodeKindPV:
		return false
	default:
		return true
	}
}

func terminalNodeKindSet(policy api.ExpansionPolicy) map[model.NodeKind]struct{} {
	if policy.ExpandTerminalNodes || len(policy.TerminalNodeKinds) == 0 {
		return nil
	}
	out := make(map[model.NodeKind]struct{}, len(policy.TerminalNodeKinds))
	for _, kind := range policy.TerminalNodeKinds {
		out[model.NodeKind(kind)] = struct{}{}
	}
	return out
}

func (s *Service) isTerminalNode(node model.Node, rootID model.CanonicalID, rootKind model.NodeKind, terminalKinds map[model.NodeKind]struct{}, expandTerminalNodes bool) bool {
	if expandTerminalNodes {
		return false
	}
	if s.isTerminalCSINode(node) {
		return true
	}
	if node.ID != rootID && rootKind == model.NodeKindPod && node.Kind == model.NodeKindPod {
		return true
	}
	if len(terminalKinds) == 0 {
		return false
	}
	_, ok := terminalKinds[node.Kind]
	return ok
}

func (s *Service) isTerminalCSINode(node model.Node) bool {
	if node.Kind != model.NodeKindPod || s.kernel == nil {
		return false
	}
	for _, edge := range s.kernel.Neighbors(node.ID) {
		switch edge.Kind {
		case model.EdgeKindImplementedByCSIController,
			model.EdgeKindImplementedByCSINodeAgent,
			model.EdgeKindManagedByCSIController,
			model.EdgeKindServedByCSINodeAgent:
			return true
		}
	}
	return false
}
