package resourcestore

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/evidence"
	"ops-platform/internal/persistence"
	"ops-platform/internal/resource"
	"strings"
	"time"
)

// SaveHardware stores identity/provenance only. Typed inventory stays in the
// Worker graph and encrypted archive, not in an independent database graph.
func (r Repository) SaveHardware(ctx context.Context, b evidence.Binding, entities []resource.Entity) error {
	if err := (evidence.Repository{Pool: r.Pool}).CheckBinding(ctx, b); err != nil {
		return err
	}
	for _, e := range entities {
		id, err := resource.ParseCanonicalID(e.CanonicalID)
		if err != nil || id.Domain != "hardware" || id.Tenant != b.Tenant || id.Kind != e.Kind {
			return resource.ErrIdentity
		}
		parent := strings.SplitN(id.StableID, "/", 2)[0]
		if resource.NormalizeHardwareUUID(parent) == "" {
			return resource.ErrIdentity
		}
		if id.Kind == "PhysicalServer" {
			serial, _ := e.Attributes["serial"].(string)
			out, err := r.Resolve(ctx, resource.SourceRef{Domain: "hardware", Tenant: b.Tenant, Scope: id.Scope, APIGroup: "redfish", Kind: id.Kind, UUID: parent, Serial: serial, Name: e.Name, SourceRegistrationID: b.SourceID, ObservedAt: e.UpdatedAt})
			if err != nil {
				return err
			}
			if out.Status != resource.Matched {
				return resource.ErrIdentity
			}
			continue
		}
		resolution := resource.Resolution{Status: resource.Matched, CanonicalID: id, Confidence: 1, RuleVersion: "redfish-member/v1", SourceValues: map[string]string{"parentUUID": parent, "memberId": strings.TrimPrefix(id.StableID, parent+"/")}, ObservedAt: e.UpdatedAt, Aliases: []resource.Alias{}, Relations: []resource.Relation{}}
		raw, _ := json.Marshal(resolution)
		err = persistence.WithTenantTx(ctx, r.Pool, uuid.MustParse(b.Tenant), func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,41))`, b.Tenant); err != nil {
				return err
			}
			var cluster uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT cluster_id FROM platform.cluster_registrations WHERE tenant_id=$1 AND cluster_uid=$2 AND status='active'`, b.Tenant, id.Scope).Scan(&cluster); err != nil {
				return err
			}
			var root bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform.resource_entities WHERE tenant_id=$1 AND canonical_id=$2 AND deleted_at IS NULL)`, b.Tenant, resource.CanonicalID{Domain: "hardware", Tenant: b.Tenant, Scope: id.Scope, APIGroup: "redfish", Kind: "PhysicalServer", StableID: parent}.String()).Scan(&root); err != nil {
				return err
			}
			if !root {
				return resource.ErrIdentity
			}
			_, err := tx.Exec(ctx, `INSERT INTO platform.resource_entities(tenant_id,canonical_id,cluster_id,kind,namespace,name,metadata,observed_at) VALUES($1,$2,$3,$4,'',$5,$6,$7) ON CONFLICT(tenant_id,canonical_id) DO UPDATE SET metadata=EXCLUDED.metadata,observed_at=EXCLUDED.observed_at WHERE resource_entities.deleted_at IS NULL AND resource_entities.observed_at<=EXCLUDED.observed_at`, b.Tenant, e.CanonicalID, cluster, e.Kind, e.Name, raw, e.UpdatedAt)
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}
func (r Repository) HardwareRelations(ctx context.Context, b evidence.Binding, entities []resource.Entity) ([]resource.Relation, error) {
	if len(b.ScopeMapping.Scopes["cluster"]) != 1 {
		return nil, evidence.ErrScopeUnverified
	}
	result := []resource.Relation{}
	roots := map[string]resource.Entity{}
	for _, e := range entities {
		id, _ := resource.ParseCanonicalID(e.CanonicalID)
		if id.Kind == "PhysicalServer" {
			roots[id.StableID] = e
		}
	}
	for _, e := range entities {
		id, _ := resource.ParseCanonicalID(e.CanonicalID)
		parent := strings.SplitN(id.StableID, "/", 2)[0]
		root, found := roots[parent]
		if id.Kind != "PhysicalServer" && found {
			result = append(result, resource.Relation{From: root.CanonicalID, To: e.CanonicalID, Kind: "contains", Confidence: 1, ObservedAt: e.UpdatedAt, ValidFrom: e.UpdatedAt, TTLSeconds: 600, Provenance: resource.Provenance{SourceRegistrationID: b.SourceID, RuleVersion: "redfish-member/v1", ObservedAt: e.UpdatedAt, SourceValues: map[string]string{"parentUUID": parent}}})
			if id.Kind == "DIMM" {
				result = append(result, resource.Relation{From: e.CanonicalID, To: root.CanonicalID, Kind: "component_of", Confidence: 1, ObservedAt: e.UpdatedAt, ValidFrom: e.UpdatedAt, TTLSeconds: 600, Provenance: resource.Provenance{SourceRegistrationID: b.SourceID, RuleVersion: "redfish-dimm-component/v1", ObservedAt: e.UpdatedAt, SourceValues: map[string]string{"parentUUID": parent}}})
			}
		}
	}
	err := persistence.WithTenantTx(ctx, r.Pool, uuid.MustParse(b.Tenant), func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT e.canonical_id,e.metadata,e.observed_at FROM platform.resource_entities e JOIN platform.cluster_registrations c USING(tenant_id,cluster_id) WHERE e.tenant_id=$1 AND c.cluster_uid=$2 AND e.kind='Node' AND e.deleted_at IS NULL`, b.Tenant, b.ScopeMapping.Scopes["cluster"][0])
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			var raw []byte
			var observed time.Time
			if err := rows.Scan(&id, &raw, &observed); err != nil {
				return err
			}
			var n resource.Resolution
			if json.Unmarshal(raw, &n) != nil {
				return resource.ErrIdentity
			}
			root, found := roots[resource.NormalizeHardwareUUID(n.SourceValues["uuid"])]
			if found {
				when := root.UpdatedAt
				result = append(result, resource.Relation{From: root.CanonicalID, To: id, Kind: "hosts", Confidence: 1, ObservedAt: when, ValidFrom: when, TTLSeconds: 600, Provenance: resource.Provenance{SourceRegistrationID: b.SourceID, RuleVersion: "system-uuid-hosts/v1", ObservedAt: when, SourceValues: map[string]string{"systemUUID": n.SourceValues["uuid"], "nodeObservedAt": observed.UTC().Format(time.RFC3339Nano)}}})
			}
		}
		return rows.Err()
	})
	if len(result) > 400 {
		return nil, evidence.ErrBudget
	}
	return result, err
}
