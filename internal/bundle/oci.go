package bundle

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

type ociDescriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
	Platform  struct {
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
	} `json:"platform"`
	Annotations map[string]string `json:"annotations"`
}

// OCI content is validated without extracting it. This catches a missing or
// altered layer even when the signed outer archive itself is authentic.
func validateOCIArchive(file, architecture string) (string, string, error) {
	f, err := os.Open(file)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	tr := tar.NewReader(f)
	blobs := map[string]int64{}
	metadata := map[string][]byte{}
	names := map[string]bool{}
	count := 0
	metadataBytes := int64(0)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", "", fmt.Errorf("read OCI tar: %w", err)
		}
		count++
		if count > 100000 {
			return "", "", errors.New("OCI archive has too many entries")
		}
		name := strings.TrimSuffix(h.Name, "/")
		if name == "." && h.Typeflag == tar.TypeDir {
			continue
		}
		if path.Clean(name) != name || strings.HasPrefix(name, "/") || strings.Contains(name, "..") || strings.ContainsAny(name, "\\\x00") {
			return "", "", errors.New("unsafe OCI archive path")
		}
		if h.Typeflag == tar.TypeDir {
			if name != "blobs" && name != "blobs/sha256" {
				return "", "", errors.New("unexpected OCI directory")
			}
			continue
		}
		if h.Typeflag != tar.TypeReg || names[name] {
			return "", "", errors.New("OCI links, special files and duplicate entries are forbidden")
		}
		names[name] = true
		if name == "index.json" || name == "oci-layout" {
			if h.Size > 16<<20 {
				return "", "", errors.New("OCI metadata too large")
			}
			data, err := io.ReadAll(tr)
			if err != nil {
				return "", "", err
			}
			metadata[name] = data
			continue
		}
		if !strings.HasPrefix(name, "blobs/sha256/") || !isSHA256("sha256:"+strings.TrimPrefix(name, "blobs/sha256/")) {
			return "", "", fmt.Errorf("unexpected OCI entry %s", name)
		}
		hash := sha256.New()
		var small []byte
		var size int64
		if h.Size <= 16<<20 {
			small, err = io.ReadAll(tr)
			size = int64(len(small))
			_, _ = hash.Write(small)
		} else {
			size, err = io.Copy(hash, tr)
		}
		if err != nil {
			return "", "", err
		}
		digest := "sha256:" + hex.EncodeToString(hash.Sum(nil))
		if digest != "sha256:"+strings.TrimPrefix(name, "blobs/sha256/") || size != h.Size {
			return "", "", errors.New("OCI blob digest or size mismatch")
		}
		blobs[digest] = size
		if small != nil && json.Valid(small) {
			metadataBytes += int64(len(small))
			if metadataBytes > 64<<20 {
				return "", "", errors.New("OCI metadata exceeds 64 MiB")
			}
			metadata[digest] = small
		}
	}
	var layout struct {
		Version string `json:"imageLayoutVersion"`
	}
	if json.Unmarshal(metadata["oci-layout"], &layout) != nil || layout.Version != "1.0.0" {
		return "", "", errors.New("OCI layout must be 1.0.0")
	}
	var index struct {
		SchemaVersion int             `json:"schemaVersion"`
		Manifests     []ociDescriptor `json:"manifests"`
	}
	if json.Unmarshal(metadata["index.json"], &index) != nil || index.SchemaVersion != 2 || len(index.Manifests) != 1 {
		return "", "", errors.New("OCI index must contain one exact platform manifest")
	}
	root := index.Manifests[0]
	selected := root
	// Preserve an upstream multi-platform digest without rewriting its index.
	// The Bundle contains only the declared platform's complete config/layers.
	// Other platform descriptors remain authenticated metadata, not installed
	// images. Exactly one matching child is required, with no recursive indexes.
	var upstreamIndex struct {
		SchemaVersion int             `json:"schemaVersion"`
		Manifests     []ociDescriptor `json:"manifests"`
	}
	if json.Unmarshal(metadata[root.Digest], &upstreamIndex) == nil && len(upstreamIndex.Manifests) > 0 {
		if upstreamIndex.SchemaVersion != 2 {
			return "", "", errors.New("invalid OCI upstream index")
		}
		matched := 0
		for _, child := range upstreamIndex.Manifests {
			if child.Platform.OS+"/"+child.Platform.Architecture == architecture {
				selected = child
				matched++
			}
		}
		if matched != 1 {
			return "", "", errors.New("OCI upstream index must select exactly one Bundle platform")
		}
	}
	var manifest struct {
		SchemaVersion int             `json:"schemaVersion"`
		Config        ociDescriptor   `json:"config"`
		Layers        []ociDescriptor `json:"layers"`
	}
	if !isSHA256(root.Digest) || blobs[root.Digest] != root.Size || root.Size == 0 {
		return "", "", errors.New("OCI image manifest missing or has incorrect size")
	}
	if !isSHA256(selected.Digest) || selected.Size < 1 || blobs[selected.Digest] != selected.Size {
		return "", "", errors.New("OCI selected platform manifest missing or has incorrect size")
	}
	if json.Unmarshal(metadata[selected.Digest], &manifest) != nil || manifest.SchemaVersion != 2 || len(manifest.Layers) == 0 {
		return "", "", errors.New("invalid OCI image manifest")
	}
	for _, d := range append([]ociDescriptor{manifest.Config}, manifest.Layers...) {
		if !isSHA256(d.Digest) || d.Size < 1 || blobs[d.Digest] != d.Size {
			return "", "", fmt.Errorf("OCI missing or invalid config/layer %s", d.Digest)
		}
	}
	var config struct {
		OS           string `json:"os"`
		Architecture string `json:"architecture"`
	}
	if json.Unmarshal(metadata[manifest.Config.Digest], &config) != nil || config.OS+"/"+config.Architecture != architecture {
		return "", "", errors.New("OCI image architecture differs from Bundle")
	}
	if root.Platform.OS != "" && root.Platform.OS+"/"+root.Platform.Architecture != architecture {
		return "", "", errors.New("OCI descriptor architecture differs from Bundle")
	}
	return root.Digest, root.Annotations["org.opencontainers.image.ref.name"], nil
}
