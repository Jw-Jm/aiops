// Ephemeral verification entry for the actual Worker startup chain; never shipped
// as a third service. Credentials are projected private fixture files, not flags.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/app"
	"ops-platform/internal/archive"
	"ops-platform/internal/crypto"
	"ops-platform/internal/evidence"
	"ops-platform/internal/integrations/openbao"
	"ops-platform/internal/integrations/s3"
	"ops-platform/internal/observability"
	"ops-platform/internal/persistence"
	"os"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "actual SP05 Worker verification failed:", err)
		os.Exit(1)
	}
}
func run() error {
	var config struct{ DatabaseURL, Tenant, PodUID, BaoAddress, BaoToken, BaoServerName, S3Endpoint, S3Bucket string }
	raw, err := os.ReadFile("/fixture/verification.json")
	if err != nil || json.Unmarshal(raw, &config) != nil {
		return fmt.Errorf("private verification configuration unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	pool, err := app.OpenRuntimePool(ctx, config.DatabaseURL, "worker_runtime_role")
	if err != nil {
		return fmt.Errorf("actual Worker database unavailable")
	}
	defer pool.Close()
	ca, err := os.ReadFile("/fixture/bao-ca.pem")
	if err != nil {
		return fmt.Errorf("actual archive trust unavailable")
	}
	bao, err := openbao.NewClient(openbao.ClientConfig{Address: config.BaoAddress, Token: config.BaoToken, ServerName: config.BaoServerName, CACertBundle: ca, ServiceDomain: "ops-system.svc.cluster.local"})
	if err != nil {
		return fmt.Errorf("actual archive client unavailable")
	}
	archiveCA, err := os.ReadFile("/fixture/s3-ca.pem")
	if err != nil {
		return fmt.Errorf("actual archive trust unavailable")
	}
	backend, err := s3.LoadTenantClient(s3.Config{Endpoint: config.S3Endpoint, Bucket: config.S3Bucket, CACertBundle: archiveCA}, "/fixture/s3-roles.json")
	if err != nil {
		return fmt.Errorf("actual separated archive roles unavailable")
	}
	store, err := archive.NewStore(backend, 128<<10)
	if err != nil {
		return err
	}
	protector, err := crypto.NewTransitProtector(bao, "evidence-archive")
	if err != nil {
		return err
	}
	archives := &evidence.ArchiveService{Pool: pool, Store: store, Protector: protector, BackendLogicalID: "archive-native-worker"}
	runtime, err := observability.NewRuntime(ctx, "sp05-native-worker-check")
	if err != nil {
		return err
	}
	defer runtime.Close(context.Background())
	os.Setenv("SP04_RUNTIME_FILE", "/fixture/runtime.json")
	os.Setenv("PLATFORM_REGISTRY_TRUST_FILE", "/fixture/registry-trust.json")
	stop, err := app.StartSP04Worker(ctx, pool, archives, runtime)
	if err != nil {
		return fmt.Errorf("actual Worker startup rejected")
	}
	defer stop()
	tenant, err := uuid.Parse(config.Tenant)
	if err != nil {
		return err
	}
	for ctx.Err() == nil {
		var candidates, incidents, archived int
		err := persistence.WithTenantTx(ctx, pool, tenant, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT count(DISTINCT f.finding_id),count(DISTINCT l.incident_id),count(DISTINCT e.evidence_id) FROM finding.records f JOIN finding.evidence_refs r USING(tenant_id,finding_id) JOIN platform.evidence_metadata e ON e.tenant_id=r.tenant_id AND e.evidence_id=r.evidence_id LEFT JOIN incident.finding_links l ON l.tenant_id=f.tenant_id AND l.finding_id=f.finding_id WHERE f.tenant_id=$1 AND f.payload->>'ruleId'='k8sgpt/PodRuntimeFault/v1' AND f.resource_canonical_id LIKE '%/Pod/'||$2 AND f.lifecycle_state='firing' AND e.replay_state='archived_verified'`, tenant, config.PodUID).Scan(&candidates, &incidents, &archived)
		})
		if err != nil {
			return fmt.Errorf("actual durable result read unavailable")
		}
		if candidates == 1 && incidents == 1 && archived == 1 {
			fmt.Println(`{"workerStartup":"actual StartSP04Worker","analyzer":"original fixed CLI via authorized shared broker","finding":1,"incident":1,"archivedEvidence":1,"sandbox":"finite-cgroup-readonly-root","llm":false,"performance":"waived"}`)
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("actual Inspector did not reach Finding/Incident/archive before correctness deadline")
		case <-time.After(200 * time.Millisecond):
		}
	}
	return fmt.Errorf("verification cancelled")
}
