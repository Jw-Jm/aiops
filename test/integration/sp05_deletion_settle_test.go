package integration

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"ops-platform/internal/app"
	"ops-platform/internal/archive"
	"ops-platform/internal/auth"
	"ops-platform/internal/configregistry"
	"ops-platform/internal/crypto"
	"ops-platform/internal/evidence"
	"ops-platform/internal/integrations/openbao"
	"ops-platform/internal/integrations/s3"
	"ops-platform/internal/observability"
	"ops-platform/internal/policy"
	"ops-platform/internal/rca"
)

// Minimal deletion-settlement probe: one unschedulable pod, one real deletion,
// one resolved finding. Isolates the subject-gone settlement path.
func TestSP05DeletionSettlesFindings(t *testing.T) {
	for _, key := range []string{"SP03_TEST_DATABASE_URL", "SP03_TEST_OPENBAO_TOKEN", "SP03_TEST_OPENBAO_CA_FILE"} {
		if os.Getenv(key) == "" {
			t.Fatalf("live gate dependency missing: %s", key)
		}
	}
	ownedTenant, ownedSource := uuid.New(), uuid.New()
	collector, ns := sp04OwnedKubernetes(t, t.Context(), ownedTenant.String(), ownedSource.String())
	ctx, db, workerPool, b := sp05DatabaseTimed(t, 8*time.Minute, sp05Seed{ClusterUID: collector.ClusterUID, Namespace: ns, Backend: collector.BackendLogicalID, Tenant: ownedTenant, Source: ownedSource})
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.role_bindings(tenant_id,binding_id,subject,role_name,cluster_scopes,namespace_scopes) VALUES($1,$2,'sp05-registry-admin','platform_admin','[]','[]')`, b.TenantID, uuid.New()); err != nil {
		t.Fatal(err)
	}
	apiConfigPool, err := pgxpool.NewWithConfig(ctx, runtimePoolConfig(t, ctx, db, workerPool.Config().ConnConfig.ConnString(), "api_runtime_role"))
	if err != nil {
		t.Fatal(err)
	}
	defer apiConfigPool.Close()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	trust := configregistry.Ed25519TrustStore{Keys: map[string]ed25519.PublicKey{"integration-key": pub}}
	trustRaw, _ := json.Marshal(map[string]string{"integration-key": base64.StdEncoding.EncodeToString(pub)})
	trustPath := filepath.Join(t.TempDir(), "registry-trust.json")
	if err := os.WriteFile(trustPath, trustRaw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PLATFORM_REGISTRY_TRUST_FILE", trustPath)
	compiler, err := policy.NewBundleCompiler(trust)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := configregistry.NewService(apiConfigPool, trust, compiler)
	if err != nil {
		t.Fatal(err)
	}
	actor := auth.RequestContext{TenantID: b.TenantID, Subject: "sp05-registry-admin", Roles: []auth.Role{auth.PlatformAdmin}}
	recipe, _ := rca.Builtin("pod-scheduling-failure")
	raw, _ := json.Marshal(recipe)
	draft, err := createRegistryDraft(ctx, apiConfigPool, registry, actor, configregistry.DraftCommand{Kind: configregistry.KindRecipe, LogicalName: "pod-scheduling-failure", Content: raw})
	if err != nil {
		t.Fatal(err)
	}
	version, err := signAndPublish(ctx, apiConfigPool, registry, actor, draft, 1, priv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := activateRegistryVersion(ctx, apiConfigPool, registry, actor, version, configregistry.KindRecipe, "pod-scheduling-failure", configregistry.Scope{Type: configregistry.ScopeTenant}, 0); err != nil {
		t.Fatal(err)
	}
	ca, err := os.ReadFile(os.Getenv("SP03_TEST_OPENBAO_CA_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	bao, err := openbao.NewClient(openbao.ClientConfig{Address: os.Getenv("SP03_TEST_OPENBAO_URL"), ServerName: "localhost", CACertBundle: ca, Token: os.Getenv("SP03_TEST_OPENBAO_TOKEN"), ServiceDomain: "ops-system.svc.cluster.local"})
	if err != nil {
		t.Fatal(err)
	}
	if err := bao.ConfigureTransit(ctx); err != nil {
		t.Fatal(err)
	}
	bucket := "sp05-del-" + uuid.NewString()[:8]
	fixture := newRoleTenantS3Fixture(t, []uuid.UUID{b.TenantID}, bucket)
	credentials, _ := os.ReadFile(fixture.CredentialFile)
	backend, err := s3.NewTenantClient(s3.Config{Endpoint: fixture.Endpoint, CACertBundle: fixture.CA, Bucket: bucket}, credentials)
	if err != nil {
		t.Fatal(err)
	}
	store, err := archive.NewStore(backend, 128<<10)
	if err != nil {
		t.Fatal(err)
	}
	protector, err := crypto.NewTransitProtector(bao, "evidence-archive")
	if err != nil {
		t.Fatal(err)
	}
	archives := &evidence.ArchiveService{Pool: workerPool, Store: store, Protector: protector, BackendLogicalID: "archive-live"}
	uid := string(sp04Kubectl(t, ctx, nil, "run", "-n", ns, "del-probe", "--image=busybox:1.36", "--restart=Never", "--overrides", `{"spec":{"nodeSelector":{"ops.platform.test.absent":"yes"}}}`))
	podUID := string(sp04Kubectl(t, ctx, nil, "get", "pod", "-n", ns, "del-probe", "-o", "jsonpath={.metadata.uid}"))
	if podUID == "" || uid == "" {
		t.Fatal("probe pod uid")
	}
	collector.Namespaces = []string{ns}
	workerConfig, _ := sp04RuntimeTLS(t, ns)
	workerConfig.Clusters = []app.SP04Cluster{collector}
	workerConfig.SP05 = &app.SP05Config{Enabled: true}
	runtime, err := observability.NewRuntime(ctx, "sp05-deletion-live")
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(context.Background())
	t.Setenv("SP04_RUNTIME_FILE", sp04ConfigFile(t, workerConfig))
	stop, err := app.StartSP04Worker(ctx, workerPool, archives, runtime)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	resource := fmt.Sprintf("k8s+v1://%s/%s/core/Pod/%s", b.TenantID, b.ClusterUID, podUID)
	for deadline := time.Now().Add(120 * time.Second); ; {
		var count int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM finding.records f WHERE f.tenant_id=$1 AND f.resource_canonical_id=$2 AND f.payload->>'normalizedSymptom'='Unschedulable' AND f.lifecycle_state='firing'`, b.TenantID, resource).Scan(&count); err == nil && count > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("unschedulable finding never fired for %s", resource)
		}
		time.Sleep(time.Second)
	}
	t.Log("firing confirmed; deleting subject")
	sp04Kubectl(t, ctx, nil, "delete", "pod", "-n", ns, "del-probe", "--wait=false")
	for deadline := time.Now().Add(180 * time.Second); ; {
		var state string
		if err := db.QueryRowContext(ctx, `SELECT lifecycle_state FROM finding.records f WHERE f.tenant_id=$1 AND f.resource_canonical_id=$2 AND f.payload->>'normalizedSymptom'='Unschedulable' ORDER BY observed_at DESC LIMIT 1`, b.TenantID, resource).Scan(&state); err == nil && state == "resolved" {
			t.Log("deletion settled the finding to resolved")
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("deletion did not settle the firing finding within 180s")
		}
		time.Sleep(2 * time.Second)
	}
}
