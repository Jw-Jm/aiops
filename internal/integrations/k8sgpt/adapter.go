package k8sgpt

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"gopkg.in/yaml.v3"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var ErrDrift = errors.New("K8SGPT_FORMAT_DRIFT")
var ErrDisabled = errors.New("K8SGPT_UNVERIFIED")

// LockedBinarySHA256 binds the unchanged Linux/arm64 CLI built from ADR-0013's
// source and licensed closure. Runtime configuration cannot admit another CLI.
const LockedBinarySHA256 = "6f9152ff31d2692a14e880ae73c2fe35dbc2938560d2c55227f4b0c67409af53"

type AnalyzerError struct {
	Text          string `json:"Text"`
	KubernetesDoc string `json:"KubernetesDoc,omitempty"`
	Sensitive     any    `json:"Sensitive,omitempty"`
}
type Result struct {
	Kind         string          `json:"kind"`
	Name         string          `json:"name"`
	Error        []AnalyzerError `json:"error"`
	Details      string          `json:"details,omitempty"`
	ParentObject string          `json:"parentObject,omitempty"`
}
type Output struct {
	Status   string   `json:"status"`
	Problems int      `json:"problems"`
	Provider string   `json:"provider"`
	Errors   []string `json:"errors"`
	Results  []Result `json:"results"`
}

func Decode(raw []byte) (Output, error) {
	var out Output
	if len(raw) > 64<<10 {
		return out, ErrDrift
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&out) != nil {
		return out, ErrDrift
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return out, ErrDrift
	}
	for _, key := range []string{"status", "problems", "provider", "errors", "results"} {
		if _, ok := fields[key]; !ok {
			return out, ErrDrift
		}
	}
	var trailing any
	if d.Decode(&trailing) != io.EOF || (out.Status != "OK" && out.Status != "ProblemDetected") || out.Provider != "" || len(out.Errors) > 0 {
		return out, ErrDrift
	}
	if out.Problems < 0 || (out.Status == "OK" && (out.Problems != 0 || len(out.Results) != 0)) || (out.Status == "ProblemDetected" && (out.Problems == 0 || len(out.Results) == 0)) {
		return out, ErrDrift
	}
	problemCount := 0
	for _, r := range out.Results {
		if r.Name == "" || r.Kind == "" || r.Details != "" || len(r.Error) == 0 {
			return out, ErrDrift
		}
		for _, e := range r.Error {
			problemCount++
			if strings.TrimSpace(e.Text) == "" || len(e.Text) > 4096 {
				return out, ErrDrift
			}
		}
	}
	if out.Problems != problemCount {
		return out, ErrDrift
	}
	if out.Results == nil {
		out.Results = []Result{}
	}
	return out, nil
}

// Only these arguments are supported. Kubeconfig is a server-owned path; no
// caller argument, provider, explain, prompt, tool or shell is accepted.
func FixedArgs(kubeconfig string) []string {
	return []string{"analyze", "--kubeconfig", kubeconfig, "--filter", "Pod,Node,PersistentVolumeClaim", "--output", "json", "--no-cache", "--max-concurrency", "1"}
}

type Adapter struct {
	Binary, Kubeconfig, SHA256, Namespace string
}

