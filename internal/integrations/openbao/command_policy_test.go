package openbao

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestCommandTransitInitializationScopeAndRerun(t *testing.T) {
	for _, scope := range []string{"base", "transit", "ssh", "drift"} {
		t.Run(scope, func(t *testing.T) {
			policies := map[string]string{}
			for _, name := range []string{"ops-api-workload", "ops-worker-workload"} {
				policies[name] = runtimePolicies[name]
				if scope == "transit" || scope == "ssh" {
					policies[name] += commandTransitPolicy
				}
			}
			if scope == "ssh" {
				policies["ops-api-workload"] += commandSSHPolicy
			}
			if scope == "drift" {
				policies["ops-api-workload"] += `path "secret/*" { capabilities = ["read"] }`
			}
			writes := 0
			ca, key := makeTestCA(t)
			server, client := newPKITestServer(t, ca, key, func(w http.ResponseWriter, r *http.Request) {
				name := strings.TrimPrefix(r.URL.Path, "/v1/sys/policies/acl/")
				if _, ok := policies[name]; !ok {
					t.Errorf("unexpected initialization request: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
					return
				}
				if r.Method == http.MethodPut {
					var body struct{ Policy string }
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					policies[name] = body.Policy
					writes++
					w.WriteHeader(http.StatusNoContent)
					return
				}
				writeJSON(w, map[string]any{"data": map[string]any{"policy": policies[name]}})
			})
			defer server.Close()
			err := client.ConfigureCommandExecutionTransit(context.Background())
			if scope == "drift" {
				if err == nil || writes != 0 {
					t.Fatalf("drift overwritten: err=%v writes=%d", err, writes)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for name, got := range policies {
				want := runtimePolicies[name] + commandTransitPolicy
				if scope == "ssh" && name == "ops-api-workload" {
					want += commandSSHPolicy
				}
				if got != want {
					t.Fatalf("%s policy changed scope: %s", name, got)
				}
			}
			before := writes
			if err := client.ConfigureCommandExecutionTransit(context.Background()); err != nil || writes != before {
				t.Fatalf("rerun changed policy: err=%v writes=%d -> %d", err, before, writes)
			}
			// Explicit SSH initialization can extend the known Transit-only state.
			if err := client.extendCommandPolicy(context.Background(), "ops-api-workload", runtimePolicies["ops-api-workload"]+commandTransitPolicy+commandSSHPolicy); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestConfigureAcceptsAnAlreadyExtendedCommandPolicy pins that re-running the
// standard OpenBao configuration after Transit or explicit SSH provisioning does
// not report least-privilege drift, while an unknown extension still fails.
func TestConfigureAcceptsAnAlreadyExtendedCommandPolicy(t *testing.T) {
	for _, scope := range []string{"base", "transit", "ssh", "drift"} {
		t.Run(scope, func(t *testing.T) {
			current := map[string]string{}
			for name := range runtimePolicies {
				current[name] = runtimePolicies[name]
			}
			if scope != "base" {
				current["ops-api-workload"] += commandTransitPolicy
				current["ops-worker-workload"] += commandTransitPolicy
			}
			if scope == "ssh" {
				current["ops-api-workload"] += commandSSHPolicy
			}
			if scope == "drift" {
				current["ops-worker-workload"] += `path "secret/*" { capabilities = ["read"] }`
			}
			writes := 0
			ca, key := makeTestCA(t)
			server, client := newPKITestServer(t, ca, key, func(w http.ResponseWriter, r *http.Request) {
				name := strings.TrimPrefix(r.URL.Path, "/v1/sys/policies/acl/")
				policy, ok := current[name]
				if !ok {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
					return
				}
				if r.Method == http.MethodPut {
					writes++
				}
				writeJSON(w, map[string]any{"data": map[string]any{"policy": policy}})
			})
			defer server.Close()
			for name, policy := range runtimePolicies {
				err := client.ensurePolicy(context.Background(), name, policy)
				if scope == "drift" && name == "ops-worker-workload" {
					if err == nil {
						t.Fatal("unknown policy extension accepted")
					}
					continue
				}
				if err != nil {
					t.Fatalf("%s: %v", name, err)
				}
			}
			if writes != 0 {
				t.Fatalf("idempotent configure rewrote %d policies", writes)
			}
		})
	}
}
