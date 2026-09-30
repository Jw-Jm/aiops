package openbao

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	workloadCertificateRolePrefix = "platform-workload-"
	workloadCertificateTTL        = time.Hour
	workloadRefreshFraction       = 2.0 / 3.0
	workloadCRLRefreshInterval    = time.Minute
	workloadCRLMaxAge             = 5 * time.Minute
)

type kubernetesLoginResponse struct {
	Auth struct {
		ClientToken   string `json:"client_token"`
		LeaseDuration int64  `json:"lease_duration"`
	} `json:"auth"`
}

type pkiSignResponse struct {
	Data struct {
		Certificate string   `json:"certificate"`
		IssuingCA   string   `json:"issuing_ca"`
		CAChain     []string `json:"ca_chain"`
	} `json:"data"`
}

type workloadCertificateState struct {
	certificate *tls.Certificate
	trust       WorkloadTrustMaterial
}

type WorkloadTrustMaterial struct {
	Roots          *x509.CertPool
	Intermediates  *x509.CertPool
	CRLIssuers     []*x509.Certificate
	RevocationList *x509.RevocationList
	CRLFetchedAt   time.Time
}

// WorkloadRoleName binds a Kubernetes ServiceAccount to its own exact-SAN PKI role.
func WorkloadRoleName(serviceAccount string) (string, error) {
	if !validKubernetesName(serviceAccount) {
		return "", errors.New("invalid workload service account")
	}
	return workloadCertificateRolePrefix + serviceAccount, nil
}

func WorkloadAuthRoleName(serviceAccount string) (string, error) {
	if !validKubernetesName(serviceAccount) {
		return "", errors.New("invalid workload service account")
	}
	return serviceAccount + "-workload", nil
}

// KubernetesLogin exchanges the projected ServiceAccount JWT for an ephemeral
// OpenBao token. The JWT is sent only in the login request body; it is never
// placed in an HTTP header or returned in errors.
func (c *Client) KubernetesLogin(ctx context.Context, projectedToken, role string) (string, time.Duration, error) {
	if strings.TrimSpace(projectedToken) == "" || len(projectedToken) > 1<<20 || !validKubernetesName(role) {
		return "", 0, errors.New("invalid OpenBao Kubernetes login input")
	}
	var response kubernetesLoginResponse
	input := map[string]string{"role": role, "jwt": strings.TrimSpace(projectedToken)}
	if err := c.requestWithToken(ctx, http.MethodPost, "/v1/auth/kubernetes/login", input, &response, ""); err != nil {
		return "", 0, errors.New("OPENBAO_KUBERNETES_LOGIN_FAILED")
	}
	if response.Auth.ClientToken == "" || response.Auth.LeaseDuration <= 0 {
		return "", 0, errors.New("OPENBAO_KUBERNETES_LOGIN_RESPONSE_INVALID")
	}
	return response.Auth.ClientToken, time.Duration(response.Auth.LeaseDuration) * time.Second, nil
}

