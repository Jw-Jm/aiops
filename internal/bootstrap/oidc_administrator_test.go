package bootstrap

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestOIDCAdministratorCannotRetireWithoutPositiveScopedControl(t *testing.T) {
	c := OIDCAdministrator{SchemaVersion: 1, TenantID: uuid.New(), Subject: uuid.New(), Username: "named-initial-admin"}
	for _, failure := range []string{"wrong-tenant", "not-enrolled", "ops-forbidden", "master-allowed", "master-unavailable"} {
		t.Run(failure, func(t *testing.T) {
			mutated := false
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					mutated = true
					w.WriteHeader(500)
					return
				}
				switch r.URL.Path {
				case "/admin/realms/ops/users/" + c.Subject.String():
					tenant := c.TenantID.String()
					if failure == "wrong-tenant" {
						tenant = uuid.NewString()
					}
					json.NewEncoder(w).Encode(map[string]any{"id": c.Subject.String(), "username": c.Username, "enabled": true, "totp": failure != "not-enrolled", "attributes": map[string]any{"tenant_ids": []string{tenant}}})
				case "/admin/realms/ops":
					if failure == "ops-forbidden" {
						w.WriteHeader(403)
					} else {
						w.WriteHeader(200)
					}
				case "/admin/realms/master":
					if failure == "master-allowed" {
						w.WriteHeader(200)
					} else if failure == "master-unavailable" {
						w.WriteHeader(503)
					} else {
						w.WriteHeader(403)
					}
				default:
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			_, err := RetireOIDCAdministrator(context.Background(), server.Client(), server.URL, c, OIDCPrivateInput{}, "verified-named-token", func() error { mutated = true; return nil })
			if err == nil || mutated {
				t.Fatal("retirement mutated before named identity and independent scoped controls passed")
			}
		})
	}
}

func TestOIDCTemporaryRefusalRequiresActualInvalidGrant(t *testing.T) {
	for _, status := range []int{401, 403, 404, 503} {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			w.Write([]byte(`{"error":"service_unavailable"}`))
		}))
		_, err := bootstrapAdministration(context.Background(), server.Client(), server.URL, OIDCPrivateInput{})
		server.Close()
		if err == nil || err == ErrOIDCTemporaryRejected {
			t.Fatal("unavailable service confused with real old credential refusal")
		}
	}
}
