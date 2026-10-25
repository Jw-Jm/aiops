package app

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/analytics"
	"ops-platform/internal/audit"
	"ops-platform/internal/auth"
	"ops-platform/internal/contract"
	"ops-platform/internal/datascope"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	kube "ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/observability"
	"ops-platform/internal/persistence"
)

type reviewRPCPKI struct {
	serverCert tls.Certificate
	trust      auth.WorkloadTrust
	client     *http.Client
}

func newReviewRPCPKI(t *testing.T) reviewRPCPKI {
	t.Helper()
	now := time.Now().UTC()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "native-internal-ca"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour),
		IsCA: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	issue := func(serial int64, usage x509.ExtKeyUsage, uri url.URL) tls.Certificate {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		template := &x509.Certificate{
			SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: uri.String()},
			NotBefore: now.Add(-time.Hour), NotAfter: now.Add(2 * time.Hour),
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage},
			URIs:     []*url.URL{&uri},
			DNSNames: []string{strings.Split(uri.Path, "/")[4] + "." + strings.Split(uri.Path, "/")[2] + ".svc.cluster.local"},
		}
		der, err := x509.CreateCertificate(rand.Reader, template, caCert, &key.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	}
	apiURI := url.URL{Scheme: "spiffe", Host: "ops.local", Path: "/ns/native-internal/sa/ops-api"}
	serverURI := url.URL{Scheme: "spiffe", Host: "ops.local", Path: "/ns/native-internal/sa/ops-worker"}
	clientCert := issue(2, x509.ExtKeyUsageClientAuth, apiURI)
	serverCert := issue(3, x509.ExtKeyUsageServerAuth, serverURI)
	crlDER, err := x509.CreateRevocationList(rand.Reader, &x509.RevocationList{
		Number: big.NewInt(1), ThisUpdate: now, NextUpdate: now.Add(time.Hour),
	}, caCert, caKey)
	if err != nil {
		t.Fatal(err)
	}
	crl, err := x509.ParseRevocationList(crlDER)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := auth.NewWorkloadIdentity("native-internal", "ops-api")
	if err != nil {
		t.Fatal(err)
	}
	identity.URI = apiURI.String()
	return reviewRPCPKI{
		serverCert: serverCert,
		trust: auth.WorkloadTrust{
			Roots: roots, Allowed: []auth.WorkloadIdentity{identity},
			RevocationList: crl, CRLFetchedAt: now, Now: func() time.Time { return now },
		},
		client: &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
			Certificates: []tls.Certificate{clientCert},
			// The test server certificate is self-signed by the throwaway CA; the
			// client certificate is still verified by the Worker trust bundle.
			InsecureSkipVerify: true, //nolint:gosec // test-only throwaway CA
			MinVersion:         tls.VersionTLS13,
		}}},
	}
}

func reviewRPCWorkerServer(t *testing.T, handler graph.InternalHandler, pki reviewRPCPKI) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{
		Certificates: []tls.Certificate{pki.serverCert},
		ClientAuth:   tls.RequireAnyClientCert,
		MinVersion:   tls.VersionTLS13,
	}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

func reviewRPCOwnedGraph(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenant, cluster string, key ed25519.PublicKey, pki reviewRPCPKI) (*httptest.Server, *graph.Lease) {
	t.Helper()
	var mu sync.Mutex
	doc := graph.LeaseDocument{APIVersion: "coordination.k8s.io/v1", Kind: "Lease"}
	doc.Metadata.Name = "graph"
	doc.Metadata.Namespace = "review"
	doc.Metadata.UID = uuid.NewString()
	doc.Metadata.ResourceVersion = "1"
	doc.Metadata.Annotations = map[string]string{"ops.platform/owner-epoch": "0"}
	leaseAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == "PUT" {
			var next graph.LeaseDocument
			if err := json.NewDecoder(r.Body).Decode(&next); err != nil || next.Metadata.ResourceVersion != doc.Metadata.ResourceVersion {
				w.WriteHeader(409)
				return
			}
			n, _ := strconv.Atoi(doc.Metadata.ResourceVersion)
			next.Metadata.ResourceVersion = strconv.Itoa(n + 1)
			doc = next
		}
		json.NewEncoder(w).Encode(doc)
	}))
	t.Cleanup(leaseAPI.Close)
	client, err := kube.NewClient(leaseAPI.URL, leaseAPI.Client(), 10, 25)
	if err != nil {
		t.Fatal(err)
	}
	gvr := kube.GVR{Version: "v1", Resource: "pods", Kind: "Pod"}
	g := graph.New(tenant, cluster, "review-worker", []kube.GVR{gvr})
	object := unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": "review-pod", "namespace": "apps", "uid": "review-pod", "resourceVersion": "1"}, "status": map[string]any{"phase": "Running"}}}
	now := time.Now()
	if err := g.Replace(ctx, 0, kube.Snapshot{GVR: gvr, Objects: []unstructured.Unstructured{object}, State: kube.GVRState{LastListCompletedAt: now, LastConnectivityProbeAt: now, WatchConnected: true, WatchContinuous: true}}); err != nil {
		t.Fatal(err)
	}
	lease := &graph.Lease{Client: client, Graph: g, Mirror: graph.Repository{Pool: pool}, Namespace: "review", Name: "graph"}
	server := reviewRPCWorkerServer(t, graph.InternalHandler{Key: key, Authorization: graph.Authorization{Pool: pool}, Trust: pki.trust, Graph: g, Lease: lease}, pki)
	lease.Endpoint = server.URL
	if err := lease.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	return server, lease
}

