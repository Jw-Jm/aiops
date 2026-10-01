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
	for _, name := range []string{"kubevirt", "cdi"} {
		component := p.Components[name]
		if component.Mode != "disabled" || component.Compatibility != "unverified" || component.DisabledReason != "development_deferred" {
			t.Errorf("core template activated deferred component %s", name)
		}
	}
}
