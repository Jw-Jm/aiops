package resourcestore

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/audit"
	"ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/persistence"
	"ops-platform/internal/resource"
	"time"
)

type Repository struct {
	Pool             persistence.TxBeginner
	ExpectedRevision int64
	BackendLogicalID string
}

// Resolve authenticates the source registration and loads only exact immutable
// UUID/UID/provider aliases. Candidate data is never accepted from a caller.
func (r Repository) Resolve(ctx context.Context, ref resource.SourceRef) (resource.Resolution, error) {
	var out resource.Resolution
	tenant, err := uuid.Parse(ref.Tenant)
	if err != nil {
		return out, resource.ErrIdentity
	}
	source, err := uuid.Parse(ref.SourceRegistrationID)
	if err != nil {
		return out, resource.ErrIdentity
	}
	err = persistence.WithTenantTx(ctx, r.Pool, tenant, func(tx pgx.Tx) error {
		// All alias/retirement changes for this tenant share one lock. A concurrent
		// discovery cannot silently choose whichever alias insert wins first.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,41))`, ref.Tenant); err != nil {
			return err
		}
		var clusterID *uuid.UUID
		var clusterUID, sourceType, backend string
		var revision int64
		if err := tx.QueryRow(ctx, `SELECT s.cluster_id,COALESCE(c.cluster_uid,''),s.source_type,s.revision,s.backend_logical_id FROM platform.source_registrations s LEFT JOIN platform.cluster_registrations c USING(tenant_id,cluster_id) JOIN platform.tenants t USING(tenant_id) WHERE s.tenant_id=$1 AND s.source_id=$2 AND s.status='active' AND t.status='active' AND (c.cluster_id IS NULL OR c.status='active')`, tenant, source).Scan(&clusterID, &clusterUID, &sourceType, &revision, &backend); err != nil {
			return err
		}
		if r.ExpectedRevision > 0 && (revision != r.ExpectedRevision || backend != r.BackendLogicalID) {
			return resource.ErrIdentity
		}
		if ref.Domain == "k8s" && (sourceType != "kubernetes" || ref.Scope != clusterUID) {
			return resource.ErrIdentity
		}
		if ref.Domain == "hardware" && sourceType != "redfish" {
			return resource.ErrIdentity
		}
		values := []string{}
		if ref.UID != "" {
			values = append(values, ref.UID)
		}
		if resource.NormalizeHardwareUUID(ref.UUID) != "" {
			values = append(values, resource.NormalizeHardwareUUID(ref.UUID))
		}
		if ref.ProviderID != "" {
			values = append(values, ref.ProviderID)
		}
		rows, err := tx.Query(ctx, `SELECT DISTINCT a.canonical_id FROM platform.resource_aliases a JOIN platform.resource_entities e USING(tenant_id,canonical_id) WHERE a.tenant_id=$1 AND e.deleted_at IS NULL AND ((a.scope=$2 AND a.alias_kind IN('uid','provider_id') AND a.alias_value=ANY($3)) OR (a.alias_kind='system_uuid' AND a.alias_value=$4)) ORDER BY a.canonical_id`, tenant, ref.Scope, values, resource.NormalizeHardwareUUID(ref.UUID))
		if err != nil {
			return err
		}
		ref.Candidates = nil
		for rows.Next() {
			var raw string
			if err := rows.Scan(&raw); err != nil {
				rows.Close()
				return err
			}
			id, err := resource.ParseCanonicalID(raw)
			if err != nil {
				rows.Close()
				return err
			}
			ref.Candidates = append(ref.Candidates, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		expected, err := resource.NewResolver().Resolve(ctx, resource.SourceRef{Domain: ref.Domain, Tenant: ref.Tenant, Scope: ref.Scope, APIGroup: ref.APIGroup, Kind: ref.Kind, UID: ref.UID, UUID: ref.UUID, ObservedAt: ref.ObservedAt})
		if err != nil {
			return err
		}
		incompatible := false
		if expected.Status == resource.Matched {
			var raw []byte
			err := tx.QueryRow(ctx, `SELECT metadata FROM platform.resource_entities WHERE tenant_id=$1 AND canonical_id=$2`, tenant, expected.CanonicalID.String()).Scan(&raw)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if err == nil {
				var previous resource.Resolution
				if json.Unmarshal(raw, &previous) != nil {
					return resource.ErrIdentity
				}
				for _, key := range []string{"uuid", "providerId"} {
					old, new := previous.SourceValues[key], map[string]string{"uuid": ref.UUID, "providerId": ref.ProviderID}[key]
					if key == "uuid" {
						old, new = resource.NormalizeHardwareUUID(old), resource.NormalizeHardwareUUID(new)
					}
					if old != "" && new != "" && old != new {
						incompatible = true
					}
				}
			}
		}
		out, err = resource.NewResolver().Resolve(ctx, ref)
		if err != nil {
			return err
		}
		if incompatible {
			out.Status = resource.Conflicted
			out.CanonicalID = resource.CanonicalID{}
			out.Aliases = nil
			out.Relations = nil
			out.Confidence = 0
		}
		if out.Status != resource.Matched {
			if out.Status == resource.Conflicted && expected.Status == resource.Matched {
				if err := identityConflictFinding(ctx, tx, tenant, source, clusterID, ref, out, expected.CanonicalID); err != nil {
					return err
				}
			}
			_, err = audit.Append(ctx, tx, audit.Entry{TenantID: tenant, RecordID: uuid.Must(uuid.NewV7()), EntityID: source, EntityKind: "source", EventType: "resource.identity_" + string(out.Status), Subject: "platform-worker", Payload: map[string]any{"resolution": out}})
			return err
		}
		metadata, err := json.Marshal(out)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `INSERT INTO platform.resource_entities(tenant_id,canonical_id,cluster_id,kind,namespace,name,metadata,observed_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT(tenant_id,canonical_id) DO UPDATE SET metadata=EXCLUDED.metadata,observed_at=EXCLUDED.observed_at,name=EXCLUDED.name WHERE resource_entities.deleted_at IS NULL AND resource_entities.observed_at<=EXCLUDED.observed_at`, tenant, out.CanonicalID.String(), clusterID, ref.Kind, ref.Namespace, ref.Name, metadata, ref.ObservedAt)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			// A Standby may finish projecting an older observation after Active.
			// The exact immutable UID is already resolved; keep newer metadata
			// and aliases while refusing to resurrect a terminal tombstone.
			var deleted *time.Time
			var namespace string
			if err := tx.QueryRow(ctx, `SELECT deleted_at,namespace FROM platform.resource_entities WHERE tenant_id=$1 AND canonical_id=$2`, tenant, out.CanonicalID.String()).Scan(&deleted, &namespace); err != nil {
				return err
			}
			if deleted != nil || namespace != ref.Namespace {
				return resource.ErrIdentity
			}
			return nil
		}
		for _, alias := range out.Aliases {
			provenance, _ := json.Marshal(alias.Provenance)
			if _, err = tx.Exec(ctx, `INSERT INTO platform.resource_aliases(tenant_id,scope,alias_kind,alias_value,canonical_id,source_id,provenance) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(tenant_id,scope,alias_kind,alias_value,canonical_id) DO UPDATE SET provenance=EXCLUDED.provenance`, tenant, alias.Scope, alias.Kind, alias.Value, alias.CanonicalID, source, provenance); err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}

// Retire is an immutable UID tombstone; old aliases remain as historical
// provenance but are excluded from active resolution.
func (r Repository) Retire(ctx context.Context, tenant uuid.UUID, id string) error {
	canonical, err := resource.ParseCanonicalID(id)
	if err != nil || canonical.Tenant != tenant.String() {
		return resource.ErrIdentity
	}
	return persistence.WithTenantTx(ctx, r.Pool, tenant, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,41))`, tenant.String()); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE platform.resource_entities SET deleted_at=COALESCE(deleted_at,clock_timestamp()) WHERE tenant_id=$1 AND canonical_id=$2`, tenant, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errors.New("resource identity not found")
		}
		return nil
	})
}

