// Review probes record current defects. Passing means the described behavior
// was reproduced; these are not acceptance tests for a corrected product.
package probes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"ops-platform/internal/analytics"
	"ops-platform/internal/datascope"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/investigation"
	"ops-platform/internal/modelprofile"
	"ops-platform/internal/resourcestore"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestReviewAccepted32KProfileRejectedBeforeModelAdmission(t *testing.T) {
	b, err := os.ReadFile("../../../../test/fixtures/assistant/model-profile.fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	var profile modelprofile.Profile
	if err = json.Unmarshal(b, &profile); err != nil {
		t.Fatal(err)
	}
	profile.ContextWindow = 32768
	if !profile.Valid("127.0.0.1:8083") {
		t.Fatal("profile itself must be valid")
	}
	reserve := investigation.Usage{ModelRequests: 1, InputTokens: int64(profile.ContextWindow), OutputTokens: int64(profile.OutputTokenLimit), ResultBytes: 65536}
	args, _ := json.Marshal(map[string]string{"requestDigest": graph.ScopeDigest("review-request")})
	// No pool is configured: ErrBudget is returned before touching persistence.
	_, err = (investigation.Repository{}).AllocateModel(context.Background(), investigation.Lease{}, reserve, args)
	if !errors.Is(err, investigation.ErrBudget) {
		t.Fatalf("got %v", err)
	}
	t.Log("valid contextWindow=32768; AllocateModel rejects its inputTokens=32768 as BUDGET_EXHAUSTED before database access")
}

func TestReviewVictoriaExpandedRawQueries(t *testing.T) {
	for _, entry := range []struct {
		dataset, metric, operation, source string
		calls                              int
	}{
		{"logs.records", "log_count", "trend", "victorialogs", 50},
		{"metrics.pod-memory", "memory_bytes", "compare", "victoriametrics", 2},
	} {
		t.Run(entry.dataset+"/"+entry.operation, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Second)
			calls := 0
			client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				if err := r.ParseForm(); err != nil {
					return nil, err
				}
				at, err := time.Parse(time.RFC3339Nano, r.Form.Get("time"))
				if err != nil {
					return nil, err
				}
				calls++
				body := fmt.Sprintf(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[%d,"2"]}]}}`, at.Unix())
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}
			tenant := uuid.NewString()
			source := evidence.Binding{SourceType: entry.source, Tenant: tenant, SourceID: uuid.NewString(), Revision: 1, Endpoint: "http://review.invalid", ScopeMapping: datascope.Mapping{RequiredLabels: map[string]string{"tenant": tenant}, Scopes: map[string][]string{"cluster": {"cluster"}, "namespace": {"apps"}}}}
			proof := analytics.Qualification{DatasetID: entry.dataset, Operation: entry.operation, SourceRevision: 1, BindingDigest: evidence.BindingScopeDigest(source), EvidenceDigest: graph.ScopeDigest("review-fixture"), CollectorVersion: "review/v1", VerifiedUntil: now.Add(time.Hour), CoverageFrom: now.Add(-2 * time.Hour), CoverageTo: now, MaxWindowSeconds: 3600}
			adapter, err := analytics.NewVictoria(source, client, func(context.Context, evidence.Binding) error { return nil }, []analytics.Qualification{proof})
			if err != nil {
				t.Fatal(err)
			}
			q := analytics.QueryIntent{SchemaVersion: "query-intent/v1", DatasetID: entry.dataset, Operation: entry.operation, Metrics: []string{entry.metric}, Dimensions: []string{"time"}, Filters: []analytics.Filter{}, Sort: []analytics.Sort{}, Limit: 50, StepSeconds: 60, TimeRange: &analytics.QueryTimeRange{From: now.Add(-49 * time.Minute), To: now, Timezone: "UTC"}}
			if entry.operation == "compare" {
				q.Dimensions, q.StepSeconds = []string{"comparison"}, 0
				q.TimeRange.From = now.Add(-time.Minute)
				q.ComparisonTimeRange = &analytics.QueryTimeRange{From: now.Add(-10 * time.Minute), To: now.Add(-9 * time.Minute), Timezone: "UTC"}
			}
			plan, err := adapter.Compile(q, analytics.Binding{Subject: "review-operator", Grant: graph.Scope{Tenant: tenant, Cluster: "cluster", Namespaces: []string{"apps"}, AuthorizationRevision: "review-grant"}})
			if err != nil {
				t.Fatal(err)
			}
			result, err := adapter.Execute(context.Background(), plan)
			if err != nil {
				t.Fatal(err)
			}
			if calls != entry.calls {
				t.Fatalf("calls=%d, want=%d", calls, entry.calls)
			}
			t.Logf("one execute_analysis_query expands to %d source HTTP requests; result rows=%d", calls, len(result.Rows))
		})
	}
}

type reviewPool struct{ begins, retirements int }
type reviewTx struct {
	pgx.Tx
	pool *reviewPool
}
type tenantRow struct{ tenant string }

func (r tenantRow) Scan(dest ...any) error { *dest[0].(*string) = r.tenant; return nil }
func (p *reviewPool) Begin(context.Context) (pgx.Tx, error) {
	p.begins++
	return &reviewTx{pool: p}, nil
}
func (tx *reviewTx) Commit(context.Context) error   { return nil }
func (tx *reviewTx) Rollback(context.Context) error { return nil }
func (tx *reviewTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	if !strings.Contains(sql, "set_config") {
		panic("unexpected query")
	}
	return tenantRow{args[0].(string)}
}
func (tx *reviewTx) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	if strings.Contains(sql, "pg_advisory_xact_lock") {
		return pgconn.NewCommandTag("SELECT 1"), nil
	}
	if !strings.Contains(sql, "UPDATE platform.resource_entities SET deleted_at") {
		return pgconn.CommandTag{}, errors.New("unexpected statement")
	}
	tx.pool.retirements++
	return pgconn.NewCommandTag("UPDATE 1"), nil
}

func TestReviewFilteredAbsenceBecomesTerminalRetirement(t *testing.T) {
	p := &reviewPool{}
	s := kubernetes.Snapshot{State: kubernetes.GVRState{LastListCompletedAt: time.Now()}}
	// FilterSnapshot produces this empty projection after a required label stops
	// matching. The native resource can still exist with exactly the same UID.
	_, err := (resourcestore.Repository{Pool: p}).SyncSnapshot(context.Background(), uuid.NewString(), uuid.NewString(), "cluster", "Pod", "core", s, map[string]string{"still-existing-pod": "rv1"})
	if err != nil || p.retirements != 1 {
		t.Fatalf("retirements=%d err=%v", p.retirements, err)
	}
	t.Log("absence from filtered Objects causes UPDATE deleted_at even without a native deletion")
}

func TestReviewFirstEmptySnapshotDoesNotReconcilePersistentInventory(t *testing.T) {
	p := &reviewPool{}
	s := kubernetes.Snapshot{State: kubernetes.GVRState{LastListCompletedAt: time.Now()}}
	_, err := (resourcestore.Repository{Pool: p}).SyncSnapshot(context.Background(), uuid.NewString(), uuid.NewString(), "cluster", "Pod", "core", s, map[string]string{})
	if err != nil || p.begins != 0 {
		t.Fatalf("begins=%d err=%v", p.begins, err)
	}
	t.Log("first empty snapshot with fresh process-local previous map performs zero persistence reads/writes")
}
