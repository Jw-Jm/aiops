package e2e

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/google/uuid"
	"ops-platform/internal/integrations/openbao"
	"ops-platform/internal/profile"
)

func verifyCoreCapabilities(t *testing.T, p profile.ResolvedProfile, bundleDir string, run func(string, ...string) []byte) {
	t.Helper()
	// The PostgreSQL image already contains psql; no client installation or pull.
	username := readCoreSecret(t, p.Kubernetes.Context, "ops-postgresql-auth", "username")
	result := run("kubectl", "--context", p.Kubernetes.Context, "-n", "ops-system", "exec", "ops-postgresql-0", "--", "psql", "-U", username, "-d", "ops", "-Atqc", "SELECT 1")
	if strings.TrimSpace(string(result)) != "1" {
		t.Fatal("PostgreSQL did not answer the read-only SQL health check")
	}

	kc := corePortForward(t, p.Kubernetes.Context, "ops-system", "ops-keycloak", "8080")
	metadata := coreGetJSON(t, kc+"/realms/master/.well-known/openid-configuration")
	issuer, _ := metadata["issuer"].(string)
	jwks, _ := metadata["jwks_uri"].(string)
	if !strings.HasSuffix(issuer, "/realms/master") || jwks == "" {
		t.Fatal("Keycloak did not expose realm discovery and a signing-key endpoint")
	}
	keys := coreGetJSON(t, kc+"/realms/master/protocol/openid-connect/certs")
	if values, ok := keys["keys"].([]any); !ok || len(values) == 0 {
		t.Fatal("Keycloak signing keys are unavailable")
	}

	s3 := corePortForward(t, p.Kubernetes.Context, "ops-system", "ops-seaweedfs-s3", "8333")
	credentials := aws.Credentials{AccessKeyID: readCoreSecret(t, p.Kubernetes.Context, "ops-seaweedfs-auth", "accessKey"), SecretAccessKey: readCoreSecret(t, p.Kubernetes.Context, "ops-seaweedfs-auth", "secretKey")}
	if err := coreS3RoundTrip(t.Context(), s3, credentials); err != nil {
		t.Fatalf("SeaweedFS signed object capability check: %v", err)
	}

	// Reuse the existing read-only adapters, including version/capability checks.
	discovery, err := profile.Discover(t.Context(), p.Kubernetes.Context, "kubectl", "../../bundle/component-catalog.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for name, capability := range map[string]string{"victoriaMetrics": "metrics", "victoriaLogs": "logs", "vmalert": "alertRules"} {
		candidates := discovery.Components[name]
		if len(candidates) != 1 || !candidates[0].Compatible || candidates[0].Version != p.Components[name].Version || !strings.Contains(strings.Join(candidates[0].Evidence, "\n"), capability+"=available") {
			t.Fatalf("%s did not pass its locked version and %s capability probe", name, capability)
		}
	}

	verifyOpenBaoReadiness(t, p, bundleDir)
	t.Log("core capabilities passed: SQL, OIDC discovery/JWKS, authenticated S3 object round-trip, Victoria source APIs, unsealed TLS-verified OpenBao")
}

func verifyOpenBaoReadiness(t *testing.T, p profile.ResolvedProfile, bundleDir string) {
	t.Helper()
	// CA trust comes from the independent bootstrap, never the Bundle or a TLS bypass.
	component := p.Components["openbao"]
	endpoint, err := url.Parse(component.Endpoint)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(endpoint.Hostname(), ".")
	if len(parts) < 3 || parts[2] != "svc" {
		t.Fatal("OpenBao health requires the resolved native Kubernetes Service")
	}
	ca, err := openbao.ReadBootstrapCA(os.Getenv("OPS_OFFLINE_OPENBAO_CA_FILE"), "../..", bundleDir)
	if err != nil {
		t.Fatal(err)
	}
	local := corePortForward(t, p.Kubernetes.Context, parts[1], parts[0], "8200")
	client, err := openbao.NewClient(openbao.ClientConfig{Address: strings.Replace(local, "http://", "https://", 1), ServerName: endpoint.Hostname(), CACertBundle: ca, ExpectedVersion: component.Version})
	if err != nil {
		t.Fatal(err)
	}
	status, err := client.Status(t.Context())
	if err != nil || !status.Initialized || status.Sealed || status.Version != component.Version {
		t.Fatalf("OpenBao initialized/unsealed/version health check failed: %v", err)
	}
}

