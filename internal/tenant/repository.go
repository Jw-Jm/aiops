package tenant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/auth"
	"ops-platform/internal/persistence"
)

type Repository struct {
	pool persistence.TxBeginner
}

func NewRepository(pool persistence.TxBeginner) (*Repository, error) {
	if pool == nil {
		return nil, errors.New("tenant database pool is required")
	}
	return &Repository{pool: pool}, nil
}

func (r *Repository) GetCurrentTenant(ctx context.Context, request auth.RequestContext) (Tenant, error) {
	if err := requireAdminContext(request); err != nil {
		return Tenant{}, err
	}
	var result Tenant
	err := persistence.WithTenantTx(ctx, r.pool, request.TenantID, func(tx pgx.Tx) error {
		if err := authorizeAdmin(ctx, tx, request); err != nil {
			return err
		}
		return tx.QueryRow(ctx, "SELECT tenant_id, slug, display_name, status, revision, created_at, updated_at "+
			"FROM platform.tenants WHERE tenant_id = $1", request.TenantID).
			Scan(&result.ID, &result.Slug, &result.DisplayName, &result.Status, &result.Revision, &result.CreatedAt, &result.UpdatedAt)
	})
	if err != nil {
		return Tenant{}, fmt.Errorf("load current tenant: %w", err)
	}
	return result, nil
}

func (r *Repository) ListCurrentRoleBindings(ctx context.Context, request auth.RequestContext) ([]RoleBinding, error) {
	if err := requireAdminContext(request); err != nil {
		return nil, err
	}
	var result []RoleBinding
	err := persistence.WithTenantTx(ctx, r.pool, request.TenantID, func(tx pgx.Tx) error {
		if err := authorizeAdmin(ctx, tx, request); err != nil {
			return err
		}
		rows, err := tx.Query(ctx,
			"SELECT binding_id, tenant_id, subject, role_name, cluster_scopes, namespace_scopes, status, revision, created_at, updated_at "+
				"FROM platform.role_bindings WHERE tenant_id = $1 ORDER BY subject, role_name, binding_id",
			request.TenantID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var binding RoleBinding
			var clustersJSON, namespacesJSON []byte
			if err := rows.Scan(&binding.ID, &binding.TenantID, &binding.Subject, &binding.Role, &clustersJSON,
				&namespacesJSON, &binding.Status, &binding.Revision, &binding.CreatedAt, &binding.UpdatedAt); err != nil {
				return err
			}
			if err := json.Unmarshal(clustersJSON, &binding.ClusterScopes); err != nil {
				return fmt.Errorf("decode role binding cluster scopes: %w", err)
			}
			if err := json.Unmarshal(namespacesJSON, &binding.NamespaceScopes); err != nil {
				return fmt.Errorf("decode role binding namespace scopes: %w", err)
			}
			result = append(result, binding)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("list current tenant role bindings: %w", err)
	}
	return result, nil
}

func authorizeAdmin(ctx context.Context, tx pgx.Tx, request auth.RequestContext) error {
	if err := requireAdminContext(request); err != nil {
		return err
	}
	var active bool
	if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM platform.tenants WHERE tenant_id = $1 AND status = 'active') "+
		"AND EXISTS (SELECT 1 FROM platform.role_bindings WHERE tenant_id = $1 AND subject = $2 AND role_name = 'platform_admin' AND status = 'active')",
		request.TenantID, request.Subject).Scan(&active); err != nil {
		return fmt.Errorf("verify tenant administrator: %w", err)
	}
	if !active {
		return ErrUnauthorized
	}
	return nil
}

func requireAdminContext(request auth.RequestContext) error {
	if request.TenantID == uuid.Nil || request.Subject == "" ||
		!auth.HasRole(auth.WithRequestContext(context.Background(), request), auth.PlatformAdmin) {
		return ErrUnauthorized
	}
	return nil
}