// SignWorkloadCSR is used by isolated integration checks and bootstrap tooling
// that already holds an authorized OpenBao token. Runtime workloads use
// CertReloader so their token comes from Kubernetes auth instead.
func (c *Client) SignWorkloadCSR(ctx context.Context, namespace, serviceAccount string, csrPEM []byte, privateKey *ecdsa.PrivateKey) (*tls.Certificate, error) {
	if c.token == "" || privateKey == nil {
		return nil, errors.New("an authorized OpenBao token and in-process private key are required")
	}
	if !validKubernetesName(namespace) || !validKubernetesName(serviceAccount) || len(csrPEM) == 0 || len(csrPEM) > 64<<10 {
		return nil, errors.New("invalid workload CSR input")
	}
	block, _ := pem.Decode(csrPEM)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return nil, errors.New("invalid workload CSR PEM")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil || csr.CheckSignature() != nil {
		return nil, errors.New("invalid workload CSR signature")
	}
	role, err := WorkloadRoleName(serviceAccount)
	if err != nil {
		return nil, err
	}
	uri := "spiffe://ops.local/ns/" + namespace + "/sa/" + serviceAccount
	dns := serviceAccount + "." + namespace + ".svc.cluster.local"
	csrPublicKey, keyTypeOK := csr.PublicKey.(*ecdsa.PublicKey)
	if len(csr.URIs) != 1 || csr.URIs[0].String() != uri || len(csr.DNSNames) != 1 || csr.DNSNames[0] != dns || !keyTypeOK || !csrPublicKey.Equal(&privateKey.PublicKey) {
		return nil, errors.New("workload CSR identity or key does not match the requested ServiceAccount")
	}
	response, err := c.signWorkloadCSR(ctx, c.token, role, csrPEM)
	if err != nil {
		return nil, err
	}
	certificate, trust, err := parseSignedWorkloadCertificate(response, privateKey, uri, dns, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	crlPEM, err := c.readPKICRL(ctx)
	if err != nil {
		return nil, errors.New("OPENBAO_PKI_CRL_UNAVAILABLE")
	}
	trust.RevocationList, err = parseAndVerifyWorkloadCRL(crlPEM, trust, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	trust.CRLFetchedAt = time.Now().UTC()
	for _, revoked := range trust.RevocationList.RevokedCertificateEntries {
		if revoked.SerialNumber != nil && certificate.Leaf.SerialNumber.Cmp(revoked.SerialNumber) == 0 {
			return nil, errors.New("OPENBAO_PKI_ISSUED_REVOKED_CERTIFICATE")
		}
	}
	return certificate, nil
}

// ReadWorkloadCRL returns the OpenBao PKI CRL as a parsed list. Callers must
// validate its signature against their pinned workload CA before trusting it.
func (c *Client) ReadWorkloadCRL(ctx context.Context) (*x509.RevocationList, error) {
	encoded, err := c.readPKICRL(ctx)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(encoded)
	if block == nil || block.Type != "X509 CRL" {
		return nil, errors.New("OPENBAO_PKI_CRL_INVALID")
	}
	crl, err := x509.ParseRevocationList(block.Bytes)
	if err != nil {
		return nil, errors.New("OPENBAO_PKI_CRL_INVALID")
	}
	return crl, nil
}

// RevokeWorkloadCertificate revokes a serial through OpenBao PKI so subsequent
// mTLS validations fail after the new CRL has propagated.
func (c *Client) RevokeWorkloadCertificate(ctx context.Context, serial *big.Int) error {
	if c.token == "" || serial == nil || serial.Sign() <= 0 || len(serial.Bytes()) > 20 {
		return errors.New("an authorized OpenBao token and valid certificate serial are required")
	}
	encoded := serial.Bytes()
	parts := make([]string, len(encoded))
	for i, octet := range encoded {
		parts[i] = fmt.Sprintf("%02x", octet)
	}
	return c.request(ctx, http.MethodPost, "/v1/pki/revoke", map[string]string{"serial_number": strings.Join(parts, ":")}, nil)
}

// CertReloader keeps the leaf private key only in process memory. It exchanges
// the projected Kubernetes token for a short-lived OpenBao token and submits a
// CSR; only the public CSR crosses the OpenBao boundary.
type CertReloader struct {
	client         *Client
	namespace      string
	serviceAccount string
	spiffeURI      string
	dnsName        string
	loginRole      string
	pkiRole        string
	current        atomic.Pointer[workloadCertificateState]
	mu             sync.Mutex
	now            func() time.Time
}

func NewCertReloader(client *Client, namespace, serviceAccount string) (*CertReloader, error) {
	if client == nil || !validKubernetesName(namespace) || !validKubernetesName(serviceAccount) {
		return nil, errors.New("OpenBao and valid workload identity are required")
	}
	role, err := WorkloadRoleName(serviceAccount)
	if err != nil {
		return nil, err
	}
	loginRole, err := WorkloadAuthRoleName(serviceAccount)
	if err != nil {
		return nil, err
	}
	return &CertReloader{
		client: client, namespace: namespace, serviceAccount: serviceAccount,
		spiffeURI: "spiffe://ops.local/ns/" + namespace + "/sa/" + serviceAccount,
		dnsName:   serviceAccount + "." + namespace + ".svc.cluster.local",
		loginRole: loginRole,
		pkiRole:   role,
		now:       func() time.Time { return time.Now().UTC() },
	}, nil
}

// Start obtains an initial certificate synchronously, then rotates at two-thirds
// of each certificate lifetime. Failed renewals retain the current certificate
// while valid and retry; the TLS callback fails closed after its expiry.
func (r *CertReloader) Start(ctx context.Context, projectedServiceAccountTokenPath string) error {
	if err := r.refresh(ctx, projectedServiceAccountTokenPath); err != nil {
		return err
	}
	go r.rotationLoop(ctx, projectedServiceAccountTokenPath)
	return nil
}

func (r *CertReloader) rotationLoop(ctx context.Context, tokenPath string) {
	ticker := time.NewTicker(workloadCRLRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		state := r.current.Load()
		if state == nil || state.certificate == nil || state.certificate.Leaf == nil {
			return
		}
		deadline := state.certificate.Leaf.NotBefore.Add(time.Duration(float64(state.certificate.Leaf.NotAfter.Sub(state.certificate.Leaf.NotBefore)) * workloadRefreshFraction))
		if !r.now().Before(deadline) {
			_ = r.refresh(ctx, tokenPath)
		} else {
			_ = r.refreshCRL(ctx)
		}
	}
}

func (r *CertReloader) refresh(ctx context.Context, tokenPath string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	projectedToken, err := readProjectedServiceAccountToken(tokenPath)
	if err != nil {
		return err
	}
	baoToken, _, err := r.client.KubernetesLogin(ctx, projectedToken, r.loginRole)
	if err != nil {
		return err
	}
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return errors.New("WORKLOAD_KEY_GENERATION_FAILED")
	}
	now := r.now().UTC()
	request := &x509.CertificateRequest{
		DNSNames: []string{r.dnsName},
		URIs:     []*url.URL{{Scheme: "spiffe", Host: "ops.local", Path: "/ns/" + r.namespace + "/sa/" + r.serviceAccount}},
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, request, privateKey)
	if err != nil {
		return errors.New("WORKLOAD_CSR_GENERATION_FAILED")
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})
	response, err := r.client.signWorkloadCSR(ctx, baoToken, r.pkiRole, csrPEM)
	if err != nil {
		return err
	}
	certificate, trust, err := parseSignedWorkloadCertificate(response, privateKey, r.spiffeURI, r.dnsName, now)
	if err != nil {
		return err
	}
	crlPEM, err := r.client.readPKICRL(ctx)
	if err != nil {
		return errors.New("OPENBAO_PKI_CRL_UNAVAILABLE")
	}
	trust.RevocationList, err = parseAndVerifyWorkloadCRL(crlPEM, trust, now)
	if err != nil {
		return err
	}
	trust.CRLFetchedAt = now
	for _, revoked := range trust.RevocationList.RevokedCertificateEntries {
		if revoked.SerialNumber != nil && certificate.Leaf.SerialNumber.Cmp(revoked.SerialNumber) == 0 {
			return errors.New("OPENBAO_PKI_ISSUED_REVOKED_CERTIFICATE")
		}
	}
	// Publish certificate and matching CA trust only after full chain/SAN validation.
	r.current.Store(&workloadCertificateState{certificate: certificate, trust: trust})
	return nil
}

