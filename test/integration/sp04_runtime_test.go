package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	api "ops-platform/gen/api"
	"ops-platform/internal/app"
	"ops-platform/internal/archive"
	"ops-platform/internal/crypto"
	"ops-platform/internal/datascope"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/integrations/openbao"
	"ops-platform/internal/integrations/s3"
	"ops-platform/internal/observability"
	"ops-platform/internal/resource"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSP04FullRuntimeOrbStackOIDCEvidenceAndFailover(t *testing.T) {
	runSP04FullRuntime(t, false)
}

func TestSP04FixtureAdaptersThroughActualRuntime(t *testing.T) {
	runSP04FullRuntime(t, true)
}

func runSP04FullRuntime(t *testing.T, fixtureAdapters bool) {
	if os.Getenv("SP04_TEST_ORBSTACK") != "1" || os.Getenv("SP04_TEST_VM_URL") == "" || os.Getenv("SP03_TEST_OPENBAO_TOKEN") == "" || os.Getenv("SP03_KEYCLOAK_TEST_ISSUER") == "" {
		t.Skip("owned SP04 live dependency environment required")
	}
	ctx, db, dir, dsn := newMigrationDatabase(t)
	if err := goose.UpToContext(ctx, db, dir, 1); err != nil {
		t.Fatal(err)
	}
	if err := runRemainingMigrationsAsMigrationRole(t, ctx, db, dsn, dir); err != nil {
		t.Fatal(err)
	}
	tenant, cluster, source, metricSource := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	collector, namespace := sp04OwnedKubernetes(t, ctx, tenant.String(), source.String())
	token := sp04KeycloakToken(t, ctx, tenant)
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.tenants(tenant_id,slug,display_name) VALUES($1,'sp04-runtime','Runtime')`, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.cluster_registrations(tenant_id,cluster_id,cluster_uid,display_name) VALUES($1,$2,$3,'OrbStack owned source')`, tenant, cluster, collector.ClusterUID); err != nil {
		t.Fatal(err)
	}
	mapping := datascope.Mapping{Scopes: map[string][]string{"cluster": {collector.ClusterUID}, "namespace": {namespace}}}
	rawMapping, _ := json.Marshal(mapping)
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.source_registrations(tenant_id,source_id,cluster_id,source_type,instance_key,auth_ref,backend_logical_id,data_scope_mapping) VALUES($1,$2,$3,'kubernetes','owned-orbstack','openbao://test/collector',$4,$5)`, tenant, source, cluster, collector.BackendLogicalID, rawMapping); err != nil {
		t.Fatal(err)
	}
	mapping.RequiredLabels = map[string]string{"tenant": tenant.String()}
	rawMapping, _ = json.Marshal(mapping)
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.source_registrations(tenant_id,source_id,cluster_id,source_type,instance_key,auth_ref,backend_logical_id,data_scope_mapping) VALUES($1,$2,$3,'victoriametrics','owned-vm','openbao://test/metrics','live-metrics',$4)`, tenant, metricSource, cluster, rawMapping); err != nil {
		t.Fatal(err)
	}
	clusters, _ := json.Marshal([]string{cluster.String()})
	namespaces, _ := json.Marshal([]map[string]string{{"clusterId": cluster.String(), "namespace": namespace}})
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.role_bindings(tenant_id,binding_id,subject,role_name,cluster_scopes,namespace_scopes) VALUES($1,$2,$3,'operator',$4,$5)`, tenant, uuid.New(), token.Subject, clusters, namespaces); err != nil {
		t.Fatal(err)
	}
	workerPool, err := pgxpool.NewWithConfig(ctx, runtimePoolConfig(t, ctx, db, dsn, "worker_runtime_role"))
	if err != nil {
		t.Fatal(err)
	}
	defer workerPool.Close()
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
	bucket := "sp04-chain-" + uuid.NewString()[:8]
	fixture := newRoleTenantS3Fixture(t, []uuid.UUID{tenant}, bucket)
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
	archiveService := &evidence.ArchiveService{Pool: workerPool, Store: store, Protector: protector, BackendLogicalID: "archive-live"}
	podData := fmt.Sprintf(`{"apiVersion":"v1","kind":"Pod","metadata":{"name":"evidence-pod","namespace":%q,"labels":{"app":"sp04"}},"spec":{"containers":[{"name":"pause","image":"registry.k8s.io/pause:3.10.1"}]}}`, namespace)
	var pod struct {
		Metadata struct {
			UID string `json:"uid"`
		}
	}
	json.Unmarshal(sp04Kubectl(t, ctx, []byte(podData), "create", "-f", "-", "-o", "json"), &pod)
	canonical := resource.CanonicalID{Domain: "k8s", Tenant: tenant.String(), Scope: collector.ClusterUID, APIGroup: "core", Kind: "Pod", StableID: pod.Metadata.UID}.String()
	now := time.Now().UTC().Truncate(time.Second)
	metric := fmt.Sprintf("kube_pod_status_phase{tenant=%q,cluster=%q,namespace=%q,uid=%q,phase=\"Running\"} 1 %d\n", tenant, collector.ClusterUID, namespace, pod.Metadata.UID, now.Add(-30*time.Second).UnixMilli())
	response, err := http.Post(os.Getenv("SP04_TEST_VM_URL")+"/api/v1/import/prometheus", "text/plain", strings.NewReader(metric))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode > 299 {
		t.Fatal("live metrics seed failed")
	}
	workerConfig, apiConfig := sp04RuntimeTLS(t, namespace)
	workerConfig.Clusters = []app.SP04Cluster{collector}
	probe := evidence.Query{ResourceCanonicalID: canonical, Namespace: namespace, Template: "pod-phase/v1", From: now.Add(-time.Minute), To: now, Limit: 20}
	workerConfig.Sources = []app.SP04Source{{Name: "victoriametrics", Binding: evidence.Binding{Tenant: tenant.String(), SourceID: metricSource.String(), SourceType: "victoriametrics", Revision: 1, BackendLogicalID: "live-metrics", Endpoint: os.Getenv("SP04_TEST_VM_URL"), ScopeMapping: mapping}, ScopeProbe: &probe}}
	var victoriaUnavailable atomic.Bool
	sourceURL, _ := url.Parse(os.Getenv("SP04_TEST_VM_URL"))
	proxy := httputil.NewSingleHostReverseProxy(sourceURL)
	sourceProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if victoriaUnavailable.Load() {
			w.WriteHeader(503)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	defer sourceProxy.Close()
	workerConfig.Sources[0].Binding.Endpoint = sourceProxy.URL
	var verifyFixtureAdapters func(func(string, string, []byte, string) (int, []byte))
	if fixtureAdapters {
		verifyFixtureAdapters = sp04RuntimeFixtureSources(t, ctx, db, tenant, cluster, collector.ClusterUID, namespace, canonical, &workerConfig)
	}
	runtime, _ := observability.NewRuntime(ctx, "sp04-full-runtime")
	defer runtime.Close(context.Background())
	t.Setenv("SP04_RUNTIME_FILE", sp04ConfigFile(t, workerConfig))
	stopA, err := app.StartSP04Worker(ctx, workerPool, archiveService, runtime)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if stopA != nil {
			stopA()
		}
	}()
	// A second process instance shares only the Kubernetes Lease and route mirror.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	standby := workerConfig
	standby.ListenAddress = address
	standby.OwnerEndpoint = "https://" + address
	t.Setenv("SP04_RUNTIME_FILE", sp04ConfigFile(t, standby))
	stopB, err := app.StartSP04Worker(ctx, workerPool, archiveService, runtime)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if stopB != nil {
			stopB()
		}
	}()
	apiLogin := runtimePoolConfig(t, ctx, db, dsn, "api_runtime_role").ConnConfig.ConnString()
	application, err := app.NewAPI(app.AppConfig{DatabaseURL: apiLogin, OIDCIssuerURL: os.Getenv("SP03_KEYCLOAK_TEST_ISSUER"), ProfilePath: isolatedRuntimeProfile(t, os.Getenv("SP03_KEYCLOAK_TEST_ISSUER"), "", "")})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SP04_RUNTIME_FILE", sp04ConfigFile(t, apiConfig))
	apiListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- application.Serve(runCtx, apiListener, runtime) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	endpoint := "http://" + apiListener.Addr().String()
	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	call := func(method, path string, data []byte, key string) (int, []byte) {
		request, _ := http.NewRequestWithContext(ctx, method, endpoint+path, bytes.NewReader(data))
		request.Header.Set("Authorization", "Bearer "+token.AccessToken)
		request.Header.Set("Idempotency-Key", key)
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err != nil {
			return 0, nil
		}
		defer response.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		return response.StatusCode, raw
	}
	path := "/api/v1/resources/by-canonical-id?canonicalId=" + url.QueryEscape(canonical)
	readyDeadline := time.Now().Add(45 * time.Second)
	var raw []byte
	for {
		status, body := call("GET", path, nil, "")
		raw = body
		if status == 200 {
			break
		}
		if time.Now().After(readyDeadline) {
			route, _ := (graph.Repository{Pool: workerPool}).Load(ctx, tenant.String(), collector.ClusterUID)
			var count int
			_ = db.QueryRowContext(ctx, `SELECT count(*) FROM platform.resource_entities WHERE tenant_id=$1`, tenant).Scan(&count)
			t.Logf("runtime route epoch=%d endpoint=%s identityCount=%d", route.Epoch, route.Endpoint, count)
			t.Fatalf("full runtime never ready status=%d body=%s", status, raw)
		}
		time.Sleep(200 * time.Millisecond)
	}
	var entity struct{ Data graph.Result }
	if err := json.Unmarshal(raw, &entity); err != nil || len(entity.Data.Nodes) != 1 {
		t.Fatalf("entity response invalid: %s", raw)
	}
	// Backend ingestion and scope verification are asynchronous. Wait for real
	// positive/negative adapter proof before measuring admitted queries; no SQL
	// insert or fixture flag may manufacture a capability grant.
	for deadline := time.Now().Add(30 * time.Second); ; {
		var verified int
		if err := db.QueryRowContext(ctx, `SELECT count(DISTINCT source_id) FROM platform.adapter_scope_verifications WHERE tenant_id=$1 AND expires_at>clock_timestamp()`, tenant).Scan(&verified); err != nil {
			t.Fatal(err)
		}
		if verified == len(workerConfig.Sources) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("actual adapter proofs not ready: %d/%d", verified, len(workerConfig.Sources))
		}
		time.Sleep(100 * time.Millisecond)
	}
	if verifyFixtureAdapters != nil {
		verifyFixtureAdapters(call)
		return
	}
	for _, direction := range []string{"in", "out"} {
		status, body := call("GET", "/api/v1/resources/neighbors?canonicalId="+url.QueryEscape(canonical)+"&direction="+direction+"&depth=2&maxNodes=10&maxEdges=10", nil, "")
		var response struct{ Data graph.Result }
		if status != 200 || json.Unmarshal(body, &response) != nil || len(response.Data.Nodes) > 10 || len(response.Data.Edges) > 10 {
			t.Fatalf("neighbor contract %s %d %s", direction, status, body)
		}
		for _, edge := range response.Data.Edges {
			if direction == "out" && edge.From != canonical {
				t.Fatalf("out edge direction mismatch: %+v", edge)
			}
			if direction == "in" && edge.To != canonical {
				t.Fatalf("in edge direction mismatch: %+v", edge)
			}
		}
	}
	status, body := call("GET", "/api/v1/resources/neighbors?canonicalId="+url.QueryEscape(canonical)+"&relationKind=__not_a_relation__", nil, "")
	var filtered struct{ Data graph.Result }
	if status != 200 || json.Unmarshal(body, &filtered) != nil || len(filtered.Data.Nodes) != 1 || len(filtered.Data.Edges) != 0 {
		t.Fatalf("actual relation filter failed: %d %s", status, body)
	}
	status, body = call("GET", "/api/v1/resources/impact-scope?canonicalId="+url.QueryEscape(canonical), nil, "")
	var impact struct{ Data graph.Result }
	if status != 200 || json.Unmarshal(body, &impact) != nil || len(impact.Data.DependencyOnly) == 0 {
		t.Fatalf("actual impact dependency category missing: %d %s", status, body)
	}
	completeRequest, _ := json.Marshal(api.DiagnosticGraphBuildRequest{EntryCanonicalId: canonical, Recipe: api.Incident, Policy: api.GraphBudget{MaxDepth: 2, MaxNodes: 10, MaxEdges: 10, TimeoutMs: 3000}})
	status, body = call("POST", "/api/v1/diagnostic-graphs:build", completeRequest, "sp04-generated-diagnostic")
	if status != 200 {
		t.Fatalf("generated diagnostic request failed: %d %s", status, body)
	}
	firstEpoch := entity.Data.GraphRevision.OwnerEpoch
	if (os.Getenv("OPS_PERFORMANCE_EXEMPTION") == "pre-sp07-user-20261004" || os.Getenv("OPS_PERFORMANCE_EXEMPTION") == "sp05-user-20261002") || os.Getenv("OPS_PERFORMANCE_EXEMPTION") == "sp06-user-20261003" {
		t.Log("SP05/SP06/pre-SP07 explicit user waiver: Graph P95 sampling not executed")
	} else {
		latencies := []time.Duration{}
		for i := 0; i < 30; i++ {
			started := time.Now()
			status, body := call("GET", "/api/v1/resources/neighbors?canonicalId="+url.QueryEscape(canonical), nil, "")
			if status != 200 {
				t.Fatalf("graph request status=%d %s", status, body)
			}
			latencies = append(latencies, time.Since(started))
		}
		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
		p95 := latencies[28]
		t.Logf("actual OIDC API -> mTLS Worker -> OrbStack collector/PG/Ariadne: graph samples=%d P95=%s; graph currently small, not 200-node production qualification", len(latencies), p95)
		if p95 > time.Second {
			t.Fatal("full graph API exceeds P95 budget")
		}
	}
	queryData, _ := json.Marshal(map[string]any{"resourceCanonicalId": canonical, "type": "metric", "queryTemplate": "pod-phase/v1", "timeRange": map[string]any{"from": probe.From, "to": probe.To}, "budget": map[string]int{"timeoutMs": 3000, "maxBytes": 65536}, "limit": 20})
	for _, bad := range []map[string]any{
		{"resourceCanonicalId": canonical, "type": "metric", "queryTemplate": "pod-phase/v1", "timeRange": map[string]any{"from": probe.From, "to": probe.To}, "budget": map[string]int{"timeoutMs": 3000, "maxBytes": 65536}, "limit": 201},
		{"resourceCanonicalId": canonical, "type": "metric", "queryTemplate": "pod-phase/v1", "timeRange": map[string]any{"from": probe.From.Add(-2 * time.Hour), "to": probe.To}, "budget": map[string]int{"timeoutMs": 3000, "maxBytes": 65536}, "limit": 20},
	} {
		raw, _ := json.Marshal(bad)
		status, body := call("POST", "/api/v1/evidence:query", raw, uuid.NewString())
		if status != 429 || !bytes.Contains(body, []byte("BUDGET_EXHAUSTED")) {
			t.Fatalf("actual budget status mismatch: %d %s", status, body)
		}
	}
	started := time.Now()
	status, body = call("POST", "/api/v1/evidence:query", queryData, "sp04-runtime-evidence")
	if status != 200 {
		t.Fatalf("evidence runtime status=%d %s", status, body)
	}
	var result struct{ Data evidence.Result }
	if json.Unmarshal(body, &result) != nil || len(result.Data.Evidence) != 1 || result.Data.Evidence[0].ReplayState != "archived_verified" {
		t.Fatalf("full archived evidence invalid: %s", body)
	}
	t.Logf("actual API->Worker->Victoria->Transit->S3->PostgreSQL query+archive=%s", time.Since(started))
	evidenceID := result.Data.Evidence[0].EvidenceID
	status, body = call("GET", "/api/v1/evidence/"+evidenceID, nil, "")
	if status != 200 {
		t.Fatalf("archive read status=%d %s", status, body)
	}
	status, body = call("POST", "/api/v1/evidence:query", queryData, "sp04-runtime-evidence")
	if status != 200 {
		t.Fatalf("idempotent replay failed %d %s", status, body)
	}
	if json.Unmarshal(body, &result) != nil || result.Data.Evidence[0].EvidenceID != evidenceID {
		t.Fatal("idempotent response changed identity")
	}
	victoriaUnavailable.Store(true)
	status, body = call("POST", "/api/v1/evidence:query", queryData, "sp04-source-degraded-observation")
	victoriaUnavailable.Store(false)
	var partial struct{ Data evidence.Result }
	if status != 200 || json.Unmarshal(body, &partial) != nil || !partial.Data.Partial || len(partial.Data.DegradedSources) == 0 {
		t.Fatalf("source outage not explicit partial2xx: %d %s", status, body)
	}
	observedMetrics := httptest.NewRecorder()
	runtime.Metrics.Handler().ServeHTTP(observedMetrics, httptest.NewRequest("GET", "/metrics", nil))
	for _, sample := range []string{`platform_query_completeness_total{area="evidence",completeness="complete",freshness="fresh"}`, `platform_query_completeness_total{area="evidence",completeness="partial",freshness="unavailable"} 1`, `platform_query_completeness_total{area="graph",completeness=`} {
		if !strings.Contains(observedMetrics.Body.String(), sample) {
			t.Fatalf("actual semantic completeness metric absent: %s", sample)
		}
	}
	t.Log("actual source outage -> HTTP200 partial/unavailable -> semantic completeness counter; complete query and Graph counters verified")
	if (os.Getenv("OPS_PERFORMANCE_EXEMPTION") == "pre-sp07-user-20261004" || os.Getenv("OPS_PERFORMANCE_EXEMPTION") == "sp05-user-20261002") || os.Getenv("OPS_PERFORMANCE_EXEMPTION") == "sp06-user-20261003" {
		t.Log("SP05/SP06/pre-SP07 explicit user waiver: convergence/Evidence/dense capacity latency sampling and P95 not executed; single owned source-change correctness check follows")
		marker := "sp05-correctness"
		sp04Kubectl(t, ctx, nil, "label", "pod", "evidence-pod", "-n", namespace, "sp04-convergence="+marker, "--overwrite")
		for deadline := time.Now().Add(60 * time.Second); ; {
			status, body = call("GET", path, nil, "")
			var observed struct{ Data graph.Result }
			if status == 200 && json.Unmarshal(body, &observed) == nil && len(observed.Data.Nodes) == 1 && observed.Data.Nodes[0].Labels["sp04-convergence"] == marker {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("owned source update never became visible")
			}
			time.Sleep(50 * time.Millisecond)
		}
	} else {
		// Measure source writes through the native API and observe the resulting
		// projection through the actual OIDC API and Active Worker protocol.
		convergence := []time.Duration{}
		for i := 0; i < 30; i++ {
			started := time.Now()
			marker := fmt.Sprintf("sample-%02d", i)
			sp04Kubectl(t, ctx, nil, "label", "pod", "evidence-pod", "-n", namespace, "sp04-convergence="+marker, "--overwrite")
			deadline := time.Now().Add(60 * time.Second)
			for {
				status, body = call("GET", path, nil, "")
				var observed struct{ Data graph.Result }
				if status == 200 && json.Unmarshal(body, &observed) == nil && len(observed.Data.Nodes) == 1 && observed.Data.Nodes[0].Labels["sp04-convergence"] == marker {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("source change did not converge: sample=%d status=%d", i, status)
				}
				time.Sleep(20 * time.Millisecond)
			}
			convergence = append(convergence, time.Since(started))
		}
		sort.Slice(convergence, func(i, j int) bool { return convergence[i] < convergence[j] })
		t.Logf("native source label write -> actual OIDC API/Active Worker projection samples=30 P95=%s; small owned cluster", convergence[28])
		if convergence[28] > 60*time.Second {
			t.Fatal("source convergence gate exceeded")
		}
		evidenceSamples := []time.Duration{}
		for i := 0; i < 30; i++ {
			started := time.Now()
			status, body = call("POST", "/api/v1/evidence:query", queryData, fmt.Sprintf("sp04-semantic-benchmark-%d", i))
			if status != 200 || json.Unmarshal(body, &result) != nil || len(result.Data.Evidence) != 1 || result.Data.Evidence[0].ReplayState != "archived_verified" {
				t.Fatalf("semantic evidence sample %d failed %d %s", i, status, body)
			}
			evidenceSamples = append(evidenceSamples, time.Since(started))
		}
		sort.Slice(evidenceSamples, func(i, j int) bool { return evidenceSamples[i] < evidenceSamples[j] })
		t.Logf("actual semantic Evidence API -> Victoria/Transit/S3/PG samples=30 P95=%s; small owned source", evidenceSamples[28])
		if evidenceSamples[28] > 5*time.Second {
			t.Fatal("semantic Evidence latency gate exceeded")
		}
		// These 199 Pods have a scheduling gate: no fixture container is launched.
		// A Service selector provides exactly 200 connected native resources.
		items := []any{map[string]any{"apiVersion": "v1", "kind": "Service", "metadata": map[string]any{"name": "dense-service", "namespace": namespace}, "spec": map[string]any{"selector": map[string]any{"sp04-dense": "yes"}, "ports": []any{map[string]any{"port": 80}}}}}
		for i := 0; i < 199; i++ {
			items = append(items, map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": fmt.Sprintf("dense-%03d", i), "namespace": namespace, "labels": map[string]any{"sp04-dense": "yes"}}, "spec": map[string]any{"schedulingGates": []any{map[string]any{"name": "ops.platform.test/fixture-only"}}, "containers": []any{map[string]any{"name": "never-launched", "image": "fixture.invalid/never-run@sha256:0000000000000000000000000000000000000000000000000000000000000000"}}}})
		}
		batch, _ := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "List", "items": items})
		sp04Kubectl(t, ctx, batch, "create", "-f", "-")
		var service struct {
			Metadata struct {
				UID string `json:"uid"`
			}
		}
		if json.Unmarshal(sp04Kubectl(t, ctx, nil, "get", "service", "dense-service", "-n", namespace, "-o", "json"), &service) != nil {
			t.Fatal("dense service UID unavailable")
		}
		serviceID := resource.CanonicalID{Domain: "k8s", Tenant: tenant.String(), Scope: collector.ClusterUID, APIGroup: "core", Kind: "Service", StableID: service.Metadata.UID}.String()
		denseRequest, _ := json.Marshal(map[string]any{"entryCanonicalId": serviceID, "recipe": "incident", "policy": map[string]any{"maxDepth": 2, "maxNodes": 200, "maxEdges": 400, "timeoutMs": 3000}})
		denseSamples := []time.Duration{}
		denseDeadline := time.Now().Add(60 * time.Second)
		for len(denseSamples) < 30 {
			started := time.Now()
			status, body = call("POST", "/api/v1/diagnostic-graphs:build", denseRequest, fmt.Sprintf("sp04-dense-%d", len(denseSamples)))
			var dense struct{ Data graph.Result }
			if status == 200 && json.Unmarshal(body, &dense) == nil && len(dense.Data.Nodes) == 200 && len(dense.Data.Edges) == 199 {
				denseSamples = append(denseSamples, time.Since(started))
				continue
			}
			if len(denseSamples) > 0 {
				t.Fatalf("200-node benchmark response failed after warmup status=%d %s", status, body)
			}
			if time.Now().After(denseDeadline) {
				t.Fatalf("200-node live diagnostic gate failed status=%d %s", status, body)
			}
			time.Sleep(100 * time.Millisecond)
		}
		sort.Slice(denseSamples, func(i, j int) bool { return denseSamples[i] < denseSamples[j] })
		t.Logf("actual OIDC API -> mTLS Active Worker -> native Service/199 gated Pods -> upstream diagnostic depth=2 nodes=200 edges=199 samples=30 P95=%s; owned local fixture, not production scale", denseSamples[28])
		if denseSamples[28] > time.Second {
			t.Fatal("200-node API latency gate exceeded")
		}

	}
	route, err := (graph.Repository{Pool: workerPool}).Load(ctx, tenant.String(), collector.ClusterUID)
	if err != nil {
		t.Fatal(err)
	}
	if route.Endpoint == workerConfig.OwnerEndpoint {
		stopA()
		stopA = nil
	} else {
		stopB()
		stopB = nil
	}
	deadline := time.Now().Add(22 * time.Second)
	for {
		status, body = call("GET", path, nil, "")
		if status == 200 {
			json.Unmarshal(body, &entity)
			if entity.Data.GraphRevision.OwnerEpoch > firstEpoch {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("native Lease failover did not complete %d %s", status, body)
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Logf("live active process stop -> qualified standby promoted ownerEpoch %d -> %d", firstEpoch, entity.Data.GraphRevision.OwnerEpoch)
	// Source scope changes invalidate graph access even though the same user
	// still has the namespace grant. Outage alone does not mutate registration.
	if _, err := db.ExecContext(ctx, `UPDATE platform.source_registrations SET revision=revision+1,data_scope_mapping=jsonb_set(data_scope_mapping,'{scopes,namespace}','["withdrawn"]') WHERE tenant_id=$1 AND source_id=$2`, tenant, source); err != nil {
		t.Fatal(err)
	}
	status, body = call("GET", path, nil, "")
	if status != 503 || !bytes.Contains(body, []byte("SOURCE_SCOPE_UNVERIFIED")) {
		t.Fatalf("withdrawn source old graph leaked: %d %s", status, body)
	}
	// Keep the graph source mismatched while separately revoking the Evidence
	// source. A historical object must not bypass an operator scope withdrawal.
	if _, err := db.ExecContext(ctx, `UPDATE platform.source_registrations SET data_scope_mapping=jsonb_set(data_scope_mapping,'{scopes,namespace}','["withdrawn"]') WHERE tenant_id=$1 AND source_id=$2`, tenant, metricSource); err != nil {
		t.Fatal(err)
	}
	status, body = call("GET", "/api/v1/evidence/"+evidenceID, nil, "")
	if status != 403 {
		t.Fatalf("same-revision withdrawn source archive leaked: %d %s", status, body)
	}
	if _, err := db.ExecContext(ctx, `UPDATE platform.role_bindings SET status='disabled',revision=revision+1 WHERE tenant_id=$1 AND subject=$2`, tenant, token.Subject); err != nil {
		t.Fatal(err)
	}
	status, _ = call("GET", path, nil, "")
	if status != 403 {
		t.Fatalf("same signed token after withdrawal status=%d", status)
	}
}
