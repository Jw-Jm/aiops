package app

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/evidence"
	"ops-platform/internal/finding"
	inspection "ops-platform/internal/inspection/kubernetes"
	"ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/persistence"
	"ops-platform/internal/resourcestore"
)

func withSP05SnapshotInspection(publish, inspect func(kubernetes.Snapshot) error) func(kubernetes.Snapshot) error {
	var pending *kubernetes.Snapshot
	return func(s kubernetes.Snapshot) error {
		if err := publish(s); err != nil {
			return err
		}
		if !s.ObservationOnly {
			pending = nil
			if !s.State.LastListCompletedAt.IsZero() && s.State.LastError != "" {
				// Initial List facts have been published, but are not eligible for
				// inspection until the authoritative initial Watch completes.
				copy := s
				pending = &copy
				return nil
			}
			return inspect(s)
		}
		if pending != nil && s.State.WatchConnected && s.State.WatchContinuous && s.State.LastError == "" {
			ready := *pending
			ready.State = s.State
			if err := inspect(ready); err != nil {
				return err
			}
			pending = nil
		}
		return nil
	}
}

func InspectSP05Snapshot(ctx context.Context, cluster SP04Cluster, s kubernetes.Snapshot, archive *evidence.ArchiveService) error {
	if s.ObservationOnly || s.State.LastListCompletedAt.IsZero() || s.State.LastError != "" {
		return nil
	}
	filtered, err := (resourcestore.Repository{Pool: archive.Pool, ExpectedRevision: cluster.SourceRevision, BackendLogicalID: cluster.BackendLogicalID}).FilterSnapshot(ctx, cluster.Tenant, cluster.SourceID, cluster.ClusterUID, s)
	if err != nil {
		return err
	}
	s = filtered
	var binding evidence.Binding
	err = persistence.WithTenantTx(ctx, archive.Pool, uuid.MustParse(cluster.Tenant), func(tx pgx.Tx) error {
		var raw []byte
		binding.Tenant, binding.SourceID, binding.Revision, binding.BackendLogicalID = cluster.Tenant, cluster.SourceID, cluster.SourceRevision, cluster.BackendLogicalID
		if err := tx.QueryRow(ctx, `SELECT source_type,data_scope_mapping FROM platform.source_registrations WHERE tenant_id=$1 AND source_id=$2 AND revision=$3 AND status='active'`, cluster.Tenant, cluster.SourceID, cluster.SourceRevision).Scan(&binding.SourceType, &raw); err != nil {
			return err
		}
		return json.Unmarshal(raw, &binding.ScopeMapping)
	})
	if err != nil {
		return err
	}
	for _, o := range s.Objects {
		for _, c := range inspection.Signals(o) {
			if c.ResourceUID == "" || c.ResourceKind == "" {
				continue
			}
			id := c.CanonicalID(cluster.Tenant, cluster.ClusterUID)
			observed := c.ObservedAt
			if observed.IsZero() {
				observed = s.State.LastListCompletedAt
			}
			fact := map[string]any{"nativeKind": o.GetKind(), "nativeUID": string(o.GetUID()), "resourceVersion": o.GetResourceVersion(), "reason": c.NormalizedSymptom}
			if o.GetKind() == "Event" {
				reporter, _, _ := unstructured.NestedString(o.Object, "reportingController")
				if reporter != "" {
					fact["reportingController"] = reporter
				}
			}
			switch c.NormalizedSymptom {
			case "NotReady":
				fact["ready"] = c.State == "resolved"
			case "PVCUnbound":
				if c.State == "firing" {
					fact["phase"] = "Pending"
				} else {
					fact["phase"] = "Bound"
				}
			}
			raw, _ := json.Marshal(fact)
			candidate := finding.FindingCandidate{ResourceCanonicalID: id, Namespace: c.Namespace, RuleID: c.RuleID, RuleFamily: c.RuleFamily, NormalizedSymptom: c.NormalizedSymptom, State: c.State, NativeIdentity: string(o.GetUID()) + "/" + o.GetResourceVersion(), IndependenceGroup: string(o.GetUID()), ObservedAt: observed, TimeReliable: c.TimeReliable, QueryTemplateVersion: "sp05-signal/v1", Data: raw}
			if err := SubmitSP05Candidate(ctx, archive, binding, candidate); err != nil {
				return err
			}
		}
	}
	return nil
}
