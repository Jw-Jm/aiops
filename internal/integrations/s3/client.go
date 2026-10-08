package s3

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	sdk "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"io"
	"net"
	"net/http"
	"net/url"
	"ops-platform/internal/archive"
	"strings"
	"time"
)

type Config struct {
	CACertBundle                                   []byte
	ServerName                                     string
	Endpoint, Region, Bucket, AccessKey, SecretKey string
	HTTPClient                                     *http.Client
	MaxObjectBytes                                 int64
}
type Client struct {
	client   *sdk.Client
	bucket   string
	maxBytes int64
}

func NewClient(c Config) (*Client, error) {
	u, err := url.Parse(c.Endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || c.Bucket == "" || strings.ContainsAny(c.Bucket, "/\\") || c.AccessKey == "" || c.SecretKey == "" {
		return nil, errors.New("explicit S3 endpoint, bucket, and credentials are required")
	}
	loopback := strings.EqualFold(u.Hostname(), "localhost")
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		loopback = ip.IsLoopback()
	}
	if u.Scheme == "http" && !loopback {
		return nil, errors.New("archive endpoint requires HTTPS outside loopback fixtures")
	}
	var roots *x509.CertPool
	if u.Scheme == "https" {
		roots = x509.NewCertPool()
		if len(c.CACertBundle) == 0 || strings.Contains(string(c.CACertBundle), "PRIVATE KEY") || !roots.AppendCertsFromPEM(c.CACertBundle) {
			return nil, errors.New("independent public archive CA is required")
		}
	}
	if c.Region == "" {
		c.Region = "us-east-1"
	}
	if c.MaxObjectBytes == 0 {
		c.MaxObjectBytes = 128 << 20
	}
	if c.MaxObjectBytes < 1 || c.MaxObjectBytes > 128<<20 {
		return nil, archive.ErrObjectTooLarge
	}
	h := c.HTTPClient
	if h == nil {
		h = &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 30 * time.Second}
	}
	hcopy := *h
	if u.Scheme == "https" {
		transport := &http.Transport{Proxy: nil}
		if h.Transport != nil {
			supplied, ok := h.Transport.(*http.Transport)
			if !ok {
				return nil, errors.New("archive HTTPS requires a verifiable TLS transport")
			}
			transport = supplied.Clone()
		}
		transport.Proxy = nil
		transport.TLSClientConfig = &tls.Config{RootCAs: roots, ServerName: c.ServerName, MinVersion: tls.VersionTLS12}
		hcopy.Transport = transport
	}
	hcopy.CheckRedirect = func(*http.Request, []*http.Request) error { return errors.New("S3 redirects are forbidden") }
	cfg := aws.Config{Region: c.Region, Credentials: aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(c.AccessKey, c.SecretKey, "")), HTTPClient: &hcopy, RetryMaxAttempts: 3}
	client := sdk.NewFromConfig(cfg, func(o *sdk.Options) {
		o.BaseEndpoint = aws.String(strings.TrimRight(c.Endpoint, "/"))
		o.UsePathStyle = true
	})
	return &Client{client: client, bucket: c.Bucket, maxBytes: c.MaxObjectBytes}, nil
}
func (c *Client) CreateBucket(ctx context.Context) error {
	_, createErr := c.client.CreateBucket(ctx, &sdk.CreateBucketInput{Bucket: aws.String(c.bucket), ObjectLockEnabledForBucket: aws.Bool(true)})
	if createErr != nil {
		if _, headErr := c.client.HeadBucket(ctx, &sdk.HeadBucketInput{Bucket: aws.String(c.bucket)}); headErr != nil {
			return errors.New("S3 bucket creation failed")
		}
	}
	if _, err := c.client.PutBucketVersioning(ctx, &sdk.PutBucketVersioningInput{Bucket: aws.String(c.bucket), VersioningConfiguration: &types.VersioningConfiguration{Status: types.BucketVersioningStatusEnabled}}); err != nil {
		return errors.New("S3 bucket versioning could not be enabled")
	}
	lock, err := c.client.GetObjectLockConfiguration(ctx, &sdk.GetObjectLockConfigurationInput{Bucket: aws.String(c.bucket)})
	if err != nil || lock.ObjectLockConfiguration == nil || lock.ObjectLockConfiguration.ObjectLockEnabled != types.ObjectLockEnabledEnabled {
		return errors.New("S3 bucket object lock is not enabled")
	}
	versioning, err := c.client.GetBucketVersioning(ctx, &sdk.GetBucketVersioningInput{Bucket: aws.String(c.bucket)})
	if err != nil || versioning.Status != types.BucketVersioningStatusEnabled {
		return errors.New("S3 bucket versioning is not enabled")
	}
	return nil
}

