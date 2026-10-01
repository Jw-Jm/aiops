package contract

import (
	"os/exec"
	"strings"
	"testing"
)

func TestBundledArchiveTLSHasNoPlaintextListener(t *testing.T) {
	output, err := exec.CommandContext(t.Context(), "helm", "template", "review", "../../deploy/charts/ops-dependencies").CombinedOutput()
	if err != nil {
		t.Fatalf("render: %v %s", err, output)
	}
	var archive string
	for _, doc := range strings.Split(string(output), "---") {
		if strings.Contains(doc, "name: ops-seaweedfs\n") || strings.Contains(doc, "name: ops-seaweedfs-s3\n") {
			archive += doc
		}
	}
	for _, required := range []string{"appProtocol: https", "-s3.cert.file=/etc/archive-tls/tls.crt", "-s3.key.file=/etc/archive-tls/tls.key", "secretName: \"ops-seaweedfs-tls\"", "mountPath: /etc/archive-tls"} {
		if !strings.Contains(archive, required) {
			t.Errorf("archive TLS missing %s", required)
		}
	}
	// With 4.47's cert/key flags, the existing port serves TLS only. A separate
	// portHttps would leave that existing port in plaintext and is forbidden.
	if strings.Contains(archive, "-s3.port.https") || strings.Contains(archive, "-s3.portHttps") {
		t.Fatal("separate HTTPS port leaves plaintext enabled")
	}
	if out, err := exec.CommandContext(t.Context(), "helm", "template", "review", "../../deploy/charts/ops-dependencies", "--set", "components.seaweedfs.endpoint=http://ops-seaweedfs-s3.ops-system.svc:8333").CombinedOutput(); err == nil || !strings.Contains(string(out), "bundled Archive requires HTTPS") {
		t.Fatal("plaintext bundled archive was accepted")
	}
}
