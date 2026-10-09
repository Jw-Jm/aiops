package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"net"
	"net/http"
	"ops-platform/internal/action"
	"ops-platform/internal/auth"
	"ops-platform/internal/configregistry"
	platformcrypto "ops-platform/internal/crypto"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/integrations/openbao"
	"ops-platform/internal/persistence"
	"ops-platform/internal/policy"
	"os"
	"time"
)

type SP07Config struct {
	SchemaVersion       string      `json:"schemaVersion"`
	Namespace           string      `json:"namespace"`
	ListenAddress       string      `json:"listenAddress"`
	APIEndpoint         string      `json:"apiEndpoint"`
	WorkloadCAFile      string      `json:"workloadCAFile"`
	PolicyName          string      `json:"policyName"`
	KubernetesEndpoint  string      `json:"kubernetesEndpoint"`
	KubernetesCAFile    string      `json:"kubernetesCAFile"`
	KubernetesTokenFile string      `json:"kubernetesTokenFile"`
	TrustConfigMap      string      `json:"trustConfigMap"`
	Tenants             []uuid.UUID `json:"tenants"`
	// These are deployment admission inputs, not business records. Profile
	// publication still uses signed, audited, tenant-admin product APIs.
	AdmittedProfiles []action.Profile        `json:"admittedProfiles"`
	Hosts            []action.Host           `json:"hosts"`
	HostReports      []action.HostOnboarding `json:"hostReports"`
}

func loadSP07() (*SP07Config, error) {
	path := os.Getenv("SP07_RUNTIME_FILE")
	if path == "" {
		return nil, nil
	}
	b, err := os.ReadFile(path)
	if err != nil || len(b) > 256<<10 {
		return nil, errors.New("SP07 configuration unavailable")
	}
	var c SP07Config
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	var extra any
	if d.Decode(&c) != nil || d.Decode(&extra) != io.EOF || c.SchemaVersion != "sp07-runtime/v1" || c.Namespace == "" || len(c.Tenants) == 0 || c.PolicyName == "" || c.ListenAddress == "" || c.WorkloadCAFile == "" || len(c.AdmittedProfiles) == 0 {
		return nil, errors.New("SP07 configuration invalid")
	}
	for _, p := range c.AdmittedProfiles {
		if p.Validate() != nil {
			return nil, action.ErrInvalid
		}
	}
	return &c, nil
}
func sp07Service(ctx context.Context, pool persistence.TxBeginner, c *SP07Config, trust configregistry.Ed25519TrustStore, sa string) (action.Service, error) {
	var out action.Service
	ca, err := os.ReadFile(os.Getenv("OPENBAO_CA_FILE"))
	if err != nil {
		return out, err
	}
	bao, err := openbao.NewClient(openbao.ClientConfig{Address: os.Getenv("OPENBAO_ADDR"), ServerName: os.Getenv("OPENBAO_SERVER_NAME"), CACertBundle: ca, ServiceDomain: os.Getenv("OPENBAO_SERVICE_DOMAIN")})
	if err != nil {
		return out, err
	}
	transit, err := openbao.NewProjectedTransit(bao, os.Getenv("OPENBAO_PROJECTED_TOKEN_FILE"), sa)
	if err != nil {
		return out, err
	}
	protector, err := platformcrypto.NewTransitProtector(transit, "evidence-archive")
	if err != nil {
		return out, err
	}
	compiler, err := policy.NewBundleCompiler(trust)
	if err != nil {
		return out, err
	}
	registry, err := configregistry.NewService(pool, trust, compiler)
	if err != nil {
		return out, err
	}
	evaluator, err := policy.NewEvaluator(registry, compiler, c.PolicyName)
	if err != nil {
		return out, err
	}
	kube, err := action.NewKubernetesREST(c.KubernetesEndpoint, c.KubernetesCAFile, c.KubernetesTokenFile)
	if err != nil {
		return out, err
	}
	kubeCA, err := os.ReadFile(c.KubernetesCAFile)
	if err != nil {
		return out, err
	}
	out = action.Service{AdmitHost: func(ctx context.Context, h action.HostOnboarding) error {
		for _, admitted := range c.HostReports {
			if bytes.Equal(action.Canonical(admitted), action.Canonical(h)) {
				return nil
			}
		}
		return action.ErrDenied
	}, Pool: pool, Trust: trust, Policy: evaluator, Protector: protector, Credentials: action.ExecutionCredentials{Kubernetes: kube, KubernetesCA: kubeCA, Hosts: c.Hosts, SSHCA: transit}, AdmitProfile: func(ctx context.Context, p action.Profile) error {
		for _, allowed := range c.AdmittedProfiles {
			if bytes.Equal(action.Canonical(allowed), action.Canonical(p)) {
				return nil
			}
		}
		return action.ErrDenied
	}}
	return out, nil
}
func StartSP07API(ctx context.Context, pool *pgxpool.Pool, sp04 *httpapi.SP04Handlers, trust configregistry.Ed25519TrustStore) (func(), error) {
	c, err := loadSP07()
	if err != nil {
		return nil, err
	}
	if c == nil {
		return func() {}, nil
	}
	s, err := sp07Service(ctx, pool, c, trust, "ops-api")
	if err != nil {
		return nil, err
	}
	runner, err := auth.NewWorkloadIdentity(c.Namespace, "ops-command-runner")
	if err != nil {
		return nil, err
	}
	tlsConfig, workloadTrust, err := runtimeWorkloadIdentity(ctx, c.Namespace, "ops-api", c.WorkloadCAFile, []auth.WorkloadIdentity{runner})
	if err != nil {
		return nil, err
	}
	handler := httpapi.RunnerHandlers{Service: s, Trust: workloadTrust}
	listener, err := net.Listen("tcp", c.ListenAddress)
	if err != nil {
		return nil, err
	}
	server := &http.Server{TLSConfig: tlsConfig, Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: time.Minute}
	sp04.Action = &httpapi.ActionHandlers{Service: s}
	go func() { _ = server.ServeTLS(listener, "", "") }()
	return func() { _ = server.Close() }, nil
}
