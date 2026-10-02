package api

import "time"

const DiagnosticSchemaVersionV1Alpha1 = "diagnostic.kubernetes-ontology.io/v1alpha1"

type NodeKind string

const (
	NodeKindCluster             NodeKind = "Cluster"
	NodeKindNamespace           NodeKind = "Namespace"
	NodeKindWorkload            NodeKind = "Workload"
	NodeKindPod                 NodeKind = "Pod"
	NodeKindContainer           NodeKind = "Container"
	NodeKindNode                NodeKind = "Node"
	NodeKindService             NodeKind = "Service"
	NodeKindConfigMap           NodeKind = "ConfigMap"
	NodeKindSecret              NodeKind = "Secret"
	NodeKindServiceAccount      NodeKind = "ServiceAccount"
	NodeKindRoleBinding         NodeKind = "RoleBinding"
	NodeKindClusterRoleBinding  NodeKind = "ClusterRoleBinding"
	NodeKindPVC                 NodeKind = "PVC"
	NodeKindPV                  NodeKind = "PV"
	NodeKindStorageClass        NodeKind = "StorageClass"
	NodeKindCSIDriver           NodeKind = "CSIDriver"
	NodeKindHelmRelease         NodeKind = "HelmRelease"
	NodeKindHelmChart           NodeKind = "HelmChart"
	NodeKindEvent               NodeKind = "Event"
	NodeKindImage               NodeKind = "Image"
	NodeKindOCIArtifactMetadata NodeKind = "OCIArtifactMetadata"
	NodeKindWebhookConfig       NodeKind = "WebhookConfig"
	NodeKindIngress             NodeKind = "Ingress"
	NodeKindEndpointSlice       NodeKind = "EndpointSlice"
	NodeKindNetworkPolicy       NodeKind = "NetworkPolicy"
	NodeKindHPA                 NodeKind = "HPA"
	NodeKindPodDisruptionBudget NodeKind = "PodDisruptionBudget"
	NodeKindVolumeAttachment    NodeKind = "VolumeAttachment"
)

type EdgeKind string

const (
	EdgeKindBelongsToNamespace         EdgeKind = "belongs_to_namespace"
	EdgeKindManagedBy                  EdgeKind = "managed_by"
	EdgeKindControlledBy               EdgeKind = "controlled_by"
	EdgeKindOwnsPod                    EdgeKind = "owns_pod"
	EdgeKindScheduledOn                EdgeKind = "scheduled_on"
	EdgeKindSelectsPod                 EdgeKind = "selects_pod"
	EdgeKindSelectedByService          EdgeKind = "selected_by_service"
	EdgeKindUsesConfigMap              EdgeKind = "uses_config_map"
	EdgeKindUsesSecret                 EdgeKind = "uses_secret"
	EdgeKindUsesServiceAccount         EdgeKind = "uses_service_account"
	EdgeKindBoundByRoleBinding         EdgeKind = "bound_by_role_binding"
	EdgeKindMountsPVC                  EdgeKind = "mounts_pvc"
	EdgeKindBoundToPV                  EdgeKind = "bound_to_pv"
	EdgeKindUsesStorageClass           EdgeKind = "uses_storage_class"
	EdgeKindProvisionedByCSIDriver     EdgeKind = "provisioned_by_csi_driver"
	EdgeKindHasContainer               EdgeKind = "has_container"
	EdgeKindUsesImage                  EdgeKind = "uses_image"
	EdgeKindHasOCIArtifact             EdgeKind = "has_oci_artifact"
	EdgeKindReportedByEvent            EdgeKind = "reported_by_event"
	EdgeKindAffectedByWebhook          EdgeKind = "affected_by_webhook"
	EdgeKindManagedByController        EdgeKind = "managed_by_controller"
	EdgeKindServedByNodeDaemon         EdgeKind = "served_by_node_daemon"
	EdgeKindImplementedByCSIController EdgeKind = "implemented_by_csi_controller"
	EdgeKindImplementedByCSINodeAgent  EdgeKind = "implemented_by_csi_node_agent"
	EdgeKindServedByCSINodeAgent       EdgeKind = "served_by_csi_node_agent"
	EdgeKindManagedByCSIController     EdgeKind = "managed_by_csi_controller"
	EdgeKindManagedByHelmRelease       EdgeKind = "managed_by_helm_release"
	EdgeKindInstallsChart              EdgeKind = "installs_chart"
	EdgeKindRoutesToService            EdgeKind = "routes_to_service"
	EdgeKindTargetsPod                 EdgeKind = "targets_pod"
	EdgeKindAppliesToPod               EdgeKind = "applies_to_pod"
	EdgeKindScalesWorkload             EdgeKind = "scales_workload"
	EdgeKindProtectsPod                EdgeKind = "protects_pod"
	EdgeKindAttachesPV                 EdgeKind = "attaches_pv"
	EdgeKindRelatedTo                  EdgeKind = "related_to"
)

type EdgeSourceType string

const (
	EdgeSourceTypeExplicitRef       EdgeSourceType = "explicit_ref"
	EdgeSourceTypeSelectorMatch     EdgeSourceType = "selector_match"
	EdgeSourceTypeOwnerReference    EdgeSourceType = "owner_reference"
	EdgeSourceTypeBindingResolution EdgeSourceType = "binding_resolution"
	EdgeSourceTypeInferenceRule     EdgeSourceType = "inference_rule"
	EdgeSourceTypeObserved          EdgeSourceType = "observed"
	EdgeSourceTypeLabelEvidence     EdgeSourceType = "label_evidence"
)

type EdgeState string