// CreateFreshBucket is the installation initializer, separate from historical
// fixture/recovery helpers. It never adopts an existing bucket or removes data
// after a partial initialization. Object-level protection remains authoritative.
func (c *Client) CreateFreshBucket(ctx context.Context) error {
	if _, err := c.client.HeadBucket(ctx, &sdk.HeadBucketInput{Bucket: aws.String(c.bucket)}); !isNotFound(err) {
		return errors.New("Archive bootstrap refuses existing or inaccessible bucket")
	}
	if _, err := c.client.CreateBucket(ctx, &sdk.CreateBucketInput{Bucket: aws.String(c.bucket), ObjectLockEnabledForBucket: aws.Bool(true)}); err != nil {
		return errors.New("Archive fresh bucket creation failed; no existing bucket adopted")
	}
	if _, err := c.client.PutBucketVersioning(ctx, &sdk.PutBucketVersioningInput{Bucket: aws.String(c.bucket), VersioningConfiguration: &types.VersioningConfiguration{Status: types.BucketVersioningStatusEnabled}}); err != nil {
		return errors.New("BOOTSTRAP_PARTIAL_BUCKET_RETAINED: Archive versioning initialization failed")
	}
	configuration := &types.ObjectLockConfiguration{ObjectLockEnabled: types.ObjectLockEnabledEnabled, Rule: &types.ObjectLockRule{DefaultRetention: &types.DefaultRetention{Mode: types.ObjectLockRetentionModeCompliance, Days: aws.Int32(365)}}}
	if _, err := c.client.PutObjectLockConfiguration(ctx, &sdk.PutObjectLockConfigurationInput{Bucket: aws.String(c.bucket), ObjectLockConfiguration: configuration}); err != nil {
		return errors.New("BOOTSTRAP_PARTIAL_BUCKET_RETAINED: Archive compliance default initialization failed")
	}
	lock, lockErr := c.client.GetObjectLockConfiguration(ctx, &sdk.GetObjectLockConfigurationInput{Bucket: aws.String(c.bucket)})
	versioning, versionErr := c.client.GetBucketVersioning(ctx, &sdk.GetBucketVersioningInput{Bucket: aws.String(c.bucket)})
	if lockErr != nil || versionErr != nil || versioning.Status != types.BucketVersioningStatusEnabled || lock.ObjectLockConfiguration == nil || lock.ObjectLockConfiguration.ObjectLockEnabled != types.ObjectLockEnabledEnabled || lock.ObjectLockConfiguration.Rule == nil || lock.ObjectLockConfiguration.Rule.DefaultRetention == nil || lock.ObjectLockConfiguration.Rule.DefaultRetention.Mode != types.ObjectLockRetentionModeCompliance || aws.ToInt32(lock.ObjectLockConfiguration.Rule.DefaultRetention.Days) != 365 || lock.ObjectLockConfiguration.Rule.DefaultRetention.Years != nil {
		return errors.New("BOOTSTRAP_PARTIAL_BUCKET_RETAINED: Archive versioning/compliance readback differs")
	}
	return nil
}
func (c *Client) Put(ctx context.Context, key string, body []byte, metadata map[string]string) (archive.Version, error) {
	retainUntil, parseErr := time.Parse(time.RFC3339Nano, metadata["retain-until"])
	if parseErr != nil || !retainUntil.After(time.Now()) {
		return archive.Version{}, archive.ErrInvalidObject
	}
	out, err := c.client.PutObject(ctx, &sdk.PutObjectInput{Bucket: aws.String(c.bucket), Key: aws.String(key), Body: bytes.NewReader(body), ContentLength: aws.Int64(int64(len(body))), ContentType: aws.String(metadata["content-type"]), Metadata: metadata, IfNoneMatch: aws.String("*"), ObjectLockMode: types.ObjectLockModeCompliance, ObjectLockRetainUntilDate: &retainUntil})
	if err != nil {
		// A timeout can arrive after the object was committed. Confirm the
		// exact immutable payload before returning success so segment recovery
		// can safely retry without replacing a different object.
		stored, getErr := c.Get(ctx, key, "")
		if getErr == nil && bytes.Equal(stored.Body, body) && equalMetadata(stored.Metadata, metadata) {
			return archive.Version{ETag: stored.ETag, VersionID: stored.VersionID}, nil
		}
		return archive.Version{}, errors.New("S3 object upload failed")
	}
	return archive.Version{ETag: aws.ToString(out.ETag), VersionID: aws.ToString(out.VersionId)}, nil
}
func (c *Client) Get(ctx context.Context, key, version string) (archive.StoredObject, error) {
	input := &sdk.GetObjectInput{Bucket: aws.String(c.bucket), Key: aws.String(key)}
	if version != "" {
		input.VersionId = aws.String(version)
	}
	out, err := c.client.GetObject(ctx, input)
	if err != nil {
		if isNotFound(err) {
			return archive.StoredObject{}, archive.ErrObjectNotFound
		}
		return archive.StoredObject{}, errors.New("S3 object download failed")
	}
	defer out.Body.Close()
	body, err := io.ReadAll(io.LimitReader(out.Body, c.maxBytes+1))
	if err != nil {
		return archive.StoredObject{}, errors.New("S3 object stream failed")
	}
	if int64(len(body)) > c.maxBytes {
		return archive.StoredObject{}, archive.ErrObjectTooLarge
	}
	return archive.StoredObject{Body: body, Metadata: out.Metadata, ETag: aws.ToString(out.ETag), VersionID: aws.ToString(out.VersionId), RetainUntil: aws.ToTime(out.ObjectLockRetainUntilDate), LegalHold: out.ObjectLockLegalHoldStatus == types.ObjectLockLegalHoldStatusOn}, nil
}
func (c *Client) Head(ctx context.Context, key, version string) (archive.StoredObject, error) {
	input := &sdk.HeadObjectInput{Bucket: aws.String(c.bucket), Key: aws.String(key)}
	if version != "" {
		input.VersionId = aws.String(version)
	}
	out, err := c.client.HeadObject(ctx, input)
	if err != nil {
		if isNotFound(err) {
			return archive.StoredObject{}, archive.ErrObjectNotFound
		}
		return archive.StoredObject{}, errors.New("S3 object metadata lookup failed")
	}
	return archive.StoredObject{Metadata: out.Metadata, ETag: aws.ToString(out.ETag), VersionID: aws.ToString(out.VersionId), RetainUntil: aws.ToTime(out.ObjectLockRetainUntilDate), LegalHold: out.ObjectLockLegalHoldStatus == types.ObjectLockLegalHoldStatusOn}, nil
}

