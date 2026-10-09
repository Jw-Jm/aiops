package openbao

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type State string

const (
	StateUninitialized      State = "uninitialized"
	StateSealed             State = "sealed"
	StateReady              State = "ready"
	StateConfigurationDrift State = "configuration_drift"
)

var (
	ErrAlreadyInitialized = errors.New("OPENBAO_ALREADY_INITIALIZED")
	ErrSealed             = errors.New("OPENBAO_SEALED")
	ErrConfigurationDrift = errors.New("OPENBAO_CONFIGURATION_DRIFT")
	ErrNoDefaultIssuer    = errors.New("OPENBAO_PKI_DEFAULT_ISSUER_MISSING")
)

type ClientConfig struct {
	Address         string
	ServerName      string
	CACertBundle    []byte
	Token           string
	RepositoryRoot  string
	BundleDirectory string
	ServiceDomain   string
	ExpectedVersion string
}

type Status struct {
	State       State  `json:"state" yaml:"state"`
	Initialized bool   `json:"initialized" yaml:"initialized"`
	Sealed      bool   `json:"sealed" yaml:"sealed"`
	Threshold   int    `json:"threshold" yaml:"threshold"`
	Shares      int    `json:"shares" yaml:"shares"`
	Progress    int    `json:"progress" yaml:"progress"`
	Version     string `json:"version" yaml:"version"`
	Detail      string `json:"detail,omitempty" yaml:"detail,omitempty"`
}

type Client struct {
	address         string
	serverName      string
	repositoryRoot  string
	bundleDirectory string
	serviceDomain   string
	expectedVersion string
	token           string
	http            *http.Client
}

type sealResponse struct {
	Initialized bool   `json:"initialized"`
	Sealed      bool   `json:"sealed"`
	Threshold   int    `json:"t"`
	Shares      int    `json:"n"`
	Progress    int    `json:"progress"`
	Version     string `json:"version"`
}

type apiError struct {
	statusCode int
	path       string
}

func (e *apiError) Error() string {
	if e.statusCode >= http.StatusInternalServerError {
		return fmt.Sprintf("OPENBAO_UNAVAILABLE: request to %s returned HTTP %d", e.path, e.statusCode)
	}
	return fmt.Sprintf("OpenBao request to %s returned HTTP %d", e.path, e.statusCode)
}

func NewClient(config ClientConfig) (*Client, error) {
	parsed, err := url.Parse(config.Address)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return nil, errors.New("OpenBao address must be an HTTPS URL")
	}
	if parsed.User != nil {
		return nil, errors.New("OpenBao address must not contain URL credentials")
	}
	if config.ServerName == "" {
		config.ServerName = parsed.Hostname()
	}
	if len(config.CACertBundle) == 0 {
		return nil, errors.New("OPENBAO_CA_UNTRUSTED: an explicit bootstrap CA bundle is required")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(config.CACertBundle) {
		return nil, errors.New("OPENBAO_CA_UNTRUSTED: bootstrap CA bundle contains no valid certificate")
	}
	if config.ServiceDomain == "" {
		config.ServiceDomain = "ops-system.svc.cluster.local"
	}
	transport := &http.Transport{
		Proxy: nil,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			RootCAs:    roots,
			ServerName: config.ServerName,
		},
	}
	return &Client{
		address:         strings.TrimRight(config.Address, "/"),
		serverName:      config.ServerName,
		repositoryRoot:  config.RepositoryRoot,
		bundleDirectory: config.BundleDirectory,
		serviceDomain:   config.ServiceDomain,
		expectedVersion: config.ExpectedVersion,
		token:           config.Token,
		http: &http.Client{
			Transport: transport,
			Timeout:   15 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return errors.New("OpenBao HTTP redirects are not allowed")
			},
		},
	}, nil
}

func (c *Client) SetToken(token string) {
	c.token = token
}

func (c *Client) Status(ctx context.Context) (Status, error) {
	seal, err := c.readSealStatus(ctx)
	if err != nil {
		return Status{}, err
	}
	status := seal.status()
	if status.State == StateReady {
		if c.token == "" {
			status.Detail = "configuration not verified; pass --recovery-file to check drift"
		} else {
			if err := c.verifyConfiguration(ctx); err != nil {
				if errors.Is(err, ErrConfigurationDrift) {
					status.State = StateConfigurationDrift
					status.Detail = ErrConfigurationDrift.Error()
					return status, nil
				}
				return Status{}, err
			}
		}
	}
	if c.expectedVersion != "" && status.Version != "" && status.Version != c.expectedVersion {
		status.State = StateConfigurationDrift
		status.Detail = "OPENBAO_VERSION_MISMATCH"
	}
	return status, nil
}

func (c *Client) RequireReady(ctx context.Context) error {
	seal, err := c.readSealStatus(ctx)
	if err != nil {
		return err
	}
	if !seal.Initialized || seal.Sealed {
		return ErrSealed
	}
	return nil
}

func (c *Client) readSealStatus(ctx context.Context) (sealResponse, error) {
	var response sealResponse
	if err := c.request(ctx, http.MethodGet, "/v1/sys/seal-status", nil, &response); err != nil {
		return sealResponse{}, err
	}
	return response, nil
}

func (s sealResponse) status() Status {
	state := StateReady
	if !s.Initialized {
		state = StateUninitialized
	} else if s.Sealed {
		state = StateSealed
	}
	return Status{
		State:       state,
		Initialized: s.Initialized,
		Sealed:      s.Sealed,
		Threshold:   s.Threshold,
		Shares:      s.Shares,
		Progress:    s.Progress,
		Version:     s.Version,
	}
}

func (c *Client) request(ctx context.Context, method, path string, input any, output any) error {
	return c.requestWithToken(ctx, method, path, input, output, c.token)
}

func (c *Client) requestWithToken(ctx context.Context, method, path string, input any, output any, token string) error {
	return c.requestWithTokenLimit(ctx, method, path, input, output, token, 1<<20)
}

func (c *Client) requestWithTokenLimit(ctx context.Context, method, path string, input any, output any, token string, responseLimit int64) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode OpenBao request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.address+path, body)
	if err != nil {
		return fmt.Errorf("create OpenBao request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("X-Vault-Token", token)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("OPENBAO_UNAVAILABLE: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		if method == http.MethodGet && path == "/v1/pki/issuer/default/json" && response.StatusCode == http.StatusInternalServerError {
			var errorResponse struct {
				Errors []string `json:"errors"`
			}
			if json.Unmarshal(body, &errorResponse) == nil {
				for _, message := range errorResponse.Errors {
					if message == "no default issuer currently configured" {
						return ErrNoDefaultIssuer
					}
				}
			}
		}
		return &apiError{statusCode: response.StatusCode, path: path}
	}
	if output == nil {
		_, err := io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
		return err
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, responseLimit+1))
	if err != nil || int64(len(raw)) > responseLimit {
		return fmt.Errorf("OpenBao response exceeds bounded read from %s", path)
	}
	if err := json.Unmarshal(raw, output); err != nil {
		return fmt.Errorf("decode OpenBao response from %s: %w", path, err)
	}
	return nil
}

func (e *apiError) isNotFound() bool { return e.statusCode == http.StatusNotFound }
