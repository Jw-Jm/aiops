package redfish

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSP05DIMMReadsTypedCurrentAlarmAndDoesNotUseLifetimeCount(t *testing.T) {
	for _, mode := range []string{"active", "lifetime-only", "missing", "healthy"} {
		t.Run(mode, func(t *testing.T) {
			routes := vendorFixture(t, "dell")
			path := "/redfish/v1/Systems/System.Embedded.1/Memory/DIMM1"
			var memory map[string]any
			if err := json.Unmarshal(routes[path], &memory); err != nil {
				t.Fatal(err)
			}
			health := "Critical"
			if mode == "healthy" {
				health = "OK"
			}
			memory["Status"] = map[string]any{"State": "Enabled", "Health": health}
			memory["Metrics"] = map[string]any{"@odata.id": path + "/MemoryMetrics"}
			routes[path], _ = json.Marshal(memory)
			metrics := map[string]any{"@odata.id": path + "/MemoryMetrics", "Id": "Metrics", "LifeTime": map[string]any{"UncorrectableECCErrorCount": 900}}
			if mode == "active" || mode == "healthy" {
				metrics["HealthData"] = map[string]any{"AlarmTrips": map[string]any{"UncorrectableECCError": mode == "active"}}
			}
			routes[path+"/MemoryMetrics"], _ = json.Marshal(metrics)
			if mode == "missing" {
				delete(routes, path+"/MemoryMetrics")
			}
			reads := 0
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Errorf("nonread method %s", r.Method)
					w.WriteHeader(405)
					return
				}
				if strings.HasSuffix(r.URL.Path, "/MemoryMetrics") {
					reads++
				}
				body, ok := routes[r.URL.Path]
				if !ok {
					w.WriteHeader(404)
					return
				}
				w.Write(body)
			}))
			defer s.Close()
			got, err := Collect(context.Background(), Config{Endpoint: s.URL, Tenant: "tenant-a", Scope: "dc-a", SourceID: "bmc-1", Client: s.Client(), Diagnostics: true})
			if err != nil {
				t.Fatal(err)
			}
			if reads != 1 {
				t.Fatalf("typed metrics endpoint not read: %d", reads)
			}
			healthFacts, eccFacts := 0, 0
			for _, f := range got.DiagnosticFacts {
				if f.RuleID == "redfish/dimm-health/v1" {
					healthFacts++
				}
				if f.RuleID == "redfish/dimm-uncorrectable-ecc/v1" {
					eccFacts++
				}
			}
			if healthFacts != 1 || eccFacts != map[string]int{"active": 1, "healthy": 1, "lifetime-only": 0, "missing": 0}[mode] {
				t.Fatalf("unsafe/missing current facts: %+v", got.DiagnosticFacts)
			}
			if mode == "missing" || mode == "lifetime-only" {
				if !got.Partial {
					t.Fatal("missing alarm treated as verified")
				}
			}
		})
	}
}
