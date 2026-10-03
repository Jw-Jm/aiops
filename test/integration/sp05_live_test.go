package integration

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"net"
	"net/http"
	"net/url"
	"ops-platform/internal/app"
	"ops-platform/internal/archive"
	"ops-platform/internal/auth"
	"ops-platform/internal/configregistry"
	"ops-platform/internal/crypto"
	"ops-platform/internal/evidence"
	"ops-platform/internal/finding"
	"ops-platform/internal/graph"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/integrations/openbao"
	"ops-platform/internal/integrations/s3"
	"ops-platform/internal/observability"
	"ops-platform/internal/policy"
	"ops-platform/internal/rca"
	"ops-platform/internal/resource"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSP05ActualOrbStackInspectionAPIWorkerArchiveAndRestart(t *testing.T) {
	// This is a required correctness gate. Missing dependencies are failures,
	// not a skipped live acceptance or substituted unit test.
	for _, key := range []string{"SP03_TEST_DATABASE_URL", "SP03_KEYCLOAK_TEST_ISSUER", "SP03_TEST_OPENBAO_TOKEN", "SP03_TEST_OPENBAO_CA_FILE"} {
		if os.Getenv(key) == "" {
			t.Fatalf("live gate dependency missing: %s", key)
		}
	}
	ownedTenant, ownedSource := uuid.New(), uuid.New()
	collector, ns := sp04OwnedKubernetes(t, t.Context(), ownedTenant.String(), ownedSource.String())
	ctx, db, workerPool, b := sp05Database(t, sp05Seed{ClusterUID: collector.ClusterUID, Namespace: ns, Backend: collector.BackendLogicalID, Tenant: ownedTenant, Source: ownedSource})
	token := sp04KeycloakToken(t, ctx, b.TenantID)
	scopes, _ := json.Marshal([]string{b.ClusterID.String()})
	namespaces, _ := json.Marshal([]map[string]string{{"clusterId": b.ClusterID.String(), "namespace": ns}})
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.role_bindings(tenant_id,binding_id,subject,role_name,cluster_scopes,namespace_scopes) VALUES($1,$2,$3,'operator',$4,$5),($1,$6,'sp05-registry-admin','platform_admin','[]','[]')`, b.TenantID, uuid.New(), token.Subject, scopes, namespaces, uuid.New()); err != nil {
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
	for _, name := range []string{"dimm-failure", "pvc-csi-failure", "node-failure"} {
		recipe, _ := rca.Builtin(name)
		raw, _ := json.Marshal(recipe)
		draft, err := createRegistryDraft(ctx, apiConfigPool, registry, actor, configregistry.DraftCommand{Kind: configregistry.KindRecipe, LogicalName: name, Content: raw})
		if err != nil {
			t.Fatal(err)
		}
		version, err := signAndPublish(ctx, apiConfigPool, registry, actor, draft, 1, priv)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := activateRegistryVersion(ctx, apiConfigPool, registry, actor, version, configregistry.KindRecipe, name, configregistry.Scope{Type: configregistry.ScopeTenant}, 0); err != nil {
			t.Fatal(err)
		}
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
	bucket := "sp05-chain-" + uuid.NewString()[:8]
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
	// Pending PVC and an unschedulable Pod are created only in the owned namespace.
	pvc := fmt.Sprintf(`{"apiVersion":"v1","kind":"PersistentVolumeClaim","metadata":{"name":"sp05-pvc","namespace":%q},"spec":{"accessModes":["ReadWriteOnce"],"storageClassName":"sp05-owned-missing-class","resources":{"requests":{"storage":"1Mi"}}}}`, ns)
	pod := fmt.Sprintf(`{"apiVersion":"v1","kind":"Pod","metadata":{"name":"sp05-unschedulable","namespace":%q},"spec":{"nodeSelector":{"ops.platform.test.sp05":"absent"},"containers":[{"name":"pause","image":"registry.k8s.io/pause:3.10.1"}]}}`, ns)
	var pvcObject struct {
		Metadata struct {
			UID string `json:"uid"`
		}
	}
	if json.Unmarshal(sp04Kubectl(t, ctx, []byte(pvc), "create", "-f", "-", "-o", "json"), &pvcObject) != nil {
		t.Fatal("PVC creation response")
	}
	sp04Kubectl(t, ctx, []byte(pod), "create", "-f", "-")
	workerConfig, apiConfig := sp04RuntimeTLS(t, ns)
	workerConfig.Clusters = []app.SP04Cluster{collector}
	sp05 := &app.SP05Config{Enabled: true, IngestionBindings: []httpapi.IngestionBinding{{Tenant: b.TenantID.String(), Subject: token.Subject, SourceID: b.SourceID.String(), RegistrationRevision: 1, CredentialRevision: 1}}}
	workerConfig.SP05 = sp05
	apiConfig.SP05 = sp05
	runtime, err := observability.NewRuntime(ctx, "sp05-live-correctness")
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(context.Background())
	t.Setenv("SP04_RUNTIME_FILE", sp04ConfigFile(t, workerConfig))
	stop, err := app.StartSP04Worker(ctx, workerPool, archives, runtime)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if stop != nil {
			stop()
		}
	}()
	application, err := app.NewAPI(app.AppConfig{DatabaseURL: apiConfigPool.Config().ConnConfig.ConnString(), OIDCIssuerURL: os.Getenv("SP03_KEYCLOAK_TEST_ISSUER"), ProfilePath: isolatedRuntimeProfile(t, os.Getenv("SP03_KEYCLOAK_TEST_ISSUER"), "", "")})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SP04_RUNTIME_FILE", sp04ConfigFile(t, apiConfig))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	run, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- application.Serve(run, listener, runtime) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	endpoint := "http://" + listener.Addr().String()
	call := func(method, path string, body []byte, key string) (int, []byte) {
		request, _ := http.NewRequestWithContext(ctx, method, endpoint+path, bytes.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+token.AccessToken)
		request.Header.Set("Content-Type", "application/json")
		if key != "" {
			request.Header.Set("Idempotency-Key", key)
		}
		response, err := client.Do(request)
		if err != nil {
			return 0, nil
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		assertSP05PublicResponseContract(t, path, response.StatusCode, raw)
		return response.StatusCode, raw
	}
	canonical := resource.CanonicalID{Domain: "k8s", Tenant: b.TenantID.String(), Scope: b.ClusterUID, APIGroup: "core", Kind: "PersistentVolumeClaim", StableID: pvcObject.Metadata.UID}.String()
	var incidentID string
	for deadline := time.Now().Add(60 * time.Second); ; {
		var findingCount, archivedCount int
		err := db.QueryRowContext(ctx, `SELECT count(DISTINCT f.finding_id),count(DISTINCT e.evidence_id),COALESCE(min(l.incident_id::text),'') FROM finding.records f LEFT JOIN finding.evidence_refs r USING(tenant_id,finding_id) LEFT JOIN platform.evidence_metadata e ON e.tenant_id=r.tenant_id AND e.evidence_id=r.evidence_id AND e.replay_state='archived_verified' LEFT JOIN incident.finding_links l ON l.tenant_id=f.tenant_id AND l.finding_id=f.finding_id WHERE f.tenant_id=$1 AND f.resource_canonical_id=$2`, b.TenantID, canonical).Scan(&findingCount, &archivedCount, &incidentID)
		if err != nil {
			t.Fatal(err)
		}
		if findingCount > 0 && archivedCount > 0 && incidentID != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("actual inspector did not reach durable archive/incident: finding=%d archive=%d incident=%s", findingCount, archivedCount, incidentID)
		}
		time.Sleep(200 * time.Millisecond)
	}
	for deadline := time.Now().Add(45 * time.Second); ; {
		status, body := call("GET", "/api/v1/resources/by-canonical-id?canonicalId="+url.QueryEscape(canonical), nil, "")
		var response struct{ Data graph.Result }
		if status == 200 && json.Unmarshal(body, &response) == nil && response.Data.Freshness == "fresh" && !response.Data.Partial {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("inspector prevented graph recovery to fresh: %d %s", status, body)
		}
		time.Sleep(200 * time.Millisecond)
	}
	var scheduled int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM finding.records WHERE tenant_id=$1 AND payload->>'normalizedSymptom'='Unschedulable'`, b.TenantID).Scan(&scheduled); err != nil || scheduled == 0 {
		t.Fatalf("real scheduling condition not ingested: %d %v", scheduled, err)
	}
	for _, path := range []string{"/api/v1/findings?clusterUid=" + url.QueryEscape(b.ClusterUID) + "&resourceCanonicalId=" + url.QueryEscape(canonical), "/api/v1/incidents/" + incidentID, "/api/v1/incidents/" + incidentID + "/timeline"} {
		if status, body := call("GET", path, nil, ""); status != 200 {
			t.Fatalf("actual API %s: %d %s", path, status, body)
		}
	}
	currentPath := "/api/v1/incidents/" + incidentID + "/rca"
	var currentRevision int64
	awaitCurrent := func() {
		for deadline := time.Now().Add(60 * time.Second); ; {
			status, body := call("GET", currentPath, nil, "")
			var view struct {
				Data struct {
					Eligible bool         `json:"currentEligible"`
					Revision rca.Revision `json:"revision"`
				}
			}
			if status == 200 && json.Unmarshal(body, &view) == nil && view.Data.Eligible {
				currentRevision = view.Data.Revision.Revision
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("current RCA did not recheck actual Graph/Recipe/Evidence: %d %s", status, body)
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	awaitCurrent()
	historyPath := fmt.Sprintf("/api/v1/incidents/%s/rca/revisions/%d", incidentID, currentRevision)
	stop()
	stop = nil
	status, body := call("GET", currentPath, nil, "")
	var stoppedView struct {
		Data struct {
			Eligible bool `json:"currentEligible"`
		}
	}
	if status != 200 || json.Unmarshal(body, &stoppedView) != nil || stoppedView.Data.Eligible {
		t.Fatalf("stopped real Worker recycled frozen eligibility: %d %s", status, body)
	}
	if status, body := call("GET", historyPath, nil, ""); status != 200 {
		t.Fatalf("Graph outage erased authorized historical archive revision: %d %s", status, body)
	}
	t.Setenv("SP04_RUNTIME_FILE", sp04ConfigFile(t, workerConfig))
	stop, err = app.StartSP04Worker(ctx, workerPool, archives, runtime)
	if err != nil {
		t.Fatal(err)
	}
	awaitCurrent()
	if status, _ := call("POST", "/api/v1/findings:ingest", []byte(`{}`), uuid.NewString()); status != 409 {
		t.Fatalf("historical contract falsely enabled: %d", status)
	}
	// Push transport uses the same durable ingress and server-owned source binding.
	envelope := sp05Envelope(b, "api-live-event", "api-live-occurrence")
	envelope.ResourceCanonicalID = canonical
	envelope.Namespace = ns
	envelope.RuleFamily = "storage"
	envelope.RuleID = "live-pvc/v1"
	envelope.NormalizedSymptom = "PVCUnbound"
	envelope.Payload = json.RawMessage(`{"phase":"Pending"}`)
	envelope.StartsAt = time.Now().Add(-time.Second).UTC()
	envelope.ObservedAt = time.Now().UTC()
	raw, _ := json.Marshal(envelope)
	for attempt := 0; attempt < 2; attempt++ {
		if status, body := call("POST", "/api/v2/findings:ingest", raw, envelope.IdempotencyKey); status != 200 {
			t.Fatalf("live bound ingestion: %d %s", status, body)
		}
	}
	stop()
	stop = nil
	t.Setenv("SP04_RUNTIME_FILE", sp04ConfigFile(t, workerConfig))
	stop, err = app.StartSP04Worker(ctx, workerPool, archives, runtime)
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(40 * time.Second); ; {
		var pending int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM finding.outbox WHERE tenant_id=$1 AND schema_version='finding/v2' AND state<>'delivered'`, b.TenantID).Scan(&pending); err != nil {
			t.Fatal(err)
		}
		if pending == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("durable consumer failed to recover after restart")
		}
		time.Sleep(200 * time.Millisecond)
	}
	stop()
	stop = nil
	sp05ReviewActualHTTPMutations(t, ctx, db, workerPool, b, token.Subject, envelope, call)
	// Current role withdrawal must deny a transport replay even with old OIDC.
	if _, err := db.ExecContext(ctx, `UPDATE platform.role_bindings SET status='disabled',revision=revision+1 WHERE tenant_id=$1 AND subject=$2`, b.TenantID, token.Subject); err != nil {
		t.Fatal(err)
	}
	if status, _ := call("POST", "/api/v2/findings:ingest", raw, envelope.IdempotencyKey); status != 403 {
		t.Fatalf("withdrawn replay returned %d", status)
	}
	t.Log("actual OrbStack native PVC/scheduling -> Worker inspector -> unified Finding ingress -> PostgreSQL outbox consumer -> Incident -> real OpenBao/SeaweedFS archive; OIDC API replay and Worker restart; no performance measurement; hardware and Node fault injection remain separate frozen fixtures")
	_ = finding.Accepted
}
