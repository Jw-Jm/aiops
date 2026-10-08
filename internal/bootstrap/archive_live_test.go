//go:build pre_sp07_live

package bootstrap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	sdk "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/google/uuid"
	"ops-platform/internal/archive"
	platforms3 "ops-platform/internal/integrations/s3"
)

func TestFormalArchiveBootstrapActualIAMAndCompliance(t *testing.T) {
	inputPath, caPath, address, endpoint := os.Getenv("PRE_SP07_ARCHIVE_IAM_INPUT"), os.Getenv("PRE_SP07_ARCHIVE_CA"), os.Getenv("PRE_SP07_ARCHIVE_LOOPBACK"), os.Getenv("PRE_SP07_ARCHIVE_ENDPOINT")
	if inputPath == "" || caPath == "" || address == "" || endpoint == "" {
		t.Fatal("actual dedicated Archive, explicit private inputs and pinned HTTPS tunnel required")
	}
	raw, err := os.ReadFile(inputPath)
	var input ArchiveIAMInput
	if err != nil || json.Unmarshal(raw, &input) != nil || len(input.Credentials.Tenants) != 1 {
		t.Fatal("actual private Archive credentials unavailable")
	}
	if _, err := BuildArchiveIAM(input); err != nil {
		t.Fatal("actual Archive IAM input failed formal validation")
	}
	ca, err := os.ReadFile(caPath)
	roots := x509.NewCertPool()
	u, parseErr := url.Parse(endpoint)
	host, _, addressErr := net.SplitHostPort(address)
	if err != nil || !roots.AppendCertsFromPEM(ca) || parseErr != nil || u.Scheme != "https" || u.Port() != "8333" || addressErr != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		t.Fatal("explicit Archive TLS input invalid")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: u.Hostname()}, DialContext: func(ctx context.Context, network, target string) (net.Conn, error) {
		if target != u.Host {
			return nil, errors.New("Archive probe route outside pinned target")
		}
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, address)
	}}
	defer transport.CloseIdleConnections()
	httpClient := &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	native := func(principal platforms3.RoleCredential) *sdk.Client {
		return sdk.New(sdk.Options{Region: "us-east-1", BaseEndpoint: aws.String(endpoint), UsePathStyle: true, Credentials: credentials.NewStaticCredentialsProvider(principal.AccessKey, principal.SecretKey, ""), HTTPClient: httpClient, RetryMaxAttempts: 1})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	admin := native(input.BootstrapAdmin)
	versioning, err := admin.GetBucketVersioning(ctx, &sdk.GetBucketVersioningInput{Bucket: aws.String(input.Bucket)})
	if err != nil || versioning.Status != types.BucketVersioningStatusEnabled {
		t.Fatal("actual Archive versioning differs")
	}
	lock, err := admin.GetObjectLockConfiguration(ctx, &sdk.GetObjectLockConfigurationInput{Bucket: aws.String(input.Bucket)})
	if err != nil || lock.ObjectLockConfiguration == nil || lock.ObjectLockConfiguration.Rule == nil || lock.ObjectLockConfiguration.Rule.DefaultRetention == nil || lock.ObjectLockConfiguration.Rule.DefaultRetention.Mode != types.ObjectLockRetentionModeCompliance || aws.ToInt32(lock.ObjectLockConfiguration.Rule.DefaultRetention.Days) != 365 {
		t.Fatal("actual default 365-day compliance differs")
	}
	tenant := input.Credentials.Tenants[0]
	roles := tenant.Roles
	principals := map[string]platforms3.RoleCredential{"write": roles.Write, "read": roles.Read, "protect": roles.Protect, "cleanup": roles.Cleanup}
	prefix := archive.TenantPrefix(tenant.TenantID)
	key := prefix + "initialization/" + uuid.NewString()
	body := []byte("actual dedicated Archive protected operation proof " + uuid.NewString())
	now := time.Now()
	created, err := native(roles.Write).PutObject(ctx, &sdk.PutObjectInput{Bucket: aws.String(input.Bucket), Key: aws.String(key), Body: bytes.NewReader(body)})
	if err != nil || aws.ToString(created.VersionId) == "" {
		t.Fatal("actual write role failed")
	}
	version := created.VersionId
	read, err := native(roles.Read).GetObject(ctx, &sdk.GetObjectInput{Bucket: aws.String(input.Bucket), Key: aws.String(key), VersionId: version})
	if err != nil {
		t.Fatal("actual read role failed")
	}
	got, err := io.ReadAll(read.Body)
	read.Body.Close()
	if err != nil || !bytes.Equal(got, body) || read.ObjectLockMode != types.ObjectLockModeCompliance || read.ObjectLockRetainUntilDate == nil || read.ObjectLockRetainUntilDate.Before(now.Add(365*24*time.Hour-time.Minute)) {
		t.Fatal("actual immutable object/default retention readback differs")
	}
	_, err = native(roles.Protect).PutObjectRetention(ctx, &sdk.PutObjectRetentionInput{Bucket: aws.String(input.Bucket), Key: aws.String(key), VersionId: version, Retention: &types.ObjectLockRetention{Mode: types.ObjectLockRetentionModeCompliance, RetainUntilDate: aws.Time(now.Add(365*24*time.Hour + time.Hour))}})
	if err != nil {
		t.Fatal("actual protection role cannot extend compliance")
	}
	_, err = native(roles.Protect).PutObjectLegalHold(ctx, &sdk.PutObjectLegalHoldInput{Bucket: aws.String(input.Bucket), Key: aws.String(key), VersionId: version, LegalHold: &types.ObjectLockLegalHold{Status: types.ObjectLockLegalHoldStatusOn}})
	if err != nil {
		t.Fatal("actual protection role cannot establish Legal Hold")
	}
	denied := func(role, operation string, err error) {
		var api smithy.APIError
		var response *smithyhttp.ResponseError
		if !errors.As(err, &api) || api.ErrorCode() != "AccessDenied" || !errors.As(err, &response) || response.HTTPStatusCode() != 403 {
			t.Fatalf("actual %s %s lacks explicit source IAM/protection 403: %v", role, operation, err)
		}
	}
	for role, principal := range principals {
		client := native(principal)
		if role != "write" {
			_, err := client.PutObject(ctx, &sdk.PutObjectInput{Bucket: aws.String(input.Bucket), Key: aws.String(key + "-forbidden-" + role), Body: bytes.NewReader(body)})
			denied(role, "write", err)
		}
		if role != "read" {
			out, err := client.GetObject(ctx, &sdk.GetObjectInput{Bucket: aws.String(input.Bucket), Key: aws.String(key), VersionId: version})
			if out != nil && out.Body != nil {
				out.Body.Close()
			}
			denied(role, "read", err)
		}
		if role != "protect" {
			_, err := client.PutObjectLegalHold(ctx, &sdk.PutObjectLegalHoldInput{Bucket: aws.String(input.Bucket), Key: aws.String(key), VersionId: version, LegalHold: &types.ObjectLockLegalHold{Status: types.ObjectLockLegalHoldStatusOn}})
			denied(role, "protection", err)
		}
		_, err := client.DeleteObject(ctx, &sdk.DeleteObjectInput{Bucket: aws.String(input.Bucket), Key: aws.String(key), VersionId: version})
		denied(role, "protected deletion", err)
		scoped, err := platforms3.NewClient(platforms3.Config{Endpoint: endpoint, CACertBundle: ca, ServerName: u.Hostname(), Bucket: input.Bucket, AccessKey: principal.AccessKey, SecretKey: principal.SecretKey, HTTPClient: httpClient})
		if err != nil {
			t.Fatal(err)
		}
		if err := scoped.VerifyTenantScope(ctx, prefix); err != nil {
			t.Fatalf("actual %s own-prefix positive/unscoped/foreign 403 failed: %v", role, err)
		}
	}
	final, err := native(roles.Read).GetObject(ctx, &sdk.GetObjectInput{Bucket: aws.String(input.Bucket), Key: aws.String(key), VersionId: version})
	if err != nil {
		t.Fatal("protected version missing after rejected deletions")
	}
	got, err = io.ReadAll(final.Body)
	final.Body.Close()
	if err != nil || !bytes.Equal(body, got) || final.ObjectLockLegalHoldStatus != types.ObjectLockLegalHoldStatusOn {
		t.Fatal("protected object/digest/Legal Hold changed")
	}
	t.Logf("actual HTTPS pinned Archive: tenant=%s key=%s version=%s digest=%x; native four-role scope denials, 365-day compliance extension, Legal Hold and deletion refusal; object/storage retained", tenant.TenantID, key, aws.ToString(version), sha256.Sum256(body))
}
