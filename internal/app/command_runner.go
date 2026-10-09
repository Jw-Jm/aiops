package app

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"ops-platform/internal/auth"
	"os"
	"time"
)

// NewCommandRunnerClient uses the same pinned PKI/CRL lifecycle as other
// workloads. It is used by one-shot Runner Jobs, never by the investigator.
func NewCommandRunnerClient(ctx context.Context, endpoint string) (*http.Client, error) {
	var config struct{ Namespace, OpenBaoAddress, OpenBaoServerName string }
	raw, err := os.ReadFile("/etc/ops/trust/runner.json")
	if err != nil || json.Unmarshal(raw, &config) != nil {
		return nil, errors.New("runner trust configuration unavailable")
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Hostname() != "ops-api."+config.Namespace+".svc.cluster.local" {
		return nil, errors.New("runner API target invalid")
	}
	for k, v := range map[string]string{"OPENBAO_ADDR": config.OpenBaoAddress, "OPENBAO_SERVER_NAME": config.OpenBaoServerName, "OPENBAO_CA_FILE": "/etc/ops/trust/openbao-ca.pem", "OPENBAO_PROJECTED_TOKEN_FILE": "/var/run/ops-identity/token"} {
		if err = os.Setenv(k, v); err != nil {
			return nil, err
		}
	}
	api, err := auth.NewWorkloadIdentity(config.Namespace, "ops-api")
	if err != nil {
		return nil, err
	}
	// A newly admitted Pod may start before its NetworkPolicy rules are installed.
	// Retry only identity bootstrap; a command claim or execution is never retried.
	configTLS, trust, err := runnerIdentityBootstrap(ctx, func(attempt context.Context) (*tls.Config, auth.WorkloadTrust, error) {
		return runtimeWorkloadIdentity(attempt, config.Namespace, "ops-command-runner", "/etc/ops/trust/workload-ca.pem", []auth.WorkloadIdentity{api})
	})
	if err != nil {
		return nil, err
	}
	configTLS.RootCAs = trust.Roots.Clone()
	configTLS.ServerName = u.Hostname()
	configTLS.ClientAuth = tls.NoClientCert
	return &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: &http.Transport{Proxy: nil, TLSClientConfig: configTLS}}, nil
}

func runnerIdentityBootstrap(ctx context.Context, initialize func(context.Context) (*tls.Config, auth.WorkloadTrust, error)) (*tls.Config, auth.WorkloadTrust, error) {
	// Successful PKI renewal remains bound to the Runner's full lifecycle,
	// not to a short bootstrap context that would stop certificate/CRL refresh.
	deadline := time.Now().Add(30 * time.Second)
	for {
		if err := ctx.Err(); err != nil {
			return nil, auth.WorkloadTrust{}, err
		}
		config, trust, err := initialize(ctx)
		if err == nil {
			return config, trust, nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, auth.WorkloadTrust{}, err
		}
		timer := time.NewTimer(min(500*time.Millisecond, remaining))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, auth.WorkloadTrust{}, err
		case <-timer.C:
		}
	}
}
