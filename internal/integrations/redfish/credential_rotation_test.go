package redfish

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type credentialTransport func(*http.Request) (*http.Response, error)

func (f credentialTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestCredentialProjectionRotatesWithoutRestart(t *testing.T) {
	file := filepath.Join(t.TempDir(), "credential.json")
	origin, _ := url.Parse("https://bmc.example")
	seen := ""
	transport := readOnlyTransport{origin: origin, credentialFile: file, base: credentialTransport(func(r *http.Request) (*http.Response, error) {
		_, seen, _ = r.BasicAuth()
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})}
	for _, password := range []string{"first-password", "rotated-password"} {
		if err := os.WriteFile(file, []byte(`{"Username":"operator","Password":"`+password+`"}`), 0600); err != nil {
			t.Fatal(err)
		}
		request, _ := http.NewRequest("GET", origin.String()+"/redfish/v1/Systems", nil)
		response, err := transport.RoundTrip(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if seen != password {
			t.Fatal("stale credential after rotation")
		}
	}
	os.Remove(file)
	request, _ := http.NewRequest("GET", origin.String()+"/redfish/v1/Systems", nil)
	if _, err := transport.RoundTrip(request); err == nil {
		t.Fatal("missing credential did not fail closed")
	}
}
