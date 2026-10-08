package bundle

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestCurrentActivationRejectsMissingAndChangedInstallationIdentity(t *testing.T) {
	b := readBusinessTest(t, validBusinessDocument())
	p := importProfile()
	for _, raw := range []string{`{"metadata":{"uid":"old-namespace","labels":{}}}`, `{"metadata":{"uid":"current","labels":{"ops.platform.io/managed-by":"opsctl-bootstrap","ops.platform.io/installation-id":"invalid"}}}`} {
		calls := 0
		_, err := currentNamespaceIdentity(t.Context(), p, b, func(context.Context, string, ...string) ([]byte, error) { calls++; return []byte(raw), nil })
		if err == nil || calls != 1 {
			t.Fatalf("namespace identity accepted or extra mutations: err=%v calls=%d", err, calls)
		}
	}
	ns := `{"metadata":{"uid":"current-namespace","labels":{"ops.platform.io/managed-by":"opsctl-bootstrap","ops.platform.io/installation-id":"018f0f2b-91c2-7d42-a8dc-f719c5987204"}}}`
	m := Manifest{BundleID: "current", Payload: Payload{Digest: "sha256:current"}}
	receipt := currentStageReceipt{Stage: "dependencies-ready", BundleID: m.BundleID, PayloadDigest: m.Payload.Digest, ProfileDigest: jsonDigest(p), BusinessDigest: jsonDigest(b.values), NamespaceUID: "old-namespace", InstallationID: "018f0f2b-91c2-7d42-a8dc-f719c5987204"}
	encoded, _ := json.Marshal(receipt)
	checkpoint, _ := json.Marshal(map[string]any{"metadata": map[string]any{"labels": map[string]string{"ops.platform.io/installation-id": receipt.InstallationID}}, "data": map[string]string{"receipt.json": string(encoded)}})
	calls := 0
	err := verifyCurrentStageReceipt(t.Context(), m, p, b, func(_ context.Context, program string, args ...string) ([]byte, error) {
		calls++
		if program != "kubectl" {
			t.Fatal("changed namespace reached deployment")
		}
		if strings.Contains(strings.Join(args, " "), "get namespace") {
			return []byte(ns), nil
		}
		return checkpoint, nil
	})
	if err == nil || calls != 2 {
		t.Fatalf("old namespace checkpoint accepted: err=%v calls=%d", err, calls)
	}
}
func TestBusinessActivationMayReuseOnlyOwnedBootstrapPolicies(t *testing.T) {
	rendered := []byte("kind: NetworkPolicy\nmetadata: {name: ops-default-deny, labels: {ops.platform.io/release: ops-platform}}\n")
	own := `{"metadata":{"uid":"current-policy","labels":{"ops.platform.io/release":"ops-platform","app.kubernetes.io/managed-by":"Helm"},"annotations":{"meta.helm.sh/release-name":"ops-platform","meta.helm.sh/release-namespace":"ops-system"}}}`
	if err := validateRenderedChartStage(t.Context(), "ops-system", rendered, nil, importProfile(), "ops-platform", func(context.Context, string, ...string) ([]byte, error) { return []byte(own), nil }, true); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{strings.ReplaceAll(own, "current-policy", ""), strings.ReplaceAll(own, "ops-system", "foreign"), strings.ReplaceAll(own, "Helm", "foreign")} {
		if err := validateRenderedChartStage(t.Context(), "ops-system", rendered, nil, importProfile(), "ops-platform", func(context.Context, string, ...string) ([]byte, error) { return []byte(raw), nil }, true); err == nil {
			t.Fatal("foreign policy adopted")
		}
	}
	deployment := []byte("kind: Deployment\nmetadata: {name: ops-api, labels: {ops.platform.io/release: ops-platform}}\n")
	if err := validateRenderedChartStage(t.Context(), "ops-system", deployment, nil, importProfile(), "ops-platform", func(context.Context, string, ...string) ([]byte, error) { return []byte(own), nil }, true); err == nil {
		t.Fatal("old workload adopted through the policy exception")
	}
}

func TestActivationMayReuseOnlyCheckpointAPIBootstrapUIDs(t *testing.T) {
	rendered := []byte("kind: Deployment\nmetadata: {name: ops-api, labels: {ops.platform.io/release: ops-platform}}\n")
	own := `{"metadata":{"uid":"bootstrap-api-uid","labels":{"ops.platform.io/release":"ops-platform","app.kubernetes.io/managed-by":"Helm"},"annotations":{"meta.helm.sh/release-name":"ops-platform","meta.helm.sh/release-namespace":"ops-system"}}}`
	recorded := map[string]string{"Deployment/ops-api": "bootstrap-api-uid"}
	run := func(context.Context, string, ...string) ([]byte, error) { return []byte(own), nil }
	if err := validateRenderedChartStage(t.Context(), "ops-system", rendered, nil, importProfile(), "ops-platform", run, true, recorded); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{strings.ReplaceAll(own, "bootstrap-api-uid", "recreated-api-uid"), strings.ReplaceAll(own, "Helm", "foreign")} {
		if err := validateRenderedChartStage(t.Context(), "ops-system", rendered, nil, importProfile(), "ops-platform", func(context.Context, string, ...string) ([]byte, error) { return []byte(raw), nil }, true, recorded); err == nil {
			t.Fatal("changed bootstrap UID/owner adopted")
		}
	}
	worker := []byte("kind: Deployment\nmetadata: {name: ops-worker, labels: {ops.platform.io/release: ops-platform}}\n")
	if err := validateRenderedChartStage(t.Context(), "ops-system", worker, nil, importProfile(), "ops-platform", run, true, recorded); err == nil {
		t.Fatal("unrecorded existing Worker adopted")
	}
}

func TestBusinessActivationRequiresAPIProofBeforeClusterAccess(t *testing.T) {
	b := readBusinessTest(t, validBusinessDocument())
	err := verifyCurrentAPIBootstrapReceipt(t.Context(), Manifest{}, importProfile(), &b, func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("unverified registration reached cluster")
		return nil, nil
	})
	if err == nil || !strings.Contains(err.Error(), "source registration verification required") {
		t.Fatalf("unverified activation accepted: %v", err)
	}
}

func TestInstallationCheckpointAllowsFreshProbeWindowButNeverScopeExpansion(t *testing.T) {
	first := readBusinessTest(t, validBusinessDocument())
	raw, _ := json.Marshal(first.values)
	var second map[string]any
	json.Unmarshal(raw, &second)
	source := object(second, "sp04")["sources"].([]any)[0].(map[string]any)
	window := object(source, "ScopeProbe")
	from, _ := time.Parse(time.RFC3339Nano, textValue(window, "from"))
	to, _ := time.Parse(time.RFC3339Nano, textValue(window, "to"))
	window["from"] = from.Add(10 * time.Second).Format(time.RFC3339Nano)
	window["to"] = to.Add(10 * time.Second).Format(time.RFC3339Nano)
	refreshed := readBusinessTest(t, second)
	if businessInstallationDigest(first) != businessInstallationDigest(refreshed) {
		t.Fatal("fresh admissible source probe window stranded the dependency checkpoint")
	}
	mapping := object(object(source, "Binding"), "ScopeMapping")
	object(mapping, "scopes")["namespace"] = []string{"apps", "foreign"}
	expanded := readBusinessTest(t, second)
	if businessInstallationDigest(first) == businessInstallationDigest(expanded) {
		t.Fatal("checkpoint accepted source scope expansion")
	}
}
