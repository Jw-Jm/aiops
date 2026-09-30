package crypto

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/google/uuid"
	"ops-platform/internal/integrations/openbao"
)

type Envelope struct {
	Version         string    `json:"version"`
	TenantID        uuid.UUID `json:"tenantId"`
	ObjectID        uuid.UUID `json:"objectId"`
	KeyName         string    `json:"keyName"`
	Ciphertext      string    `json:"ciphertext"`
	KeyVersion      string    `json:"keyVersion"`
	PlaintextDigest string    `json:"plaintextDigest"`
}
type Protector interface {
	Seal(context.Context, uuid.UUID, uuid.UUID, []byte) (Envelope, error)
	Open(context.Context, uuid.UUID, uuid.UUID, Envelope) ([]byte, error)
}
type Transit interface {
	TransitEncrypt(context.Context, string, []byte, []byte) (openbao.TransitCiphertext, error)
	TransitDecrypt(context.Context, string, string, []byte) ([]byte, error)
}
type TransitProtector struct {
	transit Transit
	key     string
}

func NewTransitProtector(transit Transit, key string) (*TransitProtector, error) {
	if transit == nil || key == "" {
		return nil, errors.New("OpenBao Transit client and key are required")
	}
	return &TransitProtector{transit, key}, nil
}
func aad(tenant, object uuid.UUID) []byte {
	return []byte("ops-sensitive-object/v1|" + tenant.String() + "|" + object.String())
}
func (p *TransitProtector) Seal(ctx context.Context, tenant, object uuid.UUID, plain []byte) (Envelope, error) {
	if tenant == uuid.Nil || object == uuid.Nil {
		return Envelope{}, errors.New("tenant and object identity are required")
	}
	encrypted, err := p.transit.TransitEncrypt(ctx, p.key, plain, aad(tenant, object))
	if err != nil {
		return Envelope{}, errors.New("sensitive write rejected: Transit encryption unavailable")
	}
	sum := sha256.Sum256(plain)
	return Envelope{Version: "transit-envelope/v1", TenantID: tenant, ObjectID: object, KeyName: p.key, Ciphertext: encrypted.Ciphertext, KeyVersion: encrypted.KeyVersion, PlaintextDigest: "sha256:" + hex.EncodeToString(sum[:])}, nil
}
func (p *TransitProtector) Open(ctx context.Context, tenant, object uuid.UUID, e Envelope) ([]byte, error) {
	if e.Version != "transit-envelope/v1" || tenant == uuid.Nil || object == uuid.Nil || e.TenantID != tenant || e.ObjectID != object || e.KeyName != p.key {
		return nil, errors.New("encrypted object identity mismatch")
	}
	version, err := openbao.TransitVersion(e.Ciphertext)
	if err != nil || version != e.KeyVersion {
		return nil, errors.New("encrypted object key version mismatch")
	}
	plain, err := p.transit.TransitDecrypt(ctx, p.key, e.Ciphertext, aad(tenant, object))
	if err != nil {
		return nil, errors.New("sensitive read rejected: Transit decryption unavailable")
	}
	sum := sha256.Sum256(plain)
	if "sha256:"+hex.EncodeToString(sum[:]) != e.PlaintextDigest {
		return nil, errors.New("encrypted object plaintext digest mismatch")
	}
	return plain, nil
}
