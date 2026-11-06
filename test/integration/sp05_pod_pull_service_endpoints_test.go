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
	"net/url"
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

// Batch-1 acceptance: a real bad-image Pod and a real backend-less Service
// traverse the native collection -> finding -> incident -> specialized Recipe
// chain. Each scenario is verified in its evidence-sufficient, evidence-
// insufficient and recovered state on the live cluster.
func TestSP05ActualPodImagePullAndServiceNoEndpointsDiagnosis(t *testing.T) {
	for _, key := range []string{"SP03_TEST_DATABASE_URL", "SP03_KEYCLOAK_TEST_ISSUER", "SP03_TEST_OPENBAO_TOKEN", "SP03_TEST_OPENBAO_CA_FILE"} {
		if os.Getenv(key) == "" {
			t.Fatalf("live gate dependency missing: %s", key)
		}
	}
	ownedTenant, ownedSource := uuid.New(), uuid.New()
	collector, ns := sp04OwnedKubernetes(t, t.Context(), ownedTenant.String(), ownedSource.String())
	ctx, db, workerPool, b := sp05DatabaseTimed(t, 22*time.Minute, sp05Seed{ClusterUID: collector.ClusterUID, Namespace: ns, Backend: collector.BackendLogicalID, Tenant: ownedTenant, Source: ownedSource})
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
	for _, name := range []string{"node-failure", "pvc-csi-failure", "pod-image-pull-failure", "service-no-endpoints"} {
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
	bucket := "sp05-batch1-" + uuid.NewString()[:8]
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

	// Three native fault subjects, one injector per recipe state:
	//   pull-a: unreachable registry -> symptom facts AND kubelet pull events.
	//   pull-b: Never policy + absent image -> symptom facts, no pull event.
	//   svc-empty: selector matches nothing -> symptom facts, no backend path.
	pullA := fmt.Sprintf(`{"apiVersion":"v1","kind":"Pod","metadata":{"name":"batch1-pull-a","namespace":%q,"labels":{"app":"batch1-pull-a"}},"spec":{"containers":[{"name":"app","image":"registry.example.invalid/batch1/frontend:v1"}]}}`, ns)
	pullB := fmt.Sprintf(`{"apiVersion":"v1","kind":"Pod","metadata":{"name":"batch1-pull-b","namespace":%q,"labels":{"app":"batch1-pull-b"}},"spec":{"containers":[{"name":"app","image":"registry.example.invalid/batch1/never:v1","imagePullPolicy":"Never"}]}}`, ns)
	svcA := fmt.Sprintf(`{"apiVersion":"v1","kind":"Service","metadata":{"name":"batch1-svc-a","namespace":%q},"spec":{"selector":{"app":"batch1-pull-a"},"ports":[{"port":80,"targetPort":80}]}}`, ns)
	svcEmpty := fmt.Sprintf(`{"apiVersion":"v1","kind":"Service","metadata":{"name":"batch1-svc-empty","namespace":%q},"spec":{"selector":{"app":"batch1-nonexistent"},"ports":[{"port":80,"targetPort":80}]}}`, ns)
	podAUID := struct {
		Metadata struct {
			UID string `json:"uid"`
		}
	}{}
	if json.Unmarshal(sp04Kubectl(t, ctx, []byte(pullA), "create", "-f", "-", "-o", "json"), &podAUID) != nil {
		t.Fatal("pod pull-a creation response")
	}
	sp04Kubectl(t, ctx, []byte(pullB), "create", "-f", "-")
	sp04Kubectl(t, ctx, []byte(svcA), "create", "-f", "-")
	sp04Kubectl(t, ctx, []byte(svcEmpty), "create", "-f", "-")

	collector.Namespaces = []string{ns}
	workerConfig, apiConfig := sp04RuntimeTLS(t, ns)
	workerConfig.Clusters = []app.SP04Cluster{collector}
	sp05 := &app.SP05Config{Enabled: true, IngestionBindings: []httpapi.IngestionBinding{{Tenant: b.TenantID.String(), Subject: token.Subject, SourceID: b.SourceID.String(), RegistrationRevision: 1, CredentialRevision: 1}}}
	workerConfig.SP05 = sp05
	apiConfig.SP05 = sp05
	runtime, err := observability.NewRuntime(ctx, "sp05-batch1-live")
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
	canonical := func(kind, uid string) string {
		return fmt.Sprintf("k8s+v1://%s/%s/core/%s/%s", b.TenantID, b.ClusterUID, kind, uid)
	}
	awaitIncident := func(resource string) string {
		t.Helper()
		for deadline := time.Now().Add(120 * time.Second); ; {
			var incidentID string
			if err := db.QueryRowContext(ctx, `SELECT l.incident_id::text FROM incident.finding_links l JOIN incident.records i USING(tenant_id,incident_id) JOIN finding.records f USING(tenant_id,finding_id) WHERE l.tenant_id=$1 AND f.resource_canonical_id=$2 AND i.state<>'closed' ORDER BY i.created_at DESC LIMIT 1`, b.TenantID, resource).Scan(&incidentID); err == nil && incidentID != "" {
				return incidentID
			}
			if time.Now().After(deadline) {
				t.Fatalf("no live incident reached for %s", resource)
			}
			time.Sleep(500 * time.Millisecond)
		}
	}
	type rcaView struct {
		Candidate string
		Recipe    string
		Status    string
		Missing   []string
	}
	awaitRCA := func(incidentID string, accept func(rcaView) bool, describe string) rcaView {
		t.Helper()
		var last rcaView
		for deadline := time.Now().Add(150 * time.Second); ; {
			status, body := call("GET", "/api/v1/incidents/" + incidentID + "/rca")
			var response struct {
				Data struct {
					Eligible  bool `json:"currentEligible"`
					Revision_ struct {
						Result struct {
							Status     string `json:"status"`
							Candidates []struct {
								Type       string   `json:"candidateType"`
								Provenance []string `json:"provenance"`
							} `json:"candidates"`
							Bundle struct {
								Missing []string `json:"missing"`
							} `json:"evidenceBundle"`
						} `json:"result"`
					} `json:"revision"`
				} `json:"data"`
			}
			if status == 200 && json.Unmarshal(body, &response) == nil && response.Data.Eligible && len(response.Data.Revision_.Result.Candidates) > 0 {
				c := response.Data.Revision_.Result.Candidates[0]
				last = rcaView{Candidate: c.Type, Recipe: c.Provenance[0], Status: response.Data.Revision_.Result.Status, Missing: response.Data.Revision_.Result.Bundle.Missing}
				if accept(last) {
					return last
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: last view %+v", describe, last)
			}
			time.Sleep(time.Second)
		}
	}
	pullACanonical := canonical("Pod", podAUID.Metadata.UID)
	// RCA confirmation is deliberately conservative while collection health
	// settles; wait for the same fresh graph gate the PVC chain uses.
	for deadline := time.Now().Add(180 * time.Second); ; {
		status, body := call("GET", "/api/v1/resources/by-canonical-id?canonicalId="+url.QueryEscape(pullACanonical))
		var response struct {
			Data struct {
				Freshness string `json:"freshness"`
				Partial   bool   `json:"partial"`
			} `json:"data"`
		}
		if status == 200 && json.Unmarshal(body, &response) == nil && response.Data.Freshness == "fresh" && !response.Data.Partial {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("collection health did not settle: %d %s", status, body)
		}
		time.Sleep(time.Second)
	}
	incidentPullA := awaitIncident(pullACanonical)
	view := awaitRCA(incidentPullA, func(v rcaView) bool {
		return v.Status == "confirmed" && v.Candidate == "pod_image_pull_failure" && v.Recipe == "pod-image-pull-failure/v1"
	}, "pull-a confirmed with kubelet event evidence")
	t.Logf("pull-a sufficient: %+v", view)

	// Never-policy pod: the pull symptom is proven, the kubelet pull error is
	// not, so the specialized recipe must stay unconfirmed and name the gap.
	pullBRaw := sp04Kubectl(t, ctx, nil, "get", "pod", "-n", ns, "batch1-pull-b", "-o", "json")
	var pullBPod struct {
		Metadata struct {
			UID string `json:"uid"`
		} `json:"metadata"`
	}
	if json.Unmarshal(pullBRaw, &pullBPod) != nil {
		t.Fatal("pull-b uid")
	}
	incidentPullB := awaitIncident(canonical("Pod", pullBPod.Metadata.UID))
	viewB := awaitRCA(incidentPullB, func(v rcaView) bool {
		return v.Candidate == "pod_image_pull_failure" && contains(v.Missing, "image-pull-error")
	}, "pull-b evidence-insufficient without pull event")
	if viewB.Status == "confirmed" {
		t.Fatalf("pull-b must not confirm: %+v", viewB)
	}
	t.Logf("pull-b insufficient: %+v", viewB)

	// Backend-less service with a selector matching nothing: symptom present,
	// selector path and backend cause both missing.
	svcEmptyUID := sp04Kubectl(t, ctx, nil, "get", "service", "-n", ns, "batch1-svc-empty", "-o", "jsonpath={.metadata.uid}")
	incidentSvcEmpty := awaitIncident(canonical("Service", string(svcEmptyUID)))
	viewEmpty := awaitRCA(incidentSvcEmpty, func(v rcaView) bool {
		return contains(v.Missing, "service-selection") && contains(v.Missing, "backend-unready")
	}, "svc-empty evidence-insufficient without selection")
	t.Logf("svc-empty insufficient: %+v", viewEmpty)

	// Service with an unready image-pull backend: slice symptom, selector path
	// and the backend cause vote must confirm together.
	svcAUID := sp04Kubectl(t, ctx, nil, "get", "service", "-n", ns, "batch1-svc-a", "-o", "jsonpath={.metadata.uid}")
	incidentSvcA := awaitIncident(canonical("Service", string(svcAUID)))
	viewSvcA := awaitRCA(incidentSvcA, func(v rcaView) bool {
		return v.Status == "confirmed" && v.Candidate == "service_no_ready_endpoints" && v.Recipe == "service-no-endpoints/v1"
	}, "svc-a confirmed via selected unready backend")
	t.Logf("svc-a sufficient: %+v", viewSvcA)

	// Recovery: repair the registry image, wait until the pause image is local,
	// then repair the local-only pod and the dead selector. All four incidents
	// must cross the settle window to resolved.
	sp04Kubectl(t, ctx, nil, "set", "image", "pod", "-n", ns, "batch1-pull-a", "app=registry.aliyuncs.com/google_containers/pause:3.10.2")
	for deadline := time.Now().Add(120 * time.Second); ; {
		phase := string(sp04Kubectl(t, ctx, nil, "get", "pod", "-n", ns, "batch1-pull-a", "-o", "jsonpath={.status.phase}"))
		if phase == "Running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pull-a did not run after image repair: %q", phase)
		}
		time.Sleep(2 * time.Second)
	}
	// pull-b keeps its Never policy; the pause image is now present locally.
	sp04Kubectl(t, ctx, nil, "set", "image", "pod", "-n", ns, "batch1-pull-b", "app=registry.aliyuncs.com/google_containers/pause:3.10.2")
	sp04Kubectl(t, ctx, nil, "patch", "service", "-n", ns, "batch1-svc-empty", "--type=merge", "-p", `{"spec":{"selector":{"app":"batch1-pull-a"}}}`)
	// Recovery is clocked by the settle window after the last finding
	// resolves; all four incidents share one bounded wait.
	pending := map[string]bool{incidentPullA: true, incidentPullB: true, incidentSvcA: true, incidentSvcEmpty: true}
	for deadline := time.Now().Add(10 * time.Minute); ; {
		rows, err := db.QueryContext(ctx, `SELECT incident_id::text FROM incident.records WHERE tenant_id=$1 AND incident_id::text = ANY($2::text[]) AND state='resolved'`, b.TenantID, keys(pending))
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			delete(pending, id)
		}
		rows.Close()
		if len(pending) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("incidents did not recover within the settle window: %v", keys(pending))
		}
		time.Sleep(2 * time.Second)
	}
	t.Log("actual cluster batch-1 acceptance: pod image pull + service endpoint diagnosis, evidence sufficient/insufficient/recovered on real kubelet events, EndpointSlices and graph selection")
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
