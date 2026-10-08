package bundle

import (
	"context"
	"strings"
	"testing"
)

func TestPolicyBootstrapRejectsWorkloadsAndEmptyTemplates(t *testing.T) {
	for _, rendered := range []string{"", "kind: Deployment\n", "kind: NetworkPolicy\n---\nkind: Pod\n"} {
		if err := validatePolicyBootstrap([]byte(rendered)); err == nil {
			t.Fatalf("unsafe policy bootstrap accepted: %q", rendered)
		}
	}
	if err := validatePolicyBootstrap([]byte("kind: NetworkPolicy\nmetadata: {name: deny}\n")); err != nil {
		t.Fatal(err)
	}
}

func TestInstallIncompleteBundleNeverRunsHelmOrRuntime(t *testing.T) {
	m, trust := signedOCIImportFixture(t, false)
	runtime := &recordingImporter{}
	calls := 0
	run := func(context.Context, string, ...string) ([]byte, error) { calls++; return nil, nil }
	_, err := Install(context.Background(), m, trust, importProfile(), runtime, run)
	if err == nil || !strings.Contains(err.Error(), "missing core") {
		t.Fatalf("incomplete install error = %v", err)
	}
	if calls != 0 || len(runtime.calls) != 0 {
		t.Fatalf("incomplete bundle caused commands=%d runtime=%v", calls, runtime.calls)
	}
}

func TestRenderRejectsOnlineImagesAndHooksBeforeKubernetes(t *testing.T) {
	for _, resource := range []string{
		"kind: Deployment\nmetadata: {name: bad, labels: {ops.platform.io/release: ops-dependencies}}\nspec: {template: {spec: {containers: [{image: example/api:latest, imagePullPolicy: Always}]}}}\n",
		"kind: ConfigMap\nmetadata: {name: bad, labels: {ops.platform.io/release: ops-dependencies}, annotations: {helm.sh/hook: pre-install}}\n",
	} {
		calls := 0
		run := func(context.Context, string, ...string) ([]byte, error) { calls++; return nil, nil }
		if err := validateRenderedChart(context.Background(), "ops-system", []byte(resource), map[string]bool{}, importProfile(), "ops-dependencies", run); err == nil {
			t.Fatal("unsafe rendered resource accepted")
		}
		if calls != 0 {
			t.Fatal("unsafe chart reached Kubernetes")
		}
	}
}

func TestOfflineRenderedImagesNeverContactRegistryAfterCacheLoss(t *testing.T) {
	image := "example.invalid/api@sha256:" + strings.Repeat("a", 64)
	allowed := map[string]bool{image: true}
	for _, policy := range []string{"", "Always", "IfNotPresent", "Never"} {
		t.Run(policy, func(t *testing.T) {
			resource := map[string]any{"spec": map[string]any{"containers": []any{map[string]any{"image": image, "imagePullPolicy": policy}}}}
			err := checkRenderedImages(resource, allowed)
			if policy == "Never" {
				if err != nil {
					t.Fatalf("verified local image rejected: %v", err)
				}
			} else if err == nil {
				t.Fatalf("cache loss may contact a registry with policy %q", policy)
			}
		})
	}
}

func TestInstallRefusesExistingResources(t *testing.T) {
	resource := []byte("kind: Service\nmetadata: {name: ops-api, labels: {ops.platform.io/release: ops-dependencies}}\n")
	run := func(context.Context, string, ...string) ([]byte, error) { return []byte("{\"kind\":\"Service\"}"), nil }
	if err := validateRenderedChart(context.Background(), "ops-system", resource, map[string]bool{}, importProfile(), "ops-dependencies", run); err == nil || !strings.Contains(err.Error(), "CONFLICT") {
		t.Fatalf("adoption error=%v", err)
	}
}

func TestRenderAcceptsOnlyOperatorSuppliedSecretReferences(t *testing.T) {
	resource := []byte(`kind: Secret
metadata:
  name: ops-openbao-external-credential-reference
  labels:
    ops.platform.io/release: ops-dependencies
  annotations:
    ops.platform.io/credential-secret-name: ops-openbao-auth
    ops.platform.io/username-key: username
    ops.platform.io/password-key: token
type: ops.platform.io/external-credential-reference
`)
	calls := 0
	run := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		calls++
		if strings.Contains(strings.Join(args, " "), "go-template=") {
			return []byte("present"), nil
		}
		return nil, nil
	}
	if err := validateRenderedChart(context.Background(), "ops-system", resource, nil, importProfile(), "ops-dependencies", run); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("expected two key checks and an ownership check; got %d", calls)
	}
	if err := validateRenderedChart(context.Background(), "ops-system", append(resource, []byte("data: {token: c2VjcmV0}\n")...), nil, importProfile(), "ops-dependencies", run); err == nil {
		t.Fatal("embedded credential accepted")
	}
}

func TestRenderRefusesUnexpectedNamespaceBeforeResourceAccess(t *testing.T) {
	called := false
	run := func(context.Context, string, ...string) ([]byte, error) { called = true; return nil, nil }
	err := validateRenderedChart(context.Background(), "ops-system", []byte("kind: ConfigMap\nmetadata: {name: wrong, namespace: user-data, labels: {ops.platform.io/release: ops-dependencies}}\n"), nil, importProfile(), "ops-dependencies", run)
	if err == nil || called {
		t.Fatalf("namespace error=%v called=%v", err, called)
	}
}

