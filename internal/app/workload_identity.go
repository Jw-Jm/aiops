package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"os"

	"ops-platform/internal/auth"
	"ops-platform/internal/integrations/openbao"
)

func validIdentityMode(mode string) bool {
	return mode == "" || mode == "file" || mode == "openbao-kubernetes"
}

// runtimeWorkloadIdentity reuses the admitted PKI lifecycle in the service
// process. Bootstrap files pin public trust; the rotating leaf key stays only
// in memory and is never written to a Secret or sent to OpenBao.
func runtimeWorkloadIdentity(ctx context.Context, namespace, serviceAccount, caFile string, allowed []auth.WorkloadIdentity) (*tls.Config, auth.WorkloadTrust, error) {
	var empty auth.WorkloadTrust
	if os.Getenv("OPENBAO_ADDR") == "" || os.Getenv("OPENBAO_CA_FILE") == "" || os.Getenv("OPENBAO_PROJECTED_TOKEN_FILE") == "" {
		return nil, empty, errors.New("workload PKI requires explicit OpenBao trust and projected token")
	}
	baoCA, err := os.ReadFile(os.Getenv("OPENBAO_CA_FILE"))
	if err != nil {
		return nil, empty, errors.New("workload OpenBao trust unavailable")
	}
	ca, err := os.ReadFile(caFile)
	roots := x509.NewCertPool()
	if err != nil || !roots.AppendCertsFromPEM(ca) {
		return nil, empty, errors.New("pinned workload CA unavailable")
	}
	client, err := openbao.NewClient(openbao.ClientConfig{Address: os.Getenv("OPENBAO_ADDR"), ServerName: os.Getenv("OPENBAO_SERVER_NAME"), CACertBundle: baoCA, ServiceDomain: namespace + ".svc.cluster.local"})
	if err != nil {
		return nil, empty, err
	}
	reloader, err := openbao.NewCertReloader(client, namespace, serviceAccount)
	if err != nil {
		return nil, empty, err
	}
	if err := reloader.StartPinned(ctx, os.Getenv("OPENBAO_PROJECTED_TOKEN_FILE"), roots); err != nil {
		return nil, empty, err
	}
	material, err := reloader.TrustMaterial()
	if err != nil {
		return nil, empty, err
	}
	active, err := reloader.TLSClientConfig("")
	if err != nil {
		return nil, empty, err
	}
	certificate := active.GetClientCertificate
	trust := auth.WorkloadTrust{Roots: roots, Intermediates: material.Intermediates, Allowed: allowed, CRLSource: reloader}
	config, err := auth.WorkloadMTLSServerConfig(ctx, trust)
	if err != nil {
		return nil, empty, err
	}
	config.GetClientCertificate = certificate
	config.GetCertificate = func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
		return certificate(&tls.CertificateRequestInfo{})
	}
	return config, trust, nil
}
