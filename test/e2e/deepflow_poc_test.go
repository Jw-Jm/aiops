package e2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"ops-platform/internal/supplychain"
)

const (
	deepFlowTestNamespace = "ops-sp02-deepflow-poc"
	deepFlowTestRelease   = "sp02-task-2-5"
	deepFlowPasswordToken = "__DEEPFLOW_POC_DB_PASSWORD__"
	deepFlowServerDigest  = "609d1f8a2020a750a9a34dbb7fff4b785cbe9b78549d86bc7cefb5bccf70b939"
	deepFlowAgentDigest   = "25419423b6ea1c73897f5ed09a244f9172acadd95cec5ac1bf07a1ba3ed31bf9"
	deepFlowMySQLDigest   = "a1f202a397782b7e2a98359b3f476132189b179e04f9123d4750d527e937f138"
	deepFlowCHDigest      = "757180f5d4efcf4a29a3147ac9f0b58b8986a42adaa79febcd70b57ea68eac76"
	deepFlowBusyboxDigest = "bd44eb136a95dcc8dc58995e43abc40a413f2e8e3d4a2aae6bccbe94686acb05"
)

func TestDeepFlowPOC(t *testing.T) {
	root := filepath.Join("..", "..")
	catalogFile, err := os.Open(filepath.Join(root, "bundle", "component-catalog.yaml"))
	if err != nil {
		t.Fatalf("open Component Catalog: %v", err)
	}
	defer catalogFile.Close()
	catalog, err := supplychain.LoadCatalogWithEvidence(catalogFile, os.DirFS(root))
	if err != nil {
		t.Fatalf("load Component Catalog: %v", err)
	}
	deepflow, ok := catalog.Component("deepflow")
	if !ok || deepflow.Version != "7.2.0" || deepflow.Commit != "e567b167453ffa99f08f26def20379b4f831e073" || deepflow.Digest != "sha256:"+deepFlowServerDigest || len(deepflow.Architectures) != 1 || deepflow.Architectures[0] != "linux/arm64" {
		t.Fatalf("DeepFlow server lock is incomplete: component=%#v", deepflow)
	}
	if deepflow.ChartLock == nil || deepflow.ChartLock.Version != "7.1.002" || deepflow.ChartLock.Digest != "sha256:ca0ce1e919cd1a4184e6fba5e41ce1935694557fd78a4b04883f036de288d519" {
		t.Fatalf("DeepFlow chart archive is not locked: %#v", deepflow.ChartLock)
	}
	wantDependencies := map[string]string{"deepflow-agent": deepFlowAgentDigest, "mysql": deepFlowMySQLDigest, "clickhouse": deepFlowCHDigest}
	for _, dependency := range deepflow.DependencyClosure {
		if want, exists := wantDependencies[dependency.Name]; exists {
			if dependency.Digest != "sha256:"+want {
				t.Errorf("%s digest = %s, want sha256:%s", dependency.Name, dependency.Digest, want)
			}
			delete(wantDependencies, dependency.Name)
		}
	}
	if len(wantDependencies) != 0 {
		t.Fatalf("DeepFlow runtime dependency locks missing: %v", wantDependencies)
	}
	for _, name := range []string{"values-dev-arm64.yaml", "install.yaml"} {
		if _, err := os.Stat(filepath.Join(root, "deploy", "addons", "deepflow", name)); err != nil {
			t.Fatalf("required DeepFlow PoC artifact %s: %v", name, err)
		}
	}
	fixturePath := filepath.Join(root, "test", "fixtures", "deepflow", "v7.2.0", "querier-show-tables-upstream.json")
	fixtureBytes, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read pinned upstream Querier API fixture: %v", err)
	}
	var apiFixture struct {
		FixtureType  string `json:"fixtureType"`
		SourceCommit string `json:"sourceCommit"`
		Request      struct {
			DB  string `json:"db"`
			SQL string `json:"sql"`
		} `json:"request"`
		Response struct {
			Status string `json:"OPT_STATUS"`
			Result struct {
				Columns []string   `json:"columns"`
				Values  [][]string `json:"values"`
			} `json:"result"`
		} `json:"response"`
	}
	if err := json.Unmarshal(fixtureBytes, &apiFixture); err != nil {
		t.Fatalf("decode pinned upstream Querier API fixture: %v", err)
	}
	if apiFixture.FixtureType != "upstream-documentation-example" || apiFixture.SourceCommit != "e567b167453ffa99f08f26def20379b4f831e073" || apiFixture.Request.DB != "flow_log" || apiFixture.Request.SQL != "show tables" || apiFixture.Response.Status != "SUCCESS" || len(apiFixture.Response.Result.Values) != 2 {
		t.Fatalf("pinned upstream Querier API fixture is incomplete or unexpected: %#v", apiFixture)
	}
	install, err := os.ReadFile(filepath.Join(root, "deploy", "addons", "deepflow", "install.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	manifest := string(install)
	if strings.Contains(manifest, "deepflow-app") || strings.Contains(manifest, ":disabled") {
		t.Fatal("DeepFlow app resource or image reference is present in the minimal PoC manifest")
	}
	if !strings.Contains(manifest, "kind: NetworkPolicy") || !strings.Contains(manifest, "kind: Secret") || !strings.Contains(manifest, "MYSQL_ROOT_PASSWORD") {
		t.Fatal("DeepFlow manifest is missing egress policy or Secret-backed credentials")
	}
	for _, match := range regexp.MustCompile(`(?m)^\s*image:\s*([^\s]+)`).FindAllStringSubmatch(manifest, -1) {
		if !strings.Contains(match[1], "@sha256:") {
			t.Errorf("DeepFlow container image is not digest-pinned: %s", match[1])
		}
	}
	for _, document := range strings.Split(manifest, "---") {
		if strings.Contains(document, "kind: ConfigMap") && strings.Contains(document, deepFlowPasswordToken) {
			t.Fatal("database credential placeholder is present in a ConfigMap")
		}
	}
	if os.Getenv("OPS_DEEPFLOW_ORBSTACK_POC") != "1" {
		t.Skip("set OPS_DEEPFLOW_ORBSTACK_POC=1 to run the isolated DeepFlow v7.2.0 OrbStack PoC")
	}
	runDeepFlowOrbStackPOC(t, root, manifest)
}

func runDeepFlowOrbStackPOC(t *testing.T, root, manifest string) {
	t.Helper()
	if output := deepFlowCommand(t, "kubectl", "--context", "orbstack", "get", "nodes", "-o", "wide"); !strings.Contains(output, "orbstack") {
		t.Fatalf("OrbStack node not found in selected context: %s", strings.TrimSpace(output))
	}
	prepareDeepFlowNamespace(t)
	apiPolicy := deepFlowAPIPolicy(t)

	secretBytes := make([]byte, 32)
	if _, err := rand.Read(secretBytes); err != nil {
		t.Fatalf("generate test-only database credential: %v", err)
	}
	password := hex.EncodeToString(secretBytes)
	materialized := strings.ReplaceAll(manifest, deepFlowPasswordToken, password) + "\n---\n" + apiPolicy
	if strings.Contains(materialized, deepFlowPasswordToken) {
		t.Fatal("failed to materialize all test-only Secret references")
	}
	installPath := filepath.Join(t.TempDir(), "deepflow-install.yaml")
	if err := os.WriteFile(installPath, []byte(materialized), 0o600); err != nil {
		t.Fatalf("write private materialized manifest: %v", err)
	}
	if info, err := os.Stat(installPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("materialized manifest mode = %v, %v; want 0600", info, err)
	}
	if output, err := deepFlowCommandResult(t, "kubectl", "--context", "orbstack", "apply", "-f", installPath); err != nil {
		t.Fatalf("apply digest-pinned DeepFlow manifest: %v\n%s", err, strings.ReplaceAll(strings.TrimSpace(output), password, "[redacted]"))
	}

	selector := "ops.platform.io/test-release=" + deepFlowTestRelease
	waitDeepFlowRuntimeReady(t, selector)
	controllerPort, querierPort := deepFlowReservePort(t), deepFlowReservePort(t)
	stopPortForward := deepFlowPortForward(t, controllerPort, querierPort)
	defer stopPortForward()
	client := &http.Client{Timeout: 20 * time.Second}
	configureDeepFlowAgentGroup(t, client, controllerPort)
	agent := waitForDeepFlowAgent(t, client, controllerPort)
	waitDeepFlowRuntimeReady(t, selector)
	assertDeepFlowRuntimeResources(t, selector)
	probeDeepFlowRuntimeEgress(t, selector)
	publicEgressDenied, egressEvidence := probeDeepFlowPublicEgress(t, selector)
	if !publicEgressDenied {
		t.Fatalf("DeepFlow live gate failed: public egress was not denied; keep fixture_only: %+v", egressEvidence)
	}
	t.Logf("DeepFlow public egress probe: %+v", egressEvidence)

	mysqlPod := strings.TrimSpace(deepFlowCommand(t, "kubectl", "--context", "orbstack", "-n", deepFlowTestNamespace, "get", "pods", "-l", "component=mysql", "-o", "jsonpath={.items[0].metadata.name}"))
	if mysqlPod == "" {
		t.Fatal("ready MySQL Pod was not found")
	}
	for index := 0; index < 24; index++ {
		_, err := deepFlowCommandResult(t, "kubectl", "--context", "orbstack", "-n", deepFlowTestNamespace, "exec", mysqlPod, "-c", "mysql", "--", "sh", "-c", `MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql --protocol=tcp --connect-timeout=5 -h ops-sp02-deepflow-mysql -P 30130 -uroot -Nse 'SELECT 1'`)
		if err != nil {
			t.Fatalf("generate application test flow %d through MySQL Service: %v", index+1, err)
		}
	}

	query, sample := waitForDeepFlowQuery(t, client, querierPort)
	if err := writeDeepFlowFixture(root, agent, query, sample); err != nil {
		t.Fatalf("write captured DeepFlow v7.2.0 query fixture: %v", err)
	}
	t.Logf("DeepFlow live PoC passed: agent=%q state=%d cluster=%q queryRows=%d columns=%v retrans_tx=%v retrans_rx=%v", agent.Name, agent.State, agent.PodClusterName, sample.RowCount, sample.Columns, sample.Sample["retrans_tx"], sample.Sample["retrans_rx"])
}

func waitDeepFlowRuntimeReady(t *testing.T, selector string) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Minute)
	for time.Now().Before(deadline) {
		waitDeepFlowPolicyRules(t, selector, 4)
		if _, err := deepFlowCommandTimeoutResult(t, 15*time.Second, "kubectl", "--context", "orbstack", "-n", deepFlowTestNamespace, "wait", "--for=condition=Ready", "pods", "-l", selector, "--timeout=5s"); err == nil {
			return
		}
	}
	t.Fatal("DeepFlow Pods did not become Ready after their scoped egress rules were programmed")
}

