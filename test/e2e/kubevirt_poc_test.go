package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
	"ops-platform/internal/profile"
	"ops-platform/internal/supplychain"
)

const (
	kubeVirtPOCRelease   = "sp02-task-2-6"
	kubeVirtPOCNamespace = "ops-sp02-kubevirt-poc"
	kubeVirtLabel        = "ops.platform.io/release"
)

var kubeVirtImageReference = regexp.MustCompile(`quay\.io/kubevirt/[A-Za-z0-9._/-]+@sha256:[a-f0-9]{64}`)

func TestKubeVirtPOCArtifactsAndOrbStackLifecycle(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, name := range []string{
		"docs/compatibility/kubevirt-kubernetes-matrix.yaml",
		"deploy/addons/kubevirt/kubevirt-resolved.yaml",
		"deploy/addons/kubevirt/cdi-resolved.yaml",
		"docs/poc/kubevirt-cdi-orbstack.md",
	} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("required Task 2.6 PoC artifact %q: %v", name, err)
		}
	}
	assertKubeVirtArtifactLocks(t, root)
	if os.Getenv("OPS_KUBEVIRT_ORBSTACK_POC") != "1" {
		t.Skip("set OPS_KUBEVIRT_ORBSTACK_POC=1 to run the isolated KubeVirt/CDI OrbStack lifecycle PoC")
	}
	runKubeVirtOrbStackPOC(t, root)
}