func (c *Client) signWorkloadCSR(ctx context.Context, token, role string, csrPEM []byte) (pkiSignResponse, error) {
	if !validKubernetesName(role) || strings.TrimSpace(token) == "" || len(csrPEM) == 0 || len(csrPEM) > 64<<10 {
		return pkiSignResponse{}, errors.New("invalid OpenBao workload signing input")
	}
	var response pkiSignResponse
	input := map[string]any{"csr": string(csrPEM), "ttl": workloadCertificateTTL.String(), "use_csr_values": true}
	if err := c.requestWithToken(ctx, http.MethodPost, "/v1/pki/sign/"+role, input, &response, token); err != nil {
		return pkiSignResponse{}, errors.New("OPENBAO_PKI_SIGN_FAILED")
	}
	return response, nil
}

func readProjectedServiceAccountToken(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", errors.New("projected ServiceAccount token path is required")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", errors.New("PROJECTED_SERVICE_ACCOUNT_TOKEN_UNAVAILABLE")
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > 1<<20 || info.Mode().Perm()&0o022 != 0 {
		return "", errors.New("PROJECTED_SERVICE_ACCOUNT_TOKEN_INVALID")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("PROJECTED_SERVICE_ACCOUNT_TOKEN_UNAVAILABLE")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) || !opened.Mode().IsRegular() {
		return "", errors.New("PROJECTED_SERVICE_ACCOUNT_TOKEN_INVALID")
	}
	value, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(value) > 1<<20 {
		return "", errors.New("PROJECTED_SERVICE_ACCOUNT_TOKEN_INVALID")
	}
	token := strings.TrimSpace(string(value))
	if token == "" {
		return "", errors.New("PROJECTED_SERVICE_ACCOUNT_TOKEN_INVALID")
	}
	return token, nil
}

