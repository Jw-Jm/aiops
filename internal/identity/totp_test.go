package identity

import (
	"context"
	"encoding/base32"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestTOTPVerificationAcceptsTheCurrentStepOnly(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0).UTC()
	step := TOTPStep(now)
	code, err := TOTPCodeAt(secret, step)
	if err != nil {
		t.Fatal(err)
	}
	matched, ok := VerifyTOTP(secret, code, now)
	if !ok || matched != step {
		t.Fatalf("the current code was rejected: ok=%v step=%d want=%d", ok, matched, step)
	}
	if _, ok := VerifyTOTP(secret, code, now.Add(5*time.Minute)); ok {
		t.Fatal("a code outside the skew window was accepted")
	}
	if _, ok := VerifyTOTP(secret, "12345", now); ok {
		t.Fatal("a short code was accepted")
	}
	if _, ok := VerifyTOTP(secret, "1234567", now); ok {
		t.Fatal("a long code was accepted")
	}
	if _, ok := VerifyTOTP(secret, "12345a", now); ok {
		t.Fatal("a non numeric code was accepted")
	}
	previous, err := TOTPCodeAt(secret, step-1)
	if err != nil {
		t.Fatal(err)
	}
	matched, ok = VerifyTOTP(secret, previous, now)
	if !ok || matched != step-1 {
		t.Fatalf("the previous step was rejected: ok=%v matched=%d", ok, matched)
	}
}

func TestTOTPURIAndSecretShape(t *testing.T) {
	secret, err := GenerateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil || len(decoded) != 20 {
		t.Fatalf("the secret must be 160 bits of base32: len=%d err=%v", len(decoded), err)
	}
	uri := TOTPURI("智能运维平台", "mfa.admin", secret)
	if uri == "" || !contains(uri, "otpauth://totp/") || !contains(uri, "secret="+secret) {
		t.Fatalf("the provisioning URI is malformed: %s", uri)
	}
}

func TestRecoveryCodesAreHighEntropySingleUseDigests(t *testing.T) {
	codes, err := NewRecoveryCodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != 10 {
		t.Fatalf("recovery code count = %d, want 10", len(codes))
	}
	seen := map[string]struct{}{}
	digests := map[string]struct{}{}
	for _, code := range codes {
		normalized, ok := NormalizeRecoveryCode(code)
		if !ok || normalized != code {
			t.Fatalf("the recovery code shape is invalid: %q", code)
		}
		if _, duplicate := seen[code]; duplicate {
			t.Fatalf("a recovery code was issued twice: %q", code)
		}
		seen[code] = struct{}{}
		digest := DigestRecoveryCode(code)
		if len(digest) != 32 {
			t.Fatalf("the recovery digest must be 32 bytes, got %d", len(digest))
		}
		if _, duplicate := digests[string(digest)]; duplicate {
			t.Fatal("two recovery codes share a digest")
		}
		digests[string(digest)] = struct{}{}
	}
	if _, ok := NormalizeRecoveryCode("abcd"); ok {
		t.Fatal("a short recovery code was accepted")
	}
	if _, ok := NormalizeRecoveryCode("ABCD-EFG"); ok {
		t.Fatal("a malformed recovery code was accepted")
	}
}

func TestFileSecretSealerBindsTheEnvelopeToItsOwner(t *testing.T) {
	directory := t.TempDir()
	keyPath := filepath.Join(directory, "key")
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	if err := os.WriteFile(keyPath, []byte(key), 0o600); err != nil {
		t.Fatal(err)
	}
	sealer, err := NewFileSecretSealer(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	tenantID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	enrollmentID := uuid.Must(uuid.NewV7())
	plain := []byte("JBSWY3DPEHPK3PXP")
	envelope, err := sealer.Seal(context.Background(), tenantID, userID, enrollmentID, plain)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.Ciphertext == "" || envelope.KeyRef == "" || envelope.KeyVersion == "" {
		t.Fatalf("the envelope is incomplete: %+v", envelope)
	}
	if contains(envelope.Ciphertext, string(plain)) {
		t.Fatal("the envelope stored the plaintext secret")
	}
	opened, err := sealer.Open(context.Background(), tenantID, userID, enrollmentID, envelope)
	if err != nil || string(opened) != string(plain) {
		t.Fatalf("the sealed secret did not open: %v", err)
	}
	if _, err := sealer.Open(context.Background(), tenantID, userID, uuid.Must(uuid.NewV7()), envelope); err == nil {
		t.Fatal("another enrolment opened the sealed secret")
	}
	if _, err := sealer.Open(context.Background(), tenantID, uuid.Must(uuid.NewV7()), enrollmentID, envelope); err == nil {
		t.Fatal("another user opened the sealed secret")
	}
	other := &FileSecretSealer{}
	if _, err := other.Open(context.Background(), tenantID, userID, enrollmentID, envelope); err == nil {
		t.Fatal("a missing key backend opened the sealed secret")
	}
}

func TestUnavailableSecretSealerNeverStoresPlaintext(t *testing.T) {
	sealer := UnavailableSecretSealer{}
	envelope, err := sealer.Seal(context.Background(), uuid.New(), uuid.New(), uuid.New(), []byte("secret"))
	if err == nil || envelope.Ciphertext != "" {
		t.Fatalf("an unavailable backend must not produce an envelope: %+v err=%v", envelope, err)
	}
	if _, err := sealer.Open(context.Background(), uuid.New(), uuid.New(), uuid.New(), SecretEnvelope{}); err == nil {
		t.Fatal("an unavailable backend must not open a secret")
	}
}

func contains(value, needle string) bool {
	return len(value) >= len(needle) && (func() bool {
		for index := 0; index+len(needle) <= len(value); index++ {
			if value[index:index+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
