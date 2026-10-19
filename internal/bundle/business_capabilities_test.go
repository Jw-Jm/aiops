package bundle

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	"ops-platform/internal/modelprofile"
)

// Admission is test data only; no live model qualification is inferred.
func capabilityDocument(t *testing.T) map[string]any {
	t.Helper()
	d := validBusinessDocument()
	raw, err := os.ReadFile("../../test/fixtures/assistant/model-profile.fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	var p modelprofile.Profile
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	model := object(object(d, "sp06"), "model")
	p.AdmissionState = "admitted"
	p.BaseURL = textValue(model, "base_url")
	p.ModelID = textValue(model, "model")
	p.TimeoutSeconds = 45
	profile, _ := json.Marshal(p)
	var material map[string]any
	json.Unmarshal(profile, &material)
	object(d, "sp06")["assistant"] = map[string]any{"enabled": true, "faultChainEnabled": true, "modelProfileVersion": p.Reference(), "profile": material}
	d["sp08"] = map[string]any{"enabled": true, "publicOrigin": "https://ops.example.invalid:30443", "tlsSecretName": "ops-web-tls", "oidcCAConfigMapName": "ops-web-oidc-ca", "oidc": map[string]any{"authority": "https://ops-keycloak:8443/realms/ops", "proxyHost": "ops-keycloak", "clientId": "ops-web"}, "service": map[string]any{"type": "NodePort", "port": 8443, "nodePort": 30443}, "ingressCIDRs": []string{"192.0.2.0/24"}}
	return d
}

func TestBusinessCapabilitiesAdmission(t *testing.T) {
	readBusinessTest(t, capabilityDocument(t))
	// Assistant dispatch uses its independent Profile; the legacy model contract stays unchanged.
	different := capabilityDocument(t)
	a := object(object(different, "sp06"), "assistant")
	material := object(a, "profile")
	material["modelID"] = "independent-assistant-model"
	encoded, _ := json.Marshal(material)
	var frozen modelprofile.Profile
	json.Unmarshal(encoded, &frozen)
	a["modelProfileVersion"] = frozen.Reference()
	readBusinessTest(t, different)
	for name, mutate := range map[string]func(map[string]any){
		"fixture": func(d map[string]any) {
			object(object(object(d, "sp06"), "assistant"), "profile")["admissionState"] = "fixture_only"
		},
		"reference": func(d map[string]any) { object(object(d, "sp06"), "assistant")["modelProfileVersion"] = "other" },
		"endpoint": func(d map[string]any) {
			object(object(d, "sp06"), "model")["base_url"] = "http://192.168.139.1:11434/other"
		},
		"model":   func(d map[string]any) { object(object(d, "sp06"), "model")["model"] = "other-model" },
		"timeout": func(d map[string]any) { object(object(d, "sp06"), "model")["timeout"] = 46 },
		"disabled fault chain": func(d map[string]any) {
			object(d, "sp06")["assistant"] = map[string]any{"enabled": false, "faultChainEnabled": true}
		},
		"origin injection":   func(d map[string]any) { object(d, "sp08")["publicOrigin"] = "https://ops.invalid'; include evil; #" },
		"origin port":        func(d map[string]any) { object(d, "sp08")["publicOrigin"] = "https://ops.invalid:65536" },
		"unbounded ingress":  func(d map[string]any) { object(d, "sp08")["ingressCIDRs"] = []string{"0.0.0.0/0"} },
		"invalid ingress":    func(d map[string]any) { object(d, "sp08")["ingressCIDRs"] = []string{"192.0.2.1/24"} },
		"image override":     func(d map[string]any) { object(d, "sp08")["image"] = "unsigned" },
		"release override":   func(d map[string]any) { object(d, "sp08")["release"] = map[string]any{"signature": "fake"} },
		"api override":       func(d map[string]any) { object(d, "sp08")["apiHost"] = "other" },
		"NodePort missing":   func(d map[string]any) { delete(object(object(d, "sp08"), "service"), "nodePort") },
		"ClusterIP NodePort": func(d map[string]any) { object(object(d, "sp08"), "service")["type"] = "ClusterIP" },
	} {
		t.Run(name, func(t *testing.T) {
			d := capabilityDocument(t)
			mutate(d)
			raw, _ := json.Marshal(d)
			if _, err := ReadBusinessValues(bytes.NewReader(raw)); err == nil {
				t.Fatal("unsafe capability accepted")
			}
		})
	}
	pending := pendingModelDocument()
	object(pending, "sp06")["assistant"] = object(capabilityDocument(t), "sp06")["assistant"]
	raw, _ := json.Marshal(pending)
	if _, err := ReadBusinessValues(bytes.NewReader(raw)); err == nil {
		t.Fatal("pending model enabled assistant")
	}
	// The legacy Holmes model contract keeps its original allowlist.
	d := validBusinessDocument()
	object(object(d, "sp06"), "model")["model"] = "other-model"
	raw, _ = json.Marshal(d)
	if _, err := ReadBusinessValues(bytes.NewReader(raw)); err == nil {
		t.Fatal("unadmitted model accepted")
	}
}

func TestWebProfileAndAuthenticatedMaterialBoundary(t *testing.T) {
	b := readBusinessTest(t, capabilityDocument(t))
	p := importProfile()
	p.Architecture = "amd64"
	keycloak := p.Components["keycloak"]
	keycloak.Endpoint = "https://ops-keycloak:8443"
	p.Components["keycloak"] = keycloak
	if err := validateWebProfile(b, p); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"https://other:8443", "https://ops-keycloak:443", "https://ops-keycloak:8443/path"} {
		keycloak.Endpoint = endpoint
		p.Components["keycloak"] = keycloak
		if err := validateWebProfile(b, p); err == nil {
			t.Fatal("mismatched OIDC upstream accepted")
		}
	}
	p.Architecture = "arm64"
	if _, err := InstallCurrent(t.Context(), Manifest{}, TrustRoot{}, p, nil, func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("unsupported architecture reached cluster")
		return nil, nil
	}, b, "dependencies"); err == nil {
		t.Fatal("unsupported Web architecture accepted")
	}
	values := map[string]any{"components": map[string]any{}}
	images := map[string]string{"holmesgpt": "signed-holmes", "platform-web": "signed-web"}
	if err := applyBusinessValues(values, b, images); err != nil {
		t.Fatal(err)
	}
	if object(object(values, "components"), "web")["image"] != "signed-web" {
		t.Fatal("Web material pairing lost")
	}
	delete(images, "platform-web")
	if err := applyBusinessValues(values, b, images); err == nil {
		t.Fatal("missing authenticated Web accepted")
	}
	base := readBusinessTest(t, validBusinessDocument())
	if err := applyBusinessValues(values, base, images); err != nil {
		t.Fatal(err)
	}
	if object(values, "sp08")["enabled"] != false || object(object(values, "components"), "web")["enabled"] != false || object(object(values, "sp06"), "assistant")["enabled"] != false {
		t.Fatal("optional defaults enabled capability")
	}
}

