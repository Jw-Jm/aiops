package integration

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/datascope"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/integrations/deepflow"
	"ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/resource"
)

type assistantNativeTokenTransport struct {
	base  http.RoundTripper
	token string
}

// Log only request classes and timings, never headers, bodies or query values.
type assistantIdentityTimingTransport struct {
	base http.RoundTripper
	t    *testing.T
}

func (a assistantIdentityTimingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	started := time.Now()
	response, err := a.base.RoundTrip(r)
	status := 0
	if response != nil {
		status = response.StatusCode
	}
	a.t.Log("identity request", r.Method, r.URL.Path, "elapsedMs", time.Since(started).Milliseconds(), "status", status, "failed", err != nil)
	return response, err
}

func (a assistantNativeTokenTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	copy := r.Clone(r.Context())
	copy.Header = r.Header.Clone()
	copy.Header.Set("Authorization", "Bearer "+a.token)
	return a.base.RoundTrip(copy)
}
func TestAssistantActualDeepFlowL4AndL7(t *testing.T) {
	if os.Getenv("OPS_ASSISTANT_DEEPFLOW_LIVE") != "1" {
		t.Skip("actual isolated remote DeepFlow source required")
	}
	cfg, _, _, _ := readAssistantLiveSource(t)
	const namespace = "ops-assistant-deepflow-20261013"
	ctx, db, pool, b := sp05DatabaseTimed(t, 8*time.Minute, sp05Seed{Tenant: cfg.TenantID, Source: cfg.SourceID, ClusterUID: cfg.ClusterUID, Namespace: namespace, Backend: "assistant-live-kubernetes"})
	collector, _ := sp04OwnedKubernetes(t, ctx, b.TenantID.String(), b.SourceID.String())
	ca, err := os.ReadFile(collector.CAFile)
	if err != nil {
		t.Fatal(err)
	}
	token, err := os.ReadFile(collector.TokenFile)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(ca)
	native, err := kubernetes.NewClient(collector.Endpoint, &http.Client{Transport: assistantNativeTokenTransport{&http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}, strings.TrimSpace(string(token))}}, 20, 20)
	if err != nil {
		t.Fatal(err)
	}
	sourceID := uuid.New()
	mapping := datascope.Mapping{Scopes: map[string][]string{"organization": {"1"}, "cluster": {cfg.ClusterUID}, "namespace": {namespace}}}
	raw, _ := json.Marshal(mapping)
	if _, err = db.ExecContext(ctx, `INSERT INTO platform.source_registrations(tenant_id,source_id,cluster_id,source_type,instance_key,auth_ref,backend_logical_id,data_scope_mapping) VALUES($1,$2,$3,'deepflow','assistant-live-deepflow','openbao://assistant/deepflow','assistant-live-deepflow',$4)`, b.TenantID, sourceID, b.ClusterID, raw); err != nil {
		t.Fatal(err)
	}
	binding := evidence.Binding{SourceType: "deepflow", Tenant: cfg.TenantID.String(), SourceID: sourceID.String(), Revision: 1, BackendLogicalID: "assistant-live-deepflow", Endpoint: "http://127.0.0.1:21416", SourceRetentionSeconds: 21600, ScopeMapping: mapping}
	repo := evidence.Repository{Pool: pool}
	var domains struct {
		Data []struct {
			LCUUID string `json:"LCUUID"`
		} `json:"DATA"`
	}
	raw, err = os.ReadFile(filepath.Join(os.Getenv("OPS_ASSISTANT_RUNTIME_PRIVATE"), "deepflow-domain.json"))
	if err != nil || json.Unmarshal(raw, &domains) != nil || len(domains.Data) != 1 {
		t.Fatal("actual isolated domain proof unavailable")
	}
	mapper, err := deepflow.NewLiveMapper(deepflow.LiveConfig{ControllerEndpoint: "http://127.0.0.1:21417", DomainLCUUID: domains.Data[0].LCUUID, ClusterUID: cfg.ClusterUID}, binding, native, http.DefaultClient, repo.CheckBinding)
	if err != nil {
		t.Fatal(err)
	}
	var pod struct{ Metadata struct{ UID string } }
	list := struct {
		Items []json.RawMessage `json:"items"`
	}{}
	raw = sp04Kubectl(t, ctx, nil, "-n", namespace, "get", "pods", "-l", "app=network-server,ops.platform.io/canary-version=deployment-v1", "-o", "json")
	if json.Unmarshal(raw, &list) != nil || len(list.Items) != 1 {
		t.Fatal("actual unique deployment canary unavailable")
	}
	raw = list.Items[0]
	if json.Unmarshal(raw, &pod) != nil || pod.Metadata.UID == "" {
		t.Fatal("actual canary Pod unavailable")
	}
	canonical := resource.CanonicalID{Domain: "k8s", Tenant: cfg.TenantID.String(), Scope: cfg.ClusterUID, APIGroup: "core", Kind: "Pod", StableID: pod.Metadata.UID}.String()
	var endpoint deepflow.Endpoint
	for attempt := 0; attempt < 100; attempt++ {
		endpoint, err = mapper.ByCanonical(ctx, canonical)
		if err == nil {
			break
		}
		if attempt%10 == 0 {
			t.Log("actual native/backend identity not ready", err)
		}
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		t.Fatal("actual UID/container mapping", err)
	}
	from := time.Now().UTC().Add(-time.Minute)
	if from.Before(endpoint.IdentityValidFrom) {
		from = endpoint.IdentityValidFrom.Add(time.Second)
	}
	query := evidence.Query{ResourceCanonicalID: canonical, Namespace: namespace, Template: "GetResourceNetworkHealth", From: from, To: time.Now().UTC(), Limit: 100, Scope: graph.Scope{Tenant: cfg.TenantID.String(), Cluster: cfg.ClusterUID, Namespaces: []string{namespace}, AuthorizationRevision: "actual-deepflow-source-probe"}}
	candidate, err := deepflow.New(binding, http.DefaultClient, mapper, repo.CheckBinding, false, "live")
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 90; n++ {
		query.To = time.Now().UTC()
		err = repo.VerifyAdapter(ctx, binding, candidate, query)
		if err == nil {
			break
		}
		if n%10 == 0 {
			result, e := candidate.Query(ctx, query)
			t.Log("actual L4 proof pending", e, result.Warnings, len(result.Evidence))
		}
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		t.Fatal("actual scoped L4 proof", err)
	}
	adapter, err := deepflow.New(binding, http.DefaultClient, mapper, repo.Authorize, true, "live")
	if err != nil {
		t.Fatal(err)
	}
	results := map[string]evidence.Result{}
	for _, operation := range []string{"GetResourceNetworkHealth", "GetNetworkDependencies", "GetNetworkPath", "FindTCPRetransmission", "FindConnectionFailure", "FindPacketLoss"} {
		query.Template = operation
		result, err := adapter.Query(ctx, query)
		if err != nil || result.Freshness == "unavailable" {
			t.Fatal("actual L4 query", operation, err, result.Warnings)
		}
		if (operation == "GetResourceNetworkHealth" || operation == "GetNetworkDependencies") && len(result.Evidence) == 0 {
			t.Fatal("actual L4 observations absent")
		}
		results[operation] = result
	}
	for n := 0; n < 90; n++ {
		query.To = time.Now().UTC()
		err = adapter.QualifyL7(ctx, query)
		if err == nil {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		t.Fatal("actual separate L7 proof", err)
	}
	query.Template = "GetL7Context"
	result, err := adapter.Query(ctx, query)
	if err != nil || result.Partial || len(result.Evidence) == 0 {
		t.Fatal("actual L7 query", err, result.Warnings)
	}
	results[query.Template] = result
	codes := map[uint64]bool{}
	for _, fact := range result.Evidence {
		var rows []struct {
			ResponseCode uint64 `json:"responseCode"`
			From, To     string
		}
		if json.Unmarshal(fact.Data, &rows) != nil {
			t.Fatal("L7 projection")
		}
		if strings.Contains(string(fact.Data), "canary-only") {
			t.Fatal("request parameters leaked")
		}
		for _, row := range rows {
			codes[row.ResponseCode] = true
			if !query.Scope.Allows(row.From, namespace) || !query.Scope.Allows(row.To, namespace) {
				t.Fatal("cross-scope endpoint")
			}
		}
	}
	if !codes[200] || !codes[503] {
		t.Fatal("actual HTTP success/error stimuli absent", codes)
	}
	capabilities, err := adapter.ScopedCapabilities(ctx, query.Scope)
	if err != nil || capabilities["network"] != "live" || capabilities["l7"] != "live" || capabilities["trace"] != "CAPABILITY_DISABLED" {
		t.Fatal("actual source capabilities", capabilities, err)
	}
	query.Template = "GetTraceContext"
	if _, err = adapter.Query(ctx, query); err != deepflow.ErrDisabled {
		t.Fatal("Trace enabled")
	}
	query.Template = "GetL7Context"
	query.Namespace = "outside"
	if _, err = adapter.Query(ctx, query); err == nil {
		t.Fatal("outside namespace accepted")
	}
	query.Namespace = namespace
	if _, err = db.ExecContext(ctx, `UPDATE platform.source_registrations SET revision=revision+1 WHERE tenant_id=$1 AND source_id=$2`, b.TenantID, sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err = adapter.Query(ctx, query); err == nil {
		t.Fatal("withdrawn source revision accepted")
	}
	raw, _ = json.MarshalIndent(map[string]any{"schemaVersion": "assistant-deepflow-live-acceptance/v1", "passed": true, "fixtureOnly": false, "modelQualityAcceptance": false, "scope": "one native cluster, isolated canary namespace, actual TCP/HTTP stimuli", "tenantId": cfg.TenantID, "clusterUid": cfg.ClusterUID, "namespace": namespace, "sourceId": sourceID, "nativePodUid": pod.Metadata.UID, "identityValidFrom": endpoint.IdentityValidFrom, "capabilities": capabilities, "httpStimuli": []int{200, 503}, "results": results, "sourceRevisionRevocationRejected": true}, "", "  ")
	output := os.Getenv("OPS_ASSISTANT_DEEPFLOW_REPORT")
	if output == "" || os.WriteFile(output, raw, 0600) != nil {
		t.Fatal("owned acceptance report unavailable")
	}
}
