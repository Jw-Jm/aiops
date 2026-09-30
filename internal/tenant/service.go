package tenant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/audit"
	"ops-platform/internal/auth"
	"ops-platform/internal/persistence"
)

type Service struct {
	repository *Repository
}

func NewService(pool persistence.TxBeginner) (*Service, error) {
	repository, err := NewRepository(pool)
	if err != nil {
		return nil, err
	}
	return &Service{repository: repository}, nil
}

func (s *Service) GetCurrentTenant(ctx context.Context, request auth.RequestContext) (Tenant, error) {
	return s.repository.GetCurrentTenant(ctx, request)
}

func (s *Service) ListCurrentRoleBindings(ctx context.Context, request auth.RequestContext) ([]RoleBinding, error) {
	return s.repository.ListCurrentRoleBindings(ctx, request)
}

func (s *Service) CreateTenant(ctx context.Context, tx pgx.Tx, actor auth.RequestContext, input CreateTenantInput) (Tenant, error) {
	if err := input.Validate(); err != nil {
		return Tenant{}, err
	}
	if err := authorizeAdmin(ctx, tx, actor); err != nil {
		return Tenant{}, err
	}
	tenantID, bindingID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	var created Tenant
	var initialBindingID uuid.UUID
	err := tx.QueryRow(ctx, "SELECT created_tenant_id, created_slug, created_display_name, created_status, created_revision, created_at, updated_at, initial_binding_id "+
		"FROM platform.provision_tenant($1, $2, $3, $4, $5, $6)",
		actor.TenantID, actor.Subject, tenantID, bindingID, input.Slug, input.DisplayName).
		Scan(&created.ID, &created.Slug, &created.DisplayName, &created.Status, &created.Revision, &created.CreatedAt, &created.UpdatedAt, &initialBindingID)
	if err != nil {
		return Tenant{}, fmt.Errorf("provision tenant: %w", err)
	}
	if _, err := audit.Append(ctx, tx, audit.Entry{
		TenantID: actor.TenantID, RecordID: uuid.Must(uuid.NewV7()), EventType: "tenant.provisioned",
		EntityKind: "tenant", EntityID: created.ID, Subject: actor.Subject,
		Payload: map[string]any{
			"tenant_id": created.ID.String(), "slug": created.Slug, "display_name": created.DisplayName,
			"initial_admin_subject": actor.Subject, "initial_admin_binding_id": initialBindingID.String(),
			"initial_admin_role": string(auth.PlatformAdmin), "revision": created.Revision,
		},
	}); err != nil {
		return Tenant{}, fmt.Errorf("audit tenant provisioning: %w", err)
	}
	return created, nil
}

func (s *Service) UpdateCurrentTenant(ctx context.Context, tx pgx.Tx, actor auth.RequestContext, expectedRevision int64, displayName, status string) (Tenant, error) {
	if expectedRevision < 1 || len(displayName) < 1 || len(displayName) > 200 ||
		(status != "active" && status != "disabled") {
		return Tenant{}, ErrInvalidInput
	}
	if err := authorizeAdmin(ctx, tx, actor); err != nil {
		return Tenant{}, err
	}
	var previous Tenant
	err := tx.QueryRow(ctx, "SELECT tenant_id, slug, display_name, status, revision, created_at, updated_at "+
		"FROM platform.tenants WHERE tenant_id = $1", actor.TenantID).
		Scan(&previous.ID, &previous.Slug, &previous.DisplayName, &previous.Status, &previous.Revision, &previous.CreatedAt, &previous.UpdatedAt)
	if err != nil {
		return Tenant{}, fmt.Errorf("load tenant before update: %w", err)
	}
	var updated Tenant
	err = tx.QueryRow(ctx, "SELECT updated_tenant_id, updated_slug, updated_display_name, updated_status, updated_revision, created_at, updated_at "+
		"FROM platform.update_tenant($1, $2, $3, $4, $5)",
		actor.TenantID, actor.Subject, expectedRevision, displayName, status).
		Scan(&updated.ID, &updated.Slug, &updated.DisplayName, &updated.Status, &updated.Revision, &updated.CreatedAt, &updated.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Tenant{}, ErrRevisionConflict
	}
	if err != nil {
		return Tenant{}, fmt.Errorf("update tenant: %w", err)
	}
	if _, err := audit.Append(ctx, tx, audit.Entry{
		TenantID: actor.TenantID, RecordID: uuid.Must(uuid.NewV7()), EventType: "tenant.updated",
		EntityKind: "tenant", EntityID: updated.ID, Subject: actor.Subject,
		Payload: map[string]any{
			"before": map[string]any{"display_name": previous.DisplayName, "status": previous.Status, "revision": previous.Revision},
			"after":  map[string]any{"display_name": updated.DisplayName, "status": updated.Status, "revision": updated.Revision},
		},
	}); err != nil {
		return Tenant{}, fmt.Errorf("audit tenant update: %w", err)
	}
	return updated, nil
}

