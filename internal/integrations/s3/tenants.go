package s3

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	sdk "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/google/uuid"
	"ops-platform/internal/archive"
)

var ErrTenantIAM = errors.New("archive tenant IAM configuration or scope is invalid")

type TenantCredential struct {
	TenantID  uuid.UUID `json:"tenantId"`
	AccessKey string    `json:"accessKey"`
	SecretKey string    `json:"secretKey"`
}
type TenantCredentials struct {
	SchemaVersion string             `json:"schemaVersion"`
	Tenants       []TenantCredential `json:"tenants"`
}
type tenantClient struct {
	client     *Client
	mu         sync.Mutex
	verifiedAt time.Time
}

// TenantClient routes immutable tenant prefixes to distinct IAM principals. It
// has no global credential fallback. Credential provisioning is operator owned.
type TenantClient struct{ tenants map[string]*tenantClient }

// LoadTenantClient bounds reads even when the source is an operator-managed file.
func LoadTenantClient(c Config, path string) (*TenantClient, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, ErrTenantIAM
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil {
		return nil, ErrTenantIAM
	}
	return NewTenantClient(c, raw)
}

func NewTenantClient(c Config, raw []byte) (*TenantClient, error) {
	if len(raw) == 0 || len(raw) > 1<<20 {
		return nil, ErrTenantIAM
	}
	strict := jsontext.NewDecoder(bytes.NewReader(raw), jsontext.AllowDuplicateNames(false), jsontext.AllowInvalidUTF8(false))
	if _, err := strict.ReadValue(); err != nil {
		return nil, ErrTenantIAM
	}
	if _, err := strict.ReadValue(); !errors.Is(err, io.EOF) {
		return nil, ErrTenantIAM
	}
	var credentials TenantCredentials
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&credentials) != nil || credentials.SchemaVersion != "ops-archive-credentials/v1" || len(credentials.Tenants) == 0 || len(credentials.Tenants) > 4096 {
		return nil, ErrTenantIAM
	}
	out := &TenantClient{tenants: map[string]*tenantClient{}}
	accessKeys := map[string]bool{}
	for _, credential := range credentials.Tenants {
		if credential.TenantID == uuid.Nil || len(credential.AccessKey) == 0 || len(credential.AccessKey) > 256 || len(credential.SecretKey) == 0 || len(credential.SecretKey) > 1024 || accessKeys[credential.AccessKey] {
			return nil, ErrTenantIAM
		}
		prefix := archive.TenantPrefix(credential.TenantID)
		if _, exists := out.tenants[prefix]; exists {
			return nil, ErrTenantIAM
		}
		accessKeys[credential.AccessKey] = true
		config := c
		config.AccessKey = credential.AccessKey
		config.SecretKey = credential.SecretKey
		client, err := NewClient(config)
		if err != nil {
			return nil, ErrTenantIAM
		}
		out.tenants[prefix] = &tenantClient{client: client}
	}
	return out, nil
}

// VerifyTenantScope requires positive own-prefix access and explicit server IAM
// denials for unscoped listing, foreign-prefix listing and foreign object reads.
// A missing object (404), a timeout or a successful empty list is not a denial.
func (c *Client) VerifyTenantScope(ctx context.Context, prefix string) error {
	if !validTenantPrefix(prefix) {
		return ErrTenantIAM
	}
	if _, err := c.client.ListObjectsV2(ctx, &sdk.ListObjectsV2Input{Bucket: aws.String(c.bucket), Prefix: aws.String(prefix), MaxKeys: aws.Int32(1)}); err != nil {
		return ErrTenantIAM
	}
	digest := sha256.Sum256([]byte("ops-archive/iam-probe|" + prefix))
	foreign := "tenants/" + hex.EncodeToString(digest[:]) + "/"
	for _, other := range []string{"", foreign} {
		_, err := c.client.ListObjectsV2(ctx, &sdk.ListObjectsV2Input{Bucket: aws.String(c.bucket), Prefix: aws.String(other), MaxKeys: aws.Int32(1)})
		if !accessDenied(err) {
			return ErrTenantIAM
		}
	}
	object, err := c.client.GetObject(ctx, &sdk.GetObjectInput{Bucket: aws.String(c.bucket), Key: aws.String(foreign + "iam-scope-probe")})
	if object != nil && object.Body != nil {
		object.Body.Close()
	}
	if !accessDenied(err) {
		return ErrTenantIAM
	}
	return nil
}
func accessDenied(err error) bool {
	var api smithy.APIError
	var response *smithyhttp.ResponseError
	return errors.As(err, &api) && api.ErrorCode() == "AccessDenied" && errors.As(err, &response) && response.HTTPStatusCode() == 403
}
func validTenantPrefix(prefix string) bool {
	if len(prefix) != 73 || !strings.HasPrefix(prefix, "tenants/") || !strings.HasSuffix(prefix, "/") {
		return false
	}
	_, err := hex.DecodeString(prefix[8:72])
	return err == nil && prefix == strings.ToLower(prefix)
}
func (c *TenantClient) scoped(ctx context.Context, key string) (*Client, error) {
	if len(key) < 73 || !validTenantPrefix(key[:73]) {
		return nil, archive.ErrTenantMismatch
	}
	tenant := c.tenants[key[:73]]
	if tenant == nil {
		return nil, ErrTenantIAM
	}
	tenant.mu.Lock()
	defer tenant.mu.Unlock()
	if time.Since(tenant.verifiedAt) >= time.Minute {
		if err := tenant.client.VerifyTenantScope(ctx, key[:73]); err != nil {
			return nil, err
		}
		tenant.verifiedAt = time.Now()
	}
	return tenant.client, nil
}
func (c *TenantClient) Put(ctx context.Context, key string, body []byte, metadata map[string]string) (archive.Version, error) {
	client, err := c.scoped(ctx, key)
	if err != nil {
		return archive.Version{}, err
	}
	return client.Put(ctx, key, body, metadata)
}
func (c *TenantClient) Get(ctx context.Context, key, version string) (archive.StoredObject, error) {
	client, err := c.scoped(ctx, key)
	if err != nil {
		return archive.StoredObject{}, err
	}
	return client.Get(ctx, key, version)
}
func (c *TenantClient) Head(ctx context.Context, key, version string) (archive.StoredObject, error) {
	client, err := c.scoped(ctx, key)
	if err != nil {
		return archive.StoredObject{}, err
	}
	return client.Head(ctx, key, version)
}
func (c *TenantClient) Delete(ctx context.Context, key, version string) error {
	client, err := c.scoped(ctx, key)
	if err != nil {
		return err
	}
	return client.Delete(ctx, key, version)
}
