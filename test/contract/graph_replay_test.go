package contract

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

type graphLockedFile struct {
	Path   string `yaml:"path"`
	SHA256 string `yaml:"sha256"`
}

type graphReplayLock struct {
	SchemaVersion int    `yaml:"schemaVersion"`
	Task          string `yaml:"task"`
	Adaptation    struct {
		Status  string          `yaml:"status"`
		Patch   graphLockedFile `yaml:"adapterPatch"`
		Test    graphLockedFile `yaml:"adapterTest"`
		Closure graphLockedFile `yaml:"dependencyClosure"`
	} `yaml:"adaptationPoC"`
	Sources map[string]struct {
		Status  string            `yaml:"status"`
		Commit  string            `yaml:"commit"`
		Archive string            `yaml:"gitArchiveSHA256"`
		Files   []graphLockedFile `yaml:"files"`
	} `yaml:"sources"`
	Replay struct {
		GoVersion string `yaml:"goVersion"`
		Image     string `yaml:"image"`
		Test      string `yaml:"test"`
	} `yaml:"nonVirtualReplay"`
}

func loadGraphReplayLock(t *testing.T) graphReplayLock {
	t.Helper()
	raw, err := os.ReadFile("../../docs/poc/graph-reuse-lock.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var lock graphReplayLock
	if err := yaml.Unmarshal(raw, &lock); err != nil {
		t.Fatal(err)
	}
	return lock
}

func TestGraphNonVirtualReplayIsLocked(t *testing.T) {
	lock := loadGraphReplayLock(t)
	if lock.SchemaVersion != 1 || lock.Task != "SP-02 Task 2.8 graph reuse baseline" || lock.Adaptation.Status != "passed-test-only" {
		t.Fatal("unexpected graph baseline scope")
	}
	if lock.Replay.GoVersion != "1.27.1" || !regexp.MustCompile(`^golang:1\.27\.1-alpine@sha256:[0-9a-f]{64}$`).MatchString(lock.Replay.Image) || lock.Replay.Test != "TestGraphNonVirtualUpstreamReplay" {
		t.Fatal("missing exact toolchain/image and executable nonvirtual replay entry point")
	}
	for _, name := range []string{"ariadne", "kubernetesOntology"} {
		source, ok := lock.Sources[name]
		if !ok || source.Status != "candidate" || !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(source.Commit) || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(source.Archive) || len(source.Files) == 0 {
			t.Fatalf("%s lacks a pinned candidate source", name)
		}
	}
	for _, file := range []graphLockedFile{lock.Adaptation.Patch, lock.Adaptation.Test, lock.Adaptation.Closure} {
		if err := verifyGraphFile("../..", file.Path, file.SHA256); err != nil {
			t.Fatal(err)
		}
	}
}

// This opt-in test replays selected source in disposable directories. It never
// accesses Kubernetes or enables any virtual capability.
func TestGraphNonVirtualUpstreamReplay(t *testing.T) {
	if os.Getenv("OPS_GRAPH_REPLAY") != "1" {
		t.Skip("set OPS_GRAPH_REPLAY=1 and OPS_GRAPH_SOURCE_DIR to replay frozen upstream source")
	}
	lock := loadGraphReplayLock(t)
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	sourceRoot := os.Getenv("OPS_GRAPH_SOURCE_DIR")
	if sourceRoot == "" {
		t.Fatal("OPS_GRAPH_SOURCE_DIR is required; no source download fallback")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	version := strings.TrimSpace(graphReplayCommand(t, ctx, root, nil, "go", "version"))
	if !strings.Contains(version, "go"+lock.Replay.GoVersion+" ") {
		t.Fatalf("wrong Go toolchain: %s", version)
	}
	cache := strings.TrimSpace(graphReplayCommand(t, ctx, root, nil, "go", "env", "GOMODCACHE"))
	if override := os.Getenv("OPS_GRAPH_MODULE_CACHE"); override != "" {
		cache = override
	}
	workspace := t.TempDir()
	ontologyDir := filepath.Join(workspace, "ontology")
	for key, directory := range map[string]string{"ariadne": "ariadne", "kubernetesOntology": "ontology"} {
		source := lock.Sources[key]
		upstream := directory
		if key == "kubernetesOntology" {
			upstream = "kubernetes-ontology"
		}
		archive := filepath.Join(workspace, directory+".tar")
		graphReplayCommand(t, ctx, root, nil, "git", "-C", filepath.Join(sourceRoot, upstream), "archive", "--format=tar", "--output="+archive, source.Commit)
		if err := verifyGraphFile(workspace, directory+".tar", source.Archive); err != nil {
			t.Fatal(err)
		}
		destination := filepath.Join(workspace, directory)
		if err := os.Mkdir(destination, 0700); err != nil {
			t.Fatal(err)
		}
		graphReplayCommand(t, ctx, root, nil, "tar", "-xf", archive, "-C", destination)
		for _, file := range source.Files {
			if err := verifyGraphFile(destination, file.Path, file.SHA256); err != nil {
				t.Fatal(err)
			}
		}
	}
	graphReplayCommand(t, ctx, ontologyDir, nil, "git", "apply", "--unidiff-zero", filepath.Join(root, lock.Adaptation.Patch.Path))
	copyGraphReplayFile(t, filepath.Join(root, lock.Adaptation.Test.Path), filepath.Join(ontologyDir, "internal/service/diagnostic/ariadne_adapter_poc_test.go"))
	// The standalone nonvirtual test deliberately excludes the preserved mixed
	// virtualization fixture and all platform runtime packages.
	graphDir := filepath.Join(workspace, "graph")
	if err := os.Mkdir(graphDir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"go.mod", "go.sum"} {
		copyGraphReplayFile(t, filepath.Join(root, file), filepath.Join(graphDir, file))
	}
	copyGraphReplayFile(t, filepath.Join(root, "test/contract/upstream_graph_nonvirtual_test.go"), filepath.Join(graphDir, "graph_test.go"))
	fixtureDir := filepath.Join(workspace, "test/fixtures/upstream-graph")
	if err := os.MkdirAll(fixtureDir, 0700); err != nil {
		t.Fatal(err)
	}
	copyGraphReplayFile(t, filepath.Join(root, "test/fixtures/upstream-graph/nonvirtual-golden-input.json"), filepath.Join(fixtureDir, "nonvirtual-golden-input.json"))
	graphReplayCommand(t, ctx, graphDir, []string{"GOPROXY=off", "GOSUMDB=off", "GOOS=linux", "GOARCH=arm64", "CGO_ENABLED=0"}, "go", "test", "-c", "-o", filepath.Join(workspace, "graph.test"))
	// Root inside an isolated container can read the 0700 disposable source;
	// all capabilities are dropped, mounts are read-only and there is no network.
	base := []string{"--context", "orbstack", "run", "--rm", "--network", "none", "--pull=never", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--tmpfs", "/tmp:rw,exec,mode=1777", "--mount", "type=bind,src=" + workspace + ",dst=/replay,readonly", "--mount", "type=bind,src=" + cache + ",dst=/go/pkg/mod,readonly", "-e", "GOPROXY=off", "-e", "GOSUMDB=off", "-e", "GOCACHE=/tmp/go-build"}
	graphArgs := append(append([]string{}, base...), "-w", "/replay/graph/run", lock.Replay.Image, "/replay/graph.test", "-test.run=^TestAriadneNonVirtualGolden20K$", "-test.v", "-test.count=1")
	// Match the fixture's repository-relative path without a platform checkout.
	if err := os.Mkdir(filepath.Join(graphDir, "run"), 0700); err != nil {
		t.Fatal(err)
	}
	graphReplayCommand(t, ctx, root, nil, "docker", graphArgs...)
	ontologyArgs := append(append([]string{}, base...), "-w", "/replay/ontology", lock.Replay.Image, "go", "test", "-mod=readonly", "-p", "1", "-count=1", "-v", "./internal/graph", "./internal/service/diagnostic")
	graphReplayCommand(t, ctx, root, nil, "docker", ontologyArgs...)
}

func graphReplayCommand(t *testing.T, ctx context.Context, dir string, environment []string, name string, args ...string) string {
	t.Helper()
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = dir
	command.Env = append(os.Environ(), environment...)
	t.Logf("command=%s %q cwd=%s", name, args, dir)
	output, err := command.CombinedOutput()
	if len(output) > 0 {
		t.Logf("%s", output)
	}
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return string(output)
}

func copyGraphReplayFile(t *testing.T, source, target string) {
	t.Helper()
	contents, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, contents, 0600); err != nil {
		t.Fatal(err)
	}
}

func verifyGraphFile(root, relative, expected string) error {
	if !filepath.IsLocal(relative) {
		return fmt.Errorf("non-local graph input %q", relative)
	}
	contents, err := os.ReadFile(filepath.Join(root, relative))
	if err != nil {
		return err
	}
	digest := sha256.Sum256(contents)
	if hex.EncodeToString(digest[:]) != expected {
		return fmt.Errorf("graph input digest mismatch: %s", relative)
	}
	return nil
}
