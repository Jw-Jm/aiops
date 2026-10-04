package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"ops-platform/internal/archive"
	"ops-platform/internal/contract"
	protect "ops-platform/internal/crypto"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/integrations/openbao"
	"ops-platform/internal/integrations/s3"
	"ops-platform/internal/persistence"
	"ops-platform/internal/resource"
	"os"
	"sort"
	"testing"
	"time"
)

type lostArchiveAck struct {
	archive.Backend
	lost bool
}

func (b *lostArchiveAck) Put(ctx context.Context, key string, body []byte, metadata map[string]string) (archive.Version, error) {
	v, err := b.Backend.Put(ctx, key, body, metadata)
	if err == nil && b.lost {
		b.lost = false
		return archive.Version{}, errors.New("injected lost upload acknowledgement")
	}
	return v, err
}

func (b *lostArchiveAck) ProtectObject(ctx context.Context, key, version string, until time.Time, hold bool) error {
	return b.Backend.(archive.ProtectionBackend).ProtectObject(ctx, key, version, until, hold)
}

func TestSP04ArchiveLiveTransitTLSIAMAndRecovery(t *testing.T) {
	address, token, caFile := os.Getenv("SP03_TEST_OPENBAO_URL"), os.Getenv("SP03_TEST_OPENBAO_TOKEN"), os.Getenv("SP03_TEST_OPENBAO_CA_FILE")
	if address == "" || token == "" || caFile == "" {
		t.Skip("owned live OpenBao fixture required")
	}
	ca, err := os.ReadFile(caFile)
	if err != nil {
		t.Fatal(err)
	}
	bao, err := openbao.NewClient(openbao.ClientConfig{Address: address, ServerName: "localhost", CACertBundle: ca, Token: token, ServiceDomain: "ops-system.svc.cluster.local"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, db, dir, dsn := newMigrationDatabase(t)
	if err := goose.UpToContext(ctx, db, dir, 1); err != nil {
		t.Fatal(err)
	}
	if err := runRemainingMigrationsAsMigrationRole(t, ctx, db, dsn, dir); err != nil {
		t.Fatal(err)
	}
	if err := bao.ConfigureTransit(ctx); err != nil {
		t.Fatal(err)
	}
	tenant, other, cluster, source := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.tenants(tenant_id,slug,display_name) VALUES($1,'sp04-live','Live'),($2,'sp04-foreign','Foreign')`, tenant, other); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.cluster_registrations(tenant_id,cluster_id,cluster_uid,display_name) VALUES($1,$2,'cluster-a','Live')`, tenant, cluster); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.source_registrations(tenant_id,source_id,cluster_id,source_type,instance_key,auth_ref,backend_logical_id) VALUES($1,$2,$3,'victoriametrics','sp04-live','openbao://test/live','metrics-live')`, tenant, source, cluster); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, runtimePoolConfig(t, ctx, db, dsn, "worker_runtime_role"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	bucket := "sp04-evidence-" + uuid.NewString()[:8]
	fixture := newRoleTenantS3Fixture(t, []uuid.UUID{tenant, other}, bucket)
	credentials, err := os.ReadFile(fixture.CredentialFile)
	if err != nil {
		t.Fatal(err)
	}
	backend, err := s3.NewTenantClient(s3.Config{Endpoint: fixture.Endpoint, CACertBundle: fixture.CA, Bucket: bucket}, credentials)
	if err != nil {
		t.Fatal(err)
	}
	lost := &lostArchiveAck{Backend: backend, lost: true}
	store, err := archive.NewStore(lost, 128<<10)
	if err != nil {
		t.Fatal(err)
	}
	protector, err := protect.NewTransitProtector(bao, "evidence-archive")
	if err != nil {
		t.Fatal(err)
	}
	service := &evidence.ArchiveService{Pool: pool, Store: store, Protector: protector, BackendLogicalID: "archive-live"}
	now := time.Now().UTC()
	data := []byte(`[{"value":"bounded-redacted-live-fact"}]`)
	canonical := resource.CanonicalID{Domain: "k8s", Tenant: tenant.String(), Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: "p1"}.String()
	scope := graph.Scope{Tenant: tenant.String(), Cluster: "cluster-a", Namespaces: []string{"apps"}, AuthorizationRevision: "live"}
	e := evidence.Evidence{SourceScopeDigest: evidence.BindingScopeDigest(evidence.Binding{Revision: 1, BackendLogicalID: "metrics-live", SourceType: "victoriametrics"}), SchemaVersion: "evidence/v2", EvidenceID: uuid.NewString(), TenantID: tenant.String(), ResourceCanonicalID: canonical, Type: "metric", DataClass: "D1", SourceRegistrationID: source.String(), SourceRevision: 1, SourceSystem: "victoriametrics", BackendLogicalID: "metrics-live", QueryTemplateVersion: "pod-phase/v1", QueryHash: evidence.Digest([]byte("bounded-query")), EffectiveScope: scope, EvaluatedAt: now, ObservedFrom: now.Add(-time.Minute), ObservedTo: now, SourceRetentionUntil: now, TimeReliable: true, ContentDigest: evidence.Digest(data), IndependenceGroup: source.String(), DerivationEvidenceRefs: []string{}, Data: data}
	if err := service.Capture(ctx, e, "apps", now.Add(181*24*time.Hour)); err == nil {
		t.Fatal("lost upload acknowledgement falsely verified")
	}
	id := uuid.MustParse(e.EvidenceID)
	var state string
	if err := db.QueryRowContext(ctx, `SELECT replay_state FROM platform.evidence_metadata WHERE tenant_id=$1 AND evidence_id=$2`, tenant, id).Scan(&state); err != nil || state != "archive_pending" {
		t.Fatalf("pending state %s %v", state, err)
	}
	if err := service.Recover(ctx, tenant, id); err != nil {
		t.Fatal(err)
	}
	plain, ref, err := service.Read(ctx, tenant, id)
	if err != nil || !bytes.Equal(plain, data) || ref.Object.VersionID == "" || ref.EncryptionKeyVersion == "" {
		t.Fatalf("verified live archive %+v %v", ref, err)
	}
	metadata, err := (evidence.Repository{Pool: pool}).Get(ctx, tenant, id, scope)
	if err != nil {
		t.Fatal(err)
	}
	metadata.FactSlice = json.RawMessage(plain)
	raw, _ := json.Marshal(metadata)
	if err := contract.Validate("https://ops.local/schemas/evidence/v2", raw); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Read(ctx, other, id); err == nil {
		t.Fatal("cross-tenant live archive read")
	}

	// The Audit dependency must reach the object backend, not only PostgreSQL.
	if err := service.ReconcileProtection(ctx, tenant, id); err != nil {
		t.Fatal(err)
	}
	_, protectedRef, err := service.Read(ctx, tenant, id)
	if err != nil || protectedRef.Object.RetainUntil.Before(now.Add(365*24*time.Hour)) {
		t.Fatalf("audit object retention did not extend: %v %v", protectedRef.Object.RetainUntil, err)
	}
	if err := persistence.WithTenantTx(ctx, pool, tenant, func(tx pgx.Tx) error { return evidence.SetLegalHold(ctx, tx, tenant, id, true, "sp04-live") }); err != nil {
		t.Fatal(err)
	}
	if err := service.ReconcileProtection(ctx, tenant, id); err != nil {
		t.Fatal(err)
	}
	actual, err := backend.Head(ctx, protectedRef.Object.Key, protectedRef.Object.VersionID)
	if err != nil || !actual.LegalHold {
		t.Fatalf("physical legal hold missing: %v", err)
	}
	if err := persistence.WithTenantTx(ctx, pool, tenant, func(tx pgx.Tx) error { return evidence.SetLegalHold(ctx, tx, tenant, id, false, "sp04-live") }); err != nil {
		t.Fatal(err)
	}
	if err := service.ReconcileProtection(ctx, tenant, id); err != nil {
		t.Fatal(err)
	}
	actual, err = backend.Head(ctx, protectedRef.Object.Key, protectedRef.Object.VersionID)
	if err != nil || actual.LegalHold || actual.RetainUntil.Before(protectedRef.Object.RetainUntil) {
		t.Fatalf("withdrawal shortened retention or left hold: %v", err)
	}
	if err := bao.TransitRotate(ctx, "evidence-archive"); err != nil {
		t.Fatal(err)
	}
	_, oldRef, err := service.Read(ctx, tenant, id)
	if err != nil || oldRef.EncryptionKeyVersion != ref.EncryptionKeyVersion {
		t.Fatalf("old key version lost %v", err)
	}
	if os.Getenv("OPS_PERFORMANCE_EXEMPTION") == "sp05-user-20261002" || os.Getenv("OPS_PERFORMANCE_EXEMPTION") == "sp06-user-20261003" {
		t.Log("SP05/SP06 explicit user waiver: archive latency sampling/P95 not executed; correctness, retention, Legal Hold and historical-key checks above remain required")
	} else {
		samples := []time.Duration{}
		for i := 0; i < 20; i++ {
			started := time.Now()
			copy := e
			copy.EvidenceID = uuid.NewString()
			if err := service.Capture(ctx, copy, "apps", now.Add(181*24*time.Hour)); err != nil {
				t.Fatal(err)
			}
			if _, _, err := service.Read(ctx, tenant, uuid.MustParse(copy.EvidenceID)); err != nil {
				t.Fatal(err)
			}
			samples = append(samples, time.Since(started))
		}
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
		p95 := samples[(len(samples)*95+99)/100-1]
		t.Logf("live Transit+TLS tenant IAM+PostgreSQL capture/read samples=%d plaintextBytes=%d P95=%s; isolated small facts, not production scale", len(samples), len(data), p95)
		if p95 > 5*time.Second {
			t.Fatalf("archive capture/read P95 exceeds semantic budget: %s", p95)
		}
	}
}
