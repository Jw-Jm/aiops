package app

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"ops-platform/internal/auth"
	"ops-platform/internal/contract"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/integrations/deepflow"
	"ops-platform/internal/integrations/kubernetes"
	"ops-platform/internal/integrations/redfish"
	"ops-platform/internal/observability"
	"ops-platform/internal/resourcestore"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
)

type SP04Cluster struct {
	SourceScopeDigest                                                                                      string `json:"-"`
	Tenant, ClusterUID, SourceID, BackendLogicalID, Endpoint, CAFile, TokenFile, LeaseNamespace, LeaseName string
	SourceRevision                                                                                         int64
	QPS                                                                                                    int
	Burst                                                                                                  int
}
type SP04Source struct {
	Name            string
	Binding         evidence.Binding
	CAFile          string
	CredentialFile  string
	ScopeProbe      *evidence.Query
	Mode            string
	FrozenEndpoints []deepflow.Endpoint
}
type SP04Config struct {
	ListenAddress, OwnerEndpoint, ServerName, CertificateFile, PrivateKeyFile, CAFile, CRLFile, Namespace, ContextPrivateKeyFile, ContextPublicKeyFile, ArchiveBackendLogicalID string
	AllowedWorkerCIDRs                                                                                                                                                          []string
	Clusters                                                                                                                                                                    []SP04Cluster
	Sources                                                                                                                                                                     []SP04Source
}

func loadSP04() (*SP04Config, error) {
	path := os.Getenv("SP04_RUNTIME_FILE")
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) > 256<<10 {
		return nil, errors.New("SP04 runtime configuration unavailable")
	}
	var c SP04Config
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&c) != nil || c.Namespace == "" || c.CAFile == "" || c.CRLFile == "" || c.CertificateFile == "" || c.PrivateKeyFile == "" || c.ServerName == "" {
		return nil, errors.New("SP04 runtime configuration invalid")
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return nil, errors.New("SP04 runtime configuration has trailing payload")
	}
	if override := os.Getenv("SP04_OWNER_ENDPOINT"); override != "" {
		c.OwnerEndpoint = override
	}
	return &c, nil
}

type fileCRL struct{ path string }

