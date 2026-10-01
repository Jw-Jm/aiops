package contract

import (
	"net/url"
	"ops-platform/internal/profile"
	"testing"
)

func TestCoreTemplateLocksHTTPSRuntimeTrustEndpoints(t *testing.T) {
	p, err := profile.ReadProfileFile("../../deploy/profiles/dev-orbstack.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"keycloak", "openbao", "seaweedfs"} {
		endpoint, err := url.Parse(p.Components[name].Endpoint)
		if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" {
			t.Errorf("core template %s runtime endpoint is not HTTPS", name)
		}
	}
}
