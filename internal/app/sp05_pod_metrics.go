package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"ops-platform/internal/evidence"
	"ops-platform/internal/finding"
	"ops-platform/internal/graph"
	inspection "ops-platform/internal/inspection/kubernetes"
	kube "ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/persistence"
	"ops-platform/internal/resource"
)

const podMetricTemplate = "sp05-official-pod-metrics/v1"

// Use the existing official observation round and shared Kubernetes request
// budget. One admitted Pod is selected; neither a caller path nor a wider
// namespace is accepted. Node capacity is contextual to this Pod's assignment.
func inspectSP05PodMetricObservation(ctx context.Context, archive *evidence.ArchiveService, cluster SP04Cluster, client *kube.Client, binding evidence.Binding, round int) (bool, error) {
	if binding.Tenant != cluster.Tenant || binding.SourceID != cluster.SourceID || binding.Revision != cluster.SourceRevision || binding.SourceType != "kubernetes" || binding.ScopeMapping.NativeTenant != "" || !slices.Contains(binding.ScopeMapping.Scopes["cluster"], cluster.ClusterUID) || len(binding.ScopeMapping.Scopes["namespace"]) == 0 {
		return false, evidence.ErrScopeUnverified
	}
	for dimension := range binding.ScopeMapping.Scopes {
		if dimension != "cluster" && dimension != "namespace" {
			return false, evidence.ErrScopeUnverified
		}
	}
	repository := evidence.Repository{Pool: archive.Pool}
	if err := repository.CheckBinding(ctx, binding); err != nil {
		return false, err
	}
	var expected unstructured.Unstructured
	err := persistence.WithTenantTx(ctx, archive.Pool, uuid.MustParse(cluster.Tenant), func(tx pgx.Tx) error {
		var name, namespace, uid string
		err := tx.QueryRow(ctx, `WITH subjects AS (SELECT DISTINCT e.name,e.namespace,a.alias_value FROM platform.resource_entities e JOIN platform.resource_aliases a USING(tenant_id,canonical_id) WHERE e.tenant_id=$1 AND e.cluster_id=(SELECT cluster_id FROM platform.source_registrations WHERE tenant_id=$1 AND source_id=$2) AND e.kind='Pod' AND e.deleted_at IS NULL AND e.namespace=ANY($3) AND a.source_id=$2 AND a.alias_kind='uid' AND a.scope=$4), numbered AS (SELECT name,namespace,alias_value,row_number() OVER(ORDER BY namespace,name,alias_value)-1 AS position,count(*) OVER() AS total FROM subjects) SELECT name,namespace,alias_value FROM numbered WHERE position=mod($5::bigint,total) LIMIT 1`, cluster.Tenant, cluster.SourceID, binding.ScopeMapping.Scopes["namespace"], cluster.ClusterUID, round).Scan(&name, &namespace, &uid)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		expected = unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Pod"}}
		expected.SetName(name)
		expected.SetNamespace(namespace)
		expected.SetUID(types.UID(uid))
		return nil
	})
	if err != nil || expected.GetUID() == "" {
		return false, err
	}
	path := "/api/v1/namespaces/" + url.PathEscape(expected.GetNamespace()) + "/pods/" + url.PathEscape(expected.GetName())
	_, beforePod, err := fixedOfficialGET(ctx, client, path, 512<<10)
	if err != nil || !admittedPodMetricIdentity(expected, beforePod, cluster.ClusterUID, binding) {
		return false, inspection.ErrObservation
	}
	nodeName, _, _ := unstructured.NestedString(beforePod.Object, "spec", "nodeName")
	if nodeName == "" {
		return false, inspection.ErrObservation
	}
	nodePath := "/api/v1/nodes/" + url.PathEscape(nodeName)
	_, beforeNode, err := fixedOfficialGET(ctx, client, nodePath, 128<<10)
	if err != nil {
		return false, err
	}
	// A Pod assignment cannot bypass the registered Source's Node label scope.
	if beforeNode.GetKind() != "Node" || beforeNode.GetAPIVersion() != "v1" || beforeNode.GetName() != nodeName || beforeNode.GetUID() == "" || beforeNode.GetNamespace() != "" || beforeNode.GetDeletionTimestamp() != nil {
		return false, inspection.ErrObservation
	}
	for key, value := range binding.ScopeMapping.RequiredLabels {
		if beforeNode.GetLabels()[key] != value {
			return false, evidence.ErrScopeUnverified
		}
	}
	metricPath := "/apis/metrics.k8s.io/v1beta1/namespaces/" + url.PathEscape(expected.GetNamespace()) + "/pods/" + url.PathEscape(expected.GetName())
	_, metric, err := fixedOfficialGET(ctx, client, metricPath, 64<<10)
	if err != nil {
		return false, err
	}
	_, afterPod, err := fixedOfficialGET(ctx, client, path, 512<<10)
	if err != nil || !admittedPodMetricIdentity(expected, afterPod, cluster.ClusterUID, binding) {
		return false, inspection.ErrObservation
	}
	afterNode, err := readAdmittedNode(ctx, client, beforeNode, binding)
	if err != nil || afterNode.GetUID() != beforeNode.GetUID() {
		return false, inspection.ErrObservation
	}
	beforeAssignment, _, _ := unstructured.NestedString(beforePod.Object, "spec", "nodeName")
	afterAssignment, _, _ := unstructured.NestedString(afterPod.Object, "spec", "nodeName")
	beforeContainers, _, _ := unstructured.NestedSlice(beforePod.Object, "status", "containerStatuses")
	afterContainers, _, _ := unstructured.NestedSlice(afterPod.Object, "status", "containerStatuses")
	beforeCapacity, _, _ := unstructured.NestedStringMap(beforeNode.Object, "status", "capacity")
	afterCapacity, _, _ := unstructured.NestedStringMap(afterNode.Object, "status", "capacity")
	beforeAllocatable, _, _ := unstructured.NestedStringMap(beforeNode.Object, "status", "allocatable")
	afterAllocatable, _, _ := unstructured.NestedStringMap(afterNode.Object, "status", "allocatable")
	if beforeAssignment != afterAssignment || !reflect.DeepEqual(beforeContainers, afterContainers) || !reflect.DeepEqual(beforeCapacity, afterCapacity) || !reflect.DeepEqual(beforeAllocatable, afterAllocatable) {
		return false, inspection.ErrObservation
	}
	now := time.Now().UTC()
	data, err := inspection.PodMetricObservation(afterPod, afterNode, metric, now)
	if err != nil {
		return false, err
	}
	if err := repository.CheckBinding(ctx, binding); err != nil {
		return false, err
	}
	var window struct {
		From  time.Time `json:"observedFrom"`
		Stamp time.Time `json:"timestamp"`
	}
	if json.Unmarshal(data, &window) != nil {
		return false, inspection.ErrObservation
	}
	canonical := resource.CanonicalID{Domain: "k8s", Tenant: cluster.Tenant, Scope: cluster.ClusterUID, APIGroup: "core", Kind: "Pod", StableID: string(expected.GetUID())}.String()
	digest := evidence.Digest(data)
	identity := finding.Hash([]any{binding.SourceID, binding.Revision, canonical, podMetricTemplate, digest})
	item := evidence.Evidence{SchemaVersion: "evidence/v2", EvidenceID: uuid.NewSHA1(uuid.NameSpaceOID, []byte(identity)).String(), TenantID: binding.Tenant, ResourceCanonicalID: canonical, Type: "metric", DataClass: "D0", SourceRegistrationID: binding.SourceID, SourceRevision: binding.Revision, SourceScopeDigest: evidence.BindingScopeDigest(binding), SourceSystem: "kubernetes", BackendLogicalID: binding.BackendLogicalID, QueryTemplateVersion: podMetricTemplate, QueryHash: identity, EffectiveScope: graph.Scope{Tenant: cluster.Tenant, Cluster: cluster.ClusterUID, Namespaces: []string{expected.GetNamespace()}, AuthorizationRevision: "collector/" + binding.SourceID + "/" + strconv.FormatInt(binding.Revision, 10)}, EvaluatedAt: window.Stamp, ObservedFrom: window.From, ObservedTo: window.Stamp, SourceRetentionUntil: window.Stamp.Add(5 * time.Minute), TimeReliable: true, ReplayState: "archive_pending", ContentDigest: digest, IndependenceGroup: binding.SourceID + "/" + string(expected.GetUID()) + "/" + podMetricTemplate, DerivationEvidenceRefs: []string{}, Data: data}
	if err := archive.Prepare(ctx, item, expected.GetNamespace(), now.Add(365*24*time.Hour)); err != nil {
		return false, err
	}
	return true, repository.CheckBinding(ctx, binding)
}

