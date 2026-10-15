package resourcestore

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/datascope"
	"ops-platform/internal/evidence"
	"ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/persistence"
	"ops-platform/internal/resource"
)

// HistoryProjection is deliberately narrower than the live graph projection:
// no labels, annotations, credentials, command text or arbitrary event messages.
func HistoryProjection(o unstructured.Unstructured) map[string]any {
	out := map[string]any{"kind": o.GetKind(), "uid": string(o.GetUID()), "name": o.GetName(), "namespace": o.GetNamespace()}
	for _, path := range [][]string{{"status", "phase"}, {"status", "conditions"}, {"status", "containerStatuses"}, {"status", "initContainerStatuses"}, {"spec", "containers"}, {"spec", "initContainers"}, {"spec", "template", "spec", "containers"}, {"spec", "template", "spec", "initContainers"}, {"spec", "nodeName"}, {"spec", "volumeName"}, {"spec", "storageClassName"}, {"spec", "replicas"}, {"spec", "unschedulable"}, {"spec", "volumes"}, {"spec", "claimRef"}, {"spec", "csi", "driver"}, {"involvedObject"}, {"regarding"}, {"reason"}, {"type"}, {"reportingController"}, {"count"}} {
		if value, ok, _ := unstructured.NestedFieldCopy(o.Object, path...); ok {
			_ = unstructured.SetNestedField(out, value, path...)
		}
	}
	owners := []any{}
	for _, ref := range o.GetOwnerReferences() {
		owners = append(owners, map[string]any{"apiVersion": ref.APIVersion, "kind": ref.Kind, "uid": string(ref.UID), "name": ref.Name})
	}
	out["owners"] = owners
	return out
}

