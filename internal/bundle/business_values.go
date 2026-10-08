package bundle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"ops-platform/internal/contract"
	"ops-platform/internal/profile"
	"ops-platform/internal/resource"
)

// BusinessValues is the versioned, secret-free operator input for the current
// nonvirtual delivery. It is separate from the frozen Deployment Profile/v1.
// Only ReadBusinessValues can construct a validated value.
type BusinessValues struct {
	values               map[string]any
	installStage         string
	registrationVerified bool
	bootstrapResources   map[string]string
}

func (b BusinessValues) Namespace() string { return textValue(b.values, "namespace") }

func (b BusinessValues) Tenants() []string {
	var result []string
	if b.values == nil {
		return result
	}
	for _, tenant := range object(b.values, "sp06")["tenants"].([]any) {
		result = append(result, tenant.(string))
	}
	return result
}

func ReadBusinessValues(r io.Reader) (BusinessValues, error) {
	raw, err := io.ReadAll(io.LimitReader(r, (256<<10)+1))
	if err != nil || len(raw) > 256<<10 {
		return BusinessValues{}, errors.New("business values unavailable or oversized")
	}
	var document map[string]any
	d := yaml.NewDecoder(bytes.NewReader(raw))
	if d.Decode(&document) != nil {
		return BusinessValues{}, errors.New("business values must be one YAML/JSON object")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return BusinessValues{}, errors.New("business values must contain exactly one document")
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return BusinessValues{}, errors.New("business values invalid")
	}
	if contract.Validate("https://ops.local/schemas/installation-business-values/v1", encoded) != nil {
		return BusinessValues{}, errors.New("business values do not satisfy installation-business-values/v1; all SP04–06 inputs must be explicit")
	}
	// Normalize numbers and collections so JSON and YAML have identical semantics.
	if json.Unmarshal(encoded, &document) != nil {
		return BusinessValues{}, errors.New("business values invalid")
	}
	if err := validateBusinessNetwork(document); err != nil {
		return BusinessValues{}, err
	}
	return BusinessValues{values: document}, nil
}
func object(m map[string]any, k string) map[string]any { v, _ := m[k].(map[string]any); return v }
func textValue(m map[string]any, k string) string      { v, _ := m[k].(string); return v }
func stringSet(v any) map[string]bool {
	out := map[string]bool{}
	for _, x := range v.([]any) {
		out[x.(string)] = true
	}
	return out
}
func privateCIDRs(v any, exact bool) ([]*net.IPNet, error) {
	out := []*net.IPNet{}
	for _, value := range v.([]any) {
		ip, n, err := net.ParseCIDR(value.(string))
		if err != nil || !ip.IsPrivate() || !ip.Equal(n.IP) {
			return nil, errors.New("business egress requires canonical private CIDRs")
		}
		ones, bits := n.Mask.Size()
		if (exact && ones != bits) || (!exact && ((bits == 32 && ones < 16) || (bits == 128 && ones < 64))) {
			return nil, errors.New("business egress CIDR exceeds the admitted boundary")
		}
		out = append(out, n)
	}
	return out, nil
}
func boundedEndpoint(ctx context.Context, endpoint string, port int, cidrs []*net.IPNet, model bool) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.User != nil || u.Hostname() == "" || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(model && u.Scheme == "http")) {
		return errors.New("business endpoint must use credential-free TLS, or the admitted private model protocol")
	}
	if model && u.Path != "/v1" {
		return errors.New("model endpoint must use /v1")
	}
	p := u.Port()
	if p == "" {
		p = "443"
		if u.Scheme == "http" {
			p = "80"
		}
	}
	actual, err := strconv.Atoi(p)
	if err != nil || actual != port {
		return errors.New("business endpoint port differs from egress")
	}
	ips := []net.IP{net.ParseIP(u.Hostname())}
	if ips[0] == nil {
		ips, err = net.DefaultResolver.LookupIP(ctx, "ip", u.Hostname())
		if err != nil || len(ips) == 0 {
			return errors.New("business endpoint DNS unavailable")
		}
	}
	for _, ip := range ips {
		allowed := false
		for _, n := range cidrs {
			allowed = allowed || n.Contains(ip)
		}
		if !ip.IsPrivate() || !allowed {
			return errors.New("business endpoint resolves outside exact private egress")
		}
	}
	return nil
}
func validateBusinessNetwork(document map[string]any) error {
	namespace := textValue(document, "namespace")
	if namespace == "default" || strings.HasPrefix(namespace, "kube-") {
		return errors.New("current installation requires a dedicated non-system namespace")
	}
	sp04, sp06 := object(document, "sp04"), object(document, "sp06")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	identities := map[string]bool{}
	for _, identity := range []string{textValue(sp04, "apiIdentitySecret"), textValue(sp04, "workerIdentitySecret"), textValue(sp06, "identitySecret"), textValue(sp06, "modelKeySecret"), textValue(sp04, "sourceCredentialsSecret")} {
		if identities[identity] {
			return errors.New("workload identities, model and source credentials require separate Secret objects")
		}
		identities[identity] = true
	}
	if _, err := privateCIDRs(sp04["allowedWorkerCIDRs"], false); err != nil {
		return err
	}
	api := object(sp04, "kubernetesAPI")
	cidrs, err := privateCIDRs(api["cidrs"], true)
	if err != nil {
		return err
	}
	tenants := stringSet(sp06["tenants"])
	leases := stringSet(sp04["leaseNames"])
	seen := map[string]bool{}
	clusterScopes := map[string]map[string]bool{}
	sourceCoverage := map[string]map[string]bool{}
	for _, c := range sp04["clusters"].([]any) {
		cluster := c.(map[string]any)
		if !tenants[textValue(cluster, "Tenant")] || !leases[textValue(cluster, "LeaseName")] || textValue(cluster, "LeaseNamespace") != namespace {
			return errors.New("cluster tenant/lease must match explicit dispatch and installation scope")
		}
		id := textValue(cluster, "SourceID")
		if seen[id] {
			return errors.New("business source IDs must be unique")
		}
		seen[id] = true
		tenant := textValue(cluster, "Tenant")
		if clusterScopes[tenant] == nil {
			clusterScopes[tenant] = map[string]bool{}
		}
		clusterScopes[tenant][textValue(cluster, "ClusterUID")] = true
		if err := boundedEndpoint(ctx, textValue(cluster, "Endpoint"), int(api["port"].(float64)), cidrs, false); err != nil {
			return err
		}
		for _, k := range []string{"CAFile", "TokenFile"} {
			p := textValue(cluster, k)
			if api["localCollector"] == true {
				want := "/var/run/secrets/ops-platform/kubernetes/ca.pem"
				if k == "TokenFile" {
					want = "/var/run/secrets/ops-platform/kubernetes/token"
				}
				if p != want {
					return errors.New("local collector must use its projected CA/token")
				}
			} else if !sourceProjectionPath(p) {
				return errors.New("remote cluster credentials require the explicit source Secret projection")
			}
		}
	}
	for _, x := range sp04["sources"].([]any) {
		s := x.(map[string]any)
		b := object(s, "Binding")
		if !tenants[textValue(b, "Tenant")] || textValue(b, "SourceType") != textValue(s, "Name") || seen[textValue(b, "SourceID")] {
			return errors.New("source type/tenant/identity differs from business scope")
		}
		seen[textValue(b, "SourceID")] = true
		tenant := textValue(b, "Tenant")
		mapping := object(b, "ScopeMapping")
		scopes := object(mapping, "scopes")
		if textValue(mapping, "nativeTenant") != "" || len(scopes) != 2 || textValue(object(mapping, "requiredLabels"), "tenant") != tenant {
			return errors.New("current Victoria source requires its qualified tenant equality and cluster/namespace mapping")
		}
		for cluster := range stringSet(scopes["cluster"]) {
			if !clusterScopes[tenant][cluster] {
				return errors.New("source cluster mapping exceeds its explicit tenant installation scope")
			}
		}
		probe := object(s, "ScopeProbe")
		id, err := resource.ParseCanonicalID(textValue(probe, "resourceCanonicalId"))
		if err != nil || id.Tenant != tenant || id.Domain != "k8s" || id.APIGroup != "core" || id.Kind != "Pod" || id.Scope != scopes["cluster"].([]any)[0].(string) || !stringSet(scopes["namespace"])[textValue(probe, "namespace")] {
			return errors.New("source positive probe exceeds its explicit identity/scope mapping")
		}
		from, fromErr := time.Parse(time.RFC3339Nano, textValue(probe, "from"))
		to, toErr := time.Parse(time.RFC3339Nano, textValue(probe, "to"))
		template := textValue(probe, "queryTemplate")
		qualifiedTemplate := textValue(s, "Name") == "victoriametrics" && (template == "pod-phase/v1" || template == "pod-restarts/v1") || textValue(s, "Name") == "victorialogs" && (template == "resource-logs/v1" || template == "kernel-logs/v1")
		if fromErr != nil || toErr != nil || !to.After(from) || to.Sub(from) > time.Hour || to.After(time.Now().Add(time.Minute)) || !qualifiedTemplate {
			return errors.New("source positive probe requires a qualified read template and bounded observation window")
		}
		if sourceCoverage[tenant] == nil {
			sourceCoverage[tenant] = map[string]bool{}
		}
		sourceCoverage[tenant][textValue(s, "Name")] = true
		for _, k := range []string{"CAFile", "CredentialFile"} {
			if !sourceProjectionPath(textValue(s, k)) {
				return errors.New("source trust and credentials require explicit Secret projections")
			}
		}
	}
	for tenant := range tenants {
		if !sourceCoverage[tenant]["victoriametrics"] || !sourceCoverage[tenant]["victorialogs"] {
			return errors.New("current installation requires explicit qualified VictoriaMetrics and VictoriaLogs sources for every tenant")
		}
	}
	for _, x := range object(document, "sp05")["ingestionBindings"].([]any) {
		b := x.(map[string]any)
		if !tenants[textValue(b, "Tenant")] || !seen[textValue(b, "SourceID")] {
			return errors.New("ingestion binding requires an explicit admitted tenant/source")
		}
	}
	model := object(sp06, "model")
	endpoint := textValue(model, "base_url")
	if textValue(sp06, "modelNetworkMode") == "orbstack-host" {
		// The host-only operator cannot resolve the native container bridge. Its
		// exact addresses and model availability must be proven inside the target
		// investigator policy before activation; other sources retain privateCIDRs.
		u, err := url.Parse(endpoint)
		if err != nil || u.Hostname() != "host.docker.internal" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/v1" || (u.Scheme != "http" && u.Scheme != "https") {
			return errors.New("OrbStack model bridge requires the exact native host and /v1")
		}
		port := u.Port()
		if port == "" {
			if u.Scheme == "https" {
				port = "443"
			} else {
				port = "80"
			}
		}
		actual, err := strconv.Atoi(port)
		if err != nil || actual != int(sp06["modelPort"].(float64)) {
			return errors.New("model endpoint port differs from egress")
		}
		for _, value := range sp06["modelCIDRs"].([]any) {
			ip, n, err := net.ParseCIDR(value.(string))
			if err != nil {
				return errors.New("exact discovered OrbStack model host CIDR required")
			}
			ones, bits := n.Mask.Size()
			synthetic := ip.To4() != nil && ip.To4()[0] == 0
			if !ip.Equal(n.IP) || ones != bits || (!ip.IsPrivate() && !synthetic) {
				return errors.New("exact native private or synthetic bridge host required")
			}
		}
		return nil
	}
	modelCIDRs, err := privateCIDRs(sp06["modelCIDRs"], true)
	if err != nil {
		return err
	}
	return boundedEndpoint(ctx, endpoint, int(sp06["modelPort"].(float64)), modelCIDRs, true)
}
func sourceProjectionPath(p string) bool {
	return path.Clean(p) == p && path.Dir(p) == "/etc/sp04/sources" && path.Base(p) != "." && path.Base(p) != ".." && !strings.ContainsAny(p, "\r\n\t\x00")
}