const (
	EdgeStateAsserted EdgeState = "asserted"
	EdgeStateInferred EdgeState = "inferred"
	EdgeStateObserved EdgeState = "observed"
)

type EntryRef struct {
	Kind        NodeKind `json:"kind"`
	CanonicalID string   `json:"canonicalId,omitempty"`
	Namespace   string   `json:"namespace,omitempty"`
	Name        string   `json:"name,omitempty"`
}

type ExpansionPolicy struct {
	MaxDepth               int        `json:"maxDepth"`
	StorageMaxDepth        int        `json:"storageMaxDepth"`
	MaxNodes               int        `json:"maxNodes,omitempty"`
	MaxEdges               int        `json:"maxEdges,omitempty"`
	TerminalNodeKinds      []NodeKind `json:"terminalNodeKinds,omitempty"`
	ExpandTerminalNodes    bool       `json:"expandTerminalNodes,omitempty"`
	IncludeSiblingPods     bool       `json:"includeSiblingPods"`
	IncludeRBAC            bool       `json:"includeRbac"`
	IncludeEvents          bool       `json:"includeEvents"`
	IncludeWebhookEvidence bool       `json:"includeWebhookEvidence"`
	IncludeStorage         bool       `json:"includeStorage"`
	IncludeOCI             bool       `json:"includeOci"`
}

type DiagnosticNode struct {
	CanonicalID string         `json:"canonicalId"`
	Kind        NodeKind       `json:"kind"`
	SourceKind  string         `json:"sourceKind,omitempty"`
	Name        string         `json:"name,omitempty"`
	Namespace   string         `json:"namespace,omitempty"`
	Attributes  map[string]any `json:"attributes,omitempty"`
}

type EdgeProvenance struct {
	SourceType EdgeSourceType `json:"sourceType"`
	State      EdgeState      `json:"state"`
	Resolver   string         `json:"resolver,omitempty"`
	LastSeenAt *time.Time     `json:"lastSeenAt,omitempty"`
	Confidence *float64       `json:"confidence,omitempty"`
}

type DiagnosticEdge struct {
	From       string         `json:"from"`
	To         string         `json:"to"`
	Kind       EdgeKind       `json:"kind"`
	Provenance EdgeProvenance `json:"provenance"`
}

type DiagnosticWarning struct {
	Code       string `json:"code"`
	Severity   string `json:"severity,omitempty"`
	Message    string `json:"message"`
	Source     string `json:"source,omitempty"`
	NextAction string `json:"nextAction,omitempty"`
}

type DegradedSource struct {
	Source     string `json:"source"`
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`
	Message    string `json:"message,omitempty"`
	Retryable  bool   `json:"retryable,omitempty"`
	NextAction string `json:"nextAction,omitempty"`
}

type DiagnosticBudget struct {
	MaxDepth          int      `json:"maxDepth"`
	StorageMaxDepth   int      `json:"storageMaxDepth"`
	MaxNodes          int      `json:"maxNodes"`
	MaxEdges          int      `json:"maxEdges"`
	NodeCount         int      `json:"nodeCount"`
	EdgeCount         int      `json:"edgeCount"`
	Truncated         bool     `json:"truncated"`
	TruncationReasons []string `json:"truncationReasons,omitempty"`
}

type RankedEvidence struct {
	Rank       int        `json:"rank"`
	Source     string     `json:"source"`
	NodeID     string     `json:"nodeId,omitempty"`
	EdgeKey    string     `json:"edgeKey,omitempty"`
	Kind       string     `json:"kind,omitempty"`
	Severity   string     `json:"severity,omitempty"`
	Reason     string     `json:"reason,omitempty"`
	Message    string     `json:"message,omitempty"`
	Confidence string     `json:"confidence,omitempty"`
	Score      float64    `json:"score,omitempty"`
	Timestamp  *time.Time `json:"timestamp,omitempty"`
}

type DiagnosticLane struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Summary string `json:"summary,omitempty"`
}

type DiagnosticConflict struct {
	Code       string   `json:"code"`
	Message    string   `json:"message"`
	NodeIDs    []string `json:"nodeIds,omitempty"`
	EdgeKeys   []string `json:"edgeKeys,omitempty"`
	Confidence string   `json:"confidence,omitempty"`
}

type DiagnosticSubgraph struct {
	SchemaVersion   string               `json:"schemaVersion,omitempty"`
	Recipe          string               `json:"recipe,omitempty"`
	Lanes           []DiagnosticLane     `json:"lanes,omitempty"`
	Entry           EntryRef             `json:"entry"`
	Nodes           []DiagnosticNode     `json:"nodes"`
	Edges           []DiagnosticEdge     `json:"edges"`
	CollectedAt     *time.Time           `json:"collectedAt,omitempty"`
	Explanation     []string             `json:"explanation,omitempty"`
	Warnings        []DiagnosticWarning  `json:"warnings,omitempty"`
	Partial         bool                 `json:"partial"`
	DegradedSources []DegradedSource     `json:"degradedSources,omitempty"`
	Budgets         DiagnosticBudget     `json:"budgets"`
	RankedEvidence  []RankedEvidence     `json:"rankedEvidence,omitempty"`
	Conflicts       []DiagnosticConflict `json:"conflicts,omitempty"`
}

type GraphSubgraph struct {
	Entry       DiagnosticNode   `json:"entry"`
	Nodes       []DiagnosticNode `json:"nodes"`
	Edges       []DiagnosticEdge `json:"edges"`
	CollectedAt *time.Time       `json:"collectedAt,omitempty"`
	Explanation []string         `json:"explanation,omitempty"`
}