func parseSignedWorkloadCertificate(response pkiSignResponse, privateKey *ecdsa.PrivateKey, expectedURI, expectedDNS string, now time.Time) (*tls.Certificate, WorkloadTrustMaterial, error) {
	leafBlock, _ := pem.Decode([]byte(response.Data.Certificate))
	if leafBlock == nil || leafBlock.Type != "CERTIFICATE" {
		return nil, WorkloadTrustMaterial{}, errors.New("OPENBAO_PKI_CERTIFICATE_INVALID")
	}
	leaf, err := x509.ParseCertificate(leafBlock.Bytes)
	if err != nil {
		return nil, WorkloadTrustMaterial{}, errors.New("OPENBAO_PKI_CERTIFICATE_INVALID")
	}
	if len(leaf.URIs) != 1 || leaf.URIs[0].String() != expectedURI || len(leaf.DNSNames) != 1 || leaf.DNSNames[0] != expectedDNS ||
		leaf.NotBefore.After(now.Add(30*time.Second)) || !leaf.NotAfter.After(now) || leaf.NotAfter.Sub(leaf.NotBefore) > workloadCertificateTTL+time.Minute ||
		leaf.KeyUsage&x509.KeyUsageDigitalSignature == 0 || !hasExtKeyUsage(leaf, x509.ExtKeyUsageClientAuth) || !hasExtKeyUsage(leaf, x509.ExtKeyUsageServerAuth) {
		return nil, WorkloadTrustMaterial{}, errors.New("OPENBAO_PKI_CERTIFICATE_IDENTITY_OR_TTL_INVALID")
	}
	chainPEM := [][]byte{pem.EncodeToMemory(leafBlock)}
	caPEMs := response.Data.CAChain
	if len(caPEMs) == 0 && response.Data.IssuingCA != "" {
		caPEMs = []string{response.Data.IssuingCA}
	}
	roots := x509.NewCertPool()
	intermediates := x509.NewCertPool()
	chainCertificates := make([]*x509.Certificate, 0, len(caPEMs))
	for _, encoded := range caPEMs {
		for len(strings.TrimSpace(encoded)) > 0 {
			block, rest := pem.Decode([]byte(encoded))
			if block == nil || block.Type != "CERTIFICATE" {
				return nil, WorkloadTrustMaterial{}, errors.New("OPENBAO_PKI_CA_CHAIN_INVALID")
			}
			ca, err := x509.ParseCertificate(block.Bytes)
			if err != nil || !ca.IsCA {
				return nil, WorkloadTrustMaterial{}, errors.New("OPENBAO_PKI_CA_CHAIN_INVALID")
			}
			chainCertificates = append(chainCertificates, ca)
			chainPEM = append(chainPEM, pem.EncodeToMemory(block))
			encoded = string(rest)
		}
	}
	if len(chainCertificates) == 0 {
		return nil, WorkloadTrustMaterial{}, errors.New("OPENBAO_PKI_CA_CHAIN_MISSING")
	}
	for _, ca := range chainCertificates {
		if ca.CheckSignatureFrom(ca) == nil {
			roots.AddCert(ca)
		} else {
			intermediates.AddCert(ca)
		}
	}
	if len(roots.Subjects()) == 0 {
		return nil, WorkloadTrustMaterial{}, errors.New("OPENBAO_PKI_ROOT_CA_MISSING")
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}}); err != nil {
		return nil, WorkloadTrustMaterial{}, errors.New("OPENBAO_PKI_CERTIFICATE_CHAIN_INVALID")
	}
	cert := &tls.Certificate{Certificate: make([][]byte, len(chainPEM)), PrivateKey: privateKey, Leaf: leaf}
	for i, encoded := range chainPEM {
		block, _ := pem.Decode(encoded)
		cert.Certificate[i] = block.Bytes
	}
	return cert, WorkloadTrustMaterial{Roots: roots, Intermediates: intermediates, CRLIssuers: chainCertificates}, nil
}

func hasExtKeyUsage(cert *x509.Certificate, expected x509.ExtKeyUsage) bool {
	for _, usage := range cert.ExtKeyUsage {
		if usage == expected || usage == x509.ExtKeyUsageAny {
			return true
		}
	}
	return len(cert.ExtKeyUsage) == 0
}

