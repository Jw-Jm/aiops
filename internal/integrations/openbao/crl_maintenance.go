package openbao

import (
	"context"
	"fmt"
	"net/http"
)

func (c *Client) ensurePKICRLMaintenance(ctx context.Context) error {
	var response dataResponse
	if err := c.request(ctx, http.MethodGet, "/v1/pki/config/crl", nil, &response); err != nil {
		return err
	}
	if response.Data["disable"] != false {
		return fmt.Errorf("%w: PKI CRL must remain enabled", ErrConfigurationDrift)
	}
	automatic, ok := response.Data["auto_rebuild"].(bool)
	if !ok {
		return fmt.Errorf("%w: PKI CRL rebuild setting is missing", ErrConfigurationDrift)
	}
	if !automatic {
		// Operator bootstrap changes only maintenance, preserving revoked entries and expiry.
		if err := c.request(ctx, http.MethodPost, "/v1/pki/config/crl", map[string]any{"auto_rebuild": true}, nil); err != nil {
			return err
		}
	}
	return c.verifyPKICRLMaintenance(ctx)
}

func (c *Client) verifyPKICRLMaintenance(ctx context.Context) error {
	var response dataResponse
	if err := c.request(ctx, http.MethodGet, "/v1/pki/config/crl", nil, &response); err != nil {
		return err
	}
	if response.Data["disable"] != false || response.Data["auto_rebuild"] != true {
		return fmt.Errorf("%w: PKI CRL requires automatic rebuilding and enabled revocation", ErrConfigurationDrift)
	}
	return nil
}
