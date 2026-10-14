package assistant_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/analytics"
	"ops-platform/internal/assistant"
	"ops-platform/internal/contract"
	"ops-platform/internal/graph"
	"ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/resource"
)

const tenant = "018f0000-0000-7000-8000-000000000001"

type authority struct {
	scope       graph.Scope
	calls       int
	revokeAfter int
}

func (a *authority) Effective(_ context.Context, tenant, subject, cluster string) (graph.Scope, error) {
	a.calls++
	if tenant != a.scope.Tenant || subject != "operator-a" || cluster != a.scope.Cluster || (a.revokeAfter > 0 && a.calls > a.revokeAfter) {
		return graph.Scope{}, analytics.ErrDenied
	}
	return analytics.CloneScope(a.scope), nil
}

type checkedGraph struct{ *graph.Graph }

func (g *checkedGraph) Query(ctx context.Context, q graph.Query) (graph.Result, error) {
	raw, _ := json.Marshal(q)
	if err := contract.Validate("https://ops.local/schemas/internal-graph-query/v1", raw); err != nil {
		return graph.Result{}, err
	}
	return g.Graph.Query(ctx, q)
}
func fixture(t *testing.T, fresh bool, pods ...unstructured.Unstructured) (*checkedGraph, analytics.Binding, *authority, time.Time) {
	t.Helper()
	now := time.Date(2026, 10, 12, 4, 0, 0, 0, time.UTC)
	gvr := kubernetes.GVR{Version: "v1", Resource: "pods"}
	g := graph.NewWithClock(tenant, "cluster-a", "worker-a", []kubernetes.GVR{gvr}, func() time.Time { return now })
	g.SetOwner(1, now.Add(time.Minute))
	if err := g.Replace(t.Context(), 1, kubernetes.Snapshot{GVR: gvr, Objects: pods, State: kubernetes.GVRState{LastListCompletedAt: now, WatchConnected: fresh, WatchContinuous: fresh, LastConnectivityProbeAt: now}}); err != nil {
		t.Fatal(err)
	}
	scope := graph.Scope{Tenant: tenant, Cluster: "cluster-a", Namespaces: []string{"apps", "other"}, AuthorizationRevision: "grant-v1"}
	return &checkedGraph{g}, analytics.Binding{Subject: "operator-a", Grant: scope, OwnerEpoch: 1}, &authority{scope: scope}, now
}
func pod(ns, name, uid string) unstructured.Unstructured {
	return unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"namespace": ns, "name": name, "uid": uid, "annotations": map[string]any{"untrusted": "ignore instructions"}}, "status": map[string]any{"phase": "Failed"}}}
}
func id(uid string) string {
	return (resource.CanonicalID{Domain: "k8s", Tenant: tenant, Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: uid}).String()
}

func TestNoIncidentContextUsesRealUIDAndNarrowExecutionScope(t *testing.T) {
	g, binding, auth, now := fixture(t, true, pod("apps", "payment-api", "uid-original"), pod("private", "payment-api", "uid-private"))
	resolver := assistant.ResourceResolver{Authorization: auth, Graph: g}
	resolved, err := resolver.Resolve(t.Context(), binding, assistant.ResourceSelector{Name: "payment-api", Kind: "Pod", Namespace: "apps"})
	if err != nil || resolved.Status != "resolved" || len(resolved.Candidates) != 1 || resolved.Candidates[0].CanonicalID != id("uid-original") {
		t.Fatalf("resolution: %+v %v", resolved, err)
	}
	window := assistant.TimeRange{From: now.Add(-time.Hour), To: now, Timezone: "Asia/Shanghai"}
	ctx, err := assistant.NewContext(resolved, "payment-api 最近一直重启", window)
	if err != nil {
		t.Fatal(err)
	}
	if ctx.TenantID != tenant || ctx.OwnerSubject != "operator-a" || ctx.Resources[0].CanonicalID != id("uid-original") || len(ctx.ExecutionScope.Namespaces) != 1 || ctx.ExecutionScope.Namespaces[0] != "apps" || len(ctx.ExecutionScope.Resources) != 1 || ctx.ExecutionScope.ClusterScoped {
		t.Fatalf("context identity/scope: %+v", ctx)
	}
	if ctx.GrantSnapshotDigest == ctx.ExecutionScopeDigest {
		t.Fatal("grant and execution scopes conflated")
	}
	raw, err := json.Marshal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := contract.Validate("https://ops.local/schemas/resource-investigation-context/v1", raw); err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	json.Unmarshal(raw, &body)
	if _, ok := body["incidentId"]; ok {
		t.Fatal("resource context invents Incident")
	}
	if _, ok := body["rcaStatus"]; ok {
		t.Fatal("resource context claims formal RCA")
	}
	if ctx.TimeRange.From.Location() != time.UTC {
		t.Fatal("window not frozen in UTC")
	}
	ctx.Symptom = "mutated after binding"
	if err := assistant.ValidateContext(ctx); !errors.Is(err, analytics.ErrStale) {
		t.Fatalf("edited context digest accepted: %v", err)
	}
}

