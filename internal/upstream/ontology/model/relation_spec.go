package model

import "time"

const (
	OntologyClassEntity               = "Entity"
	OntologyClassKubernetesResource   = "KubernetesResource"
	OntologyClassNamespacedResource   = "NamespacedResource"
	OntologyClassStorageClassConsumer = "StorageClassConsumer"
	OntologyClassRBACBinding          = "RBACBinding"
	OntologyClassPackage              = "Package"
)

type RelationSpec struct {
	Kind              EdgeKind
	Comment           string
	Domain            string
	Range             string
	InverseOf         EdgeKind
	DefaultSourceType EdgeSourceType
	DefaultState      EdgeState
	ResolverHints     []string
}

func RelationSpecs() []RelationSpec {
	out := make([]RelationSpec, len(relationSpecs))
	for index, spec := range relationSpecs {
		out[index] = cloneRelationSpec(spec)
	}
	return out
}

func RelationSpecFor(kind EdgeKind) (RelationSpec, bool) {
	for _, spec := range relationSpecs {
		if spec.Kind == kind {
			return cloneRelationSpec(spec), true
		}
	}
	return RelationSpec{}, false
}

func NewEdge(from, to CanonicalID, kind EdgeKind) Edge {
	return NewEdgeWithResolver(from, to, kind, "")
}

func NewEdgeWithResolver(from, to CanonicalID, kind EdgeKind, resolver string) Edge {
	spec, _ := RelationSpecFor(kind)
	if resolver == "" && len(spec.ResolverHints) > 0 {
		resolver = spec.ResolverHints[0]
	}
	now := time.Now().UTC()
	return Edge{
		From: from,
		To:   to,
		Kind: kind,
		Provenance: EdgeProvenance{
			SourceType: spec.DefaultSourceType,
			State:      spec.DefaultState,
			Resolver:   resolver,
			LastSeenAt: &now,
		},
	}
}

func cloneRelationSpec(spec RelationSpec) RelationSpec {
	spec.ResolverHints = append([]string(nil), spec.ResolverHints...)
	return spec
}

