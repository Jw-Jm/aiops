package observationquery

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"ops-platform/internal/analytics"
	"ops-platform/internal/contract"
	"ops-platform/internal/graph"
	"ops-platform/internal/resource"
	"testing"
	"time"
)

func validRequest() Request {
	ns, uid := "apps", "ns-first"
	tenant := "11111111-1111-4111-8111-111111111111"
	now := time.Now().UTC().Truncate(time.Second)
	return Request{SchemaVersion: Schema, QueryKind: "logs", TemplateID: "logs.records", TemplateVersion: "logs.records/v1", Resource: Resource{CanonicalID: resource.CanonicalID{Domain: "k8s", Tenant: tenant, Scope: "cluster-first", APIGroup: "core", Kind: "Pod", StableID: "pod-first"}.String(), UID: "pod-first", ClusterUID: "cluster-first", NamespaceScope: NamespaceScope{Kind: "namespaced", Namespace: &ns, NamespaceUID: &uid}}, Source: Source{RegistrationID: uuid.New(), RegistrationRevision: 1, CredentialRevision: 1, QualificationDigest: graph.ScopeDigest("qualified"), CollectorVersion: "collector/v1"}, Window: Window{From: now.Add(-time.Minute), To: now, Timezone: "Asia/Shanghai", Bounds: "[from,to)"}, Budget: Budget{MaxRows: 20, MaxBytes: 131072, MaxDurationSeconds: 8, MaxSourceCalls: 2}}
}
func TestDecodeManualQueryBoundary(t *testing.T) {
	tenant := "11111111-1111-4111-8111-111111111111"
	raw, _ := json.Marshal(validRequest())
	if _, err := Decode(raw, tenant); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Request){"cross-cluster": func(r *Request) { r.Resource.ClusterUID = "other" }, "same-name-new-uid": func(r *Request) { r.Resource.UID = "pod-recreated" }, "missing-namespace-uid": func(r *Request) { r.Resource.NamespaceScope.NamespaceUID = nil }, "window-reversed": func(r *Request) { r.Window.To = r.Window.From }, "unknown-template": func(r *Request) { r.TemplateVersion = "arbitrary/v1" }, "over-budget": func(r *Request) { r.Budget.MaxRows = 21 }, "raw-expression": func(r *Request) { r.Filters = []analytics.Filter{{Field: "expression", Operator: "eq", Value: "*"}} }} {
		t.Run(name, func(t *testing.T) {
			r := validRequest()
			mutate(&r)
			raw, _ := json.Marshal(r)
			if _, err := Decode(raw, tenant); err == nil {
				t.Fatal("unsafe manual input accepted")
			}
		})
	}
	var obj map[string]any
	_ = json.Unmarshal(raw, &obj)
	obj["conversationId"] = uuid.NewString()
	raw, _ = json.Marshal(obj)
	if _, err := Decode(raw, tenant); err == nil {
		t.Fatal("manual query accepted an AI conversation")
	}
}
func TestResultPopulationNeverComesFromSamples(t *testing.T) {
	request := validRequest()
	now := time.Now().UTC()
	rec := Record{View: View{QueryID: uuid.New(), Resource: request.Resource, Source: request.Source, Window: request.Window, Coverage: request.Coverage(), ExpiresAt: now.Add(time.Hour), Reauthorization: Reauthorization{CheckedAt: now, AuthorizationVersion: 1, SourceRegistrationRevision: 1, Result: "allowed"}}, Request: request}
	aggregate := analytics.QueryResult{Exact: true, Status: "complete", Rows: []analytics.Row{{Value: 101}}}
	samples := analytics.QueryResult{Samples: []analytics.LogSample{{Timestamp: now, Level: "info", Message: "bounded observation"}}, Truncated: true}
	raw, err := BuildResult(rec, aggregate, samples)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Status     string
		Population struct{ Count int }
		Samples    struct{ Returned int }
		Exact      bool
	}
	_ = json.Unmarshal(raw, &out)
	if out.Population.Count != 101 || out.Samples.Returned != 1 || out.Status != "partial" || out.Exact {
		t.Fatalf("sample count promoted to total: %s", raw)
	}
	samples.Samples = nil
	samples.Truncated = false
	if _, err = BuildResult(rec, aggregate, samples); err != nil {
		t.Fatal("empty sample page must not imply no_data", err)
	}
	aggregate = analytics.QueryResult{Status: "no_data", Rows: []analytics.Row{}, DegradedSources: []string{}}
	raw, err = BuildResult(rec, aggregate, samples)
	if err != nil {
		t.Fatal(err)
	}
	if err = contract.Validate("https://ops.local/schemas/ui-v12-observability/v1#/$defs/resultView", raw); err != nil {
		t.Fatal(err)
	}
	aggregate = analytics.QueryResult{Status: "partial", Rows: []analytics.Row{{Value: 0}}}
	if _, err = BuildResult(rec, aggregate, samples); err == nil {
		t.Fatal("partial upstream count became measured zero")
	}
}

func TestManualIntentKeepsEmptyFiltersAsArray(t *testing.T) {
	request := validRequest()
	for _, operation := range []string{"aggregate", "samples"} {
		raw, _ := json.Marshal(request.Intent(operation))
		if _, err := analytics.DecodeIntent(raw); err != nil {
			t.Fatalf("persisted absent filters broke %s: %v", operation, err)
		}
	}
}

func TestHistoryPageLimitRejectsBeforeMetadataIO(t *testing.T) {
	for _, limit := range []int{0, -1, 51} {
		if _, err := (Service{}).ListPage(context.Background(), graph.Claims{}, uuid.Nil, limit); !errors.Is(err, ErrInvalid) {
			t.Fatalf("limit %d: %v", limit, err)
		}
	}
}