func (r *CertReloader) getCertificate(_ *tls.CertificateRequestInfo) (*tls.Certificate, error) {
	state := r.current.Load()
	if state == nil || state.certificate == nil || state.certificate.Leaf == nil || !state.certificate.Leaf.NotAfter.After(r.now()) {
		return nil, errors.New("WORKLOAD_CERTIFICATE_UNAVAILABLE")
	}
	if !workloadCRLIsCurrent(state.trust, r.now()) || certificateIsRevoked(state.certificate.Leaf, state.trust.RevocationList) {
		return nil, errors.New("WORKLOAD_CERTIFICATE_REVOCATION_UNAVAILABLE")
	}
	return state.certificate, nil
}

func (r *CertReloader) TLSClientConfig(serverName string) (*tls.Config, error) {
	state := r.current.Load()
	if state == nil || state.trust.Roots == nil {
		return nil, errors.New("WORKLOAD_CERTIFICATE_UNAVAILABLE")
	}
	if serverName == "" {
		serverName = r.dnsName
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS13, ServerName: serverName, RootCAs: state.trust.Roots.Clone(),
		GetClientCertificate: r.getCertificate,
		VerifyConnection: func(connection tls.ConnectionState) error {
			if len(connection.PeerCertificates) == 0 {
				return errors.New("WORKLOAD_SERVER_CERTIFICATE_MISSING")
			}
			active := r.current.Load()
			if active == nil {
				return errors.New("WORKLOAD_CERTIFICATE_UNAVAILABLE")
			}
			return verifyWorkloadServerCertificate(connection.PeerCertificates, active.trust, serverName, r.now())
		},
	}, nil
}

func (r *CertReloader) rotationDeadline() (time.Time, error) {
	state := r.current.Load()
	if state == nil || state.certificate == nil || state.certificate.Leaf == nil {
		return time.Time{}, errors.New("WORKLOAD_CERTIFICATE_UNAVAILABLE")
	}
	lifetime := state.certificate.Leaf.NotAfter.Sub(state.certificate.Leaf.NotBefore)
	return state.certificate.Leaf.NotBefore.Add(time.Duration(float64(lifetime) * workloadRefreshFraction)), nil
}

func (r *CertReloader) TrustMaterial() (WorkloadTrustMaterial, error) {
	state := r.current.Load()
	if state == nil || state.trust.Roots == nil || state.trust.RevocationList == nil || !workloadCRLIsCurrent(state.trust, r.now()) {
		return WorkloadTrustMaterial{}, errors.New("WORKLOAD_CERTIFICATE_UNAVAILABLE")
	}
	material := state.trust
	material.Roots = material.Roots.Clone()
	if material.Intermediates != nil {
		material.Intermediates = material.Intermediates.Clone()
	}
	material.CRLIssuers = append([]*x509.Certificate(nil), material.CRLIssuers...)
	return material, nil
}

// CurrentWorkloadCR implements auth.WorkloadCRLSource without introducing an
// auth -> audit -> openbao import cycle. The returned list is immutable.
func (r *CertReloader) CurrentWorkloadCRL() (*x509.RevocationList, time.Time) {
	state := r.current.Load()
	if state == nil {
		return nil, time.Time{}
	}
	return state.trust.RevocationList, state.trust.CRLFetchedAt
}

func (r *CertReloader) refreshCRL(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.current.Load()
	if state == nil || state.certificate == nil || state.trust.Roots == nil {
		return errors.New("WORKLOAD_CERTIFICATE_UNAVAILABLE")
	}
	encoded, err := r.client.readPKICRL(ctx)
	if err != nil {
		return errors.New("OPENBAO_PKI_CRL_UNAVAILABLE")
	}
	now := r.now().UTC()
	crl, err := parseAndVerifyWorkloadCRL(encoded, state.trust, now)
	if err != nil {
		return err
	}
	updated := *state
	updated.trust.RevocationList = crl
	updated.trust.CRLFetchedAt = now
	if certificateIsRevoked(state.certificate.Leaf, crl) {
		// Publish the revocation before reporting failure so every later mTLS
		// check observes the revoked serial immediately.
		r.current.Store(&updated)
		return errors.New("WORKLOAD_CERTIFICATE_REVOKED")
	}
	r.current.Store(&updated)
	return nil
}

func workloadCRLIsCurrent(trust WorkloadTrustMaterial, now time.Time) bool {
	return trust.RevocationList != nil && !trust.CRLFetchedAt.IsZero() &&
		!trust.CRLFetchedAt.After(now.Add(30*time.Second)) && now.Sub(trust.CRLFetchedAt) <= workloadCRLMaxAge &&
		!trust.RevocationList.ThisUpdate.After(now) && !trust.RevocationList.NextUpdate.IsZero() && trust.RevocationList.NextUpdate.After(now)
}

