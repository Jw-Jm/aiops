package app

import (
	"errors"
	"os"
	"strings"

	"ops-platform/internal/profile"
)

func (config AppConfig) validateRuntimeProfile(worker bool) error {
	p, err := profile.ReadResolvedProfileFile(config.ProfilePath)
	if err != nil {
		// Decoder errors can contain operator-supplied values. Do not leak them
		// through a process startup log.
		return errors.New("resolved Deployment Profile is unavailable or invalid")
	}
	if !p.Installable || (p.Selected != "core" && p.Selected != "deepflow") || p.Runtime.PublicEgress != "deny" {
		return errors.New("resolved Deployment Profile is not admitted for this runtime")
	}
	for _, name := range []string{"kubevirt", "cdi"} {
		c, ok := p.Components[name]
		if !ok || c.Mode != "disabled" || c.Compatibility != "unverified" {
			return errors.New("resolved Deployment Profile must preserve ADR-0008 virtualization deferral")
		}
	}
	if worker {
		for name, value := range map[string]string{"openbao": os.Getenv("OPENBAO_ADDR"), "seaweedfs": os.Getenv("S3_ENDPOINT")} {
			c, ok := p.Components[name]
			if !ok || c.Mode == "disabled" || value == "" || strings.TrimRight(c.Endpoint, "/") != strings.TrimRight(value, "/") {
				return errors.New("worker endpoint differs from resolved Deployment Profile")
			}
		}
	} else {
		c, ok := p.Components["keycloak"]
		if !ok || c.Mode == "disabled" || config.OIDCIssuerURL != strings.TrimRight(c.Endpoint, "/")+"/realms/ops" {
			return errors.New("OIDC issuer differs from resolved Deployment Profile")
		}
	}
	return nil
}