func TestWebPreflightAndStageDigests(t *testing.T) {
	b := readBusinessTest(t, capabilityDocument(t))
	b.installStage = "business"
	seen := map[string]bool{}
	run := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		s := strings.Join(args, " ")
		seen[s] = true
		if !strings.Contains(s, "go-template=") {
			t.Fatal("preflight fetched credential contents")
		}
		return []byte("present"), nil
	}
	if err := preflightBusinessSecrets(t.Context(), importProfile(), b, run); err != nil {
		t.Fatal(err)
	}
	all := ""
	for s := range seen {
		all += s + "\n"
	}
	for _, key := range []string{"web.crt", "web.key", "oidc-ca.pem", "get configmap ops-web-oidc-ca", "-n ops-system"} {
		if !strings.Contains(all, key) {
			t.Fatal("missing preflight", key)
		}
	}
	if err := preflightBusinessSecrets(t.Context(), importProfile(), b, func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), "oidc-ca.pem") {
			return []byte("absent"), nil
		}
		return []byte("present"), nil
	}); err == nil {
		t.Fatal("missing OIDC CA accepted")
	}
	for name, mutate := range map[string]func(map[string]any){"Web": func(d map[string]any) { object(d, "sp08")["publicOrigin"] = "https://changed.invalid" }, "fault chain": func(d map[string]any) { object(object(d, "sp06"), "assistant")["faultChainEnabled"] = false }} {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(b.values)
			var changed map[string]any
			json.Unmarshal(raw, &changed)
			mutate(changed)
			next := readBusinessTest(t, changed)
			if businessInstallationDigest(b) == businessInstallationDigest(next) || registeredBusinessInstallationDigest(b) == registeredBusinessInstallationDigest(next) {
				t.Fatal("changed capability retained checkpoint binding")
			}
			// Existing stage verifiers must reject changed switches before reading resources or mutating Helm.
			p := importProfile()
			m := Manifest{BundleID: "current", Payload: Payload{Digest: "sha256:current"}}
			id := "018f0f2b-91c2-7d42-a8dc-f719c5987204"
			for _, stage := range []string{"dependencies-ready", "bootstrap-api-ready"} {
				receipt := currentStageReceipt{Stage: stage, BundleID: m.BundleID, PayloadDigest: m.Payload.Digest, ProfileDigest: jsonDigest(p), BusinessDigest: businessInstallationDigest(b), RegisteredBusinessDigest: registeredBusinessInstallationDigest(b), NamespaceUID: "current-namespace", InstallationID: id}
				raw, _ := json.Marshal(receipt)
				cm, _ := json.Marshal(map[string]any{"metadata": map[string]any{"labels": map[string]any{"ops.platform.io/installation-id": id}}, "data": map[string]string{"receipt.json": string(raw)}})
				ns, _ := json.Marshal(map[string]any{"metadata": map[string]any{"uid": "current-namespace", "labels": map[string]any{"ops.platform.io/managed-by": "opsctl-bootstrap", "ops.platform.io/installation-id": id}}})
				calls := 0
				run := func(_ context.Context, _ string, args ...string) ([]byte, error) {
					calls++
					joined := strings.Join(args, " ")
					if strings.Contains(joined, "get namespace") {
						return ns, nil
					}
					if strings.Contains(joined, "get configmap ops-") {
						return cm, nil
					}
					t.Fatal("changed capability reached resource operations", joined)
					return nil, nil
				}
				var err error
				if stage == "dependencies-ready" {
					err = verifyCurrentStageReceipt(t.Context(), m, p, next, run)
				} else {
					next.registrationVerified = true
					err = verifyCurrentAPIBootstrapReceipt(t.Context(), m, p, &next, run)
				}
				if err == nil || calls != 2 {
					t.Fatalf("changed capability checkpoint accepted: %v calls=%d", err, calls)
				}
			}
		})
	}
}

