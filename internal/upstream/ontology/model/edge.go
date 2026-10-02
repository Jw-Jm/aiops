package model

import "time"

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

type EdgeProvenance struct {
	SourceType EdgeSourceType
	State      EdgeState
	Resolver   string
	LastSeenAt *time.Time
	Confidence *float64
}

type Edge struct {
	From       CanonicalID
	To         CanonicalID
	Kind       EdgeKind
	Provenance EdgeProvenance
}

func (e Edge) Key() string {
	return e.From.String() + "|" + string(e.Kind) + "|" + e.To.String()
}
