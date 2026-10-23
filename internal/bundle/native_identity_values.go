package bundle

import (
	"errors"
	"net/url"
	"strconv"

	"github.com/google/uuid"
	"ops-platform/internal/profile"
)

func (b BusinessValues) NativeIdentity() bool {
	return textValue(object(b.values, "humanAuth"), "mode") == "native"
}

func validateHumanIdentityValues(d map[string]any) error {
	config := object(d, "humanAuth")
	if textValue(config, "mode") != "native" {
		return nil
	}
	tenant, err := uuid.Parse(textValue(config, "tenantId"))
	if err != nil || tenant == uuid.Nil {
		return errors.New("native identity requires a nonzero deployment tenant UUID")
	}
	found := false
	tenants := object(d, "sp06")["tenants"].([]any)
	if len(tenants) != 1 {
		return errors.New("native fixed-tenant installation requires exactly one installed tenant")
	}
	for _, raw := range tenants {
		id, err := uuid.Parse(raw.(string))
		if err == nil && id == tenant {
			found = true
		}
	}
	if !found {
		return errors.New("native deployment tenant is outside the installed tenant scope")
	}
	origin, err := url.Parse(textValue(config, "publicOrigin"))
	if err != nil || origin.Scheme != "https" || origin.Hostname() == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		return errors.New("native identity requires a public HTTPS origin without path")
	}
	if origin.Port() != "" {
		port, err := strconv.Atoi(origin.Port())
		if err != nil || port < 1 || port > 65535 {
			return errors.New("native identity origin port is invalid")
		}
	}
	web := object(d, "sp08")
	if web["enabled"] == true && textValue(web, "publicOrigin") != textValue(config, "publicOrigin") {
		return errors.New("native API and Web public origins must match")
	}
	return nil
}

func validateIdentityProfile(b *BusinessValues, p profile.ResolvedProfile) error {
	if b == nil || !b.NativeIdentity() {
		return nil
	}
	// Explicitly disabled avoids installing/adopting an old identity service during a native switch.
	if component, ok := p.Components["keycloak"]; !ok || component.Mode != "disabled" {
		return errors.New("native installation requires Keycloak explicitly disabled in the resolved Profile")
	}
	return nil
}

func nativeRuntimeValues(b BusinessValues) map[string]any {
	config := object(b.values, "humanAuth")
	id, _ := uuid.Parse(textValue(config, "tenantId"))
	return map[string]any{"tenantId": id.String(), "publicOrigin": textValue(config, "publicOrigin"), "mfaTransitKey": textValue(config, "mfaTransitKey")}
}
