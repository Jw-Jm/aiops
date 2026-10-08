package bootstrap

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"io"
	"ops-platform/internal/archive"
	"ops-platform/internal/integrations/s3"
	"regexp"
	"strings"
)

// Accept the exact policy structure consumed by the existing v2 Archive
// credentials. No wildcard action, foreign bucket, unbounded list or combination
// of write/read/protection/cleanup duties can pass environment bootstrap.
func validateArchiveRolePolicy(raw, bucket string) (string, string, error) {
	var policy map[string]any
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	if decoder.Decode(&policy) != nil || decoder.Decode(new(any)) != io.EOF {
		return "", "", errors.New("invalid Archive policy")
	}
	statements, ok := policy["Statement"].([]any)
	if !ok || len(statements) != 2 {
		return "", "", errors.New("Archive policy must contain exact tenant list and single object duty")
	}
	statement, ok := statements[1].(map[string]any)
	if !ok {
		return "", "", errors.New("invalid Archive object policy")
	}
	resource, ok := statement["Resource"].(string)
	if !ok {
		return "", "", errors.New("explicit Archive object resource required")
	}
	match := regexp.MustCompile(`^arn:aws:s3:::` + regexp.QuoteMeta(bucket) + `/(tenants/[a-f0-9]{64}/)\*$`).FindStringSubmatch(resource)
	if len(match) != 2 {
		return "", "", errors.New("Archive role resource exceeds exact tenant prefix")
	}
	prefix := match[1]
	actual, _ := json.Marshal(policy)
	for role := range archiveRoleActions {
		expected, _ := json.Marshal(archiveRolePolicy(bucket, prefix, role))
		if bytes.Equal(actual, expected) {
			return prefix, role, nil
		}
	}
	return "", "", errors.New("Archive policy differs from separated least-privilege duty")
}

// ArchiveIAMInput is private operator input. Credentials are explicit, with no
// generated defaults or global runtime fallback. Output is installed before the
// source starts; live credential rotation remains a separate verified operation.
type ArchiveIAMInput struct {
	SchemaVersion  int                  `json:"schemaVersion"`
	Bucket         string               `json:"bucket"`
	BootstrapAdmin s3.RoleCredential    `json:"bootstrapAdmin"`
	Credentials    s3.TenantCredentials `json:"credentials"`
}

func BuildArchiveIAM(c ArchiveIAMInput) ([]byte, error) {
	if c.SchemaVersion != 1 || !environmentBucket.MatchString(c.Bucket) || strings.Contains(c.Bucket, "..") || c.Credentials.SchemaVersion != "ops-archive-credentials/v2" || len(c.Credentials.Tenants) == 0 || len(c.Credentials.Tenants) > 4096 || c.BootstrapAdmin.AccessKey == "" || c.BootstrapAdmin.SecretKey == "" {
		return nil, errors.New("explicit bucket, bootstrap admin and separated v2 tenant credentials required")
	}
	identities := []map[string]any{{"name": "installation-bootstrap-admin", "credentials": []s3.RoleCredential{c.BootstrapAdmin}, "actions": []string{"Admin"}}}
	policies := []map[string]string{}
	tenants := map[uuid.UUID]bool{}
	keys := map[string]bool{c.BootstrapAdmin.AccessKey: true}
	secrets := map[string]bool{c.BootstrapAdmin.SecretKey: true}
	for _, tenant := range c.Credentials.Tenants {
		if tenant.TenantID == uuid.Nil || tenants[tenant.TenantID] || tenant.Roles == nil || tenant.AccessKey != "" || tenant.SecretKey != "" {
			return nil, errors.New("distinct tenant and four explicit Archive roles required")
		}
		tenants[tenant.TenantID] = true
		roles := map[string]s3.RoleCredential{"write": tenant.Roles.Write, "read": tenant.Roles.Read, "protect": tenant.Roles.Protect, "cleanup": tenant.Roles.Cleanup}
		for _, role := range []string{"write", "read", "protect", "cleanup"} {
			credential := roles[role]
			if credential.AccessKey == "" || len(credential.AccessKey) > 256 || credential.SecretKey == "" || len(credential.SecretKey) > 1024 || keys[credential.AccessKey] || secrets[credential.SecretKey] {
				return nil, errors.New("distinct explicit Archive role credentials required")
			}
			keys[credential.AccessKey] = true
			secrets[credential.SecretKey] = true
			name := "tenant-" + tenant.TenantID.String() + "-" + role
			policy, _ := json.Marshal(archiveRolePolicy(c.Bucket, archive.TenantPrefix(tenant.TenantID), role))
			policies = append(policies, map[string]string{"name": name, "content": string(policy)})
			identities = append(identities, map[string]any{"name": name, "credentials": []s3.RoleCredential{credential}, "policyNames": []string{name}})
		}
	}
	raw, err := json.Marshal(map[string]any{"identities": identities, "policies": policies})
	if err != nil {
		return nil, errors.New("Archive IAM encoding failed")
	}
	if err := validateBootstrapArchiveIAM(string(raw), map[string]string{"accessKey": c.BootstrapAdmin.AccessKey, "secretKey": c.BootstrapAdmin.SecretKey}, c.Bucket); err != nil {
		return nil, err
	}
	return raw, nil
}

var archiveRoleActions = map[string][]string{
	"write": {"s3:PutObject"}, "read": {"s3:GetObject", "s3:GetObjectVersion"},
	"protect": {"s3:PutObjectRetention", "s3:PutObjectLegalHold"}, "cleanup": {"s3:DeleteObject", "s3:DeleteObjectVersion"},
}

func archiveRolePolicy(bucket, prefix, role string) map[string]any {
	return map[string]any{"Version": "2012-10-17", "Statement": []map[string]any{
		{"Effect": "Allow", "Action": []string{"s3:ListBucket"}, "Resource": "arn:aws:s3:::" + bucket, "Condition": map[string]any{"StringLike": map[string]string{"s3:prefix": prefix + "*"}}},
		{"Effect": "Allow", "Action": archiveRoleActions[role], "Resource": "arn:aws:s3:::" + bucket + "/" + prefix + "*"},
	}}
}
