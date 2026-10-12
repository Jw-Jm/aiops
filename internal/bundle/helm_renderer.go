package bundle

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

// RenderOwnedChart adapts a pinned upstream Chart without changing its archive.
// Only the planned tagged digest is accepted; it becomes the exact OCI reference.
// Ownership labels are added before both preflight and Helm installation.
func RenderOwnedChart(input io.Reader, release, component, image, version string) ([]byte, error) {
	if component == "platform" {
		if release != "ops-platform" || image != "" || version != "" {
			return nil, errors.New("platform ownership renderer requires its fixed release and no image rewriting")
		}
		return renderPlatformOwnership(input)
	}
	if !releaseNamePattern.MatchString(release) || (component != "victoria-metrics" && component != "victoria-logs" && component != "vmalert") {
		return nil, errors.New("invalid upstream Chart release or component")
	}
	expected, err := taggedDigest(image, version)
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(input, (16<<20)+1))
	if err != nil {
		return nil, fmt.Errorf("read rendered Chart: %w", err)
	}
	if len(raw) > 16<<20 {
		return nil, errors.New("rendered Chart exceeds the 16 MiB input limit")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	var output bytes.Buffer
	seen, images := 0, 0
	for {
		var object map[string]any
		if err := decoder.Decode(&object); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		if len(object) == 0 {
			continue
		}
		seen++
		kind, _ := object["kind"].(string)
		if !offlineResourceKinds[kind] {
			return nil, fmt.Errorf("unsupported upstream Chart resource %s", kind)
		}
		if err := labelOwnedMetadata(object, release, component); err != nil {
			return nil, err
		}
		if spec, ok := object["spec"].(map[string]any); ok {
			if template, ok := spec["template"].(map[string]any); ok {
				if err := labelOwnedMetadata(template, release, component); err != nil {
					return nil, err
				}
				if podSpec, ok := template["spec"].(map[string]any); ok {
					podSpec["automountServiceAccountToken"] = false
				}
			}
			if claims, ok := spec["volumeClaimTemplates"].([]any); ok {
				for _, claim := range claims {
					metadata, ok := claim.(map[string]any)
					if !ok {
						return nil, errors.New("invalid upstream volume claim template")
					}
					if err := labelOwnedMetadata(metadata, release, component); err != nil {
						return nil, err
					}
				}
			}
		}
		var rewrite func(any) error
		rewrite = func(value any) error {
			switch v := value.(type) {
			case map[string]any:
				if ref, ok := v["image"].(string); ok {
					if ref != expected || v["imagePullPolicy"] != "Never" {
						return fmt.Errorf("upstream Chart image %q differs from the planned tagged digest or pull policy", ref)
					}
					v["image"] = image
					images++
				}
				for _, child := range v {
					if err := rewrite(child); err != nil {
						return err
					}
				}
			case []any:
				for _, child := range v {
					if err := rewrite(child); err != nil {
						return err
					}
				}
			}
			return nil
		}
		if err := rewrite(object); err != nil {
			return nil, err
		}
		encoded, err := yaml.Marshal(object)
		if err != nil {
			return nil, err
		}
		output.WriteString("---\n")
		output.Write(encoded)
	}
	if seen == 0 || images != 1 {
		return nil, errors.New("upstream Chart must render resources with exactly one planned image")
	}
	return output.Bytes(), nil
}

// renderPlatformOwnership only supplies a missing release label. It preserves
// workload selectors, components, images and all policy/RBAC content.
func renderPlatformOwnership(input io.Reader) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(input, (16<<20)+1))
	if err != nil || len(raw) > 16<<20 {
		return nil, errors.New("invalid or oversized platform Chart")
	}
	d := yaml.NewDecoder(bytes.NewReader(raw))
	var out bytes.Buffer
	seen := 0
	for {
		var v map[string]any
		if err := d.Decode(&v); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		if len(v) == 0 {
			continue
		}
		kind, _ := v["kind"].(string)
		if !offlineResourceKinds[kind] {
			return nil, fmt.Errorf("unsupported platform resource %s", kind)
		}
		m, ok := v["metadata"].(map[string]any)
		if !ok {
			return nil, errors.New("platform resource lacks metadata")
		}
		labels, _ := m["labels"].(map[string]any)
		if labels == nil {
			labels = map[string]any{}
			m["labels"] = labels
		}
		if owner, exists := labels["ops.platform.io/release"]; exists && owner != "ops-platform" {
			return nil, errors.New("platform resource claims another release")
		}
		labels["ops.platform.io/release"] = "ops-platform"
		b, err := yaml.Marshal(v)
		if err != nil {
			return nil, err
		}
		out.WriteString("---\n")
		out.Write(b)
		seen++
	}
	if seen == 0 {
		return nil, errors.New("empty platform Chart")
	}
	return out.Bytes(), nil
}

func taggedDigest(image, version string) (string, error) {
	repository, digest, ok := strings.Cut(image, "@")
	if !ok || repository == "" || strings.ContainsAny(repository, " \t\r\n") || !isSHA256(digest) || !isExactBundleVersion(version) {
		return "", errors.New("Chart requires an exact version and immutable OCI image")
	}
	if at := strings.LastIndex(repository, ":"); at > strings.LastIndex(repository, "/") {
		repository = repository[:at]
	}
	return repository + ":" + version + "@" + digest, nil
}

func labelOwnedMetadata(object map[string]any, release, component string) error {
	metadata, ok := object["metadata"].(map[string]any)
	if !ok {
		metadata = map[string]any{}
		object["metadata"] = metadata
	}
	labels, ok := metadata["labels"].(map[string]any)
	if !ok {
		labels = map[string]any{}
		metadata["labels"] = labels
	}
	if current, ok := labels["ops.platform.io/release"]; ok && current != release {
		return errors.New("upstream resource claims another release owner")
	}
	labels["ops.platform.io/release"] = release
	labels["ops.platform.io/component"] = component
	return nil
}
