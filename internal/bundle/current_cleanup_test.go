package bundle

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"ops-platform/internal/profile"
)

func currentCleanupFixture(t *testing.T) (Manifest, profile.ResolvedProfile, BusinessValues, map[string]map[string]any, CommandRunner, *[]string) {
	t.Helper()
	doc := validBusinessDocument()
	doc["namespace"] = "ops-isolated-cleanup"
	object(doc, "sp04")["clusters"].([]any)[0].(map[string]any)["LeaseNamespace"] = "ops-isolated-cleanup"
	b := readBusinessTest(t, doc)
	p := importProfile()
	m := Manifest{BundleID: "installed", Payload: Payload{Digest: "sha256:installed"}}
	installation := "018f0f2b-91c2-7d42-a8dc-f719c5987204"
	objects := map[string]map[string]any{}
	meta := func(name, uid string) map[string]any {
		return map[string]any{"name": name, "uid": uid, "resourceVersion": "10"}
	}
	ns := map[string]any{"metadata": meta(b.Namespace(), "namespace-uid")}
	object(ns, "metadata")["labels"] = map[string]any{"ops.platform.io/managed-by": "opsctl-bootstrap", "ops.platform.io/installation-id": installation}
	objects["namespace"] = ns
	for name, stage := range map[string]string{"ops-installation-checkpoint": "dependencies-ready", "ops-api-bootstrap-checkpoint": "bootstrap-api-ready"} {
		r := currentStageReceipt{Stage: stage, BundleID: m.BundleID, PayloadDigest: m.Payload.Digest, ProfileDigest: jsonDigest(p), RegisteredBusinessDigest: registeredBusinessInstallationDigest(b), NamespaceUID: "namespace-uid", InstallationID: installation}
		raw, _ := json.Marshal(r)
		cm := map[string]any{"metadata": meta(name, name+"-uid"), "data": map[string]any{"receipt.json": string(raw)}}
		object(cm, "metadata")["labels"] = map[string]any{"ops.platform.io/installation-id": installation}
		objects[name] = cm
	}
	for _, release := range []string{"ops-platform", "ops-dependencies"} {
		live := map[string]any{"kind": "Service", "metadata": meta(release, release+"-uid")}
		md := object(live, "metadata")
		md["labels"] = map[string]any{"ops.platform.io/release": release, "app.kubernetes.io/managed-by": "Helm"}
		md["annotations"] = map[string]any{"meta.helm.sh/release-name": release, "meta.helm.sh/release-namespace": b.Namespace()}
		objects[release] = live
	}
	objects["pvc"] = map[string]any{"metadata": meta("archive", "claim-uid"), "spec": map[string]any{"volumeName": "retained-volume"}}
	objects["pv"] = map[string]any{"metadata": meta("retained-volume", "volume-uid"), "spec": map[string]any{"persistentVolumeReclaimPolicy": "Retain", "claimRef": map[string]any{"uid": "claim-uid", "name": "archive", "namespace": b.Namespace()}}}
	mutations := []string{}
	run := func(_ context.Context, program string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if program == "helm" && args[0] == "uninstall" {
			mutations = append(mutations, joined)
			if !strings.Contains(joined, "--namespace "+b.Namespace()) {
				t.Fatal("cleanup used another namespace")
			}
			return nil, nil
		}
		if program == "helm" && args[0] == "get" {
			release := args[2]
			return []byte("kind: Service\nmetadata:\n  name: " + release + "\n  labels: {ops.platform.io/release: " + release + "}\n"), nil
		}
		if strings.Contains(joined, "delete --raw=") {
			body, err := os.ReadFile(args[len(args)-1])
			if err != nil {
				return nil, err
			}
			var opts map[string]any
			if json.Unmarshal(body, &opts) != nil {
				return nil, errors.New("invalid delete options")
			}
			pre := object(opts, "preconditions")
			if textValue(pre, "uid") == "" || textValue(pre, "resourceVersion") != "10" {
				t.Fatal("checkpoint deletion lacks preconditions")
			}
			mutations = append(mutations, joined)
			return nil, nil
		}
		var obj any
		switch {
		case strings.Contains(joined, "get namespace"):
			obj = objects["namespace"]
		case strings.Contains(joined, "get configmap"):
			for _, name := range []string{"ops-installation-checkpoint", "ops-api-bootstrap-checkpoint", "ops-command-initialization-checkpoint"} {
				if strings.Contains(joined, "get configmap "+name) {
					if objects[name] == nil {
						return nil, nil
					}
					obj = objects[name]
					break
				}
			}
			if obj == nil {
				return nil, nil
			}
		case strings.Contains(joined, "get pvc -o json"):
			obj = map[string]any{"items": []any{objects["pvc"]}}
		case strings.Contains(joined, "get pv ") || strings.Contains(joined, "get PersistentVolume "):
			obj = objects["pv"]
		case strings.Contains(joined, "get PersistentVolumeClaim "):
			obj = objects["pvc"]
		default:
			for _, name := range []string{"ops-platform", "ops-dependencies"} {
				if strings.Contains(joined, "get Service "+name+" ") {
					obj = objects[name]
				}
			}
		}
		if obj == nil {
			return nil, errors.New("unexpected read: " + joined)
		}
		return json.Marshal(obj)
	}
	return m, p, b, objects, run, &mutations
}

