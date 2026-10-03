package rca

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/finding"
	"ops-platform/internal/persistence"
	"time"
)

type FindingInput struct {
	FindingID            string    `json:"findingId"`
	AggregateRevision    int64     `json:"aggregateRevision"`
	OccurrenceID         string    `json:"occurrenceId"`
	SourceFingerprint    string    `json:"sourceFingerprint"`
	SourceRegistrationID string    `json:"sourceRegistrationId"`
	SourceRevision       int64     `json:"sourceRevision"`
	ResourceCanonicalID  string    `json:"resourceCanonicalId"`
	Namespace            string    `json:"namespace"`
	LifecycleState       string    `json:"lifecycleState"`
	Digest               string    `json:"digest"`
	ObservedAt           time.Time `json:"observedAt"`
}

// Freeze a bounded, sorted metadata manifest. Share locks serialize ingestion
// through the final commit; unapplied revisions must first reach correlation.
func freezeFindings(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, id string) ([]FindingInput, error) {
	rows, err := tx.Query(ctx, `SELECT f.payload,f.aggregate_revision,COALESCE(a.aggregate_revision,0) FROM finding.records f JOIN incident.finding_links l USING(tenant_id,finding_id) LEFT JOIN incident.applied_revisions a USING(tenant_id,finding_id) WHERE l.tenant_id=$1 AND l.incident_id=$2 ORDER BY f.finding_id LIMIT 201 FOR SHARE OF f`, tenant, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FindingInput{}
	for rows.Next() {
		var raw []byte
		var revision, applied int64
		if err := rows.Scan(&raw, &revision, &applied); err != nil {
			return nil, err
		}
		var f finding.Finding
		if json.Unmarshal(raw, &f) != nil || revision != applied || f.AggregateRevision != revision {
			return nil, ErrStale
		}
		out = append(out, FindingInput{f.FindingID, revision, f.OccurrenceID, f.SourceFingerprint, f.SourceRegistrationID, f.SourceRevision, f.ResourceCanonicalID, f.Namespace, f.State, f.Digest, f.ObservedAt})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 || len(out) > 200 {
		return nil, ErrStale
	}
	return out, nil
}
func (r Repository) FreezeFindings(ctx context.Context, tenant uuid.UUID, id string) ([]FindingInput, error) {
	var out []FindingInput
	err := persistence.WithTenantTx(ctx, r.Pool, tenant, func(tx pgx.Tx) error { var err error; out, err = freezeFindings(ctx, tx, tenant, id); return err })
	return out, err
}