func TestBusinessCapabilitiesRenderOnlyInBusinessPhase(t *testing.T) {
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm required")
	}
	b := readBusinessTest(t, capabilityDocument(t))
	image := "example.invalid/current@sha256:" + strings.Repeat("a", 64)
	values := map[string]any{"workloadsEnabled": true, "runtime": map[string]any{"openbaoCABundle": "TEST-PUBLIC-CA", "oidcIssuerURL": "https://ops-keycloak:8443/realms/ops", "profile": "test-profile"}, "observability": map[string]any{"scrapeKind": "none"}, "components": map[string]any{"api": map[string]any{"image": image}, "worker": map[string]any{"image": image}}}
	if err := applyBusinessValues(values, b, map[string]string{"holmesgpt": image, "platform-web": image}); err != nil {
		t.Fatal(err)
	}
	for _, stage := range []string{"dependencies", "bootstrap-api", "business"} {
		t.Run(stage, func(t *testing.T) {
			values["networkPolicyOnly"] = stage == "dependencies"
			values["bootstrapAPIOnly"] = stage == "bootstrap-api"
			raw, _ := yaml.Marshal(values)
			file := filepath.Join(t.TempDir(), "values.yaml")
			if err := os.WriteFile(file, raw, 0600); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command(helm, "template", "ops-platform", "../../deploy/charts/ops-platform", "--namespace", b.Namespace(), "-f", file).CombinedOutput()
			if err != nil {
				t.Fatalf("render: %v %s", err, out)
			}
			decoder := yaml.NewDecoder(bytes.NewReader(out))
			hasWeb := false
			hasAssistant := false
			for {
				var obj map[string]any
				err := decoder.Decode(&obj)
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				name := textValue(object(obj, "metadata"), "name")
				if name == "ops-web" && obj["kind"] == "Deployment" {
					hasWeb = true
					if object(object(object(object(obj, "spec"), "template"), "spec"), "securityContext")["runAsNonRoot"] != true {
						t.Fatal("Web lost nonroot")
					}
				}
				if name == "ops-sp06-runtime" {
					var config map[string]any
					if err := json.Unmarshal([]byte(textValue(object(obj, "data"), "api.json")), &config); err != nil {
						t.Fatal(err)
					}
					hasAssistant = object(config, "assistant")["enabled"] == true && object(config, "assistant")["faultChainEnabled"] == true
				}
				if stage != "business" && name == "ops-web-runtime" {
					t.Fatal("Web rendered before business activation")
				}
			}
			if hasWeb != (stage == "business") || hasAssistant != (stage != "dependencies") {
				t.Fatal("phase boundary lost", hasWeb, hasAssistant)
			}
		})
	}
}