func (f fileCRL) CurrentWorkloadCRL() (*x509.RevocationList, time.Time) {
	raw, err := os.ReadFile(f.path)
	if err != nil {
		return nil, time.Time{}
	}
	if b, _ := pem.Decode(raw); b != nil {
		raw = b.Bytes
	}
	crl, err := x509.ParseRevocationList(raw)
	if err != nil {
		return nil, time.Time{}
	}
	stat, err := os.Stat(f.path)
	if err != nil {
		return nil, time.Time{}
	}
	return crl, stat.ModTime()
}
func (c *SP04Config) tls(ctx context.Context, server bool) (*tls.Config, auth.WorkloadTrust, error) {
	raw, err := os.ReadFile(c.CAFile)
	if err != nil {
		return nil, auth.WorkloadTrust{}, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(raw) {
		return nil, auth.WorkloadTrust{}, errors.New("SP04 CA invalid")
	}
	cert, err := tls.LoadX509KeyPair(c.CertificateFile, c.PrivateKeyFile)
	if err != nil {
		return nil, auth.WorkloadTrust{}, err
	}
	apiID, err := auth.NewWorkloadIdentity(c.Namespace, "ops-api")
	if err != nil {
		return nil, auth.WorkloadTrust{}, err
	}
	workerID, err := auth.NewWorkloadIdentity(c.Namespace, "ops-worker")
	if err != nil {
		return nil, auth.WorkloadTrust{}, err
	}
	trust := auth.WorkloadTrust{Roots: roots, Allowed: []auth.WorkloadIdentity{apiID, workerID}, CRLSource: fileCRL{c.CRLFile}}
	if server {
		trust.Allowed = []auth.WorkloadIdentity{apiID}
		config, err := auth.WorkloadMTLSServerConfig(ctx, trust)
		if err != nil {
			return nil, trust, err
		}
		config.Certificates = []tls.Certificate{cert}
		return config, trust, nil
	}
	peerTrust := trust
	peerTrust.Allowed = []auth.WorkloadIdentity{workerID}
	return &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{cert}, ServerName: c.ServerName, VerifyConnection: func(state tls.ConnectionState) error {
		if len(state.PeerCertificates) == 0 {
			return graph.ErrScope
		}
		_, err := auth.VerifyWorkload(auth.WithWorkloadTrust(ctx, peerTrust), state.PeerCertificates[0])
		return err
	}}, trust, nil
}
func (c *SP04Config) endpointAllowed(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil {
		return false
	}
	for _, allowed := range c.AllowedWorkerCIDRs {
		_, network, err := net.ParseCIDR(allowed)
		if err == nil && network.Contains(ip) {
			return true
		}
	}
	return false
}
func NewSP04API(ctx context.Context, pool *pgxpool.Pool) (*httpapi.SP04Handlers, error) {
	h := &httpapi.SP04Handlers{Pool: pool}
	c, err := loadSP04()
	if err != nil || c == nil {
		return h, err
	}
	config, _, err := c.tls(ctx, false)
	if err != nil {
		return nil, err
	}
	key, err := os.ReadFile(c.ContextPrivateKeyFile)
	if err != nil || len(key) != ed25519.PrivateKeySize {
		return nil, errors.New("SP04 scope signing key invalid")
	}
	h.SigningKey = key
	h.Client = &http.Client{Transport: &http.Transport{TLSClientConfig: config}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	h.EndpointAllowed = c.endpointAllowed
	return h, nil
}

type tokenTransport struct {
	base http.RoundTripper
	file string
}

func (t tokenTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	raw, err := os.ReadFile(t.file)
	if err != nil {
		return nil, errors.New("source credential unavailable")
	}
	copy := r.Clone(r.Context())
	copy.Header = r.Header.Clone()
	copy.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(raw)))
	return t.base.RoundTrip(copy)
}
func clusterClient(c SP04Cluster) (*kubernetes.Client, error) {
	raw, err := os.ReadFile(c.CAFile)
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(raw) {
		return nil, errors.New("Kubernetes CA invalid")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}}
	qps, burst := c.QPS, c.Burst
	if qps == 0 {
		qps = 10
	}
	if burst == 0 {
		burst = 25
	}
	if qps > 10 || burst > 25 {
		return nil, errors.New("SP04 two-worker cluster budget requires QPS<=10 and burst<=25 per worker")
	}
	return kubernetes.NewClient(c.Endpoint, &http.Client{Transport: tokenTransport{transport, c.TokenFile}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, qps, burst)
}
func StartSP04Worker(ctx context.Context, pool *pgxpool.Pool, archive *evidence.ArchiveService, runtime *observability.Runtime) (func(), error) {
	c, err := loadSP04()
	if err != nil {
		return nil, err
	}
	if c == nil {
		return func() {}, nil
	}
	if c.ListenAddress == "" || !c.endpointAllowed(c.OwnerEndpoint) || c.ArchiveBackendLogicalID == "" {
		return nil, errors.New("SP04 Worker listener/route/archive identity invalid")
	}
	key, err := os.ReadFile(c.ContextPublicKeyFile)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return nil, errors.New("SP04 context verification key invalid")
	}
	tlsConfig, trust, err := c.tls(ctx, true)
	if err != nil {
		return nil, err
	}
	seenSources := map[string]bool{}
	for _, source := range c.Sources {
		if _, err := uuid.Parse(source.Binding.SourceID); err != nil || seenSources[source.Binding.SourceID] || (source.Binding.SourceType != "" && source.Name != source.Binding.SourceType) {
			return nil, errors.New("source IDs must be unique and Adapter type must match registration")
		}
		seenSources[source.Binding.SourceID] = true
	}
	archive.BackendLogicalID = c.ArchiveBackendLogicalID
	runContext, cancel := context.WithCancel(ctx)
	var group sync.WaitGroup
	handlers := map[string]graph.InternalHandler{}
	clusterBudgets := map[string]*kubernetes.RateBudget{}
	initialized := false
	defer func() {
		if !initialized {
			cancel()
			group.Wait()
		}
	}()
	for _, cluster := range c.Clusters {
		if _, err := uuid.Parse(cluster.Tenant); err != nil {
			cancel()
			return nil, err
		}
		if cluster.SourceRevision < 1 || cluster.BackendLogicalID == "" {
			cancel()
			return nil, errors.New("Kubernetes source revision/backend binding required")
		}
		if _, exists := handlers[cluster.Tenant+"|"+cluster.ClusterUID]; exists {
			return nil, errors.New("duplicate tenant cluster configuration")
		}
		client, err := clusterClient(cluster)
		if err != nil {
			cancel()
			return nil, err
		}
		budget := clusterBudgets[cluster.ClusterUID]
		if budget == nil {
			qps := cluster.QPS
			if qps == 0 {
				qps = 10
			}
			burst := cluster.Burst
			if burst == 0 {
				burst = 25
			}
			budget, _ = kubernetes.NewRateBudget(qps, burst)
			clusterBudgets[cluster.ClusterUID] = budget
		}
		if err := client.ShareRateBudget(budget); err != nil {
			return nil, err
		}
		sourceRepo := evidence.Repository{Pool: pool}
		initialBinding, err := sourceRepo.RegisteredBinding(ctx, evidence.Binding{Tenant: cluster.Tenant, SourceID: cluster.SourceID, Revision: cluster.SourceRevision, SourceType: "kubernetes", BackendLogicalID: cluster.BackendLogicalID})
		if err != nil {
			cancel()
			return nil, err
		}
		cluster.SourceScopeDigest = evidence.BindingScopeDigest(initialBinding)
		graphBindings := []evidence.Binding{initialBinding}
		for _, source := range c.Sources {
			if source.Binding.Tenant == cluster.Tenant && slices.Contains(source.Binding.ScopeMapping.Scopes["cluster"], cluster.ClusterUID) && (source.Name == "redfish" || source.Name == "deepflow") {
				graphBindings = append(graphBindings, source.Binding)
			}
		}
		validateSources := func(pass context.Context) error {
			for _, b := range graphBindings {
				if err := sourceRepo.CheckBinding(pass, b); err != nil {
					return err
				}
			}
			return nil
		}
		g := graph.New(cluster.Tenant, cluster.ClusterUID, uuid.NewString(), kubernetes.CoreRequiredGVRs())
		lease := &graph.Lease{Client: client, Graph: g, Mirror: graph.Repository{Pool: pool}, Namespace: cluster.LeaseNamespace, Name: cluster.LeaseName, Endpoint: c.OwnerEndpoint, Logger: runtime.Logger}
		handlers[cluster.Tenant+"|"+cluster.ClusterUID] = graph.InternalHandler{Graph: g, Lease: lease, Key: key, Authorization: graph.Authorization{Pool: pool}, Trust: trust, ValidateSources: validateSources, Metrics: runtime.Metrics, Logger: runtime.Logger}
		archiveQueue := newProjectionArchiveQueue(runContext, cluster, g, archive, &group)
		for _, gvr := range kubernetes.CoreRequiredGVRs() {
			gvr := gvr
			group.Add(1)
			go func() {
				defer group.Done()
				sink := collectorSink(runContext, cluster, g, resourcestore.Repository{Pool: pool, ExpectedRevision: cluster.SourceRevision, BackendLogicalID: cluster.BackendLogicalID}, archiveQueue)
				for runContext.Err() == nil {
					err := client.Run(runContext, gvr, sink)
					if err != nil && runContext.Err() == nil {
						runtime.Metrics.RecordOperation("kubernetes", "query", "unavailable")
						runtime.Logger.WarnContext(runContext, "Kubernetes collection unavailable", slog.String("gvr", gvr.Path()), slog.String("errorClass", collectionErrorClass(err)))
						select {
						case <-runContext.Done():
							return
						case <-time.After(time.Second):
						}
					}
				}

			}()
		}
		group.Add(1)
		go func() {
			defer group.Done()
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				deadline, done := context.WithTimeout(runContext, 3*time.Second)
				err := lease.Tick(deadline)
				done()
				result := "ok"
				if err != nil {
					result = "unavailable"
				}
				if err != nil && !g.Qualified(time.Now()) {
					runtime.Logger.WarnContext(runContext, "Graph ownership not ready", slog.Any("qualification", g.QualificationFailures(time.Now())))
				}
				runtime.Metrics.RecordOperation("graph", "query", result)
				select {
				case <-runContext.Done():
					g.InvalidateOwner()
					return
				case <-ticker.C:
				}
			}
		}()
	}
	adapters := map[string]evidence.Adapter{}
	for _, source := range c.Sources {
		if source.Name != "victoriametrics" && source.Name != "victorialogs" && source.Name != "redfish" && source.Name != "deepflow" {
			cancel()
			return nil, errors.New("SP04 source adapter configuration is not qualified")
		}
		client := &http.Client{}
		if source.CAFile != "" {
			raw, err := os.ReadFile(source.CAFile)
			if err != nil {
				cancel()
				return nil, err
			}
			roots := x509.NewCertPool()
			if !roots.AppendCertsFromPEM(raw) {
				cancel()
				return nil, errors.New("source CA invalid")
			}
			client.Transport = &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}}
		}
		if source.CredentialFile != "" {
			base := client.Transport
			if base == nil {
				base = http.DefaultTransport
			}
			client.Transport = tokenTransport{base, source.CredentialFile}
		}
		source.Binding.SourceType = source.Name
		repo := evidence.Repository{Pool: pool}
		if source.Name == "deepflow" || source.Name == "redfish" {
			var adapter, candidate evidence.Adapter
			if source.Name == "deepflow" {
				// A live mapping requires a separately qualified Server UID
				// contract; frozen endpoint files cannot promote themselves.
				if source.Mode != "fixture_only" {
					cancel()
					return nil, errors.New("live DeepFlow identity mapper unverified")
				}
				mappings := deepflow.FrozenMappings{}
				for _, e := range source.FrozenEndpoints {
					if _, exists := mappings[e.CanonicalID]; exists {
						cancel()
						return nil, errors.New("conflicting DeepFlow fixture identities")
					}
					mappings[e.CanonicalID] = e
				}
				adapter, err = deepflow.New(source.Binding, client, mappings, repo.Authorize, false, "fixture_only")
				if err != nil {
					cancel()
					return nil, err
				}
				candidate, err = deepflow.New(source.Binding, client, mappings, repo.CheckBinding, false, "fixture_only")
				if err != nil {
					cancel()
					return nil, err
				}
			} else {
				var credential struct {
					Username string `json:"username"`
					Password string `json:"password"`
				}
				raw, err := os.ReadFile(source.CredentialFile)
				if err != nil || json.Unmarshal(raw, &credential) != nil {
					cancel()
					return nil, errors.New("Redfish credential unavailable")
				}
				// Basic auth is injected only by Gofish's guarded GET transport.
				if wrapped, ok := client.Transport.(tokenTransport); ok {
					client.Transport = wrapped.base
				}
				if len(source.Binding.ScopeMapping.Scopes["cluster"]) != 1 {
					cancel()
					return nil, errors.New("Redfish scope unverified")
				}
				config := redfish.Config{Endpoint: source.Binding.Endpoint, Tenant: source.Binding.Tenant, Scope: source.Binding.ScopeMapping.Scopes["cluster"][0], SourceID: source.Binding.SourceID, Username: credential.Username, Password: credential.Password, Client: client}
				adapter = &redfish.Adapter{Binding: source.Binding, Config: config, Authorize: repo.Authorize, Mode: source.Mode}
				candidate = &redfish.Adapter{Binding: source.Binding, Config: config, Authorize: repo.CheckBinding, Mode: source.Mode}
			}
			if source.ScopeProbe != nil && len(source.Binding.ScopeMapping.Scopes["cluster"]) > 0 {
				probe := *source.ScopeProbe
				probe.Scope = graph.Scope{Tenant: source.Binding.Tenant, Cluster: source.Binding.ScopeMapping.Scopes["cluster"][0], Namespaces: source.Binding.ScopeMapping.Scopes["namespace"], ClusterScoped: source.Name == "redfish", AuthorizationRevision: "source-isolation-probe"}
				renewSourceProof(runContext, &group, source.Binding.SourceID, func(proofContext context.Context) error {
					currentProbe := probe
					if source.Name == "redfish" {
						// Inventory is a current observation, not a historic query.
						// Renew with the same bounded duration and immutable identity.
						currentProbe.To = time.Now().UTC()
						currentProbe.From = currentProbe.To.Add(-probe.To.Sub(probe.From))
					}
					return repo.VerifyAdapter(proofContext, source.Binding, candidate, currentProbe)
				}, handlers, source.Binding, runtime)
			}
			if hardwareAdapter, ok := adapter.(*redfish.Adapter); ok {
				if owner, found := handlers[source.Binding.Tenant+"|"+hardwareAdapter.Config.Scope]; found {
					collectHardware(runContext, &group, hardwareAdapter, owner.Graph, resourcestore.Repository{Pool: pool, ExpectedRevision: source.Binding.Revision, BackendLogicalID: source.Binding.BackendLogicalID}, archive, runtime.Metrics)
				}
			}
			adapters[source.Binding.SourceID] = adapter
			continue
		}
		adapter, err := evidence.NewVictoria(source.Name, source.Binding, client, (evidence.Repository{Pool: pool}).Authorize)
		if err != nil {
			cancel()
			return nil, err
		}
		if source.ScopeProbe != nil && len(source.Binding.ScopeMapping.Scopes["cluster"]) > 0 {
			probe := *source.ScopeProbe
			probe.Scope = graph.Scope{Tenant: source.Binding.Tenant, Cluster: source.Binding.ScopeMapping.Scopes["cluster"][0], Namespaces: source.Binding.ScopeMapping.Scopes["namespace"], ClusterScoped: false, AuthorizationRevision: "source-isolation-probe"}
			renewSourceProof(runContext, &group, source.Binding.SourceID, func(proofContext context.Context) error {
				return (evidence.Repository{Pool: pool}).VerifyVictoria(proofContext, adapter, probe)
			}, handlers, source.Binding, runtime)
		}
		adapters[source.Binding.SourceID] = adapter
	}
	group.Add(1)
	go func() {
		defer group.Done()
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			pass, done := context.WithTimeout(runContext, 8*time.Second)
			err := archive.MaintenancePass(pass, adapters)
			done()
			status := "ok"
			if err != nil {
				status = "unavailable"
			}
			runtime.Metrics.RecordOperation("archive", "write", status)
			select {
			case <-runContext.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	dispatch := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/internal/v1/evidence:query" || r.URL.Path == "/internal/v1/evidence:read" {
			serveWorkerEvidence(w, r, trust, key, pool, adapters, archive, handlers, runtime.Metrics)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, (64<<10)+1))
		if err != nil || len(raw) > 64<<10 {
			w.WriteHeader(400)
			return
		}
		var query graph.Query
		if json.Unmarshal(raw, &query) != nil {
			w.WriteHeader(400)
			return
		}
		handler, ok := handlers[query.Scope.Tenant+"|"+query.Scope.Cluster]
		if !ok {
			w.WriteHeader(503)
			return
		}
		r.Body = io.NopCloser(strings.NewReader(string(raw)))
		handler.ServeHTTP(w, r)
	})
	listener, err := net.Listen("tcp", c.ListenAddress)
	if err != nil {
		cancel()
		group.Wait()
		return nil, err
	}
	server := &http.Server{Handler: dispatch, TLSConfig: tlsConfig, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	group.Add(1)
	go func() {
		defer group.Done()
		if err := server.Serve(tls.NewListener(listener, tlsConfig)); err != nil && !errors.Is(err, http.ErrServerClosed) {
			runtime.Metrics.RecordOperation("graph", "error", "unavailable")
			cancel()
		}
	}()
	initialized = true
	return func() {
		cancel()
		shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = server.Shutdown(shutdown)
		group.Wait()
	}, nil
}
func serveWorkerEvidence(w http.ResponseWriter, r *http.Request, trust auth.WorkloadTrust, key ed25519.PublicKey, pool *pgxpool.Pool, adapters map[string]evidence.Adapter, archive *evidence.ArchiveService, handlers map[string]graph.InternalHandler, metrics *observability.Metrics) {
	fail := func(status int, code string) {
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]any{"code": code})
	}
	if r.Method != "POST" || r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		fail(403, "FORBIDDEN")
		return
	}
	if _, err := auth.VerifyWorkload(auth.WithWorkloadTrust(r.Context(), trust), r.TLS.PeerCertificates[0]); err != nil {
		fail(403, "FORBIDDEN")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 {
		fail(400, "INVALID_ARGUMENT")
		return
	}
	audience := "platform-evidence-query"
	if r.URL.Path == "/internal/v1/evidence:read" {
		audience = "platform-evidence-read"
	}
	claims, err := graph.VerifyContext(key, r.Header.Get("X-Graph-Context"), audience, raw)
	if err != nil {
		fail(403, "FORBIDDEN")
		return
	}
	var q struct {
		EvidenceID string         `json:"evidenceId"`
		SourceID   string         `json:"sourceRegistrationId"`
		Query      evidence.Query `json:"query"`
	}
	if json.Unmarshal(raw, &q) != nil {
		fail(400, "INVALID_ARGUMENT")
		return
	}
	scope, err := (graph.Authorization{Pool: pool}).Effective(r.Context(), claims.Scope.Tenant, claims.Subject, claims.Scope.Cluster)
	if err != nil || graph.ScopeDigest(scope) != graph.ScopeDigest(claims.Scope) {
		fail(409, "STALE_CONTEXT")
		return
	}
	if r.URL.Path == "/internal/v1/evidence:read" {
		var read struct {
			EvidenceID string `json:"evidenceId"`
		}
		if json.Unmarshal(raw, &read) != nil {
			fail(400, "INVALID_ARGUMENT")
			return
		}
		id, err := uuid.Parse(read.EvidenceID)
		if err != nil {
			fail(400, "INVALID_ARGUMENT")
			return
		}
		tenant := uuid.MustParse(scope.Tenant)
		metadata, err := (evidence.Repository{Pool: pool}).Get(r.Context(), tenant, id, scope)
		if err != nil {
			fail(403, "FORBIDDEN")
			return
		}
		readContext, readCancel := context.WithDeadline(r.Context(), claims.Deadline)
		defer readCancel()
		plain, ref, err := archive.Read(readContext, tenant, id)
		if err != nil {
			fail(503, "SOURCE_CAPABILITY_UNAVAILABLE")
			return
		}
		current, err := (graph.Authorization{Pool: pool}).Effective(r.Context(), claims.Scope.Tenant, claims.Subject, claims.Scope.Cluster)
		if err != nil || graph.ScopeDigest(current) != graph.ScopeDigest(scope) {
			fail(409, "STALE_CONTEXT")
			return
		}
		metadata.ArchiveRef = &ref
		metadata.FactSlice = json.RawMessage(plain)
		w.Header().Set("Content-Type", "application/json")
		writeEvidenceContract(w, metadata, "https://ops.local/schemas/evidence/v2", fail)
		return
	}
	q.Query.Scope = scope
	adapter, ok := adapters[q.SourceID]
	if !ok {
		fail(503, "SOURCE_SCOPE_UNVERIFIED")
		return
	}
	ctx, cancel := context.WithDeadline(r.Context(), claims.Deadline)
	defer cancel()
	requestedID, err := uuid.Parse(q.EvidenceID)
	if err != nil || requestedID == uuid.Nil {
		fail(400, "INVALID_ARGUMENT")
		return
	}
	// A replay revalidates source capability and current authorization, then
	// reads the original immutable archive rather than re-running a mutable query.
	previous, getErr := (evidence.Repository{Pool: pool}).Get(ctx, uuid.MustParse(scope.Tenant), requestedID, scope)
	var result evidence.Result
	if getErr == nil && previous.ReplayState == "archived_verified" {
		if previous.SourceRegistrationID != q.SourceID || previous.ResourceCanonicalID != q.Query.ResourceCanonicalID || previous.EffectiveScope.AuthorizationRevision != scope.AuthorizationRevision {
			fail(409, "STALE_CONTEXT")
			return
		}
		if _, err := adapter.Capabilities(ctx); err != nil {
			fail(503, "SOURCE_SCOPE_UNVERIFIED")
			return
		}
		data, ref, err := archive.Read(ctx, uuid.MustParse(scope.Tenant), requestedID)
		if err != nil {
			fail(503, "SOURCE_CAPABILITY_UNAVAILABLE")
			return
		}
		previous.Data = data
		previous.ArchiveRef = &ref
		result = evidence.Result{SchemaVersion: "evidence-result/v2", Evidence: []evidence.Evidence{previous}, Freshness: "stale", Warnings: []string{"idempotent_archive_replay"}, DegradedSources: []string{}, QueryHash: previous.QueryHash}
	} else {
		result, err = adapter.Query(ctx, q.Query)
	}
	if err != nil {
		if errors.Is(err, deepflow.ErrDisabled) {
			fail(409, "CAPABILITY_DISABLED")
		} else if errors.Is(err, evidence.ErrBudget) {
			fail(429, "BUDGET_EXHAUSTED")
		} else if errors.Is(err, evidence.ErrScopeUnverified) {
			fail(503, "SOURCE_SCOPE_UNVERIFIED")
		} else {
			fail(400, "INVALID_ARGUMENT")
		}
		return
	}
	if adapter.Name() == "deepflow" && getErr != nil {
		if handler, exists := handlers[scope.Tenant+"|"+scope.Cluster]; exists {
			edges, edgeErr := deepflow.NetworkRelations(result)
			if edgeErr == nil {
				edgeErr = handler.Graph.ObserveExternalSource(handler.Graph.OwnerEpoch(), q.SourceID, edges)
			}
			if edgeErr != nil {
				result.Partial = true
				result.Warnings = append(result.Warnings, "network_graph_projection_unavailable")
			}
		}
	}

	for i := range result.Evidence {
		e := &result.Evidence[i]
		e.EvidenceID = q.EvidenceID
		if e.ReplayState == "archived_verified" && e.ArchiveRef != nil {
			continue
		}
		if err := archive.CaptureQuery(ctx, *e, q.Query.Namespace, e.EvaluatedAt.Add(181*24*time.Hour), q.Query); err != nil {
			result.Partial = true
			stored, storedErr := (evidence.Repository{Pool: pool}).Get(ctx, uuid.MustParse(e.TenantID), uuid.MustParse(e.EvidenceID), scope)
			warning := "archive_pending"
			e.ReplayState = "archive_pending"
			if storedErr != nil || stored.ReplayState == "unavailable" {
				e.ReplayState = "unavailable"
				warning = "archive_intent_unavailable"
			}
			result.Warnings = append(result.Warnings, warning)
			result.DegradedSources = append(result.DegradedSources, "archive")
		} else {
			e.ReplayState = "archived_verified"
			_, ref, err := archive.Read(ctx, uuid.MustParse(e.TenantID), uuid.MustParse(e.EvidenceID))
			if err != nil {
				e.ReplayState = "unavailable"
				result.Partial = true
				result.DegradedSources = append(result.DegradedSources, "archive")
				result.Warnings = append(result.Warnings, "archive_verification_failed")
			} else {
				e.ArchiveRef = &ref
			}
		}
	}
	for i := range result.Evidence {
		result.Evidence[i].FactSlice = json.RawMessage(result.Evidence[i].Data)
	}
	current, err := (graph.Authorization{Pool: pool}).Effective(ctx, claims.Scope.Tenant, claims.Subject, claims.Scope.Cluster)
	if err != nil || graph.ScopeDigest(current) != graph.ScopeDigest(scope) {
		fail(409, "STALE_CONTEXT")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if writeEvidenceContract(w, result, "https://ops.local/schemas/evidence-result/v2", fail) {
		metrics.ObserveQuery("evidence", result.Partial || len(result.DegradedSources) > 0, result.Freshness)
	}
}

func writeEvidenceContract(w http.ResponseWriter, value any, schema string, fail func(int, string)) bool {
	raw, err := json.Marshal(value)
	if err != nil || contract.Validate(schema, raw) != nil {
		fail(503, "SOURCE_CAPABILITY_UNAVAILABLE")
		return false
	}
	_, _ = w.Write(raw)
	return true
}

// Source proof has a bounded lease of its own. An ingestion race or outage at
// startup keeps capability disabled and is retried; it never grants on an empty
// canary or silently relies on the declarative registration scope.
func renewSourceProof(ctx context.Context, group *sync.WaitGroup, source string, verify func(context.Context) error, handlers map[string]graph.InternalHandler, binding evidence.Binding, runtime *observability.Runtime) {
	group.Add(1)
	go func() {
		defer group.Done()
		for ctx.Err() == nil {
			bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := verify(bounded)
			cancel()
			delay := 15 * time.Minute
			reason := ""
			result := "ok"
			if err != nil {
				delay = 2 * time.Second
				reason = "source_scope_unverified"
				result = "unavailable"
			}
			for _, cluster := range binding.ScopeMapping.Scopes["cluster"] {
				if h, ok := handlers[binding.Tenant+"|"+cluster]; ok {
					h.Graph.SetSourceDegraded(source+"/proof", reason)
				}
			}
			runtime.Metrics.RecordOperation("evidence", "query", result)
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
		}
	}()
}

func collectionErrorClass(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, evidence.ErrScopeUnverified):
		return "source_scope_unverified"
	case errors.Is(err, graph.ErrStale):
		return "generation_changed"
	case errors.Is(err, evidence.ErrBudget):
		return "budget_exhausted"
	case strings.Contains(err.Error(), "IDEMPOTENCY_CONFLICT"):
		return "idempotency_conflict"
	case strings.Contains(err.Error(), "projection archive identity conflict"):
		return "projection_identity_conflict"
	default:
		return fmt.Sprintf("%T", err)
	}
}
