package graph

import (
	"context"
	"errors"
	"fmt"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/resource"
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
