package integration

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crypto/x509"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"gopkg.in/yaml.v3"
	"ops-platform/internal/app"
	"ops-platform/internal/bundle"
	"ops-platform/internal/bundle/drivers"
	"ops-platform/internal/datascope"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/integrations/openbao"
	"ops-platform/internal/profile"
	"ops-platform/internal/resource"
)

// This gate uses signed, verified Chart/image materials in an owned namespace.
// Seven shared external dependencies are preserved. Source fixtures are explicit
// isolated endpoints; neither their identities nor this selected regression
// impersonate a clean installation of the shared core release.
func TestSP04SignedChartNativeWorkerOfflineReinstall(t *testing.T) {
	if os.Getenv("SP04_NATIVE_CHART") != "1" {
		t.Skip("signed Bundle and owned native gate opt-in required")
	}
	inputs, bundleDir, trust, resolved := os.Getenv("SP04_BUNDLE_INPUTS"), os.Getenv("SP04_BUNDLE_DIR"), os.Getenv("SP04_BUNDLE_TRUST"), os.Getenv("SP04_RESOLVED_PROFILE")
	if inputs == "" || bundleDir == "" || trust == "" || resolved == "" {
		t.Fatal("actual signed materials and discovered Profile required")
	}
	ctx, db, dir, dsn := newMigrationDatabaseWithTimeout(t, 20*time.Minute)
	run := func(program string, args ...string) []byte {
		t.Helper()
		c := exec.CommandContext(ctx, program, args...)
		b, e := c.CombinedOutput()
		if e != nil {
			t.Fatalf("native operator %s failed: %v (%s)", program, e, string(b))
		}
		return b
	}
	root := filepath.Dir(dir)
	c := exec.CommandContext(ctx, "go", "run", "./cmd/opsctl", "bundle", "verify", "--manifest", filepath.Join(bundleDir, "bundle.lock.json"), "--signature", filepath.Join(bundleDir, "bundle.lock.sig"), "--payload", filepath.Join(bundleDir, "payload.tar.zst"), "--key", trust)
	c.Dir = root
	if b, e := c.CombinedOutput(); e != nil {
		t.Fatalf("signed Bundle verification failed: %v %s", e, b)
	}
	var spec struct {
		Files []struct{ Path, Source, Digest string }
	}
	raw, e := os.ReadFile(filepath.Join(inputs, "build-spec.json"))
	if e != nil || json.Unmarshal(raw, &spec) != nil {
		t.Fatal("read exact build specification")
	}
	chart := ""
	for _, f := range spec.Files {
		if f.Path == "charts/ops-platform-0.1.0.tgz" {
			b, e := os.ReadFile(f.Source)
			if e != nil || fmt.Sprintf("sha256:%x", sha256.Sum256(b)) != f.Digest {
				t.Fatal("verified Chart source digest mismatch")
			}
			chart = f.Source
			t.Logf("installed signed Chart digest=%s", f.Digest)
		}
	}
	if chart == "" {
		t.Fatal("signed Chart missing")
	}
	if e = goose.UpToContext(ctx, db, dir, 1); e != nil {
		t.Fatal(e)
	}
	if e = runRemainingMigrationsAsMigrationRole(t, ctx, db, dsn, dir); e != nil {
		t.Fatal(e)
	}
	tenant, source, cluster, metricSource := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	collector, ns := sp04OwnedKubernetes(t, ctx, tenant.String(), source.String())
	// Namespace fixture captures UID and label and uses server-side delete preconditions.
	var namespaceIdentity struct {
		Metadata struct {
			UID    string
			Labels map[string]string
		}
	}
	if json.Unmarshal(sp04Kubectl(t, ctx, nil, "--context", "orbstack", "get", "namespace", ns, "-o", "json"), &namespaceIdentity) != nil || namespaceIdentity.Metadata.UID == "" || namespaceIdentity.Metadata.Labels["ops.platform.test"] != ns {
		t.Fatal("owned namespace identity missing")
	}
	verifyOwnedNamespace := func() bool {
		raw, e := exec.Command("kubectl", "--context", "orbstack", "get", "namespace", ns, "-o", "json").Output()
		var current struct {
			Metadata struct {
				UID    string
				Labels map[string]string
			}
		}
		return e == nil && json.Unmarshal(raw, &current) == nil && current.Metadata.UID == namespaceIdentity.Metadata.UID && current.Metadata.Labels["ops.platform.test"] == ns
	}
	create := func(v any) []byte {
		b, _ := json.Marshal(v)
		return sp04Kubectl(t, ctx, b, "--context", "orbstack", "create", "-f", "-", "-o", "json")
	}

	bao, baoCA, baoEndpoint := sp04NativeBao(t, ctx, ns, collector)
	bucket := "sp04-native-" + uuid.NewString()[:8]
	s3Fixture := newRoleTenantS3Fixture(t, []uuid.UUID{tenant}, bucket)
	nativeEndpoint := func(raw string) string {
		u, e := url.Parse(raw)
		if e != nil {
			t.Fatal(e)
		}
		u.Host = "host.docker.internal:" + u.Port()
		return u.String()
	}
	archiveEndpoint := nativeEndpoint(s3Fixture.Endpoint)
	issuer, oidcCA := sp04NativeKeycloak(t, ctx, root)
	// Preserve the OIDC issuer Host while accessing the same owned Keycloak from
	// the host. Native Pods use normal DNS and independently verify discovery/JWT.
	original := http.DefaultTransport
	transport := original.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: x509.NewCertPool()}
	transport.TLSClientConfig.RootCAs.AppendCertsFromPEM(oidcCA)
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e == nil && host == "host.docker.internal" {
			address = net.JoinHostPort("127.0.0.1", port)
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = original; transport.CloseIdleConnections() })
	t.Setenv("SP03_KEYCLOAK_TEST_ISSUER", issuer)
	token := sp04KeycloakToken(t, ctx, tenant)
	if _, e = db.ExecContext(ctx, `INSERT INTO platform.tenants(tenant_id,slug,display_name) VALUES($1,$2,'Native SP04')`, tenant, tenant.String()); e != nil {
		t.Fatal(e)
	}
	if _, e = db.ExecContext(ctx, `INSERT INTO platform.cluster_registrations(tenant_id,cluster_id,cluster_uid,display_name) VALUES($1,$2,$3,'Owned OrbStack Chart')`, tenant, cluster, collector.ClusterUID); e != nil {
		t.Fatal(e)
	}
	mapping := datascope.Mapping{Scopes: map[string][]string{"cluster": {collector.ClusterUID}, "namespace": {ns}}}
	rawMapping, _ := json.Marshal(mapping)
	if _, e = db.ExecContext(ctx, `INSERT INTO platform.source_registrations(tenant_id,source_id,cluster_id,source_type,instance_key,auth_ref,backend_logical_id,data_scope_mapping) VALUES($1,$2,$3,'kubernetes',$4,'openbao://owned/collector',$5,$6)`, tenant, source, cluster, ns, collector.BackendLogicalID, rawMapping); e != nil {
		t.Fatal(e)
	}
	metricMapping := mapping
	metricMapping.RequiredLabels = map[string]string{"tenant": tenant.String()}
	rawMapping, _ = json.Marshal(metricMapping)
	if _, e = db.ExecContext(ctx, `INSERT INTO platform.source_registrations(tenant_id,source_id,cluster_id,source_type,instance_key,auth_ref,backend_logical_id,data_scope_mapping) VALUES($1,$2,$3,'victoriametrics',$4,'openbao://owned/metrics','native-metrics',$5)`, tenant, metricSource, cluster, ns, rawMapping); e != nil {
		t.Fatal(e)
	}
	scopes, _ := json.Marshal([]string{cluster.String()})
	namespaces, _ := json.Marshal([]map[string]string{{"clusterId": cluster.String(), "namespace": ns}})
	if _, e = db.ExecContext(ctx, `INSERT INTO platform.role_bindings(tenant_id,binding_id,subject,role_name,cluster_scopes,namespace_scopes) VALUES($1,$2,$3,'operator',$4,$5)`, tenant, uuid.New(), token.Subject, scopes, namespaces); e != nil {
		t.Fatal(e)
	}
	apiDSN := nativeEndpoint(runtimePoolConfig(t, ctx, db, dsn, "api_runtime_role").ConnConfig.ConnString())
	workerDSN := nativeEndpoint(runtimePoolConfig(t, ctx, db, dsn, "worker_runtime_role").ConnConfig.ConnString())
	podRaw := create(map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": "evidence-pod", "namespace": ns, "labels": map[string]string{"app": "sp04-native"}}, "spec": map[string]any{"automountServiceAccountToken": false, "containers": []any{map[string]any{"name": "probe", "image": archiveProbeImage, "imagePullPolicy": "Never", "command": []string{"sleep", "1800"}}}}})
	var pod struct{ Metadata struct{ UID string } }
	json.Unmarshal(podRaw, &pod)
	canonical := resource.CanonicalID{Domain: "k8s", Tenant: tenant.String(), Scope: collector.ClusterUID, APIGroup: "core", Kind: "Pod", StableID: pod.Metadata.UID}.String()
	now := time.Now().UTC().Truncate(time.Second)
	metric := fmt.Sprintf("kube_pod_status_phase{tenant=%q,cluster=%q,namespace=%q,uid=%q,phase=\"Running\"} 1 %d\n", tenant, collector.ClusterUID, ns, pod.Metadata.UID, now.Add(-30*time.Second).UnixMilli())
	response, e := http.Post(os.Getenv("SP04_TEST_VM_URL")+"/api/v1/import/prometheus", "text/plain", strings.NewReader(metric))
	if e != nil {
		t.Fatal(e)
	}
	response.Body.Close()
	if response.StatusCode > 299 {
		t.Fatal("native metric seed failed")
	}
	ctxPub, ctxKey, _ := ed25519.GenerateKey(rand.Reader)
	identity := func(sa string) {
		key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if e != nil {
			t.Fatal(e)
		}
		cert, e := bao.SignWorkloadCSR(ctx, ns, sa, workloadCSR(t, key, ns, sa), key)
		if e != nil {
			t.Fatal(e)
		}
		crl, e := bao.ReadWorkloadCRL(ctx)
		if e != nil {
			t.Fatal(e)
		}
		pk, _ := x509.MarshalPKCS8PrivateKey(key)
		chain := []byte{}
		for _, der := range cert.Certificate {
			chain = append(chain, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
		}
		data := map[string][]byte{"tls.crt": chain, "tls.key": pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk}), "ca.pem": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[len(cert.Certificate)-1]}), "crl.pem": pem.EncodeToMemory(&pem.Block{Type: "X509 CRL", Bytes: crl.Raw})}
		if sa == "ops-api" {
			data["context.key"] = ctxKey
		} else {
			data["context.pub"] = ctxPub
		}
		create(map[string]any{"apiVersion": "v1", "kind": "Secret", "metadata": map[string]any{"name": "ops-sp04-" + strings.TrimPrefix(sa, "ops-") + "-identity", "namespace": ns}, "data": data})
	}
	identity("ops-api")
	identity("ops-worker")
	crlPatchFile := filepath.Join(t.TempDir(), "crl-patch.json")
	refreshCtx, stopRefresh := context.WithCancel(ctx)
	refreshDone := make(chan struct{})
	refreshErrors := make(chan error, 1)
	go func() {
		defer close(refreshDone)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-refreshCtx.Done():
				return
			case <-ticker.C:
				crl, e := bao.ReadWorkloadCRL(refreshCtx)
				if e != nil {
					select {
					case refreshErrors <- e:
					default:
					}
					continue
				}
				patch, _ := json.Marshal(map[string]any{"data": map[string][]byte{"crl.pem": pem.EncodeToMemory(&pem.Block{Type: "X509 CRL", Bytes: crl.Raw})}})
				if e = os.WriteFile(crlPatchFile, patch, 0600); e != nil {
					select {
					case refreshErrors <- e:
					default:
					}
					continue
				}
				for _, name := range []string{"api", "worker"} {
					command := exec.CommandContext(refreshCtx, "kubectl", "--context", "orbstack", "-n", ns, "patch", "secret", "ops-sp04-"+name+"-identity", "--type=merge", "--patch-file", crlPatchFile)
					if e = command.Run(); e != nil {
						select {
						case refreshErrors <- e:
						default:
						}
					}
				}
			}
		}
	}()
	t.Cleanup(func() { stopRefresh(); <-refreshDone })

	credentials, _ := os.ReadFile(s3Fixture.CredentialFile)
	create(map[string]any{"apiVersion": "v1", "kind": "Secret", "metadata": map[string]any{"name": "ops-platform-runtime", "namespace": ns}, "stringData": map[string]string{"apiDatabaseURL": apiDSN, "workerDatabaseURL": workerDSN, "archiveTenantCredentials": string(credentials)}})
	create(map[string]any{"apiVersion": "v1", "kind": "Secret", "metadata": map[string]any{"name": "ops-sp04-source-credentials", "namespace": ns}, "stringData": map[string]string{"unused": "no-source-secret-needed"}})
	// Runtime endpoint Profile describes the exact owned fixture instances. The
	// separately rediscovered external Profile authorizes Bundle import only.
	runtimeProfilePath := isolatedRuntimeProfile(t, issuer, baoEndpoint, archiveEndpoint)
	runtimeProfile, _ := os.ReadFile(runtimeProfilePath)
	discovered, e := profile.ReadResolvedProfileFile(resolved)
	if e != nil {
		t.Fatal(e)
	}
	if discovered.Kubernetes.ClusterUID != collector.ClusterUID {
		t.Fatal("Bundle Profile targets another real cluster")
	}
	collector.Endpoint = "https://kubernetes.default.svc"
	collector.CAFile = "/var/run/secrets/ops-platform/kubernetes/ca.pem"
	collector.TokenFile = "/var/run/secrets/ops-platform/kubernetes/token"
	collector.QPS = 10
	collector.Burst = 25
	probe := evidence.Query{ResourceCanonicalID: canonical, Namespace: ns, Template: "pod-phase/v1", From: now.Add(-time.Minute), To: now, Limit: 20}
	sourceConfig := app.SP04Source{Name: "victoriametrics", Binding: evidence.Binding{Tenant: tenant.String(), SourceID: metricSource.String(), SourceType: "victoriametrics", Revision: 1, BackendLogicalID: "native-metrics", Endpoint: nativeEndpoint(os.Getenv("SP04_TEST_VM_URL")), ScopeMapping: metricMapping}, ScopeProbe: &probe}
	manifestRaw, e := os.ReadFile(filepath.Join(bundleDir, "bundle.lock.json"))
	if e != nil {
		t.Fatal(e)
	}
	manifest, e := bundle.ParseManifest(manifestRaw)
	if e != nil {
		t.Fatal(e)
	}
	sig, e := os.ReadFile(filepath.Join(bundleDir, "bundle.lock.sig"))
	if e != nil {
		t.Fatal(e)
	}
	manifest.Signature, e = base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if e != nil {
		t.Fatal(e)
	}
	manifest.PayloadPath = filepath.Join(bundleDir, "payload.tar.zst")
	keyFile, e := os.Open(trust)
	if e != nil {
		t.Fatal(e)
	}
	trusted, e := bundle.ReadTrustRoot(keyFile)
	keyFile.Close()
	if e != nil {
		t.Fatal(e)
	}
	planned, e := bundle.PlanImport(ctx, manifest, trusted, discovered)
	if e != nil {
		t.Fatal(e)
	}
	images := map[string]string{}
	for _, image := range planned {
		if image.Name == "platform-api" || image.Name == "platform-worker" {
			name := strings.TrimPrefix(image.Name, "platform-")

			images[name] = image.Reference
			t.Logf("native exact verified %s image=%s", name, image.Reference)
		}
	}
	if len(images) != 2 {
		t.Fatal("actual selected platform image plan incomplete")
	}
	for _, f := range spec.Files {
		if f.Path == "charts/ops-platform-0.1.0.tgz" {
			matched := false
			for _, signed := range manifest.Payload.Files {
				if signed.Path == f.Path && signed.Digest == f.Digest {
					matched = true
				}
			}
			if !matched {
				t.Fatal("Chart build input differs from signed manifest")
			}
		}
	}

	jsonValue := func(v any) map[string]any {
		b, e := json.Marshal(v)
		if e != nil {
			t.Fatal(e)
		}
		out := map[string]any{}
		if json.Unmarshal(b, &out) != nil {
			t.Fatal("native configuration conversion failed")
		}
		return out
	}
	values := map[string]any{"workloadsEnabled": true, "global": map[string]any{"imagePullPolicy": "Never"}, "components": map[string]any{"api": map[string]any{"image": images["api"]}, "worker": map[string]any{"image": images["worker"]}, "web": map[string]any{"enabled": false}, "investigator": map[string]any{"enabled": false}, "command-runner": map[string]any{"enabled": false}}, "networkPolicy": map[string]any{"managedDependencyReleases": []string{}}, "runtime": map[string]any{"profile": string(runtimeProfile), "oidcIssuerURL": issuer, "oidcCABundle": string(oidcCA), "openbaoAddress": baoEndpoint, "openbaoCABundle": string(baoCA), "archiveEndpoint": archiveEndpoint, "archiveCABundle": string(s3Fixture.CA), "archiveBucket": bucket}, "sp04": map[string]any{"enabled": true, "allowedWorkerCIDRs": []string{"192.168.194.0/25"}, "archiveBackendLogicalID": "native-archive", "clusters": []any{jsonValue(collector)}, "sources": []any{jsonValue(sourceConfig)}, "leaseNames": []string{"sp04-graph"}, "kubernetesAPI": map[string]any{"localCollector": true, "cidrs": []string{"192.168.139.2/32"}, "port": 26443}}}
	// Exact host bridge allowlist is owned test configuration for loopback-published
	// fixtures. It does not permit arbitrary external hosts, ports, or public egress.
	ports := []any{}
	for _, endpoint := range []string{apiDSN, issuer, baoEndpoint, archiveEndpoint, sourceConfig.Binding.Endpoint, collector.Endpoint} {
		u, _ := url.Parse(endpoint)
		var port int
		fmt.Sscanf(u.Port(), "%d", &port)
		if port == 0 {
			continue
		}
		ports = append(ports, map[string]any{"protocol": "TCP", "port": port})
	}
	create(map[string]any{"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy", "metadata": map[string]any{"name": "owned-host-fixtures", "namespace": ns}, "spec": map[string]any{"podSelector": map[string]any{"matchLabels": map[string]string{"ops.platform.io/release": ns}, "matchExpressions": []any{map[string]any{"key": "ops.platform.test.public-control", "operator": "DoesNotExist"}}}, "policyTypes": []string{"Egress"}, "egress": []any{map[string]any{"to": []any{map[string]any{"ipBlock": map[string]string{"cidr": "0.250.250.254/32"}}}, "ports": ports}}}})
	// Probe carries the same release/component selection as API. It must prove a
	// positive public path before the Chart imposes its actual deny policy.
	create(map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": "egress-probe", "namespace": ns, "labels": map[string]string{"ops.platform.io/release": ns, "ops.platform.io/component": "web", "ops.platform.test.public-control": "probe"}}, "spec": map[string]any{"automountServiceAccountToken": false, "containers": []any{map[string]any{"name": "probe", "image": archiveProbeImage, "imagePullPolicy": "Never", "command": []string{"sleep", "1800"}}}}})

	run("kubectl", "--context", "orbstack", "-n", ns, "wait", "--for=condition=Ready", "pod/egress-probe", "--timeout=90s")
	connectPod := func(pod, host string, port int) bool {
		script := fmt.Sprintf("timeout 4 bash -c '</dev/tcp/%s/%d'", host, port)
		c := exec.CommandContext(ctx, "kubectl", "--context", "orbstack", "-n", ns, "exec", pod, "--", "bash", "-c", script)
		return c.Run() == nil
	}
	connect := func(host string, port int) bool { return connectPod("egress-probe", host, port) }
	if !connect("1.1.1.1", 443) {
		t.Fatal("public positive control unavailable before policy")
	}
	valueFile := filepath.Join(t.TempDir(), "values.yaml")
	raw, e = yaml.Marshal(values)
	if e != nil || os.WriteFile(valueFile, raw, 0600) != nil {
		t.Fatal("write private native runtime values")
	}
	install := func() {
		run("helm", "upgrade", "--install", ns, chart, "--kube-context", "orbstack", "--namespace", ns, "--values", valueFile, "--wait", "--timeout", "4m")
	}
	// Cluster RBAC belongs to this uniquely named release; clean up only after
	// confirming namespace ownership through the fixture cleanup registered above.
	t.Cleanup(func() {
		if !verifyOwnedNamespace() {
			t.Error("refuse release cleanup after namespace ownership changed")
			return
		}
		_ = exec.Command("helm", "uninstall", ns, "--kube-context", "orbstack", "--namespace", ns, "--wait", "--timeout", "90s").Run()
	})
	if os.Getenv("SP04_COLD_IMPORT") == "1" {
		sp04ColdSelectedImages(t, ctx, planned, manifest.BundleID)
	}
	importReport, e := bundle.Import(ctx, manifest, trusted, discovered, drivers.OrbStackSharedStore{})
	if e != nil {
		t.Fatal(e)
	}
	t.Logf("actual selected Bundle import driver=%s materials=%v", importReport.Driver, importReport.Imported)
	install()
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		for _, deployment := range []string{"ops-api", "ops-worker"} {
			raw, e := exec.Command("kubectl", "--context", "orbstack", "-n", ns, "logs", "deployment/"+deployment, "--all-pods=true", "--prefix", "--tail=80").Output()
			if e == nil {
				t.Logf("owned native %s diagnostic logs: %s", deployment, raw)
			}
		}
		raw, e := exec.Command("kubectl", "--context", "orbstack", "-n", ns, "get", "lease", "sp04-graph", "-o", "json").Output()
		if e == nil {
			var lease graph.LeaseDocument
			if json.Unmarshal(raw, &lease) == nil {
				t.Logf("owned native Lease epoch=%s holder_present=%v uid=%s", lease.Metadata.Annotations["ops.platform/owner-epoch"], lease.Spec.HolderIdentity != "", lease.Metadata.UID)
			}
		}
	})

	for deadline := time.Now().Add(45 * time.Second); ; {
		if !connect("1.1.1.1", 443) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("actual Chart failed public egress deny")
		}
		time.Sleep(time.Second)
	}
	run("kubectl", "--context", "orbstack", "-n", ns, "label", "pod", "evidence-pod", "ops.platform.io/release="+ns, "ops.platform.io/component=worker")
	if !connectPod("evidence-pod", "host.docker.internal", 15484) || connectPod("evidence-pod", "1.1.1.1", 443) {
		t.Fatal("explicit registered PG source blocked by native policy")
	}
	t.Log("actual CNI positive public-before/negative public-after; exact owned host dependency allowed; selected images Never")
	// Port-forward only the owned API Service. No shared Service or route changes.
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	forward := exec.CommandContext(ctx, "kubectl", "--context", "orbstack", "-n", ns, "port-forward", "service/ops-api", fmt.Sprintf("%d:8080", port))
	forward.Stdout = io.Discard
	forward.Stderr = io.Discard
	if e = forward.Start(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = forward.Process.Kill(); _ = forward.Wait() })
	call := func(method, path string, data []byte) (int, []byte) {
		r, _ := http.NewRequestWithContext(ctx, method, fmt.Sprintf("http://127.0.0.1:%d%s", port, path), bytes.NewReader(data))
		r.Header.Set("Authorization", "Bearer "+token.AccessToken)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", uuid.NewString())
		response, e := (&http.Client{Timeout: 10 * time.Second}).Do(r)
		if e != nil {
			return 0, nil
		}
		defer response.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		return response.StatusCode, b
	}
	resourcePath := "/api/v1/resources/by-canonical-id?canonicalId=" + url.QueryEscape(canonical)
	await := func(path string) {
		for deadline := time.Now().Add(90 * time.Second); ; {
			status, b := call("GET", path, nil)
			if status == 200 {
				var r struct{ Data graph.Result }
				if json.Unmarshal(b, &r) != nil || len(r.Data.Nodes) != 1 {
					t.Fatal("native Graph contract invalid")
				}
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("actual Pod runtime resource unavailable status=%d body=%s", status, b)
			}
			time.Sleep(time.Second)
		}
	}
	await(resourcePath)
	for deadline := time.Now().Add(90 * time.Second); ; {
		var count int
		e = db.QueryRowContext(ctx, `SELECT count(*) FROM platform.adapter_scope_verifications WHERE tenant_id=$1 AND expires_at>clock_timestamp()`, tenant).Scan(&count)
		if e != nil {
			t.Fatal(e)
		}
		if count > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("actual Pod Adapter authorization proofs unavailable")
		}
		time.Sleep(time.Second)
	}
	request, _ := json.Marshal(map[string]any{"resourceCanonicalId": canonical, "type": "metric", "queryTemplate": "pod-phase/v1", "timeRange": map[string]any{"from": probe.From, "to": probe.To}, "budget": map[string]int{"timeoutMs": 3000, "maxBytes": 65536}, "limit": 20})
	status, b := call("POST", "/api/v1/evidence:query", request)
	if status != 200 {
		t.Fatalf("actual Pod evidence status=%d body=%s", status, b)
	}
	var result struct{ Data evidence.Result }
	if json.Unmarshal(b, &result) != nil || len(result.Data.Evidence) != 1 || result.Data.Evidence[0].ReplayState != "archived_verified" {
		t.Fatal("native evidence result invalid")
	}
	t.Logf("native evidence accepted bytes=%d", len(b))
	var verified int
	if e = db.QueryRowContext(ctx, `SELECT count(*) FROM platform.evidence_metadata WHERE tenant_id=$1 AND replay_state='archived_verified'`, tenant).Scan(&verified); e != nil || verified < 1 {
		t.Fatalf("native projected-login archive not verified: %d %v", verified, e)
	}
	// Reinstall just this release: the same database, archive, pre-created Lease,
	// runtime and identity Secrets remain; no shared workloads or data are touched.
	if !verifyOwnedNamespace() {
		t.Fatal("refuse reinstall cleanup after namespace ownership changed")
	}
	run("helm", "uninstall", ns, "--kube-context", "orbstack", "--namespace", ns, "--wait", "--timeout", "90s")
	if _, e = bundle.Import(ctx, manifest, trusted, discovered, drivers.OrbStackSharedStore{}); e != nil {
		t.Fatal(e)
	}
	install()
	_ = forward.Process.Kill()
	_ = forward.Wait()
	forward = exec.CommandContext(ctx, "kubectl", "--context", "orbstack", "-n", ns, "port-forward", "service/ops-api", fmt.Sprintf("%d:8080", port))
	forward.Stdout = io.Discard
	forward.Stderr = io.Discard
	if e = forward.Start(); e != nil {
		t.Fatal(e)
	}
	await(resourcePath)
	status, b = call("GET", "/api/v1/evidence/"+result.Data.Evidence[0].EvidenceID, nil)
	if status != 200 {
		t.Fatalf("reinstalled native archive unavailable status=%d body=%s", status, b)
	}
	var after int
	if e = db.QueryRowContext(ctx, `SELECT count(*) FROM platform.evidence_metadata WHERE tenant_id=$1 AND replay_state='archived_verified'`, tenant).Scan(&after); e != nil || after < verified {
		t.Fatal("owned release reinstall lost verified archive metadata")
	}
	select {
	case <-refreshErrors:
		t.Fatal("native identity CRL refresh failed")
	default:
	}
	t.Logf("signed Chart API/2 Worker native commands; real Pod-bound OpenBao login; API→Lease→Worker→VM→Transit→role-separated S3→PG; owned release reinstall passed archived_before=%d archived_after=%d; sample scale one source resource, not production-scale acceptance", verified, after)
}