func admittedPodMetricIdentity(expected, native unstructured.Unstructured, cluster string, binding evidence.Binding) bool {
	if native.GetAPIVersion() != "v1" || native.GetKind() != "Pod" || native.GetUID() == "" || native.GetUID() != expected.GetUID() || native.GetName() != expected.GetName() || native.GetNamespace() != expected.GetNamespace() || native.GetDeletionTimestamp() != nil || !slices.Contains(binding.ScopeMapping.Scopes["cluster"], cluster) || !slices.Contains(binding.ScopeMapping.Scopes["namespace"], native.GetNamespace()) {
		return false
	}
	for key, value := range binding.ScopeMapping.RequiredLabels {
		if native.GetLabels()[key] != value {
			return false
		}
	}
	return true
}

// Existing Finding evidence references expose the retained metric context to
// API/MCP and RCA. Unavailable metrics do not remove native condition evidence
// or turn source failure into a recovery signal.
func attachPodMetricContext(ctx context.Context, archive *evidence.ArchiveService, binding evidence.Binding, c *finding.FindingCandidate) error {
	id, err := resource.ParseCanonicalID(c.ResourceCanonicalID)
	if err != nil || id.Domain != "k8s" || id.Kind != "Pod" || c.Namespace == "" {
		return nil
	}
	if id.Tenant != binding.Tenant || !slices.Contains(binding.ScopeMapping.Scopes["cluster"], id.Scope) || !slices.Contains(binding.ScopeMapping.Scopes["namespace"], c.Namespace) {
		return evidence.ErrScopeUnverified
	}
	return persistence.WithTenantTx(ctx, archive.Pool, uuid.MustParse(binding.Tenant), func(tx pgx.Tx) error {
		var metric uuid.UUID
		err := tx.QueryRow(ctx, `SELECT evidence_id FROM platform.evidence_metadata WHERE tenant_id=$1 AND source_id=$2 AND canonical_id=$3 AND namespace=$4 AND NOT deleting AND replay_state='archived_verified' AND metadata->>'queryTemplateVersion'=$5 AND metadata->>'sourceScopeDigest'=$6 AND (metadata->>'sourceRevision')::bigint=$7 AND (metadata->>'observedTo')::timestamptz<=$8 AND (metadata->>'observedTo')::timestamptz>=$8::timestamptz-interval '5 minutes' AND (metadata->>'sourceRetentionUntil')::timestamptz>clock_timestamp() ORDER BY (metadata->>'observedTo')::timestamptz DESC,evidence_id DESC LIMIT 1`, binding.Tenant, binding.SourceID, c.ResourceCanonicalID, c.Namespace, podMetricTemplate, evidence.BindingScopeDigest(binding), binding.Revision, c.ObservedAt).Scan(&metric)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		current, err := evidence.GetTx(ctx, tx, uuid.MustParse(binding.Tenant), metric, graph.Scope{Tenant: id.Tenant, Cluster: id.Scope, Namespaces: []string{c.Namespace}})
		if err != nil || current.Type != "metric" || current.QueryTemplateVersion != podMetricTemplate {
			return evidence.ErrScopeUnverified
		}
		if !slices.Contains(c.EvidenceRefs, metric.String()) {
			c.EvidenceRefs = append(c.EvidenceRefs, metric.String())
			c.NativeIdentity += "/metric/" + metric.String()
		}
		return nil
	})
}
