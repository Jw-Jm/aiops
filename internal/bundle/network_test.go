package bundle

import (
	"context"
	"reflect"
	"testing"

	"ops-platform/internal/profile"
)

func TestExternalServicePolicyUsesObservedSelectorAndTargetPort(t *testing.T) {
	p := importProfile()
	p.Components["victoriaMetrics"] = profile.ResolvedComponent{Mode: "external", ObjectUID: "original-service", Namespace: "monitoring", Name: "vmsingle-vm", Endpoint: "http://vmsingle-vm.monitoring.svc.cluster.local:8429"}
	run := func(_ context.Context, program string, args ...string) ([]byte, error) {
		want := []string{"--context", "orbstack", "-n", "monitoring", "get", "service", "vmsingle-vm", "-o", "json"}
		if program != "kubectl" || !reflect.DeepEqual(args, want) {
			t.Fatalf("unexpected service discovery: %s %v", program, args)
		}
		return []byte(`{"metadata":{"uid":"original-service","namespace":"monitoring","name":"vmsingle-vm"},"spec":{"selector":{"app":"vmsingle"},"ports":[{"port":8429,"targetPort":"http","protocol":"TCP"}]}}`), nil
	}
	rules, err := externalServiceEgress(t.Context(), p, run)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 || rules[0]["namespace"] != "monitoring" || rules[0]["port"] != "http" || !reflect.DeepEqual(rules[0]["podLabels"], map[string]string{"app": "vmsingle"}) {
		t.Fatalf("external rule = %#v", rules)
	}
}

func TestExternalServicePolicyRejectsPublicAndSelectorlessServices(t *testing.T) {
	p := importProfile()
	p.Components["victoriaMetrics"] = profile.ResolvedComponent{Mode: "external", Endpoint: "https://example.com:443"}
	calls := 0
	run := func(context.Context, string, ...string) ([]byte, error) {
		calls++
		return []byte(`{"spec":{"selector":{},"ports":[{"port":8429,"targetPort":8428}]}}`), nil
	}
	if _, err := externalServiceEgress(t.Context(), p, run); err == nil || calls != 0 {
		t.Fatalf("public endpoint error=%v calls=%d", err, calls)
	}
	p.Components["victoriaMetrics"] = profile.ResolvedComponent{Mode: "external", ObjectUID: "test-service", Namespace: "monitoring", Name: "vm", Endpoint: "http://vm.monitoring.svc:8429"}
	if _, err := externalServiceEgress(t.Context(), p, run); err == nil || calls != 1 {
		t.Fatalf("selectorless endpoint error=%v calls=%d", err, calls)
	}
}

func TestExternalServicePolicyRejectsRecreatedInternalService(t *testing.T) {
	p := importProfile()
	p.Components["openbao"] = profile.ResolvedComponent{Mode: "external", ObjectUID: "original-service", Namespace: "ops-system", Name: "bao", Endpoint: "https://bao.ops-system.svc:8200"}
	run := func(context.Context, string, ...string) ([]byte, error) {
		return []byte(`{"metadata":{"uid":"replacement-service","namespace":"ops-system","name":"bao"}}`), nil
	}
	if _, err := externalServiceEgress(t.Context(), p, run); err == nil {
		t.Fatal("internal namespace bypassed Service UID lock")
	}
}
