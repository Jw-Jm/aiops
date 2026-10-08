package bundle

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	"ops-platform/internal/profile"
)

func TestLockedVictoriaChartsRenderForOfflineInstaller(t *testing.T) {
	entries := []struct{ name, version, digest, archive string }{
		{"victoria-metrics", "v1.116.0", "b10c78f4bd9b52554b7f863ff416e480d931b1811f591049d166eea1fb247638", "victoria-metrics-single-0.18.0.tgz"},
		{"victoria-logs", "v1.52.0", "47b820890d64c4575a2a0a46415dcd8a4fd59a0f1fcd6a377693d7aea639442e", "victoria-logs-single-0.13.9.tgz"},
		{"vmalert", "v1.116.0", "48e01bd36d098b9c8a1537d38235e0194013e38853196b446cb0eb1f17057311", "victoria-metrics-alert-0.18.0.tgz"},
	}
	for _, entry := range entries {
		t.Run(entry.name, func(t *testing.T) {
			image := "docker.io/victoriametrics/" + entry.name + "@sha256:" + entry.digest
			component := profile.ResolvedComponent{Image: image, Version: entry.version}
			p := importProfile()
			p.Kubernetes.StorageClass = "operator-selected-storage"
			p.Components["victoriaMetrics"] = profile.ResolvedComponent{Endpoint: "http://existing.monitoring.svc.cluster.local:8429"}
			values, err := victoriaChartValues(entry.name, component, p)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := yaml.Marshal(values)
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(t.TempDir(), "values.yaml")
			if err := os.WriteFile(file, encoded, 0600); err != nil {
				t.Fatal(err)
			}
			chart := filepath.Join("..", "..", "deploy", "addons", "victoria", "charts", entry.archive)
			raw, err := exec.Command("helm", "template", entry.name, chart, "--namespace", "ops-system", "--values", file).CombinedOutput()
			if err != nil {
				t.Fatalf("render actual pinned Chart: %v\n%s", err, raw)
			}
			rendered, err := RenderOwnedChart(bytes.NewReader(raw), entry.name, entry.name, image, entry.version)
			if err != nil {
				t.Fatal(err)
			}
			run := func(context.Context, string, ...string) ([]byte, error) { return nil, nil }
			if err := validateRenderedChart(t.Context(), "ops-system", rendered, map[string]bool{image: true}, p, entry.name, run); err != nil {
				t.Fatal(err)
			}
			decoder := yaml.NewDecoder(bytes.NewReader(rendered))
			for {
				var object struct {
					Kind string `yaml:"kind"`
				}
				if err := decoder.Decode(&object); err == io.EOF {
					break
				} else if err != nil {
					t.Fatal(err)
				}
				if object.Kind == "PersistentVolumeClaim" {
					t.Fatal("PVCs must remain StatefulSet claim templates, outside release cleanup")
				}
			}
			if !strings.Contains(string(rendered), "automountServiceAccountToken: false") {
				t.Fatal("token automount was not disabled")
			}
			if entry.name != "vmalert" && !strings.Contains(string(rendered), "storageClassName: operator-selected-storage") {
				t.Fatal("locked upstream Chart ignored resolved StorageClass")
			}
		})
	}
}

func TestOwnedRendererRejectsUnplannedImagesAndExistingOwner(t *testing.T) {
	image := "example.invalid/vmalert@sha256:" + strings.Repeat("a", 64)
	for _, input := range []string{
		"kind: Deployment\nmetadata: {name: fixture}\nspec: {template: {metadata: {}, spec: {containers: [{image: example.invalid/vmalert:latest, imagePullPolicy: Always}]}}}\n",
		"kind: ConfigMap\nmetadata: {name: fixture, labels: {ops.platform.io/release: user-release}}\n",
		"kind: PersistentVolumeClaim\nmetadata: {name: user-data}\n",
		"kind: Deployment\nmetadata: {name: fixture}\nspec: {volumeClaimTemplates: [wrong-shape]}\n",
	} {
		if output, err := RenderOwnedChart(strings.NewReader(input), "vmalert", "vmalert", image, "v1.116.0"); err == nil || len(output) != 0 {
			t.Fatalf("unsafe input yielded output=%q error=%v", output, err)
		}
	}
	if _, err := victoriaChartValues("vmalert", profile.ResolvedComponent{Image: "bad", Version: "latest"}, importProfile()); err == nil {
		t.Fatal("invalid Chart pin accepted")
	}
}

func TestOwnedRendererRefusesCacheFallbackToRegistry(t *testing.T) {
	image := "example.invalid/vmalert@sha256:" + strings.Repeat("a", 64)
	tagged, err := taggedDigest(image, "v1.116.0")
	if err != nil {
		t.Fatal(err)
	}
	input := "kind: Deployment\nmetadata: {name: fixture}\nspec: {template: {metadata: {}, spec: {containers: [{image: " + tagged + ", imagePullPolicy: IfNotPresent}]}}}\n"
	if out, err := RenderOwnedChart(strings.NewReader(input), "vmalert", "vmalert", image, "v1.116.0"); err == nil || len(out) != 0 {
		t.Fatalf("cache fallback accepted: output=%q error=%v", out, err)
	}
}
