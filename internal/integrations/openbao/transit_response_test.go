package openbao

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTransitLargeResponseAndOrdinaryResponseBoundaries(t *testing.T) {
	plain := bytes.Repeat([]byte("x"), 2<<20)
	encoded := base64.StdEncoding.EncodeToString(plain)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value := map[string]any{"data": map[string]any{"plaintext": encoded}}
		if r.URL.Path == "/v1/transit/encrypt/command-data" {
			value = map[string]any{"data": map[string]any{"ciphertext": "vault:v1:" + encoded}}
		}
		_ = json.NewEncoder(w).Encode(value)
	}))
	defer server.Close()
	c := &Client{address: server.URL, http: server.Client()}
	encrypted, err := c.TransitEncrypt(context.Background(), "command-data", plain, nil)
	if err != nil {
		t.Fatalf("valid existing Transit input cannot archive: %v", err)
	}
	recovered, err := c.TransitDecrypt(context.Background(), "command-data", encrypted.Ciphertext, nil)
	if err != nil || !bytes.Equal(recovered, plain) {
		t.Fatalf("large Transit round trip: %v", err)
	}
	var out dataResponse
	if err := c.request(context.Background(), http.MethodGet, "/v1/sys/health", nil, &out); err == nil {
		t.Fatal("ordinary response must keep its existing 1 MiB boundary")
	}
}