func certificateIsRevoked(certificate *x509.Certificate, crl *x509.RevocationList) bool {
	if certificate == nil || crl == nil {
		return true
	}
	for _, revoked := range crl.RevokedCertificateEntries {
		if revoked.SerialNumber != nil && certificate.SerialNumber.Cmp(revoked.SerialNumber) == 0 {
			return true
		}
	}
	return false
}

func (c *Client) readPKICRL(ctx context.Context) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.address+"/v1/pki/crl/pem", nil)
	if err != nil {
		return nil, errors.New("create OpenBao CRL request")
	}
	request.Header.Set("Accept", "application/x-pem-file")
	response, err := c.http.Do(request)
	if err != nil {
		return nil, errors.New("OPENBAO_PKI_CRL_UNAVAILABLE")
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, errors.New("OPENBAO_PKI_CRL_UNAVAILABLE")
	}
	const maximumCRLBytes = 4 << 20
	body, err := io.ReadAll(io.LimitReader(response.Body, maximumCRLBytes+1))
	if err != nil || len(body) > maximumCRLBytes {
		return nil, errors.New("OPENBAO_PKI_CRL_INVALID")
	}
	return body, nil
}

func parseAndVerifyWorkloadCRL(encoded []byte, trust WorkloadTrustMaterial, now time.Time) (*x509.RevocationList, error) {
	block, _ := pem.Decode(encoded)
	if block == nil || block.Type != "X509 CRL" {
		var response struct {
			Data struct {
				CRL string `json:"crl"`
			} `json:"data"`
		}
		if err := json.Unmarshal(encoded, &response); err != nil || response.Data.CRL == "" {
			return nil, errors.New("OPENBAO_PKI_CRL_INVALID")
		}
		block, _ = pem.Decode([]byte(response.Data.CRL))
		if block == nil || block.Type != "X509 CRL" {
			return nil, errors.New("OPENBAO_PKI_CRL_INVALID")
		}
	}
	crl, err := x509.ParseRevocationList(block.Bytes)
	if err != nil || crl.ThisUpdate.After(now) || crl.NextUpdate.IsZero() || !crl.NextUpdate.After(now) {
		return nil, errors.New("OPENBAO_PKI_CRL_STALE_OR_INVALID")
	}
	for _, issuer := range trust.CRLIssuers {
		if issuer != nil && crl.CheckSignatureFrom(issuer) == nil {
			return crl, nil
		}
	}
	return nil, errors.New("OPENBAO_PKI_CRL_UNTRUSTED")
}

func verifyWorkloadServerCertificate(chain []*x509.Certificate, trust WorkloadTrustMaterial, serverName string, now time.Time) error {
	if len(chain) == 0 || trust.Roots == nil || !workloadCRLIsCurrent(trust, now) {
		return errors.New("WORKLOAD_SERVER_TRUST_UNAVAILABLE")
	}
	intermediates := x509.NewCertPool()
	if trust.Intermediates != nil {
		intermediates = trust.Intermediates.Clone()
	}
	for _, certificate := range chain[1:] {
		intermediates.AddCert(certificate)
	}
	if _, err := chain[0].Verify(x509.VerifyOptions{Roots: trust.Roots, Intermediates: intermediates, CurrentTime: now, DNSName: serverName, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		return errors.New("WORKLOAD_SERVER_CERTIFICATE_UNTRUSTED")
	}
	crl := trust.RevocationList
	if crl.ThisUpdate.After(now) || crl.NextUpdate.IsZero() || !crl.NextUpdate.After(now) {
		return errors.New("WORKLOAD_SERVER_CRL_STALE")
	}
	trusted := false
	for _, issuer := range trust.CRLIssuers {
		if issuer != nil && crl.CheckSignatureFrom(issuer) == nil {
			trusted = true
			break
		}
	}
	if !trusted {
		return errors.New("WORKLOAD_SERVER_CRL_UNTRUSTED")
	}
	for _, revoked := range crl.RevokedCertificateEntries {
		if revoked.SerialNumber != nil && chain[0].SerialNumber.Cmp(revoked.SerialNumber) == 0 {
			return errors.New("WORKLOAD_SERVER_CERTIFICATE_REVOKED")
		}
	}
	return nil
}
