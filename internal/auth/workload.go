package auth

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const WorkloadTrustDomain = "ops.local"
const workloadCRLMaxAge = 5 * time.Minute

type WorkloadIdentity struct {
	Namespace      string
	ServiceAccount string
	URI            string
	DNSName        string
}

func NewWorkloadIdentity(namespace, serviceAccount string) (WorkloadIdentity, error) {
	if !validWorkloadName(namespace) || !validWorkloadName(serviceAccount) {
		return WorkloadIdentity{}, errors.New("invalid workload namespace or service account")
	}
	return WorkloadIdentity{
		Namespace:      namespace,
		ServiceAccount: serviceAccount,
		URI:            "spiffe://" + WorkloadTrustDomain + "/ns/" + namespace + "/sa/" + serviceAccount,
		DNSName:        serviceAccount + "." + namespace + ".svc.cluster.local",
	}, nil
}

func validWorkloadName(value string) bool {
	if len(value) == 0 || len(value) > 63 || value[0] == '-' || value[len(value)-1] == '-' {
		return false
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
			return false
		}
	}
	return true
}

type WorkloadTrust struct {
	Roots          *x509.CertPool
	Intermediates  *x509.CertPool
	Allowed        []WorkloadIdentity
	RevocationList *x509.RevocationList
	CRLFetchedAt   time.Time
	CRLIssuers     []*x509.Certificate
	CRLSource      WorkloadCRLSource
	Now            func() time.Time
}

type WorkloadCRLSource interface {
	CurrentWorkloadCRL() (*x509.RevocationList, time.Time)
}

type workloadTrustKey struct{}

func WithWorkloadTrust(ctx context.Context, trust WorkloadTrust) context.Context {
	if trust.Roots != nil {
		trust.Roots = trust.Roots.Clone()
	}
	if trust.Intermediates != nil {
		trust.Intermediates = trust.Intermediates.Clone()
	}
	trust.Allowed = append([]WorkloadIdentity(nil), trust.Allowed...)
	trust.CRLIssuers = append([]*x509.Certificate(nil), trust.CRLIssuers...)
	return context.WithValue(ctx, workloadTrustKey{}, trust)
}

// VerifyWorkload accepts only a CA-verified, unexpired client certificate with
// an exact SPIFFE identity from the server's configured allowlist and a current,
// signature-verified CRL. User OIDC tokens are deliberately not an input.
func VerifyWorkload(ctx context.Context, peerCert *x509.Certificate) (WorkloadIdentity, error) {
	trust, ok := ctx.Value(workloadTrustKey{}).(WorkloadTrust)
	if !ok || peerCert == nil || trust.Roots == nil || len(trust.Allowed) == 0 {
		return WorkloadIdentity{}, errors.New("workload trust is not configured")
	}
	now := time.Now().UTC()
	if trust.Now != nil {
		now = trust.Now().UTC()
	}
	crl, fetchedAt := trust.RevocationList, trust.CRLFetchedAt
	if trust.CRLSource != nil {
		crl, fetchedAt = trust.CRLSource.CurrentWorkloadCRL()
	}
	if crl == nil || fetchedAt.IsZero() || fetchedAt.After(now.Add(30*time.Second)) || now.Sub(fetchedAt) > workloadCRLMaxAge {
		return WorkloadIdentity{}, errors.New("workload certificate revocation data is stale")
	}
	chains, err := peerCert.Verify(x509.VerifyOptions{
		Roots: trust.Roots, Intermediates: trust.Intermediates,
		CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	if err != nil {
		return WorkloadIdentity{}, errors.New("workload certificate chain or validity is not trusted")
	}
	if crl.ThisUpdate.After(now) || crl.NextUpdate.IsZero() || !crl.NextUpdate.After(now) {
		return WorkloadIdentity{}, errors.New("workload certificate revocation data is stale")
	}
	crlTrusted := false
	for _, issuer := range trust.CRLIssuers {
		if issuer != nil && crl.CheckSignatureFrom(issuer) == nil {
			crlTrusted = true
			break
		}
	}
	if !crlTrusted {
		for _, chain := range chains {
			for _, issuer := range chain[1:] {
				if crl.CheckSignatureFrom(issuer) == nil {
					crlTrusted = true
					break
				}
			}
		}
	}
	if !crlTrusted {
		return WorkloadIdentity{}, errors.New("workload certificate revocation data is not signed by a trusted issuer")
	}
	for _, revoked := range crl.RevokedCertificateEntries {
		if revoked.SerialNumber != nil && peerCert.SerialNumber.Cmp(revoked.SerialNumber) == 0 {
			return WorkloadIdentity{}, errors.New("workload certificate is revoked")
		}
	}
	if len(peerCert.URIs) != 1 {
		return WorkloadIdentity{}, errors.New("workload certificate must contain exactly one SPIFFE URI SAN")
	}
	identity, err := workloadIdentityFromURI(peerCert.URIs[0])
	if err != nil {
		return WorkloadIdentity{}, err
	}
	if len(peerCert.DNSNames) != 1 || peerCert.DNSNames[0] != identity.DNSName || len(peerCert.IPAddresses) != 0 || len(peerCert.EmailAddresses) != 0 {
		return WorkloadIdentity{}, errors.New("workload certificate DNS SAN does not match its exact SPIFFE identity")
	}
	if peerCert.KeyUsage&x509.KeyUsageDigitalSignature == 0 || !hasClientAuth(peerCert) {
		return WorkloadIdentity{}, errors.New("workload certificate is not valid for client authentication")
	}
	if !containsWorkloadIdentity(trust.Allowed, identity) {
		return WorkloadIdentity{}, errors.New("workload identity is outside the exact allowlist")
	}
	return identity, nil
}

func workloadIdentityFromURI(value *url.URL) (WorkloadIdentity, error) {
	if value == nil || value.Scheme != "spiffe" || value.Host != WorkloadTrustDomain || value.User != nil || value.RawQuery != "" || value.Fragment != "" || value.RawPath != "" {
		return WorkloadIdentity{}, errors.New("invalid workload SPIFFE URI SAN")
	}
	parts := strings.Split(strings.TrimPrefix(value.Path, "/"), "/")
	if len(parts) != 4 || parts[0] != "ns" || parts[2] != "sa" {
		return WorkloadIdentity{}, errors.New("invalid workload SPIFFE path")
	}
	identity, err := NewWorkloadIdentity(parts[1], parts[3])
	if err != nil || identity.URI != value.String() {
		return WorkloadIdentity{}, fmt.Errorf("invalid workload SPIFFE identity")
	}
	return identity, nil
}

func hasClientAuth(cert *x509.Certificate) bool {
	for _, usage := range cert.ExtKeyUsage {
		if usage == x509.ExtKeyUsageClientAuth || usage == x509.ExtKeyUsageAny {
			return true
		}
	}
	return len(cert.ExtKeyUsage) == 0
}

func containsWorkloadIdentity(allowed []WorkloadIdentity, value WorkloadIdentity) bool {
	for _, candidate := range allowed {
		if candidate.URI == value.URI {
			return true
		}
	}
	return false
}
