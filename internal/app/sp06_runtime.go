package app

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"ops-platform/internal/auth"
	"ops-platform/internal/configregistry"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/integrations/openbao"
	"ops-platform/internal/investigation"
	platformmcp "ops-platform/internal/investigation/mcp"
	"ops-platform/internal/investigation/tools"
	"ops-platform/internal/persistence"
	"ops-platform/internal/policy"
	"os"
	"sync"
	"time"
)

type SP06Config struct {
	Namespace            string               `json:"namespace"`
	ListenAddress        string               `json:"listenAddress"`
	InvestigatorEndpoint string               `json:"investigatorEndpoint"`
	CertificateFile      string               `json:"certificateFile"`
	PrivateKeyFile       string               `json:"privateKeyFile"`
	CAFile               string               `json:"caFile"`
	CRLFile              string               `json:"crlFile"`
	PolicyName           string               `json:"policyName"`
	Budget               investigation.Budget `json:"budget"`
	Tenants              []uuid.UUID          `json:"tenants"`
}

func loadSP06() (*SP06Config, error) {
	path := os.Getenv("SP06_RUNTIME_FILE")
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if err != nil || len(b) > 65536 {
		return nil, errors.New("SP06 configuration unavailable")
	}
	var c SP06Config
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil {
		return nil, errors.New("SP06 configuration invalid")
	}
	var extra any
	if d.Decode(&extra) != io.EOF || c.Namespace == "" || c.PolicyName == "" || len(c.Tenants) == 0 || !c.Budget.Valid() {
		return nil, errors.New("SP06 configuration invalid")
	}
	u, err := url.Parse(c.InvestigatorEndpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("SP06 investigator route invalid")
	}
	ip := net.ParseIP(u.Hostname())
	if (ip == nil || (!ip.IsLoopback() && !ip.IsPrivate())) && u.Hostname() != "ops-investigator."+c.Namespace+".svc.cluster.local" {
		return nil, errors.New("SP06 investigator must use an approved private workload address")
	}
	return &c, nil
}
func (c *SP06Config) trust(ctx context.Context, allowed string) (*tls.Config, auth.WorkloadTrust, error) {
	b, err := os.ReadFile(c.CAFile)
	if err != nil {
		return nil, auth.WorkloadTrust{}, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(b) {
		return nil, auth.WorkloadTrust{}, errors.New("SP06 CA invalid")
	}
	cert, err := tls.LoadX509KeyPair(c.CertificateFile, c.PrivateKeyFile)
	if err != nil {
		return nil, auth.WorkloadTrust{}, err
	}
	identity, err := auth.NewWorkloadIdentity(c.Namespace, allowed)
	if err != nil {
		return nil, auth.WorkloadTrust{}, err
	}
	trust := auth.WorkloadTrust{Roots: roots, Allowed: []auth.WorkloadIdentity{identity}, CRLSource: fileCRL{c.CRLFile}}
	config, err := auth.WorkloadMTLSServerConfig(ctx, trust)
	if err != nil {
		return nil, trust, err
	}
	config.Certificates = []tls.Certificate{cert}
	return config, trust, nil
}
func sp06Signer(ctx context.Context, serviceAccount string) (investigation.ContextSigner, error) {
	ca, err := os.ReadFile(os.Getenv("OPENBAO_CA_FILE"))
	if err != nil {
		return investigation.ContextSigner{}, err
	}
	bao, err := openbao.NewClient(openbao.ClientConfig{Address: os.Getenv("OPENBAO_ADDR"), CACertBundle: ca, ServiceDomain: os.Getenv("OPENBAO_SERVICE_DOMAIN")})
	if err != nil {
		return investigation.ContextSigner{}, err
	}
	if tokenFile := os.Getenv("OPENBAO_INVOCATION_TOKEN_FILE"); tokenFile != "" {
		token, err := os.ReadFile(tokenFile)
		if err != nil || len(token) > 65536 {
			return investigation.ContextSigner{}, errors.New("SP06 mounted Transit credential unavailable")
		}
		bao.SetToken(string(bytes.TrimSpace(token)))
	} else {
		if _, err = bao.LoginProjectedServiceAccount(ctx, os.Getenv("OPENBAO_PROJECTED_TOKEN_FILE"), serviceAccount+"-workload"); err != nil {
			return investigation.ContextSigner{}, err
		}
	}
	if _, _, err = bao.SigningKeys(ctx, "investigation-signing"); err != nil {
		return investigation.ContextSigner{}, err
	}
	return investigation.ContextSigner{Transit: bao, Key: "investigation-signing"}, nil
}
func StartSP06API(ctx context.Context, pool *pgxpool.Pool, sp04 *httpapi.SP04Handlers, trust configregistry.SignatureVerifier) (func(), error) {
	c, err := loadSP06()
	if err != nil || c == nil {
		return func() {}, err
	}
	signer, err := sp06Signer(ctx, "ops-api")
	if err != nil {
		return nil, err
	}
	compiler, err := policy.NewBundleCompiler(trust)
	if err != nil {
		return nil, err
	}
	registry, err := configregistry.NewService(pool, trust, compiler)
	if err != nil {
		return nil, err
	}
	evaluator, err := policy.NewEvaluator(registry, compiler, c.PolicyName)
	if err != nil {
		return nil, err
	}
	defs, err := tools.Catalog()
	if err != nil {
		return nil, err
	}
	names := tools.Names(defs)
	resolveScope := func(ctx context.Context, tenant uuid.UUID, cluster string) (configregistry.Scope, error) {
		var id uuid.UUID
		err := persistence.WithTenantTx(ctx, pool, tenant, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT cluster_id FROM platform.cluster_registrations WHERE tenant_id=$1 AND cluster_uid=$2 AND status='active'`, tenant, cluster).Scan(&id)
		})
		return configregistry.Scope{Type: configregistry.ScopeCluster, ClusterID: id}, err
	}
	authorize := func(ctx context.Context, j investigation.Job, name string, args json.RawMessage) error {
		scope, err := resolveScope(ctx, j.TenantID, j.Scope.Cluster)
		if err != nil {
			return investigation.ErrDenied
		}
		decision, err := evaluator.Evaluate(ctx, policy.PolicyInput{Request: auth.RequestContext{TenantID: j.TenantID, Subject: j.Subject}, PrincipalType: policy.PrincipalAgent, RequestType: policy.RequestTool, Scope: scope, Invocation: &policy.InvocationContext{Verified: true, TenantID: j.TenantID, Scope: scope, AllowedTools: names}, ToolName: name, AllowedTools: names, ToolReadOnly: true, Risk: policy.RiskLow})
		if err != nil || !decision.Allow || decision.PolicyVersion != j.PolicyVersion {
			return investigation.ErrDenied
		}
		return nil
	}
	tlsConfig, workloadTrust, err := c.trust(ctx, "ops-investigator")
	if err != nil {
		return nil, err
	}
	handlers := &httpapi.InvestigationHandlers{Repository: investigation.Repository{Pool: pool}, Signer: signer, Trust: workloadTrust, CursorKey: sp04.SigningKey, Authorize: authorize, Budget: c.Budget, PolicyVersion: func(ctx context.Context, a auth.RequestContext, cluster string) (string, error) {
		scope, err := resolveScope(ctx, a.TenantID, cluster)
		if err != nil {
			return "", err
		}
		decision, err := evaluator.Evaluate(ctx, policy.PolicyInput{Request: a, PrincipalType: policy.PrincipalOperator, RequestType: policy.RequestTool, Scope: scope, ToolName: "get_incident_context", AllowedTools: names, ToolReadOnly: true, Risk: policy.RiskLow})
		if err != nil || !decision.Allow {
			return "", investigation.ErrDenied
		}
		return decision.PolicyVersion, nil
	}}
	handlers.Repository.Trust = trust
	if sp04.SP05 != nil {
		handlers.Repository.CurrentRCA = sp04.SP05.CurrentInvestigationRCA
	}
	handlers.PolicyBudget = func(ctx context.Context, a auth.RequestContext, cluster, version string) (investigation.Budget, error) {
		scope, err := resolveScope(ctx, a.TenantID, cluster)
		if err != nil {
			return investigation.Budget{}, err
		}
		active, err := registry.ResolveActive(ctx, a.TenantID, configregistry.KindPolicy, c.PolicyName, scope, time.Now())
		if err != nil || active.VersionID.String() != version {
			return investigation.Budget{}, investigation.ErrDenied
		}
		var content struct {
			Budget *investigation.Budget `json:"investigationBudget"`
		}
		if json.Unmarshal(active.Content, &content) != nil {
			return investigation.Budget{}, investigation.ErrDenied
		}
		cap := investigation.DefaultBudget()
		if content.Budget != nil {
			cap = *content.Budget
		}
		return investigation.LowerBudget(c.Budget, cap)
	}
	gateway, err := (platformmcp.Gateway{Repository: handlers.Repository, Signer: signer, Trust: workloadTrust, API: httpapi.InvestigationTools{SP04: sp04, SP05: sp04.SP05}, Authorize: authorize}).Handler()
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("/mcp", gateway)
	mux.Handle("/internal/v1/investigations/", handlers.InternalHandler())
	listener, err := net.Listen("tcp", c.ListenAddress)
	if err != nil {
		return nil, err
	}
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: time.Minute}
	sp04.Investigation = handlers
	go func() { _ = server.Serve(tls.NewListener(listener, tlsConfig)) }()
	return func() { _ = server.Close() }, nil
}
func StartSP06Worker(ctx context.Context, pool *pgxpool.Pool) (func(), error) {
	c, err := loadSP06()
	if err != nil || c == nil {
		return func() {}, err
	}
	signer, err := sp06Signer(ctx, "ops-worker")
	if err != nil {
		return nil, err
	}
	tlsConfig, trust, err := c.trust(ctx, "ops-investigator")
	if err != nil {
		return nil, err
	}
	tlsConfig.ClientAuth = tls.NoClientCert
	tlsConfig.RootCAs = trust.Roots
	tlsConfig.ServerName = trust.Allowed[0].DNSName
	tlsConfig.VerifyConnection = func(s tls.ConnectionState) error {
		if len(s.PeerCertificates) == 0 {
			return investigation.ErrDenied
		}
		_, err := auth.VerifyWorkload(auth.WithWorkloadTrust(ctx, trust), s.PeerCertificates[0])
		return err
	}
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: tlsConfig}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defs, err := tools.Catalog()
	if err != nil {
		return nil, err
	}
	dispatcher := investigation.Dispatcher{Repository: investigation.Repository{Pool: pool}, Signer: signer, Workload: trust.Allowed[0].URI, Tools: tools.Names(defs), Remote: func(ctx context.Context, q investigation.DispatchRequest) ([]byte, error) {
		b, _ := json.Marshal(q)
		req, err := http.NewRequestWithContext(ctx, "POST", c.InvestigatorEndpoint+"/internal/v1/investigate", bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return nil, errors.New("INVESTIGATOR_UNAVAILABLE")
		}
		var out struct {
			Data json.RawMessage `json:"data"`
		}
		if json.NewDecoder(io.LimitReader(resp.Body, 65537)).Decode(&out) != nil || len(out.Data) > 65536 {
			return nil, investigation.ErrInvalid
		}
		if path := os.Getenv("SP06_TEST_PROPOSAL_FILE"); path != "" {
			cleaned, err := tools.Sanitize(tools.Result{Data: out.Data}, 65536)
			if err != nil {
				return nil, err
			}
			b, _ := json.MarshalIndent(cleaned.Data, "", "  ")
			if os.WriteFile(path, b, 0600) != nil {
				return nil, errors.New("diagnostic evidence unavailable")
			}
		}
		return out.Data, nil
	}}
	workerCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	slots := make(chan struct{}, 10)
	wg.Go(func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-workerCtx.Done():
				return
			case <-ticker.C:
			}
			for _, tenant := range c.Tenants {
				_ = dispatcher.Repository.ExpirePass(workerCtx, tenant)
				select {
				case slots <- struct{}{}:
					wg.Go(func() {
						defer func() { <-slots }()
						if err := dispatcher.RunOne(workerCtx, tenant, "sp06-dispatcher"); err != nil && !errors.Is(err, investigation.ErrNoJob) && !errors.Is(err, context.Canceled) {
							slog.Error("investigator dispatch failed", "reason", err.Error())
						}
					})
				default:
				}
			}
		}
	})
	return func() { cancel(); wg.Wait(); client.CloseIdleConnections() }, nil
}