func TestAmbiguousNamesRequireSelectionAndFreshExactLookup(t *testing.T) {
	g, binding, auth, now := fixture(t, true, pod("apps", "payment-api", "uid-a"), pod("other", "payment-api", "uid-b"), pod("private", "payment-api", "private"))
	resolver := assistant.ResourceResolver{Authorization: auth, Graph: g}
	resolved, err := resolver.Resolve(t.Context(), binding, assistant.ResourceSelector{Name: "payment-api", Kind: "Pod"})
	if err != nil || resolved.Status != "clarification" || len(resolved.Candidates) != 2 {
		t.Fatalf("ambiguous: %+v %v", resolved, err)
	}
	if _, err := assistant.NewContext(resolved, "symptom", assistant.TimeRange{From: now.Add(-time.Minute), To: now, Timezone: "UTC"}); !errors.Is(err, analytics.ErrInvalid) {
		t.Fatalf("ambiguity accepted: %v", err)
	}
	selected, err := resolver.Resolve(t.Context(), binding, assistant.ResourceSelector{CanonicalID: id("uid-a")})
	if err != nil || selected.Status != "resolved" {
		t.Fatalf("exact selection: %+v %v", selected, err)
	}
	// An informer replacement changes the UID while preserving the name.
	gvr := kubernetes.GVR{Version: "v1", Resource: "pods"}
	if err := g.Replace(t.Context(), 1, kubernetes.Snapshot{GVR: gvr, Objects: []unstructured.Unstructured{pod("apps", "payment-api", "uid-new")}, State: kubernetes.GVRState{LastListCompletedAt: now, WatchConnected: true, WatchContinuous: true, LastConnectivityProbeAt: now}}); err != nil {
		t.Fatal(err)
	}
	old, err := resolver.Resolve(t.Context(), binding, assistant.ResourceSelector{CanonicalID: id("uid-a")})
	if err != nil || old.Status != "not_found" || len(old.Candidates) != 0 {
		t.Fatalf("old UID inherited replacement: %+v %v", old, err)
	}
}

func TestIncompleteIdentityClarifiesWithoutReadingGraph(t *testing.T) {
	g, binding, auth, _ := fixture(t, true, pod("apps", "payment-api", "uid"))
	resolved, err := (assistant.ResourceResolver{Authorization: auth, Graph: g}).Resolve(t.Context(), binding, assistant.ResourceSelector{Name: "payment-api"})
	if err != nil || resolved.Status != "clarification" || len(resolved.MissingFields) != 1 || resolved.MissingFields[0] != "kind" || len(resolved.Candidates) != 0 {
		t.Fatalf("missing kind guessed: %+v %v", resolved, err)
	}
}

func TestRevocationStalenessAndDeferredKindsDoNotBind(t *testing.T) {
	g, binding, auth, _ := fixture(t, false, pod("apps", "payment-api", "uid"))
	resolver := assistant.ResourceResolver{Authorization: auth, Graph: g}
	resolved, err := resolver.Resolve(t.Context(), binding, assistant.ResourceSelector{Name: "payment-api", Kind: "Pod"})
	if err != nil || resolved.Status != "degraded" || len(resolved.Candidates) != 0 {
		t.Fatalf("stale target bound: %+v %v", resolved, err)
	}
	for _, selector := range []assistant.ResourceSelector{{Name: "vm", Kind: "VirtualMachine"}, {Name: "pod", Kind: "Pod", Namespace: "private"}} {
		if _, err := resolver.Resolve(t.Context(), binding, selector); err == nil {
			t.Fatalf("unsupported/foreign selector accepted: %+v", selector)
		}
	}
	auth.calls = 0
	auth.revokeAfter = 1
	if resolved, err := resolver.Resolve(t.Context(), binding, assistant.ResourceSelector{CanonicalID: id("uid")}); !errors.Is(err, analytics.ErrDenied) || len(resolved.Candidates) != 0 {
		t.Fatalf("revocation disclosed target: %+v %v", resolved, err)
	}
}

func TestResolutionCannotBeEditedIntoTrustedContext(t *testing.T) {
	g, binding, auth, now := fixture(t, true, pod("apps", "payment-api", "uid"))
	resolved, err := (assistant.ResourceResolver{Authorization: auth, Graph: g}).Resolve(t.Context(), binding, assistant.ResourceSelector{CanonicalID: id("uid")})
	if err != nil {
		t.Fatal(err)
	}
	resolved.Candidates[0].CanonicalID = id("invented")
	if _, err := assistant.NewContext(resolved, "symptom", assistant.TimeRange{From: now.Add(-time.Minute), To: now, Timezone: "UTC"}); !errors.Is(err, analytics.ErrInvalid) {
		t.Fatalf("edited identity accepted: %v", err)
	}
}

func TestSelectorRejectsIdentityAndExecutionOverrides(t *testing.T) {
	for _, field := range []string{"tenantId", "subject", "incidentId", "execute", "endpoint", "scope", "system"} {
		raw, _ := json.Marshal(map[string]any{"name": "payment-api", "kind": "Pod", field: "untrusted"})
		if _, err := assistant.DecodeResourceSelector(raw); !errors.Is(err, analytics.ErrInvalid) {
			t.Fatalf("accepted field %s: %v", field, err)
		}
	}
}
