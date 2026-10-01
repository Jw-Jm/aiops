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

type corruptUploadBackend struct {
	memoryBackend
	mutate func(*StoredObject)
}

func (b *corruptUploadBackend) Put(ctx context.Context, key string, body []byte, metadata map[string]string) (Version, error) {
	version, err := b.memoryBackend.Put(ctx, key, body, metadata)
	stored := b.objects[key]
	b.mutate(&stored)
	b.objects[key] = stored
	return version, err
}

func TestArchiveUploadMustVerifyStoredBytesAndMetadata(t *testing.T) {
	for name, mutate := range map[string]func(*StoredObject){
		"bytes":  func(object *StoredObject) { object.Body = []byte("corrupted") },
		"tenant": func(object *StoredObject) { object.Metadata["tenant-id"] = uuid.NewString() },
		"retention": func(object *StoredObject) {
			object.Metadata["retain-until"] = time.Now().UTC().Format(time.RFC3339Nano)
		},
		"content type": func(object *StoredObject) { object.Metadata["content-type"] = "text/plain" },
	} {
		t.Run(name, func(t *testing.T) {
			backend := &corruptUploadBackend{memoryBackend: memoryBackend{objects: map[string]StoredObject{}}, mutate: mutate}
			store, err := NewStore(backend, 1024)
			if err != nil {
				t.Fatal(err)
			}
			_, err = store.Put(context.Background(), ObjectDescriptor{TenantID: uuid.New(), ObjectID: uuid.New(), Category: "audit-segment", ContentType: "application/json", RetainUntil: time.Now().Add(time.Hour)}, bytes.NewBufferString("ciphertext"))
			if err == nil {
				t.Fatal("successful upload response concealed corrupt stored object")
			}
		})
	}
}

func TestArchiveDeleteHonorsReferenceRetentionWhenBackendMetadataIsShorter(t *testing.T) {
	backend := &memoryBackend{objects: map[string]StoredObject{}}
	store, err := NewStore(backend, 1024)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := store.Put(context.Background(), ObjectDescriptor{TenantID: uuid.New(), ObjectID: uuid.New(), Category: "audit-segment", RetainUntil: time.Now().Add(time.Hour)}, bytes.NewBufferString("ciphertext"))
	if err != nil {
		t.Fatal(err)
	}
	stored := backend.objects[ref.Key]
	stored.Metadata["retain-until"] = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	backend.objects[ref.Key] = stored
	if err := store.Delete(context.Background(), ref.TenantID, ref); !errors.Is(err, ErrRetentionActive) {
		t.Fatalf("short backend retention bypassed active reference retention: %v", err)
	}
}

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