func (r Repository) observe(ctx context.Context, tenant, source, canonical string, o unstructured.Unstructured, state kubernetes.GVRState) error {
	at, err := time.Parse(time.RFC3339Nano, o.GetAnnotations()["ops.internal/observed-at"])
	if err != nil {
		return resource.ErrIdentity
	}
	coverage := "coverage_gap"
	if state.WatchContinuous && state.LastError == "" {
		coverage = "continuous_watch"
	} else if state.LastWatchEventAt.IsZero() {
		coverage = "initial_list"
	}
	var eventAt *time.Time
	if o.GetKind() == "Event" {
		for _, key := range []string{"eventTime", "lastTimestamp", "firstTimestamp"} {
			value, _, _ := unstructured.NestedString(o.Object, key)
			if parsed, e := time.Parse(time.RFC3339Nano, value); e == nil && !parsed.After(at) {
				eventAt = &parsed
				break
			}
		}
	}
	return persistence.WithTenantTx(ctx, r.Pool, uuid.MustParse(tenant), func(tx pgx.Tx) error {
		var revision int64
		var mapping []byte
		var sourceType, backend string
		if err := tx.QueryRow(ctx, `SELECT revision,data_scope_mapping,source_type,backend_logical_id FROM platform.source_registrations WHERE tenant_id=$1 AND source_id=$2 AND status='active'`, tenant, source).Scan(&revision, &mapping, &sourceType, &backend); err != nil {
			return err
		}
		var scopeMapping datascope.Mapping
		if json.Unmarshal(mapping, &scopeMapping) != nil {
			return resource.ErrIdentity
		}
		scope := evidence.BindingScopeDigest(evidence.Binding{Tenant: tenant, SourceID: source, SourceType: sourceType, BackendLogicalID: backend, Revision: revision, ScopeMapping: scopeMapping})
		if r.ExpectedRevision > 0 && revision != r.ExpectedRevision {
			return resource.ErrIdentity
		}
		var valid bool
		if tx.QueryRow(ctx, `SELECT platform.sp05_lock_evidence_source($1,$2,$3)`, tenant, source, revision).Scan(&valid) != nil || !valid {
			return resource.ErrIdentity
		}
		projection := HistoryProjection(o)
		relations, err := observedRelations(ctx, tx, tenant, canonical, source, o, at)
		if err != nil {
			return err
		}
		projection["relations"] = relations
		encoded, err := json.Marshal(projection)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO platform.resource_observations(tenant_id,observation_id,canonical_id,namespace,source_id,source_revision,source_scope_digest,resource_version,observed_at,event_at,coverage,projection,retain_until) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) ON CONFLICT(tenant_id,source_id,source_revision,canonical_id,resource_version) DO NOTHING`, tenant, uuid.Must(uuid.NewV7()), canonical, o.GetNamespace(), source, revision, scope, o.GetResourceVersion(), at, eventAt, coverage, encoded, at.Add(30*24*time.Hour))
		return err
	})
}

// Freeze only native relations whose exact target UID is already known at
// observation time. A later current lookup never fills a historical gap.
func observedRelations(ctx context.Context, tx pgx.Tx, tenant, canonical, source string, o unstructured.Unstructured, at time.Time) ([]resource.Relation, error) {
	out := []resource.Relation{}
	type endpoint struct{ kind, namespace, name, uid, group, relation string }
	candidates := []endpoint{}
	for _, owner := range o.GetOwnerReferences() {
		group := "core"
		if strings.Contains(owner.APIVersion, "/") {
			group = strings.SplitN(owner.APIVersion, "/", 2)[0]
		}
		candidates = append(candidates, endpoint{owner.Kind, o.GetNamespace(), owner.Name, string(owner.UID), group, "owned_by"})
	}
	if node, _, _ := unstructured.NestedString(o.Object, "spec", "nodeName"); node != "" {
		candidates = append(candidates, endpoint{"Node", "", node, "", "core", "scheduled_on"})
	}
	volumes, _, _ := unstructured.NestedSlice(o.Object, "spec", "volumes")
	for _, raw := range volumes {
		m, _ := raw.(map[string]any)
		pvc, _ := m["persistentVolumeClaim"].(map[string]any)
		name, _ := pvc["claimName"].(string)
		if name != "" {
			candidates = append(candidates, endpoint{"PersistentVolumeClaim", o.GetNamespace(), name, "", "core", "uses"})
		}
	}
	if volume, _, _ := unstructured.NestedString(o.Object, "spec", "volumeName"); volume != "" {
		candidates = append(candidates, endpoint{"PersistentVolume", "", volume, "", "core", "bound_to"})
	}
	for _, c := range candidates {
		rows, err := tx.Query(ctx, `SELECT canonical_id FROM platform.resource_entities WHERE tenant_id=$1 AND kind=$2 AND namespace=$3 AND name=$4 AND deleted_at IS NULL AND observed_at<=$5 ORDER BY canonical_id LIMIT 2`, tenant, c.kind, c.namespace, c.name, at)
		if err != nil {
			return nil, err
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		if len(ids) != 1 {
			continue
		}
		parsed, err := resource.ParseCanonicalID(ids[0])
		anchor, _ := resource.ParseCanonicalID(canonical)
		if parsed.Scope != anchor.Scope {
			continue
		}
		if err != nil || c.uid != "" && parsed.StableID != c.uid || parsed.APIGroup != c.group {
			continue
		}
		out = append(out, resource.Relation{From: canonical, To: ids[0], Kind: c.relation, ObservedAt: at, ValidFrom: at, Confidence: 1, Provenance: resource.Provenance{SourceRegistrationID: source, RuleVersion: "native-history/v1", ObservedAt: at}})
	}
	return out, nil
}

func (r Repository) ObserveCoverage(ctx context.Context, tenant, source string, gvr kubernetes.GVR, state kubernetes.GVRState) error {
	return persistence.WithTenantTx(ctx, r.Pool, uuid.MustParse(tenant), func(tx pgx.Tx) error {
		var valid bool
		if tx.QueryRow(ctx, `SELECT platform.sp05_lock_evidence_source($1,$2,$3)`, tenant, source, r.ExpectedRevision).Scan(&valid) != nil || !valid {
			return resource.ErrIdentity
		}
		var listed *time.Time
		if !state.LastListCompletedAt.IsZero() {
			listed = &state.LastListCompletedAt
		}
		_, err := tx.Exec(ctx, `INSERT INTO platform.resource_observation_coverage(tenant_id,source_id,source_revision,gvr,last_list_at,watch_connected,watch_continuous,has_error) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, tenant, source, r.ExpectedRevision, gvr.Key(), listed, state.WatchConnected, state.WatchContinuous, state.LastError != "")
		return err
	})
}
