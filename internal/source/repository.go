package source

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type Repository struct{}

const sourceSelect = `
SELECT s.tenant_id, s.source_id, s.source_type, s.instance_key, s.cluster_id,
       COALESCE(c.cluster_uid, ''), s.auth_ref, s.credential_revision, s.status, s.revision,
       s.created_at, s.updated_at
FROM platform.source_registrations AS s
LEFT JOIN platform.cluster_registrations AS c
  ON c.tenant_id = s.tenant_id AND c.cluster_id = s.cluster_id
`

func loadSource(ctx context.Context, tx pgx.Tx, tenantID, sourceID uuid.UUID, lock bool) (SourceRegistration, error) {
	query := sourceSelect + ` WHERE s.tenant_id = $1 AND s.source_id = $2`
	if lock {
		query += ` FOR UPDATE OF s`
	}
	var result SourceRegistration
	var clusterID pgtype.UUID
	err := tx.QueryRow(ctx, query, tenantID, sourceID).Scan(
		&result.TenantID, &result.SourceID, &result.SourceType, &result.InstanceKey, &clusterID,
		&result.ClusterUID, &result.AuthRef, &result.CredentialRevision, &result.Status, &result.Revision,
		&result.CreatedAt, &result.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return SourceRegistration{}, ErrResourceNotFound
	}
	if err != nil {
		return SourceRegistration{}, fmt.Errorf("load source registration: %w", err)
	}
	if clusterID.Valid {
		result.ClusterID = uuid.UUID(clusterID.Bytes)
	}
	return result, nil
}

func loadCluster(ctx context.Context, tx pgx.Tx, tenantID, clusterID uuid.UUID, lock bool) (ClusterRegistration, error) {
	query := `SELECT tenant_id, cluster_id, cluster_uid, display_name, status, revision, created_at, updated_at
		FROM platform.cluster_registrations WHERE tenant_id = $1 AND cluster_id = $2`
	if lock {
		query += ` FOR UPDATE`
	}
	var result ClusterRegistration
	err := tx.QueryRow(ctx, query, tenantID, clusterID).Scan(
		&result.TenantID, &result.ClusterID, &result.ClusterUID, &result.DisplayName,
		&result.Status, &result.Revision, &result.CreatedAt, &result.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ClusterRegistration{}, ErrResourceNotFound
	}
	if err != nil {
		return ClusterRegistration{}, fmt.Errorf("load cluster registration: %w", err)
	}
	return result, nil
}

func listSources(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) ([]SourceRegistration, error) {
	rows, err := tx.Query(ctx, sourceSelect+` WHERE s.tenant_id = $1 ORDER BY s.source_type, s.instance_key, s.source_id`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list source registrations: %w", err)
	}
	defer rows.Close()
	result := make([]SourceRegistration, 0)
	for rows.Next() {
		var item SourceRegistration
		var clusterID pgtype.UUID
		if err := rows.Scan(
			&item.TenantID, &item.SourceID, &item.SourceType, &item.InstanceKey, &clusterID,
			&item.ClusterUID, &item.AuthRef, &item.CredentialRevision, &item.Status, &item.Revision,
			&item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan source registration: %w", err)
		}
		if clusterID.Valid {
			item.ClusterID = uuid.UUID(clusterID.Bytes)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read source registrations: %w", err)
	}
	return result, nil
}

func listClusters(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID) ([]ClusterRegistration, error) {
	rows, err := tx.Query(ctx, `
SELECT tenant_id, cluster_id, cluster_uid, display_name, status, revision, created_at, updated_at
FROM platform.cluster_registrations WHERE tenant_id = $1 ORDER BY cluster_uid, cluster_id`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list cluster registrations: %w", err)
	}
	defer rows.Close()
	result := make([]ClusterRegistration, 0)
	for rows.Next() {
		var item ClusterRegistration
		if err := rows.Scan(&item.TenantID, &item.ClusterID, &item.ClusterUID, &item.DisplayName,
			&item.Status, &item.Revision, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan cluster registration: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read cluster registrations: %w", err)
	}
	return result, nil
}
