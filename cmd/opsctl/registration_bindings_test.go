package main

import (
	"encoding/base64"
	"net/http"
	"ops-platform/internal/auth"
	"strings"
	"testing"
)

func TestRegistrationCredentialUsesExplicitNativeSessionOrLegacyBearer(t *testing.T) {
	token := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("a", 32)))
	headers, err := registrationAuthHeaders([]byte(token+"\n"), true)
	if err != nil {
		t.Fatal(err)
	}
	r := &http.Request{Header: headers}
	cookie, err := r.Cookie(auth.NativeSessionCookieName)
	if err != nil || cookie.Value != token || headers.Get("Authorization") != "" {
		t.Fatal("native source verification did not use only its session cookie")
	}
	headers, err = registrationAuthHeaders([]byte("reviewed-legacy-token"), false)
	if err != nil || headers.Get("Authorization") != "Bearer reviewed-legacy-token" || headers.Get("Cookie") != "" {
		t.Fatal("legacy bearer compatibility changed")
	}
	for _, bad := range []string{"", "Bearer " + token, token + "; other=x", "a.b.c", token + "\nsecond", strings.Repeat("a", 44)} {
		if _, err := registrationAuthHeaders([]byte(bad), true); err == nil {
			t.Fatal("malformed native credential accepted")
		}
	}
}
