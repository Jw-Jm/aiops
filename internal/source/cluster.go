package source

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/audit"
	"ops-platform/internal/auth"
	"ops-platform/internal/persistence"
)

type ClusterRegistration struct {
	TenantID    uuid.UUID `json:"tenantId"`
	ClusterID   uuid.UUID `json:"clusterId"`
	ClusterUID  string    `json:"clusterUid"`
	DisplayName string    `json:"displayName"`
	Status      string    `json:"status"`
	Revision    int64     `json:"revision"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type ClusterCommand struct {
	ClusterUID  string
	DisplayName string
}

func (command ClusterCommand) Validate() error {
	if !validOpaqueName(command.ClusterUID, 512) || !validDisplayName(command.DisplayName) {
		return ErrInvalidInput
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
	clusterID := uuid.Must(uuid.NewV7())
	var created ClusterRegistration
	err := tx.QueryRow(ctx, `
INSERT INTO platform.cluster_registrations (tenant_id, cluster_id, cluster_uid, display_name)
VALUES ($1, $2, $3, $4)
ON CONFLICT (tenant_id, cluster_uid) DO NOTHING
RETURNING tenant_id, cluster_id, cluster_uid, display_name, status, revision, created_at, updated_at`,
		actor.TenantID, clusterID, command.ClusterUID, command.DisplayName).
		Scan(&created.TenantID, &created.ClusterID, &created.ClusterUID, &created.DisplayName,
			&created.Status, &created.Revision, &created.CreatedAt, &created.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		var existing ClusterRegistration
		err = tx.QueryRow(ctx, `SELECT tenant_id, cluster_id, cluster_uid, display_name, status, revision, created_at, updated_at
			FROM platform.cluster_registrations WHERE tenant_id = $1 AND cluster_uid = $2`, actor.TenantID, command.ClusterUID).
			Scan(&existing.TenantID, &existing.ClusterID, &existing.ClusterUID, &existing.DisplayName,
				&existing.Status, &existing.Revision, &existing.CreatedAt, &existing.UpdatedAt)
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
		Payload: map[string]any{"cluster_uid": created.ClusterUID, "display_name": created.DisplayName, "revision": created.Revision},
	}); err != nil {
		return ClusterRegistration{}, fmt.Errorf("audit cluster registration: %w", err)
	}
	return created, nil
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
	return registered.ClusterUID == command.ClusterUID && registered.DisplayName == command.DisplayName
}
