package investigation

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"io"
	"math/big"
	"ops-platform/internal/graph"
	"slices"
	"strconv"
	"strings"
	"time"
)

// TransitSigning is implemented by OpenBao. Runtime keys never leave Transit.
type TransitSigning interface {
	SigningKeys(context.Context, string) (map[int]*ecdsa.PublicKey, int, error)
	SignJWS(context.Context, string, int, []byte) ([]byte, error)
}
type ContextSigner struct {
	Transit TransitSigning
	Key     string
}
type InvocationClaims struct {
	SchemaVersion         string      `json:"schemaVersion"`
	ContextID             uuid.UUID   `json:"context_id"`
	Issuer                string      `json:"iss"`
	Audience              string      `json:"aud"`
	JobID                 uuid.UUID   `json:"job_id"`
	IncidentID            uuid.UUID   `json:"incident_id"`
	TenantID              uuid.UUID   `json:"tenant_id"`
	Subject               string      `json:"subject"`
	Scope                 graph.Scope `json:"scope"`
	AllowedTools          []string    `json:"allowed_tools"`
	AllowedDataClasses    []string    `json:"allowed_data_classes"`
	PolicyVersion         string      `json:"policy_version"`
	AuthorizationRevision string      `json:"authorizationRevision"`
	ToolCatalogDigest     string      `json:"toolCatalogDigest"`
	LeaseGeneration       int64       `json:"jobLeaseGeneration"`
	IssuedAt              int64       `json:"iat"`
	ExpiresAt             int64       `json:"exp"`
	SessionNonce          string      `json:"session_nonce"`
	WorkloadIdentity      string      `json:"-"`
	Confirmation          struct {
		WorkloadIdentity string `json:"workload_identity"`
	} `json:"cnf"`
}

func (c InvocationClaims) identity() string {
	if c.Confirmation.WorkloadIdentity != "" {
		return c.Confirmation.WorkloadIdentity
	}
	return c.WorkloadIdentity
}
func (c InvocationClaims) Valid(now time.Time) bool {
	if c.SchemaVersion != "invocation-context/v2" || c.Issuer != "platform-api" || (c.Audience != "platform-mcp-gateway" && c.Audience != "platform-job-api") || c.ContextID == uuid.Nil || c.JobID == uuid.Nil || c.IncidentID == uuid.Nil || c.TenantID == uuid.Nil || c.Subject == "" || c.Scope.Tenant != c.TenantID.String() || c.Scope.Cluster == "" || (!c.Scope.ClusterScoped && len(c.Scope.Namespaces) == 0) || c.AuthorizationRevision == "" || c.Scope.AuthorizationRevision != c.AuthorizationRevision || c.PolicyVersion == "" || c.ToolCatalogDigest == "" || c.LeaseGeneration < 1 || c.SessionNonce == "" || c.identity() == "" || c.IssuedAt > now.Unix() || c.ExpiresAt <= now.Unix() || c.ExpiresAt <= c.IssuedAt || c.ExpiresAt-c.IssuedAt > 120 || len(c.AllowedTools) == 0 || len(c.AllowedTools) > 64 || len(c.AllowedDataClasses) == 0 {
		return false
	}
	seenTools := map[string]bool{}
	for _, name := range c.AllowedTools {
		if name == "" || len(name) > 128 || seenTools[name] {
			return false
		}
		seenTools[name] = true
	}
	seenClasses := map[string]bool{}
	for _, v := range c.AllowedDataClasses {
		if (v != "D0" && v != "D1") || seenClasses[v] {
			return false
		}
		seenClasses[v] = true
	}
	return true
}
func (s ContextSigner) Sign(ctx context.Context, c InvocationClaims) (string, error) {
	if s.Transit == nil || s.Key == "" || !c.Valid(time.Now()) {
		return "", ErrInvalid
	}
	c.Confirmation.WorkloadIdentity = c.identity()
	_, version, err := s.Transit.SigningKeys(ctx, s.Key)
	if err != nil {
		return "", err
	}
	header := base64.RawURLEncoding.EncodeToString(raw(map[string]any{"alg": "ES256", "kid": s.Key + "-v" + strconv.Itoa(version), "typ": "JWT"}))
	input := header + "." + base64.RawURLEncoding.EncodeToString(raw(c))
	sig, err := s.Transit.SignJWS(ctx, s.Key, version, []byte(input))
	if err != nil || len(sig) != 64 {
		return "", errors.New("INVOCATION_SIGN_UNAVAILABLE")
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}
func strictJSON(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return ErrInvalid
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return ErrInvalid
	}
	return nil
}

