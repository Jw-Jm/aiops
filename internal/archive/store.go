package archive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	ErrTenantMismatch  = errors.New("archive tenant or object key mismatch")
	ErrDigestMismatch  = errors.New("archive object digest mismatch")
	ErrRetentionActive = errors.New("archive retention period is active")
	ErrObjectTooLarge  = errors.New("archive object exceeds size limit")
	ErrInvalidObject   = errors.New("archive object descriptor is invalid")
	ErrObjectNotFound  = errors.New("archive object not found")
	digestPattern      = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	categoryPattern    = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
)

type ObjectDescriptor struct {
	TenantID       uuid.UUID
	ObjectID       uuid.UUID
	Category       string
	ContentType    string
	ExpectedDigest string
	RetainUntil    time.Time
}
type ObjectRef struct {
	TenantID    uuid.UUID `json:"tenantId"`
	ObjectID    uuid.UUID `json:"objectId"`
	Category    string    `json:"category"`
	Key         string    `json:"key"`
	Digest      string    `json:"digest"`
	Size        int64     `json:"size"`
	ContentType string    `json:"contentType"`
	VersionID   string    `json:"versionId,omitempty"`
	ETag        string    `json:"etag,omitempty"`
	RetainUntil time.Time `json:"retainUntil"`
}
type Version struct {
	VersionID string
	ETag      string
}
type StoredObject struct {
	Body      []byte
	Metadata  map[string]string
	ETag      string
	VersionID string
}
type Backend interface {
	Put(context.Context, string, []byte, map[string]string) (Version, error)
	Get(context.Context, string, string) (StoredObject, error)
	Head(context.Context, string, string) (StoredObject, error)
	Delete(context.Context, string, string) error
}
type Store struct {
	backend  Backend
	maxBytes int64
	now      func() time.Time
}

func NewStore(backend Backend, maxBytes int64) (*Store, error) {
	if backend == nil {
		return nil, ErrInvalidObject
	}
	if maxBytes == 0 {
		maxBytes = 32 << 20
	}
	if maxBytes < 1 || maxBytes > 128<<20 {
		return nil, ErrInvalidObject
	}
	return &Store{backend: backend, maxBytes: maxBytes, now: time.Now}, nil
}
func tenantPrefix(id uuid.UUID) string {
	sum := sha256.Sum256([]byte("ops-archive/v1|" + id.String()))
	return "tenants/" + hex.EncodeToString(sum[:]) + "/"
}
func (s *Store) Put(ctx context.Context, d ObjectDescriptor, r io.Reader) (ObjectRef, error) {
	if d.TenantID == uuid.Nil || d.ObjectID == uuid.Nil || !categoryPattern.MatchString(d.Category) || r == nil || d.RetainUntil.IsZero() || !d.RetainUntil.After(s.now()) || strings.ContainsAny(d.ContentType, "\r\n") || (d.ExpectedDigest != "" && !digestPattern.MatchString(d.ExpectedDigest)) {
		return ObjectRef{}, ErrInvalidObject
	}
	body, err := io.ReadAll(io.LimitReader(r, s.maxBytes+1))
	if err != nil {
		return ObjectRef{}, err
	}
	if int64(len(body)) > s.maxBytes {
		return ObjectRef{}, ErrObjectTooLarge
	}
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	if d.ExpectedDigest != "" && d.ExpectedDigest != digest {
		return ObjectRef{}, ErrDigestMismatch
	}
	key := tenantPrefix(d.TenantID) + d.Category + "/" + d.ObjectID.String()
	metadata := map[string]string{"tenant-id": d.TenantID.String(), "object-id": d.ObjectID.String(), "category": d.Category, "sha256": digest, "size": fmt.Sprintf("%d", len(body)), "retain-until": d.RetainUntil.UTC().Format(time.RFC3339Nano), "content-type": d.ContentType}
	version, err := s.backend.Put(ctx, key, body, metadata)
	if err != nil {
		return ObjectRef{}, err
	}
	return ObjectRef{TenantID: d.TenantID, ObjectID: d.ObjectID, Category: d.Category, Key: key, Digest: digest, Size: int64(len(body)), ContentType: d.ContentType, VersionID: version.VersionID, ETag: version.ETag, RetainUntil: d.RetainUntil.UTC()}, nil
}
func validateRef(tenant uuid.UUID, ref ObjectRef) error {
	if tenant == uuid.Nil || ref.TenantID != tenant || ref.ObjectID == uuid.Nil || !categoryPattern.MatchString(ref.Category) || ref.Key != tenantPrefix(tenant)+ref.Category+"/"+ref.ObjectID.String() || strings.Contains(ref.Key, "..") {
		return ErrTenantMismatch
	}
	if !digestPattern.MatchString(ref.Digest) || ref.Size < 0 {
		return ErrInvalidObject
	}
	return nil
}

