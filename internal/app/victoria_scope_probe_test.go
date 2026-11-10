package app

import (
	"context"
	"errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/datascope"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/resource"
	"testing"
	"time"
)

func TestVictoriaCanaryFollowsPodReplacementWithinOriginalNamespace(t *testing.T) {
	gvr := kubernetes.GVR{Version: "v1", Resource: "pods"}
	owner := graph.New("tenant-a", "cluster-a", "instance", []kubernetes.GVR{gvr})
	owner.SetOwner(1, time.Now().Add(time.Minute))
	pod := unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": "replacement", "namespace": "apps", "uid": "replacement-uid", "resourceVersion": "2"}}}
	if err := owner.Replace(t.Context(), 1, kubernetes.Snapshot{GVR: gvr, Objects: []unstructured.Unstructured{pod}, State: kubernetes.GVRState{LastListCompletedAt: time.Now(), LastConnectivityProbeAt: time.Now(), WatchConnected: true, WatchContinuous: true}}); err != nil {
		t.Fatal(err)
	}
	b := evidence.Binding{Tenant: "tenant-a", ScopeMapping: datascope.Mapping{Scopes: map[string][]string{"cluster": {"cluster-a"}, "namespace": {"apps", "other"}}}}
	probe := evidence.Query{ResourceCanonicalID: "retired-uid", Namespace: "apps", Template: "pod-restarts/v1", Scope: graph.Scope{Tenant: "tenant-a", Cluster: "cluster-a", Namespaces: []string{"apps", "other"}}}
	calls := 0
	err := verifyCurrentVictoriaProbe(t.Context(), b, probe, owner, func(_ context.Context, q evidence.Query) error {
		calls++
		id, e := resource.ParseCanonicalID(q.ResourceCanonicalID)
		if e != nil || id.StableID != "replacement-uid" || q.Namespace != "apps" || q.Template != probe.Template {
			t.Fatalf("changed scope %+v", q)
		}
		return nil
	})
	if err != nil || calls != 1 || probe.ResourceCanonicalID != "retired-uid" {
		t.Fatalf("renew %d %v", calls, err)
	}
	if err = verifyCurrentVictoriaProbe(t.Context(), b, probe, owner, func(context.Context, evidence.Query) error { return evidence.ErrScopeUnverified }); !errors.Is(err, evidence.ErrScopeUnverified) {
		t.Fatalf("failed actual source read granted proof: %v", err)
	}
}
func TestVictoriaCanaryNeverExpandsScopeFromGraphCandidates(t *testing.T) {
	b := evidence.Binding{Tenant: "tenant-a", ScopeMapping: datascope.Mapping{Scopes: map[string][]string{"cluster": {"cluster-a"}, "namespace": {"apps"}}}}
	probe := evidence.Query{Namespace: "apps"}
	node := func(tenant, cluster, ns, kind string) resource.Entity {
		return resource.Entity{CanonicalID: resource.CanonicalID{Domain: "k8s", Tenant: tenant, Scope: cluster, APIGroup: "core", Kind: kind, StableID: "uid"}.String(), Kind: kind, Namespace: ns}
	}
	if got := victoriaProbeCandidates(probe, b, []resource.Entity{node("tenant-b", "cluster-a", "apps", "Pod"), node("tenant-a", "cluster-b", "apps", "Pod"), node("tenant-a", "cluster-a", "other", "Pod"), node("tenant-a", "cluster-a", "apps", "Node")}); len(got) != 0 {
		t.Fatalf("cross scope candidates: %v", got)
	}
	probe.Namespace = "other"
	if len(victoriaProbeCandidates(probe, b, []resource.Entity{node("tenant-a", "cluster-a", "other", "Pod")})) != 0 {
		t.Fatal("ungranted namespace selected")
	}
}
