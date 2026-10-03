package integration

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"github.com/google/uuid"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSP05LockedAnalyzerRealOrbStackSandboxAndNativeFaults(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	collector, ns := sp04OwnedKubernetes(t, ctx, uuid.NewString(), uuid.NewString())
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	pointer, err := os.ReadFile("/tmp/ops-sp05-k8sgpt-current-dir")
	if err != nil {
		t.Fatal("prepare locked offline K8sGPT binary before this required live gate")
	}
	binary := filepath.Join(strings.TrimSpace(string(pointer)), "output/k8sgpt")
	raw, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal("locked Analyzer binary missing")
	}
	hash := sha256.Sum256(raw)
	digest := hex.EncodeToString(hash[:])
	dir := t.TempDir()
	command := exec.CommandContext(ctx, "go", "build", "-mod=readonly", "-trimpath", "-buildvcs=false", "-o", filepath.Join(dir, "analyzer-check"), "./test/tools/sp05-analyzer-check")
	command.Dir = root
	command.Env = append(os.Environ(), "GOOS=linux", "GOARCH=arm64", "CGO_ENABLED=0")
	if _, err := command.CombinedOutput(); err != nil {
		t.Fatal("compile actual adapter verification entry point")
	}
	if err := copyOwnedFixtureFile(binary, filepath.Join(dir, "k8sgpt")); err != nil {
		t.Fatal(err)
	}
	dockerfile := "FROM scratch\nCOPY --chmod=0555 analyzer-check /analyzer-check\nCOPY --chmod=0555 k8sgpt /opt/ops/bin/k8sgpt\nUSER 65532:65532\nENTRYPOINT [\"/analyzer-check\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0600); err != nil {
		t.Fatal(err)
	}
	image := "ops-sp05-analyzer-check:" + uuid.NewString()
	if _, err := exec.CommandContext(ctx, "docker", "build", "--network=none", "--pull=false", "-t", image, dir).CombinedOutput(); err != nil {
		t.Fatal("build owned Analyzer verification image without pulls")
	}
	imageID, err := exec.CommandContext(ctx, "docker", "image", "inspect", image, "--format", "{{.Id}}").Output()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		current, err := exec.Command("docker", "image", "inspect", image, "--format", "{{.Id}}").Output()
		if err == nil && string(current) == string(imageID) {
			_ = exec.Command("docker", "image", "rm", image).Run()
		}
	})
	var service struct {
		Spec struct {
			ClusterIP string `json:"clusterIP"`
		}
	}
	if json.Unmarshal(sp04Kubectl(t, ctx, nil, "get", "service", "kubernetes", "-n", "default", "-o", "json"), &service) != nil {
		t.Fatal("native control-plane Service identity")
	}
	ca, err := os.ReadFile(collector.CAFile)
	if err != nil {
		t.Fatal(err)
	}
	config := map[string]any{"apiVersion": "v1", "kind": "Config", "current-context": "native", "clusters": []any{map[string]any{"name": "native", "cluster": map[string]any{"server": "https://" + service.Spec.ClusterIP + ":443", "certificate-authority-data": base64.StdEncoding.EncodeToString(ca)}}}, "users": []any{map[string]any{"name": "native", "user": map[string]any{"tokenFile": "/var/run/secrets/kubernetes.io/serviceaccount/token"}}}, "contexts": []any{map[string]any{"name": "native", "context": map[string]any{"cluster": "native", "user": "native"}}}}
	configJSON, _ := json.Marshal(config)
	labels := map[string]string{"ops.platform.test": ns}
	objects := []map[string]any{
		{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "analyzer-config", "namespace": ns, "labels": labels}, "data": map[string]string{"kubeconfig.json": string(configJSON)}},
		{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": "unschedulable", "namespace": ns, "labels": labels}, "spec": map[string]any{"nodeSelector": map[string]string{"ops.platform.test.nonexistent": "sp05"}, "containers": []any{map[string]any{"name": "pause", "image": "registry.k8s.io/pause:3.10"}}}},
		{"apiVersion": "v1", "kind": "PersistentVolumeClaim", "metadata": map[string]any{"name": "unbound", "namespace": ns, "labels": labels}, "spec": map[string]any{"storageClassName": "sp05-missing-class", "accessModes": []string{"ReadWriteOnce"}, "resources": map[string]any{"requests": map[string]string{"storage": "1Mi"}}}},
	}
	objects = append(objects, map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": map[string]any{"name": "owned-parent", "namespace": ns, "labels": labels}, "spec": map[string]any{"replicas": 1, "selector": map[string]any{"matchLabels": map[string]string{"app": "owned-parent"}}, "template": map[string]any{"metadata": map[string]any{"labels": map[string]string{"app": "owned-parent"}}, "spec": map[string]any{"nodeSelector": map[string]string{"ops.platform.test.nonexistent": "sp05"}, "containers": []any{map[string]any{"name": "pause", "image": "registry.k8s.io/pause:3.10"}}}}}})
	manifest, _ := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "List", "items": objects})
	sp04Kubectl(t, ctx, manifest, "create", "-f", "-")
	// Source faults are real native state; no Pod/Node is made unhealthy outside
	// this owned namespace and the unschedulable image is never pulled or run.
	for deadline := time.Now().Add(45 * time.Second); ; {
		var parents struct {
			Items []map[string]any `json:"items"`
		}
		json.Unmarshal(sp04Kubectl(t, ctx, nil, "get", "pods", "-n", ns, "-l", "app=owned-parent", "-o", "json"), &parents)
		ready := false
		for _, pod := range parents.Items {
			status, _ := pod["status"].(map[string]any)
			conditions, _ := status["conditions"].([]any)
			for _, raw := range conditions {
				c, _ := raw.(map[string]any)
				if c["reason"] == "Unschedulable" {
					ready = true
				}
			}
		}
		if ready {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("owned native Deployment Pod did not expose scheduling fault")
		}
		time.Sleep(200 * time.Millisecond)
	}
	pod := map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": "analyzer-check", "namespace": ns, "labels": labels}, "spec": map[string]any{"serviceAccountName": "collector", "restartPolicy": "Never", "securityContext": map[string]any{"runAsNonRoot": true, "runAsUser": 65532, "runAsGroup": 65532, "fsGroup": 65532}, "containers": []any{map[string]any{"name": "analyzer", "image": image, "imagePullPolicy": "Never", "env": []any{map[string]string{"name": "OPS_TEST_NAMESPACE", "value": ns}, map[string]string{"name": "OPS_TEST_ANALYZER_SHA256", "value": digest}}, "resources": map[string]any{"requests": map[string]string{"cpu": "100m", "memory": "128Mi"}, "limits": map[string]string{"cpu": "1", "memory": "512Mi"}}, "securityContext": map[string]any{"readOnlyRootFilesystem": true, "allowPrivilegeEscalation": false, "capabilities": map[string]any{"drop": []string{"ALL"}}, "seccompProfile": map[string]string{"type": "RuntimeDefault"}}, "volumeMounts": []any{map[string]any{"name": "config", "mountPath": "/fixture", "readOnly": true}, map[string]any{"name": "scratch", "mountPath": "/tmp"}}}}, "volumes": []any{map[string]any{"name": "config", "configMap": map[string]any{"name": "analyzer-config"}}, map[string]any{"name": "scratch", "emptyDir": map[string]any{"medium": "Memory", "sizeLimit": "256Mi"}}}}}
	manifest, _ = json.Marshal(pod)
	sp04Kubectl(t, ctx, manifest, "create", "-f", "-")
	var state struct {
		Status struct {
			Phase string `json:"phase"`
		}
	}
	for ctx.Err() == nil {
		data := sp04Kubectl(t, ctx, nil, "get", "pod", "analyzer-check", "-n", ns, "-o", "json")
		if json.Unmarshal(data, &state) != nil {
			t.Fatal("native Pod status")
		}
		if state.Status.Phase == "Succeeded" || state.Status.Phase == "Failed" {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	logs := sp04Kubectl(t, ctx, nil, "logs", "analyzer-check", "-n", ns)
	if state.Status.Phase != "Succeeded" {
		t.Fatalf("kernel sandbox Analyzer failed: phase=%s result=%s", state.Status.Phase, logs)
	}
	var result struct {
		Provider string `json:"provider"`
		Status   string `json:"status"`
		Sandbox  string `json:"sandbox"`
		Results  []struct {
			Kind   string `json:"kind"`
			Name   string `json:"name"`
			Parent string `json:"parentObject"`
		} `json:"results"`
	}
	if json.Unmarshal(logs, &result) != nil || result.Provider != "" || result.Status != "ProblemDetected" || result.Sandbox != "kernel-cgroup-readonly-root-verified" {
		t.Fatalf("native no-LLM Analyzer output invalid: %s", logs)
	}
	foundPod, foundPVC, foundParent := false, false, false
	for _, r := range result.Results {
		if r.Kind == "Pod" && r.Parent == "Deployment/owned-parent" {
			foundParent = true
		}
		if r.Kind == "Pod" && r.Name == ns+"/unschedulable" {
			foundPod = true
		}
		if r.Kind == "PersistentVolumeClaim" && r.Name == ns+"/unbound" {
			foundPVC = true
		}
	}
	if !foundPod || !foundPVC || !foundParent {
		t.Fatalf("native fault missed: pod=%t pvc=%t parent=%t result=%s", foundPod, foundPVC, foundParent, logs)
	}
	t.Logf("locked source f071b32aa85d77b197cd2d0f9868294f7b55c5eb binarySHA256=%s image=%s; real OrbStack Pod/PVC and Node Analyzer, fixed argv, no explain/provider, actual finite CPU/memory and read-only root", digest, strings.TrimSpace(string(imageID)))
}

func copyOwnedFixtureFile(source, target string) error {
	body, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return os.WriteFile(target, body, 0555)
}
