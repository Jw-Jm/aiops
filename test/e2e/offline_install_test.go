package e2e

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
	"ops-platform/internal/bundle"
	"ops-platform/internal/profile"
)

// This test is a live acceptance entry point, not a default-suite simulation.
// Cleanup only targets Helm releases created by this invocation and preserves PVCs.
func TestCoreOfflineInstallation(t *testing.T) {
	if os.Getenv("OPS_OFFLINE_CORE_INSTALL") != "1" {
		t.Skip("live core offline installation not requested")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	bundleDir, key, profileFile := os.Getenv("OPS_OFFLINE_BUNDLE_DIR"), os.Getenv("OPS_OFFLINE_TRUST_KEY"), os.Getenv("OPS_OFFLINE_RESOLVED_PROFILE")
	if bundleDir == "" || key == "" || profileFile == "" {
		t.Fatal("live acceptance requires the actual signed Bundle, external trusted key and resolved Profile")
	}
	raw, err := os.ReadFile(profileFile)
	if err != nil {
		t.Fatal(err)
	}
	var p profile.ResolvedProfile
	if err := yaml.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	if !p.Installable || p.Selected != "core" {
		t.Fatal("core Profile is not installable; candidate admission must be completed first")
	}
	run := func(program string, args ...string) []byte {
		t.Helper()
		command := exec.CommandContext(t.Context(), program, args...)
		command.Dir = root
		command.Env = append(os.Environ(), "GOPROXY=off", "GOSUMDB=off")
		out, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v failed: %v\n%.8192s", program, args, err, out)
		}
		return out
	}
	run("go", "run", "./cmd/opsctl", "bundle", "verify", "--manifest", filepath.Join(bundleDir, "bundle.lock.json"), "--signature", filepath.Join(bundleDir, "bundle.lock.sig"), "--payload", filepath.Join(bundleDir, "payload.tar.zst"), "--key", key)
	manifestBytes, err := os.ReadFile(filepath.Join(bundleDir, "bundle.lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := bundle.ParseManifest(manifestBytes)
	if err != nil {
		t.Fatal(err)
	}
	signatureBytes, err := os.ReadFile(filepath.Join(bundleDir, "bundle.lock.sig"))
	if err != nil {
		t.Fatal(err)
	}
	manifest.Signature, err = base64.StdEncoding.DecodeString(strings.TrimSpace(string(signatureBytes)))
	if err != nil {
		t.Fatalf("decode detached signature: %v", err)
	}
	manifest.PayloadPath = filepath.Join(bundleDir, "payload.tar.zst")
	keyFile, err := os.Open(key)
	if err != nil {
		t.Fatal(err)
	}
	trust, err := bundle.ReadTrustRoot(keyFile)
	closeErr := keyFile.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	imagePlan, err := bundle.PlanImport(t.Context(), manifest, trust, p)
	if err != nil {
		t.Fatalf("validate signed Bundle image plan: %v", err)
	}
	if len(imagePlan) == 0 {
		t.Fatal("signed Bundle contains no OCI images")
	}
	if p.Components["openbao"].Mode == "external" {
		// A stale resolved Profile must not permit mutation while the reused
		// trust service is sealed or its independent CA no longer verifies it.
		verifyOpenBaoReadiness(t, p, bundleDir)
	}
	for _, image := range imagePlan {
		// Also inspect the manifest digest, so removing or changing a repository
		// alias cannot disguise an image that remains registered in the store.
		for _, reference := range []string{image.Reference, image.Digest} {
			output, err := exec.CommandContext(t.Context(), "docker", "--context", "orbstack", "image", "inspect", reference).CombinedOutput()
			if err == nil {
				t.Fatalf("clean-cache precondition failed: Bundle image %s is already present (%s)", image.Name, reference)
			}
			if !strings.Contains(string(output), "No such image") {
				t.Fatalf("cannot establish absence of Bundle image %s: %v: %s", image.Name, err, output)
			}
		}
	}
	releases := coreReleases(p)
	acceptanceComplete := false
	defer func() {
		if !acceptanceComplete {
			t.Log("acceptance incomplete: preserve created releases, imported images and PVCs for diagnosis; subsequent cleanup must verify release ownership")
		}
	}()
	run("go", "run", "./cmd/opsctl", "bundle", "import", "--profile", profileFile, "--bundle", bundleDir, "--key", key)
	protectedBefore := snapshotProtectedResources(t, run)
	run("go", "run", "./cmd/opsctl", "install", "--profile", "core", "--resolved", profileFile, "--bundle", bundleDir, "--key", key, "--offline")
	verifyProcessAvailability(t, p.Kubernetes.Context, run)
	verifyCoreCapabilities(t, p, bundleDir, run)
	// The new dependency PVCs must also survive cleanup and reinstall with the
	// same identity, alongside the pre-existing external trust/data resources.
	protectedWithDependencies := snapshotProtectedResources(t, run)
	verifyProtectedResources(t, protectedBefore, protectedWithDependencies)
	protectedBefore = protectedWithDependencies
	for _, release := range releases {
		if err := bundle.CleanupRelease(t.Context(), p, release, commandRunner(root)); err != nil {
			t.Fatalf("clean test-owned release %s: %v", release, err)
		}
	}
	verifyProtectedResources(t, protectedBefore, snapshotProtectedResources(t, run))
	run("go", "run", "./cmd/opsctl", "install", "--profile", "core", "--resolved", profileFile, "--bundle", bundleDir, "--key", key, "--offline")
	verifyProcessAvailability(t, p.Kubernetes.Context, run)
	verifyCoreCapabilities(t, p, bundleDir, run)
	runCoreNetworkProbe(t, p, imagePlan, run, root)
	verifyProtectedResources(t, protectedBefore, snapshotProtectedResources(t, run))
	acceptanceComplete = true
}

func runCoreNetworkProbe(t *testing.T, p profile.ResolvedProfile, images []bundle.ImageArtifact, run func(string, ...string) []byte, root string) {
	t.Helper()
	image := ""
	for _, candidate := range images {
		if candidate.Name == "postgresql" {
			image = candidate.Reference
			break
		}
	}
	if image == "" {
		t.Fatal("verified Bundle is missing the PostgreSQL image required for the in-cluster network probe")
	}
	dns := run("kubectl", "--context", p.Kubernetes.Context, "-n", "kube-system", "get", "service", "kube-dns", "-o", "json")
	var service struct {
		Spec struct {
			ClusterIP string `json:"clusterIP"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(dns, &service); err != nil || net.ParseIP(service.Spec.ClusterIP) == nil {
		t.Fatalf("actual internal DNS service is unavailable: %v", err)
	}
	name := fmt.Sprintf("ops-sp02-egress-%x", time.Now().UnixNano())
	script := `set -eu
	for attempt in 1 2 3 4 5 6; do
	  if ! timeout 2 /bin/bash -c 'exec 3<>/dev/tcp/$1/$2' _ "$OPS_OFFLINE_INTERNAL_HOST" "$OPS_OFFLINE_INTERNAL_PORT" >/dev/null 2>&1; then
	    echo INTERNAL_SERVICE_UNREACHABLE
	    sleep 2
	    continue
	  fi
	  public=0
	  for address in "1.1.1.1 443" "8.8.8.8 443" "2606:4700:4700::1111 443"; do
	    set -- $address
	    if timeout 2 /bin/bash -c 'exec 3<>/dev/tcp/$1/$2' _ "$1" "$2" >/dev/null 2>&1; then
	      echo PUBLIC_EGRESS_ALLOWED:$1:$2
	      public=1
	    fi
	  done
	  if [ "$public" = 0 ]; then
	    echo INTERNAL_SERVICE_REACHABLE_PUBLIC_EGRESS_DENIED
	    exit 0
	  fi
	  sleep 2
	done
	echo NETWORK_POLICY_NOT_ENFORCED`
	overrides, err := json.Marshal(map[string]any{"spec": map[string]any{
		"automountServiceAccountToken": false,
		"securityContext":              map[string]any{"runAsNonRoot": true, "runAsUser": 65532, "runAsGroup": 65532, "seccompProfile": map[string]any{"type": "RuntimeDefault"}},
		"containers":                   []any{map[string]any{"name": name, "securityContext": map[string]any{"allowPrivilegeEscalation": false, "readOnlyRootFilesystem": true, "capabilities": map[string]any{"drop": []string{"ALL"}}}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	created := false
	defer func() {
		if created {
			if _, err := commandRunner(root)(context.Background(), "kubectl", "--context", p.Kubernetes.Context, "-n", "ops-system", "delete", "pod", name, "--ignore-not-found", "--wait=true"); err != nil {
				t.Errorf("delete temporary in-cluster egress probe: %v", err)
			}
		}
	}()
	run("kubectl", "--context", p.Kubernetes.Context, "-n", "ops-system", "run", name,
		"--image="+image, "--image-pull-policy=Never", "--restart=Never",
		"--labels=ops.platform.io/release=ops-platform,ops.platform.io/component=api",
		"--env=OPS_OFFLINE_INTERNAL_HOST="+service.Spec.ClusterIP, "--env=OPS_OFFLINE_INTERNAL_PORT=53",
		"--override-type=strategic", "--overrides="+string(overrides), "--command", "--", "/bin/bash", "-ec", script)
	created = true
	run("kubectl", "--context", p.Kubernetes.Context, "-n", "ops-system", "wait", "--for=jsonpath={.status.phase}=Succeeded", "pod/"+name, "--timeout=60s")
	output := run("kubectl", "--context", p.Kubernetes.Context, "-n", "ops-system", "logs", "pod/"+name)
	if !strings.HasSuffix(strings.TrimSpace(string(output)), "INTERNAL_SERVICE_REACHABLE_PUBLIC_EGRESS_DENIED") {
		t.Fatalf("in-cluster egress probe did not prove both controls: %s", output)
	}
}

func removeBundleImages(t *testing.T, images []bundle.ImageArtifact) {
	t.Helper()
	for _, image := range images {
		output, err := exec.Command("docker", "--context", "orbstack", "image", "inspect", image.Reference).CombinedOutput()
		if err != nil {
			if strings.Contains(string(output), "No such image") {
				continue
			}
			t.Errorf("cannot determine whether test image %s is present: %v: %s", image.Name, err, output)
			continue
		}
		output, err = exec.Command("docker", "--context", "orbstack", "image", "rm", image.Reference).CombinedOutput()
		if err != nil {
			t.Errorf("remove test-imported image %s: %v: %s", image.Name, err, output)
		}
	}
}

func commandRunner(root string) bundle.CommandRunner {
	return func(ctx context.Context, program string, args ...string) ([]byte, error) {
		command := exec.CommandContext(ctx, program, args...)
		command.Dir = root
		command.Env = append(os.Environ(), "GOPROXY=off", "GOSUMDB=off")
		output, err := command.CombinedOutput()
		if err != nil {
			return output, fmt.Errorf("%s %v failed: %w: %.4096s", program, args, err, output)
		}
		return output, nil
	}
}

func coreReleases(p profile.ResolvedProfile) []string {
	releases := []string{"ops-dependencies"}
	if p.Components["victoriaMetrics"].Mode == "bundled" {
		releases = append(releases, "victoria-metrics")
	}
	if p.Components["victoriaLogs"].Mode == "bundled" {
		releases = append(releases, "victoria-logs")
	}
	if p.Components["vmalert"].Mode == "bundled" {
		releases = append(releases, "vmalert")
	}
	return append(releases, "ops-platform")
}

func snapshotProtectedResources(t *testing.T, run func(string, ...string) []byte) map[string]string {
	t.Helper()
	result := map[string]string{}
	for _, target := range []struct{ namespace, selector string }{
		{"monitoring", "deployments,pods,services,statefulsets"},
		{"ops-system", "deployments,pods,services,statefulsets"},
		{"", "persistentvolumeclaims"},
	} {
		args := []string{"--context", "orbstack"}
		if target.namespace == "" {
			args = append(args, "get", target.selector, "-A", "-o", "json")
		} else {
			args = append(args, "-n", target.namespace, "get", target.selector, "-o", "json")
			if target.namespace == "ops-system" {
				args = append(args, "-l", "ops.platform.io/component=openbao")
			}
		}
		var inventory struct {
			Items []struct {
				Kind     string `json:"kind"`
				Metadata struct {
					Name string `json:"name"`
					UID  string `json:"uid"`
				} `json:"metadata"`
			} `json:"items"`
		}
		if err := json.Unmarshal(run("kubectl", args...), &inventory); err != nil {
			t.Fatalf("decode protected resource inventory: %v", err)
		}
		for _, item := range inventory.Items {
			result[target.namespace+"/"+item.Kind+"/"+item.Metadata.Name] = item.Metadata.UID
		}
	}
	return result
}

func verifyProtectedResources(t *testing.T, before, after map[string]string) {
	t.Helper()
	for resource, uid := range before {
		if after[resource] != uid {
			t.Fatalf("protected resource %s changed identity or disappeared: before=%s after=%s", resource, uid, after[resource])
		}
	}
}

func verifyProcessAvailability(t *testing.T, contextName string, run func(string, ...string) []byte) {
	t.Helper()
	for _, deployment := range []string{"ops-api", "ops-worker"} {
		run("kubectl", "--context", contextName, "-n", "ops-system", "rollout", "status", "deployment/"+deployment, "--timeout=90s")
	}
	// The API/Worker binaries are process skeletons. Rollout availability is
	// recorded as process readiness, not business HTTP or feature readiness.
}
