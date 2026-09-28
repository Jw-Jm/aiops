package profile

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverUsesCatalogVersionForDigestOnlyImage(t *testing.T) {
	const digest = "sha256:4ca9310dd2a50c746d4227f44058088ee0470a8470031ee3f09cc8b1a69dd7f6"
	root := t.TempDir()
	catalogPath := filepath.Join(root, "bundle", "component-catalog.yaml")
	if err := os.MkdirAll(filepath.Dir(catalogPath), 0o755); err != nil {
		t.Fatal(err)
	}
	catalog := "schemaVersion: 1\ncomponents:\n  - name: openbao\n    state: candidate\n    version: 2.7.0\n    digest: " + digest + "\n    architectures: [linux/arm64]\n"
	if err := os.WriteFile(catalogPath, []byte(catalog), 0o600); err != nil {
		t.Fatal(err)
	}

	kubectl := `#!/bin/sh
case "$4" in
  --raw=/version) printf '%s\n' '{"gitVersion":"v1.35.6+orb1","platform":"linux/arm64"}' ;;
  nodes) printf '%s\n' '{"items":[{"metadata":{"name":"orbstack"},"status":{"nodeInfo":{"architecture":"arm64"}}}]}' ;;
  namespaces) printf '%s\n' '{"items":[{"metadata":{"name":"kube-system","uid":"test-cluster"}}]}' ;;
  storageclasses) printf '%s\n' '{"items":[{"metadata":{"name":"local-path","annotations":{"storageclass.kubernetes.io/is-default-class":"true"}}}]}' ;;
  crd) printf '%s\n' '{"items":[]}' ;;
  deployments,statefulsets,daemonsets,pods,services) cat <<'JSON'
{"items":[
  {"kind":"Service","metadata":{"name":"ops-openbao","namespace":"ops-system","labels":{"app.kubernetes.io/name":"ops-openbao"}},"spec":{"selector":{"app.kubernetes.io/name":"ops-openbao"},"ports":[{"port":8200}]}},
  {"kind":"Pod","metadata":{"name":"ops-openbao-0","namespace":"ops-system","labels":{"app.kubernetes.io/name":"ops-openbao"}},"spec":{"containers":[{"name":"openbao","image":"ghcr.io/openbao/openbao@DIGEST"}]},"status":{"phase":"Running","containerStatuses":[{"name":"openbao","image":"ghcr.io/openbao/openbao@DIGEST","imageID":"docker-pullable://ghcr.io/openbao/openbao@DIGEST","ready":true}]}}
]}
JSON
    ;;
  *) printf '%s\n' '{"items":[]}' ;;
esac
`
	kubectl = strings.ReplaceAll(kubectl, "DIGEST", digest)
	kubectlPath := filepath.Join(root, "kubectl")
	if err := os.WriteFile(kubectlPath, []byte(kubectl), 0o700); err != nil {
		t.Fatal(err)
	}

	discovery, err := Discover(context.Background(), "orbstack", kubectlPath, catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	candidates := discovery.Components["openbao"]
	if len(candidates) != 1 {
		t.Fatalf("openbao candidates = %d, want exactly one: %#v", len(candidates), candidates)
	}
	candidate := candidates[0]
	if candidate.Version != "2.7.0" || candidate.Digest != digest || candidate.Image != "ghcr.io/openbao/openbao@"+digest {
		t.Fatalf("digest-only candidate lock = %#v; want exact catalog version and image digest", candidate)
	}
	if !candidate.Compatible {
		t.Fatalf("digest-only image matching the exact catalog lock must be compatible: %#v", candidate)
	}

	input, err := ReadProfileFile(filepath.Join("..", "..", "deploy", "profiles", "dev-orbstack.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	detected, err := Detect(input, discovery)
	if err != nil {
		t.Fatal(err)
	}
	if openbao := detected.Components["openbao"]; openbao.Mode != "external" || openbao.Version != "2.7.0" || openbao.Digest != digest {
		t.Fatalf("OpenBao profile resolution = %#v; want exact external catalog lock", openbao)
	}

	mismatchedCatalog := strings.Replace(catalog, digest, "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 1)
	if err := os.WriteFile(catalogPath, []byte(mismatchedCatalog), 0o600); err != nil {
		t.Fatal(err)
	}
	mismatched, err := Discover(context.Background(), "orbstack", kubectlPath, catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := mismatched.Components["openbao"][0]; got.Version != "" || got.Compatible {
		t.Fatalf("mismatched digest was assigned catalog version: %#v", got)
	}
}
