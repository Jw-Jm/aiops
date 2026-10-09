package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"log/slog"
	"ops-platform/internal/action"
	"ops-platform/internal/archive"
	"ops-platform/internal/evidence"
	"ops-platform/internal/persistence"
	"os"
	"time"
)

func StartSP07Worker(ctx context.Context, pool *pgxpool.Pool, store *archive.Store, evidenceArchive *evidence.ArchiveService) (func(), error) {
	c, err := loadSP07()
	if err != nil {
		return nil, err
	}
	if c == nil {
		return func() {}, nil
	}
	if evidenceArchive == nil || evidenceArchive.BackendLogicalID == "" {
		return nil, fmt.Errorf("SP07 requires the configured SP04 Evidence archive")
	}
	trust, err := RegistryTrustFromFile(os.Getenv("PLATFORM_REGISTRY_TRUST_FILE"))
	if err != nil {
		return nil, err
	}
	s, err := sp07Service(ctx, pool, c, trust, "ops-worker")
	if err != nil {
		return nil, err
	}
	kube, err := action.NewKubernetesREST(c.KubernetesEndpoint, c.KubernetesCAFile, c.KubernetesTokenFile)
	if err != nil {
		return nil, err
	}
	executor := action.KubernetesExecutor{Client: kube, RunnerNamespace: c.Namespace, APIEndpoint: c.APIEndpoint, TrustConfigMap: c.TrustConfigMap}
	run, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for run.Err() == nil {
			for _, tenant := range c.Tenants {
				pass, stop := context.WithTimeout(run, 30*time.Second)
				if err := s.ReconcilePass(pass, tenant, executor); err != nil {
					slog.Warn("SP07 reconcile failed", "tenant", tenant, "errorType", fmt.Sprintf("%T", err))
				}
				if err := archiveCommandPass(pass, s, store, tenant); err != nil {
					slog.Warn("SP07 archive failed", "tenant", tenant, "errorType", fmt.Sprintf("%T", err))
				}
				if err := commandPostCheckPass(pass, s, evidenceArchive, tenant); err != nil {
					slog.Warn("SP07 post-check failed", "tenant", tenant, "errorType", fmt.Sprintf("%T", err))
				}
				stop()
			}
			select {
			case <-run.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() { cancel(); <-done }, nil
}
func archiveCommandPass(ctx context.Context, s action.Service, store *archive.Store, tenant uuid.UUID) error {
	ids := []uuid.UUID{}
	err := persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT execution_id FROM action.executions WHERE tenant_id=$1 AND state IN('succeeded','failed','cancelled','execution_unknown') AND archived_at IS NULL ORDER BY completed_at LIMIT 5`, tenant)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			if err = rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	})
	if err != nil {
		return err
	}
	a := action.OutputArchive{Service: s, Store: store}
	var archiveErr error
	for _, id := range ids {
		if err = a.Archive(ctx, tenant, id); err != nil {
			archiveErr = errors.Join(archiveErr, err)
		}
	}
	return errors.Join(archiveErr, a.Cleanup(ctx, tenant))
}
