package integration

import (
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"io"
	"net/http"
	"ops-platform/internal/datascope"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/resource"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestSP04VictoriaLiveScopeProofAndRevocation(t *testing.T) {
	vm, vl := os.Getenv("SP04_TEST_VM_URL"), os.Getenv("SP04_TEST_VL_URL")
	if vm == "" || vl == "" {
		t.Skip("owned VictoriaMetrics/VictoriaLogs endpoints required")
	}
	ctx, db, dir, dsn := newMigrationDatabase(t)
	if err := goose.UpToContext(ctx, db, dir, 1); err != nil {
		t.Fatal(err)
	}
	if err := runRemainingMigrationsAsMigrationRole(t, ctx, db, dsn, dir); err != nil {
		t.Fatal(err)
	}
	tenant, other := uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.tenants(tenant_id,slug,display_name) VALUES($1,'sp04-victoria','Victoria'),($2,'sp04-foreign','Foreign')`, tenant, other); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, runtimePoolConfig(t, ctx, db, dsn, "worker_runtime_role"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	repo := evidence.Repository{Pool: pool}
	now := time.Now().UTC().Truncate(time.Second)
	id := resource.CanonicalID{Domain: "k8s", Tenant: tenant.String(), Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: "canary-a"}.String()
	scope := graph.Scope{Tenant: tenant.String(), Cluster: "cluster-a", Namespaces: []string{"apps"}, AuthorizationRevision: "scope-probe-a"}
	post := func(endpoint, body string) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode > 299 {
			raw, _ := io.ReadAll(res.Body)
			t.Fatalf("seed HTTP %d %s", res.StatusCode, raw)
		}
	}
	// Foreign-tenant and unlabeled facts share the backend and resource labels.
	metrics := ""
	logs := ""
	for _, label := range []string{tenant.String(), other.String(), ""} {
		tenantLabel := ""
		if label != "" {
			tenantLabel = fmt.Sprintf(`tenant=%q,`, label)
		}
		metrics += fmt.Sprintf("kube_pod_status_phase{%scluster=\"cluster-a\",namespace=\"apps\",uid=\"canary-a\",phase=\"Running\",api_key=\"synthetic-secret\"} 1 %d\n", tenantLabel, now.Add(-30*time.Second).UnixMilli())
		fact := map[string]any{"cluster": "cluster-a", "namespace": "apps", "uid": "canary-a", "_time": now.Format(time.RFC3339Nano), "_msg": `{"token":"synthetic-secret","message":"bounded canary"}`}
		if label != "" {
			fact["tenant"] = label
		}
		raw, _ := json.Marshal(fact)
		logs += string(raw) + "\n"
	}
	post(vm+"/api/v1/import/prometheus", metrics)
	post(vl+"/insert/jsonline?_time_field=_time&_msg_field=_msg&_stream_fields=tenant,cluster,namespace,uid", logs)
	for name, endpoint := range map[string]string{"victoriametrics": vm, "victorialogs": vl} {
		t.Run(name, func(t *testing.T) {
			source := uuid.New()
			mapping := datascope.Mapping{RequiredLabels: map[string]string{"tenant": tenant.String()}, Scopes: map[string][]string{"cluster": {"cluster-a"}, "namespace": {"apps"}}}
			rawMapping, _ := json.Marshal(mapping)
			if _, err := db.ExecContext(ctx, `INSERT INTO platform.source_registrations(tenant_id,source_id,source_type,instance_key,auth_ref,backend_logical_id,data_scope_mapping) VALUES($1,$2,$3,$3,'openbao://test/sp04','shared-victoria',$4)`, tenant, source, name, rawMapping); err != nil {
				t.Fatal(err)
			}
			binding := evidence.Binding{Tenant: tenant.String(), SourceID: source.String(), SourceType: name, Revision: 1, BackendLogicalID: "shared-victoria", Endpoint: endpoint, ScopeMapping: mapping}
			adapter, err := evidence.NewVictoria(name, binding, &http.Client{}, repo.Authorize)
			if err != nil {
				t.Fatal(err)
			}
			template := "pod-phase/v1"
			if name == "victorialogs" {
				template = "resource-logs/v1"
			}
			q := evidence.Query{ResourceCanonicalID: id, Namespace: "apps", Template: template, From: now.Add(-time.Minute), To: now.Add(time.Second), Limit: 20, Scope: scope}
			if _, err := adapter.Query(ctx, q); err != evidence.ErrScopeUnverified {
				t.Fatalf("declarative mapping granted query: %v", err)
			}
			// Both engines asynchronously ingest; this is bounded proof readiness,
			// never a fallback that enables an empty or failing query.
			deadline := time.Now().Add(15 * time.Second)
			var proofErr error
			for {
				proofErr = repo.VerifyVictoria(ctx, adapter, q)
				if proofErr == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("actual isolation proof: %v", proofErr)
				}
				select {
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(100 * time.Millisecond):
				}
			}
			durations := []time.Duration{}
			waived := ((os.Getenv("OPS_PERFORMANCE_EXEMPTION") == "pre-sp07-user-20261004" || os.Getenv("OPS_PERFORMANCE_EXEMPTION") == "sp05-user-20261002") || os.Getenv("OPS_PERFORMANCE_EXEMPTION") == "sp06-user-20261003")
			observations := 50
			if waived {
				observations = 1
			}
			for i := 0; i < observations; i++ {
				var start time.Time
				if !waived {
					start = time.Now()
				}
				result, err := adapter.Query(ctx, q)
				if !waived {
					durations = append(durations, time.Since(start))
				}
				if err != nil || result.Partial || len(result.Evidence) != 1 {
					t.Fatalf("query %+v %v", result, err)
				}
				data := string(result.Evidence[0].Data)
				if strings.Contains(data, "synthetic-secret") || strings.Contains(data, other.String()) {
					t.Fatal("secret or foreign tenant leaked")
				}
				var rows []any
				if json.Unmarshal(result.Evidence[0].Data, &rows) != nil || len(rows) != 1 {
					t.Fatalf("missing-tenant/foreign rows survived: %s", data)
				}
			}
			if waived {
				t.Log("SP05/SP06/pre-SP07 explicit user waiver: Victoria repeated latency sampling/P95 not executed; actual isolation, redaction and revoke checks retained")
			} else {
				sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
				p95 := durations[47]
				if p95 > 5*time.Second {
					t.Fatalf("P95 %v", p95)
				}
				t.Logf("live %s: shared backend with own/foreign/unlabeled canaries; templates fixed; 50 sequential requests P95=%v, one returned row, small-scale contract qualification", name, p95)
			}
			if _, err := db.ExecContext(ctx, `UPDATE platform.source_registrations SET revision=revision+1 WHERE tenant_id=$1 AND source_id=$2`, tenant, source); err != nil {
				t.Fatal(err)
			}
			if _, err := adapter.Query(ctx, q); err != evidence.ErrScopeUnverified {
				t.Fatalf("revoked revision queried: %v", err)
			}
		})
	}
}
