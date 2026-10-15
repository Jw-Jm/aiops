package integration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/analytics"
	"ops-platform/internal/datascope"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/resource"
)

type assistantLiveConfig struct {
	TenantID         uuid.UUID `json:"tenantId"`
	ClusterUID       string    `json:"clusterUid"`
	Namespace        string    `json:"namespace"`
	SourceID         uuid.UUID `json:"sourceId"`
	MetricsSourceID  uuid.UUID `json:"metricsSourceId"`
	LogsSourceID     uuid.UUID `json:"logsSourceId"`
	CollectorVersion string    `json:"collectorVersion"`
	CollectorDigest  string    `json:"collectorDigest"`
}

func readAssistantLiveSource(t *testing.T) (assistantLiveConfig, time.Time, time.Time, string) {
	t.Helper()
	var cfg assistantLiveConfig
	path := os.Getenv("OPS_ASSISTANT_LIVE_SOURCE_DIR")
	if path == "" {
		t.Skip("actual remote collected source receipt required")
	}
	raw, err := os.ReadFile(filepath.Join(path, "config.json"))
	if err != nil || json.Unmarshal(raw, &cfg) != nil {
		t.Fatal("live source config unavailable", err)
	}
	raw, err = os.ReadFile(filepath.Join(path, "receipt.json"))
	if err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		Fixture  bool              `json:"fixtureOnly"`
		Started  time.Time         `json:"startedAt"`
		Last     time.Time         `json:"lastObservedAt"`
		Finished time.Time         `json:"finishedAt"`
		Cycles   int               `json:"cycles"`
		Errors   []json.RawMessage `json:"errors"`
		Digest   string            `json:"observationsDigest"`
	}
	if json.Unmarshal(raw, &receipt) != nil || receipt.Fixture || receipt.Finished.IsZero() || receipt.Cycles < 60 || len(receipt.Errors) > 0 || receipt.Last.Sub(receipt.Started) < 5*time.Minute {
		t.Fatal("live collection incomplete/degraded; cannot qualify source")
	}
	data, err := os.ReadFile(filepath.Join(path, "observations.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if receipt.Digest != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Fatal("collected observations digest mismatch")
	}
	return cfg, receipt.Started.UTC(), receipt.Last.UTC(), graph.ScopeDigest(json.RawMessage(raw))
}