// Empty evidence is an explicit source fixture, not archive qualification. The
// counter proves the real signed Worker route reached the adapter after re-proof.
type reviewRPCEvidence struct{ calls *atomic.Int64 }

func (a reviewRPCEvidence) Name() string { return "victoriametrics" }
func (a reviewRPCEvidence) Capabilities(context.Context) (evidence.CapabilitySet, error) {
	return evidence.CapabilitySet{"metric": "fixture"}, nil
}
func (a reviewRPCEvidence) Query(context.Context, evidence.Query) (evidence.Result, error) {
	a.calls.Add(1)
	return evidence.Result{SchemaVersion: "evidence-result/v2", Evidence: []evidence.Evidence{}, Freshness: "fresh", Warnings: []string{}, DegradedSources: []string{}, QueryHash: graph.ScopeDigest("empty-source")}, nil
}

type reviewRPCVerifier struct{ calls *atomic.Int64 }

func (v reviewRPCVerifier) VerifyRange(context.Context, uuid.UUID, int64, int64) error {
	v.calls.Add(1)
	return nil
}

// MR-017: actual native session re-proof with worker_runtime_role, signed contexts
// and mTLS through Evidence, Analysis and Audit HTTP routes. The companion
// integration test covers Graph 200 and a properly admitted native task.
func TestMainReviewNativeEvidenceAnalysisAuditRPC200(t *testing.T) {
	adminURL := os.Getenv("SP03_TEST_DATABASE_URL")
	if adminURL == "" {
		t.Skip("disposable PostgreSQL required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	admin, err := sql.Open("pgx", adminURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	database := "r13_rpc_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if _, err := admin.ExecContext(ctx, `CREATE DATABASE "`+database+`"`); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(adminURL)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + database
	db, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// Audit evidence is retained; never bypass append-only protections for cleanup.
	t.Logf("owned PostgreSQL evidence retained in %s", database)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpToContext(ctx, db, "../../migrations", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `GRANT CREATE ON DATABASE "`+database+`" TO schema_owner`); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpContext(ctx, db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(u.String())
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["role"] = "worker_runtime_role"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var currentRole string
	if err := pool.QueryRow(ctx, `SELECT current_user`).Scan(&currentRole); err != nil || currentRole != "worker_runtime_role" {
		t.Fatal("wrong runtime role", err)
	}
	var directAccounts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM identity.accounts`).Scan(&directAccounts); err == nil {
		t.Fatal("worker can directly read native accounts")
	}
	tenant, clusterID, sourceID := uuid.New(), uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.tenants(tenant_id,slug,display_name) VALUES($1,'rpc-review','RPC review')`, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.cluster_registrations(tenant_id,cluster_id,cluster_uid,display_name) VALUES($1,$2,'rpc-review-cluster','RPC review')`, tenant, clusterID); err != nil {
		t.Fatal(err)
	}
	seedAccount := func(role string) (uuid.UUID, uuid.UUID, string) {
		t.Helper()
		user, session := uuid.New(), uuid.New()
		subject := "user:" + user.String()
		if _, err := db.ExecContext(ctx, `INSERT INTO identity.accounts(tenant_id,user_id,login_name,display_name,subject,status,password_state,must_change_password,account_revision,credential_version,authorization_version,session_version) VALUES($1,$2,$3,'RPC',$4,'active','active',false,1,1,1,1)`, tenant, user, "rpc."+user.String(), subject); err != nil {
			t.Fatal(err)
		}
		scope := `{}`
		if role == "operator" {
			scope = `{"clusterUids":["rpc-review-cluster"],"namespaces":["apps"]}`
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO identity.role_grants(tenant_id,user_id,binding_id,role,scope,authorization_version,status) VALUES($1,$2,$3,$4,$5,1,'active')`, tenant, user, uuid.New(), role, scope); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO identity.sessions(tenant_id,session_id,user_id,mode,token_digest,csrf_digest,issued_credential_version,issued_authorization_version,issued_session_version,issued_at,last_seen_at,idle_expires_at,absolute_expires_at) VALUES($1,$2,$3,'full',$4,$5,1,1,1,clock_timestamp(),clock_timestamp(),clock_timestamp()+interval '30 minutes',clock_timestamp()+interval '8 hours')`, tenant, session, user, []byte(uuid.NewString()[:32]), []byte(uuid.NewString()[:32])); err != nil {
			t.Fatal(err)
		}
		return user, session, subject
	}
	operator, operatorSession, operatorSubject := seedAccount("operator")
	_, adminSession, adminSubject := seedAccount("platform_admin")
	var scope graph.Scope
	if err := persistence.WithTenantTx(ctx, pool, tenant, func(tx pgx.Tx) error {
		var e error
		scope, e = graph.EffectivePersistedNativeTx(ctx, tx, tenant.String(), operatorSubject, "rpc-review-cluster", operator, 1)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	public, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pki := newReviewRPCPKI(t)
	graphServer, lease := reviewRPCOwnedGraph(t, ctx, pool, tenant.String(), scope.Cluster, public, pki)
	defer graphServer.Close()
	owner := graph.InternalHandler{Graph: lease.Graph, Lease: lease}
	now := time.Now().UTC().Truncate(time.Second)
	metricServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": []any{map[string]any{"metric": map[string]string{}, "value": []any{now.Unix(), "6"}}}}})
	}))
	defer metricServer.Close()
	binding := evidence.Binding{Tenant: tenant.String(), SourceID: sourceID.String(), SourceType: "victoriametrics", Revision: 1, BackendLogicalID: "rpc-metrics", Endpoint: metricServer.URL, ScopeMapping: datascope.Mapping{RequiredLabels: map[string]string{"tenant": tenant.String()}, Scopes: map[string][]string{"cluster": {scope.Cluster}, "namespace": {"apps"}}}}
	mapping, _ := json.Marshal(binding.ScopeMapping)
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.source_registrations(tenant_id,source_id,cluster_id,source_type,instance_key,auth_ref,backend_logical_id,data_scope_mapping) VALUES($1,$2,$3,'victoriametrics','rpc-metrics','openbao://test/metrics','rpc-metrics',$4)`, tenant, sourceID, clusterID, mapping); err != nil {
		t.Fatal(err)
	}
	// Source registrations participate in the grant revision; freeze only after setup.
	if err := persistence.WithTenantTx(ctx, pool, tenant, func(tx pgx.Tx) error {
		var e error
		scope, e = graph.EffectivePersistedNativeTx(ctx, tx, tenant.String(), operatorSubject, "rpc-review-cluster", operator, 1)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	proof := analytics.Qualification{DatasetID: "metrics.pod-restarts", Operation: "aggregate", SourceRevision: 1, BindingDigest: evidence.BindingScopeDigest(binding), EvidenceDigest: graph.ScopeDigest("controlled-source"), CollectorVersion: "fixture/v1", VerifiedUntil: now.Add(time.Hour), CoverageFrom: now.Add(-time.Hour), CoverageTo: now, MaxWindowSeconds: 3600}
	adapter, err := analytics.NewVictoria(binding, metricServer.Client(), (evidence.Repository{Pool: pool}).CheckBinding, []analytics.Qualification{proof})
	if err != nil {
		t.Fatal(err)
	}
	var evidenceCalls, verifyCalls atomic.Int64
	var sequence audit.Sequence
	if err := persistence.WithTenantTx(ctx, pool, tenant, func(tx pgx.Tx) error {
		var e error
		sequence, e = audit.Append(ctx, tx, audit.Entry{TenantID: tenant, RecordID: uuid.New(), EventType: "review.rpc", EntityKind: "review", Subject: adminSubject, Payload: map[string]any{"case": "native-rpc"}})
		return e
	}); err != nil {
		t.Fatal(err)
	}
	handlers := map[string]graph.InternalHandler{tenant.String() + "|" + scope.Cluster: owner}
	metrics := observability.NewMetrics()
	mux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/internal/v1/evidence:query":
			serveWorkerEvidence(w, r, pki.trust, public, pool, map[string]evidence.Adapter{sourceID.String(): reviewRPCEvidence{&evidenceCalls}}, nil, handlers, metrics)
		case "/internal/v1/analysis:query":
			serveWorkerAnalysis(w, r, pki.trust, public, pool, map[string]*analytics.Victoria{sourceID.String(): adapter}, handlers, nil)
		case "/internal/v1/audit:verify":
			serveWorkerAudit(w, r, pki.trust, public, pool, "native-internal", reviewRPCVerifier{&verifyCalls})
		default:
			w.WriteHeader(404)
		}
	})
	server := httptest.NewUnstartedServer(mux)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{pki.serverCert}, ClientAuth: tls.RequireAnyClientCert, MinVersion: tls.VersionTLS13}
	server.StartTLS()
	defer server.Close()
	post := func(path, audience, subject string, session uuid.UUID, body any, source string) (int, json.RawMessage) {
		t.Helper()
		raw, _ := json.Marshal(body)
		claims := graph.Claims{Source: source, Subject: subject, Scope: scope, GrantSnapshotDigest: graph.ScopeDigest(scope), Audience: audience, QueryDigest: graph.ScopeDigest(json.RawMessage(raw)), Deadline: time.Now().Add(10 * time.Second)}
		if source == graph.SourceNativeInteractive {
			claims.Native = &graph.NativeProof{SessionID: session.String()}
		}
		token, err := graph.SignContext(key, claims)
		if err != nil {
			t.Fatal(err)
		}
		request, err := http.NewRequest("POST", server.URL+path, bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("X-Graph-Context", token)
		response, err := pki.client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var out json.RawMessage
		if err := json.NewDecoder(response.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, out
	}
	intent := analytics.QueryIntent{SchemaVersion: "query-intent/v1", DatasetID: proof.DatasetID, Operation: proof.Operation, Metrics: []string{"restart_increase"}, Dimensions: []string{}, Filters: []analytics.Filter{}, Sort: []analytics.Sort{}, Limit: 1, TimeRange: &analytics.QueryTimeRange{From: now.Add(-time.Minute), To: now, Timezone: "UTC"}}
	cases := []struct {
		path, audience, subject string
		session                 uuid.UUID
		body                    any
		schema                  string
	}{
		{"/internal/v1/evidence:query", "platform-evidence-query", operatorSubject, operatorSession, map[string]any{"evidenceId": uuid.NewString(), "sourceRegistrationId": sourceID.String(), "query": map[string]any{"resourceCanonicalId": "unused", "namespace": "apps", "queryTemplate": "fixture/v1", "from": now.Add(-time.Minute), "to": now, "limit": 1}}, "https://ops.local/schemas/evidence-result/v2"},
		{"/internal/v1/analysis:query", "platform-analysis-query", operatorSubject, operatorSession, map[string]any{"intent": intent, "ownerEpoch": 1}, ""},
		{"/internal/v1/audit:verify", "platform-audit-verify", adminSubject, adminSession, map[string]any{"tenantId": tenant.String(), "first": strconv.FormatInt(sequence.Audit, 10), "last": strconv.FormatInt(sequence.Audit, 10)}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.audience, func(t *testing.T) {
			if err := lease.Tick(ctx); err != nil {
				t.Fatal(err)
			}
			status, raw := post(tc.path, tc.audience, tc.subject, tc.session, tc.body, graph.SourceNativeInteractive)
			if status != 200 {
				t.Fatalf("native RPC=%d %s", status, raw)
			}
			if tc.schema != "" {
				if err := contract.Validate(tc.schema, raw); err != nil {
					t.Fatal(err)
				}
			}
			if tc.audience == "platform-analysis-query" {
				var out struct {
					Result analytics.QueryResult `json:"result"`
				}
				if err := json.Unmarshal(raw, &out); err != nil || len(out.Result.Rows) != 1 || out.Result.Rows[0].Value != 6 {
					t.Fatal("analysis fixture not read")
				}
			}
			status, _ = post(tc.path, tc.audience, tc.subject, tc.session, tc.body, graph.SourceLegacy)
			if status == 200 {
				t.Fatal("legacy borrowed native authority")
			}
		})
	}
	if evidenceCalls.Load() != 1 || verifyCalls.Load() != 1 {
		t.Fatal("RPC did not reach source/verifier exactly once")
	}
	if _, err := db.ExecContext(ctx, `UPDATE identity.sessions SET revoked_at=clock_timestamp(),revoked_reason='review' WHERE tenant_id=$1 AND session_id IN($2,$3)`, tenant, operatorSession, adminSession); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		status, _ := post(tc.path, tc.audience, tc.subject, tc.session, tc.body, graph.SourceNativeInteractive)
		if status == 200 {
			t.Fatal("revoked session passed RPC")
		}
	}
	if evidenceCalls.Load() != 1 || verifyCalls.Load() != 1 {
		t.Fatal("revoked session reached source/verifier")
	}
}