func (a Adapter) Run(ctx context.Context) (Output, error) {
	if !filepath.IsAbs(a.Binary) || !filepath.IsAbs(a.Kubeconfig) || len(a.SHA256) != 64 || !regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`).MatchString(a.Namespace) || !hardLimits() {
		return Output{}, ErrDisabled
	}
	// Exec credential plugins would introduce another executable. Refuse them.
	raw, err := os.ReadFile(a.Kubeconfig)
	if err != nil || len(raw) > 64<<10 {
		return Output{}, ErrDisabled
	}
	if !safeKubeconfig(raw) {
		return Output{}, ErrDisabled
	}

	dir, err := os.MkdirTemp("", "sp05-k8sgpt-")
	if err != nil {
		return Output{}, err
	}
	defer os.RemoveAll(dir)
	// Execute the same verified bytes from a private copy. A replace/rename of
	// the image path after hashing cannot change what this invocation executes.
	input, err := os.Open(a.Binary)
	if err != nil {
		return Output{}, ErrDisabled
	}
	defer input.Close()
	file, err := os.OpenFile(filepath.Join(dir, "analyzer"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0500)
	if err != nil {
		return Output{}, err
	}
	hash := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(input, (256<<20)+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || size > 256<<20 || hex.EncodeToString(hash.Sum(nil)) != a.SHA256 {
		return Output{}, ErrDisabled
	}
	privateKube := filepath.Join(dir, "kubeconfig")
	if err := os.WriteFile(privateKube, raw, 0600); err != nil {
		return Output{}, err
	}
	config := filepath.Join(dir, "config.json")
	if err := os.WriteFile(config, []byte("{}\n"), 0600); err != nil {
		return Output{}, err
	}
	pass, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	args := append(FixedArgs(privateKube), "--config", config, "--namespace", a.Namespace)
	cmd := exec.CommandContext(pass, filepath.Join(dir, "analyzer"), args...)
	cmd.Dir = dir
	cmd.Env = []string{"HOME=" + dir, "XDG_CONFIG_HOME=" + dir, "XDG_CACHE_HOME=" + dir, "XDG_DATA_HOME=" + dir, "GOMAXPROCS=1", "GOMEMLIMIT=256MiB"}
	out := &boundedBuffer{}
	stderr := &boundedBuffer{}
	cmd.Stdout = out
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return Output{}, errors.New("K8SGPT_UNAVAILABLE")
	}
	if strings.Contains(stderr.String(), "explain") {
		return Output{}, ErrDrift
	}
	return Decode(out.Bytes())
}

// Required by the shipped Worker pod: finite CPU/memory cgroups and a read-only
// root mount. Configuration cannot assert that these kernel controls exist.
func hardLimits() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	memory, err := os.ReadFile("/sys/fs/cgroup/memory.max")
	if err != nil {
		return false
	}
	m, err := strconv.ParseInt(strings.TrimSpace(string(memory)), 10, 64)
	if err != nil || m <= 0 || m > 512<<20 {
		return false
	}
	cpu, err := os.ReadFile("/sys/fs/cgroup/cpu.max")
	if err != nil {
		return false
	}
	values := strings.Fields(string(cpu))
	if len(values) != 2 {
		return false
	}
	quota, e1 := strconv.ParseInt(values[0], 10, 64)
	period, e2 := strconv.ParseInt(values[1], 10, 64)
	if e1 != nil || e2 != nil || quota <= 0 || period <= 0 || quota > period {
		return false
	}
	mounts, err := os.ReadFile("/proc/self/mounts")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(mounts), "\n") {
		f := strings.Fields(line)
		if len(f) > 3 && f[1] == "/" {
			for _, option := range strings.Split(f[3], ",") {
				if option == "ro" {
					return true
				}
			}
		}
	}
	return false
}

type boundedBuffer struct{ bytes.Buffer }

func (b *boundedBuffer) Write(raw []byte) (int, error) {
	if b.Len()+len(raw) > 64<<10 {
		return 0, ErrDrift
	}
	return b.Buffer.Write(raw)
}

func safeKubeconfig(raw []byte) bool {
	var kube struct {
		Users []struct {
			Name string         `yaml:"name"`
			User map[string]any `yaml:"user"`
		} `yaml:"users"`
		Clusters []struct {
			Name    string         `yaml:"name"`
			Cluster map[string]any `yaml:"cluster"`
		} `yaml:"clusters"`
		Contexts []struct {
			Name    string         `yaml:"name"`
			Context map[string]any `yaml:"context"`
		} `yaml:"contexts"`
		Current string `yaml:"current-context"`
	}
	if yaml.Unmarshal(raw, &kube) != nil || len(kube.Users) != 1 || len(kube.Clusters) != 1 || len(kube.Contexts) != 1 || kube.Current != kube.Contexts[0].Name {
		return false
	}
	for key := range kube.Users[0].User {
		if key != "token" && key != "tokenFile" {
			return false
		}
	}
	c := kube.Contexts[0].Context
	if c["cluster"] != kube.Clusters[0].Name || c["user"] != kube.Users[0].Name {
		return false
	}
	for key := range c {
		if key != "cluster" && key != "user" && key != "namespace" {
			return false
		}
	}
	cluster := kube.Clusters[0].Cluster
	server, _ := cluster["server"].(string)
	if !strings.HasPrefix(server, "https://") || cluster["insecure-skip-tls-verify"] == true {
		return false
	}
	for key := range cluster {
		if key != "server" && key != "certificate-authority" && key != "certificate-authority-data" && key != "insecure-skip-tls-verify" {
			return false
		}
	}
	return true
}
