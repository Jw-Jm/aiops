package contract

import (
	"os/exec"
	"strings"
	"testing"
)

func TestBundledKeycloakUsesIndependentTLSSecret(t *testing.T) {
	output, err := exec.CommandContext(t.Context(), "helm", "template", "review", "../../deploy/charts/ops-dependencies").CombinedOutput()
	if err != nil {
		t.Fatalf("render: %v %s", err, output)
	}
	var deployment string
	for _, doc := range strings.Split(string(output), "---") {
		if strings.Contains(doc, "kind: Deployment") && strings.Contains(doc, "name: ops-keycloak\n") {
			deployment = doc
		}
	}
	for _, required := range []string{"name: https", "containerPort: 8443", "name: KC_HTTP_ENABLED\n              value: \"false\"", "name: KC_HTTPS_CERTIFICATE_FILE", "name: KC_HTTPS_CERTIFICATE_KEY_FILE", "secretName: \"ops-keycloak-tls\"", "mountPath: /etc/keycloak/tls"} {
		if !strings.Contains(deployment, required) {
			t.Errorf("Keycloak TLS missing %s", required)
		}
	}
}
