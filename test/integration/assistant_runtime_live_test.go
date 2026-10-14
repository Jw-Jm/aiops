package integration

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"ops-platform/internal/assistant"
	"ops-platform/internal/auth"
	"ops-platform/internal/configregistry"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/modelprofile"
	"ops-platform/internal/observability"
	"ops-platform/internal/source"
)

// Actual OIDC/HTTP/API/Worker/model/MCP runtime. Model answer quality is explicitly
// excluded by ADR-0039; schema/reference/fencing and authorization remain required.
func TestAssistantActualOIDCAndBrowserRuntime(t *testing.T) {
	if os.Getenv("OPS_ASSISTANT_RUNTIME_ACCEPTANCE") != "1" {
		t.Skip("owned remote OIDC/browser runtime required")
	}
	cfg, _, _, _ := readAssistantLiveSource(t)
	if os.Getenv("OPS_ASSISTANT_DEEPFLOW_GRAPH_ONLY") == "1" {
		cfg.Namespace = "ops-assistant-deepflow-20261013"
	}
	private := os.Getenv("OPS_ASSISTANT_RUNTIME_PRIVATE")
	if private == "" {
		t.Fatal("private owned coordination directory required")
	}
	var identities struct {
		Operator struct {
			Subject string `json:"subject"`
		} `json:"operator"`
		Admin struct {
			Subject string `json:"subject"`
		} `json:"admin"`
	}
	raw, err := os.ReadFile(filepath.Join(private, "identities.json"))
	if err != nil || json.Unmarshal(raw, &identities) != nil {
		t.Fatal("private identity config unavailable")
	}
	assistantRealRuntime(t, true, func(ctx context.Context, db *sql.DB, pool *pgxpool.Pool, b source.BoundSourceContext, repo assistant.Repository, profile modelprofile.Profile, _ *assistantEvaluationTrace, sp04 *httpapi.SP04Handlers) {
		scopes, _ := json.Marshal([]map[string]string{{"clusterId": b.ClusterID.String(), "namespace": cfg.Namespace}})
		for role, subject := range map[string]string{"operator": identities.Operator.Subject, "platform_admin": identities.Admin.Subject} {
			if _, err := db.ExecContext(ctx, `INSERT INTO platform.role_bindings(tenant_id,binding_id,subject,role_name,cluster_scopes,namespace_scopes) VALUES($1,$2,$3,$4,$5,$6)`, b.TenantID, uuid.New(), subject, role, `["`+b.ClusterID.String()+`"]`, scopes); err != nil {
				t.Fatal(err)
			}
		}
		ca, err := os.ReadFile("/data/ops-sp08-20261011/private/web.crt")
		if err != nil {
			t.Fatal(err)
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			roots = x509.NewCertPool()
		}
		roots.AppendCertsFromPEM(ca)
		client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}, Timeout: 10 * time.Second}
		oidcCtx := oidc.ClientContext(ctx, client)
		bindings, err := auth.NewPostgreSQLRoleBindingSource(repo.Pool)
		if err != nil {
			t.Fatal(err)
		}
		authenticator, err := auth.NewOIDCAuthenticator(oidcCtx, "https://192.168.0.63:18643/oidc/realms/ops-assistant-20261013", "ops-api", bindings)
		if err != nil {
			t.Fatal("actual OIDC discovery", err)
		}
		runtime, err := observability.NewRuntime(ctx, "assistant-actual-oidc")
		if err != nil {
			t.Fatal(err)
		}
		defer runtime.Close(context.Background())
		// Signing trust is already frozen by SP06; Foundation's unrelated publication
		// routes remain disabled without the private fixture publication key.
		trust := configregistry.Ed25519TrustStore{Keys: map[string]ed25519.PublicKey{}}
		handler, err := httpapi.NewFoundationHandlerWithSP04(repo.Pool, authenticator, trust, runtime, sp04)
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewServer(handler)
		defer server.Close()
		defer server.CloseClientConnections()
		runtimeID := uuid.NewString()
		raw, _ := json.Marshal(map[string]any{"runtimeId": runtimeID, "address": server.URL, "tenantId": b.TenantID, "clusterId": b.ClusterID, "clusterUid": cfg.ClusterUID, "namespace": cfg.Namespace, "profileReference": profile.Reference(), "modelQualityAcceptance": false})
		path := filepath.Join(private, "api-address.json")
		if err = os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(path)
		t.Log("actual OIDC/API ready; waiting for isolated browser receipt")
		receipt := filepath.Join(private, "browser-receipt.json")
		for ctx.Err() == nil {
			raw, err := os.ReadFile(receipt)
			if err == nil {
				var result struct {
					Passed       bool   `json:"passed"`
					RuntimeID    string `json:"runtimeId"`
					Profile      string `json:"profileReference"`
					ModelQuality bool   `json:"modelQualityAcceptance"`
				}
				if json.Unmarshal(raw, &result) != nil || !result.Passed || result.RuntimeID != runtimeID || result.Profile != profile.Reference() || result.ModelQuality {
					t.Fatal("browser receipt failed or belongs to another runtime")
				}
				t.Log("actual PKCE OIDC + current DB role bindings + API/Worker + persistent turn/SSE/browser receipt passed; no model quality criterion")
				return
			}
			time.Sleep(time.Second)
		}
		t.Fatal("browser did not complete before owned runtime deadline")
	})
}
