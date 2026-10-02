package app

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/resource"
	"ops-platform/internal/resourcestore"
	"strconv"
	"time"
)

func collectorSink(ctx context.Context, cluster SP04Cluster, g *graph.Graph, store resourcestore.Repository, queue *projectionArchiveQueue) func(kubernetes.Snapshot) error {
	previous := map[string]string{}
	kind, group := "", ""
	return func(s kubernetes.Snapshot) error {
		if s.ObservationOnly {
			return g.UpdateSourceState(g.OwnerEpoch(), s.GVR, s.State)
		}
		if len(s.Objects) > 0 {
			kind = s.Objects[0].GetKind()
			group = s.Objects[0].GroupVersionKind().Group
			if group == "" {
				group = "core"
			}
		}
		if s.State.LastListCompletedAt.IsZero() {
			return replaceCurrent(ctx, g, s)
		}
		var err error
		s, err = store.FilterSnapshot(ctx, cluster.Tenant, cluster.SourceID, cluster.ClusterUID, s)
		if err != nil {
			s.State.LastError = "source_scope_unverified"
			_ = g.UpdateSourceState(g.OwnerEpoch(), s.GVR, s.State)
			return err
		}
		next, err := store.SyncSnapshot(ctx, cluster.Tenant, cluster.SourceID, cluster.ClusterUID, kind, group, s, previous)
		if err != nil {
			s.State.LastError = "identity_or_metadata_unavailable"
			_ = g.UpdateSourceState(g.OwnerEpoch(), s.GVR, s.State)
			return err
		}
		// Graph publication does not depend on external archive availability.
		for i := range s.Objects {
			a := s.Objects[i].GetAnnotations()
			if a == nil {
				a = map[string]string{}
			}
			a["ops.internal/source-id"] = cluster.SourceID
			s.Objects[i].SetAnnotations(a)
		}
		if err := replaceCurrent(ctx, g, s); err != nil {
			return err
		}
		for _, o := range s.Objects {
			if previous[string(o.GetUID())] == o.GetResourceVersion() {
				continue
			}
			if !shortLivedOrHistorical(o) {
				continue
			}
			if err := queue.enqueue(o); err != nil {
				// Do not advance the resourceVersion acknowledgement. The full
				// snapshot is retried while previously queued facts remain owned.
				return err
			}
		}
		previous = next
		return nil
	}
}
func replaceCurrent(ctx context.Context, g *graph.Graph, s kubernetes.Snapshot) error {
	for attempt := 0; attempt < 3; attempt++ {
		err := g.Replace(ctx, g.OwnerEpoch(), s)
		if !errors.Is(err, graph.ErrStale) {
			return err
		}
	}
	return graph.ErrStale
}
func shortLivedOrHistorical(o unstructured.Unstructured) bool {
	if len(o.GetOwnerReferences()) > 0 {
		return true
	}
	switch o.GetKind() {
	case "Pod", "Event", "Job", "ReplicaSet", "PersistentVolumeClaim", "PersistentVolume", "VolumeAttachment", "EndpointSlice":
		return true
	}
	return false
}
func prepareProjection(ctx context.Context, cluster SP04Cluster, o unstructured.Unstructured, archive *evidence.ArchiveService) (uuid.UUID, error) {
	if archive == nil {
		return uuid.Nil, errors.New("archive unavailable")
	}
	copy := o.DeepCopy()
	copy.SetAnnotations(nil)
	data, err := json.Marshal(copy.Object)
	if err != nil {
		return uuid.Nil, err
	}
	if len(data) > 64<<10 {
		return uuid.Nil, evidence.ErrBudget
	}
	group := o.GroupVersionKind().Group
	if group == "" {
		group = "core"
	}
	id := resource.CanonicalID{Domain: "k8s", Tenant: cluster.Tenant, Scope: cluster.ClusterUID, APIGroup: group, Kind: o.GetKind(), StableID: string(o.GetUID())}.String()
	observed, _ := time.Parse(time.RFC3339Nano, o.GetAnnotations()["ops.internal/observed-at"])
	if observed.IsZero() {
		return uuid.Nil, resource.ErrIdentity
	}
	identity, _ := json.Marshal(struct{ ID, RV string }{id, o.GetResourceVersion()})
	objectID := uuid.NewSHA1(uuid.NameSpaceOID, append([]byte(cluster.SourceID), identity...))
	scope := graph.Scope{Tenant: cluster.Tenant, Cluster: cluster.ClusterUID, Namespaces: []string{o.GetNamespace()}, ClusterScoped: true, AuthorizationRevision: "collector/" + cluster.SourceID + "/" + strconv.FormatInt(cluster.SourceRevision, 10)}
	metadata := evidence.Evidence{SourceScopeDigest: cluster.SourceScopeDigest, SchemaVersion: "evidence/v2", EvidenceID: objectID.String(), TenantID: cluster.Tenant, ResourceCanonicalID: id, Type: "resource_state", DataClass: "D0", SourceRegistrationID: cluster.SourceID, SourceRevision: cluster.SourceRevision, SourceSystem: "kubernetes", BackendLogicalID: cluster.BackendLogicalID, QueryTemplateVersion: "kubernetes-projection/v1", QueryHash: evidence.Digest(identity), EffectiveScope: scope, EvaluatedAt: observed, ObservedFrom: observed, ObservedTo: observed, SourceRetentionUntil: observed, TimeReliable: true, ReplayState: "archive_pending", ContentDigest: evidence.Digest(data), DerivationEvidenceRefs: []string{}, IndependenceGroup: cluster.SourceID, Data: data}
	// Re-listing or a second Worker may observe the same immutable UID/RV at a
	// later wall clock. Reuse the first durable observation; the new observation
	// cannot rebind its archive identity or renew history retention.
	previous, previousErr := (evidence.Repository{Pool: archive.Pool}).Get(ctx, uuid.MustParse(cluster.Tenant), objectID, scope)
	if previousErr == nil {
		if previous.ContentDigest != metadata.ContentDigest || previous.SourceRevision != metadata.SourceRevision || previous.SourceRegistrationID != metadata.SourceRegistrationID || previous.ResourceCanonicalID != metadata.ResourceCanonicalID {
			return uuid.Nil, errors.New("projection archive identity conflict")
		}
		metadata = previous
		metadata.Data = data
		observed = previous.EvaluatedAt
	} else if !errors.Is(previousErr, pgx.ErrNoRows) {
		return uuid.Nil, previousErr
	}

	bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return objectID, archive.Prepare(bounded, metadata, o.GetNamespace(), observed.Add(181*24*time.Hour))
}