func TestRenderRejectsUnlabeledResourcesBeforeClusterAccess(t *testing.T) {
	called := false
	run := func(context.Context, string, ...string) ([]byte, error) { called = true; return nil, nil }
	err := validateRenderedChart(context.Background(), "ops-system", []byte("kind: ConfigMap\nmetadata: {name: unowned}\n"), nil, importProfile(), "ops-platform", run)
	if err == nil || called || !strings.Contains(err.Error(), "release label") {
		t.Fatalf("ownership error=%v called=%v", err, called)
	}
}

func TestCleanupReleaseVerifiesOwnershipAndNeverDeletesPVCs(t *testing.T) {
	p := importProfile()
	var calls [][]string
	run := func(_ context.Context, program string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{program}, args...))
		joined := strings.Join(append([]string{program}, args...), " ")
		switch {
		case strings.Contains(joined, "helm list"):
			return []byte(`[{"name":"ops-platform"}]`), nil
		case strings.Contains(joined, "helm get manifest"):
			return []byte("kind: ConfigMap\nmetadata:\n  name: ops-core-profile\n  labels:\n    ops.platform.io/release: ops-platform\n"), nil
		case strings.Contains(joined, "kubectl"):
			return []byte(`{"metadata":{"labels":{"ops.platform.io/release":"ops-platform"}}}`), nil
		default:
			return nil, nil
		}
	}
	if err := CleanupRelease(context.Background(), p, "ops-platform", run); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 4 || !strings.Contains(strings.Join(calls[3], " "), "helm uninstall ops-platform") {
		t.Fatalf("cleanup call order=%v", calls)
	}
	for _, call := range calls {
		if strings.Contains(strings.Join(call, " "), "persistentvolume") {
			t.Fatalf("cleanup touched PVCs: %v", call)
		}
	}
}

func TestCleanupReleaseRejectsChangedOwnershipBeforeUninstall(t *testing.T) {
	p := importProfile()
	uninstalled := false
	run := func(_ context.Context, program string, args ...string) ([]byte, error) {
		joined := strings.Join(append([]string{program}, args...), " ")
		switch {
		case strings.Contains(joined, "helm list"):
			return []byte(`[{"name":"ops-platform"}]`), nil
		case strings.Contains(joined, "helm get manifest"):
			return []byte("kind: ConfigMap\nmetadata:\n  name: ops-core-profile\n  labels:\n    ops.platform.io/release: ops-platform\n"), nil
		case strings.Contains(joined, "kubectl"):
			return []byte(`{"metadata":{"labels":{"ops.platform.io/release":"someone-else"}}}`), nil
		case strings.Contains(joined, "helm uninstall"):
			uninstalled = true
		}
		return nil, nil
	}
	if err := CleanupRelease(context.Background(), p, "ops-platform", run); err == nil || uninstalled {
		t.Fatalf("changed ownership error=%v uninstall=%v", err, uninstalled)
	}
}

func TestCleanupReleaseRejectsPersistentVolumeClaimsBeforeUninstall(t *testing.T) {
	p := importProfile()
	uninstalled := false
	run := func(_ context.Context, program string, args ...string) ([]byte, error) {
		joined := strings.Join(append([]string{program}, args...), " ")
		switch {
		case strings.Contains(joined, "helm list"):
			return []byte(`[{"name":"ops-platform"}]`), nil
		case strings.Contains(joined, "helm get manifest"):
			return []byte("kind: PersistentVolumeClaim\nmetadata:\n  name: user-data\n  labels:\n    ops.platform.io/release: ops-platform\n"), nil
		case strings.Contains(joined, "helm uninstall"):
			uninstalled = true
		}
		return nil, nil
	}
	if err := CleanupRelease(context.Background(), p, "ops-platform", run); err == nil || uninstalled {
		t.Fatalf("PVC cleanup error=%v uninstall=%v", err, uninstalled)
	}
}

func TestRequireAbsentReleaseRejectsExistingHelmRelease(t *testing.T) {
	run := func(context.Context, string, ...string) ([]byte, error) {
		return []byte(`[{"name":"ops-platform"}]`), nil
	}
	if err := requireAbsentRelease(context.Background(), "ops-system", importProfile(), "ops-platform", run); err == nil || !strings.Contains(err.Error(), "CONFLICT") {
		t.Fatalf("existing release error=%v", err)
	}
}

func TestCleanupReleaseAcceptsOwnedMonitoringResourcesAndRejectsForeignOnes(t *testing.T) {
	for _, kind := range []string{"VMServiceScrape", "ServiceMonitor"} {
		for _, owner := range []string{"ops-platform", "protected-monitoring"} {
			t.Run(kind+"/"+owner, func(t *testing.T) {
				uninstalled := false
				run := func(_ context.Context, program string, args ...string) ([]byte, error) {
					joined := strings.Join(append([]string{program}, args...), " ")
					switch {
					case strings.Contains(joined, "helm list"):
						return []byte(`[{"name":"ops-platform"}]`), nil
					case strings.Contains(joined, "helm get manifest"):
						return []byte("kind: " + kind + "\nmetadata:\n  name: ops-platform\n  namespace: ops-system\n  labels:\n    ops.platform.io/release: ops-platform\n"), nil
					case strings.Contains(joined, "kubectl"):
						return []byte(`{"metadata":{"labels":{"ops.platform.io/release":"` + owner + `"}}}`), nil
					case strings.Contains(joined, "helm uninstall"):
						uninstalled = true
					}
					return nil, nil
				}
				err := CleanupRelease(context.Background(), importProfile(), "ops-platform", run)
				if owner == "ops-platform" {
					if err != nil || !uninstalled {
						t.Fatalf("owned %s cleanup failed: %v, uninstalled=%t", kind, err, uninstalled)
					}
				} else if err == nil || uninstalled {
					t.Fatalf("foreign monitoring object accepted: %v, uninstalled=%t", err, uninstalled)
				}
			})
		}
	}
}
