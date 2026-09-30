package archive

import (
	"bytes"
	"context"
	"errors"
	"github.com/google/uuid"
	"testing"
	"time"
)

func TestArchiveBindsTenantDigestAndRetention(t *testing.T) {
	backend := &memoryBackend{objects: map[string]StoredObject{}}
	store, err := NewStore(backend, 1024)
	if err != nil {
		t.Fatal(err)
	}
	tenant, object := uuid.New(), uuid.New()
	desc := ObjectDescriptor{TenantID: tenant, ObjectID: object, Category: "evidence", ContentType: "application/octet-stream", RetainUntil: time.Now().Add(time.Hour)}
	ref, err := store.Put(context.Background(), desc, bytes.NewBufferString("ciphertext-only"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(context.Background(), tenant, ref)
	if err != nil || string(got) != "ciphertext-only" {
		t.Fatalf("archive roundtrip: %q %v", got, err)
	}
	existing, found, err := store.Find(context.Background(), tenant, object, "evidence")
	if err != nil || !found || existing.Digest != ref.Digest || existing.Category != ref.Category {
		t.Fatalf("find immutable archive object: found=%v ref=%#v err=%v", found, existing, err)
	}
	if _, err := store.Get(context.Background(), uuid.New(), ref); !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("cross tenant: %v", err)
	}
	forged := ref
	forged.Key = "tenants/other/evidence/" + object.String()
	if _, err := store.Get(context.Background(), tenant, forged); !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("forged prefix: %v", err)
	}
	forged = ref
	forged.Category = "other"
	if _, err := store.Get(context.Background(), tenant, forged); !errors.Is(err, ErrTenantMismatch) {
		t.Fatalf("forged category: %v", err)
	}
	if err := store.Delete(context.Background(), tenant, ref); !errors.Is(err, ErrRetentionActive) {
		t.Fatalf("retention bypass: %v", err)
	}
	bad := desc
	bad.ObjectID = uuid.New()
	bad.ExpectedDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := store.Put(context.Background(), bad, bytes.NewBufferString("mismatch")); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("digest mismatch: %v", err)
	}
	stored := backend.objects[ref.Key]
	stored.Body = []byte("tampered")
	backend.objects[ref.Key] = stored
	if _, err := store.Get(context.Background(), tenant, ref); !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("tampered object: %v", err)
	}
}

type memoryBackend struct{ objects map[string]StoredObject }

func (m *memoryBackend) Put(_ context.Context, key string, body []byte, metadata map[string]string) (Version, error) {
	m.objects[key] = StoredObject{Body: append([]byte(nil), body...), Metadata: metadata}
	return Version{}, nil
}
func (m *memoryBackend) Get(_ context.Context, key, version string) (StoredObject, error) {
	return m.objects[key], nil
}
func (m *memoryBackend) Head(_ context.Context, key, version string) (StoredObject, error) {
	return m.objects[key], nil
}
func (m *memoryBackend) Delete(_ context.Context, key, version string) error {
	delete(m.objects, key)
	return nil
}