func (s *Service) CreateRoleBinding(ctx context.Context, tx pgx.Tx, actor auth.RequestContext, input CreateRoleBindingInput) (RoleBinding, error) {
	if err := input.Validate(); err != nil {
		return RoleBinding{}, err
	}
	if err := authorizeAdmin(ctx, tx, actor); err != nil {
		return RoleBinding{}, err
	}
	if err := validateClusterScopes(ctx, tx, actor.TenantID, input.ClusterScopes); err != nil {
		return RoleBinding{}, err
	}
	clusterJSON, err := json.Marshal(input.ClusterScopes)
	if err != nil {
		return RoleBinding{}, err
	}
	namespaceJSON, err := json.Marshal(input.NamespaceScopes)
	if err != nil {
		return RoleBinding{}, err
	}
	bindingID := uuid.Must(uuid.NewV7())
	var created RoleBinding
	err = tx.QueryRow(ctx, "INSERT INTO platform.role_bindings "+
		"(tenant_id, binding_id, subject, role_name, cluster_scopes, namespace_scopes) "+
		"VALUES ($1, $2, $3, $4, $5::jsonb, $6::jsonb) "+
		"RETURNING binding_id, tenant_id, subject, role_name, cluster_scopes, namespace_scopes, status, revision, created_at, updated_at",
		actor.TenantID, bindingID, input.Subject, input.Role, clusterJSON, namespaceJSON).
		Scan(&created.ID, &created.TenantID, &created.Subject, &created.Role, &clusterJSON, &namespaceJSON,
			&created.Status, &created.Revision, &created.CreatedAt, &created.UpdatedAt)
	if err != nil {
		return RoleBinding{}, fmt.Errorf("create role binding: %w", err)
	}
	created.ClusterScopes, created.NamespaceScopes = input.ClusterScopes, input.NamespaceScopes
	if _, err := audit.Append(ctx, tx, audit.Entry{
		TenantID: actor.TenantID, RecordID: uuid.Must(uuid.NewV7()), EventType: "role_binding.created",
		EntityKind: "role_binding", EntityID: created.ID, Subject: actor.Subject,
		Payload: map[string]any{
			"target_subject": created.Subject, "role": string(created.Role),
			"cluster_scopes": created.ClusterScopes, "namespace_scopes": created.NamespaceScopes,
			"status": created.Status, "revision": created.Revision,
		},
	}); err != nil {
		return RoleBinding{}, fmt.Errorf("audit role binding creation: %w", err)
	}
	return created, nil
}

