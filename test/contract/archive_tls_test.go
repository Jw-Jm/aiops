package contract

import (
	"bytes"
	"io"
	"os/exec"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestArchiveReadinessWaitsForS3Listener(t *testing.T) {
	output, err := exec.CommandContext(t.Context(), "helm", "template", "review", "../../deploy/charts/ops-dependencies").CombinedOutput()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(output))
	for {
		var resource struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name string `yaml:"name"`
			} `yaml:"metadata"`
			Spec struct {
				Template struct {
					Spec struct {
						Containers []struct {
							ReadinessProbe struct {
								TCPSocket struct {
									Port string `yaml:"port"`
								} `yaml:"tcpSocket"`
								HTTPGet map[string]any `yaml:"httpGet"`
							} `yaml:"readinessProbe"`
						} `yaml:"containers"`
					} `yaml:"spec"`
				} `yaml:"template"`
			} `yaml:"spec"`
		}
		if err := decoder.Decode(&resource); err == io.EOF {
			t.Fatal("bundled Archive workload missing")
		} else if err != nil {
			t.Fatal(err)
		}
		if resource.Kind != "StatefulSet" || resource.Metadata.Name != "ops-seaweedfs" {
			continue
		}
		if len(resource.Spec.Template.Spec.Containers) != 1 {
			t.Fatal("unexpected Archive process layout")
		}
		probe := resource.Spec.Template.Spec.Containers[0].ReadinessProbe
		if probe.TCPSocket.Port != "s3" || len(probe.HTTPGet) != 0 {
			t.Fatal("Archive may become Ready before its S3 listener starts")
		}
		return
	}
}

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
