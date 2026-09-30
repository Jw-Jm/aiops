package configregistry

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func loadDraft(ctx context.Context, tx pgx.Tx, tenantID, draftID uuid.UUID, lock bool) (Draft, error) {
	query := `SELECT tenant_id, draft_id, kind, logical_name, revision, content, status, updated_by, created_at, updated_at
		FROM platform.registry_drafts WHERE tenant_id = $1 AND draft_id = $2`
	if lock {
		query += ` FOR UPDATE`
	}
	var value Draft
	var content []byte
	err := tx.QueryRow(ctx, query, tenantID, draftID).Scan(&value.TenantID, &value.DraftID, &value.Kind,
		&value.LogicalName, &value.Revision, &content, &value.Status, &value.UpdatedBy, &value.CreatedAt, &value.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Draft{}, ErrNotFound
	}
	if err != nil {
		return Draft{}, fmt.Errorf("load registry draft: %w", err)
	}
	value.Content = append(value.Content[:0], content...)
	return value, nil
}

func loadVersion(ctx context.Context, tx pgx.Tx, tenantID, versionID uuid.UUID, lock bool) (PublishedVersion, error) {
	query := `SELECT tenant_id, version_id, draft_id, kind, logical_name, version_number, content, digest,
		signature, signer_key_id, published_by, published_at, retired_at
		FROM platform.registry_versions WHERE tenant_id = $1 AND version_id = $2`
	if lock {
		query += ` FOR UPDATE`
	}
	var value PublishedVersion
	var content []byte
	err := tx.QueryRow(ctx, query, tenantID, versionID).Scan(&value.TenantID, &value.VersionID, &value.DraftID,
		&value.Kind, &value.LogicalName, &value.VersionNumber, &content, &value.Digest, &value.Signature,
		&value.SignerKeyID, &value.PublishedBy, &value.PublishedAt, &value.RetiredAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return PublishedVersion{}, ErrNotFound
	}
	if err != nil {
		return PublishedVersion{}, fmt.Errorf("load registry version: %w", err)
	}
	value.Content = append(value.Content[:0], content...)
	return value, nil
}

func listVersions(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, kind Kind) ([]PublishedVersion, error) {
	rows, err := tx.Query(ctx, `SELECT tenant_id, version_id, draft_id, kind, logical_name, version_number, content,
		digest, signature, signer_key_id, published_by, published_at, retired_at
		FROM platform.registry_versions WHERE tenant_id = $1 AND kind = $2
		ORDER BY logical_name, version_number DESC, version_id`, tenantID, kind)
	if err != nil {
		return nil, fmt.Errorf("list registry versions: %w", err)
	}
	defer rows.Close()
	result := make([]PublishedVersion, 0)
	for rows.Next() {
		var value PublishedVersion
		var content []byte
		if err := rows.Scan(&value.TenantID, &value.VersionID, &value.DraftID, &value.Kind, &value.LogicalName,
			&value.VersionNumber, &content, &value.Digest, &value.Signature, &value.SignerKeyID,
			&value.PublishedBy, &value.PublishedAt, &value.RetiredAt); err != nil {
			return nil, fmt.Errorf("scan registry version: %w", err)
		}
		value.Content = append(value.Content[:0], content...)
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read registry versions: %w", err)
	}
	return result, nil
}

type activationRow struct {
	ActivationID uuid.UUID
	VersionID    uuid.UUID
	Revision     int64
	Type         ScopeType
	ClusterID    uuid.UUID
	Namespace    string
	ActivatedBy  string
	ActivatedAt  time.Time
}

func loadActivation(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, kind Kind, logicalName string, scope Scope, lock bool) (activationRow, error) {
	query := `SELECT activation_id, version_id, revision, scope_type, cluster_id, namespace, activated_by, activated_at
		FROM platform.registry_activations WHERE tenant_id = $1 AND kind = $2 AND logical_name = $3
		AND scope_type = $4 AND cluster_id IS NOT DISTINCT FROM $5 AND namespace IS NOT DISTINCT FROM $6`
	if lock {
		query += ` FOR UPDATE`
	}
	var value activationRow
	var clusterID pgtype.UUID
	var namespace pgtype.Text
	err := tx.QueryRow(ctx, query, tenantID, kind, logicalName, scope.Type, nullableUUID(scope.ClusterID), nullableText(scope.Namespace)).
		Scan(&value.ActivationID, &value.VersionID, &value.Revision, &value.Type, &clusterID, &namespace, &value.ActivatedBy, &value.ActivatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return activationRow{}, ErrNotFound
	}
	if err != nil {
		return activationRow{}, fmt.Errorf("load registry activation: %w", err)
	}
	if clusterID.Valid {
		value.ClusterID = uuid.UUID(clusterID.Bytes)
	}
	if namespace.Valid {
		value.Namespace = namespace.String
	}
	return value, nil
}

func nullableUUID(value uuid.UUID) any {
	if value == uuid.Nil {
		return nil
	}
	return value
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}
