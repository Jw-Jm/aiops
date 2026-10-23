package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"ops-platform/internal/bundle"
	"strings"
)

func nativePasswordWrite(method, path string) bool {
	return method == http.MethodPost && (path == "/api/v1/admin/users" || strings.HasPrefix(path, "/api/v1/admin/users/") && strings.HasSuffix(path, "/password:reset"))
}

// A persisted request digest must not become a fast offline password verifier.
// The verified session CSRF secret is never stored in plaintext in the DB.
func nativeBoundWriteBody(csrf, method, path string, body []byte) ([]byte, error) {
	if len(body) == 0 {
		return nil, nil
	}
	canonical, err := bundle.CanonicalizeJSON(body)
	if err != nil {
		return nil, err
	}
	if !nativePasswordWrite(method, path) {
		return canonical, nil
	}
	if len(csrf) < 16 {
		return nil, errors.New("verified session CSRF secret required")
	}
	var value map[string]any
	if err := json.Unmarshal(canonical, &value); err != nil {
		return nil, err
	}
	if password, ok := value["initialPassword"].(string); ok {
		mac := hmac.New(sha256.New, []byte(csrf))
		mac.Write([]byte(password))
		value["initialPassword"] = "hmac-sha256:" + hex.EncodeToString(mac.Sum(nil))
	}
	body, err = json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return bundle.CanonicalizeJSON(body)
}
