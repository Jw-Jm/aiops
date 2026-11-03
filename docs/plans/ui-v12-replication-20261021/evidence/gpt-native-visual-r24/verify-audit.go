//go:build ignore

// Verify the task Worker's persisted archive through production crypto/storage code.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/app"
	"ops-platform/internal/archive"
	"ops-platform/internal/audit"
	platformcrypto "ops-platform/internal/crypto"
	"ops-platform/internal/integrations/openbao"
	"ops-platform/internal/integrations/s3"
	"ops-platform/internal/persistence"
)

func check(stage string, err error) {
	if err != nil {
		panic(stage + " failed; private inputs omitted")
	}
}

func main() {
	root := "/data/ops-v12-native-visual-r24-20261026/"
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	raw, err := os.ReadFile(root + "private/database-urls.json")
	check("database input", err)
	var urls map[string]string
	check("database input decode", json.Unmarshal(raw, &urls))
	pool, err := app.OpenRuntimePool(ctx, urls["worker"], "worker_runtime_role")
	check("worker pool", err)
	defer pool.Close()
	raw, err = os.ReadFile(root + "evidence/first-tenant.json")
	check("tenant receipt", err)
	var receipt struct {
		TenantID uuid.UUID `json:"tenantId"`
	}
	check("tenant receipt decode", json.Unmarshal(raw, &receipt))
	ca, err := os.ReadFile(root + "private/bao-ca.pem")
	check("OpenBao CA", err)
	bao, err := openbao.NewClient(openbao.ClientConfig{Address: os.Getenv("OPENBAO_ADDR"), ServerName: os.Getenv("OPENBAO_SERVER_NAME"), CACertBundle: ca})
	check("OpenBao client", err)
	_, err = bao.LoginProjectedServiceAccount(ctx, os.Getenv("OPENBAO_PROJECTED_TOKEN_FILE"), "ops-worker")
	check("projected worker login", err)
	ca, err = os.ReadFile(root + "private/archive-ca.pem")
	check("Archive CA", err)
	backend, err := s3.LoadTenantClient(s3.Config{Endpoint: os.Getenv("S3_ENDPOINT"), ServerName: os.Getenv("S3_SERVER_NAME"), Bucket: "ops-v12-r24-archive", CACertBundle: ca}, root+"private/archive-credentials.json")
	check("separated S3 client", err)
	store, err := archive.NewStore(backend, 0)
	check("archive store", err)
	protector, err := platformcrypto.NewTransitProtector(bao, "evidence-archive")
	check("Transit protector", err)
	service, err := audit.NewSegmentService(pool, store, protector, bao, "audit-signing", audit.DefaultArchiveRetention)
	check("audit verifier", err)
	var first, last int64
	var count int
	check("signed bounds", persistence.WithTenantTx(ctx, pool, receipt.TenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT min(first_audit_seq),max(last_audit_seq),count(*) FROM audit.signed_segments WHERE status='signed'").Scan(&first, &last, &count)
	}))
	check("actual archive/signature/predecessor verification", service.VerifyRange(ctx, receipt.TenantID, first, last))
	fmt.Printf("{\"status\":\"passed\",\"tenantId\":%q,\"first\":%d,\"last\":%d,\"signedSegments\":%d,\"verification\":\"production S3 read/decrypt/digest/signature/predecessor chain\"}\n", receipt.TenantID.String(), first, last, count)
}