func readCoreSecret(t *testing.T, kubeContext, name, key string) string {
	t.Helper()
	command := exec.CommandContext(t.Context(), "kubectl", "--context", kubeContext, "-n", "ops-system", "get", "secret", name, "-o", "jsonpath={.data."+key+"}")
	// Never print credentials or attach Secret contents to command errors.
	encoded, err := command.Output()
	if err != nil {
		t.Fatalf("required bootstrap Secret %s/%s is unavailable", name, key)
	}
	decoded, err := base64.StdEncoding.DecodeString(string(encoded))
	if err != nil || len(decoded) == 0 {
		t.Fatalf("required bootstrap Secret %s/%s is invalid", name, key)
	}
	return string(decoded)
}

func corePortForward(t *testing.T, kubeContext, namespace, service, port string) string {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	command := exec.CommandContext(ctx, "kubectl", "--context", kubeContext, "-n", namespace, "port-forward", "--address=127.0.0.1", "service/"+service, ":"+port)
	stdout, err := command.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	t.Cleanup(func() { cancel(); <-done })
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if address, ok := strings.CutPrefix(scanner.Text(), "Forwarding from 127.0.0.1:"); ok {
				if number, _, ok := strings.Cut(address, " -> "); ok {
					select {
					case ready <- "http://127.0.0.1:" + number:
					default:
					}
				}
			}
		}
	}()
	select {
	case address := <-ready:
		return address
	case <-time.After(20 * time.Second):
		t.Fatalf("port-forward for %s/%s did not become ready", namespace, service)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	return ""
}

func coreGetJSON(t *testing.T, endpoint string) map[string]any {
	t.Helper()
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("core HTTP health returned %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20+1))
	if err != nil || len(data) > 1<<20 {
		t.Fatal("invalid or oversized core health response")
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func coreS3RoundTrip(ctx context.Context, endpoint string, credentials aws.Credentials) error {
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/", nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden && response.StatusCode != http.StatusUnauthorized {
		return fmt.Errorf("anonymous S3 access was not rejected: HTTP %d", response.StatusCode)
	}
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	bucket := "ops-task27-" + id.String()
	payload := []byte("Task 2.7 offline object capability " + id.String())
	send := func(method, path string, body []byte) ([]byte, error) {
		r, err := http.NewRequestWithContext(ctx, method, endpoint+"/"+path, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		digest := sha256.Sum256(body)
		r.Header.Set("X-Amz-Content-Sha256", hex.EncodeToString(digest[:]))
		if err := v4.NewSigner().SignHTTP(ctx, credentials, r, hex.EncodeToString(digest[:]), "s3", "us-east-1", time.Now().UTC()); err != nil {
			return nil, err
		}
		res, err := client.Do(r)
		if err != nil {
			return nil, err
		}
		defer res.Body.Close()
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			var problem struct {
				Code string `xml:"Code"`
			}
			data, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
			_ = xml.Unmarshal(data, &problem)
			return nil, fmt.Errorf("owned S3 %s check returned HTTP %d (%s)", method, res.StatusCode, problem.Code)
		}
		data, err := io.ReadAll(io.LimitReader(res.Body, 4097))
		if len(data) > 4096 {
			return nil, fmt.Errorf("oversized S3 capability response")
		}
		return data, err
	}
	if _, err := send(http.MethodPut, bucket, nil); err != nil {
		return err
	}
	// Cleanup only follows acknowledged creation of this invocation's UUID bucket.
	object := bucket + "/probe.txt"
	_, putErr := send(http.MethodPut, object, payload)
	var getErr error
	if putErr == nil {
		data, err := send(http.MethodGet, object, nil)
		getErr = err
		if err == nil && !bytes.Equal(data, payload) {
			getErr = fmt.Errorf("S3 object contents differ")
		}
	}
	_, deleteObjectErr := send(http.MethodDelete, object, nil)
	_, deleteBucketErr := send(http.MethodDelete, bucket, nil)
	for _, err := range []error{putErr, getErr, deleteObjectErr, deleteBucketErr} {
		if err != nil {
			return err
		}
	}
	return nil
}

func TestCoreS3CapabilityRejectsAnonymousAccessAndChecksObjectContents(t *testing.T) {
	object := []byte(nil)
	created, deleted := false, false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=fixture/") {
			t.Error("missing SDK signature")
		}
		if strings.HasSuffix(r.URL.Path, "/probe.txt") {
			switch r.Method {
			case "PUT":
				object, _ = io.ReadAll(r.Body)
			case "GET":
				w.Write(object)
			case "DELETE":
				object = nil
			}
		} else if r.Method == "PUT" {
			created = true
		} else if r.Method == "DELETE" {
			deleted = true
		}
	}))
	defer server.Close()
	if err := coreS3RoundTrip(t.Context(), server.URL, aws.Credentials{AccessKeyID: "fixture", SecretAccessKey: "fixture-secret"}); err != nil {
		t.Fatal(err)
	}
	if !created || !deleted || object != nil {
		t.Fatal("owned capability resources were not cleaned")
	}
}

