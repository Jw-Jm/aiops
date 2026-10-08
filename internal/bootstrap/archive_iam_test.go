package bootstrap

import (
	"encoding/json"
	"testing"
)

func TestEnvironmentAcceptsFourSeparatedArchiveRoles(t *testing.T) {
	c, secrets := validEnvironment(t)
	identities := []map[string]any{{"name": "bootstrap-admin", "credentials": []map[string]string{{"accessKey": "explicit-admin", "secretKey": "private-fixture-only"}}, "actions": []string{"Admin"}}}
	policies := []map[string]string{}
	prefix := "tenants/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef/"
	actions := map[string][]string{"write": {"s3:PutObject"}, "read": {"s3:GetObject", "s3:GetObjectVersion"}, "protect": {"s3:PutObjectRetention", "s3:PutObjectLegalHold"}, "cleanup": {"s3:DeleteObject", "s3:DeleteObjectVersion"}}
	for _, role := range []string{"write", "read", "protect", "cleanup"} {
		name := "tenant-explicit-" + role
		policy, _ := json.Marshal(map[string]any{"Version": "2012-10-17", "Statement": []map[string]any{
			{"Effect": "Allow", "Action": []string{"s3:ListBucket"}, "Resource": "arn:aws:s3:::current-evidence", "Condition": map[string]any{"StringLike": map[string]string{"s3:prefix": prefix + "*"}}},
			{"Effect": "Allow", "Action": actions[role], "Resource": "arn:aws:s3:::current-evidence/" + prefix + "*"},
		}})
		policies = append(policies, map[string]string{"name": name, "content": string(policy)})
		identities = append(identities, map[string]any{"name": name, "credentials": []map[string]string{{"accessKey": "explicit-" + role, "secretKey": "private-" + role}}, "policyNames": []string{name}})
	}
	setIAM := func(value any) {
		raw, _ := json.Marshal(value)
		for i := range secrets {
			if secrets[i].Name == "ops-seaweedfs-iam" {
				secrets[i].Data["s3.json"] = string(raw)
			}
		}
	}
	setIAM(map[string]any{"identities": identities, "policies": policies})
	if err := c.Validate(secrets); err != nil {
		t.Fatal(err)
	}
	for _, altered := range []string{
		`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["s3:*"],"Resource":"*"}]}`,
		`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["s3:GetObject"],"Resource":"arn:aws:s3:::foreign/tenants/*"}]}`,
	} {
		copyPolicies := append([]map[string]string{}, policies...)
		copyPolicies[0] = map[string]string{"name": policies[0]["name"], "content": altered}
		setIAM(map[string]any{"identities": identities, "policies": copyPolicies})
		if c.Validate(secrets) == nil {
			t.Fatal("unbounded/mixed Archive duty accepted")
		}
	}
}