func TestModelProbeBindsAssistantModelAvailability(t *testing.T) {
	d := capabilityDocument(t)
	a := object(object(d, "sp06"), "assistant")
	object(a, "profile")["modelID"] = "assistant-only-model"
	raw, _ := json.Marshal(a["profile"])
	var material modelprofile.Profile
	json.Unmarshal(raw, &material)
	a["modelProfileVersion"] = material.Reference()
	b := readBusinessTest(t, d)
	expected := textValue(object(object(object(b.values, "sp06"), "assistant"), "profile"), "modelID")
	for _, reported := range []string{"", "wrong-model", expected} {
		t.Run("reported-"+reported, func(t *testing.T) {
			created := false
			deleted := false
			run := func(_ context.Context, _ string, args ...string) ([]byte, error) {
				joined := strings.Join(args, " ")
				if strings.Contains(joined, " run ops-model-check-") {
					created = true
					var input map[string]any
					found := false
					for _, arg := range args {
						if strings.HasPrefix(arg, "--env=OPS_MODEL_CHECK=") {
							if err := json.Unmarshal([]byte(strings.TrimPrefix(arg, "--env=OPS_MODEL_CHECK=")), &input); err != nil {
								t.Fatal(err)
							}
							found = input["assistantModel"] == expected
						}
					}
					if !found || !strings.Contains(joined, "c['assistantModel'] not in models") {
						t.Fatal("assistant availability not checked")
					}
					return []byte(`{"metadata":{"uid":"probe-uid"}}`), nil
				}
				if strings.Contains(joined, " logs pod/") {
					return json.Marshal(map[string]any{"status": "MODEL_ENDPOINT_SCOPE_AND_AVAILABILITY_VERIFIED", "addresses": []string{"192.168.139.1"}, "model": textValue(object(object(b.values, "sp06"), "model"), "model"), "assistantModel": reported})
				}
				if strings.Contains(joined, " get pod ") {
					return []byte(`{"metadata":{"uid":"probe-uid"}}`), nil
				}
				if strings.Contains(joined, " delete pod ") {
					deleted = true
					return nil, nil
				}
				if strings.Contains(joined, " wait ") {
					return nil, nil
				}
				t.Fatal("unexpected operation", joined)
				return nil, nil
			}
			err := verifyCurrentModel(t.Context(), importProfile(), b, []ImageArtifact{{Name: "holmesgpt", Reference: "signed-holmes"}}, run)
			if (err == nil) != (reported == expected) || !created || !deleted {
				t.Fatalf("model verification mismatch: %v created=%v deleted=%v", err, created, deleted)
			}
		})
	}
}
