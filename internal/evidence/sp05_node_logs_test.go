package evidence

import (
	"context"
	"net/http"
	"net/http/httptest"
	"ops-platform/internal/datascope"
	"ops-platform/internal/graph"
	"ops-platform/internal/resource"
	"strings"
	"testing"
	"time"
)

func TestNodeKernelTemplateBindsClusterUIDAndCannotGrantPodScope(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	reads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads++
		query := r.URL.Query().Get("query")
		for _, term := range []string{`_TRANSPORT="kernel"`, `tenant="tenant-a"`, `cluster="cluster-a"`, `uid="node-a"`, `namespace=""`} {
			if !strings.Contains(query, term) {
				t.Errorf("missing exact isolation term %s", term)
			}
		}
		w.Write([]byte(`{"_TRANSPORT":"kernel","tenant":"tenant-a","cluster":"cluster-a","uid":"node-a","namespace":"","_time":"` + now.Format(time.RFC3339Nano) + `","_msg":"BUG: unable to handle kernel NULL pointer dereference at 0000"}`))
	}))
	defer server.Close()
	b := Binding{Tenant: "tenant-a", SourceID: "logs", Revision: 1, BackendLogicalID: "logs-a", Endpoint: server.URL, ScopeMapping: datascope.Mapping{RequiredLabels: map[string]string{"tenant": "tenant-a"}, Scopes: map[string][]string{"cluster": {"cluster-a"}, "namespace": {"apps"}}}}
	a, err := NewVictoria("victorialogs", b, server.Client(), func(context.Context, Binding) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	id := resource.CanonicalID{Domain: "k8s", Tenant: "tenant-a", Scope: "cluster-a", APIGroup: "core", Kind: "Node", StableID: "node-a"}
	q := Query{ResourceCanonicalID: id.String(), Template: "node-kernel-logs/v1", From: now.Add(-time.Minute), To: now.Add(time.Second), Limit: 10, Scope: graph.Scope{Tenant: "tenant-a", Cluster: "cluster-a", ClusterScoped: true, Namespaces: []string{"apps"}}}
	got, err := a.Query(context.Background(), q)
	if err != nil || got.Partial || len(got.Evidence) != 1 || reads != 1 {
		t.Fatalf("Node native log unavailable: %+v %v reads=%d", got, err, reads)
	}
	id.Kind = "Pod"
	q.ResourceCanonicalID = id.String()
	if _, err := a.Query(context.Background(), q); err == nil {
		t.Fatal("cluster permission expanded to Pod")
	}
	id.Kind = "Node"
	q.ResourceCanonicalID = id.String()
	q.Namespace = "restricted"
	if _, err := a.Query(context.Background(), q); err == nil {
		t.Fatal("Node template accepted invented namespace")
	}
}