const archiveProbeImage = "docker.io/library/postgres@sha256:75731e2765e7d0c8bb7dea960ef3bdcde68d16314991ab2057a2a74ea0fff257"

func sp04NativeBao(t *testing.T, ctx context.Context, ns string, collector app.SP04Cluster) (*openbao.Client, []byte, string) {
	t.Helper()
	dir := t.TempDir()
	label := "sp04-native-" + uuid.NewString()
	name := "ops-" + label
	write := func(name string, b []byte) {
		if os.WriteFile(filepath.Join(dir, name), b, 0600) != nil {
			t.Fatal("private native Bao material write failed")
		}
	}
	c := exec.CommandContext(ctx, "openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-keyout", filepath.Join(dir, "tls.key"), "-out", filepath.Join(dir, "tls.crt"), "-days", "1", "-subj", "/CN=localhost", "-addext", "subjectAltName=DNS:localhost,DNS:host.docker.internal,IP:127.0.0.1")
	if c.Run() != nil {
		t.Fatal("native fixture TLS creation failed")
	}
	write("bao.hcl", []byte("ui=false\nstorage \"inmem\" {}\nlistener \"tcp\" { address=\"0.0.0.0:8200\" tls_cert_file=\"/cfg/tls.crt\" tls_key_file=\"/cfg/tls.key\" }\n"))
	ca, e := os.ReadFile(collector.CAFile)
	if e != nil {
		t.Fatal(e)
	}
	write("ca.crt", ca)
	write("token", sp04Kubectl(t, ctx, nil, "--context", "orbstack", "create", "token", "collector", "-n", ns, "--duration=30m"))
	rb := map[string]any{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "ClusterRoleBinding", "metadata": map[string]any{"name": label, "labels": map[string]string{"ops.platform.test": label}}, "roleRef": map[string]string{"apiGroup": "rbac.authorization.k8s.io", "kind": "ClusterRole", "name": "system:auth-delegator"}, "subjects": []any{map[string]string{"kind": "ServiceAccount", "name": "collector", "namespace": ns}}}
	raw, _ := json.Marshal(rb)
	created := sp04Kubectl(t, ctx, raw, "--context", "orbstack", "create", "-f", "-", "-o", "json")
	var object struct{ Metadata struct{ UID string } }
	json.Unmarshal(created, &object)
	t.Cleanup(func() {
		options, _ := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "DeleteOptions", "preconditions": map[string]string{"uid": object.Metadata.UID}})
		command := exec.Command("kubectl", "--context", "orbstack", "delete", "--raw", "/apis/rbac.authorization.k8s.io/v1/clusterrolebindings/"+label, "-f", "-")
		command.Stdin = bytes.NewReader(options)
		if command.Run() != nil {
			t.Error("owned TokenReview binding cleanup failed")
		}
	})
	serviceIP := strings.TrimSpace(string(sp04Kubectl(t, ctx, nil, "--context", "orbstack", "get", "service", "kubernetes", "-n", "default", "-o", "jsonpath={.spec.clusterIP}")))
	image := "ghcr.io/openbao/openbao@sha256:4ca9310dd2a50c746d4227f44058088ee0470a8470031ee3f09cc8b1a69dd7f6"
	output, e := exec.CommandContext(ctx, "docker", "run", "-d", "--pull=never", "--name", name, "--label", "ops.platform.test="+label, "--add-host", "kubernetes.default.svc:"+serviceIP, "-p", "127.0.0.1::8200", "-v", dir+":/cfg:ro", "-v", filepath.Join(dir, "token")+":/var/run/secrets/kubernetes.io/serviceaccount/token:ro", "-v", filepath.Join(dir, "ca.crt")+":/var/run/secrets/kubernetes.io/serviceaccount/ca.crt:ro", image, "server", "-config=/cfg/bao.hcl").Output()
	if e != nil {
		t.Fatal("owned native Bao start failed")
	}
	id := strings.TrimSpace(string(output))
	t.Cleanup(func() {
		b, e := exec.Command("docker", "inspect", id).Output()
		var objects []struct {
			ID     string `json:"Id"`
			Config struct{ Labels map[string]string }
		}
		if e != nil || json.Unmarshal(b, &objects) != nil || len(objects) != 1 || objects[0].ID != id || objects[0].Config.Labels["ops.platform.test"] != label {
			t.Error("refuse native Bao cleanup ownership mismatch")
			return
		}
		if exec.Command("docker", "rm", "-f", id).Run() != nil {
			t.Error("owned Bao cleanup failed")
		}
	})
	output, e = exec.CommandContext(ctx, "docker", "port", id, "8200/tcp").Output()
	if e != nil {
		t.Fatal(e)
	}
	_, port, e := net.SplitHostPort(strings.TrimSpace(string(output)))
	if e != nil {
		t.Fatal(e)
	}
	ca, e = os.ReadFile(filepath.Join(dir, "tls.crt"))
	if e != nil {
		t.Fatal(e)
	}
	config := openbao.ClientConfig{Address: "https://127.0.0.1:" + port, ServerName: "localhost", CACertBundle: ca, ServiceDomain: ns + ".svc.cluster.local", ExpectedVersion: "2.7.0", RepositoryRoot: filepath.Clean(filepath.Join("..", ".."))}
	bao, e := openbao.NewClient(config)
	if e != nil {
		t.Fatal(e)
	}
	for deadline := time.Now().Add(30 * time.Second); ; {
		_, e := bao.Status(ctx)
		if e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("owned native Bao TLS readiness failed")
		}
		time.Sleep(200 * time.Millisecond)
	}
	recoveryFile := filepath.Join(dir, "recovery.json")
	if e = bao.Initialize(ctx, recoveryFile); e != nil {
		t.Fatal(e)
	}
	material, e := openbao.ReadExternalRecoveryMaterial(recoveryFile, config.RepositoryRoot, "")
	if e != nil {
		t.Fatal(e)
	}
	for _, share := range material.Shares[:2] {
		if _, e = bao.Unseal(ctx, share); e != nil {
			t.Fatal(e)
		}
	}
	config.Token = material.RootToken
	bao, e = openbao.NewClient(config)
	if e != nil {
		t.Fatal(e)
	}
	if e = bao.Configure(ctx); e != nil {
		t.Fatal(e)
	}
	t.Logf("owned native Bao exact image=%s namespace=%s; local real Kubernetes TokenReview configuration", image, ns)
	return bao, ca, "https://host.docker.internal:" + port
}

