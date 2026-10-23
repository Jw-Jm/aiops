package identity

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// RFC 6238 TOTP parameters. They are fixed by the reviewed identity contract
// (6 digits, SHA-1, 30 second period) and are implemented with the standard
// library only: no extra dependency, no home-grown primitive.
const (
	TOTPPeriodSeconds = 30
	TOTPDigits        = 6
	totpSecretBytes   = 20
)

// GenerateTOTPSecret returns a new 160-bit secret in RFC 4648 base32 (no
// padding), which is what authenticator applications expect.
func GenerateTOTPSecret() (string, error) {
	raw := make([]byte, totpSecretBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate totp secret: %w", err)
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw), nil
}

// TOTPURI builds the provisioning URI shown exactly once at enrolment.
func TOTPURI(issuer, account, secret string) string {
	label := url.PathEscape(issuer + ":" + account)
	query := url.Values{}
	query.Set("secret", secret)
	query.Set("issuer", issuer)
	query.Set("algorithm", "SHA1")
	query.Set("digits", fmt.Sprint(TOTPDigits))
	query.Set("period", fmt.Sprint(TOTPPeriodSeconds))
	return "otpauth://totp/" + label + "?" + query.Encode()
}

// TOTPStep is the time step for an instant.
func TOTPStep(now time.Time) int64 {
	return now.UTC().Unix() / TOTPPeriodSeconds
}

// TOTPCodeAt computes the code for one step. It is exported for the contract
// tests and for the enrolment confirmation, which must show the same value the
// authenticator application displays.
func TOTPCodeAt(secret string, step int64) (string, error) {
	normalized := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(secret), " ", ""))
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(normalized)
	if err != nil || len(key) < 10 {
		return "", fmt.Errorf("invalid totp secret")
	}
	counter := make([]byte, 8)
	binary.BigEndian.PutUint64(counter, uint64(step))
	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(counter)
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", TOTPDigits, value%1000000), nil
}

// VerifyTOTP accepts the current step and one adjacent step in each direction
// (clock skew). It returns the matched step so the caller can persist it and
// reject a replay inside the same window. A code that is not exactly the
// contract shape is rejected before any comparison.
func VerifyTOTP(secret, code string, now time.Time) (int64, bool) {
	if len(code) != TOTPDigits {
		return 0, false
	}
	for _, character := range code {
		if character < '0' || character > '9' {
			return 0, false
		}
	}
	current := TOTPStep(now)
	for _, candidate := range []int64{current, current - 1, current + 1} {
		expected, err := TOTPCodeAt(secret, candidate)
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(expected), []byte(code)) == 1 {
			return candidate, true
		}
	}
	return 0, false
}

// NewRecoveryCodes returns ten high-entropy single-use codes in the contract
// shape XXXX-XXXX. Only their SHA-256 digests are ever stored.
func NewRecoveryCodes() ([]string, error) {
	codes := make([]string, 0, 10)
	seen := map[string]struct{}{}
	for len(codes) < 10 {
		raw := make([]byte, 8)
		if _, err := rand.Read(raw); err != nil {
			return nil, fmt.Errorf("generate recovery code: %w", err)
		}
		encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)
		if len(encoded) < 8 {
			continue
		}
		code := encoded[0:4] + "-" + encoded[4:8]
		if _, duplicate := seen[code]; duplicate {
			continue
		}
		seen[code] = struct{}{}
		codes = append(codes, code)
	}
	return codes, nil
}

// NormalizeRecoveryCode accepts the displayed shape only.
func NormalizeRecoveryCode(value string) (string, bool) {
	normalized := strings.ToUpper(strings.TrimSpace(value))
	if len(normalized) != 9 || normalized[4] != '-' {
		return "", false
	}
	for index, character := range normalized {
		if index == 4 {
			continue
		}
		if !((character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9')) {
			return "", false
		}
	}
	return normalized, true
}
