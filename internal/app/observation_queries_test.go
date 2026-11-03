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
	service := observationService(nil, nil, graph.InternalHandler{Lease: &graph.Lease{Client: client}}, []string{"apps"})
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
		service := observationService(nil, nil, owner, []string{"apps"})
		configureNativeLogs(&service, owner, SP04Cluster{PodLogs: enabled, SourceID: "11111111-1111-4111-8111-111111111111", SourceRevision: 3})
		if (service.NativeLogs != nil) != enabled {
			t.Fatal("native source flag not respected")
		}
	}
}
