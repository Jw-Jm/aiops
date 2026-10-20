package action

import (
	"encoding/json"
	"github.com/google/uuid"
	"strings"
	"testing"
	"time"
)

func TestKubernetesRunnerPodSpecHasNoCommandOrCredentials(t *testing.T) {
	d := Dispatch{TenantID: uuid.New(), ExecutionID: uuid.New(), Token: strings.Repeat("a", 43), Profile: Profile{SchemaVersion: "execution-profile/v2", ID: uuid.New(), Version: 1, Name: "namespaced", Type: "k8s_namespace", AllowedTargets: []string{"k8s+v1://tenant/cluster/core/Pod/uid"}, ClusterUID: "cluster", Namespace: "target", ToolImageDigest: "local/tool@" + Digest([]byte("tool")), Tools: []string{"bash", "kubectl"}, NetworkPolicyRef: "namespaced", CredentialRef: "serviceaccount:execution-target", TimeoutSeconds: 900, MaxOutputBytes: DefaultMaxOutputBytes}, Binding: Binding{Options: Options{TimeoutSeconds: 900}}}
	k := KubernetesExecutor{RunnerNamespace: "ops-runners", APIEndpoint: "https://ops-api:8445", TrustConfigMap: "runner-trust", IdentitySecret: "ops-api-identity"}
	b, err := k.Job(d)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "actualCommand") || strings.Contains(string(b), "KUBECONFIG") || strings.Contains(string(b), "bearer") {
		t.Fatal("spec leaked execution material")
	}
	var v map[string]any
	if json.Unmarshal(b, &v) != nil {
		t.Fatal("invalid job")
	}
	spec := v["spec"].(map[string]any)
	if spec["backoffLimit"] != float64(0) {
		t.Fatal("job permits automatic retry")
	}
}
func TestPostCheckIsIndependentOfExitCode(t *testing.T) {
	completed := time.Now()
	f := PostCheckFacts{CollectedAfter: completed.Add(1), CheckedAt: completed.Add(2), Authorized: true, SourceAvailable: true, Complete: true, FaultPresent: true, EvidenceRefs: []string{"fact"}}
	if JudgePostCheck(f, completed) != "not_resolved" {
		t.Fatal("fault ignored")
	}
	f.SourceAvailable = false
	if JudgePostCheck(f, completed) != "inconclusive" {
		t.Fatal("unavailable source passed")
	}
	f.SourceAvailable = true
	f.FaultPresent = false
	if JudgePostCheck(f, completed) != "resolved" {
		t.Fatal("fresh recovery rejected")
	}
	f.CollectedAfter = completed
	if JudgePostCheck(f, completed) != "inconclusive" {
		t.Fatal("historical evidence passed")
	}
}

func TestPVCVerificationJobConfinesMountAndDoesNotChownBusinessVolume(t *testing.T) {
	ns := "runners"
	target := probeTarget("PersistentVolumeClaim")
	d := Dispatch{TenantID: uuid.New(), ExecutionID: uuid.New(), Token: strings.Repeat("a", 43), Profile: Profile{SchemaVersion: "execution-profile/v2", ID: uuid.New(), Version: 1, Name: "probe", Type: "k8s_namespace", AllowedTargets: []string{target}, ClusterUID: "cluster", Namespace: ns, ToolImageDigest: "local/tool@" + Digest([]byte("tool")), Tools: []string{"ops-verify"}, NetworkPolicyRef: "probe", CredentialRef: "serviceaccount:execution-target", TimeoutSeconds: 30, MaxOutputBytes: 4096}, Binding: Binding{Target: target, Namespace: &ns, VerificationNamespace: ns, VerificationTargetName: "probe-pvc", Verification: &VerificationSpec{Kind: "pvc_read_write"}, Options: Options{TimeoutSeconds: 30}}}
	k := KubernetesExecutor{RunnerNamespace: ns, APIEndpoint: "https://api", TrustConfigMap: "trust", IdentitySecret: "api-identity"}
	raw, err := k.Job(d)
	if err != nil {
		t.Fatal(err)
	}
	var job map[string]any
	_ = json.Unmarshal(raw, &job)
	pod := job["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
	if _, ok := pod["securityContext"].(map[string]any)["fsGroup"]; ok {
		t.Fatal("probe can recursively change volume ownership")
	}
	container := pod["containers"].([]any)[0].(map[string]any)
	mounts := container["volumeMounts"].([]any)
	mount := mounts[len(mounts)-1].(map[string]any)
	if mount["subPath"] != VerificationSubPath || mount["mountPath"] != VerificationMount {
		t.Fatal("unbounded volume mount", mount)
	}
	other := "business"
	d.Binding.Namespace = &other
	if _, err := k.Job(d); err == nil {
		t.Fatal("cross namespace PVC allowed")
	}
}

// TestRunnerTrustTakesTheWorkloadCAFromTheIdentitySecret pins that the Runner
// cannot be pinned to an operator-supplied CA: the workload CA is projected from
// the same Secret the API serves with, alongside the ConfigMap bootstrap material.
func TestRunnerTrustTakesTheWorkloadCAFromTheIdentitySecret(t *testing.T) {
	ns := "runners"
	target := probeTarget("PersistentVolumeClaim")
	d := Dispatch{TenantID: uuid.New(), ExecutionID: uuid.New(), Token: strings.Repeat("a", 43), Profile: Profile{SchemaVersion: "execution-profile/v2", ID: uuid.New(), Version: 1, Name: "probe", Type: "k8s_namespace", AllowedTargets: []string{target}, ClusterUID: "cluster", Namespace: ns, ToolImageDigest: "local/tool@" + Digest([]byte("tool")), Tools: []string{"ops-verify"}, NetworkPolicyRef: "probe", CredentialRef: "serviceaccount:execution-target", TimeoutSeconds: 30, MaxOutputBytes: 4096}, Binding: Binding{Target: target, Namespace: &ns, VerificationNamespace: ns, VerificationTargetName: "probe-pvc", Verification: &VerificationSpec{Kind: "pvc_read_write"}, Options: Options{TimeoutSeconds: 30}}}
	k := KubernetesExecutor{RunnerNamespace: ns, APIEndpoint: "https://api", TrustConfigMap: "trust", IdentitySecret: "api-identity"}
	raw, err := k.Job(d)
	if err != nil {
		t.Fatal(err)
	}
	var job map[string]any
	if err := json.Unmarshal(raw, &job); err != nil {
		t.Fatal(err)
	}
	pod := job["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
	for _, item := range pod["volumes"].([]any) {
		volume := item.(map[string]any)
		if volume["name"] != "trust" {
			continue
		}
		if _, isConfigMap := volume["configMap"]; isConfigMap {
			t.Fatal("trust volume still relies on a supplied workload CA")
		}
		sources := volume["projected"].(map[string]any)["sources"].([]any)
		secret := sources[1].(map[string]any)["secret"].(map[string]any)
		if secret["name"] != "api-identity" {
			t.Fatalf("workload CA source = %v", secret["name"])
		}
		items := secret["items"].([]any)
		item0 := items[0].(map[string]any)
		if item0["key"] != "ca.pem" || item0["path"] != "workload-ca.pem" {
			t.Fatalf("workload CA projection = %v", item0)
		}
		return
	}
	t.Fatal("trust volume missing")
}