func TestSeaweedPinnedOfflineS3Capability(t *testing.T) {
	if os.Getenv("OPS_ORBSTACK_SEAWEED_S3_POC") != "1" {
		t.Skip("explicit isolated SeaweedFS S3 PoC not requested")
	}
	const image = "docker.io/chrislusf/seaweedfs@sha256:d4cf67729aa8777e1a43a5b61d72e5b96179e4b7bac9a221cb14cbc2036cb32e"
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	owner := "ops-task27-s3-" + id.String()
	run := func(args ...string) []byte {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		command := exec.CommandContext(ctx, "docker", append([]string{"--context", "orbstack"}, args...)...)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("isolated S3 PoC Docker operation %s failed: %v: %.4096s", args[0], err, output)
		}
		return bytes.TrimSpace(output)
	}
	run("image", "inspect", image)
	network := string(run("network", "create", "--internal", "--label", "ops.platform.io/poc-owner="+owner, owner))
	t.Cleanup(func() { run("network", "rm", network) })
	credentials := aws.Credentials{AccessKeyID: "task27-" + id.String(), SecretAccessKey: id.String() + id.String()}
	private, err := os.MkdirTemp("", "ops-task27-s3-credentials-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(private) })
	env := filepath.Join(private, "credentials.env")
	if err := os.WriteFile(env, []byte("AWS_ACCESS_KEY_ID="+credentials.AccessKeyID+"\nAWS_SECRET_ACCESS_KEY="+credentials.SecretAccessKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	container := string(run("run", "-d", "--pull=never", "--network", network, "--label", "ops.platform.io/poc-owner="+owner,
		"--user=10001:10001", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--memory=1g",
		"--tmpfs", "/data:uid=10001,gid=10001,mode=0700", "--tmpfs", "/tmp:uid=10001,gid=10001,mode=0700",
		"--env-file", env, image, "server", "-dir=/data", "-s3", "-volume.max=5"))
	t.Cleanup(func() {
		if string(run("inspect", "--format", `{{index .Config.Labels "ops.platform.io/poc-owner"}}`, container)) != owner {
			t.Error("S3 PoC cleanup ownership differs")
			return
		}
		run("rm", "-f", container)
	})
	address := string(run("inspect", "--format", `{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}`, container))
	ip := net.ParseIP(address)
	if ip == nil || !ip.IsPrivate() {
		t.Fatal("S3 PoC requires the private address of its internal Docker network")
	}
	endpoint := "http://" + net.JoinHostPort(address, "8333")
	deadline := time.Now().Add(90 * time.Second)
	for {
		request, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, endpoint+"/", nil)
		response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusForbidden {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("isolated SeaweedFS did not become ready with anonymous access rejected")
		}
		time.Sleep(time.Second)
	}
	if err := coreS3RoundTrip(t.Context(), endpoint, credentials); err != nil {
		t.Fatal(err)
	}
	t.Log("exact pinned SeaweedFS: internal Docker network, non-root/read-only, anonymous rejected, signed object round-trip passed")
}
