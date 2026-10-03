package source

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrUnauthorized     = errors.New("source identity is not authorized")
	ErrInvalidInput     = errors.New("invalid source or cluster registration input")
	ErrRevisionConflict = errors.New("source registration revision changed")
	ErrResourceNotFound = errors.New("source or cluster registration was not found")
	ErrIdentityConflict = errors.New("registered source or cluster identity conflicts with the requested identity")
	allowedSourceTypes  = map[string]struct{}{
		"victoriametrics": {}, "victorialogs": {}, "deepflow": {}, "kubernetes": {},
		"redfish": {}, "ipmi": {}, "smart": {}, "object_storage": {}, "model": {},
	}
)

type SourceRegistration struct {
	TenantID           uuid.UUID        `json:"tenantId"`
	SourceID           uuid.UUID        `json:"sourceId"`
	SourceType         string           `json:"sourceType"`
	InstanceKey        string           `json:"instanceKey"`
	ClusterID          uuid.UUID        `json:"clusterId,omitempty"`
	ClusterUID         string           `json:"clusterUid,omitempty"`
	AuthRef            string           `json:"authRef"`
	AllowedSchemas     []string         `json:"allowedSchemas"`
	BackendLogicalID   string           `json:"backendLogicalId"`
	DataScopeMapping   DataScopeMapping `json:"dataScopeMapping"`
	CredentialRevision int64            `json:"credentialRevision"`
	Status             string           `json:"status"`
	Revision           int64            `json:"revision"`
	CreatedAt          time.Time        `json:"createdAt"`
	UpdatedAt          time.Time        `json:"updatedAt"`
}

type RegisterCommand struct {
	SourceType       string
	InstanceKey      string
	ClusterID        *uuid.UUID
	AuthRef          string
	AllowedSchemas   []string
	BackendLogicalID string
	DataScopeMapping DataScopeMapping
}

func (command RegisterCommand) Validate() error {
	if _, allowed := allowedSourceTypes[command.SourceType]; !allowed ||
		!validOpaqueName(command.InstanceKey, 512) || !validAuthRef(command.AuthRef) ||
		(command.ClusterID != nil && *command.ClusterID == uuid.Nil) || !validAllowedSchemas(command.AllowedSchemas) ||
		!validScopeBinding(command.BackendLogicalID, command.DataScopeMapping) {
		return ErrInvalidInput
	}
	return nil
}

type SourceIdentity struct {
	TenantID           uuid.UUID
	SourceID           uuid.UUID
	CredentialRevision int64
	Proof              []byte
}

type EnvelopeSource struct {
	System   string
	Instance string
}

type FindingEnvelope struct {
	SchemaVersion string `json:"schemaVersion"`
	TenantID      uuid.UUID
	ClusterUID    string
	Source        EnvelopeSource
}

type BoundSourceContext struct {
	TenantID             uuid.UUID
	SourceID             uuid.UUID
	ClusterID            uuid.UUID
	ClusterUID           string
	CredentialRevision   int64
	RegistrationRevision int64
}

type CredentialVerifier interface {
	Verify(context.Context, string, SourceIdentity, FindingEnvelope) error
}

type CredentialVerifierFunc func(context.Context, string, SourceIdentity, FindingEnvelope) error

func (verify CredentialVerifierFunc) Verify(ctx context.Context, authRef string, identity SourceIdentity, envelope FindingEnvelope) error {
	return verify(ctx, authRef, identity, envelope)
}

type SourceUpdateCommand struct {
	ExpectedRevision int64
	ClusterID        *uuid.UUID
	ClusterIDSet     bool
	Status           string
	AllowedSchemas   []string
	BackendLogicalID *string
	DataScopeMapping *DataScopeMapping
}

func validAllowedSchemas(values []string) bool {
	if len(values) < 1 || len(values) > 2 {
		return false
	}
	seen := map[string]bool{}
	for _, v := range values {
		if (v != "finding-envelope/v1" && v != "finding-envelope/v2") || seen[v] {
			return false
		}
		seen[v] = true
	}
	return true
}

type CredentialRotationCommand struct {
	ExpectedRevision int64
	AuthRef          string
}

type SourceRegistrationRollbackCommand struct {
	ExpectedRevision int64
	TargetRevision   int64
}

func validOpaqueName(value string, limit int) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= limit &&
		!strings.ContainsAny(value, "\x00\r\n")
}

func validDisplayName(value string) bool {
	return validOpaqueName(value, 200)
}

func validAuthRef(value string) bool {
	if value == "" || len(value) > 1024 || strings.TrimSpace(value) != value ||
		!strings.HasPrefix(value, "openbao://") || strings.ContainsAny(value, "?#%\\") {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "openbao" || parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" ||
		parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path == "" {
		return false
	}
	for _, part := range strings.Split(strings.Trim(parsed.Path, "/"), "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}
