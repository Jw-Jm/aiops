package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"ops-platform/internal/audit"
)

// ErrTenantExists is returned when the requested first-tenant slug is already
// present. Bootstrapping never adopts or rewrites an existing tenant.
var ErrTenantExists = errors.New("tenant slug is already initialized")

var firstTenantSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)

// FirstTenantInput creates a tenant and its first native administrator in one
// controlled bootstrap transaction. It is the native cold-start path: no OIDC
// realm, issuer or bearer token is involved.
type FirstTenantInput struct {
	Slug             string
	DisplayName      string
	AdminLoginName   string
	AdminDisplayName string
	Password         string
}

// MarshalJSON never encodes the initial password.
func (input FirstTenantInput) MarshalJSON() ([]byte, error) {
	type safeFirstTenantInput struct {
		Slug             string `json:"slug"`
		DisplayName      string `json:"displayName"`
		AdminLoginName   string `json:"adminLoginName"`
		AdminDisplayName string `json:"adminDisplayName"`
		PasswordPresent  bool   `json:"passwordPresent"`
	}
	return json.Marshal(safeFirstTenantInput{
		Slug: input.Slug, DisplayName: input.DisplayName,
		AdminLoginName: input.AdminLoginName, AdminDisplayName: input.AdminDisplayName,
		PasswordPresent: input.Password != "",
	})
}

type FirstTenantResult struct {
	TenantID      uuid.UUID
	Administrator FirstAdministratorResult
}

// Validate rejects empty or malformed input before any transaction starts.
func (input FirstTenantInput) Validate() error {
	if !firstTenantSlugPattern.MatchString(input.Slug) {
		return ErrTenantUnavailable
	}
	display := strings.TrimSpace(input.DisplayName)
	if display == "" || len(display) > 200 {
		return ErrTenantUnavailable
	}
	admin := FirstAdministratorInput{
		TenantSlug: input.Slug, LoginName: NormalizeLoginName(input.AdminLoginName),
		DisplayName: strings.TrimSpace(input.AdminDisplayName), Password: input.Password,
	}
	return admin.Validate()
}

// CreateFirstTenant installs the tenant and its first administrator with the
// dedicated migration-duty LOGIN. The tenant row, the account, its credential,
// the platform_admin grant, the initialization marker and both audit records
// commit atomically; a loser of a concurrent attempt leaves nothing behind.
func (r *Repository) CreateFirstTenant(ctx context.Context, input FirstTenantInput) (FirstTenantResult, error) {
	var result FirstTenantResult
	if r == nil || r.pool == nil {
		return result, ErrRepositoryRequired
	}
	input.Slug = strings.ToLower(strings.TrimSpace(input.Slug))
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.AdminLoginName = NormalizeLoginName(input.AdminLoginName)
	input.AdminDisplayName = strings.TrimSpace(input.AdminDisplayName)
	if err := input.Validate(); err != nil {
		return result, err
	}
	admin := FirstAdministratorInput{
		TenantSlug: input.Slug, LoginName: input.AdminLoginName,
		DisplayName: input.AdminDisplayName, Password: input.Password,
	}
	passwordHash, err := HashPassword(input.Password)
	if err != nil {
		return result, err
	}
	userID, err := uuid.NewV7()
	if err != nil {
		return result, fmt.Errorf("generate user id: %w", err)
	}
	subject := "user:" + userID.String()
	if err := validateSubject(subject); err != nil {
		return result, err
	}
	err = r.withBootstrapDuty(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('identity:first-tenant:' || $1, 0))`, input.Slug); err != nil {
			return fmt.Errorf("lock first tenant bootstrap: %w", err)
		}
		var existing uuid.UUID
		err := tx.QueryRow(ctx, `SELECT tenant_id FROM platform.tenants WHERE slug=$1`, input.Slug).Scan(&existing)
		if err == nil {
			return ErrTenantExists
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("check first tenant slug: %w", err)
		}
		tenantID, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("generate tenant id: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO platform.tenants(tenant_id,slug,display_name) VALUES($1,$2,$3)`, tenantID, input.Slug, input.DisplayName); err != nil {
			return fmt.Errorf("create first tenant: %w", err)
		}
		var installed string
		if err := tx.QueryRow(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tenantID.String()).Scan(&installed); err != nil || installed != tenantID.String() {
			return fmt.Errorf("set first tenant context: %w", err)
		}
		var tenantRevision int64
		if err := tx.QueryRow(ctx, `SELECT revision FROM platform.tenants WHERE tenant_id=$1`, tenantID).Scan(&tenantRevision); err != nil {
			return fmt.Errorf("read first tenant revision: %w", err)
		}
		recordID, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("generate first tenant audit id: %w", err)
		}
		if _, err = audit.Append(ctx, tx, audit.Entry{
			TenantID: tenantID, RecordID: recordID,
			EventType: "identity.first_tenant.initialized", EntityKind: "tenant", EntityID: tenantID,
			Subject: bootstrapActor,
			Payload: map[string]any{
				"slug": input.Slug, "displayName": input.DisplayName,
				"status": "active", "revision": tenantRevision,
			},
		}); err != nil {
			return err
		}
		result.TenantID = tenantID
		return createFirstAdministratorTx(ctx, tx, tenantID, admin, userID, subject, passwordHash, &result.Administrator)
	})
	if err != nil {
		return FirstTenantResult{}, err
	}
	return result, nil
}
