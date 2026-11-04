package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"ops-platform/internal/action"
	"ops-platform/internal/configregistry"
	"ops-platform/internal/httpapi"
)

func TestSP07ConfigurationOnlyNeedsNoExecutionCredentialsOrWorker(t *testing.T) {
	p := action.Profile{SchemaVersion: "execution-profile/v2", ID: uuid.New(), Version: 1, Name: "configuration-test", Type: "k8s_namespace", AllowedTargets: []string{"k8s+v1://tenant/cluster/core/Pod/uid"}, ClusterUID: "cluster", Namespace: "apps", ToolImageDigest: "ops.local/runner@sha256:" + strings.Repeat("a", 64), Tools: []string{"kubectl"}, NetworkPolicyRef: "runner", CredentialRef: "serviceaccount:/apps/runner", TimeoutSeconds: 60, MaxOutputBytes: 1024}
	c := SP07Config{SchemaVersion: "sp07-runtime/v1", ConfigurationOnly: true, Namespace: "ops", Tenants: []uuid.UUID{uuid.New()}, AdmittedProfiles: []action.Profile{p}}
	path := filepath.Join(t.TempDir(), "runtime.json")
	raw, _ := json.Marshal(c)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SP07_RUNTIME_FILE", path)
	loaded, err := loadSP07()
	if err != nil {
		t.Fatal(err)
	}
	h := &httpapi.SP04Handlers{}
	stop, err := StartSP07API(context.Background(), nil, h, configregistry.Ed25519TrustStore{})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if h.Action == nil || !h.Action.ConfigurationOnly {
		t.Fatal("configuration handler unavailable")
	}
	workerStop, err := StartSP07Worker(context.Background(), nil, nil, nil)
	if err != nil {
		t.Fatal("configuration started an execution worker", err)
	}
	workerStop()
	if err := loaded.admitProfile(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	p.TimeoutSeconds++
	if loaded.admitProfile(context.Background(), p) == nil {
		t.Fatal("unadmitted profile accepted")
	}
	c.ConfigurationOnly = false
	raw, _ = json.Marshal(c)
	os.WriteFile(path, raw, 0600)
	if _, err := loadSP07(); err == nil {
		t.Fatal("full execution runtime accepted missing execution admission")
	}
}
