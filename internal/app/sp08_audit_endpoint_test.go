package app

import "testing"

func TestSP08AuditServiceRouteRemainsSeparateFromGraphOwnerCIDRs(t *testing.T) {
	c := SP04Config{Namespace: "test-platform", AllowedWorkerCIDRs: []string{"10.244.0.0/24"}}
	good := "https://ops-worker.test-platform.svc.cluster.local:8082"
	if !c.auditEndpointAllowed(good) || c.endpointAllowed(good) {
		t.Fatal("dedicated audit service must not broaden graph owner routes")
	}
	for _, bad := range []string{"http://ops-worker.test-platform.svc.cluster.local:8082", "https://ops-worker.foreign.svc.cluster.local:8082", "https://user@ops-worker.test-platform.svc.cluster.local:8082", "https://ops-worker.test-platform.svc.cluster.local:8082/path", "https://ops-worker.test-platform.svc.cluster.local:8082?redirect=1", "https://10.244.0.5:8082", "https://ops-worker.test-platform.svc.cluster.local:443"} {
		if c.auditEndpointAllowed(bad) {
			t.Fatalf("unexpected audit route: %s", bad)
		}
	}
}
