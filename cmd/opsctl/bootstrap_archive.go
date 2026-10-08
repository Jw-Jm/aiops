package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/integrations/s3"
)

func runBootstrapArchive(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("opsctl bootstrap archive-bucket", flag.ContinueOnError)
	flags.SetOutput(stderr)
	profilePath := flags.String("profile", "", "explicit current bundled Profile")
	privatePath := flags.String("secrets-file", "", "private external Archive bootstrap credentials")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *profilePath == "" || *privatePath == "" {
		return errors.New("Archive bootstrap requires --profile and --secrets-file")
	}
	info, err := os.Stat(*privatePath)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !outsideGitTree(*privatePath) {
		return errors.New("Archive bootstrap credentials require a private repository-external file")
	}
	raw, err := readBoundedFile(*privatePath, 64<<10)
	var private struct {
		AccessKey string `json:"accessKey"`
		SecretKey string `json:"secretKey"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err != nil || decoder.Decode(&private) != nil || decoder.Decode(new(any)) != io.EOF || private.AccessKey == "" || private.SecretKey == "" {
		return errors.New("explicit Archive bootstrap credentials required")
	}
	p, err := readResolvedProfile(*profilePath)
	if err != nil {
		return err
	}
	component := p.Components["seaweedfs"]
	endpoint, err := url.Parse(component.Endpoint)
	if err != nil || component.Mode != "bundled" || component.Namespace == "" || component.Namespace == "default" || strings.HasPrefix(component.Namespace, "kube-") || endpoint.Scheme != "https" || endpoint.Hostname() != "ops-seaweedfs-s3."+component.Namespace+".svc.cluster.local" || endpoint.Port() != "8333" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.Path != "" {
		return errors.New("explicit current bundled Archive target required")
	}
	cmd := exec.CommandContext(ctx, "kubectl", "--context", p.Kubernetes.Context, "get", "namespace", component.Namespace, "-o", "json")
	raw, err = cmd.Output()
	var namespace struct {
		Metadata struct {
			UID    string
			Labels map[string]string
		}
	}
	if err != nil || json.Unmarshal(raw, &namespace) != nil || namespace.Metadata.UID == "" || namespace.Metadata.Labels["ops.platform.io/managed-by"] != "opsctl-bootstrap" {
		return errors.New("Archive namespace must come from formal environment bootstrap")
	}
	if _, err = uuid.Parse(namespace.Metadata.Labels["ops.platform.io/installation-id"]); err != nil {
		return errors.New("Archive installation identity invalid")
	}
	cmd = exec.CommandContext(ctx, "kubectl", "--context", p.Kubernetes.Context, "-n", component.Namespace, "get", "configmap", "ops-platform-bootstrap", "-o", "json")
	raw, err = cmd.Output()
	var trust struct{ Data map[string]string }
	if err != nil || json.Unmarshal(raw, &trust) != nil {
		return errors.New("independent Archive bootstrap trust unavailable")
	}
	ca := []byte(trust.Data["archive-ca.pem"])
	roots := x509.NewCertPool()
	if bytes.Contains(ca, []byte("PRIVATE KEY")) || !roots.AppendCertsFromPEM(ca) || trust.Data["archive-bucket"] == "" {
		return errors.New("independent public Archive trust and bucket required")
	}
	address, stop, err := serviceBootstrapForward(ctx, p.Kubernetes.Context, component.Namespace, "ops-seaweedfs-s3", 8333)
	if err != nil {
		return err
	}
	defer stop()
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: endpoint.Hostname()}, DialContext: func(ctx context.Context, network, target string) (net.Conn, error) {
		if target != endpoint.Host {
			return nil, errors.New("Archive route outside fixed bootstrap endpoint")
		}
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, address)
	}}
	defer transport.CloseIdleConnections()
	client, err := s3.NewClient(s3.Config{Endpoint: component.Endpoint, CACertBundle: ca, ServerName: endpoint.Hostname(), Bucket: trust.Data["archive-bucket"], AccessKey: private.AccessKey, SecretKey: private.SecretKey, HTTPClient: &http.Client{Transport: transport, Timeout: 30 * time.Second}})
	if err != nil {
		return errors.New("Archive bootstrap client unavailable")
	}
	if err = client.CreateFreshBucket(ctx); err != nil {
		return err
	}
	return json.NewEncoder(stdout).Encode(map[string]any{"namespace": component.Namespace, "namespaceUid": namespace.Metadata.UID, "installationId": namespace.Metadata.Labels["ops.platform.io/installation-id"], "endpoint": component.Endpoint, "bucket": trust.Data["archive-bucket"], "versioning": "Enabled", "objectLock": "Enabled", "defaultRetentionMode": "COMPLIANCE", "defaultRetentionDays": 365, "bootstrapAdministratorRetirement": "pending explicit IAM activation and bootstrap credential retirement"})
}
