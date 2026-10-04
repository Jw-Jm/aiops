package openbao

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"regexp"
	"strings"
)

var transitNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,127}$`)
var transitValuePattern = regexp.MustCompile(`^vault:v([1-9][0-9]*):[A-Za-z0-9+/=]+$`)

type TransitCiphertext struct {
	Ciphertext string `json:"ciphertext"`
	KeyVersion string `json:"keyVersion"`
}
type TransitSignature struct {
	Signature  string
	KeyVersion string
}

func TransitVersion(value string) (string, error) {
	parts := transitValuePattern.FindStringSubmatch(value)
	if len(parts) != 2 {
		return "", errors.New("invalid Transit value")
	}
	return parts[1], nil
}
func (c *Client) TransitEncrypt(ctx context.Context, key string, plain, aad []byte) (TransitCiphertext, error) {
	if !transitNamePattern.MatchString(key) || len(plain) > 20<<20 {
		return TransitCiphertext{}, errors.New("invalid Transit encryption input")
	}
	var out dataResponse
	input := map[string]string{"plaintext": base64.StdEncoding.EncodeToString(plain), "associated_data": base64.StdEncoding.EncodeToString(aad)}
	if err := c.request(ctx, http.MethodPost, "/v1/transit/encrypt/"+key, input, &out); err != nil {
		return TransitCiphertext{}, errors.New("TRANSIT_ENCRYPT_UNAVAILABLE")
	}
	ciphertext, _ := out.Data["ciphertext"].(string)
	version, err := TransitVersion(ciphertext)
	if err != nil {
		return TransitCiphertext{}, err
	}
	return TransitCiphertext{ciphertext, version}, nil
}
func (c *Client) TransitDecrypt(ctx context.Context, key, ciphertext string, aad []byte) ([]byte, error) {
	if !transitNamePattern.MatchString(key) {
		return nil, errors.New("invalid Transit key")
	}
	if _, err := TransitVersion(ciphertext); err != nil {
		return nil, err
	}
	var out dataResponse
	if err := c.request(ctx, http.MethodPost, "/v1/transit/decrypt/"+key, map[string]string{"ciphertext": ciphertext, "associated_data": base64.StdEncoding.EncodeToString(aad)}, &out); err != nil {
		return nil, errors.New("TRANSIT_DECRYPT_UNAVAILABLE")
	}
	encoded, _ := out.Data["plaintext"].(string)
	plain, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errors.New("invalid Transit plaintext response")
	}
	return plain, nil
}
func (c *Client) TransitSign(ctx context.Context, key string, message []byte) (TransitSignature, error) {
	if !transitNamePattern.MatchString(key) {
		return TransitSignature{}, errors.New("invalid Transit signing key")
	}
	var out dataResponse
	if err := c.request(ctx, http.MethodPost, "/v1/transit/sign/"+key, map[string]string{"input": base64.StdEncoding.EncodeToString(message)}, &out); err != nil {
		return TransitSignature{}, errors.New("TRANSIT_SIGN_UNAVAILABLE")
	}
	signature, _ := out.Data["signature"].(string)
	version, err := TransitVersion(signature)
	if err != nil {
		return TransitSignature{}, err
	}
	return TransitSignature{signature, version}, nil
}
func (c *Client) TransitVerify(ctx context.Context, key string, message []byte, signature string) error {
	if !transitNamePattern.MatchString(key) {
		return errors.New("invalid Transit signing key")
	}
	var out dataResponse
	if err := c.request(ctx, http.MethodPost, "/v1/transit/verify/"+key, map[string]string{"input": base64.StdEncoding.EncodeToString(message), "signature": signature}, &out); err != nil {
		return errors.New("TRANSIT_VERIFY_UNAVAILABLE")
	}
	valid, _ := out.Data["valid"].(bool)
	if !valid {
		return errors.New("TRANSIT_SIGNATURE_INVALID")
	}
	return nil
}
func (c *Client) ConfigureTransit(ctx context.Context) error {
	if err := c.ensureMount(ctx, "transit", "transit"); err != nil {
		return err
	}
	if err := c.ensureTransitKey(ctx); err != nil {
		return err
	}
	if err := c.ensureAuditSigningKey(ctx); err != nil {
		return err
	}
	return c.ConfigureInvocationSigning(ctx)
}

func (c *Client) ensureAuditSigningKey(ctx context.Context) error {
	path := "/v1/transit/keys/audit-signing"
	var out dataResponse
	if err := c.request(ctx, http.MethodGet, path, nil, &out); err != nil {
		var status *apiError
		if errors.As(err, &status) && status.isNotFound() {
			if err := c.request(ctx, http.MethodPost, path, map[string]any{"type": "ed25519", "exportable": false, "allow_plaintext_backup": false}, nil); err != nil {
				return err
			}
			return c.verifyAuditSigningKey(ctx)
		}
		return err
	}
	if out.Data["type"] != "ed25519" && out.Data["key_type"] != "ed25519" {
		return ErrConfigurationDrift
	}
	if out.Data["exportable"] == true || out.Data["allow_plaintext_backup"] == true {
		return ErrConfigurationDrift
	}
	return nil
}

func (c *Client) verifyAuditSigningKey(ctx context.Context) error {
	var out dataResponse
	if err := c.request(ctx, http.MethodGet, "/v1/transit/keys/audit-signing", nil, &out); err != nil {
		return err
	}
	if out.Data["type"] != "ed25519" && out.Data["key_type"] != "ed25519" {
		return ErrConfigurationDrift
	}
	if out.Data["exportable"] == true || out.Data["allow_plaintext_backup"] == true {
		return ErrConfigurationDrift
	}
	return nil
}
func (c *Client) TransitRotate(ctx context.Context, key string) error {
	if !transitNamePattern.MatchString(key) || strings.TrimSpace(c.token) == "" {
		return errors.New("authorized Transit key rotation is required")
	}
	return c.request(ctx, http.MethodPost, "/v1/transit/keys/"+key+"/rotate", map[string]any{}, nil)
}