// Find returns the immutable object bound to a tenant, category and UUID. It
// supports crash recovery when the S3 upload succeeded but its response or the
// database signature commit was lost.
func (s *Store) Find(ctx context.Context, tenant, object uuid.UUID, category string) (ObjectRef, bool, error) {
	if tenant == uuid.Nil || object == uuid.Nil || !categoryPattern.MatchString(category) {
		return ObjectRef{}, false, ErrInvalidObject
	}
	key := tenantPrefix(tenant) + category + "/" + object.String()
	stored, err := s.backend.Head(ctx, key, "")
	if err != nil {
		if errors.Is(err, ErrObjectNotFound) {
			return ObjectRef{}, false, nil
		}
		return ObjectRef{}, false, err
	}
	if len(stored.Metadata) == 0 {
		return ObjectRef{}, false, nil
	}
	if stored.Metadata["tenant-id"] != tenant.String() || stored.Metadata["object-id"] != object.String() || stored.Metadata["category"] != category {
		return ObjectRef{}, false, ErrTenantMismatch
	}
	size, err := strconv.ParseInt(stored.Metadata["size"], 10, 64)
	if err != nil || size < 0 || size > s.maxBytes || !digestPattern.MatchString(stored.Metadata["sha256"]) {
		return ObjectRef{}, false, ErrInvalidObject
	}
	retainUntil, err := time.Parse(time.RFC3339Nano, stored.Metadata["retain-until"])
	if err != nil {
		return ObjectRef{}, false, ErrInvalidObject
	}
	ref := ObjectRef{TenantID: tenant, ObjectID: object, Category: category, Key: key, Digest: stored.Metadata["sha256"], Size: size, ContentType: stored.Metadata["content-type"], VersionID: stored.VersionID, ETag: stored.ETag, RetainUntil: retainUntil}
	if err := validateRef(tenant, ref); err != nil {
		return ObjectRef{}, false, err
	}
	if _, err := s.Get(ctx, tenant, ref); err != nil {
		return ObjectRef{}, false, err
	}
	return ref, true, nil
}
func (s *Store) Get(ctx context.Context, tenant uuid.UUID, ref ObjectRef) ([]byte, error) {
	if err := validateRef(tenant, ref); err != nil {
		return nil, err
	}
	object, err := s.backend.Get(ctx, ref.Key, ref.VersionID)
	if err != nil {
		return nil, err
	}
	if object.Metadata["tenant-id"] != tenant.String() || object.Metadata["object-id"] != ref.ObjectID.String() || object.Metadata["category"] != ref.Category {
		return nil, ErrTenantMismatch
	}
	sum := sha256.Sum256(object.Body)
	if int64(len(object.Body)) != ref.Size || int64(len(object.Body)) > s.maxBytes || "sha256:"+hex.EncodeToString(sum[:]) != ref.Digest || object.Metadata["sha256"] != ref.Digest || object.Metadata["size"] != strconv.FormatInt(ref.Size, 10) {
		return nil, ErrDigestMismatch
	}
	return object.Body, nil
}
func (s *Store) Delete(ctx context.Context, tenant uuid.UUID, ref ObjectRef) error {
	if err := validateRef(tenant, ref); err != nil {
		return err
	}
	object, err := s.backend.Head(ctx, ref.Key, ref.VersionID)
	if err != nil {
		return err
	}
	if object.Metadata["tenant-id"] != tenant.String() || object.Metadata["object-id"] != ref.ObjectID.String() || object.Metadata["category"] != ref.Category {
		return ErrTenantMismatch
	}
	until, err := time.Parse(time.RFC3339Nano, object.Metadata["retain-until"])
	if err != nil {
		return ErrInvalidObject
	}
	if s.now().Before(until) {
		return ErrRetentionActive
	}
	return s.backend.Delete(ctx, ref.Key, ref.VersionID)
}
