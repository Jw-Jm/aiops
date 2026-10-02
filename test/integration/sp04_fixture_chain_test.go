package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/app"
	"ops-platform/internal/datascope"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/integrations/deepflow"
	"ops-platform/internal/integrations/redfish"
	"ops-platform/internal/resource"
)

// This exercises fixture sources through the same startup, source proof, OIDC,
// signed internal protocol, collectors and encrypted archive as live metrics.
// It does not turn frozen vendor/Querier data into a live qualification.
func sp04RuntimeFixtureSources(t *testing.T, ctx context.Context, db *sql.DB, tenant, cluster uuid.UUID, clusterUID, namespace, podID string, config *app.SP04Config) func(func(string, string, []byte, string) (int, []byte)) {
	t.Helper()
	raw, err := os.ReadFile("../fixtures/redfish/dell/inventory.json")
	if err != nil {
		t.Fatal(err)
	}
	var routes map[string]json.RawMessage
	if err := json.Unmarshal(raw, &routes); err != nil {
		t.Fatal(err)
	}
	rf := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "fixture-reader" || password != "fixture-only" {
			w.WriteHeader(401)
			return
		}
		if r.Method != "GET" {
			t.Error("Redfish write attempted")
			w.WriteHeader(405)
			return
		}
		body, ok := routes[r.URL.Path]
		if !ok {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
	t.Cleanup(rf.Close)
	dir := t.TempDir()
	caFile, credentialFile := filepath.Join(dir, "redfish-ca.pem"), filepath.Join(dir, "redfish-credential.json")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rf.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credentialFile, []byte(`{"username":"fixture-reader","password":"fixture-only"}`), 0600); err != nil {
		t.Fatal(err)
	}
	initial, err := redfish.Collect(ctx, redfish.Config{Endpoint: rf.URL, Tenant: tenant.String(), Scope: clusterUID, SourceID: uuid.NewString(), Username: "fixture-reader", Password: "fixture-only", Client: rf.Client()})
	if err != nil || initial.Partial || len(initial.Entities) != 7 {
		t.Fatalf("TLS fixture setup failed: %+v %v", initial, err)
	}
	dfRaw, err := os.ReadFile("../fixtures/deepflow/v7.2.0/l4-flow-response.json")
	if err != nil {
		t.Fatal(err)
	}
	df := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/query/" || r.Header.Get("X-Org-Id") != "2" {
			t.Error("unexpected Querier contract")
			w.WriteHeader(400)
			return
		}
		if err := r.ParseForm(); err != nil || r.Form.Get("db") != "flow_log" || !strings.Contains(r.Form.Get("sql"), "pod_id_0 = 1") {
			t.Error("unbound Querier request")
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(dfRaw)
	}))
	t.Cleanup(df.Close)
	hardwareID := resource.CanonicalID{Domain: "hardware", Tenant: tenant.String(), Scope: clusterUID, APIGroup: "redfish", Kind: "PhysicalServer", StableID: "550e8400-e29b-41d4-a716-446655440000"}.String()
	now := time.Now().UTC()
	hardwareProbe := evidence.Query{ResourceCanonicalID: hardwareID, Template: "hardware-inventory/v1", From: now.Add(-time.Minute), To: now, Limit: 20}
	networkProbe := evidence.Query{ResourceCanonicalID: podID, Namespace: namespace, Template: "GetResourceNetworkHealth", From: time.Unix(1790812770, 0).UTC(), To: time.Unix(1790812830, 0).UTC(), Limit: 20}
	rfID, dfID := uuid.New(), uuid.New()
	for _, source := range []app.SP04Source{
		{Name: "redfish", Mode: "fixture_only", CAFile: caFile, CredentialFile: credentialFile, ScopeProbe: &hardwareProbe, Binding: evidence.Binding{Tenant: tenant.String(), SourceID: rfID.String(), SourceType: "redfish", Revision: 1, BackendLogicalID: "fixture-redfish", Endpoint: rf.URL, ScopeMapping: datascope.Mapping{Scopes: map[string][]string{"cluster": {clusterUID}}}}},
		{Name: "deepflow", Mode: "fixture_only", ScopeProbe: &networkProbe, FrozenEndpoints: []deepflow.Endpoint{{CanonicalID: podID, Namespace: namespace, PodID: 1, ClusterID: 2, NamespaceID: 3}}, Binding: evidence.Binding{Tenant: tenant.String(), SourceID: dfID.String(), SourceType: "deepflow", Revision: 1, BackendLogicalID: "fixture-querier", Endpoint: df.URL, ScopeMapping: datascope.Mapping{Scopes: map[string][]string{"cluster": {clusterUID}, "namespace": {namespace}, "organization": {"2"}}}}},
	} {
		mapping, _ := json.Marshal(source.Binding.ScopeMapping)
		if _, err := db.ExecContext(ctx, `INSERT INTO platform.source_registrations(tenant_id,source_id,cluster_id,source_type,instance_key,auth_ref,backend_logical_id,data_scope_mapping) VALUES($1,$2,$3,$4,$5,'openbao://fixture/readonly',$6,$7)`, tenant, source.Binding.SourceID, cluster, source.Name, source.Binding.SourceID, source.Binding.BackendLogicalID, mapping); err != nil {
			t.Fatal(err)
		}
		config.Sources = append(config.Sources, source)
	}
	return func(call func(string, string, []byte, string) (int, []byte)) {
		for _, item := range []struct {
			id, kind, source string
			query            evidence.Query
		}{{hardwareID, "hardware", rfID.String(), hardwareProbe}, {podID, "network", dfID.String(), networkProbe}} {
			deadline := time.Now().Add(45 * time.Second)
			for {
				status, body := call("GET", "/api/v1/resources/by-canonical-id?canonicalId="+url.QueryEscape(item.id), nil, "")
				if status == 200 {
					break
				}
				if time.Now().After(deadline) {
					var count int
					_ = db.QueryRowContext(ctx, `SELECT count(*) FROM platform.resource_entities WHERE tenant_id=$1 AND kind='PhysicalServer'`, tenant).Scan(&count)
					t.Logf("published hardware identities=%d expected Canonical=%s initial Canonical=%s", count, hardwareID, initial.Entities[0].CanonicalID)
					t.Fatalf("fixture inventory not published status=%d %s", status, body)
				}
				time.Sleep(200 * time.Millisecond)
			}
			var body []byte
			for {
				query := item.query
				if item.kind == "hardware" {
					query.To = time.Now().UTC()
					query.From = query.To.Add(-time.Minute)
				}
				data, _ := json.Marshal(map[string]any{"resourceCanonicalId": item.id, "sourceRegistrationId": item.source, "type": item.kind, "namespace": query.Namespace, "queryTemplate": query.Template, "timeRange": map[string]any{"from": query.From, "to": query.To}, "budget": map[string]int{"timeoutMs": 3000, "maxBytes": 65536}, "limit": 20})
				status, raw := call("POST", "/api/v1/evidence:query", data, uuid.NewString())
				body = raw
				if status == 200 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("fixture proof/query not ready status=%d %s", status, body)
				}
				time.Sleep(200 * time.Millisecond)
			}
			var result struct{ Data evidence.Result }
			if json.Unmarshal(body, &result) != nil || len(result.Data.Evidence) != 1 || result.Data.Evidence[0].ReplayState != "archived_verified" || !strings.Contains(string(body), "fixture_only") {
				t.Fatalf("fixture provenance/archive invalid %s", body)
			}
			status, raw := call("GET", "/api/v1/evidence/"+result.Data.Evidence[0].EvidenceID, nil, "")
			if status != 200 {
				t.Fatalf("fixture archive unreadable %d %s", status, raw)
			}
			t.Logf("%s fixture_only: actual OIDC API -> mTLS Worker -> qualified Adapter proof -> PostgreSQL/Transit/TLS S3 -> verified archive/read passed", item.kind)
		}
		diagnostic, _ := json.Marshal(map[string]any{"entryCanonicalId": hardwareID, "recipe": "incident", "policy": map[string]int{"maxDepth": 2, "maxNodes": 200, "maxEdges": 200, "timeoutMs": 3000}})
		deadline := time.Now().Add(45 * time.Second)
		for {
			status, body := call("POST", "/api/v1/diagnostic-graphs:build", diagnostic, uuid.NewString())
			var result struct{ Data graph.Result }
			if status == 200 && json.Unmarshal(body, &result) == nil && len(result.Data.Nodes) == 7 && len(result.Data.Edges) == 6 && result.Data.Partial {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("fixture hardware component graph invalid %d %s", status, body)
			}
			time.Sleep(200 * time.Millisecond)
		}
		for _, template := range []string{"GetL7Context", "GetTraceContext"} {
			data, _ := json.Marshal(map[string]any{"resourceCanonicalId": podID, "sourceRegistrationId": dfID, "type": "network", "queryTemplate": template, "timeRange": map[string]any{"from": networkProbe.From, "to": networkProbe.To}, "budget": map[string]int{"timeoutMs": 3000, "maxBytes": 65536}, "limit": 20})
			status, raw := call("POST", "/api/v1/evidence:query", data, uuid.NewString())
			if status != 409 || !strings.Contains(string(raw), "CAPABILITY_DISABLED") {
				t.Fatalf("disabled capability escaped %d %s", status, raw)
			}
		}
	}
}