func sp04NativeKeycloak(t *testing.T, ctx context.Context, root string) (string, []byte) {
	t.Helper()
	dir := t.TempDir()
	label := "sp04-native-kc-" + uuid.NewString()
	name := "ops-" + label
	command := exec.CommandContext(ctx, "openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-keyout", filepath.Join(dir, "tls.key"), "-out", filepath.Join(dir, "tls.crt"), "-days", "1", "-subj", "/CN=localhost", "-addext", "subjectAltName=DNS:localhost,DNS:host.docker.internal,IP:127.0.0.1")
	if command.Run() != nil {
		t.Fatal("owned Keycloak TLS setup failed")
	}
	password := os.Getenv("SP03_KEYCLOAK_TEST_ADMIN_PASSWORD")
	if password == "" {
		t.Fatal("owned Keycloak bootstrap credential required")
	}
	envFile := filepath.Join(dir, "keycloak.env")
	if os.WriteFile(envFile, []byte("KC_BOOTSTRAP_ADMIN_USERNAME=admin\nKC_BOOTSTRAP_ADMIN_PASSWORD="+password+"\n"), 0600) != nil {
		t.Fatal("private Keycloak bootstrap file write failed")
	}
	image := "quay.io/keycloak/keycloak@sha256:1f91ac24e8d68b8189d5d53a8381464c1db0fcff479348d5de973a86b63d621c"
	output, e := exec.CommandContext(ctx, "docker", "run", "-d", "--pull=never", "--name", name, "--label", "ops.platform.test="+label, "--env-file", envFile, "-p", "127.0.0.1::8443", "-v", dir+":/cfg:ro", "-v", filepath.Join(root, "deploy/keycloak/realm-ops.json")+":/opt/keycloak/data/import/realm-ops.json:ro", image, "start-dev", "--import-realm", "--http-enabled=false", "--hostname-strict=false", "--https-certificate-file=/cfg/tls.crt", "--https-certificate-key-file=/cfg/tls.key").Output()
	if e != nil {
		t.Fatal("owned TLS Keycloak start failed")
	}
	id := strings.TrimSpace(string(output))
	t.Cleanup(func() {
		raw, e := exec.Command("docker", "inspect", id).Output()
		var v []struct {
			ID     string `json:"Id"`
			Config struct{ Labels map[string]string }
		}
		if e != nil || json.Unmarshal(raw, &v) != nil || len(v) != 1 || v[0].ID != id || v[0].Config.Labels["ops.platform.test"] != label {
			t.Error("refuse Keycloak cleanup with mismatched ownership")
			return
		}
		if exec.Command("docker", "rm", "-f", id).Run() != nil {
			t.Error("owned TLS Keycloak cleanup failed")
		}
	})
	output, e = exec.CommandContext(ctx, "docker", "port", id, "8443/tcp").Output()
	if e != nil {
		t.Fatal(e)
	}
	_, port, e := net.SplitHostPort(strings.TrimSpace(string(output)))
	if e != nil {
		t.Fatal(e)
	}
	ca, e := os.ReadFile(filepath.Join(dir, "tls.crt"))
	if e != nil {
		t.Fatal(e)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(ca)
	tr := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 3 * time.Second}
	for deadline := time.Now().Add(90 * time.Second); ; {
		response, e := client.Get("https://127.0.0.1:" + port + "/realms/ops/.well-known/openid-configuration")
		if e == nil {
			response.Body.Close()
			if response.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("owned TLS Keycloak readiness failed")
		}
		time.Sleep(time.Second)
	}
	t.Logf("owned TLS Keycloak exact image=%s", image)
	return "https://host.docker.internal:" + port + "/realms/ops", ca
}

