package rca

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/audit"
	"ops-platform/internal/configregistry"
	"ops-platform/internal/evidence"
	"ops-platform/internal/finding"
	"ops-platform/internal/graph"
	"ops-platform/internal/incident"
	"ops-platform/internal/persistence"
	"time"
)

var ErrStale = errors.New("STALE_CONTEXT")

type Revision struct {
	TenantID             string    `json:"tenantId"`
	IncidentID           string    `json:"incidentId"`
	Revision             int64     `json:"revision"`
	BaseIncidentRevision int64     `json:"baseIncidentRevision"`
	InputDigest          string    `json:"inputDigest"`
	Actor                string    `json:"actor"`
	Source               string    `json:"source"`
	Superseded           bool      `json:"superseded"`
	InputManifest        Input     `json:"inputManifest"`
	Result               Result    `json:"result"`
	CreatedAt            time.Time `json:"createdAt"`
}
type Repository struct {
	Pool       persistence.TxBeginner
	Trust      configregistry.Ed25519TrustStore
	Archive    *evidence.ArchiveService
	Graph      *graph.Graph
	GraphScope graph.Scope
}

// Commit re-evaluates the frozen input, verifies every actual archive metadata
// reference and protects dependencies in the same append-only revision TX.
func (r Repository) Commit(ctx context.Context, tenant uuid.UUID, id, actor, key string, base, current int64, recipeVersionID *uuid.UUID, recipe Recipe, input Input) (Revision, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(recipe.Budget.TimeoutMs)*time.Millisecond)
	defer cancel()
	result, err := Evaluate(recipe, input)
	if err != nil {
		return Revision{}, err
	}
	if result.Status == "confirmed" {
		if r.Archive == nil || r.Graph == nil || r.GraphScope.Tenant != tenant.String() {
			return Revision{}, ErrStale
		}
		actualGraph, err := QueryImpact(ctx, r.Graph, recipe, graph.Query{CanonicalID: input.ResourceCanonicalID, Scope: r.GraphScope})
		if err != nil || !sameJSON(actualGraph.GraphRevision, input.Graph.GraphRevision) {
			return Revision{}, ErrStale
		}
		input.Graph = actualGraph
		input.Evidence = append([]evidence.Evidence(nil), input.Evidence...)
		for index, e := range input.Evidence {
			ref, err := uuid.Parse(e.EvidenceID)
			if err != nil {
				return Revision{}, ErrStale
			}
			data, archiveRef, err := r.Archive.Read(ctx, tenant, ref)
			if err != nil || evidence.Digest(data) != e.ContentDigest {
				return Revision{}, ErrStale
			}
			input.Evidence[index].Data = data
			input.Evidence[index].ArchiveRef = &archiveRef
		}
		result, err = Evaluate(recipe, input)
		if err != nil {
			return Revision{}, err
		}
		if result.Status != "confirmed" {
			return Revision{}, ErrStale
		}
	}
	var out Revision
	for i := range input.Evidence {
		input.Evidence[i].FactSlice = nil
	}
	digest := InputDigest(input, result)
	commit := func(ctx context.Context) error {
		return persistence.WithTenantTx(ctx, r.Pool, tenant, func(tx pgx.Tx) error {
			i, err := incident.Load(ctx, tx, tenant, id, true)
			if err != nil {
				return err
			}
			if i.ResourceCanonicalID != input.ResourceCanonicalID {
				return ErrStale
			}
			if err := r.verifyRecipe(ctx, tx, tenant, recipeVersionID, i.ClusterUID, i.Namespace, recipe); err != nil {
				return err
			}
			for _, e := range input.Evidence {
				var content, state string
				var metadata []byte
				if err := tx.QueryRow(ctx, `SELECT content_digest,replay_state,metadata FROM platform.evidence_metadata WHERE tenant_id=$1 AND evidence_id=$2 AND NOT deleting`, tenant, e.EvidenceID).Scan(&content, &state, &metadata); err != nil {
					return err
				}
				var actual evidence.Evidence
				if json.Unmarshal(metadata, &actual) != nil || content != e.ContentDigest || !sameMetadata(actual, e) {
					return ErrStale
				}
				if state != "archived_verified" && result.Status == "confirmed" {
					return ErrStale
				}
				// A revocation is a hard authorization fence even for immutable archives.
				var active bool
				if err := tx.QueryRow(ctx, `SELECT platform.sp05_lock_evidence_source($1,$2,$3)`, tenant, e.SourceRegistrationID, e.SourceRevision).Scan(&active); err != nil {
					return err
				}
				if !active {
					return ErrStale
				}
			}
			var previous []byte
			err = tx.QueryRow(ctx, `SELECT result FROM incident.rca_revisions WHERE tenant_id=$1 AND incident_id=$2 AND evaluation_key=$3`, tenant, id, key).Scan(&previous)
			if err == nil {
				var stored Revision
				if json.Unmarshal(previous, &stored) != nil {
					return ErrStale
				}
				if stored.InputDigest != digest {
					return finding.ErrConflict
				}
				out = stored
				_, err = tx.Exec(ctx, `UPDATE incident.records SET last_rca_checked_at=clock_timestamp() WHERE tenant_id=$1 AND incident_id=$2`, tenant, id)
				return err
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if i.CurrentRCARevision != current {
				return ErrStale
			}
			superseded := i.Revision != base
			var next int64
			if err := tx.QueryRow(ctx, `SELECT COALESCE(max(revision),0)+1 FROM incident.rca_revisions WHERE tenant_id=$1 AND incident_id=$2`, tenant, id).Scan(&next); err != nil {
				return err
			}

			out = Revision{TenantID: tenant.String(), IncidentID: id, Revision: next, BaseIncidentRevision: base, InputDigest: digest, Actor: actor, Source: "deterministic", Superseded: superseded, InputManifest: input, Result: result, CreatedAt: time.Now().UTC()}
			raw, _ := json.Marshal(out)
			manifest, _ := json.Marshal(input)
			provenance, _ := json.Marshal(map[string]any{"recipe": recipe.Name, "version": recipe.Version, "digest": result.RecipeDigest, "graphRevision": input.Graph.GraphRevision})
			_, err = tx.Exec(ctx, `INSERT INTO incident.rca_revisions(tenant_id,incident_id,revision,evaluation_key,input_digest,base_incident_revision,recipe_version_id,recipe_digest,actor,source,provenance,input_manifest,result,superseded,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'deterministic',$10,$11,$12,$13,$14)`, tenant, id, next, key, digest, base, recipeVersionID, result.RecipeDigest, actor, provenance, manifest, raw, superseded, out.CreatedAt)
			if err != nil {
				return err
			}
			auditID := uuid.Must(uuid.NewV7())
			until := time.Now().Add(365 * 24 * time.Hour)
			for _, e := range input.Evidence {
				ref, err := uuid.Parse(e.EvidenceID)
				if err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `INSERT INTO incident.rca_evidence_refs VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, tenant, id, next, ref); err != nil {
					return err
				}
				for _, kind := range []string{"rca", "incident", "audit"} {
					reference := uuid.MustParse(id)
					if kind == "audit" {
						reference = auditID
					}
					if err := evidence.Protect(ctx, tx, tenant, ref, reference, kind, until, kind == "incident" && incident.Active(i.State)); err != nil {
						return err
					}
				}
			}
			if !superseded {
				tag, err := tx.Exec(ctx, `UPDATE incident.records SET current_rca_revision=$3,last_rca_checked_at=clock_timestamp() WHERE tenant_id=$1 AND incident_id=$2 AND revision=$4 AND current_rca_revision=$5`, tenant, id, next, base, current)
				if err != nil {
					return err
				}
				if tag.RowsAffected() != 1 {
					return ErrStale
				}
			}
			if _, err := tx.Exec(ctx, `UPDATE incident.outbox SET state='delivered' WHERE tenant_id=$1 AND incident_id=$2 AND revision<=$3`, tenant, id, base); err != nil {
				return err
			}
			_, err = audit.Append(ctx, tx, audit.Entry{TenantID: tenant, RecordID: auditID, EntityID: uuid.MustParse(id), EntityKind: "rca", EventType: "rca.revision", Subject: actor, Payload: map[string]any{"revision": next, "baseIncidentRevision": base, "inputDigest": digest, "recipeDigest": result.RecipeDigest, "status": result.Status, "superseded": superseded}})
			return err
		})
	}
	if result.Status == "confirmed" {
		err = r.Graph.WithCurrentResponse(ctx, input.Graph, commit)
	} else {
		err = commit(ctx)
	}
	return out, err
}
func (r Repository) Read(ctx context.Context, tenant uuid.UUID, id string, revision int64) (Revision, error) {
	var out Revision
	err := persistence.WithTenantTx(ctx, r.Pool, tenant, func(tx pgx.Tx) error {
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT result FROM incident.rca_revisions WHERE tenant_id=$1 AND incident_id=$2 AND revision=$3`, tenant, id, revision).Scan(&raw); err != nil {
			return err
		}
		return json.Unmarshal(raw, &out)
	})
	return out, err
}
