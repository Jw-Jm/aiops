package crypto

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"ops-platform/internal/integrations/openbao"
	"testing"
)

func TestProtectorBindsTenantObjectAndFailsClosed(t *testing.T) {
	fake := &fakeTransit{}
	p, err := NewTransitProtector(fake, "evidence-archive")
	if err != nil {
		t.Fatal(err)
	}
	tenant, object := uuid.New(), uuid.New()
	sealed, err := p.Seal(context.Background(), tenant, object, []byte("unit-test-plaintext"))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := p.Open(context.Background(), tenant, object, sealed)
	if err != nil || string(plain) != "unit-test-plaintext" {
		t.Fatalf("roundtrip: %v", err)
	}
	if _, err := p.Open(context.Background(), uuid.New(), object, sealed); err == nil {
		t.Fatal("cross tenant decrypted")
	}
	fake.fail = true
	if _, err := p.Seal(context.Background(), tenant, object, []byte("sensitive")); err == nil {
		t.Fatal("Transit outage accepted write")
	}
}

type fakeTransit struct {
	plain, aad []byte
	fail       bool
}

func (f *fakeTransit) TransitEncrypt(_ context.Context, _ string, plain, aad []byte) (openbao.TransitCiphertext, error) {
	if f.fail {
		return openbao.TransitCiphertext{}, errors.New("unavailable")
	}
	f.plain = append([]byte(nil), plain...)
	f.aad = append([]byte(nil), aad...)
	return openbao.TransitCiphertext{Ciphertext: "vault:v1:dGVzdA==", KeyVersion: "1"}, nil
}
func (f *fakeTransit) TransitDecrypt(_ context.Context, _ string, cipher string, aad []byte) ([]byte, error) {
	if f.fail || string(aad) != string(f.aad) {
		return nil, errors.New("unavailable or AAD mismatch")
	}
	return append([]byte(nil), f.plain...), nil
}
