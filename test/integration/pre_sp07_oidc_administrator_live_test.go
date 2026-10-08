//go:build pre_sp07_live

package integration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/bootstrap"
)

func TestFormalOIDCAdministratorRetirementAndRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	privateDir := os.Getenv("PRE_SP07_OIDC_PRIVATE_DIR")
	evidenceDir := os.Getenv("PRE_SP07_OIDC_ADMIN_EVIDENCE_DIR")
	if privateDir == "" || evidenceDir == "" {
		t.Fatal("actual enrolled HTTPS Keycloak and controlled evidence directories required")
	}
	tokens, input, private := freshFormalOIDCLogin(t)
	subject, err := uuid.Parse(tokens.Subject)
	if err != nil {
		t.Fatal("actual named subject invalid")
	}
	config := bootstrap.OIDCAdministrator{SchemaVersion: 1, TenantID: input.TenantID, Subject: subject, Username: input.Username}
	configPath := filepath.Join(privateDir, "administrator-input-r1.json")
	privatePath := filepath.Join(privateDir, "administrator-private-r1.json")
	raw, _ := json.Marshal(config)
	if os.WriteFile(configPath, raw, 0600) != nil {
		t.Fatal("explicit administrator input unavailable")
	}
	if _, err := os.Stat(privatePath); os.IsNotExist(err) {
		raw, _ := json.Marshal(map[string]string{"bootstrapUsername": private.BootstrapUsername, "bootstrapPassword": private.BootstrapPassword, "replacementBootstrapPassword": uuid.NewString() + uuid.NewString()})
		if os.WriteFile(privatePath, raw, 0600) != nil {
			t.Fatal("private explicit replacement credential unavailable")
		}
	}
	binary := filepath.Join(privateDir, "opsctl-administrator-r1")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/opsctl")
	build.Dir = "../.."
	if build.Run() != nil {
		t.Fatal("formal opsctl build failed")
	}
	invoke := func(stage, configFile string, success bool, name string) {
		command := exec.CommandContext(ctx, binary, "bootstrap", "oidc-administrator", "--stage", stage, "--profile", filepath.Join(privateDir, "resolved-profile.yaml"), "--config", configFile, "--secrets-file", privatePath, "--ca", filepath.Join(privateDir, "oidc-ca.pem"), "--token-file", filepath.Join(privateDir, "fresh-loa2.token"))
		output, err := command.CombinedOutput()
		if (err == nil) != success {
			os.WriteFile(filepath.Join(privateDir, name+"-private-error.log"), output, 0600)
			t.Fatal("formal administrator gate differed; private original error retained: " + name)
		}
		if success {
			var receipt map[string]any
			if json.Unmarshal(output, &receipt) != nil {
				t.Fatal("formal public administrator receipt invalid")
			}
			if stage == "retire" && (receipt["namedPostRestartManagementVerified"] != true || receipt["oldCredentialAndTokenRejected"] != true || receipt["masterAdministrationDenied"] != true) {
				t.Fatal("actual retirement controls incomplete")
			}
			if os.WriteFile(filepath.Join(evidenceDir, name+".json"), output, 0644) != nil {
				t.Fatal("public receipt unavailable")
			}
		} else {
			raw, _ := json.Marshal(map[string]any{"operation": stage, "expected": "refused before mutation", "exitCode": command.ProcessState.ExitCode()})
			os.WriteFile(filepath.Join(evidenceDir, name+".json"), append(raw, '\n'), 0644)
		}
	}
	// A correctly signed current LoA-2 platform administrator token has no
	// Keycloak realm role yet. Retirement must fail with independent positives
	// subsequently succeeding, rather than relying on a broken target.
	invoke("retire", configPath, false, "oidc-administrator-before-role-refusal-r1")
	wrong := config
	wrong.TenantID = uuid.New()
	raw, _ = json.Marshal(wrong)
	wrongPath := filepath.Join(privateDir, "administrator-wrong-tenant-r1.json")
	os.WriteFile(wrongPath, raw, 0600)
	invoke("prepare", wrongPath, false, "oidc-administrator-wrong-tenant-refusal-r1")
	invoke("prepare", configPath, true, "oidc-administrator-role-prepared-r1")
	waitForNextTOTPPeriod(t, ctx)
	_, _, _ = freshFormalOIDCLogin(t)
	invoke("retire", configPath, true, "oidc-administrator-retired-r1")
	t.Log("real HTTPS OTP/LoA-2 identity, named ops realm role, master refusal, wrong tenant/before-role refusal, private Secret rotation, temporary principal removal, old credential/token refusal, restart and named post-restart management verified; separate fresh-login gate required; no operating platform scopes assigned")
}

// A separate invocation opens a new Service tunnel after the old selected Pod
// has been replaced; a persisted bearer alone cannot prove new OTP login.
func TestFormalOIDCNamedPostRetirementFreshLogin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	waitForNextTOTPPeriod(t, ctx)
	post, input, _ := freshFormalOIDCLogin(t)
	privateDir := os.Getenv("PRE_SP07_OIDC_PRIVATE_DIR")
	raw, err := os.ReadFile(filepath.Join(privateDir, "administrator-input-r1.json"))
	var expected bootstrap.OIDCAdministrator
	if err != nil || json.Unmarshal(raw, &expected) != nil || expected.Validate() != nil || post.Subject != expected.Subject.String() || post.TenantID != expected.TenantID.String() || input.Username != expected.Username {
		t.Fatal("named fresh OTP/LoA-2 login identity differs after retirement")
	}
	t.Log("new HTTPS PKCE/nonce/OTP/LoA-2 named login after bootstrap removal and actual Keycloak restart verified")
}
