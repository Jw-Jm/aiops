package app

import (
	"errors"
	"os"

	platformcrypto "ops-platform/internal/crypto"
	"ops-platform/internal/identity"
	"ops-platform/internal/integrations/openbao"
)

// An explicitly configured Transit backend never falls back to a file key.
func nativeMFASealerFromEnv() (identity.SecretSealer, error) {
	key, file := os.Getenv("OPENBAO_MFA_TRANSIT_KEY"), os.Getenv("OPS_MFA_SECRET_KEY_FILE")
	if key != "" {
		if file != "" {
			return nil, errors.New("configure only one MFA secret backend")
		}
		ca, err := os.ReadFile(os.Getenv("OPENBAO_CA_FILE"))
		if err != nil {
			return nil, errors.New("MFA OpenBao CA is unavailable")
		}
		client, err := openbao.NewClient(openbao.ClientConfig{Address: os.Getenv("OPENBAO_ADDR"), ServerName: os.Getenv("OPENBAO_SERVER_NAME"), CACertBundle: ca, ServiceDomain: os.Getenv("OPENBAO_SERVICE_DOMAIN")})
		if err != nil {
			return nil, errors.New("MFA OpenBao configuration is invalid")
		}
		transit, err := openbao.NewProjectedTransit(client, os.Getenv("OPENBAO_PROJECTED_TOKEN_FILE"), "ops-api")
		if err != nil {
			return nil, errors.New("MFA projected identity is unavailable")
		}
		protector, err := platformcrypto.NewTransitProtector(transit, key)
		if err != nil {
			return nil, err
		}
		return identity.NewProtectorSecretSealer(protector, key)
	}
	if file != "" {
		return identity.NewFileSecretSealer(file)
	}
	return identity.UnavailableSecretSealer{}, nil
}
