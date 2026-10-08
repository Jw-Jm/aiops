// Package bootstrap holds controlled installation operations. It is not served
// by API/Worker and requires the separately held database bootstrap identity.
package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/audit"
	"ops-platform/internal/auth"
	"ops-platform/internal/tenant"
)

var ErrInitialized = errors.New("BOOTSTRAP_ALREADY_INITIALIZED")
var ErrIdentity = errors.New("BOOTSTRAP_IDENTITY_REQUIRED")

type FirstTenant struct {
	TenantID     uuid.UUID `json:"tenantId"`
	Slug         string    `json:"slug"`
	DisplayName  string    `json:"displayName"`
	AdminSubject string    `json:"adminSubject"`
}

func (c FirstTenant) Validate() error {
	if c.TenantID == uuid.Nil || strings.TrimSpace(c.AdminSubject) == "" || len(c.AdminSubject) > 512 {
		return ErrIdentity
	}
	return (tenant.CreateTenantInput{Slug: c.Slug, DisplayName: c.DisplayName}).Validate()
}

// ProvisionFirstTenant runs once on a globally empty migrated database. The
// caller must verify the exact OIDC identity before opening this privileged
// connection. Existing installation permissions can only change via the API.
func ProvisionFirstTenant(ctx context.Context, conn *pgx.Conn, c FirstTenant) (uuid.UUID, error) {
	if conn == nil {
		return uuid.Nil, ErrIdentity
	}
	if err := c.Validate(); err != nil {
		return uuid.Nil, err
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx)
	var privileged bool
	if err = tx.QueryRow(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname=session_user`).Scan(&privileged); err != nil || !privileged {
		return uuid.Nil, ErrIdentity
	}
	// SET ROLE cannot promote a runtime/migration login into a bootstrap identity.
	if _, err = tx.Exec(ctx, `RESET ROLE`); err != nil {
		return uuid.Nil, err
	}
	if _, err = tx.Exec(ctx, `LOCK TABLE platform.tenants IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return uuid.Nil, err
	}
	var anyTenant bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform.tenants)`).Scan(&anyTenant); err != nil {
		return uuid.Nil, err
	}
	if anyTenant {
		return uuid.Nil, ErrInitialized
	}
	if _, err = tx.Exec(ctx, `SELECT set_config('app.tenant_id',$1,true)`, c.TenantID.String()); err != nil {
		return uuid.Nil, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO platform.tenants(tenant_id,slug,display_name) VALUES($1,$2,$3)`, c.TenantID, c.Slug, c.DisplayName); err != nil {
		return uuid.Nil, fmt.Errorf("provision first tenant: %w", err)
	}
	binding := uuid.Must(uuid.NewV7())
	if _, err = tx.Exec(ctx, `INSERT INTO platform.role_bindings(tenant_id,binding_id,subject,role_name,cluster_scopes,namespace_scopes) VALUES($1,$2,$3,'platform_admin','[]','[]')`, c.TenantID, binding, c.AdminSubject); err != nil {
		return uuid.Nil, err
	}
	if _, err = audit.Append(ctx, tx, audit.Entry{TenantID: c.TenantID, RecordID: uuid.Must(uuid.NewV7()), EventType: "installation.first_tenant_bootstrapped", EntityKind: "tenant", EntityID: c.TenantID, Subject: c.AdminSubject, Payload: map[string]any{"tenant_id": c.TenantID.String(), "initial_admin_subject": c.AdminSubject, "initial_admin_role": string(auth.PlatformAdmin), "initial_admin_binding_id": binding.String(), "cluster_scopes": []string{}, "namespace_scopes": []string{}}}); err != nil {
		return uuid.Nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	return binding, nil
}
