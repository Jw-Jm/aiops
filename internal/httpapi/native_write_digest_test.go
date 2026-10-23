package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestNativePasswordBindingDoesNotPersistFastPasswordVerifier(t *testing.T) {
	raw := []byte(`{"displayName":"Fixture","initialPassword":"fixture-password-only","loginName":"fixture"}`)
	a, err := nativeBoundWriteBody("fixture-csrf-key-0001", "POST", "/api/v1/admin/users", raw)
	if err != nil {
		t.Fatal(err)
	}
	b, err := nativeBoundWriteBody("fixture-csrf-key-0002", "POST", "/api/v1/admin/users", raw)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a, b) || bytes.Contains(a, []byte("fixture-password-only")) {
		t.Fatal("password request binding is not keyed by its session")
	}
	sum := sha256.Sum256(a)
	// Cross-language vector shared with native_write_digest.test.ts.
	if hex.EncodeToString(sum[:]) != "fc2b165f95a07f306fb21f5e74d14bcc50cfdb31f6b16429262f8e6f3dfae586" {
		t.Fatal("password binding drifted from the independent Web vector")
	}
	if _, err := nativeBoundWriteBody("", "POST", "/api/v1/admin/users", raw); err == nil {
		t.Fatal("password binding accepted a missing session secret")
	}
	reordered := []byte(`{"loginName":"fixture","initialPassword":"fixture-password-only","displayName":"Fixture"}`)
	again, err := nativeBoundWriteBody("fixture-csrf-key-0001", "POST", "/api/v1/admin/users", reordered)
	if err != nil || !bytes.Equal(a, again) {
		t.Fatal("JSON order changed password binding")
	}
}
