package bundle

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
	"ops-platform/internal/contract"
	"ops-platform/internal/profile"
)

const MetricsAddonRelease = "ops-metrics-server"
const metricsAddonName = MetricsAddonRelease + "-metrics"
const metricsAddonImage = "registry.k8s.io/metrics-server/metrics-server@sha256:8f49cf1b0688bb0eae18437882dbf6de2c7a2baac71b1492bc4eca25439a1bf2"

// Separate install input; no additions to the frozen Deployment Profile/v1.
// The CA is public. Its private key and serving identity stay outside bundles.
type MetricsAddonValues struct {
	SchemaVersion      int      `json:"schemaVersion"`
	Namespace          string   `json:"namespace"`
	ServingTLSSecret   string   `json:"servingTLSSecret"`
	ServingCA          string   `json:"servingCA"`
	KubeletCAConfigMap string   `json:"kubeletCAConfigMap"`
	NodeCIDRs          []string `json:"nodeCIDRs"`
	APIServerCIDRs     []string `json:"apiServerCIDRs"`
	APIServerPort      int      `json:"apiServerPort"`
}

func ReadMetricsAddonValues(r io.Reader) (MetricsAddonValues, error) {
	var c MetricsAddonValues
	raw, err := io.ReadAll(io.LimitReader(r, (128<<10)+1))
	if err != nil || len(raw) > 128<<10 {
		return c, errors.New("Metrics addon input unavailable or oversized")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF {
		return c, errors.New("Metrics addon requires one strict JSON document")
	}
	if err := contract.Validate("https://ops.local/schemas/metrics-addon/v1", raw); err != nil {
		return c, errors.New("Metrics addon inputs do not satisfy metrics-addon/v1")
	}
	return c, c.Validate()
}
func (c MetricsAddonValues) Validate() error {
	if c.SchemaVersion != 1 || !releaseNamePattern.MatchString(c.Namespace) || len(c.Namespace) > 63 || strings.HasPrefix(c.Namespace, "kube-") || c.Namespace == "default" || !releaseNamePattern.MatchString(c.ServingTLSSecret) || !releaseNamePattern.MatchString(c.KubeletCAConfigMap) || c.APIServerPort < 1 || c.APIServerPort > 65535 {
		return errors.New("explicit Metrics addon namespace, trust references and API port required")
	}
	if _, err := metricsCA(c.ServingCA); err != nil {
		return err
	}
	for _, list := range [][]string{c.NodeCIDRs, c.APIServerCIDRs} {
		if len(list) == 0 || len(list) > 64 {
			return errors.New("explicit bounded Metrics network required")
		}
		seen := map[string]bool{}
		for _, s := range list {
			ip, n, err := net.ParseCIDR(s)
			if err != nil {
				return errors.New("invalid Metrics CIDR")
			}
			ones, bits := n.Mask.Size()
			if !ip.IsPrivate() || !ip.Equal(n.IP) || ones != bits || seen[s] || n.String() != s {
				return errors.New("Metrics network requires unique exact canonical private addresses")
			}
			seen[s] = true
		}
	}
	return nil
}
func metricsCA(raw string) (*x509.CertPool, error) {
	rest := []byte(raw)
	pool := x509.NewCertPool()
	count := 0
	for len(bytes.TrimSpace(rest)) > 0 {
		b, next := pem.Decode(rest)
		if b == nil || b.Type != "CERTIFICATE" {
			return nil, errors.New("Metrics trust must contain only CA certificates")
		}
		cert, err := x509.ParseCertificate(b.Bytes)
		if err != nil || !cert.IsCA || time.Now().Before(cert.NotBefore) || !time.Now().Before(cert.NotAfter) {
			return nil, errors.New("Metrics CA invalid or expired")
		}
		pool.AddCert(cert)
		rest = next
		count++
	}
	if count == 0 {
		return nil, errors.New("independent Metrics serving CA required")
	}
	return pool, nil
}
func (c MetricsAddonValues) chartValues() map[string]any {
	return map[string]any{"image": metricsAddonImage, "servingTLSSecret": c.ServingTLSSecret, "servingCA": c.ServingCA, "kubeletCAConfigMap": c.KubeletCAConfigMap, "nodeCIDRs": c.NodeCIDRs, "apiServerCIDRs": c.APIServerCIDRs, "apiServerPort": c.APIServerPort, "networkPolicyOnly": false}
}

func metricsNamespace(ctx context.Context, p profile.ResolvedProfile, c MetricsAddonValues, run CommandRunner) (namespaceIdentity, error) {
	raw, err := run(ctx, "kubectl", "--context", p.Kubernetes.Context, "get", "namespace", c.Namespace, "-o", "json")
	var ns map[string]any
	if err != nil || json.Unmarshal(raw, &ns) != nil {
		return namespaceIdentity{}, errors.New("Metrics namespace unavailable")
	}
	m := object(ns, "metadata")
	labels := object(m, "labels")
	uid := textValue(m, "uid")
	id := textValue(labels, "ops.platform.io/installation-id")
	if uid == "" || textValue(labels, "ops.platform.io/managed-by") != "opsctl-bootstrap" {
		return namespaceIdentity{}, errors.New("Metrics namespace must come from formal environment bootstrap")
	}
	if _, err := uuid.Parse(id); err != nil {
		return namespaceIdentity{}, errors.New("Metrics installation identity unavailable")
	}
	return namespaceIdentity{UID: uid, InstallationID: id}, nil
}
func metricsTrust(ctx context.Context, p profile.ResolvedProfile, c MetricsAddonValues, ns namespaceIdentity, run CommandRunner) error {
	roots, err := metricsCA(c.ServingCA)
	if err != nil {
		return err
	}
	raw, err := run(ctx, "kubectl", "--context", p.Kubernetes.Context, "-n", c.Namespace, "get", "secret", c.ServingTLSSecret, "-o", "json")
	var secret struct {
		Metadata struct {
			UID    string            `json:"uid"`
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
		Type string            `json:"type"`
		Data map[string]string `json:"data"`
	}
	if err != nil || len(raw) > 128<<10 || json.Unmarshal(raw, &secret) != nil || secret.Type != "kubernetes.io/tls" || secret.Metadata.UID == "" || secret.Metadata.Labels["ops.platform.io/installation-id"] != ns.InstallationID {
		return errors.New("owned Metrics serving identity required")
	}
	certBytes, e1 := base64.StdEncoding.DecodeString(secret.Data["tls.crt"])
	keyBytes, e2 := base64.StdEncoding.DecodeString(secret.Data["tls.key"])
	if e1 != nil || e2 != nil {
		return errors.New("Metrics serving identity invalid")
	}
	if err := validateMetricsServingIdentity(c, roots, certBytes, keyBytes); err != nil {
		return err
	}
	raw, err = run(ctx, "kubectl", "--context", p.Kubernetes.Context, "-n", c.Namespace, "get", "configmap", c.KubeletCAConfigMap, "-o", "json")
	var cm struct {
		Metadata struct {
			UID    string            `json:"uid"`
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
		Data map[string]string `json:"data"`
	}
	if err != nil || len(raw) > 128<<10 || json.Unmarshal(raw, &cm) != nil || cm.Metadata.UID == "" || cm.Metadata.Labels["ops.platform.io/installation-id"] != ns.InstallationID {
		return errors.New("owned independent kubelet CA required")
	}
	_, err = metricsCA(cm.Data["ca.crt"])
	return err
}
func metricsDiscoveredNetwork(ctx context.Context, p profile.ResolvedProfile, c MetricsAddonValues, run CommandRunner) error {
	raw, err := run(ctx, "kubectl", "--context", p.Kubernetes.Context, "get", "nodes", "-o", "json")
	var nodes struct {
		Items []struct {
			Metadata struct {
				UID string `json:"uid"`
			} `json:"metadata"`
			Status struct {
				Addresses []struct {
					Type    string `json:"type"`
					Address string `json:"address"`
				} `json:"addresses"`
				DaemonEndpoints struct {
					KubeletEndpoint struct {
						Port int `json:"port"`
					} `json:"kubeletEndpoint"`
				} `json:"daemonEndpoints"`
			} `json:"status"`
		} `json:"items"`
	}
	if err != nil || len(raw) > 4<<20 || json.Unmarshal(raw, &nodes) != nil || len(nodes.Items) == 0 {
		return errors.New("current Node discovery unavailable")
	}
	nodeSet := map[string]bool{}
	exact := func(s string) (string, error) {
		ip := net.ParseIP(s)
		if ip == nil || !ip.IsPrivate() {
			return "", errors.New("discovered Metrics address outside private range")
		}
		bits := 128
		if ip.To4() != nil {
			bits = 32
		}
		return fmt.Sprintf("%s/%d", ip.String(), bits), nil
	}
	for _, n := range nodes.Items {
		if n.Metadata.UID == "" || n.Status.DaemonEndpoints.KubeletEndpoint.Port != 10250 {
			return errors.New("Node UID or admitted kubelet port differs")
		}
		for _, a := range n.Status.Addresses {
			if a.Type == "InternalIP" {
				s, err := exact(a.Address)
				if err != nil {
					return err
				}
				nodeSet[s] = true
			}
		}
	}
	if !sameMetricsAddresses(nodeSet, c.NodeCIDRs) {
		return errors.New("Metrics Node addresses differ from current discovery")
	}

	raw, err = run(ctx, "kubectl", "--context", p.Kubernetes.Context, "-n", "default", "get", "endpointslices", "--selector=kubernetes.io/service-name=kubernetes", "-o", "json")
	var slices struct {
		Items []struct {
			AddressType string `json:"addressType"`
			Endpoints   []struct {
				Addresses  []string `json:"addresses"`
				Conditions struct {
					Ready *bool `json:"ready"`
				} `json:"conditions"`
			} `json:"endpoints"`
			Ports []struct {
				Name     string `json:"name"`
				Protocol string `json:"protocol"`
				Port     int    `json:"port"`
			} `json:"ports"`
		} `json:"items"`
	}
	if err != nil || len(raw) > 4<<20 || json.Unmarshal(raw, &slices) != nil || len(slices.Items) == 0 {
		return errors.New("current API EndpointSlices unavailable")
	}
	apiSet := map[string]bool{}
	for _, slice := range slices.Items {
		if (slice.AddressType != "IPv4" && slice.AddressType != "IPv6") || len(slice.Ports) != 1 || slice.Ports[0].Name != "https" || slice.Ports[0].Protocol != "TCP" || slice.Ports[0].Port != c.APIServerPort {
			return errors.New("Metrics API port/address type differs from current EndpointSlice")
		}
		for _, endpoint := range slice.Endpoints {
			if endpoint.Conditions.Ready != nil && !*endpoint.Conditions.Ready {
				continue
			}
			for _, address := range endpoint.Addresses {
				ip := net.ParseIP(address)
				if ip == nil || (ip.To4() != nil) != (slice.AddressType == "IPv4") {
					return errors.New("Metrics EndpointSlice address family drift")
				}
				cidr, err := exact(address)
				if err != nil {
					return err
				}
				apiSet[cidr] = true
			}
		}
	}
	if len(apiSet) == 0 {
		return errors.New("Metrics API has no ready discovered endpoint")
	}
	raw, err = run(ctx, "kubectl", "--context", p.Kubernetes.Context, "-n", "default", "get", "service", "kubernetes", "-o", "json")
	var svc struct {
		Spec struct {
			ClusterIPs []string `json:"clusterIPs"`
		} `json:"spec"`
	}
	if err != nil || len(raw) > 128<<10 || json.Unmarshal(raw, &svc) != nil || len(svc.Spec.ClusterIPs) == 0 {
		return errors.New("current API service addresses unavailable")
	}
	for _, address := range svc.Spec.ClusterIPs {
		cidr, err := exact(address)
		if err != nil {
			return err
		}
		apiSet[cidr] = true
	}
	if !sameMetricsAddresses(apiSet, c.APIServerCIDRs) {
		return errors.New("Metrics API addresses differ from current discovery")
	}
	return nil
}
func sameMetricsAddresses(expected map[string]bool, actual []string) bool {
	if len(expected) != len(actual) {
		return false
	}
	for _, s := range actual {
		if !expected[s] {
			return false
		}
	}
	return true
}

// InstallMetricsAddon authenticates the complete Bundle before any mutation,
// preflights current trust and discovery, refuses existing global resources, and
// starts only the locked upstream process. Investigation acceptance is separate.
func InstallMetricsAddon(ctx context.Context, m Manifest, trust TrustRoot, p profile.ResolvedProfile, runtime RuntimeImporter, run CommandRunner, c MetricsAddonValues) (ImportReport, error) {
	if run == nil {
		return ImportReport{}, errors.New("Metrics installation runner required")
	}
	if err := c.Validate(); err != nil {
		return ImportReport{}, err
	}
	v, err := prepare(ctx, m, trust)
	if err != nil {
		return ImportReport{}, fmt.Errorf("checkpoint=verify-bundle: %w", err)
	}
	defer v.close()
	if err := validateInstallProfile(p, m.Architecture); err != nil {
		return ImportReport{}, err
	}
	component := p.Components["metrics-server"]
	if component.Mode != "bundled" || component.AdmissionState != "qualified" || component.Image != metricsAddonImage || component.Version != "v0.8.0" {
		return ImportReport{}, errors.New("explicit qualified bundled Metrics Profile component required")
	}
	var image ImageArtifact
	chart := ""
	for _, material := range v.manifest.Materials {
		if material.Name == "metrics-server" && material.Kind == "container-image" {
			path := filepath.Join(v.path, filepath.FromSlash(material.PayloadRef))
			digest, _, err := validateOCIArchive(path, m.Architecture)
			if err != nil || digest != component.Digest {
				return ImportReport{}, errors.New("Metrics OCI differs from resolved lock")
			}
			image = ImageArtifact{Name: material.Name, Path: path, Reference: metricsAddonImage, Digest: digest, Architecture: m.Architecture}
		}
		if material.Name == "ops-metrics-server-chart" && material.Kind == "chart" {
			chart = filepath.Join(v.path, filepath.FromSlash(material.PayloadRef))
		}
	}
	if image.Path == "" || chart == "" {
		return ImportReport{}, errors.New("Metrics OCI, corresponding source and first-party Chart required in signed Bundle")
	}
	ns, err := metricsNamespace(ctx, p, c, run)
	if err != nil {
		return ImportReport{}, err
	}
	if err := metricsTrust(ctx, p, c, ns, run); err != nil {
		return ImportReport{}, err
	}
	if err := metricsDiscoveredNetwork(ctx, p, c, run); err != nil {
		return ImportReport{}, err
	}
	if err := requireAbsentRelease(ctx, c.Namespace, p, MetricsAddonRelease, run); err != nil {
		return ImportReport{}, err
	}
	values := filepath.Join(v.root, "metrics-addon-values.yaml")
	encoded, err := yaml.Marshal(c.chartValues())
	if err != nil {
		return ImportReport{}, err
	}
	if err := os.WriteFile(values, encoded, 0600); err != nil {
		return ImportReport{}, err
	}
	raw, err := run(ctx, "helm", "template", MetricsAddonRelease, chart, "--namespace", c.Namespace, "--values", values)
	if err != nil {
		return ImportReport{}, err
	}
	if err := validateMetricsChart(ctx, raw, c, p, run); err != nil {
		return ImportReport{}, err
	}
	policy, err := run(ctx, "helm", "template", MetricsAddonRelease, chart, "--namespace", c.Namespace, "--values", values, "--set", "networkPolicyOnly=true")
	if err != nil {
		return ImportReport{}, err
	}
	if err := validateMetricsPolicy(policy, c); err != nil {
		return ImportReport{}, err
	}
	report, err := importImages(ctx, m.BundleID, p, []ImageArtifact{image}, runtime)
	if err != nil {
		return report, err
	}
	for _, phase := range []string{"true", "false"} {
		if _, err := run(ctx, "helm", "upgrade", "--install", MetricsAddonRelease, chart, "--kube-context", p.Kubernetes.Context, "--namespace", c.Namespace, "--values", values, "--set", "networkPolicyOnly="+phase, "--wait", "--timeout", "5m"); err != nil {
			return report, fmt.Errorf("checkpoint=metrics-install phase=%s: %w", phase, err)
		}
	}
	if _, err := run(ctx, "kubectl", "--context", p.Kubernetes.Context, "-n", c.Namespace, "rollout", "status", "deployment/"+metricsAddonName, "--timeout=120s"); err != nil {
		return report, err
	}
	if _, err := run(ctx, "kubectl", "--context", p.Kubernetes.Context, "wait", "--for=condition=Available", "apiservice/v1beta1.metrics.k8s.io", "--timeout=120s"); err != nil {
		return report, err
	}
	return report, nil
}
func validateMetricsChart(ctx context.Context, raw []byte, c MetricsAddonValues, p profile.ResolvedProfile, run CommandRunner) error {
	expected := map[string]string{"ServiceAccount/" + metricsAddonName: c.Namespace, "ClusterRole/" + metricsAddonName: "", "ClusterRoleBinding/" + metricsAddonName: "", "ClusterRoleBinding/" + metricsAddonName + "-auth-delegator": "", "RoleBinding/" + metricsAddonName + "-auth-reader": "kube-system", "Service/" + metricsAddonName: c.Namespace, "Deployment/" + metricsAddonName: c.Namespace, "APIService/v1beta1.metrics.k8s.io": "", "NetworkPolicy/" + metricsAddonName: c.Namespace}
	seen := map[string]bool{}
	d := yaml.NewDecoder(bytes.NewReader(raw))
	for {
		var obj map[string]any
		if err := d.Decode(&obj); err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		if len(obj) == 0 {
			continue
		}
		kind := textValue(obj, "kind")
		meta := object(obj, "metadata")
		name := textValue(meta, "name")
		id := kind + "/" + name
		ns, ok := expected[id]
		if !ok || seen[id] || textValue(meta, "namespace") != ns || textValue(object(meta, "labels"), "ops.platform.io/release") != MetricsAddonRelease || object(meta, "annotations")["helm.sh/hook"] != nil {
			return errors.New("Metrics chart resource outside fixed scope")
		}
		seen[id] = true
		if kind == "APIService" {
			spec := object(obj, "spec")
			if spec["insecureSkipTLSVerify"] != nil || textValue(spec, "caBundle") != base64.StdEncoding.EncodeToString([]byte(c.ServingCA)) || textValue(object(spec, "service"), "namespace") != c.Namespace || textValue(object(spec, "service"), "name") != metricsAddonName {
				return errors.New("Metrics aggregation trust differs")
			}
		}
		if kind == "APIService" {
			expectedSpec := map[string]any{"group": "metrics.k8s.io", "version": "v1beta1", "groupPriorityMinimum": 100, "versionPriority": 100, "caBundle": base64.StdEncoding.EncodeToString([]byte(c.ServingCA)), "service": map[string]any{"name": metricsAddonName, "namespace": c.Namespace}}
			if jsonDigest(obj["spec"]) != jsonDigest(expectedSpec) {
				return errors.New("Metrics APIService differs from fixed aggregation contract")
			}
		}
		if kind == "NetworkPolicy" && jsonDigest(obj["spec"]) != jsonDigest(metricsNetworkSpec(c)) {
			return errors.New("Metrics policy scope differs from explicit discovered addresses")
		}

		if kind == "ClusterRole" {
			expectedRules := []any{map[string]any{"apiGroups": []any{""}, "resources": []any{"nodes/metrics"}, "verbs": []any{"get"}}, map[string]any{"apiGroups": []any{""}, "resources": []any{"pods", "nodes"}, "verbs": []any{"get", "list", "watch"}}}
			if jsonDigest(obj["rules"]) != jsonDigest(expectedRules) {
				return errors.New("Metrics RBAC wider than upstream minimal observations")
			}
		}
		if kind == "ClusterRoleBinding" || kind == "RoleBinding" {
			roleKind, roleName := "ClusterRole", metricsAddonName
			if name == metricsAddonName+"-auth-delegator" {
				roleName = "system:auth-delegator"
			}
			if kind == "RoleBinding" {
				roleKind = "Role"
				roleName = "extension-apiserver-authentication-reader"
			}
			if jsonDigest(obj["roleRef"]) != jsonDigest(map[string]any{"apiGroup": "rbac.authorization.k8s.io", "kind": roleKind, "name": roleName}) || jsonDigest(obj["subjects"]) != jsonDigest([]any{map[string]any{"kind": "ServiceAccount", "name": metricsAddonName, "namespace": c.Namespace}}) {
				return errors.New("Metrics RBAC binding outside fixed identity")
			}
		}
		if kind == "Deployment" {
			pod := object(object(obj, "spec"), "template")
			spec := object(pod, "spec")
			containers, ok := spec["containers"].([]any)
			if !ok || len(containers) != 1 {
				return errors.New("Metrics requires single upstream container")
			}
			container := containers[0].(map[string]any)
			if textValue(container, "image") != metricsAddonImage || textValue(container, "imagePullPolicy") != "Never" || spec["hostNetwork"] == true || spec["hostPID"] == true || spec["initContainers"] != nil {
				return errors.New("Metrics workload outside offline boundary")
			}
			args, _ := container["args"].([]any)
			for _, arg := range args {
				if arg == "--kubelet-insecure-tls" {
					return errors.New("Metrics kubelet TLS may not be relaxed")
				}
			}
		}
		if kind == "Deployment" {
			spec := object(object(object(obj, "spec"), "template"), "spec")
			container := spec["containers"].([]any)[0].(map[string]any)
			expectedArgs := []any{"--secure-port=10250", "--tls-cert-file=/etc/metrics-serving/tls.crt", "--tls-private-key-file=/etc/metrics-serving/tls.key", "--kubelet-certificate-authority=/etc/kubelet-ca/ca.crt", "--kubelet-preferred-address-types=InternalIP", "--kubelet-use-node-status-port", "--metric-resolution=15s"}
			expectedSecurity := map[string]any{"runAsNonRoot": true, "runAsUser": 1000, "allowPrivilegeEscalation": false, "readOnlyRootFilesystem": true, "capabilities": map[string]any{"drop": []any{"ALL"}}, "seccompProfile": map[string]any{"type": "RuntimeDefault"}}
			expectedVolumes := []any{map[string]any{"name": "serving", "secret": map[string]any{"secretName": c.ServingTLSSecret, "defaultMode": 288}}, map[string]any{"name": "kubelet-ca", "configMap": map[string]any{"name": c.KubeletCAConfigMap}}}
			expectedMounts := []any{map[string]any{"name": "serving", "mountPath": "/etc/metrics-serving", "readOnly": true}, map[string]any{"name": "kubelet-ca", "mountPath": "/etc/kubelet-ca", "readOnly": true}}
			if textValue(spec, "serviceAccountName") != metricsAddonName || jsonDigest(container["args"]) != jsonDigest(expectedArgs) || jsonDigest(container["securityContext"]) != jsonDigest(expectedSecurity) || jsonDigest(spec["volumes"]) != jsonDigest(expectedVolumes) || jsonDigest(container["volumeMounts"]) != jsonDigest(expectedMounts) || container["env"] != nil || container["envFrom"] != nil || container["command"] != nil {
				return errors.New("Metrics process identity, arguments, mounts or isolation differ from fixed contract")
			}
		}

		args := []string{"--context", p.Kubernetes.Context, "get", kind, name, "--ignore-not-found", "-o", "json"}
		if ns != "" {
			args = append(args, "-n", ns)
		}
		existing, err := run(ctx, "kubectl", args...)
		if err != nil {
			return errors.New("Metrics ownership discovery failed")
		}
		if len(bytes.TrimSpace(existing)) != 0 {
			return fmt.Errorf("PROFILE_COMPONENT_CONFLICT: Metrics resource %s already exists; refusing adoption", id)
		}
	}
	if len(seen) != len(expected) {
		return errors.New("Metrics chart fixed resource set incomplete")
	}
	return nil
}

func ValidateMetricsAddonIdentity(c MetricsAddonValues, certBytes, keyBytes []byte, kubeletCA string) error {
	if err := c.Validate(); err != nil {
		return err
	}
	roots, err := metricsCA(c.ServingCA)
	if err != nil {
		return err
	}
	if err := validateMetricsServingIdentity(c, roots, certBytes, keyBytes); err != nil {
		return err
	}
	_, err = metricsCA(kubeletCA)
	return err
}
func validateMetricsServingIdentity(c MetricsAddonValues, roots *x509.CertPool, certBytes, keyBytes []byte) error {
	pair, err := tls.X509KeyPair(certBytes, keyBytes)
	if err != nil || len(pair.Certificate) == 0 {
		return errors.New("Metrics serving identity invalid")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return errors.New("Metrics serving certificate invalid")
	}
	intermediates := x509.NewCertPool()
	for _, b := range pair.Certificate[1:] {
		cert, err := x509.ParseCertificate(b)
		if err != nil {
			return errors.New("Metrics serving chain invalid")
		}
		intermediates.AddCert(cert)
	}
	for _, dns := range []string{metricsAddonName + "." + c.Namespace + ".svc", metricsAddonName + "." + c.Namespace + ".svc.cluster.local"} {
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, DNSName: dns, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
			return errors.New("Metrics serving certificate does not match independent CA and service SAN")
		}
	}
	return nil
}

func metricsNetworkSpec(c MetricsAddonValues) map[string]any {
	blocks := func(cidrs []string) []any {
		out := make([]any, 0, len(cidrs))
		for _, cidr := range cidrs {
			out = append(out, map[string]any{"ipBlock": map[string]any{"cidr": cidr}})
		}
		return out
	}
	ports := func(port int) []any { return []any{map[string]any{"protocol": "TCP", "port": port}} }
	return map[string]any{"podSelector": map[string]any{"matchLabels": map[string]any{"app.kubernetes.io/name": metricsAddonName}}, "policyTypes": []any{"Ingress", "Egress"}, "ingress": []any{map[string]any{"from": blocks(c.APIServerCIDRs), "ports": ports(10250)}}, "egress": []any{map[string]any{"to": blocks(c.NodeCIDRs), "ports": ports(10250)}, map[string]any{"to": blocks(c.APIServerCIDRs), "ports": ports(c.APIServerPort)}}}
}

func validateMetricsPolicy(raw []byte, c MetricsAddonValues) error {
	d := yaml.NewDecoder(bytes.NewReader(raw))
	count := 0
	for {
		var obj map[string]any
		if err := d.Decode(&obj); err == io.EOF {
			break
		} else if err != nil {
			return err
		}
		if len(obj) == 0 {
			continue
		}
		meta := object(obj, "metadata")
		if textValue(obj, "kind") != "NetworkPolicy" || textValue(meta, "name") != metricsAddonName || textValue(meta, "namespace") != c.Namespace || textValue(object(meta, "labels"), "ops.platform.io/release") != MetricsAddonRelease || object(meta, "annotations")["helm.sh/hook"] != nil || jsonDigest(obj["spec"]) != jsonDigest(metricsNetworkSpec(c)) {
			return errors.New("Metrics policy bootstrap outside fixed discovered network")
		}
		count++
	}
	if count != 1 {
		return errors.New("Metrics bootstrap requires exactly one fixed policy")
	}
	return nil
}
