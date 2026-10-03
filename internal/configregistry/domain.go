package configregistry

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/bundle"
	"ops-platform/internal/contract"
)

type Kind string

const (
	KindPolicy Kind = "policy"
	KindRecipe Kind = "recipe"
	KindTool   Kind = "tool"
)

type ScopeType string

const (
	ScopeTenant    ScopeType = "tenant"
	ScopeCluster   ScopeType = "cluster"
	ScopeNamespace ScopeType = "namespace"
)

var (
	ErrUnauthorized              = errors.New("configuration registry administrator is not authorized")
	ErrInvalidInput              = errors.New("configuration registry input is invalid")
	ErrNotFound                  = errors.New("configuration registry resource was not found")
	ErrRevisionConflict          = errors.New("configuration registry revision is stale")
	ErrScopeConflict             = errors.New("configuration registry scope is already activated")
	ErrImmutable                 = errors.New("published configuration is immutable")
	ErrInvalidSignature          = errors.New("configuration publication signature is invalid")
	ErrPolicyCompilerUnavailable = errors.New("policy bundle compiler is unavailable")
	ErrVersionActive             = errors.New("an active configuration version cannot be retired")
	ErrStateConflict             = errors.New("configuration registry state conflicts with this operation")
	logicalNamePattern           = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,199}$`)
	namespacePattern             = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
)

type Scope struct {
	Type      ScopeType `json:"type"`
	ClusterID uuid.UUID `json:"clusterId,omitempty"`
	Namespace string    `json:"namespace,omitempty"`
}

func (scope Scope) Validate() error {
	switch scope.Type {
	case ScopeTenant:
		if scope.ClusterID != uuid.Nil || scope.Namespace != "" {
			return ErrInvalidInput
		}
	case ScopeCluster:
		if scope.ClusterID == uuid.Nil || scope.Namespace != "" {
			return ErrInvalidInput
		}
	case ScopeNamespace:
		if scope.ClusterID == uuid.Nil || !namespacePattern.MatchString(scope.Namespace) {
			return ErrInvalidInput
		}
	default:
		return ErrInvalidInput
	}
	return nil
}

func (scope Scope) key(tenantID uuid.UUID, kind Kind, logicalName string) string {
	return strings.Join([]string{tenantID.String(), string(kind), logicalName, string(scope.Type), scope.ClusterID.String(), scope.Namespace}, "|")
}

type DraftRef struct {
	TenantID uuid.UUID
	DraftID  uuid.UUID
}

type VersionRef struct {
	TenantID  uuid.UUID
	VersionID uuid.UUID
}

type DraftCommand struct {
	Kind        Kind
	LogicalName string
	Content     json.RawMessage
}

type DraftUpdateCommand struct {
	ExpectedRevision int64
	Content          json.RawMessage
}

type Draft struct {
	TenantID    uuid.UUID       `json:"tenantId"`
	DraftID     uuid.UUID       `json:"draftId"`
	Kind        Kind            `json:"kind"`
	LogicalName string          `json:"logicalName"`
	Revision    int64           `json:"revision"`
	Content     json.RawMessage `json:"content"`
	Status      string          `json:"status"`
	UpdatedBy   string          `json:"updatedBy"`
	CreatedAt   time.Time       `json:"createdAt"`
	UpdatedAt   time.Time       `json:"updatedAt"`
}

type PublicationSignature struct {
	KeyID     string
	Signature []byte
}

type PublishedVersion struct {
	TenantID      uuid.UUID       `json:"tenantId"`
	VersionID     uuid.UUID       `json:"versionId"`
	DraftID       uuid.UUID       `json:"draftId"`
	Kind          Kind            `json:"kind"`
	LogicalName   string          `json:"logicalName"`
	VersionNumber int32           `json:"versionNumber"`
	Content       json.RawMessage `json:"content"`
	Digest        string          `json:"digest"`
	Signature     []byte          `json:"signature"`
	SignerKeyID   string          `json:"signerKeyId"`
	PublishedBy   string          `json:"publishedBy"`
	PublishedAt   time.Time       `json:"publishedAt"`
	RetiredAt     *time.Time      `json:"retiredAt,omitempty"`
}

type Activation struct {
	TenantID     uuid.UUID `json:"tenantId"`
	ActivationID uuid.UUID `json:"activationId"`
	Kind         Kind      `json:"kind"`
	LogicalName  string    `json:"logicalName"`
	Scope        Scope     `json:"scope"`
	VersionID    uuid.UUID `json:"versionId"`
	Revision     int64     `json:"revision"`
	ActivatedBy  string    `json:"activatedBy"`
	ActivatedAt  time.Time `json:"activatedAt"`
}

type SignatureVerifier interface {
	Verify(context.Context, string, []byte, []byte) error
}

// PolicyPublicationValidator compiles signed policy content before it can be
// inserted as an immutable published version.
type PolicyPublicationValidator interface {
	ValidatePolicyPublication(context.Context, json.RawMessage) error
}

type Ed25519TrustStore struct {
	Keys map[string]ed25519.PublicKey
}

func (trust Ed25519TrustStore) Verify(ctx context.Context, keyID string, message, signature []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	key, ok := trust.Keys[keyID]
	if !ok || len(key) != ed25519.PublicKeySize || len(signature) != ed25519.SignatureSize || !ed25519.Verify(key, message, signature) {
		return ErrInvalidSignature
	}
	return nil
}

func ValidateContent(kind Kind, content json.RawMessage) error {
	schemaID, ok := schemaForContent(kind, content)
	if !ok {
		return ErrInvalidInput
	}
	canonical, err := bundle.CanonicalizeJSON(content)
	if err != nil {
		return fmt.Errorf("canonicalize registry content: %w", err)
	}
	if err := contract.Validate(schemaID, canonical); err != nil {
		return fmt.Errorf("validate %s registry content: %w", kind, err)
	}
	return nil
}

func SigningPayload(tenantID uuid.UUID, kind Kind, logicalName string, content json.RawMessage) ([]byte, string, error) {
	if tenantID == uuid.Nil || !validKind(kind) || !logicalNamePattern.MatchString(logicalName) {
		return nil, "", ErrInvalidInput
	}
	canonical, err := bundle.CanonicalizeJSON(content)
	if err != nil {
		return nil, "", fmt.Errorf("canonicalize registry content: %w", err)
	}
	digestBytes := sha256.Sum256(canonical)
	digest := "sha256:" + hex.EncodeToString(digestBytes[:])
	schema, _ := schemaForContent(kind, content)
	publication := struct {
		Domain      string `json:"domain"`
		TenantID    string `json:"tenantId"`
		Kind        Kind   `json:"kind"`
		LogicalName string `json:"logicalName"`
		SchemaID    string `json:"schemaId"`
		Digest      string `json:"digest"`
	}{"ops-config-registry/v1", tenantID.String(), kind, logicalName, schema, digest}
	encoded, err := json.Marshal(publication)
	if err != nil {
		return nil, "", err
	}
	message, err := bundle.CanonicalizeJSON(encoded)
	if err != nil {
		return nil, "", err
	}
	return message, digest, nil
}

func schemaID(kind Kind) (string, bool) {
	switch kind {
	case KindPolicy:
		return "https://ops.local/schemas/policy-registry/v1", true
	case KindRecipe:
		return "https://ops.local/schemas/recipe-registry/v1", true
	case KindTool:
		return "https://ops.local/schemas/tool-registry/v1", true
	default:
		return "", false
	}
}

func validKind(kind Kind) bool {
	_, ok := schemaID(kind)
	return ok
}

func validLogicalName(value string) bool {
	return logicalNamePattern.MatchString(value)
}

func schemaForContent(kind Kind, content json.RawMessage) (string, bool) {
	var header struct {
		SchemaVersion string `json:"schemaVersion"`
	}
	if json.Unmarshal(content, &header) != nil {
		return "", false
	}
	if kind == KindRecipe && header.SchemaVersion == "recipe-registry/v2" {
		return "https://ops.local/schemas/recipe-registry/v2", true
	}
	return schemaID(kind)
}
