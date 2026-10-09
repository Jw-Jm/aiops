package action

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"net"
	"ops-platform/internal/auth"
	"ops-platform/internal/persistence"
	"ops-platform/internal/resource"
	"regexp"
	"slices"
	"time"
)

// HostOnboarding is an immutable signed admission report. Native verification
// happens before admission; a caller-supplied assertion alone is insufficient.
type HostOnboarding struct {
	SchemaVersion string    `json:"schemaVersion"`
	Host          Host      `json:"host"`
	ClusterUID    string    `json:"clusterUid"`
	ObservedAt    time.Time `json:"observedAt"`
	ExpiresAt     time.Time `json:"expiresAt"`
	ReportDigest  string    `json:"reportDigest"`
}
type OnboardHostRequest struct {
	Report      HostOnboarding `json:"report"`
	SignerKeyID string         `json:"signerKeyId"`
	Signature   []byte         `json:"signature"`
}

func HostSigningPayload(tenant uuid.UUID, report HostOnboarding) []byte {
	return Canonical(struct {
		Domain string         `json:"domain"`
		Tenant uuid.UUID      `json:"tenantId"`
		Report HostOnboarding `json:"report"`
	}{"ops-host-onboarding/v1", tenant, report})
}
func (h HostOnboarding) Validate(tenant uuid.UUID) error {
	id, err := resource.ParseCanonicalID(h.Host.Target)
	if err != nil || id.Domain != "hardware" || id.Tenant != tenant.String() || id.Scope != h.ClusterUID || id.StableID != h.Host.UID || resource.NormalizeHardwareUUID(h.Host.UID) == "" || id.Kind != "System" || net.ParseIP(h.Host.Address) == nil || h.Host.Port < 1 || h.Host.Port > 65535 || h.Host.Principal == "" || h.Host.Role == "" || h.Host.UserCAPublicKey == "" || h.Host.KnownHosts == "" || h.Host.OnboardingRef != h.ReportDigest || h.SchemaVersion != "host-onboarding/v1" || !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(h.ReportDigest) || !h.ObservedAt.Before(h.ExpiresAt) || h.ObservedAt.After(time.Now()) || !time.Now().Before(h.ExpiresAt) {
		return ErrInvalid
	}
	return nil
}
func (s Service) OnboardHost(ctx context.Context, a auth.RequestContext, in OnboardHostRequest, key string) error {
	if !slices.Contains(a.Roles, auth.PlatformAdmin) || a.TenantID == uuid.Nil || !time.Now().Before(a.TokenExpiresAt) || s.AdmitHost == nil || s.Trust == nil {
		return ErrDenied
	}
	if err := in.Report.Validate(a.TenantID); err != nil {
		return err
	}
	if err := s.Trust.Verify(ctx, in.SignerKeyID, HostSigningPayload(a.TenantID, in.Report), in.Signature); err != nil {
		return ErrDenied
	}
	if err := s.AdmitHost(ctx, in.Report); err != nil {
		return err
	}
	return persistence.WithTenantTx(ctx, s.Pool, a.TenantID, func(tx pgx.Tx) error {
		var role uuid.UUID
		if tx.QueryRow(ctx, `SELECT binding_id FROM platform.role_bindings WHERE tenant_id=$1 AND subject=$2 AND role_name='platform_admin' AND status='active' FOR SHARE`, a.TenantID, a.Subject).Scan(&role) != nil {
			return ErrDenied
		}
		if _, err := auth.TouchCurrentStepUpSession(ctx, tx, a, []string{auth.StepUpACRLevel2}); err != nil {
			return err
		}
		d, err := persistence.Begin(ctx, tx, persistence.Scope{TenantID: a.TenantID, Subject: a.Subject, Operation: "onboard-host"}, key, persistence.Digest(Digest(Canonical(in))))
		if err != nil {
			return err
		}
		if d.Kind == persistence.DecisionReplay {
			return nil
		}
		if d.Kind != persistence.DecisionProceed {
			return ErrConflict
		}
		var accepted bool
		if err = tx.QueryRow(ctx, `SELECT action.onboard_host($1,$2,$3)`, a.TenantID, Canonical(in.Report), a.Subject).Scan(&accepted); err != nil {
			return err
		}
		if !accepted {
			return ErrConflict
		}
		if err = appendAudit(ctx, tx, a, uuid.Must(uuid.NewV7()), "host.onboarded", map[string]any{"target": in.Report.Host.Target, "reportDigest": in.Report.ReportDigest}); err != nil {
			return err
		}
		return complete(ctx, tx, d, in.Report, 201)
	})
}
func hostAdmittedTx(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, p Profile) error {
	if p.Type != "ssh_user" && p.Type != "ssh_root" {
		return nil
	}
	for _, target := range p.AllowedTargets {
		var raw []byte
		if tx.QueryRow(ctx, `SELECT report FROM action.host_onboardings WHERE tenant_id=$1 AND target=$2 AND principal=$3 AND report_digest=$4 AND expires_at>clock_timestamp()`, tenant, target, p.Principal, p.HostOnboardingRef).Scan(&raw) != nil {
			return ErrDenied
		}
		var report HostOnboarding
		if json.Unmarshal(raw, &report) != nil || report.Validate(tenant) != nil || report.Host.Role != p.CredentialRef {
			return ErrDenied
		}
	}
	return nil
}
