package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"ops-platform/internal/archive"
	"ops-platform/internal/audit"
	platformcrypto "ops-platform/internal/crypto"
	"ops-platform/internal/evidence"
	"ops-platform/internal/integrations/openbao"
	"ops-platform/internal/integrations/s3"
	"ops-platform/internal/observability"
	"ops-platform/internal/persistence"
)

// SealAuditPass visits every tenant under its own SET LOCAL boundary. One
// unavailable tenant/segment cannot prevent the other tenants from progressing.
func SealAuditPass(ctx context.Context, pool *pgxpool.Pool, service *audit.SegmentService, metrics *observability.Metrics) error {
	rows, err := pool.Query(ctx, `SELECT tenant_id FROM platform.tenants ORDER BY tenant_id`)
	if err != nil {
		return errors.New("audit tenant enumeration failed")
	}
	var tenants []uuid.UUID
	for rows.Next() {
		var tenant uuid.UUID
		if err := rows.Scan(&tenant); err != nil {
			rows.Close()
			return err
		}
		tenants = append(tenants, tenant)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	var passErr error
	var maxDelay float64
	for _, tenant := range tenants {
		for batch := 0; batch < 32; batch++ {
			_, worked, err := service.SealNext(ctx, tenant)
			if err != nil {
				passErr = fmt.Errorf("audit signing or archive unavailable: %w", err)
				break
			}
			if !worked {
				break
			}
		}
		err := persistence.WithTenantTx(ctx, pool, tenant, func(tx pgx.Tx) error {
			var delay float64
			if err := tx.QueryRow(ctx, `SELECT COALESCE(EXTRACT(EPOCH FROM clock_timestamp()-min(created_at)),0)::double precision FROM audit.records WHERE audit_seq > COALESCE((SELECT max(last_audit_seq) FROM audit.signed_segments WHERE status='signed'),0)`).Scan(&delay); err != nil {
				return err
			}
			if delay > maxDelay {
				maxDelay = delay
			}
			return nil
		})
		if err != nil {
			passErr = errors.New("audit backlog measurement failed")
		}
	}
	metrics.SetAuditSigningDelay(maxDelay)
	return passErr
}

func (application *WorkerApp) Serve(ctx context.Context, runtime *observability.Runtime) error {
	if runtime == nil {
		return errors.New("worker observability is required")
	}
	if err := application.config.validateRuntimeProfile(true); err != nil {
		return err
	}
	pool, err := OpenRuntimePool(ctx, application.config.DatabaseURL, "worker_runtime_role")
	if err != nil {
		return err
	}
	defer pool.Close()
	ca, err := os.ReadFile(os.Getenv("OPENBAO_CA_FILE"))
	if err != nil {
		return errors.New("worker OpenBao CA is unavailable")
	}
	bao, err := openbao.NewClient(openbao.ClientConfig{Address: os.Getenv("OPENBAO_ADDR"), CACertBundle: ca, ServiceDomain: os.Getenv("OPENBAO_SERVICE_DOMAIN")})
	if err != nil {
		return errors.New("worker OpenBao configuration is invalid")
	}
	var archiveCA []byte
	if path := os.Getenv("S3_CA_FILE"); path != "" {
		archiveCA, err = os.ReadFile(path)
		if err != nil {
			return errors.New("worker independent archive CA is unavailable")
		}
	}
	backend, err := s3.LoadTenantClient(s3.Config{Endpoint: os.Getenv("S3_ENDPOINT"), Bucket: os.Getenv("S3_BUCKET"), CACertBundle: archiveCA}, os.Getenv("S3_TENANT_CREDENTIALS_FILE"))
	if err != nil {
		return errors.New("worker archive configuration is invalid")
	}
	if os.Getenv("SP04_RUNTIME_FILE") != "" && !backend.RoleSeparated() {
		return errors.New("SP04 archive requires separated v2 write/read/protect/cleanup credentials")
	}
	store, err := archive.NewStore(backend, 0)
	if err != nil {
		return err
	}
	protector, err := platformcrypto.NewTransitProtector(bao, "evidence-archive")
	if err != nil {
		return err
	}
	service, err := audit.NewSegmentService(pool, store, protector, bao, "audit-signing", audit.DefaultArchiveRetention)
	if err != nil {
		return err
	}
	tokenPath := os.Getenv("OPENBAO_PROJECTED_TOKEN_FILE")
	evidenceArchive := &evidence.ArchiveService{Pool: pool, Store: store, Protector: protector}
	stopSP04, err := StartSP04Worker(ctx, pool, evidenceArchive, runtime)
	if err != nil {
		return err
	}
	defer stopSP04()
	stopSP06, err := StartSP06Worker(ctx, pool)
	if err != nil {
		return err
	}
	defer stopSP06()
	stopSP07, err := StartSP07Worker(ctx, pool, store, evidenceArchive)
	if err != nil {
		return err
	}
	defer stopSP07()
	if tokenPath == "" {
		tokenPath = "/var/run/secrets/ops-platform/openbao/token"
	}
	var renewAt time.Time
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return nil
		}
		passContext, cancel := context.WithTimeout(ctx, 45*time.Second)
		var passErr error
		if !time.Now().Before(renewAt) {
			ttl, loginErr := bao.LoginProjectedServiceAccount(passContext, tokenPath, "ops-worker")
			if loginErr != nil {
				passErr = loginErr
			} else {
				renewAt = time.Now().Add(ttl * 2 / 3)
			}
		}
		if passErr == nil {
			passErr = SealAuditPass(passContext, pool, service, runtime.Metrics)
		}
		cancel()
		runtime.Metrics.SetComponentDegraded("audit_signing", passErr != nil)
		if passErr != nil {
			runtime.Logger.ErrorContext(ctx, "audit signature pass failed; retry scheduled", "operation", "audit_signing", "failure_code", auditFailureCode(passErr))
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// Retain the cause for callers while logging only a bounded, nonsensitive code.
func auditFailureCode(err error) string {
	if cause := errors.Unwrap(err); cause != nil {
		err = cause
	}
	message := err.Error()
	switch {
	case strings.Contains(message, "S3") || strings.Contains(message, "archive"):
		return "archive"
	case strings.Contains(message, "Transit") || strings.Contains(message, "encryption"):
		return "transit"
	case strings.Contains(message, "signing") || strings.Contains(message, "signature"):
		return "signature"
	default:
		return "database"
	}
}
