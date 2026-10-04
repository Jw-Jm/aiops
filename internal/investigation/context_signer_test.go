package investigation

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"github.com/google/uuid"
	"ops-platform/internal/graph"
	"testing"
	"time"
)

type fixtureTransit struct{ key *ecdsa.PrivateKey }

func (f fixtureTransit) SigningKeys(context.Context, string) (map[int]*ecdsa.PublicKey, int, error) {
	return map[int]*ecdsa.PublicKey{1: &f.key.PublicKey}, 1, nil
}
func (f fixtureTransit) SignJWS(_ context.Context, _ string, _ int, b []byte) ([]byte, error) {
	d := sha256.Sum256(b)
	r, s, e := ecdsa.Sign(rand.Reader, f.key, d[:])
	if e != nil {
		return nil, e
	}
	out := make([]byte, 64)
	r.FillBytes(out[:32])
	s.FillBytes(out[32:])
	return out, nil
}
func TestInvocationSignatureBoundaryAndExpiry(t *testing.T) {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	signer := ContextSigner{Transit: fixtureTransit{k}, Key: "investigation-signing"}
	now := time.Now().UTC().Truncate(time.Second)
	c := InvocationClaims{SchemaVersion: "invocation-context/v2", ContextID: uuid.New(), Issuer: "platform-api", Audience: "platform-mcp-gateway", JobID: uuid.New(), IncidentID: uuid.New(), TenantID: uuid.New(), Subject: "operator", Scope: graph.Scope{Cluster: "c", ClusterScoped: true}, AllowedTools: []string{"get_incident_context"}, AllowedDataClasses: []string{"D0", "D1"}, PolicyVersion: "p/v1", ToolCatalogDigest: "sha256:c", AuthorizationRevision: "sha256:a", LeaseGeneration: 1, SessionNonce: uuid.NewString(), IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Minute).Unix(), WorkloadIdentity: "spiffe://ops.local/ns/test/sa/investigator"}
	c.Scope.Tenant = c.TenantID.String()
	c.Scope.AuthorizationRevision = c.AuthorizationRevision
	token, e := signer.Sign(context.Background(), c)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = signer.VerifyContext(context.Background(), token, c.Audience, c.WorkloadIdentity); e != nil {
		t.Fatal(e)
	}
	for _, pair := range [][2]string{{"other", c.WorkloadIdentity}, {c.Audience, "other"}} {
		if _, e = signer.VerifyContext(context.Background(), token, pair[0], pair[1]); e == nil {
			t.Fatal("audience or workload accepted")
		}
	}
	if _, e = signer.VerifyContext(context.Background(), base64.RawURLEncoding.EncodeToString([]byte("{}"))+token, c.Audience, c.WorkloadIdentity); e == nil {
		t.Fatal("tampering accepted")
	}
	c.IssuedAt = now.Add(time.Minute).Unix()
	if _, e = signer.Sign(context.Background(), c); e == nil {
		t.Fatal("future iat accepted")
	}
	c.IssuedAt = now.Unix()
	c.ExpiresAt = now.Unix()
	if _, e = signer.Sign(context.Background(), c); e == nil {
		t.Fatal("expired accepted")
	}
}