func TestAssistantActualCollectedSourcesAndCapacity(t *testing.T) {
	cfg, start, end, proof := readAssistantLiveSource(t)
	ctx, db, pool, b := sp05DatabaseTimed(t, 5*time.Minute, sp05Seed{Tenant: cfg.TenantID, Source: cfg.SourceID, ClusterUID: cfg.ClusterUID, Namespace: cfg.Namespace, Backend: "assistant-live-kubernetes"})
	scope := graph.Scope{Tenant: cfg.TenantID.String(), Cluster: cfg.ClusterUID, Namespaces: []string{cfg.Namespace}, AuthorizationRevision: "live-test-bound-scope"}
	repo := evidence.Repository{Pool: pool}
	adapters := map[string]*analytics.Victoria{}
	mapping := datascope.Mapping{RequiredLabels: map[string]string{"tenant": cfg.TenantID.String()}, Scopes: map[string][]string{"cluster": {cfg.ClusterUID}, "namespace": {cfg.Namespace}}}
	endpoint := map[string]string{"victoriametrics": "http://127.0.0.1:18428", "victorialogs": "http://127.0.0.1:19428"}
	dataset := map[string]string{"metrics.pod-cpu": "cpu_cores", "metrics.pod-restarts": "restart_increase", "metrics.pod-memory": "memory_bytes", "metrics.pod-phase": "phase_count", "logs.records": "log_count"}
	var firstUID string
	data, _ := os.ReadFile(filepath.Join(os.Getenv("OPS_ASSISTANT_LIVE_SOURCE_DIR"), "observations.jsonl"))
	for _, line := range splitJSONLines(data) {
		var row struct {
			Labels map[string]string `json:"labels"`
		}
		if json.Unmarshal(line, &row) == nil && row.Labels["uid"] != "" {
			firstUID = row.Labels["uid"]
			break
		}
	}
	if firstUID == "" {
		t.Fatal("real observed Pod UID absent")
	}
	for kind, sourceID := range map[string]uuid.UUID{"victoriametrics": cfg.MetricsSourceID, "victorialogs": cfg.LogsSourceID} {
		binding := evidence.Binding{SourceType: kind, Tenant: cfg.TenantID.String(), SourceID: sourceID.String(), Revision: 1, BackendLogicalID: "assistant-live-" + kind, Endpoint: endpoint[kind], ScopeMapping: mapping}
		encoded, _ := json.Marshal(mapping)
		if _, err := db.ExecContext(ctx, `INSERT INTO platform.source_registrations(tenant_id,source_id,cluster_id,source_type,instance_key,auth_ref,backend_logical_id,data_scope_mapping) VALUES($1,$2,$3,$4,$4,'openbao://assistant/live',$5,$6)`, cfg.TenantID, sourceID, b.ClusterID, kind, binding.BackendLogicalID, encoded); err != nil {
			t.Fatal(err)
		}
		adapter, err := evidence.NewVictoria(kind, binding, http.DefaultClient, repo.Authorize)
		if err != nil {
			t.Fatal(err)
		}
		template := "pod-phase/v1"
		if kind == "victorialogs" {
			template = "resource-logs/v1"
		}
		canonical := resource.CanonicalID{Domain: "k8s", Tenant: cfg.TenantID.String(), Scope: cfg.ClusterUID, APIGroup: "core", Kind: "Pod", StableID: firstUID}.String()
		if kind == "victorialogs" {
			canonical = actualPositiveLogResource(t, data, cfg, end)
		}
		probe := evidence.Query{ResourceCanonicalID: canonical, Namespace: cfg.Namespace, Template: template, From: end.Add(-time.Minute), To: end, Limit: 200, Scope: scope}
		if err = repo.VerifyVictoria(ctx, adapter, probe); err != nil {
			t.Fatal("actual adapter isolation proof", kind, err)
		}
		qualifications := []analytics.Qualification{}
		for id := range dataset {
			if (kind == "victorialogs") != (id == "logs.records") {
				continue
			}
			from := start
			operations := []string{"aggregate", "trend", "compare", "compare_percent"}
			if id == "metrics.pod-memory" || id == "metrics.pod-phase" {
				operations = append(operations, "average")
			}
			if id == "logs.records" {
				operations = append(operations, "samples")
			}
			for _, op := range operations {
				qualifications = append(qualifications, analytics.Qualification{DatasetID: id, Operation: op, SourceRevision: 1, BindingDigest: evidence.BindingScopeDigest(binding), EvidenceDigest: proof, CollectorVersion: cfg.CollectorVersion, VerifiedUntil: time.Now().Add(time.Hour), CoverageFrom: from, CoverageTo: end, MaxWindowSeconds: 60})
			}
		}
		v, err := analytics.NewVictoria(binding, http.DefaultClient, repo.Authorize, qualifications)
		if err != nil {
			t.Fatal(err)
		}
		adapters[kind] = v
	}
	to := end.Truncate(time.Second).Add(-2 * time.Second)
	window := &analytics.QueryTimeRange{From: to.Add(-60 * time.Second), To: to, Timezone: "Asia/Shanghai"}
	report := map[string]any{"schemaVersion": "assistant-live-source-acceptance/v1", "scope": "actual two-Pod Kubernetes canary only", "modelQualityAcceptance": false, "sourceProofDigest": proof, "collectorVersion": cfg.CollectorVersion, "collectorDigest": cfg.CollectorDigest, "coverageFrom": start, "coverageTo": end, "maxWindowSeconds": 60, "sourceRevision": 1, "queries": map[string]any{}}
	query := func(id, operation string) analytics.QueryResult {
		t.Helper()
		kind := "victoriametrics"
		if id == "logs.records" {
			kind = "victorialogs"
		}
		intent := analytics.QueryIntent{SchemaVersion: "query-intent/v1", DatasetID: id, Operation: operation, Metrics: []string{dataset[id]}, Dimensions: []string{}, Filters: []analytics.Filter{}, Sort: []analytics.Sort{}, Limit: 20, TimeRange: window}
		if id == "metrics.pod-phase" {
			intent.Dimensions = []string{"phase"}
		}
		if operation == "trend" {
			intent.Dimensions = []string{"time"}
			intent.StepSeconds = 60
		}
		if operation == "compare" || operation == "compare_percent" {
			intent.Dimensions = []string{"comparison"}
			intent.TimeRange = &analytics.QueryTimeRange{From: window.To.Add(-30 * time.Second), To: window.To, Timezone: window.Timezone}
			intent.ComparisonTimeRange = &analytics.QueryTimeRange{From: intent.TimeRange.From.Add(-30 * time.Second), To: intent.TimeRange.From, Timezone: window.Timezone}
		}
		if operation == "samples" {
			intent.Limit = 20
		}
		plan, err := adapters[kind].Compile(intent, analytics.Binding{Subject: "actual-source-test", Grant: scope})
		if err != nil {
			t.Fatal(id, operation, err)
		}
		out, err := adapters[kind].Execute(ctx, plan)
		if err != nil {
			t.Fatal(id, operation, err)
		}
		if out.SourceManifest == nil || out.SourceManifest.Revision != 1 || out.SourceManifest.QualificationDigest == "" {
			t.Fatal("source provenance missing")
		}
		return out
	}
	for id := range dataset {
		originalWindow := window
		if id == "metrics.pod-restarts" {
			previous := map[string]float64{}
			var increaseAt time.Time
			for _, line := range splitJSONLines(data) {
				var observation struct {
					Metric    string            `json:"metric"`
					Labels    map[string]string `json:"labels"`
					Value     float64           `json:"value"`
					Timestamp int64             `json:"timestampMs"`
				}
				if json.Unmarshal(line, &observation) != nil {
					t.Fatal("invalid observation")
				}
				if observation.Metric != "kube_pod_container_status_restarts_total" {
					continue
				}
				key := observation.Labels["uid"] + "/" + observation.Labels["container"]
				if prior, ok := previous[key]; ok && observation.Value > prior {
					increaseAt = time.UnixMilli(observation.Timestamp).UTC()
				}
				previous[key] = observation.Value
			}
			if increaseAt.IsZero() {
				t.Fatal("native restart transition absent")
			}
			to := increaseAt.Add(10 * time.Second).Truncate(time.Second)
			if to.After(end) {
				to = end.Truncate(time.Second)
			}
			window = &analytics.QueryTimeRange{From: to.Add(-time.Minute), To: to, Timezone: originalWindow.Timezone}
			report["restartTransitionAt"] = increaseAt
		}
		out := query(id, "aggregate")
		if out.Status != "complete" || !out.Exact || len(out.Rows) == 0 || (id != "metrics.pod-phase" && len(out.Rows) != 1) || math.IsNaN(out.Rows[0].Value) {
			t.Fatal("live aggregate incomplete", id, out)
		}
		if id == "metrics.pod-cpu" && (out.Rows[0].Value <= 0 || out.Rows[0].Value > .4) {
			t.Fatal("actual CPU exceeds two 200m canary limits", out.Rows)
		}
		if id == "metrics.pod-restarts" && out.Rows[0].Value <= 0 {
			t.Fatal("actual canary restart not observed in acceptance window")
		}
		if id == "logs.records" {
			golden := 0
			for _, line := range splitJSONLines(data) {
				var record struct {
					Type string `json:"type"`
					Row  struct {
						Time      time.Time `json:"_time"`
						Tenant    string    `json:"tenant"`
						Cluster   string    `json:"cluster"`
						Namespace string    `json:"namespace"`
						UID       string    `json:"uid"`
					} `json:"row"`
				}
				if json.Unmarshal(line, &record) != nil {
					t.Fatal("invalid raw observation")
				}
				if record.Type == "log" && !record.Row.Time.Before(window.From) && record.Row.Time.Before(window.To) {
					if record.Row.Tenant != scope.Tenant || record.Row.Cluster != scope.Cluster || record.Row.Namespace != cfg.Namespace || record.Row.UID == "" {
						t.Fatal("raw log identity mismatch")
					}
					golden++
				}
			}
			if out.Rows[0].Value != float64(golden) {
				t.Fatalf("actual log count %v differs from raw independent count %d", out.Rows[0].Value, golden)
			}
			report["logCountGolden"] = golden
		}
		report["queries"].(map[string]any)[id] = out
		window = originalWindow
	}
	for _, id := range []string{"metrics.pod-cpu", "metrics.pod-restarts", "logs.records"} {
		report["queries"].(map[string]any)[id+"/trend"] = query(id, "trend")
	}
	for _, id := range []string{"metrics.pod-cpu", "logs.records"} {
		out := query(id, "compare")
		if out.Exact && (len(out.Rows) != 3 || math.Abs(out.Rows[2].Value-(out.Rows[1].Value-out.Rows[0].Value)) > 1e-9) {
			t.Fatal("comparison arithmetic")
		}
		report["queries"].(map[string]any)[id+"/compare"] = out
	}
	samples := query("logs.records", "samples")
	if len(samples.Samples) == 0 {
		t.Fatal("actual log samples absent")
	}
	if strings.Contains(string(rawResultForLive(samples)), "password=canary-only") {
		t.Fatal("canary secret not redacted")
	}
	report["queries"].(map[string]any)["logs.records/samples"] = samples
	// Bounded concurrent source measurement; does not infer production capacity.
	var mu sync.Mutex
	durations := []time.Duration{}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 25; n++ {
				before := time.Now()
				_ = query("logs.records", "aggregate")
				mu.Lock()
				durations = append(durations, time.Since(before))
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	report["capacity"] = map[string]any{"p95Nanoseconds": durations[94], "p50Nanoseconds": durations[49], "requests": len(durations), "concurrency": 4, "latenciesNanoseconds": durations, "scopePods": 2, "sourceFaultsInjected": false}
	// Current registration changes invalidate both actual runtime and capability metadata.
	if _, err := db.ExecContext(ctx, `UPDATE platform.source_registrations SET revision=revision+1 WHERE tenant_id=$1 AND source_id=$2`, cfg.TenantID, cfg.LogsSourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := adapters["victorialogs"].Capabilities(ctx, scope); err == nil {
		t.Fatal("changed source still qualified")
	}
	report["revisionRevocationRejected"] = true
	output := os.Getenv("OPS_ASSISTANT_LIVE_REPORT")
	if output == "" {
		t.Fatal("owned report path required")
	}
	raw, _ := json.MarshalIndent(report, "", "  ")
	if err := os.WriteFile(output, raw, 0600); err != nil {
		t.Fatal(err)
	}
}
func splitJSONLines(data []byte) [][]byte {
	out := [][]byte{}
	start := 0
	for i, c := range data {
		if c == '\n' {
			if i > start {
				out = append(out, data[start:i])
			}
			start = i + 1
		}
	}
	return out
}

func rawResultForLive(v any) []byte { b, _ := json.Marshal(v); return b }

// The log isolation canary must be an actually observed log-producing UID.
// An idle client with no logs cannot qualify or disqualify a different server.
func actualPositiveLogResource(t *testing.T, data []byte, cfg assistantLiveConfig, end time.Time) string {
	t.Helper()
	for _, line := range splitJSONLines(data) {
		var entry struct {
			Type string            `json:"type"`
			Row  map[string]string `json:"row"`
		}
		if json.Unmarshal(line, &entry) != nil || entry.Type != "log" {
			continue
		}
		row := entry.Row
		at, err := time.Parse(time.RFC3339Nano, row["_time"])
		if err != nil || at.Before(end.Add(-time.Minute)) || !at.Before(end) || row["namespace"] != cfg.Namespace || row["tenant"] != cfg.TenantID.String() || row["cluster"] != cfg.ClusterUID || row["uid"] == "" {
			continue
		}
		return resource.CanonicalID{Domain: "k8s", Tenant: cfg.TenantID.String(), Scope: cfg.ClusterUID, APIGroup: "core", Kind: "Pod", StableID: row["uid"]}.String()
	}
	t.Fatal("actual positive log UID missing in probe window")
	return ""
}
