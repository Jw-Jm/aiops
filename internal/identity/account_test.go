package identity

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestLoginNameNormalizationIsExplicitAndBounded(t *testing.T) {
	if got := NormalizeLoginName("  Ada.Lovelace-01 "); got != "ada.lovelace-01" {
		t.Fatalf("normalized login = %q", got)
	}
	valid := []string{"a", strings.Repeat("a", 128), "user.name-01"}
	for _, value := range valid {
		if err := ValidateLoginName(value); err != nil {
			t.Fatalf("valid login %q rejected: %v", value, err)
		}
	}
	invalid := []string{
		"", strings.Repeat("a", 129), ".leader", "-leader", "_leader",
		"space name", "unicode-名", "bad/slash", "trailing\n",
	}
	for _, value := range invalid {
		if err := ValidateLoginName(value); err == nil {
			t.Fatalf("invalid login %q accepted", value)
		}
	}
	if NormalizeLoginName("TRAILING\t") != "trailing" {
		t.Fatal("normalization did not remove trailing whitespace")
	}
}

func TestDisplayNameIsNotNormalizedOrTruncated(t *testing.T) {
	if err := ValidateDisplayName("Ada Lovelace"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateDisplayName("   "); err == nil {
		t.Fatal("blank display name accepted")
	}
	if err := ValidateDisplayName(strings.Repeat("名", 257)); err == nil {
		t.Fatal("over-long display name accepted")
	}
	if err := ValidateDisplayName(strings.Repeat("名", 256)); err != nil {
		t.Fatal("maximum rune-count display name rejected")
	}
}

func TestFirstAdministratorInputRejectsAmbiguousTenantAndInvalidIdentity(t *testing.T) {
	base := FirstAdministratorInput{
		LoginName: "root-admin", DisplayName: "Root Administrator",
		Password: "first-password-value",
	}
	if err := (FirstAdministratorInput{}).Validate(); err == nil {
		t.Fatal("empty tenant accepted")
	}
	both := base
	both.TenantID = uuid.New()
	both.TenantSlug = "primary"
	if err := both.Validate(); err == nil {
		t.Fatal("both tenant selectors accepted")
	}
	noPassword := base
	noPassword.TenantSlug = "primary"
	noPassword.Password = strings.Repeat("x", 11)
	if err := noPassword.Validate(); err == nil {
		t.Fatal("short bootstrap password accepted")
	}
	good := base
	good.TenantSlug = "primary"
	if err := good.Validate(); err != nil {
		t.Fatalf("valid bootstrap input rejected: %v", err)
	}
}

func TestLoginAccountJSONNeverSerializesPasswordHash(t *testing.T) {
	now := time.Now().UTC()
	account := LoginAccount{
		TenantID: uuid.New(), UserID: uuid.New(), LoginName: "root-admin",
		DisplayName: "Root Administrator", Status: "active",
		PasswordState: PasswordChangeRequired, MustChangePassword: true,
		AccountRevision: 1, CredentialVersion: 1, AuthorizationVersion: 1,
		SessionVersion: 1, PasswordHash: "argon2id$secret-material",
		WindowStartedAt: &now,
	}
	encoded, err := json.Marshal(account)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if strings.Contains(text, account.PasswordHash) || strings.Contains(text, "passwordHash\":") {
		t.Fatalf("password hash was serialized: %s", text)
	}
	if !strings.Contains(text, `"passwordHashPresent":true`) {
		t.Fatalf("safe account view lacks non-secret hash presence flag: %s", text)
	}
}

func TestFirstAdministratorInputJSONNeverSerializesPassword(t *testing.T) {
	input := FirstAdministratorInput{
		TenantSlug: "primary", LoginName: "root-admin",
		DisplayName: "Root Administrator", Password: "secret-bootstrap-password",
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if strings.Contains(text, input.Password) || strings.Contains(text, `"password":`) {
		t.Fatalf("bootstrap password was serialized: %s", text)
	}
	if !strings.Contains(text, `"passwordPresent":true`) {
		t.Fatalf("safe input view lacks password presence flag: %s", text)
	}
}