// Evict only these exact new first-party images after proving they have no
// consumers. Shared dependency and user images never enter this operation.
func sp04ColdSelectedImages(t *testing.T, ctx context.Context, images []bundle.ImageArtifact, bundleID string) {
	t.Helper()
	type container struct{ Image string }
	type status struct{ ImageID string }
	var pods struct {
		Items []struct {
			Spec   struct{ Containers, InitContainers, EphemeralContainers []container }
			Status struct{ ContainerStatuses, InitContainerStatuses, EphemeralContainerStatuses []status }
		}
	}
	if json.Unmarshal(sp04Kubectl(t, ctx, nil, "--context", "orbstack", "get", "pods", "--all-namespaces", "-o", "json"), &pods) != nil {
		t.Fatal("native Pod inventory unavailable before cold import")
	}
	for _, image := range images {
		if (image.Name != "platform-api" && image.Name != "platform-worker") || !strings.HasPrefix(image.Reference, "ops.local/task27/") {
			t.Fatal("refuse cold-cache operation on shared dependency")
		}
		ownedTag := "ops.local/" + bundleID + "/" + image.Name + ":1.0.0"
		ownedDigest := "ops.local/" + bundleID + "/" + image.Name + "@" + strings.Split(image.Reference, "@")[1]
		for _, reference := range []string{image.Reference, ownedTag} {
			raw, err := exec.CommandContext(ctx, "docker", "--context", "orbstack", "image", "inspect", reference).CombinedOutput()
			if err != nil {
				if !bytes.Contains(raw, []byte("No such image:")) {
					t.Fatal("cold-cache inventory failed")
				}
				continue
			}
			var data []sp04ColdImageIdentity
			if json.Unmarshal(raw, &data) != nil || len(data) != 1 {
				t.Fatal("exact image identity unavailable")
			}
			d := data[0]
			if !sp04ColdIdentityMatches(d, image.Reference, ownedDigest, ownedTag) {
				t.Fatal("refuse deleting image without exclusive signed identity")
			}
			for _, p := range pods.Items {
				for _, group := range [][]container{p.Spec.Containers, p.Spec.InitContainers, p.Spec.EphemeralContainers} {
					for _, c := range group {
						if c.Image == image.Reference || c.Image == ownedTag || c.Image == ownedDigest || c.Image == d.ID {
							t.Fatal("refuse removing existing Pod image")
						}
					}
				}
				for _, group := range [][]status{p.Status.ContainerStatuses, p.Status.InitContainerStatuses, p.Status.EphemeralContainerStatuses} {
					for _, c := range group {
						id := c.ImageID
						if _, suffix, ok := strings.Cut(id, "://"); ok {
							id = suffix
						}
						if id == image.Reference || id == ownedDigest || id == d.ID || id == strings.TrimPrefix(image.Reference, "ops.local/task27/"+image.Name+"@") {
							t.Fatal("refuse removing existing Pod image status")
						}
					}
				}
			}
			output, err := exec.CommandContext(ctx, "docker", "--context", "orbstack", "ps", "-aq", "--filter", "ancestor="+d.ID).Output()
			if err != nil || len(bytes.TrimSpace(output)) != 0 {
				t.Fatal("refuse removing image with existing container or unverified inventory")
			}
			// Remove the sole owned preparation tag first, without force. No shared tag
			// or consumer is allowed above; the immutable image ID is then safe to evict.
			for _, tag := range d.RepoTags {
				if exec.CommandContext(ctx, "docker", "--context", "orbstack", "image", "rm", tag).Run() != nil {
					t.Fatal("owned build tag removal failed")
				}
			}
			check, checkErr := exec.CommandContext(ctx, "docker", "--context", "orbstack", "image", "inspect", d.ID).CombinedOutput()
			if checkErr == nil {
				if exec.CommandContext(ctx, "docker", "--context", "orbstack", "image", "rm", d.ID).Run() != nil {
					t.Fatal("owned selected image removal failed")
				}
			} else if !bytes.Contains(check, []byte("No such image:")) {
				t.Fatal("image ID inventory failed")
			}
		}
		for _, ref := range []string{image.Reference, ownedTag} {
			raw, err := exec.CommandContext(ctx, "docker", "--context", "orbstack", "image", "inspect", ref).CombinedOutput()
			if err == nil || !bytes.Contains(raw, []byte("No such image:")) {
				t.Fatal("selected immutable/build-tag cache not demonstrably empty")
			}
		}
		t.Logf("actual cold selected cache absent name=%s reference=%s; normal/init/ephemeral Pod and container consumers checked; shared dependencies preserved", image.Name, image.Reference)
	}
}

// Positive identity proof is required even when an owned preparation tag exists.
// An empty RepoDigests list cannot prove that a mutable tag still denotes the
// immutable signed material after an external replacement.
type sp04ColdImageIdentity struct {
	ID                    string `json:"Id"`
	RepoDigests, RepoTags []string
}

func sp04ColdIdentityMatches(d sp04ColdImageIdentity, signed, ownDigest, ownTag string) bool {
	if d.ID == "" || len(d.RepoDigests) == 0 {
		return false
	}
	for _, ref := range d.RepoDigests {
		if ref != signed && ref != ownDigest {
			return false
		}
	}
	for _, tag := range d.RepoTags {
		if tag != ownTag {
			return false
		}
	}
	return true
}
