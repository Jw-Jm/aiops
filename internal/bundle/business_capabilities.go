package bundle

import (
	"encoding/json"
	"errors"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"ops-platform/internal/modelprofile"
	"ops-platform/internal/profile"
)

func (b BusinessValues) WebEnabled() bool { return object(b.values, "sp08")["enabled"] == true }

func validateBusinessCapabilities(d map[string]any) error {
	sp06 := object(d, "sp06")
	assistant := object(sp06, "assistant")
	if assistant["enabled"] == true {
		var material modelprofile.Profile
		raw, _ := json.Marshal(assistant["profile"])
		model := object(sp06, "model")
		if json.Unmarshal(raw, &material) != nil || material.AdmissionState != "admitted" || !material.Valid(":8083") || material.Reference() != textValue(assistant, "modelProfileVersion") || material.BaseURL != textValue(model, "base_url") || material.APIKeyRef != textValue(model, "api_key_ref") || float64(material.TimeoutSeconds) != model["timeout"] {
			return errors.New("assistant requires an admitted frozen Profile matching the shared endpoint, credential reference and timeout")
		}
	}
	web := object(d, "sp08")
	if web["enabled"] != true {
		return nil
	}
	origin, err := url.Parse(textValue(web, "publicOrigin"))
	if err != nil || origin.Hostname() == "" {
		return errors.New("Web requires a public HTTPS origin")
	}
	if origin.Port() != "" {
		port, err := strconv.Atoi(origin.Port())
		if err != nil || port < 1 || port > 65535 {
			return errors.New("Web public origin port is invalid")
		}
	}
	for _, raw := range web["ingressCIDRs"].([]any) {
		prefix, err := netip.ParsePrefix(raw.(string))
		if err != nil || prefix.Bits() == 0 || prefix != prefix.Masked() || prefix.Addr().IsMulticast() || prefix.Addr().IsUnspecified() || prefix.Addr().Is4In6() {
			return errors.New("Web requires explicit canonical bounded client CIDRs")
		}
	}
	return nil
}

// The nginx recipe uses fixed API/8443 OIDC upstreams. Bind them to the
// installed Profile instead of accepting caller-selected proxy destinations.
func validateWebProfile(b BusinessValues, p profile.ResolvedProfile) error {
	if !b.WebEnabled() {
		return nil
	}
	// shortcut: the current Web recipe copies amd64 libraries; remove this fence only after per-architecture material qualification.
	if p.Architecture != "amd64" {
		return errors.New("SP08 Web image recipe currently supports amd64 only; other architectures remain unverified")
	}
	endpoint := strings.TrimSuffix(p.Components["keycloak"].Endpoint, "/")
	u, err := url.Parse(endpoint)
	oidc := object(object(b.values, "sp08"), "oidc")
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.Port() != "8443" || u.Path != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Hostname() != textValue(oidc, "proxyHost") || endpoint+"/realms/ops" != textValue(oidc, "authority") {
		return errors.New("Web OIDC authority/proxy must match the installed Keycloak HTTPS/8443 realm")
	}
	return nil
}
