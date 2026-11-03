package openbao

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestNativeMFAProvisioningIsNarrowAndDriftSafe(t *testing.T) {
	for _, scenario := range []string{"valid", "missing", "exportable", "backup", "foreign-role", "foreign-policy"} {
		t.Run(scenario, func(t *testing.T) {
			policies := map[string]string{}
			role := map[string]any{"bound_service_account_names": []any{"ops-api"}, "bound_service_account_namespaces": []any{"native-test"}, "audience": "openbao", "policies": []any{"ops-api-workload"}, "token_policies": []any{"ops-api-workload"}, "token_ttl": "15m", "token_max_ttl": "1h"}
			if scenario == "foreign-role" {
				role["bound_service_account_namespaces"] = []any{"foreign"}
			}
			if scenario == "foreign-policy" {
				policies["ops-native-mfa-native-test"] = `path "transit/*" { capabilities=["update"] }`
			}
			writes := []string{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					writes = append(writes, r.URL.Path)
				}
				switch {
				case r.URL.Path == "/v1/transit/keys/native-test":
					if r.Method != http.MethodGet {
						t.Error("key mutation")
						w.WriteHeader(500)
						return
					}
					if scenario == "missing" {
						w.WriteHeader(404)
						return
					}
					_ = json.NewEncoder(w).Encode(dataResponse{Data: map[string]any{"type": "aes256-gcm96", "exportable": scenario == "exportable", "allow_plaintext_backup": scenario == "backup"}})
				case strings.HasPrefix(r.URL.Path, "/v1/sys/policies/acl/"):
					name := strings.TrimPrefix(r.URL.Path, "/v1/sys/policies/acl/")
					if r.Method == http.MethodPost {
						var body map[string]string
						_ = json.NewDecoder(r.Body).Decode(&body)
						policies[name] = body["policy"]
						w.WriteHeader(204)
						return
					}
					if policy, ok := policies[name]; ok {
						_ = json.NewEncoder(w).Encode(dataResponse{Data: map[string]any{"policy": policy}})
					} else {
						w.WriteHeader(404)
					}
				case r.URL.Path == "/v1/auth/kubernetes/role/ops-api-workload":
					if r.Method == http.MethodPost {
						_ = json.NewDecoder(r.Body).Decode(&role)
						w.WriteHeader(204)
						return
					}
					_ = json.NewEncoder(w).Encode(dataResponse{Data: role})
				default:
					t.Errorf("unexpected endpoint %s", r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			client := &Client{http: server.Client(), address: server.URL, serviceDomain: "native-test.svc.cluster.local", token: "test-operator", nativeMFAKey: "native-test"}
			err := client.configureNativeMFA(context.Background())
			if err == nil {
				err = client.ensureKubernetesWorkloadRole(context.Background(), "ops-api-workload", "ops-api")
			}
			if scenario != "valid" {
				if !errors.Is(err, ErrConfigurationDrift) {
					t.Fatalf("drift accepted: %v", err)
				}
				if scenario != "foreign-role" && len(writes) != 0 {
					t.Fatalf("invalid key/policy caused writes: %v", writes)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			policy := policies["ops-native-mfa-native-test"]
			if strings.Contains(policy, "*") || strings.Count(policy, "capabilities") != 2 || !strings.Contains(policy, "transit/decrypt/native-test") {
				t.Fatalf("unexpected policy %s", policy)
			}
			if !reflect.DeepEqual(client.workloadPolicies("ops-worker-workload"), []any{"ops-worker-workload"}) {
				t.Fatal("worker received MFA authority")
			}
			before := len(writes)
			if err := client.verifyNativeMFA(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(writes) != before {
				t.Fatal("status verification wrote configuration")
			}
			if err := client.configureNativeMFA(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := client.ensureKubernetesWorkloadRole(context.Background(), "ops-api-workload", "ops-api"); err != nil {
				t.Fatal(err)
			}
			if len(writes) != before {
				t.Fatal("idempotent rerun wrote configuration")
			}
			policies["ops-native-mfa-native-test"] = `path "transit/*" { capabilities=["update"] }`
			if err := client.verifyNativeMFA(context.Background()); !errors.Is(err, ErrConfigurationDrift) {
				t.Fatalf("status accepted policy drift: %v", err)
			}
		})
	}
}

func TestNativeMFARejectsSharedAndPathKeyNames(t *testing.T) {
	for _, key := range []string{"../other", "*", "evidence-archive", "audit-signing", "investigation-signing"} {
		if _, err := NewClient(ClientConfig{NativeMFAKey: key}); err == nil || !strings.Contains(err.Error(), "distinct Transit") {
			t.Fatalf("key %q accepted: %v", key, err)
		}
	}
}
