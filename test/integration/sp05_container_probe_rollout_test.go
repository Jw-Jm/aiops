package integration

import (
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
	"ops-platform/internal/app"
	"ops-platform/internal/archive"
	"ops-platform/internal/auth"
	"ops-platform/internal/configregistry"
	"ops-platform/internal/crypto"
	"ops-platform/internal/evidence"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/integrations/openbao"
	"ops-platform/internal/integrations/s3"
	"ops-platform/internal/observability"
	"ops-platform/internal/policy"
	"ops-platform/internal/rca"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Batch-1 follow-up acceptance: crash loop, OOM kill, probe failure, stuck
// rollout and the deterministic network fault (host-port conflict) traverse
// the same native chain. Recovery uses repair, rollout completion and real
// object deletion (subject-gone settlement).
func TestSP05ActualContainerProbeRolloutAndSchedulingDiagnosis(t *testing.T) {
	for _, key := range []string{"SP03_TEST_DATABASE_URL", "SP03_KEYCLOAK_TEST_ISSUER", "SP03_TEST_OPENBAO_TOKEN", "SP03_TEST_OPENBAO_CA_FILE"} {
		if os.Getenv(key) == "" {
			t.Fatalf("live gate dependency missing: %s", key)
		}
	}
	ownedTenant, ownedSource := uuid.New(), uuid.New()
	collector, ns := sp04OwnedKubernetes(t, t.Context(), ownedTenant.String(), ownedSource.String())
	ctx, db, workerPool, b := sp05DatabaseTimed(t, 35*time.Minute, sp05Seed{ClusterUID: collector.ClusterUID, Namespace: ns, Backend: collector.BackendLogicalID, Tenant: ownedTenant, Source: ownedSource})
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
	for _, name := range []string{"node-failure", "pvc-csi-failure", "pod-image-pull-failure", "service-no-endpoints", "pod-crashloop-failure", "pod-oom-failure", "pod-probe-failure", "deployment-rollout-stuck", "pod-scheduling-failure"} {
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
	bucket := "sp05-batch1b-" + uuid.NewString()[:8]
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

	// busybox without a command exits immediately (crash loop); the OOM probe
	// writes past its own limit; the probe pod fails readiness; the deployment
	// misses its 150s progress deadline; two pods claim one host port.
	sp04Kubectl(t, ctx, nil, "run", "-n", ns, "batch1b-crash", "--image=busybox:1.36", "--restart=Always")
		sp04Kubectl(t, ctx, []byte(fmt.Sprintf(`{"apiVersion":"v1","kind":"Pod","metadata":{"name":"batch1b-oom","namespace":%q},"spec":{"restartPolicy":"Always","containers":[{"name":"app","image":"busybox:1.36","command":["sh","-c","awk 'BEGIN{s=\"x\"; while(1) s=s s; }'"],"resources":{"limits":{"memory":"64Mi"}}}]}}`, ns)), "create", "-f", "-")
	sp04Kubectl(t, ctx, []byte(fmt.Sprintf(`{"apiVersion":"v1","kind":"Pod","metadata":{"name":"batch1b-probe","namespace":%q},"spec":{"restartPolicy":"Always","containers":[{"name":"app","image":"busybox:1.36","command":["sh","-c","sleep 600"],"readinessProbe":{"exec":{"command":["sh","-c","exit 1"]},"initialDelaySeconds":2,"periodSeconds":3}}]}}`, ns)), "create", "-f", "-")
	sp04Kubectl(t, ctx, []byte(fmt.Sprintf(`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"batch1b-rollout","namespace":%q},"spec":{"progressDeadlineSeconds":150,"replicas":1,"selector":{"matchLabels":{"app":"batch1b-rollout"}},"template":{"metadata":{"labels":{"app":"batch1b-rollout"}},"spec":{"restartPolicy":"Always","containers":[{"name":"app","image":"registry.example.invalid/batch1b/web:v1"}]}}}}`, ns)), "create", "-f", "-")
	sp04Kubectl(t, ctx, []byte(fmt.Sprintf(`{"apiVersion":"v1","kind":"Pod","metadata":{"name":"batch1b-port-a","namespace":%q},"spec":{"restartPolicy":"Never","containers":[{"name":"app","image":"busybox:1.36","command":["sh","-c","sleep 3000"],"ports":[{"containerPort":8080,"hostPort":31888}]}]}}`, ns)), "create", "-f", "-")
	for deadline := time.Now().Add(90 * time.Second); ; {
		phase := string(sp04Kubectl(t, ctx, nil, "get", "pod", "-n", ns, "batch1b-port-a", "-o", "jsonpath={.status.phase}"))
		if phase == "Running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("port holder did not run: %q", phase)
		}
		time.Sleep(2 * time.Second)
	}
	sp04Kubectl(t, ctx, []byte(fmt.Sprintf(`{"apiVersion":"v1","kind":"Pod","metadata":{"name":"batch1b-port-b","namespace":%q},"spec":{"restartPolicy":"Never","containers":[{"name":"app","image":"busybox:1.36","command":["sh","-c","sleep 3000"],"ports":[{"containerPort":8080,"hostPort":31888}]}]}}`, ns)), "create", "-f", "-")

	collector.Namespaces = []string{ns}
	workerConfig, apiConfig := sp04RuntimeTLS(t, ns)
	workerConfig.Clusters = []app.SP04Cluster{collector}
	sp05 := &app.SP05Config{Enabled: true, IngestionBindings: []httpapi.IngestionBinding{{Tenant: b.TenantID.String(), Subject: token.Subject, SourceID: b.SourceID.String(), RegistrationRevision: 1, CredentialRevision: 1}}}
	workerConfig.SP05 = sp05
	apiConfig.SP05 = sp05
	runtime, err := observability.NewRuntime(ctx, "sp05-batch1b-live")
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
	call := func(method, path string) (int, []byte) {
		request, _ := http.NewRequestWithContext(ctx, method, endpoint+path, nil)
		request.Header.Set("Authorization", "Bearer "+token.AccessToken)
		response, err := client.Do(request)
		if err != nil {
			return 0, nil
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(response.Body, 4<<20))
		return response.StatusCode, raw
	}
	uid := func(kind, name string) string {
		t.Helper()
		return string(sp04Kubectl(t, ctx, nil, "get", kind, "-n", ns, name, "-o", "jsonpath={.metadata.uid}"))
	}
	awaitIncident := func(kind, name string) string {
		t.Helper()
		group := "core"
		if kind == "Deployment" || kind == "ReplicaSet" || kind == "StatefulSet" || kind == "DaemonSet" || kind == "Job" {
			group = "apps"
		}
		resource := fmt.Sprintf("k8s+v1://%s/%s/%s/%s/%s", b.TenantID, b.ClusterUID, group, kind, uid(kind, name))
		for deadline := time.Now().Add(240 * time.Second); ; {
			var incidentID string
			if err := db.QueryRowContext(ctx, `SELECT l.incident_id::text FROM incident.finding_links l JOIN incident.records i USING(tenant_id,incident_id) JOIN finding.records f USING(tenant_id,finding_id) WHERE l.tenant_id=$1 AND f.resource_canonical_id=$2 AND i.state<>'closed' ORDER BY i.created_at DESC LIMIT 1`, b.TenantID, resource).Scan(&incidentID); err == nil && incidentID != "" {
				return incidentID
			}
			if time.Now().After(deadline) {
				t.Fatalf("no live incident reached for %s/%s", kind, name)
			}
			time.Sleep(time.Second)
		}
	}
	// Five churning subjects keep the current-view graph-revision gate flapping,
	// so the durable committed revision is the acceptance artifact here; the
	// current-view path is already covered by the batch-1 acceptance.
	awaitRCA := func(incidentID string, accept func(candidate, recipe, status string, missing []string) bool, describe string) {
		t.Helper()
		var last string
		for deadline := time.Now().Add(300 * time.Second); ; {
			var status, missing, candidates string
			err := db.QueryRowContext(ctx, `SELECT result->'result'->>'status',coalesce(result->'result'->'evidenceBundle'->>'missing','[]'),coalesce(result->'result'->'candidates'->0->>'candidateType','') FROM incident.rca_revisions WHERE tenant_id=$1 AND incident_id=$2 AND NOT superseded ORDER BY revision DESC LIMIT 1`, b.TenantID, incidentID).Scan(&status, &missing, &candidates)
			var missingList []string
			json.Unmarshal([]byte(missing), &missingList)
			if err == nil && accept(candidates, "", status, missingList) {
				t.Logf("%s: candidate=%s status=%s missing=%s", describe, candidates, status, missing)
				return
			} else if err == nil {
				last = fmt.Sprintf("candidate=%s status=%s missing=%s", candidates, status, missing)
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: last revision %s", describe, last)
			}
			time.Sleep(2 * time.Second)
		}
	}
	sufficient := func(candidate, status string) func(string, string, string, []string) bool {
		return func(c, r, s string, missing []string) bool { return c == candidate && s == status && len(missing) == 0 }
	}
	_ = call

	incidentCrash := awaitIncident("Pod", "batch1b-crash")
	awaitRCA(incidentCrash, sufficient("pod_crash_loop", "confirmed"), "crash sufficient")
	incidentOOM := awaitIncident("Pod", "batch1b-oom")
	awaitRCA(incidentOOM, sufficient("pod_oom_killed", "probable"), "oom sufficient")
	incidentProbe := awaitIncident("Pod", "batch1b-probe")
	awaitRCA(incidentProbe, sufficient("pod_probe_failure", "probable"), "probe sufficient")
	incidentPort := awaitIncident("Pod", "batch1b-port-b")
	awaitRCA(incidentPort, sufficient("pod_scheduling_failure", "confirmed"), "port-conflict scheduling sufficient")
	incidentRollout := awaitIncident("Deployment", "batch1b-rollout")
	awaitRCA(incidentRollout, sufficient("deployment_rollout_stuck", "confirmed"), "rollout sufficient")

	// Recovery: image repair for the crash loop, rollout completion for the
	// deployment, and real deletion for the immutable-command probes.
	// kubectl run names the container after the pod.
	sp04Kubectl(t, ctx, nil, "set", "image", "pod", "-n", ns, "batch1b-crash", "batch1b-crash=registry.aliyuncs.com/google_containers/pause:3.10.2")
	sp04Kubectl(t, ctx, nil, "set", "image", "deployment", "-n", ns, "batch1b-rollout", "app=registry.aliyuncs.com/google_containers/pause:3.10.2")
	sp04Kubectl(t, ctx, nil, "delete", "pod", "-n", ns, "batch1b-oom", "--wait=false")
	sp04Kubectl(t, ctx, nil, "delete", "pod", "-n", ns, "batch1b-probe", "--wait=false")
	sp04Kubectl(t, ctx, nil, "delete", "pod", "-n", ns, "batch1b-port-a", "--wait=false")
	// Every incident linked to this owned namespace must settle: repair
	// settles what it healed and deletion settles the immutable-command pods
	// and the rollout's replaced pods.
	for deadline := time.Now().Add(12 * time.Minute); ; {
		var active int
		if err := db.QueryRowContext(ctx, `SELECT count(DISTINCT i.incident_id) FROM incident.records i JOIN incident.finding_links l USING(tenant_id,incident_id) JOIN finding.records f USING(tenant_id,finding_id) WHERE i.tenant_id=$1 AND f.namespace=$2 AND i.state IN ('open','acknowledged','mitigating')`, b.TenantID, ns).Scan(&active); err != nil {
			t.Fatal(err)
		}
		if active == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("incidents in %s did not recover within the settle window: %d active", ns, active)
		}
		time.Sleep(3 * time.Second)
	}
	t.Log("actual cluster batch-1 follow-up acceptance: crash loop, OOM, probe failure, stuck rollout and host-port conflict diagnosed on real kubelet/scheduler evidence; recovery via repair, rollout completion and subject deletion settlement")
}
