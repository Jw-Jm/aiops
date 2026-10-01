package openbao

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestRuntimeTransitRoleRequiresProjectedTokenAudience(t *testing.T) {
	ca, key := makeTestCA(t)
	var body map[string]any
	server, client := newPKITestServer(t, ca, key, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	defer server.Close()
	if err := client.ensureKubernetesRole(context.Background(), "ops-worker", "ops-worker"); err != nil {
		t.Fatal(err)
	}
	if body["audience"] != "openbao" {
		t.Fatalf("runtime Transit role audience=%v, expected openbao", body["audience"])
	}
}

func TestServiceDomainBindsTheInstalledNamespace(t *testing.T) {
	for domain, expected := range map[string]string{
		"ops-system.svc.cluster.local":               "ops-system",
		"ops-sp03-review-20261001.svc.cluster.local": "ops-sp03-review-20261001",
	} {
		if got := namespaceFromServiceDomain(domain); got != expected {
			t.Errorf("domain %q bound namespace %q; expected %q", domain, got, expected)
		}
	}
}
