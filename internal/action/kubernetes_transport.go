package action

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type KubernetesREST struct {
	Endpoint, TokenFile string
	Client              *http.Client
}

func NewKubernetesREST(endpoint, caFile, tokenFile string) (*KubernetesREST, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Path != "" || u.RawQuery != "" || tokenFile == "" {
		return nil, ErrInvalid
	}
	b, err := os.ReadFile(caFile)
	roots := x509.NewCertPool()
	if err != nil || !roots.AppendCertsFromPEM(b) {
		return nil, ErrInvalid
	}
	return &KubernetesREST{Endpoint: endpoint, TokenFile: tokenFile, Client: &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots}}}}, nil
}
func (k *KubernetesREST) request(ctx context.Context, method, path string, in, out any) error {
	if !strings.HasPrefix(path, "/apis/") {
		return ErrInvalid
	}
	return k.coreRequest(ctx, method, path, in, out)
}
func (k *KubernetesREST) coreRequest(ctx context.Context, method, path string, in, out any) error {
	if k == nil || k.Client == nil || !(strings.HasPrefix(path, "/apis/") || strings.HasPrefix(path, "/api/v1/")) {
		return ErrInvalid
	}
	var body io.Reader
	if in != nil {
		switch b := in.(type) {
		case []byte:
			body = bytes.NewReader(b)
		default:
			body = bytes.NewReader(Canonical(in))
		}
	}
	token, err := os.ReadFile(k.TokenFile)
	if err != nil || len(token) > 1<<20 {
		return errors.New("execution Kubernetes credential unavailable")
	}
	defer clear(token)
	req, err := http.NewRequestWithContext(ctx, method, k.Endpoint+path, body)
	if err != nil {
		return ErrInvalid
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	req.Header.Set("Content-Type", "application/json")
	res, err := k.Client.Do(req)
	if err != nil {
		return errors.New("execution Kubernetes request uncertain")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return errors.New("execution Kubernetes request rejected")
	}
	if out != nil {
		d := json.NewDecoder(io.LimitReader(res.Body, 2<<20))
		return d.Decode(out)
	}
	return nil
}
func (k *KubernetesREST) CreateJob(ctx context.Context, ns string, b []byte) (string, error) {
	if !namePattern.MatchString(ns) {
		return "", ErrInvalid
	}
	var out struct{ Metadata struct{ Name, UID string } }
	err := k.request(ctx, "POST", "/apis/batch/v1/namespaces/"+ns+"/jobs", b, &out)
	if err != nil {
		return "", err
	}
	if out.Metadata.UID == "" {
		return "", ErrInvalid
	}
	return out.Metadata.Name + "/" + out.Metadata.UID, nil
}
func (k *KubernetesREST) DeleteJob(ctx context.Context, ns, name, uid string) error {
	if !namePattern.MatchString(ns) || !namePattern.MatchString(name) || uid == "" {
		return ErrInvalid
	}
	return k.request(ctx, "DELETE", "/apis/batch/v1/namespaces/"+ns+"/jobs/"+name, map[string]any{"apiVersion": "v1", "kind": "DeleteOptions", "propagationPolicy": "Foreground", "preconditions": map[string]string{"uid": uid}}, nil)
}
func (k *KubernetesREST) JobState(ctx context.Context, ns, ref string) (string, error) {
	parts := strings.Split(ref, "/")
	if !namePattern.MatchString(ns) || len(parts) != 2 || !namePattern.MatchString(parts[0]) {
		return "", ErrInvalid
	}
	var out struct {
		Metadata struct{ UID string }
		Status   struct{ Succeeded, Failed, Active int }
	}
	if err := k.request(ctx, "GET", "/apis/batch/v1/namespaces/"+ns+"/jobs/"+parts[0], nil, &out); err != nil {
		return "unknown", err
	}
	if out.Metadata.UID != parts[1] {
		return "unknown", ErrDenied
	}
	if out.Status.Succeeded > 0 {
		return "completed", nil
	}
	if out.Status.Failed > 0 {
		return "failed", nil
	}
	return "active", nil
}