func TestCurrentCleanupPreservesStorageAndUsesCheckpointPreconditions(t *testing.T) {
	m, p, b, _, run, mutations := currentCleanupFixture(t)
	plan, err := planCurrentCleanup(t.Context(), m, p, b, run)
	if err != nil {
		t.Fatal(err)
	}
	if len(*mutations) != 0 || len(plan.ProtectedClaims) != 1 || len(plan.ProtectedVolumes) != 1 || plan.Namespace != b.Namespace() {
		t.Fatalf("unsafe plan: %+v", plan)
	}
	result, err := applyCurrentCleanup(t.Context(), m, p, b, run, plan)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Applied || len(*mutations) != 4 {
		t.Fatalf("cleanup mutations: %v", *mutations)
	}
	for _, cmd := range *mutations {
		if strings.Contains(cmd, "delete pvc") || strings.Contains(cmd, "delete pv ") || strings.Contains(cmd, "delete namespace") || strings.Contains(cmd, "delete secret") {
			t.Fatalf("protected state deleted: %s", cmd)
		}
	}
}

func TestCurrentCleanupRejectsChangedCheckpointNamespaceOwnerAndStorageBeforeMutation(t *testing.T) {
	for _, change := range []string{"namespace", "checkpoint", "owner", "retention", "binding", "plan-race"} {
		t.Run(change, func(t *testing.T) {
			m, p, b, objects, run, mutations := currentCleanupFixture(t)
			plan, err := planCurrentCleanup(t.Context(), m, p, b, run)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "namespace":
				object(objects["namespace"], "metadata")["uid"] = "foreign"
			case "checkpoint":
				object(objects["ops-api-bootstrap-checkpoint"], "data")["receipt.json"] = "{}"
			case "owner":
				object(object(objects["ops-platform"], "metadata"), "annotations")["meta.helm.sh/release-namespace"] = "foreign"
			case "retention":
				object(objects["pv"], "spec")["persistentVolumeReclaimPolicy"] = "Delete"
			case "binding":
				object(object(objects["pv"], "spec"), "claimRef")["uid"] = "foreign"
			case "plan-race":
				object(objects["ops-platform"], "metadata")["uid"] = "replacement"
			}
			if _, err = applyCurrentCleanup(t.Context(), m, p, b, run, plan); err == nil || len(*mutations) != 0 {
				t.Fatalf("unsafe changed installation accepted: err=%v mutations=%v", err, *mutations)
			}
		})
	}
}

func TestCurrentCleanupRefusesForeignResourcesHooksStorageAndBootstrapSecrets(t *testing.T) {
	_, p, b, _, run, _ := currentCleanupFixture(t)
	for _, raw := range []string{
		"kind: PersistentVolumeClaim\nmetadata: {name: archive, labels: {ops.platform.io/release: ops-platform}}\n",
		"kind: Secret\nmetadata: {name: ops-platform-runtime, labels: {ops.platform.io/release: ops-platform}}\n",
		"kind: Service\nmetadata: {name: ops-platform, namespace: foreign, labels: {ops.platform.io/release: ops-platform}}\n",
		"kind: Service\nmetadata: {name: ops-platform, labels: {ops.platform.io/release: foreign}}\n",
		"kind: Service\nmetadata: {name: ops-platform, labels: {ops.platform.io/release: ops-platform}, annotations: {helm.sh/hook: pre-delete}}\n",
	} {
		if _, err := currentCleanupReleaseResources(t.Context(), []byte(raw), "ops-platform", p, b, run); err == nil {
			t.Fatalf("unsafe manifest accepted: %s", raw)
		}
	}
}

func TestCleanupAuthenticatesRetiredBundleWithoutPromotingItForRuntime(t *testing.T) {
	m, trust, _ := signedFixture(t, func(m *fixtureManifest) { m.Materials[0].Name = "retired-third-party" })
	if _, err := Verify(t.Context(), m, trust); err == nil {
		t.Fatal("retired material gained runtime admission")
	}
	v, err := prepareCleanupArtifact(t.Context(), m, trust)
	if err != nil {
		t.Fatal(err)
	}
	defer v.close()
	if v.report.PayloadFilesVerified != 3 || !v.report.SignatureVerified || !v.report.PayloadDigestVerified || v.catalog != nil {
		t.Fatalf("cleanup artifact did not authenticate exact payload: %+v", v.report)
	}
	for _, change := range []string{"signature", "payload"} {
		t.Run(change, func(t *testing.T) {
			copy := m
			if change == "signature" {
				copy.Signature = append([]byte{}, m.Signature...)
				copy.Signature[0] ^= 1
			} else {
				copy.PayloadPath = t.TempDir() + "/payload.tar.zst"
				if err := os.WriteFile(copy.PayloadPath, []byte("tampered"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			_, err := CleanupCurrent(t.Context(), copy, trust, importProfile(), BusinessValues{}, func(context.Context, string, ...string) ([]byte, error) { calls++; return nil, nil }, true)
			if err == nil || calls != 0 {
				t.Fatalf("unauthenticated cleanup reached Kubernetes: err=%v calls=%d", err, calls)
			}
		})
	}
}
