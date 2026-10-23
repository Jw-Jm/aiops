package identity

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
	platformcrypto "ops-platform/internal/crypto"
)

// SecretEnvelope is what the database stores for a TOTP secret. The plaintext
// secret never reaches a table, a log line, an audit payload or a response body
// after the creation call.
type SecretEnvelope struct {
	Ciphertext string
	KeyRef     string
	KeyVersion string
}

// SecretSealer is the task-scoped key backend. Two implementations exist:
// OpenBao Transit (reusing the platform's existing machine-identity chain) and
// a local 32-byte key file for the isolated development instance. Neither
// hardcodes a key and neither stores one in the database.
type SecretSealer interface {
	Seal(ctx context.Context, tenantID, userID, enrollmentID uuid.UUID, plain []byte) (SecretEnvelope, error)
	Open(ctx context.Context, tenantID, userID, enrollmentID uuid.UUID, envelope SecretEnvelope) ([]byte, error)
}

var ErrSecretBackendUnavailable = errors.New("mfa secret backend is unavailable")

func mfaAAD(tenantID, userID, enrollmentID uuid.UUID) []byte {
	return []byte("ops-mfa-totp/v1|" + tenantID.String() + "|" + userID.String() + "|" + enrollmentID.String())
}

// FileSecretSealer encrypts with AES-256-GCM using a key read from a private
// 0600 file. The key file lives in the task's private directory, is never part
// of the repository and is loaded once at process start.
type FileSecretSealer struct {
	aead cipher.AEAD
	ref  string
}

func NewFileSecretSealer(path string) (*FileSecretSealer, error) {
	if strings.TrimSpace(path) == "" {
		return nil, ErrSecretBackendUnavailable
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%w: key file must be private", ErrSecretBackendUnavailable)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: key file is unreadable", ErrSecretBackendUnavailable)
	}
	encoded := strings.TrimSpace(string(raw))
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("%w: key must be 32 base64-encoded bytes", ErrSecretBackendUnavailable)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: key is invalid", ErrSecretBackendUnavailable)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("%w: cipher is unavailable", ErrSecretBackendUnavailable)
	}
	return &FileSecretSealer{aead: aead, ref: "file:mfa-totp/v1"}, nil
}

func (s *FileSecretSealer) Seal(_ context.Context, tenantID, userID, enrollmentID uuid.UUID, plain []byte) (SecretEnvelope, error) {
	if s == nil || s.aead == nil || tenantID == uuid.Nil || userID == uuid.Nil || enrollmentID == uuid.Nil || len(plain) == 0 {
		return SecretEnvelope{}, ErrSecretBackendUnavailable
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return SecretEnvelope{}, fmt.Errorf("%w: nonce", ErrSecretBackendUnavailable)
	}
	sealed := s.aead.Seal(nonce, nonce, plain, mfaAAD(tenantID, userID, enrollmentID))
	return SecretEnvelope{
		Ciphertext: base64.StdEncoding.EncodeToString(sealed),
		KeyRef:     s.ref, KeyVersion: "1",
	}, nil
}

func (s *FileSecretSealer) Open(_ context.Context, tenantID, userID, enrollmentID uuid.UUID, envelope SecretEnvelope) ([]byte, error) {
	if s == nil || s.aead == nil || envelope.KeyRef != s.ref || envelope.KeyVersion != "1" {
		return nil, ErrSecretBackendUnavailable
	}
	raw, err := base64.StdEncoding.DecodeString(envelope.Ciphertext)
	if err != nil || len(raw) <= s.aead.NonceSize() {
		return nil, ErrSecretBackendUnavailable
	}
	nonce := raw[:s.aead.NonceSize()]
	plain, err := s.aead.Open(nil, nonce, raw[s.aead.NonceSize():], mfaAAD(tenantID, userID, enrollmentID))
	if err != nil {
		return nil, ErrSecretBackendUnavailable
	}
	return plain, nil
}

// ProtectorSecretSealer reuses the existing OpenBao Transit envelope (the same
// implementation used for other sensitive platform objects) instead of adding a
// second crypto path.
type ProtectorSecretSealer struct {
	protector platformcrypto.Protector
	keyName   string
}

func NewProtectorSecretSealer(protector platformcrypto.Protector, keyName string) (*ProtectorSecretSealer, error) {
	if protector == nil || strings.TrimSpace(keyName) == "" {
		return nil, ErrSecretBackendUnavailable
	}
	return &ProtectorSecretSealer{protector: protector, keyName: keyName}, nil
}

func (s *ProtectorSecretSealer) Seal(ctx context.Context, tenantID, userID, enrollmentID uuid.UUID, plain []byte) (SecretEnvelope, error) {
	envelope, err := s.protector.Seal(ctx, tenantID, enrollmentID, plain)
	if err != nil {
		return SecretEnvelope{}, fmt.Errorf("%w: %v", ErrSecretBackendUnavailable, err)
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return SecretEnvelope{}, ErrSecretBackendUnavailable
	}
	return SecretEnvelope{
		Ciphertext: string(encoded), KeyRef: "openbao:" + envelope.KeyName,
		KeyVersion: envelope.KeyVersion,
	}, nil
}

func (s *ProtectorSecretSealer) Open(ctx context.Context, tenantID, userID, enrollmentID uuid.UUID, envelope SecretEnvelope) ([]byte, error) {
	var decoded platformcrypto.Envelope
	if err := json.Unmarshal([]byte(envelope.Ciphertext), &decoded); err != nil {
		return nil, ErrSecretBackendUnavailable
	}
	if envelope.KeyRef != "openbao:"+s.keyName || decoded.KeyName != s.keyName {
		return nil, ErrSecretBackendUnavailable
	}
	plain, err := s.protector.Open(ctx, tenantID, enrollmentID, decoded)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSecretBackendUnavailable, err)
	}
	return plain, nil
}

// UnavailableSecretSealer keeps the API serving without a key backend: MFA
// routes answer 503 instead of disappearing or silently falling back to
// plaintext storage.
type UnavailableSecretSealer struct{}

func (UnavailableSecretSealer) Seal(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, []byte) (SecretEnvelope, error) {
	return SecretEnvelope{}, ErrSecretBackendUnavailable
}

func (UnavailableSecretSealer) Open(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, SecretEnvelope) ([]byte, error) {
	return nil, ErrSecretBackendUnavailable
}

// DigestRecoveryCode returns the stored representation of a recovery code.
func DigestRecoveryCode(code string) []byte {
	sum := sha256.Sum256([]byte("ops-mfa-recovery/v1|" + code))
	return sum[:]
}
