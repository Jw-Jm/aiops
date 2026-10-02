package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"net/http"
	"ops-platform/internal/graph"
	"ops-platform/internal/integrations/kubernetes"
	"os"
	"strings"
	"time"
)

type graphRecoveryConfig struct {
	DatabaseURL, Endpoint, CAFile, TokenFile string
	Request                                  graph.LeaseRecoveryRequest
}
type recoveryTokenTransport struct {
	base http.RoundTripper
	file string
}

func (t recoveryTokenTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	token, err := os.ReadFile(t.file)
	if err != nil || len(token) > 64<<10 {
		return nil, errors.New("operator Kubernetes credential unavailable")
	}
	value := strings.TrimSpace(string(token))
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return nil, errors.New("operator Kubernetes credential invalid")
	}
	clone := request.Clone(request.Context())
	clone.Header.Set("Authorization", "Bearer "+value)
	return t.base.RoundTrip(clone)
}
func runGraphRecovery(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("opsctl graph lease-recover", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "", "private operator JSON; database and Kubernetes authority credentials")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *path == "" || flags.NArg() != 0 {
		return errors.New("graph recovery requires --config <private-json>")
	}
	info, err := os.Stat(*path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return errors.New("graph recovery configuration must be private (0600)")
	}
	raw, err := os.ReadFile(*path)
	if err != nil || len(raw) > 256<<10 {
		return errors.New("graph recovery configuration unavailable")
	}
	var config graphRecoveryConfig
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil || config.DatabaseURL == "" || config.TokenFile == "" || config.CAFile == "" || !strings.HasPrefix(config.Endpoint, "https://") {
		return errors.New("graph recovery configuration invalid")
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return errors.New("graph recovery configuration has trailing payload")
	}
	cert, err := os.ReadFile(config.CAFile)
	if err != nil {
		return errors.New("operator Kubernetes CA unavailable")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(cert) {
		return errors.New("operator Kubernetes CA invalid")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}}
	defer transport.CloseIdleConnections()
	bounded, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	client, err := kubernetes.NewClient(config.Endpoint, &http.Client{Transport: recoveryTokenTransport{transport, config.TokenFile}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, 10, 25)
	if err != nil {
		return err
	}
	pool, err := pgxpool.New(bounded, config.DatabaseURL)
	if err != nil {
		return errors.New("graph recovery database unavailable")
	}
	defer pool.Close()
	receipt, err := graph.RecoverLease(bounded, client, graph.Repository{Pool: pool}, config.Request)
	if err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(receipt)
}
