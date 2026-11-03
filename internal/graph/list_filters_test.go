package graph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/contract"
	"ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/resource"
	"strings"
	"testing"
	"time"
)

func TestFilteredSortedScopedPages(t *testing.T) {
	gvr := kubernetes.GVR{Version: "v1", Resource: "pods"}
	g := New("tenant-a", "cluster-a", "instance", []kubernetes.GVR{gvr})
	g.SetOwner(1, time.Now().Add(time.Minute))
	objects := []unstructured.Unstructured{}
	for _, spec := range []struct{ name, uid, ns, phase string }{{"a", "03", "apps", "Running"}, {"b", "02", "apps", "Running"}, {"c", "01", "apps", "Failed"}, {"foreign", "00", "foreign", "Running"}} {
		o := object("Pod", spec.ns, spec.name, spec.uid, nil)
		o.SetLabels(map[string]string{"app": "web"})
		o.Object["status"] = map[string]any{"phase": spec.phase}
		objects = append(objects, o)
	}
	if err := g.Replace(context.Background(), 1, kubernetes.Snapshot{GVR: gvr, Objects: objects, State: kubernetes.GVRState{LastListCompletedAt: time.Now(), WatchConnected: true, WatchContinuous: true, LastConnectivityProbeAt: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	q := Query{QueryKind: "list", ExpectedOwnerEpoch: 1, MaxDepth: 1, MaxNodes: 1, MaxEdges: 2, Scope: Scope{Tenant: "tenant-a", Cluster: "cluster-a", Namespaces: []string{"apps"}}, Health: "normal", Label: "app=web", Sort: "name", Order: "desc"}
	first, err := g.Query(context.Background(), q)
	if err != nil || len(first.Nodes) != 1 || first.Nodes[0].Name != "b" || first.NextCanonicalID == "" {
		t.Fatalf("first page %+v %v", first, err)
	}
	if first.Summary == nil || first.Summary.Total != 2 || first.Summary.ByKind["Pod"] != 2 {
		t.Fatalf("summary must count matching authorized nodes across pages, not foreign nodes: %+v", first.Summary)
	}
	q.AfterCanonicalID = first.NextCanonicalID
	q.CursorRevision = &first.GraphRevision
	second, err := g.Query(context.Background(), q)
	if err != nil || len(second.Nodes) != 1 || second.Nodes[0].Name != "a" || second.NextCanonicalID != "" {
		t.Fatalf("second page %+v %v", second, err)
	}
	changed := q
	changed.Label = "app=other"
	if QueryDigest(q) == QueryDigest(changed) {
		t.Fatal("filters not bound into cursor")
	}
	q.Sort = "arbitrary SQL"
	if _, err := g.Query(context.Background(), q); err == nil {
		t.Fatal("unknown sort accepted")
	}
}

func TestExactResourceResolutionBeyondFirstPage(t *testing.T) {
	gvr := kubernetes.GVR{Version: "v1", Resource: "pods"}
	g := New("tenant-a", "cluster-a", "instance", []kubernetes.GVR{gvr})
	g.SetOwner(1, time.Now().Add(time.Minute))
	objects := []unstructured.Unstructured{}
	for i := 0; i < 80; i++ {
		objects = append(objects, object("Pod", "apps", fmt.Sprintf("service-%02d", i), fmt.Sprintf("uid-%02d", i), nil))
	}
	objects = append(objects, object("Pod", "foreign", "private", "private", nil))
	if err := g.Replace(context.Background(), 1, kubernetes.Snapshot{GVR: gvr, Objects: objects, State: kubernetes.GVRState{LastListCompletedAt: time.Now(), LastConnectivityProbeAt: time.Now(), WatchConnected: true, WatchContinuous: true}}); err != nil {
		t.Fatal(err)
	}
	id := func(uid string) string {
		return (resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: uid}).String()
	}
	q := Query{QueryKind: "list", ExpectedOwnerEpoch: 1, MaxDepth: 1, MaxNodes: 50, MaxEdges: 2, Scope: Scope{Tenant: "tenant-a", Cluster: "cluster-a", Namespaces: []string{"apps"}}, CanonicalIDs: []string{id("uid-75"), id("uid-79"), id("private")}}
	out, err := g.Query(context.Background(), q)
	if err != nil || len(out.Nodes) != 2 || out.Summary.Total != 2 || out.NextCanonicalID != "" {
		t.Fatalf("selected authorized names beyond first 50: %+v %v", out, err)
	}
	names := map[string]bool{}
	for _, node := range out.Nodes {
		names[node.Name] = true
	}
	if !names["service-75"] || !names["service-79"] {
		t.Fatal(names)
	}
	oldDigest := QueryDigest(q)
	q.CanonicalIDs = []string{id("uid-74")}
	if QueryDigest(q) == oldDigest {
		t.Fatal("resource selection missing from cursor binding")
	}
	q.CanonicalIDs = []string{(resource.CanonicalID{Domain: "k8s", Tenant: "other", Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: "uid-75"}).String()}
	if _, err = g.Query(context.Background(), q); !errors.Is(err, ErrScope) {
		t.Fatalf("foreign selection must fail closed: %v", err)
	}
	q.CanonicalIDs = []string{id("uid-75"), id("uid-75")}
	if _, err = g.Query(context.Background(), q); err == nil {
		t.Fatal("duplicate selection accepted")
	}
	q.CanonicalIDs = make([]string, 51)
	if _, err = g.Query(context.Background(), q); err == nil {
		t.Fatal("selection budget exceeded")
	}
}

func TestEmptyLabelIsDistinctFromMissingLabel(t *testing.T) {
	q := Query{QueryKind: "list", Label: "ops.example/team="}
	if err := validateList(q); err != nil {
		t.Fatalf("legal empty value rejected: %v", err)
	}
	for _, tc := range []struct {
		name   string
		labels map[string]string
		want   bool
	}{
		{"present-empty", map[string]string{"ops.example/team": ""}, true},
		{"missing", map[string]string{}, false},
		{"present-nonempty", map[string]string{"ops.example/team": "ops"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := listMatches(q, resource.Entity{Labels: tc.labels}); got != tc.want {
				t.Fatalf("matched=%v want=%v", got, tc.want)
			}
		})
	}
	for _, label := range []string{"=value", "no-separator"} {
		q.Label = label
		if validateList(q) == nil {
			t.Fatalf("invalid label %q accepted", label)
		}
	}
}

func TestDomainFilterAndSummaryShareScopedPopulation(t *testing.T) {
	gvr := kubernetes.GVR{Version: "v1", Resource: "pods"}
	g := New("tenant-a", "cluster-a", "instance", []kubernetes.GVR{gvr})
	g.SetOwner(1, time.Now().Add(time.Minute))
	objects := []unstructured.Unstructured{object("Pod", "apps", "visible", "01", nil), object("Pod", "foreign", "hidden", "02", nil)}
	objects[0].Object["status"] = map[string]any{"phase": "Running"}
	if err := g.Replace(t.Context(), 1, kubernetes.Snapshot{GVR: gvr, Objects: objects, State: kubernetes.GVRState{LastListCompletedAt: time.Now(), WatchConnected: true, WatchContinuous: true, LastConnectivityProbeAt: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	q := Query{QueryKind: "list", IncludeDomains: true, Domain: "kubernetes", ExpectedOwnerEpoch: 1, MaxDepth: 1, MaxNodes: 1, MaxEdges: 2, Scope: Scope{Tenant: "tenant-a", Cluster: "cluster-a", Namespaces: []string{"apps"}}}
	got, err := g.Query(t.Context(), q)
	if err != nil || len(got.Nodes) != 1 || got.Summary.ByDomain["kubernetes"].Total != 1 || got.Summary.ByDomain["kubernetes"].ByHealth["normal"] != 1 {
		t.Fatalf("scope/domain summary %+v %v", got, err)
	}
	wire, marshalErr := json.Marshal(got)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if err := contract.Validate("https://ops.local/schemas/resource-graph/v2", wire); err != nil {
		t.Fatal("domain summary wire contract", err)
	}
	legacy := q
	legacy.IncludeDomains = false
	if QueryDigest(legacy) == QueryDigest(q) {
		t.Fatal("domain summary flag not bound to cursor")
	}
	old, legacyErr := g.Query(t.Context(), legacy)
	if legacyErr != nil || old.Summary.ByDomain != nil {
		t.Fatal("legacy response acquired a new closed-schema field", legacyErr)
	}
	before := QueryDigest(q)
	q.Domain = "storage"
	if before == QueryDigest(q) {
		t.Fatal("domain not bound to cursor")
	}
	got, err = g.Query(t.Context(), q)
	if err != nil || len(got.Nodes) != 0 || got.Summary.Total != 0 {
		t.Fatalf("nonmatching domain %+v %v", got, err)
	}
	q.Domain = "virtualization"
	if _, err = g.Query(t.Context(), q); err == nil {
		t.Fatal("deferred domain admitted")
	}
}

func TestResourceDomainDoesNotBorrowPodOrVirtualizationSemantics(t *testing.T) {
	for _, item := range []struct{ domain, kind, want string }{{"hardware", "Disk", "hardware"}, {"k8s", "PersistentVolumeClaim", "storage"}, {"k8s", "CSIDriver", "storage"}, {"k8s", "Service", "network"}, {"k8s", "Pod", "kubernetes"}, {"k8s", "VirtualMachine", "virtualization"}} {
		e := resource.Entity{Kind: item.kind, CanonicalID: resource.CanonicalID{Domain: item.domain, Tenant: "tenant", Scope: "cluster", APIGroup: "core", Kind: item.kind, StableID: "uid"}.String()}
		if actual := entityDomain(e); actual != item.want {
			t.Fatal(item, actual)
		}
	}
}

func TestResourceSearchSharesAuthorizationSummaryAndCursor(t *testing.T) {
	gvr := kubernetes.GVR{Version: "v1", Resource: "pods"}
	g := New("tenant-a", "cluster-a", "instance", []kubernetes.GVR{gvr})
	g.SetOwner(1, time.Now().Add(time.Minute))
	objects := []unstructured.Unstructured{object("Pod", "apps", "支付-Gateway", "uid-visible", nil), object("Pod", "foreign", "支付-Gateway", "uid-foreign", nil), object("Pod", "apps", "database", "uid-other", nil)}
	objects[0].Object["status"] = map[string]any{"phase": "Running"}
	if err := g.Replace(context.Background(), 1, kubernetes.Snapshot{GVR: gvr, Objects: objects, State: kubernetes.GVRState{LastListCompletedAt: time.Now(), LastConnectivityProbeAt: time.Now(), WatchConnected: true, WatchContinuous: true}}); err != nil {
		t.Fatal(err)
	}
	q := Query{QueryKind: "list", ExpectedOwnerEpoch: 1, MaxDepth: 1, MaxNodes: 1, MaxEdges: 2, Scope: Scope{Tenant: "tenant-a", Cluster: "cluster-a", Namespaces: []string{"apps"}}, Search: "支付-gAtE", Health: "normal"}
	for _, search := range []string{"支付-gAtE", "VISIBLE", "pod"} {
		q.Search = search
		out, err := g.Query(context.Background(), q)
		if err != nil || len(out.Nodes) != 1 || out.Nodes[0].Name != "支付-Gateway" || out.Summary.Total != 1 || out.Nodes[0].Attributes["health"] != "normal" || out.Nodes[0].Attributes["resourceDomain"] != "kubernetes" {
			t.Fatalf("search %q: %+v %v", search, out, err)
		}
	}
	digest := QueryDigest(q)
	q.Search = "foreign"
	if digest == QueryDigest(q) {
		t.Fatal("search not bound to cursor")
	}
	out, err := g.Query(context.Background(), q)
	if err != nil || len(out.Nodes) != 0 || out.Summary.Total != 0 {
		t.Fatalf("foreign search leaked result/summary: %+v %v", out, err)
	}
	q.Search = strings.Repeat("字", 257)
	if _, err = g.Query(context.Background(), q); err == nil {
		t.Fatal("oversized search accepted")
	}
	q.Search = strings.Repeat("字", 256)
	if _, err = g.Query(context.Background(), q); err != nil {
		t.Fatal("256 Unicode codepoints rejected", err)
	}
	entityQ := q
	entityQ.Search = ""
	entityQ.QueryKind = "entity"
	entityQ.CanonicalID = resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: "uid-visible"}.String()
	entity, err := g.Query(context.Background(), entityQ)
	if err != nil || len(entity.Nodes) != 1 || entity.Nodes[0].Attributes["health"] != "normal" {
		t.Fatalf("detail health differs from list/filter: %+v %v", entity, err)
	}
}

func TestResourceFilterOptionsUseWholeAuthorizedSnapshot(t *testing.T) {
	gvr := kubernetes.GVR{Version: "v1", Resource: "pods"}
	g := New("tenant-a", "cluster-a", "instance", []kubernetes.GVR{gvr})
	g.SetOwner(1, time.Now().Add(time.Minute))
	objects := []unstructured.Unstructured{object("Pod", "apps", "a", "01", nil), object("Service", "second", "b", "02", nil), object("Secret", "foreign", "hidden", "03", nil), object("Node", "", "node", "04", nil)}
	if err := g.Replace(t.Context(), 1, kubernetes.Snapshot{GVR: gvr, Objects: objects, State: kubernetes.GVRState{LastListCompletedAt: time.Now(), WatchConnected: true, WatchContinuous: true, LastConnectivityProbeAt: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	q := Query{QueryKind: "list", IncludeFilterOptions: true, Search: "nonexistent", Kind: "Pod", Namespace: "apps", ExpectedOwnerEpoch: 1, MaxDepth: 1, MaxNodes: 1, MaxEdges: 2, Scope: Scope{Tenant: "tenant-a", Cluster: "cluster-a", Namespaces: []string{"apps", "second"}}}
	got, err := g.Query(t.Context(), q)
	if err != nil || got.Summary.Total != 0 {
		t.Fatalf("filtered results: %+v %v", got, err)
	}
	options := got.Summary.FilterOptions
	if options == nil || strings.Join(options.Kinds, ",") != "Pod,Service" || strings.Join(options.Namespaces, ",") != "apps,second" || options.ClusterScoped {
		t.Fatalf("scope leaked or candidates paged/filtered: %+v", options)
	}
	wire, _ := json.Marshal(got)
	if err := contract.Validate("https://ops.local/schemas/resource-graph/v2", wire); err != nil {
		t.Fatal(err)
	}
	legacy := q
	legacy.IncludeFilterOptions = false
	if QueryDigest(legacy) == QueryDigest(q) {
		t.Fatal("flag not cursor bound")
	}
	old, err := g.Query(t.Context(), legacy)
	if err != nil || old.Summary.FilterOptions != nil {
		t.Fatal("legacy acquired candidates", err)
	}
	q.Search = ""
	q.Kind = ""
	q.Namespace = "__cluster__"
	q.Scope.ClusterScoped = true
	got, err = g.Query(t.Context(), q)
	if err != nil || len(got.Nodes) != 1 || got.Nodes[0].Kind != "Node" || !got.Summary.FilterOptions.ClusterScoped {
		t.Fatalf("cluster scope selector: %+v %v", got, err)
	}
	q.Scope.ClusterScoped = false
	got, err = g.Query(t.Context(), q)
	if err != nil || len(got.Nodes) != 0 || got.Summary.FilterOptions.ClusterScoped {
		t.Fatalf("cluster scope authorization: %+v %v", got, err)
	}
	q.Namespace = ""
	q.Scope.Resources = []string{resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: "Service", StableID: "02"}.String()}
	got, err = g.Query(t.Context(), q)
	if err != nil || len(got.Nodes) != 1 || strings.Join(got.Summary.FilterOptions.Kinds, ",") != "Service" || strings.Join(got.Summary.FilterOptions.Namespaces, ",") != "second" {
		t.Fatalf("exact resource scope leaked candidates: %+v %v", got, err)
	}
}