func (s *Service) UpdateRoleBinding(ctx context.Context, tx pgx.Tx, actor auth.RequestContext, bindingID uuid.UUID, expectedRevision int64, input CreateRoleBindingInput, status string) (RoleBinding, error) {
	if bindingID == uuid.Nil || expectedRevision < 1 || (status != "active" && status != "disabled") {
		return RoleBinding{}, ErrInvalidInput
	}
	if err := input.Validate(); err != nil {
		return RoleBinding{}, err
	}
	if err := authorizeAdmin(ctx, tx, actor); err != nil {
		return RoleBinding{}, err
	}
	if err := validateClusterScopes(ctx, tx, actor.TenantID, input.ClusterScopes); err != nil {
		return RoleBinding{}, err
	}
	var previous RoleBinding
	var previousClusters, previousNamespaces []byte
	err := tx.QueryRow(ctx, "SELECT binding_id, tenant_id, subject, role_name, cluster_scopes, namespace_scopes, status, revision, created_at, updated_at "+
		"FROM platform.role_bindings WHERE tenant_id = $1 AND binding_id = $2", actor.TenantID, bindingID).
		Scan(&previous.ID, &previous.TenantID, &previous.Subject, &previous.Role, &previousClusters, &previousNamespaces,
			&previous.Status, &previous.Revision, &previous.CreatedAt, &previous.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return RoleBinding{}, ErrResourceNotFound
	}
	if err != nil {
		return RoleBinding{}, fmt.Errorf("load role binding before update: %w", err)
	}
	if err := json.Unmarshal(previousClusters, &previous.ClusterScopes); err != nil {
		return RoleBinding{}, fmt.Errorf("decode previous cluster scopes: %w", err)
	}
	if err := json.Unmarshal(previousNamespaces, &previous.NamespaceScopes); err != nil {
		return RoleBinding{}, fmt.Errorf("decode previous namespace scopes: %w", err)
	}
	clusterJSON, err := json.Marshal(input.ClusterScopes)
	if err != nil {
		return RoleBinding{}, fmt.Errorf("encode cluster scopes: %w", err)
	}
	namespaceJSON, err := json.Marshal(input.NamespaceScopes)
	if err != nil {
		return RoleBinding{}, fmt.Errorf("encode namespace scopes: %w", err)
	}
	var updated RoleBinding
	err = tx.QueryRow(ctx, "UPDATE platform.role_bindings "+
		"SET subject = $1, role_name = $2, cluster_scopes = $3::jsonb, namespace_scopes = $4::jsonb, status = $5, revision = revision + 1, updated_at = clock_timestamp() "+
		"WHERE tenant_id = $6 AND binding_id = $7 AND revision = $8 "+
		"RETURNING binding_id, tenant_id, subject, role_name, cluster_scopes, namespace_scopes, status, revision, created_at, updated_at",
		input.Subject, input.Role, clusterJSON, namespaceJSON, status, actor.TenantID, bindingID, expectedRevision).
		Scan(&updated.ID, &updated.TenantID, &updated.Subject, &updated.Role, &clusterJSON, &namespaceJSON,
			&updated.Status, &updated.Revision, &updated.CreatedAt, &updated.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return RoleBinding{}, ErrRevisionConflict
	}
	if err != nil {
		return RoleBinding{}, fmt.Errorf("update role binding: %w", err)
	}
	updated.ClusterScopes, updated.NamespaceScopes = input.ClusterScopes, input.NamespaceScopes
	if _, err := audit.Append(ctx, tx, audit.Entry{
		TenantID: actor.TenantID, RecordID: uuid.Must(uuid.NewV7()), EventType: "role_binding.updated",
		EntityKind: "role_binding", EntityID: updated.ID, Subject: actor.Subject,
		Payload: map[string]any{
			"before": map[string]any{
				"target_subject": previous.Subject, "role": string(previous.Role), "status": previous.Status,
				"revision": previous.Revision, "cluster_scopes": previous.ClusterScopes, "namespace_scopes": previous.NamespaceScopes,
			},
			"after": map[string]any{
				"target_subject": updated.Subject, "role": string(updated.Role), "status": updated.Status,
				"revision": updated.Revision, "cluster_scopes": updated.ClusterScopes, "namespace_scopes": updated.NamespaceScopes,
			},
		},
	}); err != nil {
		return RoleBinding{}, fmt.Errorf("audit role binding update: %w", err)
	}
	return updated, nil
}

func validateClusterScopes(ctx context.Context, tx pgx.Tx, tenantID uuid.UUID, clusterIDs []uuid.UUID) error {
	for _, clusterID := range clusterIDs {
		var exists bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM platform.cluster_registrations WHERE tenant_id = $1 AND cluster_id = $2 AND status = 'active')", tenantID, clusterID).Scan(&exists); err != nil {
			return fmt.Errorf("validate role binding cluster scope: %w", err)
		}
		if !exists {
			return fmt.Errorf("%w: cluster scope does not exist in this tenant", ErrInvalidInput)
		}
	}
	return nil
}