func waitDeepFlowPolicyRules(t *testing.T, selector string, count int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		podJSON := deepFlowCommand(t, "kubectl", "--context", "orbstack", "-n", deepFlowTestNamespace, "get", "pods", "-l", selector, "-o", "json")
		var pods struct {
			Items []struct {
				Metadata struct {
					Name string `json:"name"`
					UID  string `json:"uid"`
				} `json:"metadata"`
				Status struct {
					IP string `json:"podIP"`
				} `json:"status"`
			} `json:"items"`
		}
		if err := json.Unmarshal([]byte(podJSON), &pods); err != nil {
			t.Fatal(err)
		}
		rules := deepFlowCommand(t, "kubectl", "--context", "orbstack", "-n", "ops-dev-network", "exec", "daemonset/ops-dev-network", "--", "iptables-save")
		ready := len(pods.Items) == count
		for _, pod := range pods.Items {
			if !deepFlowPolicyRejectsUnmarkedEgress(rules, pod.Status.IP) {
				ready = false
			}
		}
		if ready {
			var approved []string
			for _, pod := range pods.Items {
				if pod.Metadata.UID == "" {
					t.Fatal("Pod UID is missing from scoped network gate")
				}
				approved = append(approved, pod.Metadata.UID)
			}
			current := deepFlowCommand(t, "kubectl", "--context", "orbstack", "-n", deepFlowTestNamespace, "get", "configmap", "ops-sp02-deepflow-network-gate", "-o", "json")
			var gate struct {
				Data map[string]string `json:"data"`
			}
			if err := json.Unmarshal([]byte(current), &gate); err != nil {
				t.Fatal(err)
			}
			value := strings.Join(approved, "\n") + "\n"
			if gate.Data["approved-uids"] != value {
				patch, err := json.Marshal(map[string]any{"data": map[string]string{"approved-uids": value}})
				if err != nil {
					t.Fatal(err)
				}
				deepFlowCommand(t, "kubectl", "--context", "orbstack", "-n", deepFlowTestNamespace, "patch", "configmap", "ops-sp02-deepflow-network-gate", "--type=merge", "-p", string(patch))
				for _, pod := range pods.Items {
					t.Logf("confirmed programmed egress deny before approving Pod UID %s: %s %s", pod.Metadata.UID, pod.Metadata.Name, pod.Status.IP)
				}
			}
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatal("policy controller did not install release Pod egress rules; runtime network gate stays closed")
}

func deepFlowPolicyRejectsUnmarkedEgress(rules, address string) bool {
	ip, err := netip.ParseAddr(address)
	if err != nil || !ip.Is4() || !ip.IsPrivate() {
		return false
	}
	chain := ""
	for _, line := range strings.Split(rules, "\n") {
		if strings.HasPrefix(line, "-A KUBE-ROUTER-FORWARD ") && strings.Contains(line, "-s "+address+"/32 ") {
			parts := strings.Fields(line)
			for i, part := range parts {
				if part == "-j" && i+1 < len(parts) && strings.HasPrefix(parts[i+1], "KUBE-POD-FW-") {
					chain = parts[i+1]
				}
			}
		}
	}
	if chain == "" {
		return false
	}
	policy, reject := false, false
	for _, line := range strings.Split(rules, "\n") {
		if !strings.HasPrefix(line, "-A "+chain+" ") {
			continue
		}
		if strings.Contains(line, "-s "+address+"/32 ") {
			if strings.Contains(line, "-j KUBE-NWPLCY-DEFAULT") {
				return false
			}
			if strings.Contains(line, "-j KUBE-NWPLCY-") {
				policy = true
			}
		}
		if strings.Contains(line, "-m mark ! --mark 0x10000/0x10000 -j REJECT") {
			reject = true
		}
	}
	return policy && reject
}

func configureDeepFlowAgentGroup(t *testing.T, client *http.Client, port int) {
	t.Helper()
	call := func(method, path string, body []byte) []byte {
		req, err := http.NewRequest(method, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Org-Id", "1")
		req.Header.Set("X-User-Id", "1")
		req.Header.Set("X-User-Type", "1")
		req.Header.Set("Content-Type", "application/json")
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			Status string `json:"OPT_STATUS"`
		}
		if json.Unmarshal(b, &envelope) != nil || res.StatusCode != http.StatusOK || envelope.Status != "SUCCESS" {
			t.Fatalf("upstream Agent group API %s %s failed HTTP %d: %s", method, path, res.StatusCode, b)
		}
		return b
	}
	var groups struct {
		Data []struct {
			Name   string `json:"NAME"`
			LCUUID string `json:"LCUUID"`
		} `json:"DATA"`
	}
	if err := json.Unmarshal(call(http.MethodGet, "/v1/vtap-groups/", nil), &groups); err != nil {
		t.Fatal(err)
	}
	groupID := ""
	for _, g := range groups.Data {
		if g.Name == "default" {
			groupID = g.LCUUID
		}
	}
	if groupID == "" {
		t.Fatal("fresh PoC has no upstream default Agent group")
	}
	serverIP := strings.TrimSpace(deepFlowCommand(t, "kubectl", "--context", "orbstack", "-n", deepFlowTestNamespace, "get", "pods", "-l", "component=deepflow-server", "-o", "jsonpath={.items[0].status.podIP}"))
	ip, err := netip.ParseAddr(serverIP)
	if err != nil || !ip.IsPrivate() {
		t.Fatalf("server Pod IP is not private: %q", serverIP)
	}
	config := map[string]any{"global": map[string]any{"communication": map[string]any{"proxy_controller_ip": serverIP, "proxy_controller_port": 20035, "ingester_ip": serverIP, "ingester_port": 20033}}}
	b, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	call(http.MethodPost, "/v1/agent-group-configuration/"+url.PathEscape(groupID)+"/json", b)
	t.Log("upstream default Agent group configured with the observed server Pod IP and internal control/ingester ports")
}

func probeDeepFlowRuntimeEgress(t *testing.T, selector string) {
	t.Helper()
	output := deepFlowCommand(t, "kubectl", "--context", "orbstack", "-n", deepFlowTestNamespace, "get", "pods", "-l", selector, "-o", "json")
	var pods struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				HostNetwork bool `json:"hostNetwork"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(output), &pods); err != nil {
		t.Fatal(err)
	}
	if len(pods.Items) != 4 {
		t.Fatalf("expected four runtime Pods, got %d", len(pods.Items))
	}
	profile := filepath.Join(t.TempDir(), "probe-container.json")
	if err := os.WriteFile(profile, []byte(`{"securityContext":{"runAsUser":65534,"runAsNonRoot":true,"allowPrivilegeEscalation":false,"capabilities":{"drop":["ALL"]},"seccompProfile":{"type":"RuntimeDefault"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	script := `set -eu
nslookup kubernetes.default.svc >/dev/null
nc -z -w 3 ops-sp02-deepflow-mysql 30130
echo internal-dns-and-mysql-connected
for ip in 1.1.1.1 8.8.8.8 2606:4700:4700::1111; do
  for port in 80 443; do
    if nc -z -w 2 "$ip" "$port" >/dev/null 2>&1; then echo "public-connected:$ip:$port"; exit 10; fi
    echo "public-denied:$ip:$port"
  done
done`
	for _, pod := range pods.Items {
		if pod.Spec.HostNetwork {
			t.Fatalf("cannot claim Pod-policy isolation for host-network Pod %s", pod.Metadata.Name)
		}
		deepFlowCommand(t, "kubectl", "--context", "orbstack", "-n", deepFlowTestNamespace, "debug", "pod/"+pod.Metadata.Name, "--container=sp02-network-proof", "--image=docker.io/library/busybox:1.36.1@sha256:"+deepFlowBusyboxDigest, "--image-pull-policy=Never", "--profile=restricted", "--custom="+profile, "--attach=true", "--quiet", "--", "sh", "-c", script)
		logs := deepFlowCommand(t, "kubectl", "--context", "orbstack", "-n", deepFlowTestNamespace, "logs", pod.Metadata.Name, "-c", "sp02-network-proof")
		if !strings.Contains(logs, "internal-dns-and-mysql-connected") || strings.Count(logs, "public-denied:") != 6 || strings.Contains(logs, "public-connected:") {
			t.Fatalf("runtime Pod isolation failed %s: %s", pod.Metadata.Name, logs)
		}
		t.Logf("DeepFlow runtime network namespace %s: %s", pod.Metadata.Name, strings.TrimSpace(logs))
	}
}

// Pin the API exception to endpoints discovered from this selected cluster.
// Broad node/Internet CIDRs would defeat the live isolation gate.
func deepFlowAPIPolicy(t *testing.T) string {
	t.Helper()
	endpoints := deepFlowCommand(t, "kubectl", "--context", "orbstack", "-n", "default", "get", "endpoints", "kubernetes", "-o", "json")
	var object struct {
		Subsets []struct {
			Addresses []struct {
				IP string `json:"ip"`
			} `json:"addresses"`
			Ports []struct {
				Port     int    `json:"port"`
				Protocol string `json:"protocol"`
			} `json:"ports"`
		} `json:"subsets"`
	}
	if err := json.Unmarshal([]byte(endpoints), &object); err != nil {
		t.Fatal(err)
	}
	var rules []any
	for _, subset := range object.Subsets {
		for _, endpoint := range subset.Addresses {
			ip, err := netip.ParseAddr(endpoint.IP)
			if err != nil || !ip.IsPrivate() {
				t.Fatalf("API endpoint is not a verified private IP: %q", endpoint.IP)
			}
			for _, port := range subset.Ports {
				if port.Protocol != "TCP" || port.Port < 1 || port.Port > 65535 {
					t.Fatalf("invalid API endpoint port: %+v", port)
				}
				rules = append(rules, map[string]any{"to": []any{map[string]any{"ipBlock": map[string]string{"cidr": netip.PrefixFrom(ip, ip.BitLen()).String()}}}, "ports": []any{map[string]any{"protocol": "TCP", "port": port.Port}}})
			}
		}
	}
	if len(rules) == 0 {
		t.Fatal("no private Kubernetes API endpoint was discovered")
	}
	// Some policy implementations evaluate before Service DNAT. Permit only
	// the API Service's exact VIP and HTTPS port as well as its backend above.
	serviceJSON := deepFlowCommand(t, "kubectl", "--context", "orbstack", "-n", "default", "get", "service", "kubernetes", "-o", "json")
	var service struct {
		Spec struct {
			ClusterIPs []string `json:"clusterIPs"`
			Ports      []struct {
				Name string `json:"name"`
				Port int    `json:"port"`
			} `json:"ports"`
		} `json:"spec"`
	}
	if err := json.Unmarshal([]byte(serviceJSON), &service); err != nil {
		t.Fatal(err)
	}
	for _, vip := range service.Spec.ClusterIPs {
		ip, err := netip.ParseAddr(vip)
		if err != nil || !ip.IsPrivate() {
			t.Fatalf("API VIP is not private: %q", vip)
		}
		for _, port := range service.Spec.Ports {
			if port.Name == "https" && port.Port > 0 && port.Port < 65536 {
				rules = append(rules, map[string]any{"to": []any{map[string]any{"ipBlock": map[string]string{"cidr": netip.PrefixFrom(ip, ip.BitLen()).String()}}}, "ports": []any{map[string]any{"protocol": "TCP", "port": port.Port}}})
			}
		}
	}
	policy := map[string]any{"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy", "metadata": map[string]any{"name": "deepflow-api-only", "namespace": deepFlowTestNamespace, "labels": map[string]string{"ops.platform.io/test-release": deepFlowTestRelease}}, "spec": map[string]any{"podSelector": map[string]any{"matchLabels": map[string]string{"ops.platform.io/test-release": deepFlowTestRelease}}, "policyTypes": []string{"Egress"}, "egress": rules}}
	contents, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("DeepFlow Kubernetes API egress exceptions: %v", rules)
	return string(contents)
}

func prepareDeepFlowNamespace(t *testing.T) {
	t.Helper()
	output, err := deepFlowCommandResult(t, "kubectl", "--context", "orbstack", "get", "namespace", deepFlowTestNamespace, "-o", "json")
	if err == nil {
		var namespace struct {
			Metadata struct {
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
		}
		if err := json.Unmarshal([]byte(output), &namespace); err != nil || namespace.Metadata.Labels["ops.platform.io/test-release"] != deepFlowTestRelease {
			t.Fatalf("refusing to use pre-existing namespace %s without its Task 2.5 label", deepFlowTestNamespace)
		}
		deepFlowCommand(t, "kubectl", "--context", "orbstack", "-n", deepFlowTestNamespace, "delete", "all,configmaps,secrets,persistentvolumeclaims,serviceaccounts,roles,rolebindings,networkpolicies,leases", "-l", "ops.platform.io/test-release="+deepFlowTestRelease, "--ignore-not-found", "--wait=true", "--timeout=2m")
		deepFlowCommand(t, "kubectl", "--context", "orbstack", "delete", "clusterroles,clusterrolebindings", "-l", "ops.platform.io/test-release="+deepFlowTestRelease, "--ignore-not-found", "--wait=true", "--timeout=2m")
		remaining := deepFlowCommand(t, "kubectl", "--context", "orbstack", "-n", deepFlowTestNamespace, "get", "all,configmaps,secrets,persistentvolumeclaims,serviceaccounts,roles,rolebindings,networkpolicies,leases", "-o", "json")
		var resources struct {
			Items []struct {
				Kind     string `json:"kind"`
				Metadata struct {
					Name   string            `json:"name"`
					Labels map[string]string `json:"labels"`
				} `json:"metadata"`
			} `json:"items"`
		}
		if err := json.Unmarshal([]byte(remaining), &resources); err != nil {
			t.Fatalf("decode resources remaining in Task 2.5 namespace: %v", err)
		}
		for _, resource := range resources.Items {
			if resource.Metadata.Labels["ops.platform.io/test-release"] != deepFlowTestRelease {
				if (resource.Kind == "ServiceAccount" && resource.Metadata.Name == "default") || (resource.Kind == "ConfigMap" && resource.Metadata.Name == "kube-root-ca.crt") {
					continue
				}
				t.Fatalf("refusing to run PoC in namespace containing unowned resource %q", resource.Metadata.Name)
			}
		}
	} else if !strings.Contains(output, "NotFound") && !strings.Contains(output, "not found") && !strings.Contains(err.Error(), "NotFound") && !strings.Contains(err.Error(), "not found") {
		t.Fatalf("cannot verify Task 2.5 namespace status: %v: %s", err, strings.TrimSpace(output))
	}
	for _, resource := range []string{"clusterrole/ops-sp02-deepflow-agent", "clusterrolebinding/ops-sp02-deepflow-agent"} {
		output, err := deepFlowCommandResult(t, "kubectl", "--context", "orbstack", "get", resource, "-o", "json")
		if err == nil {
			var object struct {
				Metadata struct {
					Labels map[string]string `json:"labels"`
				} `json:"metadata"`
			}
			if json.Unmarshal([]byte(output), &object) != nil || object.Metadata.Labels["ops.platform.io/test-release"] != deepFlowTestRelease {
				t.Fatalf("refusing to replace pre-existing unowned cluster resource %s", resource)
			}
		} else if !strings.Contains(output, "NotFound") && !strings.Contains(output, "not found") && !strings.Contains(err.Error(), "NotFound") && !strings.Contains(err.Error(), "not found") {
			t.Fatalf("cannot verify ownership of cluster resource %s: %v: %s", resource, err, strings.TrimSpace(output))
		}
	}
	t.Cleanup(func() {
		deepFlowCommand(t, "kubectl", "--context", "orbstack", "-n", deepFlowTestNamespace, "delete", "all,configmaps,secrets,persistentvolumeclaims,serviceaccounts,roles,rolebindings,networkpolicies,leases", "-l", "ops.platform.io/test-release="+deepFlowTestRelease, "--ignore-not-found", "--wait=true", "--timeout=3m")
		deepFlowCommand(t, "kubectl", "--context", "orbstack", "delete", "clusterroles,clusterrolebindings", "-l", "ops.platform.io/test-release="+deepFlowTestRelease, "--ignore-not-found", "--wait=true", "--timeout=3m")
	})
}

func assertDeepFlowRuntimeResources(t *testing.T, selector string) {
	t.Helper()
	var pods struct {
		Items []struct {
			Metadata struct {
				Name   string            `json:"name"`
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Spec struct {
				Containers []struct {
					Name  string `json:"name"`
					Image string `json:"image"`
				} `json:"containers"`
			} `json:"spec"`
			Status struct {
				Phase             string `json:"phase"`
				ContainerStatuses []struct {
					Name  string `json:"name"`
					Ready bool   `json:"ready"`
					ID    string `json:"imageID"`
				} `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}
	output := deepFlowCommand(t, "kubectl", "--context", "orbstack", "-n", deepFlowTestNamespace, "get", "pods", "-l", selector, "-o", "json")
	if err := json.Unmarshal([]byte(output), &pods); err != nil || len(pods.Items) == 0 {
		t.Fatalf("decode DeepFlow Pod status: %v", err)
	}
	wantImages := map[string]string{"deepflow-agent": deepFlowAgentDigest, "mysql": deepFlowMySQLDigest, "deepflow-server": deepFlowServerDigest, "clickhouse": deepFlowCHDigest}
	ready := make(map[string]bool)
	for _, pod := range pods.Items {
		if pod.Status.Phase != "Running" || pod.Metadata.Labels["ops.platform.io/test-release"] != deepFlowTestRelease {
			t.Errorf("Pod %s phase or ownership label invalid: phase=%s labels=%v", pod.Metadata.Name, pod.Status.Phase, pod.Metadata.Labels)
		}
		for _, container := range pod.Spec.Containers {
			for component, digest := range wantImages {
				if strings.Contains(container.Image, component) {
					for _, status := range pod.Status.ContainerStatuses {
						if status.Name == container.Name {
							matched := status.Ready && strings.Contains(status.ID, "@sha256:"+digest)
							ready[component] = ready[component] || matched
							if !matched {
								t.Errorf("Pod %s imageID %q is not Ready at sha256:%s", pod.Metadata.Name, status.ID, digest)
							}
						}
					}
				}
			}
		}
	}
	for component := range wantImages {
		if !ready[component] {
			t.Errorf("no Ready Pod container found for %s at locked digest", component)
		}
	}
	objects := deepFlowCommand(t, "kubectl", "--context", "orbstack", "-n", deepFlowTestNamespace, "get", "deployments,daemonsets,statefulsets,services", "-o", "name")
	if strings.Contains(objects, "deepflow-app") {
		t.Fatalf("unexpected deepflow-app resource: %s", strings.TrimSpace(objects))
	}
	serviceTypes := deepFlowCommand(t, "kubectl", "--context", "orbstack", "-n", deepFlowTestNamespace, "get", "services", "-o", "jsonpath={range .items[*]}{.metadata.name}{\" \"}{.spec.type}{\"\\n\"}{end}")
	for _, line := range strings.Split(strings.TrimSpace(serviceTypes), "\n") {
		if parts := strings.Fields(line); len(parts) != 2 || parts[1] != "ClusterIP" {
			t.Errorf("DeepFlow Service is not ClusterIP-only: %q", line)
		}
	}
	policy := deepFlowCommand(t, "kubectl", "--context", "orbstack", "-n", deepFlowTestNamespace, "get", "networkpolicy", "deepflow-isolated", "-o", "jsonpath={.spec.egress}")
	if !strings.Contains(policy, "kube-system") || !strings.Contains(policy, deepFlowTestNamespace) {
		t.Fatalf("DeepFlow egress is not limited to internal services and DNS: %s", policy)
	}
}

type deepFlowEgressEvidence struct {
	PolicyName  string `json:"policyName"`
	ProbeImage  string `json:"probeImage"`
	Destination string `json:"destination"`
	PodPhase    string `json:"podPhase"`
	ExitCode    int    `json:"exitCode"`
	Log         string `json:"log"`
	Denied      bool   `json:"denied"`
}

func probeDeepFlowPublicEgress(t *testing.T, selector string) (bool, deepFlowEgressEvidence) {
	t.Helper()
	name := "sp02-deepflow-egress-probe"
	// The probe initially waits without opening a socket. Its policy rules
	// must exist before the exec below performs the connection attempt.
	probe := `sleep 600`
	deepFlowCommand(t, "kubectl", "--context", "orbstack", "-n", deepFlowTestNamespace, "run", name, "--image=docker.io/library/busybox:1.36.1@sha256:"+deepFlowBusyboxDigest, "--image-pull-policy=IfNotPresent", "--restart=Never", "--labels="+selector, "--command", "--", "sh", "-c", probe)
	waitDeepFlowPolicyRules(t, selector, 5)
	probeOutput, probeError := deepFlowCommandResult(t, "kubectl", "--context", "orbstack", "-n", deepFlowTestNamespace, "exec", name, "--", "sh", "-c", `if nc -z -w 4 1.1.1.1 80; then echo public-egress-connected; exit 10; else echo public-egress-denied; fi`)
	return probeError == nil && strings.Contains(probeOutput, "public-egress-denied"), deepFlowEgressEvidence{PolicyName: "deepflow-isolated", ProbeImage: "docker.io/library/busybox:1.36.1@sha256:" + deepFlowBusyboxDigest, Destination: "tcp://1.1.1.1:80", PodPhase: "Running", Log: strings.TrimSpace(probeOutput), Denied: probeError == nil && strings.Contains(probeOutput, "public-egress-denied")}
}

type deepFlowAgentRecord struct {
	Name           string `json:"NAME"`
	State          int    `json:"STATE"`
	PodClusterName string `json:"POD_CLUSTER_NAME"`
}

func waitForDeepFlowAgent(t *testing.T, client *http.Client, port int) deepFlowAgentRecord {
	t.Helper()
	deadline := time.Now().Add(8 * time.Minute)
	last := "no response"
	for time.Now().Before(deadline) {
		waitDeepFlowPolicyRules(t, "ops.platform.io/test-release="+deepFlowTestRelease+",component", 4)
		request, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/v1/vtaps/", port), nil)
		request.Header.Set("X-Org-Id", "1")
		response, err := client.Do(request)
		if err == nil {
			body, readErr := io.ReadAll(response.Body)
			response.Body.Close()
			if readErr == nil && response.StatusCode == http.StatusOK {
				var envelope struct {
					OptStatus string                `json:"OPT_STATUS"`
					Data      []deepFlowAgentRecord `json:"DATA"`
				}
				if json.Unmarshal(body, &envelope) == nil {
					for _, agent := range envelope.Data {
						if agent.State == 1 && agent.PodClusterName == "ops-sp02-deepflow-poc" {
							return agent
						}
					}
					last = fmt.Sprintf("status=%s agents=%s", envelope.OptStatus, strings.TrimSpace(string(body)))
				}
			} else {
				last = fmt.Sprintf("HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
			}
		} else {
			last = err.Error()
		}
		time.Sleep(5 * time.Second)
	}
	t.Fatalf("DeepFlow Agent did not register RUNNING in the PoC cluster: %s", last)
	return deepFlowAgentRecord{}
}

type deepFlowQuerySample struct {
	Columns  []string       `json:"columns"`
	RowCount int            `json:"rowCount"`
	Sample   map[string]any `json:"sample"`
}

func waitForDeepFlowQuery(t *testing.T, client *http.Client, port int) (string, deepFlowQuerySample) {
	t.Helper()
	query := "SELECT ip_0, ip_1, server_port, flow_id, retrans_tx, retrans_rx FROM l4_flow_log WHERE server_port = 30130 LIMIT 20"
	deadline := time.Now().Add(6 * time.Minute)
	last := "no response"
	for time.Now().Before(deadline) {
		waitDeepFlowPolicyRules(t, "ops.platform.io/test-release="+deepFlowTestRelease+",component", 4)
		form := url.Values{"db": {"flow_log"}, "sql": {query}}
		request, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/v1/query/", port), strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("X-Org-Id", "1")
		response, err := client.Do(request)
		if err == nil {
			body, readErr := io.ReadAll(response.Body)
			response.Body.Close()
			if readErr == nil && response.StatusCode == http.StatusOK {
				var envelope struct {
					OptStatus   string `json:"OPT_STATUS"`
					Description string `json:"DESCRIPTION"`
					Result      struct {
						Columns []string `json:"columns"`
						Values  [][]any  `json:"values"`
					} `json:"result"`
				}
				if json.Unmarshal(body, &envelope) == nil && envelope.OptStatus == "SUCCESS" {
					last = fmt.Sprintf("status=%s description=%s columns=%v rows=%d", envelope.OptStatus, envelope.Description, envelope.Result.Columns, len(envelope.Result.Values))
					if sample, ok := validateDeepFlowQueryRows(envelope.Result.Columns, envelope.Result.Values); ok {
						return query, sample
					}
				} else {
					last = fmt.Sprintf("HTTP %d status=%s description=%s body=%s", response.StatusCode, envelope.OptStatus, envelope.Description, strings.TrimSpace(string(body)))
				}
			} else {
				last = fmt.Sprintf("HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
			}
		} else {
			last = err.Error()
		}
		time.Sleep(15 * time.Second)
	}
	t.Fatalf("DeepFlow Querier did not return dependency endpoints and retransmission fields: %s", last)
	return query, deepFlowQuerySample{}
}

func validateDeepFlowQueryRows(columns []string, rows [][]any) (deepFlowQuerySample, bool) {
	indices := make(map[string]int, len(columns))
	for index, column := range columns {
		indices[column] = index
	}
	for _, required := range []string{"ip_0", "ip_1", "server_port", "flow_id", "retrans_tx", "retrans_rx"} {
		if _, ok := indices[required]; !ok {
			return deepFlowQuerySample{}, false
		}
	}
	for _, row := range rows {
		if len(row) < len(columns) || fmt.Sprint(row[indices["ip_0"]]) == "" || fmt.Sprint(row[indices["ip_1"]]) == "" || fmt.Sprint(row[indices["server_port"]]) != "30130" {
			continue
		}
		if _, err := netip.ParseAddr(fmt.Sprint(row[indices["ip_0"]])); err != nil {
			continue
		}
		if _, err := netip.ParseAddr(fmt.Sprint(row[indices["ip_1"]])); err != nil {
			continue
		}
		if row[indices["flow_id"]] == nil {
			continue
		}
		tx, txOK := row[indices["retrans_tx"]].(float64)
		rx, rxOK := row[indices["retrans_rx"]].(float64)
		if !txOK || !rxOK || tx < 0 || rx < 0 {
			continue
		}
		return deepFlowQuerySample{Columns: columns, RowCount: len(rows), Sample: map[string]any{
			"sourceEndpointObserved": true, "destinationEndpointObserved": true,
			"server_port": row[indices["server_port"]], "flow_id_present": row[indices["flow_id"]] != nil,
			"retrans_tx": row[indices["retrans_tx"]], "retrans_rx": row[indices["retrans_rx"]],
		}}, true
	}
	return deepFlowQuerySample{Columns: columns, RowCount: len(rows)}, false
}

func writeDeepFlowFixture(root string, agent deepFlowAgentRecord, query string, sample deepFlowQuerySample) error {
	fixture := map[string]any{
		"apiVersion": "deepflow.io/v7.2.0/query-fixture/v1", "sourceCommit": "e567b167453ffa99f08f26def20379b4f831e073",
		"observedAgent": map[string]any{"registered": true, "state": agent.State, "cluster": agent.PodClusterName},
		"query":         query, "querierStatus": "SUCCESS", "networkEvidence": true, "networkEvidenceMode": "live", "querySample": sample,
		"l7Context": false, "traceCompletion": false, "capturedAtUTC": time.Now().UTC().Format(time.RFC3339),
		"privacyRedaction": "endpoint IPs and flow identifiers are not retained; counters and port are copied from the live response",
	}
	contents, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		return err
	}
	directory := filepath.Join(root, "test", "fixtures", "deepflow", "v7.2.0")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, "querier-l4-flow-live.json"), append(contents, '\n'), 0o644)
}

func deepFlowPortForward(t *testing.T, controllerPort, querierPort int) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	command := exec.CommandContext(ctx, "kubectl", "--context", "orbstack", "-n", deepFlowTestNamespace, "port-forward", "--address", "127.0.0.1", "service/ops-sp02-deepflow-server", fmt.Sprintf("%d:20417", controllerPort), fmt.Sprintf("%d:20416", querierPort))
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Start(); err != nil {
		cancel()
		t.Fatalf("start temporary loopback-only DeepFlow port-forward: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		controller, controllerErr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", controllerPort), 250*time.Millisecond)
		querier, querierErr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", querierPort), 250*time.Millisecond)
		if controllerErr == nil && querierErr == nil {
			controller.Close()
			querier.Close()
			return func() {
				cancel()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					_ = command.Process.Kill()
					<-done
				}
			}
		}
		if controller != nil {
			controller.Close()
		}
		if querier != nil {
			querier.Close()
		}
		select {
		case err := <-done:
			t.Fatalf("DeepFlow port-forward exited early: %v; stdout=%s stderr=%s", err, strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()))
		default:
		}
		time.Sleep(250 * time.Millisecond)
	}
	cancel()
	t.Fatalf("DeepFlow port-forward did not bind loopback ports: stdout=%s stderr=%s", strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()))
	return func() {}
}

func deepFlowReservePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve local port for DeepFlow port-forward: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("close reserved local port: %v", err)
	}
	return port
}

func deepFlowCommand(t *testing.T, name string, args ...string) string {
	t.Helper()
	output, err := deepFlowCommandResult(t, name, args...)
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, strings.TrimSpace(output))
	}
	return output
}

func deepFlowCommandTimeout(t *testing.T, timeout time.Duration, name string, args ...string) string {
	t.Helper()
	output, err := deepFlowCommandTimeoutResult(t, timeout, name, args...)
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, strings.TrimSpace(output))
	}
	return output
}

func deepFlowCommandResult(t *testing.T, name string, args ...string) (string, error) {
	t.Helper()
	return deepFlowCommandTimeoutResult(t, 12*time.Minute, name, args...)
}

func deepFlowCommandTimeoutResult(t *testing.T, timeout time.Duration, name string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, name, args...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return string(output) + stderr.String(), fmt.Errorf("%w", err)
	}
	return string(output), nil
}
