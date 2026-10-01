package s3

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"ops-platform/internal/archive"
)

func TestTenantIAMRefusesGlobalCredentialsAndUnknownTenant(t *testing.T) {
	tenant := uuid.New()
	prefix := archive.TenantPrefix(tenant)
	var broad atomic.Bool
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/xml")
		if r.URL.Query().Get("prefix") == prefix || broad.Load() {
			w.Write([]byte(`<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><IsTruncated>false</IsTruncated></ListBucketResult>`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`<Error><Code>AccessDenied</Code><Message>Access Denied</Message></Error>`))
	}))
	defer server.Close()
	config := Config{Endpoint: server.URL, Bucket: "archive", CACertBundle: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})}
	raw, _ := json.Marshal(TenantCredentials{SchemaVersion: "ops-archive-credentials/v1", Tenants: []TenantCredential{{TenantID: tenant, AccessKey: "tenant-principal", SecretKey: "fixture-secret"}}})
	client, err := NewTenantClient(config, raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.scoped(context.Background(), prefix+"evidence/test"); err != nil {
		t.Fatal(err)
	}
	before := requests.Load()
	if _, err := client.scoped(context.Background(), archive.TenantPrefix(uuid.New())+"evidence/test"); !errors.Is(err, ErrTenantIAM) || requests.Load() != before {
		t.Fatal("unknown tenant used a fallback credential")
	}
	broad.Store(true)
	client, err = NewTenantClient(config, raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.scoped(context.Background(), prefix+"evidence/test"); !errors.Is(err, ErrTenantIAM) {
		t.Fatal("global S3 principal passed tenant IAM gate")
	}
}

func TestTenantCredentialContractRejectsAmbiguity(t *testing.T) {
	id := uuid.NewString()
	valid := `{"schemaVersion":"ops-archive-credentials/v1","tenants":[{"tenantId":"` + id + `","accessKey":"a","secretKey":"s"}]}`
	for _, raw := range []string{strings.Replace(valid, `"schemaVersion":`, `"schemaVersion":"duplicate","schemaVersion":`, 1), valid + `{}`, strings.Replace(valid, `"secretKey":"s"`, `"secretKey":"s","global":true`, 1), strings.Replace(valid, `}]}`, `},{"tenantId":"`+id+`","accessKey":"b","secretKey":"s"}]}`, 1)} {
		if _, err := NewTenantClient(Config{Endpoint: "http://localhost:1234", Bucket: "archive"}, []byte(raw)); !errors.Is(err, ErrTenantIAM) {
			t.Fatal("ambiguous credential map accepted")
		}
	}
}
