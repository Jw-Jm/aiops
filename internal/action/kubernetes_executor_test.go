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
	k := KubernetesExecutor{RunnerNamespace: "ops-runners", APIEndpoint: "https://ops-api:8445", TrustConfigMap: "runner-trust"}
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