func assertKubeVirtArtifactLocks(t *testing.T, root string) {
	t.Helper()
	catalogFile, err := os.Open(filepath.Join(root, "bundle", "component-catalog.yaml"))
	if err != nil {
		t.Fatalf("open Component Catalog: %v", err)
	}
	catalog, err := supplychain.LoadCatalogWithEvidence(catalogFile, os.DirFS(root))
	_ = catalogFile.Close()
	if err != nil {
		t.Fatalf("load Component Catalog: %v", err)
	}
	locked := make(map[string]bool)
	for _, name := range []string{"kubevirt", "cdi"} {
		component, ok := catalog.Component(name)
		if !ok || component.State != "candidate" || component.Version == "pending" || !strings.HasPrefix(component.Digest, "sha256:") {
			t.Fatalf("%s must have an exact, candidate Component Catalog lock before live PoC: %#v", name, component)
		}
		if len(component.Architectures) != 1 || component.Architectures[0] != "linux/arm64" {
			t.Fatalf("%s PoC image lock is not restricted to verified linux/arm64: %v", name, component.Architectures)
		}
		locked[component.Digest] = true
		for _, dependency := range component.DependencyClosure {
			if dependency.Version != component.Version || !strings.HasPrefix(dependency.Digest, "sha256:") {
				t.Fatalf("%s dependency %q is not exact and digest-pinned: %#v", name, dependency.Name, dependency)
			}
			locked[dependency.Digest] = true
		}
	}
	for _, name := range []string{"kubevirt-resolved.yaml", "cdi-resolved.yaml"} {
		contents, err := os.ReadFile(filepath.Join(root, "deploy", "addons", "kubevirt", name))
		if err != nil {
			t.Fatal(err)
		}
		refs := kubeVirtImageReference.FindAllString(string(contents), -1)
		if len(refs) == 0 {
			t.Fatalf("%s contains no digest-pinned kubevirt images", name)
		}
		for _, ref := range refs {
			_, digest, found := strings.Cut(ref, "@")
			if !found || !locked[digest] {
				t.Errorf("%s references an image not locked in the Component Catalog: %s", name, ref)
			}
		}
	}
	kubevirtManifest, err := os.ReadFile(filepath.Join(root, "deploy", "addons", "kubevirt", "kubevirt-resolved.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(kubevirtManifest), "useEmulation: true") {
		t.Fatal("KubeVirt resolved manifest must explicitly enable emulation for a node without /dev/kvm")
	}
}

type kubeVirtPOCRunner struct {
	ctx     context.Context
	records []string
	started time.Time
	cleaned bool
}

func runKubeVirtOrbStackPOC(t *testing.T, root string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Minute)
	defer cancel()
	runner := &kubeVirtPOCRunner{ctx: ctx, started: time.Now().UTC()}
	testNamespaceCreated := false
	defer func() {
		if testNamespaceCreated {
			runner.cleanupTestNamespace(t, kubeVirtPOCNamespace)
		}
		status := "FAILED; see command evidence below"
		if runner.cleaned {
			status = "PASSED: KubeVirt/CDI control plane and one VM/VMI/DataVolume lifecycle observed; test resources deleted"
		}
		if err := runner.writeReport(root, status); err != nil {
			t.Errorf("write Task 2.6 PoC evidence report: %v", err)
		}
	}()

	matrixPath := filepath.Join(root, "docs", "compatibility", "kubevirt-kubernetes-matrix.yaml")
	matrix, err := profile.LoadKubeVirtCompatibilityMatrix(matrixPath)
	if err != nil {
		t.Fatalf("load frozen official compatibility matrix: %v", err)
	}
	versionOutput := runner.mustKubectl(t, "get", "--raw", "/version")
	var server struct {
		GitVersion string `json:"gitVersion"`
		Platform   string `json:"platform"`
		GitCommit  string `json:"gitCommit"`
	}
	if err := json.Unmarshal([]byte(versionOutput), &server); err != nil || server.GitVersion == "" || server.Platform == "" {
		t.Fatalf("decode live Kubernetes Server version/platform: %v; output=%s", err, versionOutput)
	}
	decision := profile.DecideKubeVirtCompatibility(server.GitVersion, matrix)
	runner.record("Compatibility decision", fmt.Sprintf("server=%s platform=%s commit=%s decision=%s kubevirt=%s cdi=%s source=%s", server.GitVersion, server.Platform, server.GitCommit, decision.Decision, decision.KubeVirtVersion, decision.CDIVersion, decision.SourceDigest))
	if decision.Decision != "supported" || decision.KubeVirtVersion != matrix.Selected.KubeVirtVersion || decision.CDIVersion != matrix.Selected.CDIVersion {
		t.Fatalf("refuse KubeVirt installation before creating resources: frozen compatibility decision is %#v", decision)
	}
	if server.Platform != "linux/arm64" {
		t.Fatalf("refuse arm64 PoC artifacts on unexpected Server platform %q", server.Platform)
	}
	nodeOutput := runner.mustKubectl(t, "get", "nodes", "-o", "json")
	var nodes struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Status struct {
				NodeInfo struct {
					Architecture string `json:"architecture"`
				} `json:"nodeInfo"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(nodeOutput), &nodes); err != nil || len(nodes.Items) != 1 || nodes.Items[0].Status.NodeInfo.Architecture != "arm64" {
		t.Fatalf("require one live linux/arm64 OrbStack node, got decode error=%v nodes=%s", err, nodeOutput)
	}
	runner.record("Node", fmt.Sprintf("name=%s architecture=%s", nodes.Items[0].Metadata.Name, nodes.Items[0].Status.NodeInfo.Architecture))

	// KVM detection uses the already-running node-exporter host-root mount and is read-only.
	podList := runner.mustKubectl(t, "-n", "monitoring", "get", "pods", "-o", "json")
	var pods struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Status struct {
				Phase string `json:"phase"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(podList), &pods); err != nil {
		t.Fatalf("decode existing monitoring Pods for read-only KVM probe: %v", err)
	}
	var nodeExporter string
	for _, pod := range pods.Items {
		if strings.Contains(pod.Metadata.Name, "node-exporter") && pod.Status.Phase == "Running" {
			nodeExporter = pod.Metadata.Name
			break
		}
	}
	if nodeExporter == "" {
		t.Fatal("KVM capability is unverified: no already-running node-exporter with a read-only host-root mount")
	}
	kvmProbe := runner.kubectl("-n", "monitoring", "exec", nodeExporter, "--", "sh", "-c", "if test -c /host/root/dev/kvm; then echo present; else echo absent; fi")
	if kvmProbe.err != nil || (strings.TrimSpace(kvmProbe.output) != "present" && strings.TrimSpace(kvmProbe.output) != "absent") {
		t.Fatalf("KVM capability is unverified from read-only /dev/kvm probe: output=%q error=%v", kvmProbe.output, kvmProbe.err)
	}
	kvm := strings.TrimSpace(kvmProbe.output) == "present"
	runner.record("KVM capability", fmt.Sprintf("/host/root/dev/kvm=%s; useEmulation=%t", map[bool]string{true: "character-device-present", false: "absent"}[kvm], !kvm))
	if !kvm && !strings.Contains(stringMustRead(t, filepath.Join(root, "deploy", "addons", "kubevirt", "kubevirt-resolved.yaml")), "useEmulation: true") {
		t.Fatal("KVM is absent but resolved KubeVirt manifest does not explicitly enable software emulation")
	}

	// Reuse only a complete installation created by this PoC and still matching the exact lock.
	// Any partial, externally owned, or drifted installation is a conflict; never apply over it.
	baseResources := []string{"crd/kubevirts.kubevirt.io", "crd/cdis.cdi.kubevirt.io", "namespace/kubevirt", "namespace/cdi"}
	presence := make([]bool, 0, len(baseResources))
	for _, resource := range baseResources {
		result := runner.kubectl("get", resource, "-o", "json")
		if result.err == nil {
			presence = append(presence, true)
			runner.record("Pre-install inventory", resource+" present")
			continue
		}
		if !isKubernetesNotFound(result.output) {
			t.Fatalf("cannot safely establish presence of %s; refusing installation: output=%s error=%v", resource, result.output, result.err)
		}
		presence = append(presence, false)
		runner.record("Pre-install inventory", resource+" absent")
	}
	if result := runner.kubectl("get", "namespace", kubeVirtPOCNamespace, "-o", "json"); result.err == nil {
		t.Fatalf("test namespace %s already exists; refusing to touch resources from another PoC run: %s", kubeVirtPOCNamespace, result.output)
	} else if !isKubernetesNotFound(result.output) {
		t.Fatalf("cannot safely establish test namespace absence: %s (%v)", result.output, result.err)
	}
	installedCount := 0
	for _, exists := range presence {
		if exists {
			installedCount++
		}
	}
	if installedCount != 0 && installedCount != len(baseResources) {
		t.Fatalf("partial pre-existing KubeVirt/CDI installation (%d of %d ownership resources); refusing to alter it", installedCount, len(baseResources))
	}
	reuseInstallation := installedCount == len(baseResources)
	if reuseInstallation {
		for _, resource := range baseResources {
			result := runner.mustKubectl(t, "get", resource, "-o", "json")
			var object map[string]any
			if err := json.Unmarshal([]byte(result), &object); err != nil {
				t.Fatalf("decode existing %s: %v", resource, err)
			}
			if firstStringAt(object, "metadata", "labels", kubeVirtLabel) != kubeVirtPOCRelease {
				t.Fatalf("existing %s is not owned by release %q; refusing to reuse or mutate it", resource, kubeVirtPOCRelease)
			}
		}
		runner.record("Installation mode", "reusing the complete Task 2.6 release after ownership labels matched; no existing cluster objects were applied or changed")
	} else {
		runner.record("Installation mode", "no existing KubeVirt/CDI resources found; applying exact resolved manifests")
		for _, manifestName := range []string{"kubevirt-resolved.yaml", "cdi-resolved.yaml"} {
			manifest, err := os.ReadFile(filepath.Join(root, "deploy", "addons", "kubevirt", manifestName))
			if err != nil {
				t.Fatalf("read resolved %s: %v", manifestName, err)
			}
			images := uniqueSorted(kubeVirtImageReference.FindAllString(string(manifest), -1))
			for _, image := range images {
				result := runner.command("docker", "pull", "--platform", "linux/arm64", image)
				if result.err != nil {
					t.Fatalf("pre-pull exact arm64 image %s failed: %v; output=%s", image, result.err, result.output)
				}
				runner.record("Image pre-pull", image+" verified by Docker pull --platform linux/arm64")
			}
		}

		kubevirtManifest, err := os.ReadFile(filepath.Join(root, "deploy", "addons", "kubevirt", "kubevirt-resolved.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		kubevirtBase, kubevirtCR, err := labelAndSplitKubernetesYAML(kubevirtManifest, "KubeVirt")
		if err != nil {
			t.Fatalf("prepare release-labeled upstream KubeVirt resources: %v", err)
		}
		cdiManifest, err := os.ReadFile(filepath.Join(root, "deploy", "addons", "kubevirt", "cdi-resolved.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		cdiBase, cdiCR, err := labelAndSplitKubernetesYAML(cdiManifest, "CDI")
		if err != nil {
			t.Fatalf("prepare release-labeled upstream CDI resources: %v", err)
		}
		kubevirtBaseFile := runner.tempManifest(t, "kubevirt-base-*.yaml", kubevirtBase)
		kubevirtCRFile := runner.tempManifest(t, "kubevirt-cr-*.yaml", kubevirtCR)
		cdiBaseFile := runner.tempManifest(t, "cdi-base-*.yaml", cdiBase)
		cdiCRFile := runner.tempManifest(t, "cdi-cr-*.yaml", cdiCR)

		runner.mustKubectl(t, "apply", "-f", kubevirtBaseFile)
		runner.mustKubectl(t, "wait", "--for=condition=Established", "crd/kubevirts.kubevirt.io", "--timeout=180s")
		runner.mustKubectl(t, "apply", "-f", cdiBaseFile)
		runner.mustKubectl(t, "wait", "--for=condition=Established", "crd/cdis.cdi.kubevirt.io", "--timeout=180s")
		runner.mustKubectl(t, "apply", "-f", kubevirtCRFile)
		runner.mustKubectl(t, "apply", "-f", cdiCRFile)
		runner.mustKubectl(t, "-n", "kubevirt", "rollout", "status", "deployment/virt-operator", "--timeout=10m")
		runner.mustKubectl(t, "-n", "cdi", "rollout", "status", "deployment/cdi-operator", "--timeout=10m")
		runner.mustKubectl(t, "-n", "kubevirt", "wait", "--for=condition=Available", "kubevirt/kubevirt", "--timeout=25m")
		runner.mustKubectl(t, "-n", "cdi", "wait", "--for=condition=Available", "cdi/cdi", "--timeout=25m")
	}

	kubeVirtState := runner.mustKubectl(t, "-n", "kubevirt", "get", "kubevirt", "kubevirt", "-o", "json")
	cdiState := runner.mustKubectl(t, "-n", "cdi", "get", "cdi", "cdi", "-o", "json")
	var kubevirtObject map[string]any
	var cdiObject map[string]any
	if err := json.Unmarshal([]byte(kubeVirtState), &kubevirtObject); err != nil {
		t.Fatalf("decode installed KubeVirt status: %v", err)
	}
	if err := json.Unmarshal([]byte(cdiState), &cdiObject); err != nil {
		t.Fatalf("decode installed CDI status: %v", err)
	}
	if reuseInstallation {
		if firstStringAt(kubevirtObject, "metadata", "labels", kubeVirtLabel) != kubeVirtPOCRelease || firstStringAt(cdiObject, "metadata", "labels", kubeVirtLabel) != kubeVirtPOCRelease {
			t.Fatal("existing KubeVirt or CDI object is not owned by this Task 2.6 release; refusing to reuse it")
		}
	}
	actualKubeVirt := firstStringAt(kubevirtObject, "status", "observedKubeVirtVersion")
	if actualKubeVirt == "" {
		actualKubeVirt = firstStringAt(kubevirtObject, "status", "operatorVersion")
	}
	actualCDI := firstStringAt(cdiObject, "status", "operatorVersion")
	if strings.TrimPrefix(actualKubeVirt, "v") != strings.TrimPrefix(decision.KubeVirtVersion, "v") || strings.TrimPrefix(actualCDI, "v") != strings.TrimPrefix(decision.CDIVersion, "v") {
		t.Fatalf("installed versions do not match frozen selection: observed KubeVirt=%q CDI=%q; expected %q/%q", actualKubeVirt, actualCDI, decision.KubeVirtVersion, decision.CDIVersion)
	}
	kubeVirtOperatorImage := runner.mustKubectl(t, "-n", "kubevirt", "get", "deployment", "virt-operator", "-o", "jsonpath={.spec.template.spec.containers[0].image}")
	cdiOperatorImage := runner.mustKubectl(t, "-n", "cdi", "get", "deployment", "cdi-operator", "-o", "jsonpath={.spec.template.spec.containers[0].image}")
	if !strings.Contains(kubeVirtOperatorImage, "@sha256:") || !strings.Contains(cdiOperatorImage, "@sha256:") {
		t.Fatalf("operator image refs are not immutable digests: KubeVirt=%q CDI=%q", kubeVirtOperatorImage, cdiOperatorImage)
	}
	if reuseInstallation && (!strings.Contains(kubeVirtOperatorImage, "virt-operator@sha256:1c962a0ada538339b59d1059231c96b3f245f174ed8c30b64ef6c9f2b28cd074") || !strings.Contains(cdiOperatorImage, "cdi-operator@sha256:e82318852aa9eae2174cdc3e7be9f2cfc6fbcc1375822597db1f5daf04f13d2b")) {
		t.Fatalf("existing owned release has image digest drift; refusing reuse: KubeVirt=%s CDI=%s", kubeVirtOperatorImage, cdiOperatorImage)
	}
	if !kvm && !firstBoolAt(kubevirtObject, "spec", "configuration", "developerConfiguration", "useEmulation") {
		t.Fatalf("existing KubeVirt release does not enable emulation although /dev/kvm is absent")
	}
	runner.record("Installed versions", fmt.Sprintf("KubeVirt.status.operatorVersion=%s operatorImage=%s; CDI.status.operatorVersion=%s operatorImage=%s", actualKubeVirt, kubeVirtOperatorImage, actualCDI, cdiOperatorImage))
	virtualizationCapability := profile.VirtualizationCapability{
		KVM:             kvm,
		Emulation:       !kvm,
		KubeVirtVersion: actualKubeVirt,
		CDIVersion:      actualCDI,
		Architectures:   []string{server.Platform},
	}
	capabilityBytes, err := json.Marshal(virtualizationCapability)
	if err != nil {
		t.Fatalf("encode observed virtualization capability: %v", err)
	}
	runner.record("VirtualizationCapability", string(capabilityBytes))

	storageClasses := runner.mustKubectl(t, "get", "storageclass", "-o", "json")
	var storageClassList struct {
		Items []struct {
			Metadata struct {
				Name        string            `json:"name"`
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(storageClasses), &storageClassList); err != nil {
		t.Fatalf("decode OrbStack StorageClasses: %v", err)
	}
	var defaultStorageClass string
	for _, storageClass := range storageClassList.Items {
		if storageClass.Metadata.Annotations["storageclass.kubernetes.io/is-default-class"] == "true" {
			defaultStorageClass = storageClass.Metadata.Name
			break
		}
	}
	if defaultStorageClass == "" {
		t.Fatalf("OrbStack default StorageClass is unavailable; refusing to create a DataVolume: %s", storageClasses)
	}
	runner.record("StorageClass", "default="+defaultStorageClass)
	namespaceManifest := fmt.Sprintf("apiVersion: v1\nkind: Namespace\nmetadata:\n  name: %s\n  labels:\n    %s: %s\n", kubeVirtPOCNamespace, kubeVirtLabel, kubeVirtPOCRelease)
	nsFile := runner.tempManifest(t, "kubevirt-poc-namespace-*.yaml", []byte(namespaceManifest))
	runner.mustKubectl(t, "apply", "-f", nsFile)
	testNamespaceCreated = true
	dvManifest := fmt.Sprintf(strings.TrimSpace(`apiVersion: cdi.kubevirt.io/v1beta1
kind: DataVolume
metadata:
  name: sp02-blank-volume
  namespace: %s
  labels:
    %s: %s
spec:
  source:
    blank: {}
  storage:
    accessModes:
      - ReadWriteOnce
    resources:
      requests:
        storage: 1Gi
    storageClassName: %s
    volumeMode: Filesystem
	`)+"\n", kubeVirtPOCNamespace, kubeVirtLabel, kubeVirtPOCRelease, defaultStorageClass)
	dvFile := runner.tempManifest(t, "kubevirt-poc-datavolume-*.yaml", []byte(dvManifest))
	runner.mustKubectl(t, "apply", "-f", dvFile)
	runner.record("DataVolume binding", "the observed default StorageClass uses WaitForFirstConsumer; create the consuming VM before waiting for DataVolume Succeeded")
	vmManifest := fmt.Sprintf(strings.TrimSpace(`apiVersion: kubevirt.io/v1
kind: VirtualMachine
metadata:
  name: sp02-lifecycle-vm
  namespace: %s
  labels:
    %s: %s
spec:
  running: true
  template:
    metadata:
      labels:
        kubevirt.io/domain: sp02-lifecycle-vm
        %s: %s
    spec:
      architecture: arm64
      domain:
        cpu:
          cores: 1
        resources:
          requests:
            memory: 512Mi
        devices:
          disks:
            - name: rootdisk
              disk:
                bus: virtio
      volumes:
        - name: rootdisk
          dataVolume:
            name: sp02-blank-volume
	`)+"\n", kubeVirtPOCNamespace, kubeVirtLabel, kubeVirtPOCRelease, kubeVirtLabel, kubeVirtPOCRelease)
	vmFile := runner.tempManifest(t, "kubevirt-poc-vm-*.yaml", []byte(vmManifest))
	runner.mustKubectl(t, "apply", "-f", vmFile)
	waitForKubeVirtJSON(t, runner, "-n", kubeVirtPOCNamespace, "get", "datavolume", "sp02-blank-volume", "-o", "json", "status.phase", "Succeeded", 10*time.Minute)
	waitForKubeVirtJSON(t, runner, "-n", kubeVirtPOCNamespace, "get", "vmi", "sp02-lifecycle-vm", "-o", "json", "status.phase", "Running", 20*time.Minute)
	runner.mustKubectl(t, "-n", kubeVirtPOCNamespace, "get", "vm", "sp02-lifecycle-vm", "-o", "yaml")
	runner.mustKubectl(t, "-n", kubeVirtPOCNamespace, "get", "vmi", "sp02-lifecycle-vm", "-o", "yaml")
	runner.mustKubectl(t, "-n", kubeVirtPOCNamespace, "get", "datavolume", "sp02-blank-volume", "-o", "yaml")
	runner.mustKubectl(t, "-n", kubeVirtPOCNamespace, "get", "events", "--sort-by=.lastTimestamp")
	runner.mustKubectl(t, "-n", kubeVirtPOCNamespace, "delete", "vm/sp02-lifecycle-vm", "datavolume/sp02-blank-volume", "--wait=true", "--timeout=5m")
	runner.mustKubectl(t, "delete", "namespace", kubeVirtPOCNamespace, "--wait=true", "--timeout=5m")
	testNamespaceCreated = false
	if result := runner.kubectl("get", "namespace", kubeVirtPOCNamespace); result.err == nil {
		t.Fatalf("Task 2.6 test namespace still exists after deletion: %s", result.output)
	}
	runner.record("Cleanup", "deleted VM, DataVolume and release-labeled test namespace; left installed KubeVirt/CDI operators and existing platform resources untouched")
	runner.cleaned = true
}

func waitForKubeVirtJSON(t *testing.T, runner *kubeVirtPOCRunner, args ...any) {
	t.Helper()
	timeout := args[len(args)-1].(time.Duration)
	want := args[len(args)-2].(string)
	field := args[len(args)-3].(string)
	commandArgs := make([]string, 0, len(args)-3)
	for _, arg := range args[:len(args)-3] {
		commandArgs = append(commandArgs, arg.(string))
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		result := runner.kubectl(commandArgs...)
		if result.err == nil {
			var object map[string]any
			if json.Unmarshal([]byte(result.output), &object) == nil {
				if got := firstStringAt(object, strings.Split(field, ".")...); got == want {
					runner.record("Lifecycle observed", strings.Join(commandArgs, " ")+" "+field+"="+got)
					return
				}
				if terminal := terminalKubeVirtFailure(object); terminal != "" {
					runner.record("Lifecycle blocked", strings.Join(commandArgs, " ")+"\n"+terminal)
					if namespace := kubeVirtNamespaceArg(commandArgs); namespace != "" {
						events := runner.kubectl("-n", namespace, "get", "events", "--sort-by=.lastTimestamp")
						runner.record("Lifecycle warning events", events.output)
					}
					t.Fatalf("KubeVirt reported a non-transient VM startup failure while waiting for %s=%s: %s", field, want, terminal)
				}
				phase := firstStringAt(object, "status", "phase")
				runner.record("Lifecycle pending", strings.Join(commandArgs, " ")+" status.phase="+phase)
			}
		} else {
			runner.record("Lifecycle pending", strings.Join(commandArgs, " ")+" error="+result.err.Error()+" output="+result.output)
		}
		select {
		case <-runner.ctx.Done():
			t.Fatalf("timed out waiting for %s=%s: %v", field, want, runner.ctx.Err())
		case <-time.After(5 * time.Second):
		}
	}
	last := runner.kubectl(commandArgs...)
	t.Fatalf("timed out waiting for %s=%s after %s: output=%s error=%v", field, want, timeout, last.output, last.err)
}

type kubeVirtCommandResult struct {
	output string
	err    error
}

func (runner *kubeVirtPOCRunner) command(binary string, args ...string) kubeVirtCommandResult {
	command := exec.CommandContext(runner.ctx, binary, args...)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	err := command.Run()
	text := strings.TrimSpace(output.String())
	if text == "" {
		text = "(no output)"
	}
	status := "exit=0"
	if err != nil {
		status = "error=" + err.Error()
	}
	runner.record("$ "+binary+" "+strings.Join(args, " "), status+"\n"+limitKubeVirtOutput(text, 5000))
	return kubeVirtCommandResult{output: text, err: err}
}

func (runner *kubeVirtPOCRunner) kubectl(args ...string) kubeVirtCommandResult {
	all := append([]string{"--context", "orbstack"}, args...)
	return runner.command("kubectl", all...)
}

func (runner *kubeVirtPOCRunner) mustKubectl(t *testing.T, args ...string) string {
	t.Helper()
	result := runner.kubectl(args...)
	if result.err != nil {
		t.Fatalf("kubectl %s failed: %v\n%s", strings.Join(args, " "), result.err, result.output)
	}
	return result.output
}

func (runner *kubeVirtPOCRunner) tempManifest(t *testing.T, pattern string, contents []byte) string {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), pattern)
	if err != nil {
		t.Fatalf("create temporary Kubernetes manifest: %v", err)
	}
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		t.Fatalf("set temporary manifest mode: %v", err)
	}
	if _, err := file.Write(contents); err != nil {
		_ = file.Close()
		t.Fatalf("write temporary Kubernetes manifest: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close temporary Kubernetes manifest: %v", err)
	}
	return file.Name()
}

func (runner *kubeVirtPOCRunner) record(label, value string) {
	runner.records = append(runner.records, "### "+label+"\n\n"+value)
}

func (runner *kubeVirtPOCRunner) writeReport(root, status string) error {
	path := filepath.Join(root, "docs", "poc", "kubevirt-cdi-orbstack.md")
	contents, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	base := string(contents)
	previousExecution := ""
	if marker := strings.Index(base, "## PoC execution"); marker >= 0 {
		previousExecution = strings.TrimSpace(base[marker:])
		base = strings.TrimSpace(base[:marker])
	}
	attempt := 1
	if previousExecution != "" {
		attempt = strings.Count(previousExecution, "### Attempt ") + 1
		if !strings.Contains(previousExecution, "### Attempt ") {
			if offset := strings.Index(previousExecution, "\n"); offset >= 0 {
				previousExecution = previousExecution[:offset+1] + "\n### Attempt 1\n\n" + previousExecution[offset+1:]
			}
		}
	}
	section := "## PoC execution\n\n"
	if previousExecution != "" {
		section = previousExecution + "\n\n---\n\n"
	}
	section += fmt.Sprintf("### Attempt %d\n\n- Run started: %s\n- Status: %s\n- Context: `orbstack`\n- Duration: %s\n\n", attempt, runner.started.Format(time.RFC3339), status, time.Since(runner.started).Round(time.Second))
	section += strings.Join(runner.records, "\n\n") + "\n"
	return os.WriteFile(path, []byte(base+"\n\n"+section), 0644)
}

func (runner *kubeVirtPOCRunner) cleanupTestNamespace(t *testing.T, namespace string) {
	t.Helper()
	result := runner.kubectl("get", "namespace", namespace, "-o", "json")
	if result.err != nil {
		return
	}
	var object map[string]any
	if json.Unmarshal([]byte(result.output), &object) != nil || firstStringAt(object, "metadata", "labels", kubeVirtLabel) != kubeVirtPOCRelease {
		runner.record("Cleanup", "refused to delete test namespace because release ownership label did not match")
		return
	}
	for _, resource := range []string{"virtualmachine/sp02-lifecycle-vm", "virtualmachineinstance/sp02-lifecycle-vm", "datavolume/sp02-blank-volume"} {
		runner.cleanupOwnedKubeVirtResource(t, namespace, resource)
	}
	delete := runner.kubectl("delete", "namespace", namespace, "--wait=true", "--timeout=5m")
	if delete.err == nil {
		runner.record("Cleanup", "deleted release-labeled test namespace after interrupted or failed PoC")
		return
	}
	runner.record("Cleanup", "namespace deletion did not complete: "+delete.err.Error()+" "+delete.output)
}

func (runner *kubeVirtPOCRunner) cleanupOwnedKubeVirtResource(t *testing.T, namespace, resource string) {
	t.Helper()
	result := runner.kubectl("-n", namespace, "get", resource, "-o", "json")
	if result.err != nil {
		if isKubernetesNotFound(result.output) {
			return
		}
		runner.record("Cleanup", "could not inspect "+resource+": "+result.err.Error()+" "+result.output)
		return
	}
	var object map[string]any
	if err := json.Unmarshal([]byte(result.output), &object); err != nil || firstStringAt(object, "metadata", "labels", kubeVirtLabel) != kubeVirtPOCRelease {
		runner.record("Cleanup", "refused to delete "+resource+" because release ownership label did not match")
		return
	}
	delete := runner.kubectl("-n", namespace, "delete", resource, "--wait=true", "--timeout=90s")
	if delete.err == nil {
		runner.record("Cleanup", "deleted release-labeled "+resource)
		return
	}
	remaining := runner.kubectl("-n", namespace, "get", resource, "-o", "json")
	if remaining.err != nil || json.Unmarshal([]byte(remaining.output), &object) != nil || firstStringAt(object, "metadata", "labels", kubeVirtLabel) != kubeVirtPOCRelease {
		runner.record("Cleanup", "refused finalizer recovery for "+resource+" because release ownership could not be reconfirmed")
		return
	}
	finalizers, _ := object["metadata"].(map[string]any)["finalizers"].([]any)
	if len(finalizers) == 0 {
		runner.record("Cleanup", "delete remains pending without finalizers for "+resource+": "+delete.err.Error())
		return
	}
	patch := runner.kubectl("-n", namespace, "patch", resource, "--type=merge", "-p", `{"metadata":{"finalizers":[]}}`)
	if patch.err != nil {
		runner.record("Cleanup", "could not clear finalizer on release-labeled "+resource+": "+patch.err.Error()+" "+patch.output)
		return
	}
	runner.record("Cleanup", fmt.Sprintf("removed stuck finalizers %v from verified release-labeled %s after normal delete timed out: %s", finalizers, resource, delete.err))
}

func labelAndSplitKubernetesYAML(contents []byte, customKind string) ([]byte, []byte, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(contents))
	var base bytes.Buffer
	var custom bytes.Buffer
	baseEncoder := yaml.NewEncoder(&base)
	customEncoder := yaml.NewEncoder(&custom)
	defer baseEncoder.Close()
	defer customEncoder.Close()
	for {
		var document map[string]any
		err := decoder.Decode(&document)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, err
		}
		if len(document) == 0 {
			continue
		}
		metadata, ok := document["metadata"].(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("%s document has no metadata object", document["kind"])
		}
		labels, ok := metadata["labels"].(map[string]any)
		if !ok {
			labels = make(map[string]any)
			metadata["labels"] = labels
		}
		labels[kubeVirtLabel] = kubeVirtPOCRelease
		encoder := baseEncoder
		if document["kind"] == customKind {
			encoder = customEncoder
		}
		if err := encoder.Encode(document); err != nil {
			return nil, nil, err
		}
	}
	return base.Bytes(), custom.Bytes(), nil
}

func firstStringAt(value any, path ...string) string {
	current := value
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = object[key]
	}
	text, _ := current.(string)
	return text
}

func firstBoolAt(value any, path ...string) bool {
	current := value
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return false
		}
		current = object[key]
	}
	result, _ := current.(bool)
	return result
}

func terminalKubeVirtFailure(object map[string]any) string {
	status, _ := object["status"].(map[string]any)
	conditions, _ := status["conditions"].([]any)
	for _, value := range conditions {
		condition, _ := value.(map[string]any)
		if condition["status"] != "False" {
			continue
		}
		message, _ := condition["message"].(string)
		if strings.Contains(message, "unsupported configuration: CPU mode") || strings.Contains(message, "no gateway address found in routes") {
			conditionType, _ := condition["type"].(string)
			return fmt.Sprintf("condition %s: %s", conditionType, message)
		}
	}
	return ""
}

func kubeVirtNamespaceArg(args []string) string {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == "-n" {
			return args[index+1]
		}
	}
	return ""
}

func isKubernetesNotFound(output string) bool {
	return strings.Contains(output, "NotFound") || strings.Contains(strings.ToLower(output), "not found")
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]bool)
	result := make([]string, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func limitKubeVirtOutput(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max] + "\n...(truncated)"
}

func stringMustRead(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}