// VerifyContext authenticates only. It never consumes nonce or reserves budget.
// BeginCall owns those changes in one shared PostgreSQL transaction.
func (s ContextSigner) VerifyContext(ctx context.Context, token, audience, workload string) (InvocationClaims, error) {
	var c InvocationClaims
	if len(token) > 16<<10 || s.Transit == nil {
		return c, ErrDenied
	}
	p := strings.Split(token, ".")
	if len(p) != 3 {
		return c, ErrDenied
	}
	h, err := base64.RawURLEncoding.DecodeString(p[0])
	if err != nil {
		return c, ErrDenied
	}
	var header struct {
		Alg  string `json:"alg"`
		KID  string `json:"kid"`
		Type string `json:"typ"`
	}
	if strictJSON(h, &header) != nil || header.Alg != "ES256" || header.Type != "JWT" || !strings.HasPrefix(header.KID, s.Key+"-v") {
		return c, ErrDenied
	}
	version, err := strconv.Atoi(strings.TrimPrefix(header.KID, s.Key+"-v"))
	if err != nil || version < 1 {
		return c, ErrDenied
	}
	keys, _, err := s.Transit.SigningKeys(ctx, s.Key)
	if err != nil {
		return c, ErrDenied
	}
	key := keys[version]
	if key == nil || key.Curve.Params().BitSize != 256 {
		return c, ErrDenied
	}
	sig, err := base64.RawURLEncoding.DecodeString(p[2])
	if err != nil || len(sig) != 64 {
		return c, ErrDenied
	}
	digest := sha256.Sum256([]byte(p[0] + "." + p[1]))
	if !ecdsa.Verify(key, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		return c, ErrDenied
	}
	b, err := base64.RawURLEncoding.DecodeString(p[1])
	if err != nil || strictJSON(b, &c) != nil || !c.Valid(time.Now()) || c.Audience != audience || c.identity() != workload {
		return InvocationClaims{}, ErrDenied
	}
	return c, nil
}
func (c InvocationClaims) Bind(j Job, l Lease, catalog string, tool string) error {
	if c.JobID != j.JobID || c.TenantID != j.TenantID || c.IncidentID != j.IncidentID || c.Subject != j.Subject || c.LeaseGeneration != l.Generation || c.PolicyVersion != j.PolicyVersion || c.ToolCatalogDigest != catalog || catalog != j.ToolCatalogDigest || graph.ScopeDigest(c.Scope) != j.EffectiveScopeDigest || c.AuthorizationRevision != j.Scope.AuthorizationRevision {
		return ErrLease
	}
	for _, class := range c.AllowedDataClasses {
		if !slices.Contains(j.Budget.AllowedDataClasses, class) {
			return ErrDenied
		}
	}
	if tool != "" && !slices.Contains(c.AllowedTools, tool) {
		return ErrDenied
	}
	return nil
}
func (s ContextSigner) Issue(ctx context.Context, j Job, l Lease, audience, workload string, tools []string) (string, error) {
	now := time.Now().UTC()
	expires := now.Add(time.Minute)
	if j.ExpiresAt.Before(expires) {
		expires = j.ExpiresAt
	}
	c := InvocationClaims{SchemaVersion: "invocation-context/v2", ContextID: uuid.New(), Issuer: "platform-api", Audience: audience, JobID: j.JobID, IncidentID: j.IncidentID, TenantID: j.TenantID, Subject: j.Subject, Scope: j.Scope, AllowedTools: tools, AllowedDataClasses: j.Budget.AllowedDataClasses, PolicyVersion: j.PolicyVersion, AuthorizationRevision: j.Scope.AuthorizationRevision, ToolCatalogDigest: j.ToolCatalogDigest, LeaseGeneration: l.Generation, IssuedAt: now.Unix(), ExpiresAt: expires.Unix(), SessionNonce: uuid.NewString(), WorkloadIdentity: workload}
	if l.JobID != j.JobID || l.TenantID != j.TenantID {
		return "", fmt.Errorf("%w: lease binding", ErrInvalid)
	}
	return s.Sign(ctx, c)
}

// IssueRegistered persists the exact verified claims before releasing a Context.
func (s ContextSigner) IssueRegistered(ctx context.Context, r Repository, j Job, l Lease, audience, workload string, tools []string) (string, error) {
	token, err := s.Issue(ctx, j, l, audience, workload, tools)
	if err != nil {
		return "", err
	}
	claims, err := s.VerifyContext(ctx, token, audience, workload)
	if err != nil {
		return "", err
	}
	if err = r.RegisterContext(ctx, l, claims); err != nil {
		return "", err
	}
	return token, nil
}