var relationSpecs = []RelationSpec{
	{
		Kind:              EdgeKindBelongsToNamespace,
		Comment:           "Relates a namespaced resource to its namespace.",
		Domain:            OntologyClassNamespacedResource,
		Range:             string(NodeKindNamespace),
		DefaultSourceType: EdgeSourceTypeExplicitRef,
		DefaultState:      EdgeStateAsserted,
	},
	{
		Kind:              EdgeKindManagedBy,
		Comment:           "Relates a Pod to each workload recovered from ownerReferences and owner-chain inference.",
		Domain:            string(NodeKindPod),
		Range:             string(NodeKindWorkload),
		InverseOf:         EdgeKindOwnsPod,
		DefaultSourceType: EdgeSourceTypeInferenceRule,
		DefaultState:      EdgeStateInferred,
		ResolverHints:     []string{"owner-chain/v1"},
	},
	{
		Kind:              EdgeKindControlledBy,
		Comment:           "Relates a workload to an owning parent workload recovered from ownerReferences.",
		Domain:            string(NodeKindWorkload),
		Range:             string(NodeKindWorkload),
		DefaultSourceType: EdgeSourceTypeOwnerReference,
		DefaultState:      EdgeStateInferred,
		ResolverHints:     []string{"owner-chain/v1"},
	},
	{
		Kind:              EdgeKindOwnsPod,
		Comment:           "Relates a workload to a Pod it owns through owner-chain recovery.",
		Domain:            string(NodeKindWorkload),
		Range:             string(NodeKindPod),
		InverseOf:         EdgeKindManagedBy,
		DefaultSourceType: EdgeSourceTypeOwnerReference,
		DefaultState:      EdgeStateInferred,
		ResolverHints:     []string{"owner-chain/v1"},
	},
	{
		Kind:              EdgeKindScheduledOn,
		Comment:           "Relates a Pod to the Node named in pod.spec.nodeName.",
		Domain:            string(NodeKindPod),
		Range:             string(NodeKindNode),
		DefaultSourceType: EdgeSourceTypeExplicitRef,
		DefaultState:      EdgeStateAsserted,
		ResolverHints:     []string{"pod-spec-node/v1"},
	},
	{
		Kind:              EdgeKindSelectsPod,
		Comment:           "Relates a Service to a Pod selected by matching service selectors against Pod labels.",
		Domain:            string(NodeKindService),
		Range:             string(NodeKindPod),
		InverseOf:         EdgeKindSelectedByService,
		DefaultSourceType: EdgeSourceTypeSelectorMatch,
		DefaultState:      EdgeStateAsserted,
		ResolverHints:     []string{"service-selector/v1"},
	},
	{
		Kind:              EdgeKindSelectedByService,
		Comment:           "Inverse of selects_pod for consumers that prefer Pod-centered service traversal.",
		Domain:            string(NodeKindPod),
		Range:             string(NodeKindService),
		InverseOf:         EdgeKindSelectsPod,
		DefaultSourceType: EdgeSourceTypeSelectorMatch,
		DefaultState:      EdgeStateAsserted,
		ResolverHints:     []string{"service-selector/v1"},
	},
	{
		Kind:              EdgeKindUsesConfigMap,
		Comment:           "Relates a Pod to a ConfigMap referenced by env or volume configuration.",
		Domain:            string(NodeKindPod),
		Range:             string(NodeKindConfigMap),
		DefaultSourceType: EdgeSourceTypeExplicitRef,
		DefaultState:      EdgeStateAsserted,
		ResolverHints:     []string{"pod-config/v1"},
	},
	{
		Kind:              EdgeKindUsesSecret,
		Comment:           "Relates a Pod to a Secret referenced by env or volume configuration.",
		Domain:            string(NodeKindPod),
		Range:             string(NodeKindSecret),
		DefaultSourceType: EdgeSourceTypeExplicitRef,
		DefaultState:      EdgeStateAsserted,
		ResolverHints:     []string{"pod-secret/v1"},
	},
	{
		Kind:              EdgeKindUsesServiceAccount,
		Comment:           "Relates a Pod to its ServiceAccount.",
		Domain:            string(NodeKindPod),
		Range:             string(NodeKindServiceAccount),
		DefaultSourceType: EdgeSourceTypeExplicitRef,
		DefaultState:      EdgeStateAsserted,
		ResolverHints:     []string{"pod-serviceaccount/v1"},
	},
	{
		Kind:              EdgeKindBoundByRoleBinding,
		Comment:           "Relates a ServiceAccount subject to the RoleBinding or ClusterRoleBinding that binds it.",
		Domain:            string(NodeKindServiceAccount),
		Range:             OntologyClassRBACBinding,
		DefaultSourceType: EdgeSourceTypeBindingResolution,
		DefaultState:      EdgeStateInferred,
		ResolverHints:     []string{"rbac-binding/v1"},
	},
	{
		Kind:              EdgeKindMountsPVC,
		Comment:           "Relates a Pod to a PersistentVolumeClaim referenced by a volume.",
		Domain:            string(NodeKindPod),
		Range:             string(NodeKindPVC),
		DefaultSourceType: EdgeSourceTypeExplicitRef,
		DefaultState:      EdgeStateAsserted,
		ResolverHints:     []string{"pod-volumes/v1"},
	},
	{
		Kind:              EdgeKindBoundToPV,
		Comment:           "Relates a PersistentVolumeClaim to its bound PersistentVolume.",
		Domain:            string(NodeKindPVC),
		Range:             string(NodeKindPV),
		DefaultSourceType: EdgeSourceTypeExplicitRef,
		DefaultState:      EdgeStateAsserted,
		ResolverHints:     []string{"pvc-pv-binding/v1"},
	},
	{
		Kind:              EdgeKindUsesStorageClass,
		Comment:           "Relates a PersistentVolumeClaim or PersistentVolume to its StorageClass.",
		Domain:            OntologyClassStorageClassConsumer,
		Range:             string(NodeKindStorageClass),
		DefaultSourceType: EdgeSourceTypeExplicitRef,
		DefaultState:      EdgeStateAsserted,
		ResolverHints:     []string{"pvc-storageclass/v1", "pvc-bound-pv-storageclass/v1", "pv-storageclass/v1"},
	},
	{
		Kind:              EdgeKindProvisionedByCSIDriver,
		Comment:           "Relates a StorageClass to an observed or inferred CSI driver provisioner.",
		Domain:            string(NodeKindStorageClass),
		Range:             string(NodeKindCSIDriver),
		DefaultSourceType: EdgeSourceTypeInferenceRule,
		DefaultState:      EdgeStateInferred,
		ResolverHints:     []string{"storageclass-provisioner/v1"},
	},
	{
		Kind:              EdgeKindHasContainer,
		Comment:           "Relates a Pod to a contained container. Reserved by the current model for container-level expansion.",
		Domain:            string(NodeKindPod),
		Range:             string(NodeKindContainer),
		DefaultSourceType: EdgeSourceTypeExplicitRef,
		DefaultState:      EdgeStateAsserted,
	},
	{
		Kind:              EdgeKindUsesImage,
		Comment:           "Relates a Pod to a container image reference parsed from its container specs.",
		Domain:            string(NodeKindPod),
		Range:             string(NodeKindImage),
		DefaultSourceType: EdgeSourceTypeExplicitRef,
		DefaultState:      EdgeStateAsserted,
		ResolverHints:     []string{"container-image/v1"},
	},
	{
		Kind:              EdgeKindHasOCIArtifact,
		Comment:           "Relates an image reference to parsed OCI artifact metadata. Reserved by the current model for richer image metadata.",
		Domain:            string(NodeKindImage),
		Range:             string(NodeKindOCIArtifactMetadata),
		DefaultSourceType: EdgeSourceTypeObserved,
		DefaultState:      EdgeStateObserved,
	},
	{
		Kind:              EdgeKindReportedByEvent,
		Comment:           "Relates an Event node to the resource it reports on through involvedObject.",
		Domain:            string(NodeKindEvent),
		Range:             OntologyClassKubernetesResource,
		DefaultSourceType: EdgeSourceTypeExplicitRef,
		DefaultState:      EdgeStateAsserted,
		ResolverHints:     []string{"event-involved-object/v1"},
	},
	{
		Kind:              EdgeKindAffectedByWebhook,
		Comment:           "Relates a workload to webhook configuration evidence when admission-related events are observed.",
		Domain:            string(NodeKindWorkload),
		Range:             string(NodeKindWebhookConfig),
		DefaultSourceType: EdgeSourceTypeObserved,
		DefaultState:      EdgeStateObserved,
		ResolverHints:     []string{"webhook-evidence/v1"},
	},
	{
		Kind:              EdgeKindManagedByController,
		Comment:           "Relates a workload to a configured controller Pod that manages it.",
		Domain:            string(NodeKindWorkload),
		Range:             string(NodeKindPod),
		DefaultSourceType: EdgeSourceTypeInferenceRule,
		DefaultState:      EdgeStateInferred,
		ResolverHints:     []string{"workload-controller-rule/v1"},
	},
	{
		Kind:              EdgeKindServedByNodeDaemon,
		Comment:           "Relates a workload to a same-node daemon Pod inferred by configured controller rules.",
		Domain:            string(NodeKindWorkload),
		Range:             string(NodeKindPod),
		DefaultSourceType: EdgeSourceTypeInferenceRule,
		DefaultState:      EdgeStateInferred,
		ResolverHints:     []string{"workload-controller-rule/v1"},
	},
	{
		Kind:              EdgeKindImplementedByCSIController,
		Comment:           "Relates a CSI driver to controller Pods inferred by CSI component rules.",
		Domain:            string(NodeKindCSIDriver),
		Range:             string(NodeKindPod),
		DefaultSourceType: EdgeSourceTypeInferenceRule,
		DefaultState:      EdgeStateInferred,
		ResolverHints:     []string{"csi-component-rule/<driver>/v1"},
	},
	{
		Kind:              EdgeKindImplementedByCSINodeAgent,
		Comment:           "Relates a CSI driver to node-agent Pods inferred by CSI component rules.",
		Domain:            string(NodeKindCSIDriver),
		Range:             string(NodeKindPod),
		DefaultSourceType: EdgeSourceTypeInferenceRule,
		DefaultState:      EdgeStateInferred,
		ResolverHints:     []string{"csi-component-rule/<driver>/v1"},
	},
	{
		Kind:              EdgeKindServedByCSINodeAgent,
		Comment:           "Relates a PersistentVolume to a CSI node-agent Pod on a PV affinity node or consuming Pod node.",
		Domain:            string(NodeKindPV),
		Range:             string(NodeKindPod),
		DefaultSourceType: EdgeSourceTypeInferenceRule,
		DefaultState:      EdgeStateInferred,
		ResolverHints:     []string{"csi-component-rule/<driver>/pv-agent/v1"},
	},
	{
		Kind:              EdgeKindManagedByCSIController,
		Comment:           "Relates a PersistentVolume to CSI controller Pods inferred for the PV driver.",
		Domain:            string(NodeKindPV),
		Range:             string(NodeKindPod),
		DefaultSourceType: EdgeSourceTypeInferenceRule,
		DefaultState:      EdgeStateInferred,
		ResolverHints:     []string{"csi-component-rule/<driver>/pv-controller/v1"},
	},
	{
		Kind:              EdgeKindManagedByHelmRelease,
		Comment:           "Relates a Kubernetes resource to a Helm release inferred from standard Helm and Kubernetes app labels.",
		Domain:            OntologyClassKubernetesResource,
		Range:             string(NodeKindHelmRelease),
		DefaultSourceType: EdgeSourceTypeLabelEvidence,
		DefaultState:      EdgeStateInferred,
		ResolverHints:     []string{"helm-labels/v1"},
	},
	{
		Kind:              EdgeKindInstallsChart,
		Comment:           "Relates a Helm release to the chart label observed on resources in that release.",
		Domain:            string(NodeKindHelmRelease),
		Range:             string(NodeKindHelmChart),
		DefaultSourceType: EdgeSourceTypeLabelEvidence,
		DefaultState:      EdgeStateInferred,
		ResolverHints:     []string{"helm-labels/v1"},
	},
	{
		Kind:              EdgeKindRoutesToService,
		Comment:           "Relates an ingress-like route resource to a Service referenced by backend routing rules.",
		Domain:            string(NodeKindIngress),
		Range:             string(NodeKindService),
		DefaultSourceType: EdgeSourceTypeExplicitRef,
		DefaultState:      EdgeStateAsserted,
		ResolverHints:     []string{"ingress-backend/v1"},
	},
	{
		Kind:              EdgeKindTargetsPod,
		Comment:           "Relates an EndpointSlice to a Pod targetRef observed in its endpoint list.",
		Domain:            string(NodeKindEndpointSlice),
		Range:             string(NodeKindPod),
		DefaultSourceType: EdgeSourceTypeExplicitRef,
		DefaultState:      EdgeStateAsserted,
		ResolverHints:     []string{"endpointslice-targetref/v1"},
	},
	{
		Kind:              EdgeKindAppliesToPod,
		Comment:           "Relates a NetworkPolicy to Pods selected by its podSelector.",
		Domain:            string(NodeKindNetworkPolicy),
		Range:             string(NodeKindPod),
		DefaultSourceType: EdgeSourceTypeSelectorMatch,
		DefaultState:      EdgeStateAsserted,
		ResolverHints:     []string{"networkpolicy-selector/v1"},
	},
	{
		Kind:              EdgeKindScalesWorkload,
		Comment:           "Relates a HorizontalPodAutoscaler to the workload named by scaleTargetRef.",
		Domain:            string(NodeKindHPA),
		Range:             string(NodeKindWorkload),
		DefaultSourceType: EdgeSourceTypeExplicitRef,
		DefaultState:      EdgeStateAsserted,
		ResolverHints:     []string{"hpa-scale-target/v1"},
	},
	{
		Kind:              EdgeKindProtectsPod,
		Comment:           "Relates a PodDisruptionBudget to Pods selected by its selector.",
		Domain:            string(NodeKindPodDisruptionBudget),
		Range:             string(NodeKindPod),
		DefaultSourceType: EdgeSourceTypeSelectorMatch,
		DefaultState:      EdgeStateAsserted,
		ResolverHints:     []string{"pdb-selector/v1"},
	},
	{
		Kind:              EdgeKindAttachesPV,
		Comment:           "Relates a VolumeAttachment to the PersistentVolume named by its source.",
		Domain:            string(NodeKindVolumeAttachment),
		Range:             string(NodeKindPV),
		DefaultSourceType: EdgeSourceTypeExplicitRef,
		DefaultState:      EdgeStateAsserted,
		ResolverHints:     []string{"volumeattachment-source/v1"},
	},
	{
		Kind:              EdgeKindRelatedTo,
		Comment:           "Generic relation for topology slices and future relations that do not yet have a specific edge kind.",
		Domain:            OntologyClassEntity,
		Range:             OntologyClassEntity,
		DefaultSourceType: EdgeSourceTypeObserved,
		DefaultState:      EdgeStateObserved,
	},
}
