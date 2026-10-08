package bundle

import (
	"bytes"
	"context"
	"encoding/json"
	artifacts "ops-platform/bundle"
	"ops-platform/internal/resource"
	"ops-platform/internal/supplychain"
	"strings"
	"testing"
	"time"
)

func validBusinessDocument() map[string]any {
	sources := []any{}
	now := time.Now().UTC()
	for i, name := range []string{"victoriametrics", "victorialogs"} {
		id := "44444444-4444-4444-8444-444444444444"
		template := "pod-phase/v1"
		if i == 1 {
			id = "55555555-5555-4555-8555-555555555555"
			template = "resource-logs/v1"
		}
		canonical := resource.CanonicalID{Domain: "k8s", Tenant: "11111111-1111-4111-8111-111111111111", Scope: "current-cluster", APIGroup: "core", Kind: "Pod", StableID: "current-pod"}.String()
		sources = append(sources, map[string]any{"Name": name, "Binding": map[string]any{"Tenant": "11111111-1111-4111-8111-111111111111", "SourceID": id, "SourceType": name, "Revision": 1, "BackendLogicalID": "current-" + name, "Endpoint": "https://10.0.0.20:8443", "SourceRetentionSeconds": 3600, "ScopeMapping": map[string]any{"scopes": map[string]any{"cluster": []string{"current-cluster"}, "namespace": []string{"apps"}}, "requiredLabels": map[string]any{"tenant": "11111111-1111-4111-8111-111111111111"}}}, "CAFile": "/etc/sp04/sources/" + name + "-ca.pem", "CredentialFile": "/etc/sp04/sources/" + name + "-credential", "ScopeProbe": map[string]any{"resourceCanonicalId": canonical, "namespace": "apps", "queryTemplate": template, "from": now.Add(-time.Minute).Format(time.RFC3339Nano), "to": now.Format(time.RFC3339Nano), "limit": 1}})
	}
	return map[string]any{
		"schemaVersion":    1,
		"namespace":        "ops-system",
		"workloadIdentity": map[string]any{"enabled": true, "mode": "openbao-kubernetes"},
		"sp04":             map[string]any{"enabled": true, "graphPort": 8082, "workerReplicas": 2, "apiIdentitySecret": "ops-sp04-api-identity", "workerIdentitySecret": "ops-sp04-worker-identity", "sourceCredentialsSecret": "ops-sp04-source-credentials", "allowedWorkerCIDRs": []string{"10.42.0.0/24"}, "archiveBackendLogicalID": "archive-current", "clusters": []any{map[string]any{"Tenant": "11111111-1111-4111-8111-111111111111", "ClusterUID": "current-cluster", "SourceID": "22222222-2222-4222-8222-222222222222", "BackendLogicalID": "current-kubernetes", "Endpoint": "https://10.0.0.1:6443", "CAFile": "/var/run/secrets/ops-platform/kubernetes/ca.pem", "TokenFile": "/var/run/secrets/ops-platform/kubernetes/token", "LeaseNamespace": "ops-system", "LeaseName": "ops-graph-current", "SourceRevision": 1, "QPS": 10, "Burst": 25}}, "sources": sources, "kubernetesAPI": map[string]any{"cidrs": []string{"10.0.0.1/32"}, "port": 6443, "localCollector": true}, "leaseNames": []string{"ops-graph-current"}},
		"sp05":             map[string]any{"enabled": true, "ingestionBindings": []any{}, "analyzer": map[string]any{"enabled": true, "sha256": "6f9152ff31d2692a14e880ae73c2fe35dbc2938560d2c55227f4b0c67409af53"}},
		"sp06":             map[string]any{"enabled": true, "gatewayPort": 8083, "policyName": "read-only-current", "identitySecret": "ops-sp06-investigator-identity", "modelKeySecret": "ops-sp06-model-key", "tenants": []string{"11111111-1111-4111-8111-111111111111"}, "modelCIDRs": []string{"192.168.139.1/32"}, "modelPort": 11434, "model": map[string]any{"base_url": "http://192.168.139.1:11434/v1", "api_key_ref": "secret://model/api-key", "model": "llama3.1:8b-16k", "timeout": 45, "token_budget": 1024}, "budget": map[string]any{"durationSeconds": 600, "toolCalls": 40, "rawQueries": 10, "resultBytes": 41943040, "graphNodes": 8000, "evidenceItems": 4000, "modelRequests": 8, "inputTokens": 32768, "outputTokens": 8192, "modelCostMicros": 0, "allowedDataClasses": []string{"D0", "D1"}}},
	}
}
func readBusinessTest(t *testing.T, d map[string]any) BusinessValues {
	t.Helper()
	b, _ := json.Marshal(d)
	v, err := ReadBusinessValues(strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func TestBusinessValuesRejectUnsafeAndIncompleteConfiguration(t *testing.T) {
	cases := map[string]func(map[string]any){
		"missing install namespace":   func(d map[string]any) { delete(d, "namespace") },
		"reserved install namespace":  func(d map[string]any) { d["namespace"] = "kube-system" },
		"missing continuous identity": func(d map[string]any) { delete(d, "workloadIdentity") },
		"static workload identity":    func(d map[string]any) { d["workloadIdentity"].(map[string]any)["mode"] = "file" },
		"missing tenant":              func(d map[string]any) { delete(d["sp06"].(map[string]any), "tenants") },
		"missing policy":              func(d map[string]any) { d["sp06"].(map[string]any)["policyName"] = "" },
		"disabled SP05":               func(d map[string]any) { d["sp05"].(map[string]any)["enabled"] = false },
		"generic overrides": func(d map[string]any) {
			d["components"] = map[string]any{"command-runner": map[string]any{"enabled": true}}
		},
		"public model": func(d map[string]any) { d["sp06"].(map[string]any)["modelCIDRs"] = []string{"1.1.1.1/32"} },
		"broad model":  func(d map[string]any) { d["sp06"].(map[string]any)["modelCIDRs"] = []string{"192.168.139.0/24"} },
		"wrong model host": func(d map[string]any) {
			d["sp06"].(map[string]any)["model"].(map[string]any)["base_url"] = "http://192.168.1.2:11434/v1"
		},
		"budget expansion": func(d map[string]any) { d["sp06"].(map[string]any)["budget"].(map[string]any)["toolCalls"] = 41 },
		"missing source":   func(d map[string]any) { d["sp04"].(map[string]any)["clusters"] = []any{} },
		"cross tenant cluster": func(d map[string]any) {
			d["sp04"].(map[string]any)["clusters"].([]any)[0].(map[string]any)["Tenant"] = "33333333-3333-4333-8333-333333333333"
		},
		"shared workload identity": func(d map[string]any) {
			d["sp04"].(map[string]any)["workerIdentitySecret"] = d["sp04"].(map[string]any)["apiIdentitySecret"]
		},
		"aliased remote credential path": func(d map[string]any) {
			d["sp04"].(map[string]any)["kubernetesAPI"].(map[string]any)["localCollector"] = false
			d["sp04"].(map[string]any)["clusters"].([]any)[0].(map[string]any)["CAFile"] = "/etc/sp04/sources/../sources/ca.pem"
			d["sp04"].(map[string]any)["clusters"].([]any)[0].(map[string]any)["TokenFile"] = "/etc/sp04/sources/token"
		},
		"credential plaintext": func(d map[string]any) {
			d["sp06"].(map[string]any)["model"].(map[string]any)["api_key"] = "must-not-be-accepted"
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			d := validBusinessDocument()
			mutate(d)
			b, _ := json.Marshal(d)
			if _, err := ReadBusinessValues(strings.NewReader(string(b))); err == nil {
				t.Fatal("unsafe current configuration accepted")
			}
		})
	}
	readBusinessTest(t, validBusinessDocument())
}
func TestBusinessValuesNeverOverrideAuthenticatedImages(t *testing.T) {
	v := readBusinessTest(t, validBusinessDocument())
	images := map[string]string{"platform-api": "signed-api", "platform-worker": "signed-worker", "holmesgpt": "signed-investigator"}
	values := map[string]any{"components": map[string]any{"api": map[string]any{"image": images["platform-api"]}, "worker": map[string]any{"image": images["platform-worker"]}, "web": map[string]any{"enabled": false}, "investigator": map[string]any{"enabled": false}, "command-runner": map[string]any{"enabled": false}}}
	if err := applyBusinessValues(values, v, images); err != nil {
		t.Fatal(err)
	}
	c := values["components"].(map[string]any)
	if c["investigator"].(map[string]any)["image"] != "signed-investigator" || c["investigator"].(map[string]any)["enabled"] != true || c["command-runner"].(map[string]any)["enabled"] != false {
		t.Fatal("authenticated component boundary lost")
	}
	delete(images, "holmesgpt")
	if err := applyBusinessValues(values, v, images); err == nil {
		t.Fatal("missing authenticated investigator accepted")
	}
}
func TestBusinessSecretPreflightUsesOnlyPresenceQueries(t *testing.T) {
	v := readBusinessTest(t, validBusinessDocument())
	calls := 0
	run := func(_ context.Context, program string, args ...string) ([]byte, error) {
		calls++
		if program != "kubectl" || !strings.Contains(strings.Join(args, " "), "go-template=") || strings.Contains(strings.Join(args, " "), "-o json") {
			t.Fatal("secret value query")
		}
		return []byte("present"), nil
	}
	if err := preflightBusinessSecrets(context.Background(), importProfile(), v, run); err != nil {
		t.Fatal(err)
	}
	if calls < 8 {
		t.Fatal("identity/model/context key checks absent")
	}
	if err := preflightBusinessSecrets(context.Background(), importProfile(), v, func(context.Context, string, ...string) ([]byte, error) { return []byte("absent"), nil }); err == nil {
		t.Fatal("missing identity accepted")
	}
}

func TestCurrentBusinessNamespaceFencesLeaseAndSecretLookups(t *testing.T) {
	d := validBusinessDocument()
	d["namespace"] = "ops-fresh-current"
	raw, _ := json.Marshal(d)
	if _, err := ReadBusinessValues(strings.NewReader(string(raw))); err == nil {
		t.Fatal("historical lease namespace accepted for a fresh installation")
	}
	object(d, "sp04")["clusters"].([]any)[0].(map[string]any)["LeaseNamespace"] = "ops-fresh-current"
	b := readBusinessTest(t, d)
	if b.Namespace() != "ops-fresh-current" {
		t.Fatal("installation scope lost")
	}
	if err := preflightBusinessSecrets(t.Context(), importProfile(), b, func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if !strings.Contains(strings.Join(args, " "), "-n ops-fresh-current") {
			t.Fatal("secret lookup borrowed another namespace")
		}
		return []byte("present"), nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCurrentInstallationRequiresBothVictoriaSourcesAndQualifiedScope(t *testing.T) {
	for name, mutate := range map[string]func(map[string]any){
		"no fact sources": func(d map[string]any) { d["sp04"].(map[string]any)["sources"] = []any{} },
		"missing log source": func(d map[string]any) {
			d["sp04"].(map[string]any)["sources"] = d["sp04"].(map[string]any)["sources"].([]any)[:1]
		},
		"another cluster": func(d map[string]any) {
			source := d["sp04"].(map[string]any)["sources"].([]any)[0].(map[string]any)
			object(object(source, "Binding"), "ScopeMapping")["scopes"].(map[string]any)["cluster"] = []string{"foreign-cluster"}
		},
		"unqualified native tenant": func(d map[string]any) {
			source := d["sp04"].(map[string]any)["sources"].([]any)[0].(map[string]any)
			object(object(source, "Binding"), "ScopeMapping")["nativeTenant"] = "unqualified"
		},
		"missing tenant equality": func(d map[string]any) {
			source := d["sp04"].(map[string]any)["sources"].([]any)[0].(map[string]any)
			object(object(source, "Binding"), "ScopeMapping")["requiredLabels"] = map[string]any{}
		},
		"unsupported probe tool": func(d map[string]any) {
			source := d["sp04"].(map[string]any)["sources"].([]any)[0].(map[string]any)
			object(source, "ScopeProbe")["queryTemplate"] = "execute_command"
		},
		"probe window expansion": func(d map[string]any) {
			source := d["sp04"].(map[string]any)["sources"].([]any)[0].(map[string]any)
			object(source, "ScopeProbe")["from"] = time.Now().Add(-2 * time.Hour).Format(time.RFC3339Nano)
		},
		"foreign probe": func(d map[string]any) {
			source := d["sp04"].(map[string]any)["sources"].([]any)[0].(map[string]any)
			object(source, "ScopeProbe")["namespace"] = "foreign"
		},
	} {
		t.Run(name, func(t *testing.T) {
			d := validBusinessDocument()
			mutate(d)
			raw, _ := json.Marshal(d)
			if _, err := ReadBusinessValues(strings.NewReader(string(raw))); err == nil {
				t.Fatal("incomplete or unqualified current source configuration accepted")
			}
		})
	}
}

func TestCurrentModelUsesExplicitOrbStackBridgeWithoutBroadeningFactSources(t *testing.T) {
	d := validBusinessDocument()
	sp06 := object(d, "sp06")
	sp06["modelNetworkMode"] = "orbstack-host"
	sp06["modelCIDRs"] = []string{"0.250.250.254/32"}
	object(sp06, "model")["base_url"] = "http://host.docker.internal:11434/v1"
	readBusinessTest(t, d)
	for _, endpoint := range []string{"http://0.250.250.254:11434/v1", "http://attacker.example:11434/v1", "http://host.docker.internal:11434/v1/arbitrary"} {
		object(sp06, "model")["base_url"] = endpoint
		raw, _ := json.Marshal(d)
		if _, err := ReadBusinessValues(strings.NewReader(string(raw))); err == nil {
			t.Fatal("bridge admitted another target or path")
		}
	}
	object(sp06, "model")["base_url"] = "http://host.docker.internal:11434/v1"
	sp06["modelCIDRs"] = []string{"0.250.250.0/24"}
	raw, _ := json.Marshal(d)
	if _, err := ReadBusinessValues(strings.NewReader(string(raw))); err == nil {
		t.Fatal("bridge admitted broad egress")
	}
}

func TestCurrentBusinessUsesAdmittedHolmesMaterialName(t *testing.T) {
	evidence, err := artifacts.ComponentEvidence()
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := supplychain.LoadCatalogWithEvidence(bytes.NewReader(artifacts.ComponentCatalog()), evidence)
	if err != nil {
		t.Fatal(err)
	}
	holmes, ok := catalog.Component("holmesgpt")
	if !ok || holmes.State != "qualified" {
		t.Fatal("locked Holmes material not admitted")
	}
	image := "ops/investigator@" + holmes.Digest
	values := map[string]any{"components": map[string]any{}}
	if err := applyBusinessValues(values, readBusinessTest(t, validBusinessDocument()), map[string]string{"holmesgpt": image}); err != nil {
		t.Fatal(err)
	}
	if textValue(object(object(values, "components"), "investigator"), "image") != image {
		t.Fatal("workload not bound to exact admitted Holmes image")
	}
	if err := applyBusinessValues(values, readBusinessTest(t, validBusinessDocument()), map[string]string{"investigator": "unqualified@" + holmes.Digest}); err == nil {
		t.Fatal("invented material alias accepted")
	}
}
