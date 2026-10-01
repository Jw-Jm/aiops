package source

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/audit"
	"ops-platform/internal/auth"
	"ops-platform/internal/persistence"
)

type ClusterRegistration struct {
	TenantID       uuid.UUID         `json:"tenantId"`
	ClusterID      uuid.UUID         `json:"clusterId"`
	ClusterUID     string            `json:"clusterUid"`
	DisplayName    string            `json:"displayName"`
	APIEndpointRef string            `json:"apiEndpointRef"`
	Distribution   string            `json:"distribution"`
	ActualVersions map[string]string `json:"actualVersions"`
	Capabilities   map[string]bool   `json:"capabilities"`
	Status         string            `json:"status"`
	Revision       int64             `json:"revision"`
	CreatedAt      time.Time         `json:"createdAt"`
	UpdatedAt      time.Time         `json:"updatedAt"`
}

type ClusterCommand struct {
	ExpectedRevision int64
	ClusterUID       string
	DisplayName      string
	APIEndpointRef   string
	Distribution     string
	ActualVersions   map[string]string
	Capabilities     map[string]bool
}

func (command ClusterCommand) Validate() error {
	if command.ExpectedRevision < 0 || !validOpaqueName(command.ClusterUID, 512) || !validDisplayName(command.DisplayName) || !validAuthRef(command.APIEndpointRef) || !validOpaqueName(command.Distribution, 64) || len(command.ActualVersions) == 0 || len(command.ActualVersions) > 64 || len(command.Capabilities) == 0 || len(command.Capabilities) > 64 || command.ActualVersions["kubernetes"] == "" {
		return ErrInvalidInput
	}
	keyPattern := regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{0,63}$`)
	versionPattern := regexp.MustCompile(`^v?[0-9]+\.[0-9]+(\.[0-9]+)?([-+][a-zA-Z0-9.-]+)?$`)
	for key, version := range command.ActualVersions {
		if !keyPattern.MatchString(key) || len(version) > 128 || !versionPattern.MatchString(version) {
			return ErrInvalidInput
		}
	}
	for key, enabled := range command.Capabilities {
		virtual := map[string]bool{"kubevirt": true, "cdi": true, "virtualization": true, "full": true, "vm": true, "vmi": true, "datavolume": true}
		if !keyPattern.MatchString(key) || (enabled && virtual[strings.ToLower(key)]) {
			return ErrInvalidInput
		}
	}
	return nil
}

func (service *Service) RegisterCluster(ctx context.Context, tx pgx.Tx, actor auth.RequestContext, command ClusterCommand) (ClusterRegistration, error) {
	if err := command.Validate(); err != nil {
		return ClusterRegistration{}, err
	}
	if err := authorizeAdmin(ctx, tx, actor); err != nil {
		return ClusterRegistration{}, err
	}
	if command.ExpectedRevision > 0 {
		return service.updateClusterMetadata(ctx, tx, actor, command)
	}
	clusterID := uuid.Must(uuid.NewV7())
	var created ClusterRegistration
	err := tx.QueryRow(ctx, `
INSERT INTO platform.cluster_registrations (tenant_id, cluster_id, cluster_uid, display_name, api_endpoint_ref, distribution, actual_versions, capabilities)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (tenant_id, cluster_uid) DO NOTHING
RETURNING tenant_id, cluster_id, cluster_uid, display_name, status, revision, created_at, updated_at, api_endpoint_ref, distribution, actual_versions, capabilities`,
		actor.TenantID, clusterID, command.ClusterUID, command.DisplayName, command.APIEndpointRef, command.Distribution, command.ActualVersions, command.Capabilities).
		Scan(&created.TenantID, &created.ClusterID, &created.ClusterUID, &created.DisplayName,
			&created.Status, &created.Revision, &created.CreatedAt, &created.UpdatedAt, &created.APIEndpointRef, &created.Distribution, &created.ActualVersions, &created.Capabilities)
	if errors.Is(err, pgx.ErrNoRows) {
		var existing ClusterRegistration
		err = tx.QueryRow(ctx, `SELECT tenant_id, cluster_id, cluster_uid, display_name, status, revision, created_at, updated_at, api_endpoint_ref, distribution, actual_versions, capabilities
			FROM platform.cluster_registrations WHERE tenant_id = $1 AND cluster_uid = $2`, actor.TenantID, command.ClusterUID).
			Scan(&existing.TenantID, &existing.ClusterID, &existing.ClusterUID, &existing.DisplayName,
				&existing.Status, &existing.Revision, &existing.CreatedAt, &existing.UpdatedAt, &existing.APIEndpointRef, &existing.Distribution, &existing.ActualVersions, &existing.Capabilities)
		if err != nil {
			return ClusterRegistration{}, fmt.Errorf("load duplicate cluster registration: %w", err)
		}
		if !sameClusterIdentity(existing, command) {
			return ClusterRegistration{}, ErrIdentityConflict
		}
		return existing, nil
	}
	if err != nil {
		return ClusterRegistration{}, fmt.Errorf("register cluster: %w", err)
	}
	if err := appendClusterRevision(ctx, tx, created, actor.Subject); err != nil {
		return ClusterRegistration{}, err
	}
	if _, err := audit.Append(ctx, tx, audit.Entry{
		TenantID: actor.TenantID, RecordID: uuid.Must(uuid.NewV7()), EventType: "cluster_registration.created",
		EntityKind: "cluster_registration", EntityID: created.ClusterID, Subject: actor.Subject,
		Payload: map[string]any{"cluster_uid": created.ClusterUID, "display_name": created.DisplayName, "revision": created.Revision, "api_endpoint_ref": created.APIEndpointRef, "distribution": created.Distribution, "actual_versions": created.ActualVersions, "capabilities": created.Capabilities},
	}); err != nil {
		return ClusterRegistration{}, fmt.Errorf("audit cluster registration: %w", err)
	}
	return created, nil
}

func (service *Service) updateClusterMetadata(ctx context.Context, tx pgx.Tx, actor auth.RequestContext, command ClusterCommand) (ClusterRegistration, error) {
	var id uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT cluster_id FROM platform.cluster_registrations WHERE tenant_id=$1 AND cluster_uid=$2`, actor.TenantID, command.ClusterUID).Scan(&id); errors.Is(err, pgx.ErrNoRows) {
		return ClusterRegistration{}, ErrResourceNotFound
	} else if err != nil {
		return ClusterRegistration{}, err
	}
	previous, err := loadCluster(ctx, tx, actor.TenantID, id, true)
	if err != nil {
		return ClusterRegistration{}, err
	}
	if previous.Revision != command.ExpectedRevision {
		return ClusterRegistration{}, ErrRevisionConflict
	}
	if previous.DisplayName != command.DisplayName {
		return ClusterRegistration{}, ErrIdentityConflict
	}
	if sameClusterIdentity(previous, command) {
		return previous, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE platform.cluster_registrations SET api_endpoint_ref=$1, distribution=$2, actual_versions=$3, capabilities=$4, revision=revision+1, updated_at=clock_timestamp() WHERE tenant_id=$5 AND cluster_id=$6`, command.APIEndpointRef, command.Distribution, command.ActualVersions, command.Capabilities, actor.TenantID, id); err != nil {
		return ClusterRegistration{}, err
	}
	updated, err := loadCluster(ctx, tx, actor.TenantID, id, false)
	if err != nil {
		return ClusterRegistration{}, err
	}
	if err := appendClusterRevision(ctx, tx, updated, actor.Subject); err != nil {
		return ClusterRegistration{}, err
	}
	if _, err := audit.Append(ctx, tx, audit.Entry{TenantID: actor.TenantID, RecordID: uuid.Must(uuid.NewV7()), EventType: "cluster_registration.metadata_updated", EntityKind: "cluster_registration", EntityID: id, Subject: actor.Subject, Payload: map[string]any{"previous_revision": previous.Revision, "revision": updated.Revision, "api_endpoint_ref": updated.APIEndpointRef, "distribution": updated.Distribution, "actual_versions": updated.ActualVersions, "capabilities": updated.Capabilities}}); err != nil {
		return ClusterRegistration{}, err
	}
	return updated, nil
}

func (service *Service) ListClusters(ctx context.Context, request auth.RequestContext) ([]ClusterRegistration, error) {
	if err := requireAdminContext(request); err != nil {
		return nil, err
	}
	var result []ClusterRegistration
	err := persistence.WithTenantTx(ctx, service.pool, request.TenantID, func(tx pgx.Tx) error {
		if err := authorizeAdmin(ctx, tx, request); err != nil {
			return err
		}
		var err error
		result, err = listClusters(ctx, tx, request.TenantID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("list current tenant clusters: %w", err)
	}
	return result, nil
}

func sameClusterIdentity(registered ClusterRegistration, command ClusterCommand) bool {
	return registered.ClusterUID == command.ClusterUID && registered.DisplayName == command.DisplayName && registered.APIEndpointRef == command.APIEndpointRef && registered.Distribution == command.Distribution && maps.Equal(registered.ActualVersions, command.ActualVersions) && maps.Equal(registered.Capabilities, command.Capabilities)
}
