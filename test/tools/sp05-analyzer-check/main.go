// Verification entry point only. Not installed in an API/Worker runtime image.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"ops-platform/internal/integrations/k8sgpt"
	"ops-platform/internal/integrations/kubernetes"
	"os"
	"strings"
)

func main() {
	ctx := context.Background()
	var config struct {
		Clusters []struct {
			Cluster struct {
				Server string `json:"server"`
				CA     string `json:"certificate-authority-data"`
			} `json:"cluster"`
		} `json:"clusters"`
		Users []struct {
			User struct {
				TokenFile string `json:"tokenFile"`
			} `json:"user"`
		} `json:"users"`
	}
	raw, err := os.ReadFile("/fixture/kubeconfig.json")
	if err != nil || json.Unmarshal(raw, &config) != nil || len(config.Clusters) != 1 || len(config.Users) != 1 {
		panic("native verification config unavailable")
	}
	ca, err := base64.StdEncoding.DecodeString(config.Clusters[0].Cluster.CA)
	if err != nil {
		panic("native verification trust unavailable")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		panic("native verification trust invalid")
	}
	token, err := os.ReadFile(config.Users[0].User.TokenFile)
	if err != nil {
		panic("native verification credential unavailable")
	}
	client, _ := kubernetes.NewClient(config.Clusters[0].Cluster.Server, &http.Client{Transport: verifiedTransport{base: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}}, token: strings.TrimSpace(string(token))}}, 10, 25)
	broker := &k8sgpt.ReadBroker{Client: client, Namespace: os.Getenv("OPS_TEST_NAMESPACE"), Authorize: func(context.Context) error { return nil }}
	private, closeBroker, err := broker.Start(ctx)
	if err != nil {
		panic("private verification read broker unavailable")
	}
	defer closeBroker()
	out, err := (k8sgpt.Adapter{Binary: "/opt/ops/bin/k8sgpt", Kubeconfig: private, SHA256: os.Getenv("OPS_TEST_ANALYZER_SHA256"), Namespace: os.Getenv("OPS_TEST_NAMESPACE")}).Run(ctx)
	if err != nil || !broker.Complete() {
		fmt.Fprintln(os.Stderr, err, broker.Failures())
		os.Exit(1)
	}
	// Never log native error text, Sensitive maps or credentials.
	type result struct {
		Kind   string `json:"kind"`
		Name   string `json:"name"`
		Errors int    `json:"errors"`
		Parent string `json:"parentObject"`
	}
	items := []result{}
	for _, item := range out.Results {
		items = append(items, result{item.Kind, item.Name, len(item.Error), item.ParentObject})
	}
	raw, _ = json.Marshal(map[string]any{"status": out.Status, "provider": out.Provider, "problems": out.Problems, "results": items, "sandbox": "kernel-cgroup-readonly-root-verified", "llm": false})
	fmt.Println(string(raw))
}

type verifiedTransport struct {
	base  http.RoundTripper
	token string
}

func (t verifiedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	copy := r.Clone(r.Context())
	copy.Header = r.Header.Clone()
	copy.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(copy)
}
