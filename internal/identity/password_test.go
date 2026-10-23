package identity

import (
	"strings"
	"testing"
)

func TestHashAndVerifyPasswordUsesFixedBoundedArgon2id(t *testing.T) {
	password := strings.Repeat("correct-horse-", 18) // 196 characters
	encoded, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encoded, "argon2id$v=19$m=65536,t=2,p=1$") {
		t.Fatalf("unexpected fixed profile: %s", encoded)
	}
	if len(encoded) > maxEncodedHashLength {
		t.Fatalf("encoded hash exceeds bound: %d", len(encoded))
	}
	ok, err := VerifyPassword(password, encoded)
	if err != nil || !ok {
		t.Fatalf("correct password rejected: ok=%v err=%v", ok, err)
	}
	ok, err = VerifyPassword(password+"x", encoded)
	if err != nil || ok {
		t.Fatalf("wrong password accepted: ok=%v err=%v", ok, err)
	}
	second, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	if second == encoded || strings.Contains(second, strings.Split(encoded, "$")[3]) {
		t.Fatal("password hashes did not use independent random salts")
	}
}

func TestPasswordInputBoundsRejectSilentTruncation(t *testing.T) {
	short := strings.Repeat("x", 11)
	long := strings.Repeat("x", 257)
	for name, password := range map[string]string{"short": short, "long": long} {
		if _, err := HashPassword(password); err != ErrPasswordInvalid {
			t.Fatalf("%s password hash error = %v, want ErrPasswordInvalid", name, err)
		}
		if ok, err := VerifyPassword(password, strings.Repeat("argon2id$", 10)); err != ErrPasswordInvalid || ok {
			t.Fatalf("%s verify password error = %v ok=%v", name, err, ok)
		}
	}
}

func TestVerifyPasswordRejectsMalformedOrUnboundedProfiles(t *testing.T) {
	valid, err := HashPassword("bounded-password-value")
	if err != nil {
		t.Fatal(err)
	}
	prefix := strings.SplitN(valid, "$", 3)
	if len(prefix) != 3 {
		t.Fatal("unexpected encoded hash")
	}
	cases := map[string]string{
		"short":        "argon2id$v=19$m=8192,t=1,p=1$short$short",
		"huge-memory":  "argon2id$v=19$m=999999999,t=2,p=1$" + strings.Repeat("A", 22) + "$" + strings.Repeat("A", 43),
		"many-passes":  "argon2id$v=19$m=65536,t=99,p=1$" + strings.Repeat("A", 22) + "$" + strings.Repeat("A", 43),
		"many-lanes":   "argon2id$v=19$m=65536,t=2,p=99$" + strings.Repeat("A", 22) + "$" + strings.Repeat("A", 43),
		"future":       "argon2id$v=20$m=65536,t=2,p=1$" + strings.Repeat("A", 22) + "$" + strings.Repeat("A", 43),
		"trailing":     prefix[0] + "$" + prefix[1] + "$m=65536,t=2,p=1junk$" + strings.Repeat("A", 22) + "$" + strings.Repeat("A", 43),
		"wrong-keylen": "argon2id$v=19$m=65536,t=2,p=1$" + strings.Repeat("A", 22) + "$" + strings.Repeat("A", 44),
	}
	for name, encoded := range cases {
		ok, err := VerifyPassword("bounded-password-value", encoded)
		if err == nil || ok {
			t.Fatalf("%s hash was accepted: ok=%v err=%v", name, ok, err)
		}
	}
}

func TestEncodedHashGrammarIsConstrained(t *testing.T) {
	encoded, err := HashPassword("bounded-password-value")
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(encoded, "\r\n\x00") {
		t.Fatal("encoded hash contains control characters")
	}
	if !strings.HasPrefix(encoded, "argon2id$") {
		t.Fatal("encoded hash lacks versioned algorithm prefix")
	}
}
