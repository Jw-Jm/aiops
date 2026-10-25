package integration

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/analytics"
	"ops-platform/internal/assistant"
	"ops-platform/internal/auth"
	"ops-platform/internal/contract"
	"ops-platform/internal/graph"
	"ops-platform/internal/httpapi"
	kube "ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/investigation"
	gateway "ops-platform/internal/investigation/mcp"
	"ops-platform/internal/investigation/tools"
)

// Only the Kubernetes Lease transport and input Pod are protocol fixtures. The
// ownership state machine, PostgreSQL mirror, signed Worker RPC and graph are real.
func mainReviewOwnedGraph(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenant, cluster string, key ed25519.PublicKey, pki nativeInternalPKI) (*httptest.Server, *graph.Lease) {
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
	server := nativeInternalWorkerServer(t, graph.InternalHandler{Key: key, Authorization: graph.Authorization{Pool: pool}, Trust: pki.trust, Graph: g, Lease: lease}, pki)
	lease.Endpoint = server.URL
	if err := lease.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	return server, lease
}

type mainReviewBeforeSource struct {
	base  http.RoundTripper
	check func()
}

func (tr mainReviewBeforeSource) RoundTrip(r *http.Request) (*http.Response, error) {
	tr.check()
	return tr.base.RoundTrip(r)
}