func isNotFound(err error) bool {
	var apiError smithy.APIError
	if errors.As(err, &apiError) {
		if code := apiError.ErrorCode(); code == "NoSuchKey" || code == "NoSuchBucket" || code == "NotFound" || code == "404" {
			return true
		}
	}
	var responseError *smithyhttp.ResponseError
	return errors.As(err, &responseError) && responseError.HTTPStatusCode() == http.StatusNotFound
}

func equalMetadata(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range b {
		if a[key] != value {
			return false
		}
	}
	return true
}
func (c *Client) Delete(ctx context.Context, key, version string) error {
	input := &sdk.DeleteObjectInput{Bucket: aws.String(c.bucket), Key: aws.String(key)}
	if version != "" {
		input.VersionId = aws.String(version)
	}
	_, err := c.client.DeleteObject(ctx, input)
	if err != nil {
		return errors.New("S3 object delete failed")
	}
	return nil
}

func (c *Client) ProtectObject(ctx context.Context, key, version string, until time.Time, hold bool) error {
	if version == "" || until.IsZero() {
		return archive.ErrInvalidObject
	}
	actual, err := c.Head(ctx, key, version)
	if err != nil {
		return err
	}
	return c.protectKnownObject(ctx, key, version, until, hold, actual.RetainUntil)
}

func (c *Client) protectKnownObject(ctx context.Context, key, version string, until time.Time, hold bool, actualRetainUntil time.Time) error {
	if version == "" || until.IsZero() {
		return archive.ErrInvalidObject
	}
	var err error
	if until.After(actualRetainUntil) {
		_, err = c.client.PutObjectRetention(ctx, &sdk.PutObjectRetentionInput{Bucket: aws.String(c.bucket), Key: aws.String(key), VersionId: aws.String(version), Retention: &types.ObjectLockRetention{Mode: types.ObjectLockRetentionModeCompliance, RetainUntilDate: &until}})
		if err != nil {
			return errors.New("S3 immutable retention extension failed")
		}
	}
	status := types.ObjectLockLegalHoldStatusOff
	if hold {
		status = types.ObjectLockLegalHoldStatusOn
	}
	_, err = c.client.PutObjectLegalHold(ctx, &sdk.PutObjectLegalHoldInput{Bucket: aws.String(c.bucket), Key: aws.String(key), VersionId: aws.String(version), LegalHold: &types.ObjectLockLegalHold{Status: status}})
	if err != nil {
		return errors.New("S3 version Legal Hold failed")
	}
	return nil
}
