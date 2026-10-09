package action

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"io"
	"ops-platform/internal/configregistry"
	"ops-platform/internal/resource"
	"regexp"
	"slices"
	"strings"
)

var imagePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:/-]*@sha256:[0-9a-f]{64}$`)
var namePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

type Profile struct {
	SchemaVersion       string    `json:"schemaVersion"`
	ID                  uuid.UUID `json:"executionProfileId"`
	Version             int       `json:"version"`
	Name                string    `json:"name"`
	Type                string    `json:"type"`
	AllowedTargets      []string  `json:"allowedTargets"`
	ClusterUID          string    `json:"clusterUid"`
	Namespace           string    `json:"namespace"`
	ToolImageDigest     string    `json:"toolImageDigest"`
	Tools               []string  `json:"tools"`
	NetworkPolicyRef    string    `json:"networkPolicyRef"`
	CredentialRef       string    `json:"credentialRef"`
	TimeoutSeconds      int       `json:"timeoutSeconds"`
	MaxOutputBytes      int64     `json:"maxOutputBytes"`
	Principal           string    `json:"principal"`
	HostOnboardingRef   string    `json:"hostOnboardingRef"`
	DevelopmentFallback bool      `json:"developmentFallback"`
}

func (p Profile) HighPrivilege() bool { return p.Type == "k8s_cluster" || p.Type == "ssh_root" }
func (p Profile) Validate() error {
	if p.SchemaVersion != "execution-profile/v2" || p.ID == uuid.Nil || p.Version < 1 || !namePattern.MatchString(p.Name) || len(p.AllowedTargets) == 0 || len(p.AllowedTargets) > 100 || p.ClusterUID == "" || !imagePattern.MatchString(p.ToolImageDigest) || !namePattern.MatchString(p.NetworkPolicyRef) || p.CredentialRef == "" || len(p.Tools) == 0 || p.TimeoutSeconds < 1 || p.TimeoutSeconds > DefaultTimeoutSeconds || p.MaxOutputBytes < 1 || p.MaxOutputBytes > DefaultMaxOutputBytes {
		return ErrInvalid
	}
	for _, target := range p.AllowedTargets {
		id, err := resource.ParseCanonicalID(target)
		if err != nil || id.Scope != p.ClusterUID {
			return ErrInvalid
		}
	}
	for _, tool := range p.Tools {
		if !namePattern.MatchString(tool) || tool == "virtctl" {
			return ErrInvalid
		}
	}
	switch p.Type {
	case "k8s_namespace":
		if !namePattern.MatchString(p.Namespace) || p.Principal != "" {
			return ErrInvalid
		}
	case "k8s_cluster":
		if p.Namespace != "" || p.Principal != "" {
			return ErrInvalid
		}
	case "ssh_user":
		if !namePattern.MatchString(p.Principal) || p.Principal == "root" || p.HostOnboardingRef == "" || p.Namespace != "" {
			return ErrInvalid
		}
	case "ssh_root":
		if p.Principal != "root" || p.HostOnboardingRef == "" || p.Namespace != "" {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	if p.DevelopmentFallback {
		return ErrDenied
	}
	return nil
}
func (p Profile) Allows(target string, options Options) bool {
	return slices.Contains(p.AllowedTargets, target) && options.TimeoutSeconds > 0 && options.TimeoutSeconds <= p.TimeoutSeconds && options.MaxOutputBytes > 0 && options.MaxOutputBytes <= p.MaxOutputBytes && options.WorkingDirectory == "" && len(options.Environment) == 0
}
func ProfileSigningPayload(tenant uuid.UUID, p Profile) []byte {
	return Canonical(struct {
		Domain  string    `json:"domain"`
		Tenant  uuid.UUID `json:"tenantId"`
		Profile Profile   `json:"profile"`
	}{"ops-execution-profile/v2", tenant, p})
}
func VerifyProfile(ctx context.Context, trust configregistry.SignatureVerifier, tenant uuid.UUID, p Profile, key string, signature []byte) error {
	if p.Validate() != nil || tenant == uuid.Nil || trust == nil || strings.TrimSpace(key) == "" {
		return ErrInvalid
	}
	return trust.Verify(ctx, key, ProfileSigningPayload(tenant, p), signature)
}
func DecodeProfile(b []byte) (Profile, error) {
	var p Profile
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if err := d.Decode(&p); err != nil {
		return p, ErrInvalid
	}
	var trailing any
	if d.Decode(&trailing) != io.EOF {
		return p, ErrInvalid
	}
	return p, p.Validate()
}