func TestMainReviewCurrentResourceMCPReservationBeforeSource(t *testing.T) {
	ctx, db, worker, b, repo, jobs := assistantExtendedFixture(t)
	admission := repo.Admit
	repo.Admit = func(ctx context.Context, c assistant.Conversation, s string) (assistant.Admission, error) {
		a, err := admission(ctx, c, s)
		a.Budget.RawQueries = 1
		return a, err
	}
	conversation, err := repo.Create(ctx, assistant.Conversation{TenantID: b.TenantID, OwnerSubject: "assistant-owner", Title: "Current resource budget", Mode: "data_query", AuthorizationDomain: "operational", ClusterUID: b.ClusterUID, Binding: assistant.ConversationBinding{Timezone: "UTC", Selection: analytics.Selection{Namespaces: []string{"apps"}}}})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := repo.Submit(ctx, b.TenantID, conversation.ConversationID, conversation.OwnerSubject, "budget", assistant.SubmitMessage{Message: "统计当前Pod", ExpectedConversationRevision: conversation.Revision})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := jobs.Claim(ctx, b.TenantID, "mcp-budget-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	job, err := jobs.Get(ctx, b.TenantID, turn.JobID)
	if err != nil {
		t.Fatal(err)
	}
	public, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pki := newNativeInternalPKI(t)
	workerServer, graphLease := mainReviewOwnedGraph(t, ctx, worker, b.TenantID.String(), b.ClusterUID, public, pki)
	var sourceCalls atomic.Int64
	sourceClient := *pki.client
	sourceClient.Transport = mainReviewBeforeSource{base: pki.client.Transport, check: func() {
		sourceCalls.Add(1)
		var reserved, admissions int64
		if err := db.QueryRowContext(ctx, `SELECT (budget_reserved->>'rawQueries')::bigint FROM investigation.jobs WHERE tenant_id=$1 AND job_id=$2`, b.TenantID, job.JobID).Scan(&reserved); err != nil || reserved != 1 {
			t.Errorf("source preceded durable raw query reservation: %d %v", reserved, err)
		}
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM investigation.admissions WHERE tenant_id=$1 AND job_id=$2 AND state='reserved' AND (reservation->>'rawQueries')::bigint=1`, b.TenantID, job.JobID).Scan(&admissions); err != nil || admissions != 1 {
			t.Errorf("source preceded durable admission: %d %v", admissions, err)
		}
	}}
	api := httpapi.AssistantTools{Legacy: httpapi.InvestigationTools{SP04: &httpapi.SP04Handlers{Pool: repo.Pool, Client: &sourceClient, SigningKey: key, EndpointAllowed: func(endpoint string) bool { return endpoint == workerServer.URL }}}}
	signer := investigation.ContextSigner{Transit: newNativeTestTransit(t), Key: "mcp-budget"}
	files := sp06Certificates(t, t.TempDir())
	ca, err := os.ReadFile(files["ca.pem"])
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(ca)
	crlPEM, err := os.ReadFile(files["crl.pem"])
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(crlPEM)
	crl, err := x509.ParseRevocationList(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := auth.NewWorkloadIdentity("sp06-test", "ops-investigator")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := (gateway.Gateway{Repository: jobs, Signer: signer, Trust: auth.WorkloadTrust{Roots: roots, Allowed: []auth.WorkloadIdentity{identity}, RevocationList: crl, CRLFetchedAt: time.Now()}, API: api, Authorize: api.CheckScope}).Handler()
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	serverCert, err := tls.LoadX509KeyPair(files["ops-api.pem"], files["ops-api.key"])
	if err != nil {
		t.Fatal(err)
	}
	server.TLS = &tls.Config{Certificates: []tls.Certificate{serverCert}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: roots, MinVersion: tls.VersionTLS13}
	server.StartTLS()
	defer server.Close()
	clientCert, err := tls.LoadX509KeyPair(files["ops-investigator.pem"], files["ops-investigator.key"])
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, Certificates: []tls.Certificate{clientCert}, ServerName: "ops-api.sp06-test.svc.cluster.local", MinVersion: tls.VersionTLS13}}
	defer transport.CloseIdleConnections()
	defs, err := tools.ForJob(job)
	if err != nil {
		t.Fatal(err)
	}
	token, err := signer.IssueRegistered(ctx, jobs, job, lease, "platform-mcp-gateway", identity.URI, tools.Names(defs))
	if err != nil {
		t.Fatal(err)
	}
	session, err := sdk.NewClient(&sdk.Implementation{Name: "main-review", Version: "1"}, nil).Connect(ctx, &sdk.StreamableClientTransport{Endpoint: server.URL, HTTPClient: &http.Client{Transport: sp06BearerTransport{transport, token}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	intent := analytics.QueryIntent{SchemaVersion: "query-intent/v1", DatasetID: "resources.current", Operation: "aggregate", Metrics: []string{"resource_count"}, Dimensions: []string{"kind"}, Filters: []analytics.Filter{{Field: "kind", Operator: "eq", Value: "Pod"}}, Sort: []analytics.Sort{}, Limit: 50}
	call := func(q analytics.QueryIntent) *sdk.CallToolResult {
		t.Helper()
		args, _ := json.Marshal(map[string]any{"intent": q})
		a, err := jobs.AllocateTool(ctx, lease, "execute_analysis_query", args)
		if err != nil {
			t.Fatal(err)
		}
		params := &sdk.CallToolParams{Name: "execute_analysis_query", Arguments: json.RawMessage(args)}
		params.SetMeta(map[string]any{"ops/stepId": a.StepID.String(), "ops/jti": a.JTI})
		out, err := session.CallTool(ctx, params)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if err := graphLease.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	first := call(intent)
	if first.IsError || sourceCalls.Load() != 1 {
		t.Fatalf("current query failed or source call count differs: %+v calls=%d", first, sourceCalls.Load())
	}
	encodedFirst, err := json.Marshal(first.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var currentResult struct {
		Data struct {
			Stored investigation.StoredQueryResult `json:"storedQueryResult"`
		} `json:"data"`
	}
	if err := json.Unmarshal(encodedFirst, &currentResult); err != nil || len(currentResult.Data.Stored.Result.Rows) != 1 || currentResult.Data.Stored.Result.Rows[0].Value != 1 {
		t.Fatal("current Pod count did not survive MCP settlement")
	}
	loaded, err := jobs.Get(ctx, b.TenantID, job.JobID)
	if err != nil || loaded.Consumed.RawQueries != 1 || loaded.Reserved != (investigation.Usage{}) {
		t.Fatalf("query budget settlement: %+v %v", loaded, err)
	}
	intent.Filters[0].Value = "Node"
	if out := call(intent); !out.IsError {
		t.Fatal("exhausted raw query budget accepted")
	}
	if sourceCalls.Load() != 1 {
		t.Fatal("denied query reached source")
	}
}

func TestMainReviewNativeInteractiveAndTaskGraphRPC200(t *testing.T) {
	f := newNativeJobFixture(t)
	account := f.newAccount(t, `"apps"`)
	_, turn := f.submit(t, account, "Native Worker RPC")
	_, err := f.jobs.Claim(context.Background(), f.tenantID, "native-rpc-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	job, err := f.jobs.Get(context.Background(), f.tenantID, turn.JobID)
	if err != nil {
		t.Fatal(err)
	}
	public, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pki := newNativeInternalPKI(t)
	server, lease := mainReviewOwnedGraph(t, f.ctx, f.workerPool, f.tenantID.String(), f.clusterUID, public, pki)
	payload, _ := json.Marshal(graph.Query{QueryKind: "list", Kind: "Pod", Scope: job.Scope, ExpectedOwnerEpoch: 1, MaxDepth: 1, MaxNodes: 10, MaxEdges: 10, RelationKinds: []string{}})
	post := func(source string, proof *graph.NativeProof) int {
		t.Helper()
		token, err := graph.SignContext(key, graph.Claims{Source: source, Native: proof, Subject: account.subject, Scope: job.Scope, GrantSnapshotDigest: job.Assistant.GrantSnapshotDigest, Audience: "platform-graph-query", QueryDigest: graph.ScopeDigest(json.RawMessage(payload)), Deadline: time.Now().Add(10 * time.Second)})
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest("POST", server.URL+"/internal/v1/graph:query", bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Graph-Context", token)
		response, err := pki.client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if response.StatusCode == 200 {
			var result json.RawMessage
			if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
				t.Fatal(err)
			}
			if err := contract.Validate("https://ops.local/schemas/resource-graph/v2", result); err != nil {
				t.Fatal(err)
			}
			var snapshot graph.Result
			if err := json.Unmarshal(result, &snapshot); err != nil || len(snapshot.Nodes) != 1 || snapshot.Nodes[0].Name != "review-pod" {
				t.Fatal("native RPC lost graph data")
			}

		}
		return response.StatusCode
	}
	if err := lease.Tick(f.ctx); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		source string
		proof  *graph.NativeProof
	}{{graph.SourceNativeInteractive, &graph.NativeProof{SessionID: account.sessionID.String()}}, {graph.SourceNativeTask, &graph.NativeProof{JobID: job.JobID.String()}}} {
		if status := post(tc.source, tc.proof); status != 200 {
			t.Fatalf("%s RPC=%d, want contract-valid 200", tc.source, status)
		}
	}
	if _, err := f.db.ExecContext(f.ctx, `UPDATE identity.role_grants SET status='revoked',revoked_at=clock_timestamp(),revoked_reason='review' WHERE tenant_id=$1 AND user_id=$2 AND role='operator'`, f.tenantID, account.userID); err != nil {
		t.Fatal(err)
	}
	if status := post(graph.SourceNativeTask, &graph.NativeProof{JobID: job.JobID.String()}); status == 200 {
		t.Fatal("revoked task reached graph")
	}
}