// SyncSnapshot persists identity/provenance and UID retirement; it does not
// persist a graph, adjacency index or full source object payload.
func (r Repository) SyncSnapshot(ctx context.Context, tenant, source, cluster, kind, apiGroup string, s kubernetes.Snapshot, previous map[string]string) (map[string]string, error) {
	if s.State.LastListCompletedAt.IsZero() {
		return previous, nil
	}
	current := map[string]string{}
	for _, o := range s.Objects {
		uid := string(o.GetUID())
		current[uid] = o.GetResourceVersion()
		if previous[uid] == o.GetResourceVersion() {
			continue
		}
		group := o.GroupVersionKind().Group
		if group == "" {
			group = "core"
		}
		observed, _ := time.Parse(time.RFC3339Nano, o.GetAnnotations()["ops.internal/observed-at"])
		if observed.IsZero() {
			observed = s.State.LastListCompletedAt
		}
		hardwareUUID, _, _ := unstructured.NestedString(o.Object, "status", "nodeInfo", "systemUUID")
		providerID, _, _ := unstructured.NestedString(o.Object, "spec", "providerID")
		out, err := r.Resolve(ctx, resource.SourceRef{Domain: "k8s", Tenant: tenant, Scope: cluster, APIGroup: group, Kind: o.GetKind(), UID: uid, UUID: hardwareUUID, ProviderID: providerID, Name: o.GetName(), Namespace: o.GetNamespace(), SourceRegistrationID: source, ObservedAt: observed})
		if err != nil {
			return previous, err
		}
		if out.Status != resource.Matched {
			return previous, resource.ErrIdentity
		}
	}
	for uid := range previous {
		if _, present := current[uid]; present {
			continue
		}
		for _, kind := range []string{kind} {
			id := resource.CanonicalID{Domain: "k8s", Tenant: tenant, Scope: cluster, APIGroup: apiGroup, Kind: kind, StableID: uid}.String()
			if err := r.Retire(ctx, uuid.MustParse(tenant), id); err != nil {
				return previous, err
			}
		}
	}
	return current, nil
}
