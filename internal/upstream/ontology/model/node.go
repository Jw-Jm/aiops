package model

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

type Node struct {
	ID         CanonicalID
	Kind       NodeKind
	SourceKind string
	Name       string
	Namespace  string
	Attributes map[string]any
}
