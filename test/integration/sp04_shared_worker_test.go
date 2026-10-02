package integration

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Reuse the SP03 process-kill/pending/recovery assertions with this run's own
// native namespace, projected Pod token and native TokenReview-enabled Bao.
func TestSP04SharedAuditWorkerProjectedRecovery(t *testing.T) {
	if os.Getenv("SP04_TEST_ORBSTACK") != "1" {
		t.Skip("owned OrbStack dependencies required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	collector, ns := sp04OwnedKubernetes(t, ctx, uuid.NewString(), uuid.NewString())
	bao, ca, endpoint := sp04NativeBao(t, ctx, ns, collector)
	raw, _ := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "List", "items": []any{
		map[string]any{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": map[string]any{"name": "ops-worker", "namespace": ns}, "automountServiceAccountToken": false},
		map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": "audit-worker-token", "namespace": ns}, "spec": map[string]any{"serviceAccountName": "ops-worker", "automountServiceAccountToken": false, "containers": []any{map[string]any{"name": "token-holder", "image": archiveProbeImage, "imagePullPolicy": "Never", "command": []string{"sleep", "300"}, "volumeMounts": []any{map[string]any{"name": "token", "mountPath": "/var/run/secrets/openbao", "readOnly": true}}}}, "volumes": []any{map[string]any{"name": "token", "projected": map[string]any{"sources": []any{map[string]any{"serviceAccountToken": map[string]any{"audience": "openbao", "expirationSeconds": 600, "path": "token"}}}}}}}},
	}})
	sp04Kubectl(t, ctx, raw, "--context", "orbstack", "create", "-f", "-")
	sp04Kubectl(t, ctx, nil, "--context", "orbstack", "-n", ns, "wait", "--for=condition=Ready", "pod/audit-worker-token", "--timeout=90s")
	dir := t.TempDir()
	token := sp04Kubectl(t, ctx, nil, "--context", "orbstack", "-n", ns, "exec", "audit-worker-token", "--", "cat", "/var/run/secrets/openbao/token")
	if os.WriteFile(filepath.Join(dir, "ops-worker"), token, 0600) != nil || os.WriteFile(filepath.Join(dir, "ca.pem"), ca, 0600) != nil {
		t.Fatal("private projected workload fixture write failed")
	}
	u, _ := url.Parse(endpoint)
	u.Host = "127.0.0.1:" + u.Port()
	t.Setenv("SP03_TEST_WORKLOAD_OPENBAO_URL", u.String())
	t.Setenv("SP03_TEST_WORKLOAD_OPENBAO_CA_FILE", filepath.Join(dir, "ca.pem"))
	t.Setenv("SP03_TEST_KUBERNETES_TOKEN_DIR", dir)
	runAuditWorkerProjectedRecovery(t, bao, ns)
	t.Log("shared SP03 Audit actual Worker command: native Pod-bound projected OpenBao login, archive outage pending, SIGKILL and resumed signed chain passed in owned namespace")
}
