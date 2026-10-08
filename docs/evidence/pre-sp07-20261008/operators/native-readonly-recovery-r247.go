//go:build ops_readonly_recovery

// This bounded operator verifies existing restored rows using product services.
// It creates no business objects and accepts no arbitrary query or URL request.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
	"ops-platform/internal/persistence"
)

type configuration struct {
	DatabaseURL, TenantID, BaoAddress, BaoCAFile, BaoToken, S3Endpoint, S3CAFile, S3CredentialsFile, Bucket, ArchiveBackendLogicalID, Output string
}
type result struct {
	RetentionKinds              map[string]int `json:"retentionKinds"`
	ActiveRetentionReferences   int            `json:"activeRetentionReferences"`
	AdditionalConservativeHolds int            `json:"additionalConservativeHolds"`
	ProtectionMismatch          map[string]any `json:"protectionMismatch,omitempty"`
	ObservedAt                  time.Time      `json:"observedAt"`
	TenantID                    string         `json:"tenantId"`
	DatabaseReadOnly            bool           `json:"databaseReadOnly"`
	VerifiedEvidence            int            `json:"verifiedEvidence"`
	VerifiedProtectionVersions  int            `json:"verifiedProtectionVersions"`
	VerifiedLegalHolds          int            `json:"verifiedLegalHolds"`
	RetentionReferenceRows      int            `json:"retentionReferenceRows"`
	EvidenceDependencyRows      int            `json:"evidenceDependencyRows"`
	UnsignedAuditTailRows       int            `json:"unsignedAuditTailRows"`
	VerifiedSignedAuditSegments int            `json:"verifiedSignedAuditSegments"`
	FirstAudit, LastAudit       int64
	Evidence                    []map[string]any `json:"evidence"`
	FullRecoveryAcceptance      bool             `json:"fullRecoveryAcceptance"`
	Original27Recovery          bool             `json:"original27Recovery"`
	FailureStage                string           `json:"failureStage,omitempty"`
	ExitCode                    int              `json:"exitCode"`
}

