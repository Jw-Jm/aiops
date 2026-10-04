package openbao

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

func (c *Client) ConfigureInvocationSigning(ctx context.Context) error {
	const path = "/v1/transit/keys/investigation-signing"
	var out dataResponse
	if err := c.request(ctx, http.MethodGet, path, nil, &out); err != nil {
		var status *apiError
		if !errors.As(err, &status) || !status.isNotFound() {
			return err
		}
		if err = c.request(ctx, http.MethodPost, path, map[string]any{"type": "ecdsa-p256", "exportable": false, "allow_plaintext_backup": false}, nil); err != nil {
			return err
		}
	}
	_, _, err := c.SigningKeys(ctx, "investigation-signing")
	return err
}
func (c *Client) SigningKeys(ctx context.Context, key string) (map[int]*ecdsa.PublicKey, int, error) {
	if !transitNamePattern.MatchString(key) {
		return nil, 0, ErrConfigurationDrift
	}
	var out dataResponse
	if err := c.request(ctx, http.MethodGet, "/v1/transit/keys/"+key, nil, &out); err != nil {
		return nil, 0, errors.New("INVOCATION_KEYS_UNAVAILABLE")
	}
	if out.Data["type"] != "ecdsa-p256" || out.Data["exportable"] == true || out.Data["allow_plaintext_backup"] == true {
		return nil, 0, ErrConfigurationDrift
	}
	versions, ok := out.Data["keys"].(map[string]any)
	if !ok {
		return nil, 0, ErrConfigurationDrift
	}
	minVersion := 1
	if v, ok := out.Data["min_decryption_version"].(float64); ok && v > 0 {
		minVersion = int(v)
	}
	keys := map[int]*ecdsa.PublicKey{}
	latest := 0
	for v, value := range versions {
		n, e := strconv.Atoi(v)
		entry, ok := value.(map[string]any)
		if e != nil || !ok {
			return nil, 0, ErrConfigurationDrift
		}
		public, _ := entry["public_key"].(string)
		block, _ := pem.Decode([]byte(public))
		if block == nil {
			return nil, 0, ErrConfigurationDrift
		}
		k, e := x509.ParsePKIXPublicKey(block.Bytes)
		if e != nil {
			return nil, 0, ErrConfigurationDrift
		}
		ec, ok := k.(*ecdsa.PublicKey)
		if !ok || ec.Curve.Params().BitSize != 256 {
			return nil, 0, ErrConfigurationDrift
		}
		if n >= minVersion {
			keys[n] = ec
		}
		if n > latest {
			latest = n
		}
	}
	if latest < 1 {
		return nil, 0, ErrConfigurationDrift
	}
	return keys, latest, nil
}
func (c *Client) SignJWS(ctx context.Context, key string, version int, message []byte) ([]byte, error) {
	if !transitNamePattern.MatchString(key) || version < 1 || len(message) > 16<<10 {
		return nil, ErrConfigurationDrift
	}
	var out dataResponse
	if err := c.request(ctx, http.MethodPost, "/v1/transit/sign/"+key, map[string]any{"input": base64.StdEncoding.EncodeToString(message), "key_version": version, "marshaling_algorithm": "jws", "hash_algorithm": "sha2-256"}, &out); err != nil {
		return nil, errors.New("INVOCATION_SIGN_UNAVAILABLE")
	}
	sig, _ := out.Data["signature"].(string)
	parts := strings.Split(sig, ":")
	if len(parts) != 3 || parts[0] != "vault" || parts[1] != "v"+strconv.Itoa(version) {
		return nil, ErrConfigurationDrift
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(b) != 64 {
		return nil, ErrConfigurationDrift
	}
	return b, nil
}
