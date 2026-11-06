package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"ops-platform/internal/graph"
	"ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/observationquery"
	"testing"
)

func TestObservationAdmissionRejectsSameNameReplacement(t *testing.T) {
	uid := "original"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/namespaces/apps/pods/web" {
			t.Error("identity lookup left its exact scope:", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		if uid == "deleted" {
			w.WriteHeader(404)
			return
		}
		_, _ = w.Write([]byte(`{"metadata":{"name":"web","namespace":"apps","uid":"` + uid + `"}}`))
	}))
	defer server.Close()
	client, err := kubernetes.NewClient(server.URL, server.Client(), 20, 20)
	if err != nil {
		t.Fatal(err)
	}
	service := observationService(nil, nil, graph.InternalHandler{Lease: &graph.Lease{Client: client}}, SP04Cluster{Namespaces: []string{"apps"}})
	ns, nsuid := "apps", "namespace-one"
	target := observationquery.Resource{Name: "web", UID: "original", NamespaceScope: observationquery.NamespaceScope{Kind: "namespaced", Namespace: &ns, NamespaceUID: &nsuid}}
	if err = service.CheckResource(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	uid = "replacement"
	if err = service.CheckResource(context.Background(), target); !errors.Is(err, observationquery.ErrDenied) {
		t.Fatal("same-name replacement admitted:", err)
	}
	uid = "deleted"
	if err = service.CheckResource(context.Background(), target); !errors.Is(err, observationquery.ErrDenied) {
		t.Fatal("missing native Pod admitted:", err)
	}
}

func TestObservationNativeLogsRequiresExplicitClusterOptIn(t *testing.T) {
	owner := graph.InternalHandler{Lease: &graph.Lease{}}
	for _, enabled := range []bool{false, true} {
		service := observationService(nil, nil, owner, SP04Cluster{Namespaces: []string{"apps"}})
		configureNativeLogs(&service, owner, SP04Cluster{PodLogs: enabled, SourceID: "11111111-1111-4111-8111-111111111111", SourceRevision: 3})
		if (service.NativeLogs != nil) != enabled {
			t.Fatal("native source flag not respected")
		}
	}
}

func TestObservationClusterWatchListsOnlyRegisteredAndGrantedNamespaces(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/namespaces":
			_, _ = w.Write([]byte(`{"items":[{"metadata":{"name":"apps","uid":"ns-apps"}},{"metadata":{"name":"other","uid":"ns-other"}},{"metadata":{"name":"foreign","uid":"ns-foreign"}}]}`))
		case "/api/v1/pods":
			_, _ = w.Write([]byte(`{"items":[{"metadata":{"name":"web","namespace":"apps","uid":"web-one"}},{"metadata":{"name":"db","namespace":"other","uid":"db-one"}},{"metadata":{"name":"foreign","namespace":"foreign","uid":"foreign-one"}}]}`))
		default:
			t.Error("unexpected lookup:", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := kubernetes.NewClient(server.URL, server.Client(), 10, 25)
	if err != nil {
		t.Fatal(err)
	}
	owner := graph.InternalHandler{Lease: &graph.Lease{Client: client}, ValidateSources: func(context.Context) error { return nil }}
	cluster := SP04Cluster{ClusterWatch: true, Namespaces: []string{"apps", "other"}}
	service := observationService(nil, nil, owner, cluster)
	rows, err := service.Resources(t.Context(), graph.Scope{Tenant: "tenant", Cluster: "cluster", ClusterScoped: true, Namespaces: []string{"apps", "foreign"}})
	if err != nil || len(rows) != 1 || rows[0].Name != "web" || *rows[0].NamespaceScope.NamespaceUID != "ns-apps" {
		t.Fatalf("unexpected scope: %v, %v", rows, err)
	}
}
