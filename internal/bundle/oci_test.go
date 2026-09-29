package bundle

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOCIOriginalIndexRequiresUnambiguousCompleteNativeClosure(t *testing.T) {
	for _, test := range []struct {
		name                                  string
		missingLayer, missingChild, ambiguous bool
	}{
		{"valid", false, false, false}, {"missing-native-layer", true, false, false},
		{"missing-native-manifest", false, true, false}, {"ambiguous-native-platform", false, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			content, childDigest := ociFixture(t, test.missingLayer, false)
			tr := tar.NewReader(bytes.NewReader(content))
			var entries []tarEntry
			var childSize int64
			for {
				h, err := tr.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				data, err := io.ReadAll(tr)
				if err != nil {
					t.Fatal(err)
				}
				if h.Name == "index.json" {
					continue
				}
				if h.Name == "blobs/sha256/"+strings.TrimPrefix(childDigest, "sha256:") {
					childSize = h.Size
					if test.missingChild {
						continue
					}
				}
				entries = append(entries, tarEntry{header: *h, body: data})
			}
			child := map[string]any{"digest": childDigest, "size": childSize, "platform": map[string]string{"os": "linux", "architecture": "arm64"}}
			children := []any{child, map[string]any{"digest": fixtureDigest("foreign"), "size": 123, "platform": map[string]string{"os": "linux", "architecture": "amd64"}}}
			if test.ambiguous {
				children = append(children, child)
			}
			upstream, _ := json.Marshal(map[string]any{"schemaVersion": 2, "manifests": children})
			digest := fixtureDigest(string(upstream))
			index, _ := json.Marshal(map[string]any{"schemaVersion": 2, "manifests": []any{map[string]any{"digest": digest, "size": len(upstream), "annotations": map[string]string{"org.opencontainers.image.ref.name": "local/pg@" + digest}}}})
			for name, data := range map[string][]byte{"index.json": index, "blobs/sha256/" + strings.TrimPrefix(digest, "sha256:"): upstream} {
				entries = append(entries, tarEntry{header: tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0600, Size: int64(len(data))}, body: data})
			}
			file := filepath.Join(t.TempDir(), "image.tar")
			if err := os.WriteFile(file, makeTar(t, entries), 0600); err != nil {
				t.Fatal(err)
			}
			got, ref, err := validateOCIArchive(file, "linux/arm64")
			if test.name == "valid" {
				if err != nil || got != digest || !strings.HasSuffix(ref, "@"+digest) {
					t.Fatalf("digest=%s ref=%s err=%v", got, ref, err)
				}
			} else if err == nil {
				t.Fatal("unsafe native closure accepted")
			}
		})
	}
}

func ociFixture(t *testing.T, missingLayer, corruptLayer bool) ([]byte, string) {
	t.Helper()
	config := []byte(`{"architecture":"arm64","os":"linux"}`)
	layer := []byte("fixture rootfs")
	configDigest, layerDigest := fixtureDigest(string(config)), fixtureDigest(string(layer))
	manifest, err := json.Marshal(map[string]any{
		"schemaVersion": 2,
		"config":        map[string]any{"digest": configDigest, "size": len(config)},
		"layers":        []any{map[string]any{"digest": layerDigest, "size": len(layer)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	manifestDigest := fixtureDigest(string(manifest))
	index, err := json.Marshal(map[string]any{"schemaVersion": 2, "manifests": []any{map[string]any{
		"digest": manifestDigest, "size": len(manifest),
		"annotations": map[string]string{"org.opencontainers.image.ref.name": "ops.local/platform-api@" + manifestDigest},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	entries := []tarEntry{}
	add := func(name string, content []byte) {
		entries = append(entries, tarEntry{header: tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0600, Size: int64(len(content))}, body: content})
	}
	add("oci-layout", []byte(`{"imageLayoutVersion":"1.0.0"}`))
	add("index.json", index)
	add("blobs/sha256/"+strings.TrimPrefix(manifestDigest, "sha256:"), manifest)
	add("blobs/sha256/"+strings.TrimPrefix(configDigest, "sha256:"), config)
	if !missingLayer {
		if corruptLayer {
			layer[0] ^= 1
		}
		add("blobs/sha256/"+strings.TrimPrefix(layerDigest, "sha256:"), layer)
	}
	return makeTar(t, entries), manifestDigest
}

func TestOCIValidatesLayerClosureAndArchitecture(t *testing.T) {
	for _, test := range []struct {
		name             string
		missing, corrupt bool
		architecture     string
		valid            bool
	}{
		{"valid", false, false, "linux/arm64", true},
		{"missing-layer", true, false, "linux/arm64", false},
		{"altered-layer", false, true, "linux/arm64", false},
		{"wrong-architecture", false, false, "linux/amd64", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			content, digest := ociFixture(t, test.missing, test.corrupt)
			file := filepath.Join(t.TempDir(), "image.tar")
			if err := os.WriteFile(file, content, 0600); err != nil {
				t.Fatal(err)
			}
			got, ref, err := validateOCIArchive(file, test.architecture)
			if test.valid && (err != nil || got != digest || !strings.HasSuffix(ref, "@"+digest)) {
				t.Fatalf("valid OCI: digest=%s ref=%s err=%v", got, ref, err)
			}
			if !test.valid && err == nil {
				t.Fatal("invalid OCI accepted")
			}
		})
	}
}
