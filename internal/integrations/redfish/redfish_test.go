package redfish

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func vendorFixture(t *testing.T, vendor string) map[string]json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile("../../../test/fixtures/redfish/" + vendor + "/inventory.json")
	if err != nil {
		t.Fatal(err)
	}
	var routes map[string]json.RawMessage
	if err = json.Unmarshal(raw, &routes); err != nil {
		t.Fatal(err)
	}
	return routes
}
func collectFixture(t *testing.T, routes map[string]json.RawMessage) Inventory {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("write operation")
			w.WriteHeader(405)
			return
		}
		raw, ok := routes[r.URL.Path]
		if !ok {
			t.Logf("missing fixture path %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		w.Write(raw)
	}))
	defer s.Close()
	got, err := Collect(context.Background(), Config{Endpoint: s.URL, Tenant: "tenant-a", Scope: "dc-a", SourceID: "bmc-1", Client: s.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return got
}
func TestRedfishThreeVendors(t *testing.T) {
	expected := map[string]string{"dell": "normal", "hpe": "degraded", "lenovo": "critical"}
	for vendor, state := range expected {
		t.Run(vendor, func(t *testing.T) {
			got := collectFixture(t, vendorFixture(t, vendor))
			if got.Partial || len(got.Entities) != 7 || got.Health != state {
				t.Fatalf("inventory %+v", got)
			}
			for _, entity := range got.Entities {
				if entity.CanonicalID == "" || len(entity.Attributes) == 0 {
					t.Fatal("empty inventory mapping")
				}
			}
		})
	}
}

func TestRedfishAuthenticatesInitialServiceRootRead(t *testing.T) {
	routes := vendorFixture(t, "dell")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "reader" || password != "test-only" {
			w.WriteHeader(401)
			return
		}
		if r.Method != "GET" {
			t.Error("write attempted")
			w.WriteHeader(405)
			return
		}
		body, ok := routes[r.URL.Path]
		if !ok {
			w.WriteHeader(404)
			return
		}
		w.Write(body)
	}))
	defer server.Close()
	inventory, err := Collect(context.Background(), Config{Endpoint: server.URL, Tenant: "tenant-a", Scope: "dc-a", SourceID: "bmc-1", Username: "reader", Password: "test-only", Client: server.Client()})
	if err != nil || inventory.Partial || len(inventory.Entities) != 7 {
		t.Fatalf("authenticated inventory failed: %+v %v", inventory, err)
	}
}
func TestRedfishMissingUUIDDuplicateSerialAndPartialInventory(t *testing.T) {
	routes := vendorFixture(t, "dell")
	path := "/redfish/v1/Systems/System.Embedded.1"
	var system map[string]any
	json.Unmarshal(routes[path], &system)
	delete(system, "UUID")
	routes[path], _ = json.Marshal(system)
	got := collectFixture(t, routes)
	if !got.Partial || len(got.Entities) != 0 || got.Health != "unknown" {
		t.Fatalf("missing UUID %+v", got)
	}
	routes = vendorFixture(t, "dell")
	json.Unmarshal(routes[path], &system)
	system["UUID"] = "550e8400-e29b-41d4-a716-446655440001"
	system["Id"] = "second"
	system["@odata.id"] = "/redfish/v1/Systems/second"
	routes["/redfish/v1/Systems/second"], _ = json.Marshal(system)
	routes["/redfish/v1/Systems"] = json.RawMessage(`{"Members":[{"@odata.id":"/redfish/v1/Systems/System.Embedded.1"},{"@odata.id":"/redfish/v1/Systems/second"}]}`)
	got = collectFixture(t, routes)
	if !got.Partial || len(got.Entities) != 14 || got.Entities[0].CanonicalID == got.Entities[7].CanonicalID {
		t.Fatalf("duplicate serial merge %+v", got)
	}
	routes = vendorFixture(t, "dell")
	delete(routes, path+"/Memory/DIMM1")
	got = collectFixture(t, routes)
	if !got.Partial || len(got.Entities) != 6 {
		t.Fatalf("partial DIMM %+v", got)
	}
}
func TestRedfishAuthAndTimeoutDegrade(t *testing.T) {
	for _, slow := range []bool{false, true} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if slow {
				<-r.Context().Done()
				return
			}
			w.WriteHeader(401)
		}))
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		got, err := Collect(ctx, Config{Endpoint: s.URL, Tenant: "tenant-a", Scope: "dc-a", SourceID: "bmc-1", Client: s.Client()})
		cancel()
		s.Close()
		if err != nil || !got.Partial || got.Health != "unknown" {
			t.Fatalf("outage %+v %v", got, err)
		}
	}
}

func TestDuplicateHardwareUUIDIsAnUnresolvedConflict(t *testing.T) {
	routes := vendorFixture(t, "dell")
	var system map[string]any
	json.Unmarshal(routes["/redfish/v1/Systems/System.Embedded.1"], &system)
	system["Id"] = "second"
	system["@odata.id"] = "/redfish/v1/Systems/second"
	routes["/redfish/v1/Systems/second"], _ = json.Marshal(system)
	routes["/redfish/v1/Systems"] = json.RawMessage(`{"Members":[{"@odata.id":"/redfish/v1/Systems/System.Embedded.1"},{"@odata.id":"/redfish/v1/Systems/second"}]}`)
	got := collectFixture(t, routes)
	if !got.Partial || len(got.Entities) != 0 || got.Health != "unknown" {
		t.Fatalf("ambiguous UUID published: %+v", got)
	}
}
