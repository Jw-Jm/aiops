//go:build ignore

// Probe the real wrapper's mTLS and request boundary without creating a Job/model call.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"ops-platform/internal/auth"
	"ops-platform/internal/integrations/openbao"
	"os"
	"strings"
	"time"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	ns := "ops-native-v12-r34-20261027"
	ca, err := os.ReadFile(os.Getenv("OPENBAO_CA_FILE"))
	must(err)
	pinned, err := os.ReadFile("/etc/r34/workload-ca.pem")
	must(err)
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pinned) {
		panic("invalid pinned CA")
	}
	bao, err := openbao.NewClient(openbao.ClientConfig{Address: os.Getenv("OPENBAO_ADDR"), CACertBundle: ca, ServiceDomain: ns + ".svc.cluster.local"})
	must(err)
	reloader, err := openbao.NewCertReloader(bao, ns, "ops-worker")
	must(err)
	must(reloader.StartPinned(ctx, os.Getenv("OPENBAO_PROJECTED_TOKEN_FILE"), roots))
	config, err := reloader.TLSClientConfig("ops-investigator." + ns + ".svc.cluster.local")
	must(err)
	identity, err := auth.NewWorkloadIdentity(ns, "ops-investigator")
	must(err)
	material, err := reloader.TrustMaterial()
	must(err)
	trust := auth.WorkloadTrust{Roots: roots, Intermediates: material.Intermediates, Allowed: []auth.WorkloadIdentity{identity}, CRLSource: reloader}
	config.VerifyConnection = func(s tls.ConnectionState) error {
		if len(s.PeerCertificates) == 0 {
			return fmt.Errorf("missing peer")
		}
		_, err := auth.VerifyWorkload(auth.WithWorkloadTrust(ctx, trust), s.PeerCertificates[0])
		return err
	}
	call := func(c *tls.Config) (int, error) {
		client := &http.Client{Transport: &http.Transport{TLSClientConfig: c}, Timeout: 10 * time.Second}
		defer client.CloseIdleConnections()
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://127.0.0.1:18412/internal/v1/investigate", strings.NewReader(`{}`))
		if err != nil {
			return 0, err
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err != nil {
			return 0, err
		}
		defer response.Body.Close()
		_, err = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return response.StatusCode, err
	}
	status, err := call(config)
	must(err)
	if status != 400 {
		panic("valid Worker identity did not reach request validation")
	}
	denied := config.Clone()
	denied.GetClientCertificate = nil
	denied.Certificates = nil
	rejected, err := call(denied)
	if err == nil && rejected != 403 {
		panic("missing workload certificate was accepted")
	}
	must(json.NewEncoder(os.Stdout).Encode(map[string]any{"workerMTLS": true, "investigatorRequestValidationStatus": status, "withoutClientCertificateRejected": true, "modelRequests": 0, "newJobs": 0}))
}
func must(err error) {
	if err != nil {
		panic("workload probe failed; no credentials retained in output")
	}
}
