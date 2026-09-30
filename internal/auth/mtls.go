package auth

import (
	"context"
	"crypto/tls"
	"errors"
)

// WorkloadMTLSServerConfig requires verified client certificates and applies
// the exact SPIFFE allowlist plus current CRL checks on every new TLS session.
func WorkloadMTLSServerConfig(ctx context.Context, trust WorkloadTrust) (*tls.Config, error) {
	if trust.Roots == nil || (trust.RevocationList == nil && trust.CRLSource == nil) || len(trust.Allowed) == 0 {
		return nil, errors.New("workload CA, current CRL, and exact identity allowlist are required")
	}
	trustContext := WithWorkloadTrust(ctx, trust)
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		ClientAuth: tls.RequireAndVerifyClientCert,
		ClientCAs:  trust.Roots.Clone(),
		VerifyConnection: func(state tls.ConnectionState) error {
			if len(state.PeerCertificates) == 0 {
				return errors.New("workload client certificate is required")
			}
			_, err := VerifyWorkload(trustContext, state.PeerCertificates[0])
			return err
		},
	}, nil
}