func main() {
	if len(os.Args) != 2 {
		os.Exit(2)
	}
	var c configuration
	raw, err := os.ReadFile(os.Args[1])
	if err != nil || json.Unmarshal(raw, &c) != nil {
		os.Exit(2)
	}
	out := result{ObservedAt: time.Now().UTC(), TenantID: c.TenantID, ExitCode: 1, Evidence: []map[string]any{}}
	stage := "configuration"
	err = verify(c, &out, &stage)
	if err != nil {
		out.FailureStage = stage
		_ = os.WriteFile(c.Output+".private-error", []byte(err.Error()), 0600)
	} else {
		out.ExitCode = 0
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	if os.WriteFile(c.Output, append(b, '\n'), 0600) != nil {
		os.Exit(2)
	}
	fmt.Printf("Restored product Evidence/Audit read-only verification exit %d stage %s\n", out.ExitCode, stage)
	os.Exit(out.ExitCode)
}
func verify(c configuration, out *result, stage *string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	tenant, err := uuid.Parse(c.TenantID)
	if err != nil {
		return err
	}
	*stage = "restored-database"
	config, err := pgxpool.ParseConfig(c.DatabaseURL)
	if err != nil {
		return err
	}
	config.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"
	config.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return err
	}
	defer pool.Close()
	var readonly string
	if err := pool.QueryRow(ctx, "SHOW transaction_read_only").Scan(&readonly); err != nil {
		return err
	}
	if readonly != "on" {
		return fmt.Errorf("database is writable")
	}
	out.DatabaseReadOnly = true
	*stage = "restored-transit"
	ca, err := os.ReadFile(c.BaoCAFile)
	if err != nil {
		return err
	}
	bao, err := openbao.NewClient(openbao.ClientConfig{Address: c.BaoAddress, ServerName: "localhost", CACertBundle: ca, Token: c.BaoToken})
	if err != nil {
		return err
	}
	protector, err := platformcrypto.NewTransitProtector(bao, "evidence-archive")
	if err != nil {
		return err
	}
	*stage = "restored-archive"
	s3CA, err := os.ReadFile(c.S3CAFile)
	if err != nil {
		return err
	}
	backend, err := s3.LoadTenantClient(s3.Config{Endpoint: c.S3Endpoint, ServerName: "localhost", CACertBundle: s3CA, Bucket: c.Bucket}, c.S3CredentialsFile)
	if err != nil {
		return err
	}
	if !backend.RoleSeparated() {
		return fmt.Errorf("archive roles not separated")
	}
	store, err := archive.NewStore(backend, 0)
	if err != nil {
		return err
	}
	*stage = "restored-retention-reference-closure"
	protection, err := loadProtection(ctx, pool, tenant, out)
	if err != nil {
		return err
	}
	*stage = "restored-evidence-enumeration"
	var ids []uuid.UUID
	err = persistence.WithTenantTx(ctx, pool, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT m.evidence_id FROM platform.evidence_metadata m JOIN platform.evidence_archive_intents i USING(tenant_id,evidence_id) WHERE m.tenant_id=$1 AND m.replay_state='archived_verified' AND i.status='verified' AND NOT m.deleting ORDER BY m.evidence_id LIMIT 4097`, tenant)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	})
	if err != nil {
		return err
	}
	if len(ids) == 0 || len(ids) > 4096 {
		return fmt.Errorf("invalid bounded evidence set")
	}
	service := evidence.ArchiveService{Pool: pool, Store: store, Protector: protector, BackendLogicalID: c.ArchiveBackendLogicalID}
	*stage = "restored-evidence-version-and-digest"
	for _, id := range ids {
		plain, ref, err := service.Read(ctx, tenant, id)
		if err != nil {
			return err
		}
		if evidence.Digest(plain) != ref.PlaintextDigest || ref.Object.VersionID == "" || !ref.Object.RetainUntil.After(time.Now()) {
			return fmt.Errorf("restored evidence differs")
		}
		expected, ok := protection[id]
		if !ok {
			return fmt.Errorf("missing restored protection node")
		}
		actual, err := backend.Head(ctx, ref.Object.Key, ref.Object.VersionID)
		if err != nil {
			return err
		}
		minimum := expected.until
		if ref.Object.RetainUntil.After(minimum) {
			minimum = ref.Object.RetainUntil
		}
		if actual.VersionID != ref.Object.VersionID || actual.RetainUntil.Before(minimum) || (expected.hold && !actual.LegalHold) {
			out.ProtectionMismatch = map[string]any{"evidenceId": id.String(), "expectedVersion": ref.Object.VersionID, "actualVersion": actual.VersionID, "requiredLegalHold": expected.hold, "actualLegalHold": actual.LegalHold, "closureMinimumRetainUntil": minimum, "actualRetainUntil": actual.RetainUntil}
			return fmt.Errorf("restored version closure protection differs")
		}
		out.VerifiedProtectionVersions++
		if expected.hold {
			out.VerifiedLegalHolds++
		}
		if actual.LegalHold && !expected.hold {
			out.AdditionalConservativeHolds++
		}
		out.VerifiedEvidence++
		out.Evidence = append(out.Evidence, map[string]any{"evidenceId": id.String(), "archiveVersionId": ref.Object.VersionID, "plaintextDigest": ref.PlaintextDigest, "ciphertextDigest": ref.CiphertextDigest, "encryptionKeyVersion": ref.EncryptionKeyVersion, "retainUntil": ref.Object.RetainUntil})
	}
	*stage = "restored-audit-range"
	err = persistence.WithTenantTx(ctx, pool, tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT min(first_audit_seq),max(last_audit_seq),count(*) FROM audit.signed_segments WHERE tenant_id=$1 AND status='signed'`, tenant).Scan(&out.FirstAudit, &out.LastAudit, &out.VerifiedSignedAuditSegments)
	})
	if err != nil {
		return err
	}
	if out.VerifiedSignedAuditSegments == 0 {
		return fmt.Errorf("no signed range")
	}
	verifier, err := audit.NewSegmentService(pool, store, protector, bao, "audit-signing", audit.DefaultArchiveRetention)
	if err != nil {
		return err
	}
	*stage = "restored-audit-cryptographic-range"
	if err := verifier.VerifyRange(ctx, tenant, out.FirstAudit, out.LastAudit); err != nil {
		return err
	}
	err = persistence.WithTenantTx(ctx, pool, tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT count(*) FROM audit.records WHERE tenant_id=$1 AND audit_seq>$2`, tenant, out.LastAudit).Scan(&out.UnsignedAuditTailRows)
	})
	if err != nil {
		return err
	}
	*stage = "complete-narrow-restored-evidence-protection-and-signed-audit"
	return nil
}

// Independently enumerate the stored closure and compare its minimum protection
// with native immutable versions. Active references protect their dependencies;
// base Evidence retention alone is not the effective retention contract.
type protectedNode struct {
	until time.Time
	hold  bool
}

func loadProtection(ctx context.Context, pool *pgxpool.Pool, tenant uuid.UUID, out *result) (map[uuid.UUID]protectedNode, error) {
	nodes := map[uuid.UUID]protectedNode{}
	dependents := map[uuid.UUID][]uuid.UUID{}
	out.RetentionKinds = map[string]int{}
	err := persistence.WithTenantTx(ctx, pool, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT evidence_id,retain_until,legal_hold FROM platform.evidence_metadata WHERE tenant_id=$1 ORDER BY evidence_id LIMIT 4097`, tenant)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id uuid.UUID
			var n protectedNode
			if err := rows.Scan(&id, &n.until, &n.hold); err != nil {
				rows.Close()
				return err
			}
			nodes[id] = n
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(nodes) > 4096 {
			return fmt.Errorf("protection node bound exceeded")
		}
		rows, err = tx.Query(ctx, `SELECT evidence_id,reference_kind,retain_until,active FROM platform.evidence_retention_references WHERE tenant_id=$1 LIMIT 65537`, tenant)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id uuid.UUID
			var kind string
			var until time.Time
			var active bool
			if err := rows.Scan(&id, &kind, &until, &active); err != nil {
				rows.Close()
				return err
			}
			n, ok := nodes[id]
			if !ok {
				rows.Close()
				return fmt.Errorf("dangling restored retention reference")
			}
			if until.After(n.until) {
				n.until = until
			}
			n.hold = n.hold || active
			nodes[id] = n
			out.RetentionReferenceRows++
			out.RetentionKinds[kind]++
			if active {
				out.ActiveRetentionReferences++
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if out.RetentionReferenceRows > 65536 {
			return fmt.Errorf("retention reference bound exceeded")
		}
		rows, err = tx.Query(ctx, `SELECT referrer_id,dependency_id FROM platform.evidence_dependencies WHERE tenant_id=$1 LIMIT 65537`, tenant)
		if err != nil {
			return err
		}
		for rows.Next() {
			var ref, dep uuid.UUID
			if err := rows.Scan(&ref, &dep); err != nil {
				rows.Close()
				return err
			}
			if _, ok := nodes[ref]; !ok {
				rows.Close()
				return fmt.Errorf("dangling restored referrer")
			}
			if _, ok := nodes[dep]; !ok {
				rows.Close()
				return fmt.Errorf("dangling restored dependency")
			}
			dependents[dep] = append(dependents[dep], ref)
			out.EvidenceDependencyRows++
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if out.EvidenceDependencyRows > 65536 {
			return fmt.Errorf("dependency bound exceeded")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	result := map[uuid.UUID]protectedNode{}
	for root := range nodes {
		seen := map[uuid.UUID]bool{}
		queue := []uuid.UUID{root}
		var required protectedNode
		for len(queue) > 0 {
			id := queue[0]
			queue = queue[1:]
			if seen[id] {
				continue
			}
			seen[id] = true
			n := nodes[id]
			if n.until.After(required.until) {
				required.until = n.until
			}
			required.hold = required.hold || n.hold
			queue = append(queue, dependents[id]...)
		}
		result[root] = required
	}
	return result, nil
}
