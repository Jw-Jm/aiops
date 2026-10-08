//go:build pre_sp07_live

package app

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"os"
	"testing"
)

func TestNativeVictoriaSourceAuthentication(t *testing.T) {
	path := os.Getenv("PRE_SP07_NATIVE_SOURCE_AUTH_CONFIG")
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		t.Fatal("owned native TLS source configuration required")
	}
	var configs []struct{ Name, Endpoint, CAFile, CredentialFile, Path, ContainerID string }
	raw, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(raw, &configs) != nil || len(configs) != 2 {
		t.Fatal("two exact official native Victoria sources required")
	}
	for _, config := range configs {
		t.Run(config.Name, func(t *testing.T) {
			ca, err := os.ReadFile(config.CAFile)
			roots := x509.NewCertPool()
			if err != nil || !roots.AppendCertsFromPEM(ca) {
				t.Fatal("independent native source CA required")
			}
			base := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}}
			defer base.CloseIdleConnections()
			request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, config.Endpoint+config.Path, nil)
			if err != nil {
				t.Fatal("exact source request unavailable")
			}
			response, err := base.RoundTrip(request)
			if err != nil {
				t.Fatal("native TLS source unavailable")
			}
			response.Body.Close()
			if response.StatusCode != http.StatusUnauthorized {
				t.Fatalf("native unauthenticated request returned %d", response.StatusCode)
			}
			transport := httpSourceTransport{base, config.CredentialFile}
			response, err = transport.RoundTrip(request)
			if err != nil {
				t.Fatal("configured native source authentication failed")
			}
			response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("configured native source authentication returned %d", response.StatusCode)
			}
			if request.Header.Get("Authorization") != "" {
				t.Fatal("source credential modified the original request")
			}
			original, err := os.ReadFile(config.CredentialFile)
			if err != nil {
				t.Fatal("source credential unavailable")
			}
			defer os.WriteFile(config.CredentialFile, original, 0600)
			var credential map[string]string
			if json.Unmarshal(original, &credential) != nil {
				t.Fatal("explicit native credential missing")
			}
			credential["password"] = "wrong-native-password-with-24-characters"
			wrong, _ := json.Marshal(credential)
			if os.WriteFile(config.CredentialFile, wrong, 0600) != nil {
				t.Fatal("isolated credential rotation unavailable")
			}
			response, err = transport.RoundTrip(request)
			if err != nil {
				t.Fatal("native negative control unavailable")
			}
			response.Body.Close()
			if response.StatusCode != http.StatusUnauthorized {
				t.Fatal("wrong native credential accepted")
			}
			if os.WriteFile(config.CredentialFile, original, 0600) != nil {
				t.Fatal("isolated credential recovery unavailable")
			}
			response, err = transport.RoundTrip(request)
			if err != nil {
				t.Fatal("native recovery positive unavailable")
			}
			response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatal("native source credential recovery failed")
			}
			plain := request.Clone(request.Context())
			plain.URL.Scheme = "http"
			if response, err = transport.RoundTrip(plain); err == nil || response != nil {
				t.Fatal("Basic credential allowed on plaintext")
			}
			credential["unexpected"] = "must-not-forward"
			malformed, _ := json.Marshal(credential)
			if os.WriteFile(config.CredentialFile, malformed, 0600) != nil {
				t.Fatal("isolated malformed credential unavailable")
			}
			if response, err = transport.RoundTrip(request); err == nil || response != nil {
				t.Fatal("unknown private credential fields accepted")
			}
		})
	}
}
