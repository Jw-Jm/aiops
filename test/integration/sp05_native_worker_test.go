package integration

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"github.com/google/uuid"
	"ops-platform/internal/app"
	"ops-platform/internal/integrations/k8sgpt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSP05ActualLinuxWorkerAuthorizedAnalyzerDurableChain(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	tenant, source := uuid.New(), uuid.New()
	collector, ns := sp04OwnedKubernetes(t, ctx, tenant.String(), source.String())
	pass, db, pool, _ := sp05Database(t, sp05Seed{Tenant: tenant, Source: source, ClusterUID: collector.ClusterUID, Namespace: ns, Backend: collector.BackendLogicalID})
	_, trust := sp05GoldenRegistry(t, pass, db, pool, tenant)
	archives := sp05GoldenArchive(t, pass, pool, tenant)
	_ = archives // Same real service setup qualifies Transit before native startup.
	bucket := "sp05-native-worker-" + uuid.NewString()[:8]
	fixture := newRoleTenantS3Fixture(t, []uuid.UUID{tenant}, bucket)
	credentials, err := os.ReadFile(fixture.CredentialFile)
	if err != nil {
		t.Fatal("native archive roles unavailable")
	}
	worker, _ := sp04RuntimeTLS(t, ns)
	files := map[string][]byte{}
	addFile := func(name, path string) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("native private input missing: %s", name)
		}
		files[name] = raw
	}
	addFile("worker-ca.pem", worker.CAFile)
	addFile("worker-crl.pem", worker.CRLFile)
	addFile("worker.pem", worker.CertificateFile)
	addFile("worker.key", worker.PrivateKeyFile)
	addFile("context.pub", worker.ContextPublicKeyFile)
	addFile("context.key", worker.ContextPrivateKeyFile)
	addFile("kubernetes-ca.pem", collector.CAFile)
	addFile("bao-ca.pem", os.Getenv("SP03_TEST_OPENBAO_CA_FILE"))
	files["s3-ca.pem"] = fixture.CA
	files["s3-roles.json"] = credentials
	worker.CAFile = "/fixture/worker-ca.pem"
	worker.CRLFile = "/fixture/worker-crl.pem"
	worker.CertificateFile = "/fixture/worker.pem"
	worker.PrivateKeyFile = "/fixture/worker.key"
	worker.ContextPublicKeyFile = "/fixture/context.pub"
	worker.ContextPrivateKeyFile = "/fixture/context.key"
	collector.CAFile = "/fixture/kubernetes-ca.pem"
	collector.TokenFile = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	var service struct {
		Spec struct {
			ClusterIP string `json:"clusterIP"`
		} `json:"spec"`
	}
	if json.Unmarshal(sp04Kubectl(t, ctx, nil, "get", "service", "kubernetes", "-n", "default", "-o", "json"), &service) != nil || service.Spec.ClusterIP == "" {
		t.Fatal("native Service unavailable")
	}
	collector.Endpoint = "https://" + service.Spec.ClusterIP + ":443"
	worker.Clusters = []app.SP04Cluster{collector}
	worker.ArchiveBackendLogicalID = "archive-native-worker"
	worker.SP05 = &app.SP05Config{Enabled: true, Analyzer: app.SP05AnalyzerConfig{Enabled: true, SHA256: k8sgpt.LockedBinarySHA256}}
	files["runtime.json"], _ = json.Marshal(worker)
	keys := map[string]string{}
	for name, pub := range trust.Keys {
		keys[name] = base64.StdEncoding.EncodeToString(ed25519.PublicKey(pub))
	}
	files["registry-trust.json"], _ = json.Marshal(keys)
	pod := map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": "native-analyzer-fault", "namespace": ns}, "spec": map[string]any{"containers": []any{map[string]any{"name": "never-pulled", "image": "fixture.invalid/sp05-native-fault:missing", "imagePullPolicy": "Never"}}, "restartPolicy": "Never"}}
	raw, _ := json.Marshal(pod)
	var created struct {
		Metadata struct {
			UID string `json:"uid"`
		} `json:"metadata"`
	}
	if json.Unmarshal(sp04Kubectl(t, ctx, raw, "create", "-f", "-", "-o", "json"), &created) != nil || created.Metadata.UID == "" {
		t.Fatal("native fault Pod unavailable")
	}
	verification := map[string]string{"DatabaseURL": pool.Config().ConnConfig.ConnString(), "Tenant": tenant.String(), "PodUID": created.Metadata.UID, "BaoAddress": os.Getenv("SP03_TEST_OPENBAO_URL"), "BaoToken": os.Getenv("SP03_TEST_OPENBAO_TOKEN"), "BaoServerName": "localhost", "S3Endpoint": fixture.Endpoint, "S3Bucket": bucket}
	files["verification.json"], _ = json.Marshal(verification)
	secretData := map[string]string{}
	for name, data := range files {
		secretData[name] = base64.StdEncoding.EncodeToString(data)
	}
	secret := map[string]any{"apiVersion": "v1", "kind": "Secret", "metadata": map[string]any{"name": "native-worker-inputs", "namespace": ns, "labels": map[string]string{"ops.platform.test": ns}}, "type": "Opaque", "data": secretData}
	raw, _ = json.Marshal(secret)
	sp04Kubectl(t, ctx, raw, "create", "-f", "-")
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	task := t.TempDir()
	command := exec.CommandContext(ctx, "go", "build", "-mod=readonly", "-trimpath", "-buildvcs=false", "-o", filepath.Join(task, "worker-check"), "./test/tools/sp05-worker-check")
	command.Dir = root
	command.Env = append(os.Environ(), "GOOS=linux", "GOARCH=arm64", "CGO_ENABLED=0")
	if _, err := command.CombinedOutput(); err != nil {
		t.Fatal("compile production Worker entry verifier")
	}
	pointer, err := os.ReadFile("/tmp/ops-sp05-k8sgpt-current-dir")
	if err != nil {
		t.Fatal("qualified CLI source required")
	}
	if err := copyOwnedFixtureFile(filepath.Join(strings.TrimSpace(string(pointer)), "output/k8sgpt"), filepath.Join(task, "k8sgpt")); err != nil {
		t.Fatal(err)
	}
	dockerfile := "FROM scratch\nCOPY --chmod=0555 worker-check /worker-check\nCOPY --chmod=0555 k8sgpt /opt/ops/bin/k8sgpt\nUSER 65532:65532\nENTRYPOINT [\"/worker-check\"]\n"
	if err := os.WriteFile(filepath.Join(task, "Dockerfile"), []byte(dockerfile), 0600); err != nil {
		t.Fatal(err)
	}
	image := "ops-sp05-native-worker-check:" + uuid.NewString()
	if _, err := exec.CommandContext(ctx, "docker", "build", "--network=none", "--pull=false", "-t", image, task).CombinedOutput(); err != nil {
		t.Fatal("owned no-pull native verification image build")
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
	check := map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": "native-worker-check", "namespace": ns, "labels": map[string]string{"ops.platform.test": ns}}, "spec": map[string]any{"hostNetwork": true, "serviceAccountName": "collector", "restartPolicy": "Never", "securityContext": map[string]any{"runAsNonRoot": true, "runAsUser": 65532, "runAsGroup": 65532, "fsGroup": 65532}, "containers": []any{map[string]any{"name": "worker", "image": image, "imagePullPolicy": "Never", "resources": map[string]any{"requests": map[string]string{"cpu": "100m", "memory": "128Mi"}, "limits": map[string]string{"cpu": "1", "memory": "512Mi"}}, "securityContext": map[string]any{"readOnlyRootFilesystem": true, "allowPrivilegeEscalation": false, "capabilities": map[string]any{"drop": []string{"ALL"}}, "seccompProfile": map[string]string{"type": "RuntimeDefault"}}, "volumeMounts": []any{map[string]any{"name": "inputs", "mountPath": "/fixture", "readOnly": true}, map[string]any{"name": "scratch", "mountPath": "/tmp"}}}}, "volumes": []any{map[string]any{"name": "inputs", "secret": map[string]any{"secretName": "native-worker-inputs", "defaultMode": 288}}, map[string]any{"name": "scratch", "emptyDir": map[string]any{"medium": "Memory", "sizeLimit": "256Mi"}}}}}
	raw, _ = json.Marshal(check)
	sp04Kubectl(t, ctx, raw, "create", "-f", "-")
	var state struct {
		Status struct {
			Phase string `json:"phase"`
		} `json:"status"`
	}
	for ctx.Err() == nil {
		json.Unmarshal(sp04Kubectl(t, ctx, nil, "get", "pod", "native-worker-check", "-n", ns, "-o", "json"), &state)
		if state.Status.Phase == "Succeeded" || state.Status.Phase == "Failed" {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	logs := sp04Kubectl(t, ctx, nil, "logs", "native-worker-check", "-n", ns)
	if state.Status.Phase != "Succeeded" || !strings.Contains(string(logs), `"archivedEvidence":1`) {
		t.Fatalf("actual Linux Worker/Analyzer chain failed: phase=%s sanitized=%s", state.Status.Phase, logs)
	}
	t.Logf("actual authorized Worker startup + original fixed CLI + native OrbStack fault Pod + common ingestion + real PG Incident + real OpenBao/TLS/IAM S3 Evidence; image=%s; private inputs remain in owned Secret, no performance samples", strings.TrimSpace(string(imageID)))
}
