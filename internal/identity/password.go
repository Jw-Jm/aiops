// Package identity contains native account, credential and session storage.
//
// P2-01 intentionally has no HTTP login route. The repository and SQL objects
// are the data foundation for P2-02 and the restricted first-administrator
// bootstrap path. Secrets exist only as function inputs and one-time return
// values; they are never serialized by repository result types.
package identity

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters are fixed for credential version 1. They favor a clear,
// bounded profile over runtime tuning knobs: 64 MiB, two passes, one lane.
// A future credential version must use a new hash prefix and may change only
// these documented values after review.
const (
	argon2Version         = 19
	argon2MemoryKiB       = 64 * 1024
	argon2Time            = 2
	argon2Parallelism     = 1
	argon2SaltBytes       = 16
	argon2KeyBytes        = 32
	maxEncodedHashLength  = 512
	minEncodedHashLength  = 80
	minPasswordCharacters = 12
	maxPasswordCharacters = 256
)

var (
	ErrPasswordInvalid = errors.New("password is invalid")
	ErrPasswordHash    = errors.New("password hash is invalid")
)

// HashPassword encodes the fixed Argon2id profile. The plaintext is neither
// trimmed nor normalized; a rejected length is never silently truncated.
func HashPassword(password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	salt := make([]byte, argon2SaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argon2Time, argon2MemoryKiB, argon2Parallelism, argon2KeyBytes)
	encoded := fmt.Sprintf(
		"argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2Version, argon2MemoryKiB, argon2Time, argon2Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	)
	if len(encoded) < minEncodedHashLength || len(encoded) > maxEncodedHashLength {
		return "", ErrPasswordHash
	}
	return encoded, nil
}

type argon2Parameters struct {
	Version     int
	MemoryKiB   uint32
	Time        uint32
	Parallelism uint8
	Salt        []byte
	Key         []byte
}

// VerifyPassword performs a constant-time comparison after strict bounded
// parsing. Unknown formats and out-of-range parameters fail closed; they are
// never upgraded, relaxed, or silently re-derived with different parameters.
func VerifyPassword(password, encoded string) (bool, error) {
	if err := validatePasswordLength(password); err != nil {
		return false, err
	}
	if len(encoded) < minEncodedHashLength || len(encoded) > maxEncodedHashLength {
		return false, ErrPasswordHash
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 5 || parts[0] != "argon2id" {
		return false, ErrPasswordHash
	}
	parameters, err := parseArgon2Parameters(parts[1], parts[2], parts[3], parts[4])
	if err != nil {
		return false, err
	}
	key := argon2.IDKey([]byte(password), parameters.Salt, parameters.Time,
		parameters.MemoryKiB, parameters.Parallelism, uint32(len(parameters.Key)))
	return subtle.ConstantTimeCompare(key, parameters.Key) == 1, nil
}

func parseArgon2Parameters(versionText, costText, saltText, keyText string) (argon2Parameters, error) {
	versionMatches := versionParameterPattern.FindStringSubmatch(versionText)
	if versionMatches == nil {
		return argon2Parameters{}, ErrPasswordHash
	}
	version, err := strconv.Atoi(versionMatches[1])
	if err != nil || version != argon2Version {
		return argon2Parameters{}, ErrPasswordHash
	}
	costMatches := costParameterPattern.FindStringSubmatch(costText)
	if costMatches == nil {
		return argon2Parameters{}, ErrPasswordHash
	}
	memory, err := strconv.ParseUint(costMatches[1], 10, 32)
	if err != nil {
		return argon2Parameters{}, ErrPasswordHash
	}
	time, err := strconv.ParseUint(costMatches[2], 10, 32)
	if err != nil {
		return argon2Parameters{}, ErrPasswordHash
	}
	parallelism, err := strconv.ParseUint(costMatches[3], 10, 8)
	if err != nil {
		return argon2Parameters{}, ErrPasswordHash
	}
	// Bounded profiles prevent a malformed database row from forcing a huge
	// allocation or many passes. The active profile must be within this range;
	// exact equality is not required so a reviewed credential version can move
	// inside the audited envelope without breaking verification.
	if memory < 8192 || memory > 256*1024 || time < 1 || time > 4 || parallelism < 1 || parallelism > 4 {
		return argon2Parameters{}, ErrPasswordHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(saltText)
	if err != nil || len(salt) < 16 || len(salt) > 32 {
		return argon2Parameters{}, ErrPasswordHash
	}
	key, err := base64.RawStdEncoding.DecodeString(keyText)
	if err != nil || len(key) != argon2KeyBytes {
		return argon2Parameters{}, ErrPasswordHash
	}
	return argon2Parameters{
		Version: version, MemoryKiB: uint32(memory), Time: uint32(time),
		Parallelism: uint8(parallelism), Salt: salt, Key: key,
	}, nil
}

var (
	versionParameterPattern = regexp.MustCompile(`^v=([0-9]+)$`)
	costParameterPattern    = regexp.MustCompile(`^m=([0-9]+),t=([0-9]+),p=([0-9]+)$`)
)

func ValidatePassword(password string) error {
	return validatePasswordLength(password)
}

func validatePasswordLength(password string) error {
	if runes := len([]rune(password)); runes < minPasswordCharacters || runes > maxPasswordCharacters {
		return ErrPasswordInvalid
	}
	return nil
}
