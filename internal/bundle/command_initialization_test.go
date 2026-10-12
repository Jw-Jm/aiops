package bundle

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"ops-platform/internal/profile"
)

func TestCommandInitializationPreservesBaseConfigurationBinding(t *testing.T) {
	base := readBusinessTest(t, validBusinessDocument())
	expected := registeredBusinessInstallationDigest(base)
	raw, _ := json.Marshal(base.values)
	var extended map[string]any
	json.Unmarshal(raw, &extended)
	// Digest isolation does not authorize this synthetic object: production
	// construction still requires the complete installation Schema and API proof.
	extended["sp07"] = map[string]any{"enabled": true, "workloadCABundle": "native CA", "hosts": []any{}}
	b := BusinessValues{values: extended}
	if apiBootstrapBusinessDigest(b) == expected {
		t.Fatal("implicit SP07 change bypassed checkpoint")
	}
	b.initializeCommands = true
	if apiBootstrapBusinessDigest(b) != expected {
		t.Fatal("explicit first commands initialization changed base binding")
	}
	object(object(b.values, "sp06"), "model")["base_url"] = "http://192.168.1.2:11434/v1"
	if apiBootstrapBusinessDigest(b) == expected {
		t.Fatal("command initialization permitted unrelated model change")
	}
	if _, err := base.InitializeCommands(); err == nil {
		t.Fatal("missing SP07 input accepted")
	}
}

func TestCommandInitializationIntentRejectsChangedAndForeignCheckpoint(t *testing.T) {
	b := readBusinessTest(t, validBusinessDocument())
	b.values["sp07"] = map[string]any{"enabled": true}
	b.initializeCommands, b.registrationVerified, b.installStage = true, true, "business"
	var receipt string
	calls := 0
	foreign := false
	run := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "get namespace") {
			return []byte(`{"metadata":{"uid":"namespace-uid","labels":{"ops.platform.io/managed-by":"opsctl-bootstrap","ops.platform.io/installation-id":"018f0f2b-91c2-7d42-a8dc-f719c5987204"}}}`), nil
		}
		if strings.Contains(joined, "get configmap") {
			if receipt == "" {
				return nil, nil
			}
			managedBy := "opsctl-bootstrap"
			if foreign {
				managedBy = "foreign"
			}
			data, _ := json.Marshal(map[string]any{"metadata": map[string]any{"uid": "intent-uid", "labels": map[string]any{"ops.platform.io/managed-by": managedBy, "ops.platform.io/installation-id": "018f0f2b-91c2-7d42-a8dc-f719c5987204"}}, "data": map[string]any{"receipt.json": receipt}})
			return data, nil
		}
		calls++
		for _, arg := range args {
			if strings.HasPrefix(arg, "--from-literal=receipt.json=") {
				receipt = strings.TrimPrefix(arg, "--from-literal=receipt.json=")
			}
		}
		return nil, nil
	}
	m := Manifest{BundleID: "native-current"}
	if err := recordCommandInitialization(t.Context(), m, importProfile(), b, run); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || receipt == "" {
		t.Fatal("intent was not created and labeled")
	}
	if err := recordCommandInitialization(t.Context(), m, importProfile(), b, run); err != nil || calls != 2 {
		t.Fatal("same intent was not reused read-only", err)
	}
	m.BundleID = "changed"
	if err := recordCommandInitialization(t.Context(), m, importProfile(), b, run); err == nil || calls != 2 {
		t.Fatal("changed intent overwritten")
	}
	m.BundleID = "native-current"
	foreign = true
	if err := recordCommandInitialization(t.Context(), m, importProfile(), b, run); err == nil || calls != 2 {
		t.Fatal("foreign intent adopted")
	}
	b.registrationVerified = false
	if err := recordCommandInitialization(t.Context(), m, importProfile(), b, run); err == nil {
		t.Fatal("unverified API registration accepted")
	}
}

func TestCommandInitializationGraphLeaseBaseBinding(t *testing.T) {
	base := readBusinessTest(t, validBusinessDocument())
	expected, err := plannedGraphLeases(base)
	if err != nil {
		t.Fatal(err)
	}
	namespaceUID, installationID := "namespace-uid", "11111111-1111-4111-8111-111111111111"
	profile := profile.ResolvedProfile{Kubernetes: profile.KubernetesDiscovery{Context: "test"}}
	receipt := GraphLeaseBootstrapReceipt{NamespaceUID: namespaceUID, InstallationID: installationID, ProfileDigest: jsonDigest(profile), BusinessDigest: registeredBusinessInstallationDigest(base), Bindings: expected}
	for i := range receipt.Bindings {
		receipt.Bindings[i].UID = "lease-uid"
	}
	encoded, _ := json.Marshal(receipt)
	run := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		command := strings.Join(args, " ")
		var result any
		switch {
		case strings.Contains(command, "get namespace"):
			result = map[string]any{"metadata": map[string]any{"uid": namespaceUID, "labels": map[string]string{"ops.platform.io/managed-by": "opsctl-bootstrap", "ops.platform.io/installation-id": installationID}}}
		case strings.Contains(command, "get configmap"):
			result = map[string]any{"metadata": map[string]any{"labels": map[string]string{"ops.platform.io/installation-id": installationID}}, "data": map[string]string{"receipt.json": string(encoded)}}
		case strings.Contains(command, "get lease"):
			result = map[string]any{"metadata": map[string]any{"uid": "lease-uid", "labels": map[string]string{"ops.platform.io/managed-by": "opsctl-bootstrap", "ops.platform.io/installation-id": installationID}, "annotations": map[string]string{"ops.platform/owner-epoch": "0", "ops.platform.io/tenant": expected[0].Tenant, "ops.platform.io/cluster-uid": expected[0].ClusterUID}}}
		default:
			t.Fatalf("unexpected mutation/command: %s", command)
		}
		return json.Marshal(result)
	}
	raw, _ := json.Marshal(base.values)
	var values map[string]any
	json.Unmarshal(raw, &values)
	values["sp07"] = map[string]any{"enabled": true}
	extended := BusinessValues{values: values}
	if preflightCurrentGraphLeases(context.Background(), profile, extended, run) == nil {
		t.Fatal("implicit SP07 addition bypassed Lease binding")
	}
	extended.initializeCommands = true
	if err := preflightCurrentGraphLeases(context.Background(), profile, extended, run); err != nil {
		t.Fatal(err)
	}
	object(extended.values, "sp06")["policyName"] = "other-policy"
	if preflightCurrentGraphLeases(context.Background(), profile, extended, run) == nil {
		t.Fatal("changed base scope bypassed Lease binding")
	}
}
