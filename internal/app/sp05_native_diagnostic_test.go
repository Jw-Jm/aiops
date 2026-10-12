//go:build sp05_diagnostic

package app

import (
	"context"
	"errors"
	"flag"
	"ops-platform/internal/evidence"
	"ops-platform/internal/integrations/k8sgpt"
	"os"
	"testing"
	"time"
)

// Explicit read-only diagnostic inside the current Worker container. It uses
// the installed source binding and credentials and never submits candidates.
var sp05NativeReadOnly = flag.Bool("sp05-native-readonly", false, "explicit read-only diagnostic in installed Worker")

func TestNativeSP05ReadBrokerDiagnostic(t *testing.T) {
	if !*sp05NativeReadOnly {
		t.Skip("explicit native diagnostic required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	c, err := loadSP04()
	if err != nil || c == nil || len(c.Clusters) != 1 || c.SP05 == nil {
		t.Fatal("current configuration unavailable")
	}
	pool, err := OpenRuntimePool(ctx, os.Getenv("DATABASE_URL"), "worker_runtime_role")
	if err != nil {
		t.Fatalf("pool unavailable (%T)", err)
	}
	defer pool.Close()
	cluster := c.Clusters[0]
	client, err := clusterClient(cluster)
	if err != nil {
		t.Fatalf("client unavailable (%T)", err)
	}
	repo := evidence.Repository{Pool: pool}
	binding, err := repo.RegisteredBinding(ctx, evidence.Binding{Tenant: cluster.Tenant, SourceID: cluster.SourceID, Revision: cluster.SourceRevision, SourceType: "kubernetes", BackendLogicalID: cluster.BackendLogicalID})
	if err != nil {
		t.Fatalf("binding unavailable (%T)", err)
	}
	if len(binding.ScopeMapping.Scopes["namespace"]) != 1 {
		t.Fatal("one current namespace required")
	}
	ns := binding.ScopeMapping.Scopes["namespace"][0]
	broker := k8sgpt.ReadBroker{Client: client, Namespace: ns, RequiredLabels: binding.ScopeMapping.RequiredLabels, Authorize: func(ctx context.Context) error { return repo.CheckBinding(ctx, binding) }}
	path, closeBroker, err := broker.Start(ctx)
	if err != nil {
		t.Fatalf("broker unavailable (%T)", err)
	}
	defer closeBroker()
	out, err := (k8sgpt.Adapter{Binary: "/opt/ops/bin/k8sgpt", Kubeconfig: path, SHA256: c.SP05.Analyzer.SHA256, Namespace: ns}).Run(ctx)
	code := "none"
	if errors.Is(err, k8sgpt.ErrDisabled) {
		code = "K8SGPT_UNVERIFIED"
	} else if errors.Is(err, k8sgpt.ErrDrift) {
		code = "K8SGPT_FORMAT_DRIFT"
	} else if err != nil {
		code = "OTHER"
	}
	t.Logf("status=%s problems=%d errorCode=%s errorType=%T complete=%t brokerFailures=%v", out.Status, out.Problems, code, err, broker.Complete(), broker.Failures())
}