func applyBusinessValues(values map[string]any, b BusinessValues, images map[string]string) error {
	if b.values == nil {
		return errors.New("explicit current business values required")
	}
	if images["holmesgpt"] == "" {
		return errors.New("authenticated locked holmesgpt OCI material is required")
	}
	for _, name := range []string{"sp04", "sp05", "sp06"} {
		values[name] = b.values[name]
	}
	values["workloadIdentity"] = b.values["workloadIdentity"]
	components := object(values, "components")
	components["investigator"] = map[string]any{"enabled": true, "image": images["holmesgpt"]}
	return nil
}
func preflightBusinessSecrets(ctx context.Context, p profile.ResolvedProfile, b BusinessValues, run CommandRunner) error {
	if b.values == nil {
		return errors.New("explicit current business values required")
	}
	sp04, sp06 := object(b.values, "sp04"), object(b.values, "sp06")
	required := map[string][]string{
		textValue(sp04, "apiIdentitySecret"):    {"ca.pem", "context.key"},
		textValue(sp04, "workerIdentitySecret"): {"ca.pem", "context.pub"},
		textValue(sp06, "identitySecret"):       {"ca.pem"},
		textValue(sp06, "modelKeySecret"):       {"apiKey"},
	}
	for _, x := range sp04["sources"].([]any) {
		s := x.(map[string]any)
		for _, key := range []string{"CAFile", "CredentialFile"} {
			secret := textValue(sp04, "sourceCredentialsSecret")
			required[secret] = append(required[secret], path.Base(textValue(s, key)))
		}
	}
	api := object(sp04, "kubernetesAPI")
	if api["localCollector"] != true {
		for _, x := range sp04["clusters"].([]any) {
			c := x.(map[string]any)
			secret := textValue(sp04, "sourceCredentialsSecret")
			for _, key := range []string{"CAFile", "TokenFile"} {
				required[secret] = append(required[secret], path.Base(textValue(c, key)))
			}
		}
	}
	for secret, keys := range required {
		for _, key := range keys {
			template := fmt.Sprintf(`go-template={{if index .data %q}}present{{else}}absent{{end}}`, key)
			raw, err := run(ctx, "kubectl", "--context", p.Kubernetes.Context, "-n", b.Namespace(), "get", "secret", secret, "-o", template)
			if err != nil || strings.TrimSpace(string(raw)) != "present" {
				return fmt.Errorf("business identity/credential Secret %s lacks required key %s", secret, key)
			}
		}
	}
	return nil
}
