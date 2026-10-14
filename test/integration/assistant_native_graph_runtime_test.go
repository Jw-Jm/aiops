package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"ops-platform/internal/analytics"
	"ops-platform/internal/app"
	"ops-platform/internal/archive"
	platformcrypto "ops-platform/internal/crypto"
	"ops-platform/internal/datascope"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/integrations/deepflow"
	"ops-platform/internal/integrations/openbao"
	"ops-platform/internal/integrations/s3"
	"ops-platform/internal/observability"
	"ops-platform/internal/resource"
	"ops-platform/internal/source"
)

// Actual native Kubernetes informer/Lease + actual Victoria/Transit. S3 is an
// explicit ancillary protocol fixture; no production archive admission claimed.
func assistantNativeGraphRuntime(t *testing.T, ctx context.Context, db *sql.DB, pool *pgxpool.Pool, apiPool *pgxpool.Pool, b source.BoundSourceContext, bao *openbao.Client) *httpapi.SP04Handlers {
	t.Helper()
	cfg, start, end, proof := readAssistantLiveSource(t)
	graphOnly := os.Getenv("OPS_ASSISTANT_DEEPFLOW_GRAPH_ONLY") == "1"
	if graphOnly {
		cfg.Namespace = "ops-assistant-deepflow-20261013"
	}
	collector, _ := sp04OwnedKubernetes(t, ctx, cfg.TenantID.String(), cfg.SourceID.String())
	collector.BackendLogicalID = "assistant-live-kubernetes"
	collector.QPS = 10
	collector.Burst = 20
	if collector.ClusterUID != cfg.ClusterUID {
		t.Fatal("collector cluster differs from collected live source")
	}
	workerConfig, apiConfig := sp04RuntimeTLS(t, "sp06-test")
	workerConfig.Clusters = []app.SP04Cluster{collector}
	refreshAssistantFixtureCRL(ctx, workerConfig.CRLFile)
	mapping := datascope.Mapping{RequiredLabels: map[string]string{"tenant": cfg.TenantID.String()}, Scopes: map[string][]string{"cluster": {cfg.ClusterUID}, "namespace": {cfg.Namespace}}}
	endpoints := map[string]string{"victoriametrics": "http://127.0.0.1:18428", "victorialogs": "http://127.0.0.1:19428"}
	data, _ := os.ReadFile(filepath.Join(os.Getenv("OPS_ASSISTANT_LIVE_SOURCE_DIR"), "observations.jsonl"))
	uid := ""
	for _, line := range splitJSONLines(data) {
		var row struct {
			Labels map[string]string `json:"labels"`
		}
		if json.Unmarshal(line, &row) == nil && row.Labels["uid"] != "" {
			uid = row.Labels["uid"]
			break
		}
	}
	canonical := resource.CanonicalID{Domain: "k8s", Tenant: cfg.TenantID.String(), Scope: cfg.ClusterUID, APIGroup: "core", Kind: "Pod", StableID: uid}.String()
	sources := map[string]uuid.UUID{"victoriametrics": cfg.MetricsSourceID, "victorialogs": cfg.LogsSourceID}
	if graphOnly {
		sources = map[string]uuid.UUID{}
	}
	for kind, id := range sources {
		binding := evidence.Binding{SourceType: kind, Tenant: cfg.TenantID.String(), SourceID: id.String(), Revision: 1, BackendLogicalID: "assistant-live-" + kind, Endpoint: endpoints[kind], ScopeMapping: mapping}
		raw, _ := json.Marshal(mapping)
		if _, err := db.ExecContext(ctx, `INSERT INTO platform.source_registrations(tenant_id,source_id,cluster_id,source_type,instance_key,auth_ref,backend_logical_id,data_scope_mapping) VALUES($1,$2,$3,$4,$4,'openbao://assistant/live',$5,$6)`, cfg.TenantID, id, b.ClusterID, kind, binding.BackendLogicalID, raw); err != nil {
			t.Fatal(err)
		}
		repo := evidence.Repository{Pool: pool}
		adapter, err := evidence.NewVictoria(kind, binding, http.DefaultClient, repo.Authorize)
		if err != nil {
			t.Fatal(err)
		}
		template := "pod-phase/v1"
		if kind == "victorialogs" {
			template = "resource-logs/v1"
		}
		probe := evidence.Query{ResourceCanonicalID: canonical, Namespace: cfg.Namespace, Template: template, From: end.Add(-time.Minute), To: end, Limit: 200, Scope: graph.Scope{Tenant: cfg.TenantID.String(), Cluster: cfg.ClusterUID, Namespaces: []string{cfg.Namespace}, AuthorizationRevision: "live-source-probe"}}
		if err = repo.VerifyVictoria(ctx, adapter, probe); err != nil {
			t.Fatal("live source verification", kind, err)
		}
		qualifications := []analytics.Qualification{}
		ids := []string{"metrics.pod-cpu", "metrics.pod-restarts", "metrics.pod-memory", "metrics.pod-phase"}
		if kind == "victorialogs" {
			ids = []string{"logs.records"}
		}
		for _, dataset := range ids {
			from := start
			ops := []string{"aggregate", "trend", "compare", "compare_percent"}
			if dataset == "metrics.pod-memory" || dataset == "metrics.pod-phase" {
				ops = append(ops, "average")
			}
			if dataset == "logs.records" {
				ops = append(ops, "samples")
			}
			for _, op := range ops {
				qualifications = append(qualifications, analytics.Qualification{DatasetID: dataset, Operation: op, SourceRevision: 1, BindingDigest: evidence.BindingScopeDigest(binding), EvidenceDigest: proof, CollectorVersion: cfg.CollectorVersion, CoverageFrom: from, CoverageTo: end, VerifiedUntil: time.Now().Add(time.Hour), MaxWindowSeconds: 60})
			}
		}
		workerConfig.Sources = append(workerConfig.Sources, app.SP04Source{Name: kind, Binding: binding, AnalysisQualifications: qualifications, ScopeProbe: &probe})
	}
	if graphOnly {
		var pods struct {
			Items []struct{ Metadata struct{ UID string } }
		}
		raw := sp04Kubectl(t, ctx, nil, "-n", cfg.Namespace, "get", "pods", "-l", "app=network-server,ops.platform.io/canary-version=deployment-v1", "-o", "json")
		if json.Unmarshal(raw, &pods) != nil || len(pods.Items) != 1 {
			t.Fatal("native DeepFlow target missing")
		}
		canonical := resource.CanonicalID{Domain: "k8s", Tenant: b.TenantID.String(), Scope: cfg.ClusterUID, APIGroup: "core", Kind: "Pod", StableID: pods.Items[0].Metadata.UID}.String()
		sourceID := uuid.New()
		scopeMapping := datascope.Mapping{Scopes: map[string][]string{"organization": {"1"}, "cluster": {cfg.ClusterUID}, "namespace": {cfg.Namespace}}}
		encoded, _ := json.Marshal(scopeMapping)
		if _, err := db.ExecContext(ctx, `INSERT INTO platform.source_registrations(tenant_id,source_id,cluster_id,source_type,instance_key,auth_ref,backend_logical_id,data_scope_mapping) VALUES($1,$2,$3,'deepflow','assistant-live-deepflow','openbao://assistant/deepflow','assistant-live-deepflow',$4)`, b.TenantID, sourceID, b.ClusterID, encoded); err != nil {
			t.Fatal(err)
		}
		var domains struct {
			Data []struct {
				LCUUID string `json:"LCUUID"`
			} `json:"DATA"`
		}
		raw, err := os.ReadFile(filepath.Join(os.Getenv("OPS_ASSISTANT_RUNTIME_PRIVATE"), "deepflow-domain.json"))
		if err != nil || json.Unmarshal(raw, &domains) != nil || len(domains.Data) != 1 {
			t.Fatal("native DeepFlow domain unavailable")
		}
		now := time.Now().UTC()
		probe := evidence.Query{ResourceCanonicalID: canonical, Namespace: cfg.Namespace, Template: "GetResourceNetworkHealth", From: now.Add(-20 * time.Second), To: now, Limit: 100}
		l7Probe := probe
		l7Probe.Template = "GetL7Context"
		workerConfig.Sources = append(workerConfig.Sources, app.SP04Source{Name: "deepflow", Mode: "live", L7Context: true, ScopeProbe: &probe, L7ScopeProbe: &l7Probe, Binding: evidence.Binding{SourceType: "deepflow", Tenant: b.TenantID.String(), SourceID: sourceID.String(), Revision: 1, BackendLogicalID: "assistant-live-deepflow", Endpoint: "http://127.0.0.1:21416", SourceRetentionSeconds: 21600, ScopeMapping: scopeMapping}, DeepFlowLive: &deepflow.LiveConfig{ControllerEndpoint: "http://127.0.0.1:21417", DomainLCUUID: domains.Data[0].LCUUID, ClusterUID: cfg.ClusterUID}})
	}
	bucket := "assistant-native-" + uuid.NewString()[:8]
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
	protector, err := platformcrypto.NewTransitProtector(bao, "evidence-archive")
	if err != nil {
		t.Fatal(err)
	}
	archives := &evidence.ArchiveService{Pool: pool, Store: store, Protector: protector, BackendLogicalID: "assistant-native-archive-fixture"}
	workerConfig.ArchiveBackendLogicalID = archives.BackendLogicalID
	runtime, err := observability.NewRuntime(ctx, "assistant-native-sources")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	t.Setenv("SP04_RUNTIME_FILE", sp04ConfigFile(t, workerConfig))
	stop, err := app.StartSP04Worker(ctx, pool, archives, runtime)
	if err != nil {
		t.Fatal("native Kubernetes/source worker", err)
	}
	t.Cleanup(stop)
	t.Setenv("SP04_RUNTIME_FILE", sp04ConfigFile(t, apiConfig))
	handlers, err := app.NewSP04API(ctx, apiPool)
	if err != nil {
		t.Fatal(err)
	}
	handlers.SP05 = &httpapi.SP05Handlers{Pool: apiPool, Enabled: true, GraphAPI: handlers}
	for n := 0; n < 120; n++ {
		route, err := (graph.Repository{Pool: pool}).Load(ctx, cfg.TenantID.String(), cfg.ClusterUID)
		if err == nil && route.Epoch > 0 && route.ExpiresAt.After(time.Now()) {
			proofsReady := true
			for _, configured := range workerConfig.Sources {
				if configured.Name == "deepflow" && (evidence.Repository{Pool: pool}).Authorize(ctx, configured.Binding) != nil {
					proofsReady = false
				}
			}
			if !proofsReady {
				time.Sleep(500 * time.Millisecond)
				continue
			}
			t.Log("actual native Graph informer + Kubernetes Lease route ready; archive dependency fixture explicitly excluded")
			return handlers
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatal("actual native Graph owner did not qualify")
	return nil
}
