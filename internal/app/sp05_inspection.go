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
	"ops-platform/internal/resource"
	"ops-platform/internal/resourcestore"
	"slices"
	"strings"
)

func withSP05SnapshotInspection(publish, inspect func(kubernetes.Snapshot) error) func(kubernetes.Snapshot) error {
	var pending *kubernetes.Snapshot
	// Absence-based deletion backstop: informer tombstone deltas can be lost
	// across a collector restart or reconnect, while authoritative absence in
	// a complete snapshot never lies (the same basis as identity retirement).
	seen := map[string]unstructured.Unstructured{}
	return func(s kubernetes.Snapshot) error {
		if !s.ObservationOnly && !s.State.LastListCompletedAt.IsZero() && s.State.LastError == "" {
			current := map[string]bool{}
			for _, o := range s.Objects {
				current[string(o.GetUID())] = true
			}
			for uid, old := range seen {
				if !current[uid] {
					s.Deleted = append(s.Deleted, old)
				}
			}
			next := map[string]unstructured.Unstructured{}
			for _, o := range s.Objects {
				next[string(o.GetUID())] = o
			}
			seen = next
		}
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
	// Deleting the subject of a fault ends every occurrence on it: watch
	// tombstones and the authoritative relist diff both arrive here. This is
	// subject-gone settlement, never a healthy-recovery claim.
	deletionSymptoms := map[string][][2]string{
		"Pod": {{"ImagePullFailure", "image"}, {"ImagePullError", "image"}, {"CrashLoopBackOff", "container"}, {"ContainerBackOffError", "container"}, {"OOMKilled", "container"}, {"ProbeFailure", "probe"}, {"Unschedulable", "scheduling"}, {"FailedScheduling", "scheduling"}, {"FailedMount", "storage"}, {"FailedAttachVolume", "storage"}},
		"PersistentVolumeClaim": {{"PVCUnbound", "storage"}, {"ProvisioningFailed", "storage"}},
		"Deployment":            {{"RolloutStuck", "workload"}},
		"Service":               {{"ServiceNoEndpoints", "network"}},
	}
	// Deletion and aggregation observations are clocked by the latest live
	// observation, never a stale list completion time: the poll anti-flap
	// guard rejects a settle whose observation predates the firing one.
	observed := s.State.LastListCompletedAt
	if s.State.LastWatchEventAt.After(observed) {
		observed = s.State.LastWatchEventAt
	}
	for _, o := range s.Deleted {
		symptoms, known := deletionSymptoms[o.GetKind()]
		if !known || o.GetUID() == "" {
			continue
		}
		id := resource.CanonicalID{Domain: "k8s", Tenant: cluster.Tenant, Scope: cluster.ClusterUID, APIGroup: inspection.CanonicalGroupFor(o.GetKind()), Kind: o.GetKind(), StableID: string(o.GetUID())}.String()
		for _, entry := range symptoms {
			raw, _ := json.Marshal(map[string]any{"nativeKind": o.GetKind(), "nativeUID": string(o.GetUID()), "resourceVersion": o.GetResourceVersion(), "reason": entry[0], "deleted": true})
			candidate := finding.FindingCandidate{ResourceCanonicalID: id, Namespace: o.GetNamespace(), RuleID: "kubernetes/" + entry[0] + "/v1", RuleFamily: entry[1], NormalizedSymptom: entry[0], State: "resolved", NativeIdentity: string(o.GetUID()) + "/deleted", IndependenceGroup: string(o.GetUID()), ObservedAt: observed, TimeReliable: !observed.IsZero(), QueryTemplateVersion: "sp05-signal/v1", Data: raw}
			if err := SubmitSP05Candidate(ctx, archive, binding, candidate); err != nil {
				return err
			}
		}
	}
	// Service endpoint health is aggregated across every slice of one
	// Service before submission: a service has backends when any slice
	// reports a ready endpoint, and per-slice observations never flap the
	// shared occurrence.
	type sliceAggregate struct {
		ready     int
		namespace string
		uid       string
		identity  []string
	}
	services := map[string]*sliceAggregate{}
	for _, o := range s.Objects {
		if o.GetKind() != "EndpointSlice" {
			continue
		}
		service := inspection.OwningServiceUID(o)
		if service == "" {
			continue
		}
		id := resource.CanonicalID{Domain: "k8s", Tenant: cluster.Tenant, Scope: cluster.ClusterUID, APIGroup: "core", Kind: "Service", StableID: service}.String()
		agg, exists := services[id]
		if !exists {
			agg = &sliceAggregate{namespace: o.GetNamespace(), uid: service}
			services[id] = agg
		}
		agg.ready += countReadyEndpoints(o)
		agg.identity = append(agg.identity, string(o.GetUID())+"/"+o.GetResourceVersion())
	}
	for id, agg := range services {
		slices.Sort(agg.identity)
		identity := strings.Join(agg.identity, ";")
		if len(identity) > 1024 {
			identity = identity[:1024]
		}
		state := "firing"
		if agg.ready > 0 {
			state = "resolved"
		}
		raw, _ := json.Marshal(map[string]any{"nativeKind": "EndpointSlice", "nativeUID": agg.uid, "resourceVersion": identity, "reason": "ServiceNoEndpoints", "readyEndpoints": agg.ready})
		candidate := finding.FindingCandidate{ResourceCanonicalID: id, Namespace: agg.namespace, RuleID: "kubernetes/ServiceNoEndpoints/v1", RuleFamily: "network", NormalizedSymptom: "ServiceNoEndpoints", State: state, NativeIdentity: identity, IndependenceGroup: "endpointslices/" + agg.uid, ObservedAt: observed, TimeReliable: !observed.IsZero(), QueryTemplateVersion: "sp05-signal/v1", Data: raw}
		if err := SubmitSP05Candidate(ctx, archive, binding, candidate); err != nil {
			return err
		}
	}
	for _, o := range s.Objects {
		for _, c := range inspection.Signals(o) {
			if c.ResourceUID == "" || c.ResourceKind == "" {
				continue
			}
			if c.NormalizedSymptom == "ServiceNoEndpoints" {
				// handled by the slice aggregation above
				continue
			}
			id := c.CanonicalID(cluster.Tenant, cluster.ClusterUID)
			observed := c.ObservedAt
			if observed.IsZero() {
				observed = s.State.LastListCompletedAt
			}
			fact := map[string]any{"nativeKind": o.GetKind(), "nativeUID": string(o.GetUID()), "resourceVersion": o.GetResourceVersion(), "reason": c.NormalizedSymptom}
			if o.GetKind() == "Event" {
				// Native core/v1 events carry reportingComponent; the legacy
				// source.component is the fallback for older API servers.
				reporter, _, _ := unstructured.NestedString(o.Object, "reportingComponent")
				if reporter == "" {
					reporter, _, _ = unstructured.NestedString(o.Object, "source", "component")
				}
				if reporter != "" {
					fact["reportingController"] = reporter
				}
				if message, _, _ := unstructured.NestedString(o.Object, "message"); message != "" {
					fact["message"] = message
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
			case "Unschedulable":
				fact["unschedulable"] = c.State == "firing"
			case "ImagePullFailure":
				fact["imagePullFailing"] = c.State == "firing"
			case "CrashLoopBackOff":
				fact["crashLoopFailing"] = c.State == "firing"
			case "OOMKilled":
				fact["oomKilled"] = c.State == "firing"
				fact["lastExitCode"] = lastTerminatedExitCode(o)
			case "ProbeFailure":
				fact["probeFailing"] = c.State == "firing"
			case "RolloutStuck":
				fact["rolloutStuck"] = c.State == "firing"
			case "ServiceNoEndpoints":
				fact["readyEndpoints"] = countReadyEndpoints(o)
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

// A missing conditions.ready on an EndpointSlice endpoint means ready, per the
// discovery.k8s.io contract; only an explicit false marks it unready.
func countReadyEndpoints(o unstructured.Unstructured) int {
	ready := 0
	endpoints, _, _ := unstructured.NestedSlice(o.Object, "endpoints")
	for _, raw := range endpoints {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		conditions, _ := m["conditions"].(map[string]any)
		if unready, ok := conditions["ready"].(bool); ok && !unready {
			continue
		}
		ready++
	}
	return ready
}

// The kernel OOM kill surfaces as exit code 137 on the terminated container
// that kubelet reports in lastState.
func lastTerminatedExitCode(o unstructured.Unstructured) float64 {
	for _, field := range []string{"containerStatuses", "initContainerStatuses"} {
		statuses, _, _ := unstructured.NestedSlice(o.Object, "status", field)
		for _, raw := range statuses {
			m, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			lastState, _ := m["lastState"].(map[string]any)
			terminated, _ := lastState["terminated"].(map[string]any)
			if terminated["reason"] == "OOMKilled" {
				switch code := terminated["exitCode"].(type) {
				case int64:
					return float64(code)
				case float64:
					return code
				}
			}
		}
	}
	return -1
}
