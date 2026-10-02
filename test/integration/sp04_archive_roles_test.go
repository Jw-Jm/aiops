package integration

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
	"github.com/google/uuid"
	"net/http"
	"ops-platform/internal/archive"
	"ops-platform/internal/integrations/s3"
	"os"
	"testing"
	"time"
)

func TestSP04ArchiveCredentialRolesAtActualIAM(t *testing.T) {
	if os.Getenv("SP04_TEST_ORBSTACK") != "1" {
		t.Skip("explicit owned fixture opt-in required")
	}
	tenant := uuid.New()
	bucket := "sp04-roles-" + uuid.NewString()
	fixture := newRoleTenantS3Fixture(t, []uuid.UUID{tenant}, bucket)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	client, err := s3.LoadTenantClient(s3.Config{Endpoint: fixture.Endpoint, CACertBundle: fixture.CA, Bucket: bucket}, fixture.CredentialFile)
	if err != nil || !client.RoleSeparated() {
		t.Fatalf("separated credentials unavailable: %v", err)
	}
	roles := fixture.Credentials.Tenants[0].Roles
	principals := map[string]s3.RoleCredential{"write": roles.Write, "read": roles.Read, "protect": roles.Protect, "cleanup": roles.Cleanup}
	now := time.Now().UTC()
	key := archive.TenantPrefix(tenant) + "evidence/role-probe"
	metadata := map[string]string{"retain-until": now.Add(time.Hour).Format(time.RFC3339Nano)}
	version, err := client.Put(ctx, key, []byte("sealed-role-fixture"), metadata)
	if err != nil || version.VersionID == "" {
		t.Fatalf("separated upload failed: %v", err)
	}
	if _, err := client.Get(ctx, key, version.VersionID); err != nil {
		t.Fatalf("read role failed: %v", err)
	}
	if err := client.ProtectObject(ctx, key, version.VersionID, now.Add(2*time.Hour), true); err != nil {
		t.Fatalf("protect role failed: %v", err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(fixture.CA)
	native := func(principal s3.RoleCredential) *sdk.Client {
		return sdk.New(sdk.Options{Region: "us-east-1", BaseEndpoint: aws.String(fixture.Endpoint), UsePathStyle: true, Credentials: credentials.NewStaticCredentialsProvider(principal.AccessKey, principal.SecretKey, ""), HTTPClient: &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots}}}})
	}
	denied := func(name, operation string, err error) {
		var api smithy.APIError
		var response *smithyhttp.ResponseError
		if !errors.As(err, &api) || api.ErrorCode() != "AccessDenied" || !errors.As(err, &response) || response.HTTPStatusCode() != 403 {
			t.Fatalf("%s %s did not receive native IAM 403 AccessDenied: %v", name, operation, err)
		}
	}
	mutableKey := key + "-cleanup-positive"
	uploaded, err := native(roles.Write).PutObject(ctx, &sdk.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(mutableKey), Body: bytes.NewReader([]byte("isolated unprotected IAM operation probe"))})
	if err != nil || aws.ToString(uploaded.VersionId) == "" {
		t.Fatalf("native write positive failed: %v", err)
	}
	denials := 0
	for name, principal := range principals {
		direct := native(principal)
		if name != "write" {
			_, err := direct.PutObject(ctx, &sdk.PutObjectInput{Bucket: aws.String(bucket), Key: aws.String(key + "-" + name), Body: bytes.NewReader([]byte("forbidden"))})
			denied(name, "upload", err)
			denials++
		}
		if name != "read" {
			object, err := direct.GetObject(ctx, &sdk.GetObjectInput{Bucket: aws.String(bucket), Key: aws.String(key), VersionId: aws.String(version.VersionID)})
			if object != nil && object.Body != nil {
				object.Body.Close()
			}
			denied(name, "read", err)
			denials++
		}
		if name != "protect" {
			_, err := direct.PutObjectLegalHold(ctx, &sdk.PutObjectLegalHoldInput{Bucket: aws.String(bucket), Key: aws.String(key), VersionId: aws.String(version.VersionID), LegalHold: &types.ObjectLockLegalHold{Status: types.ObjectLockLegalHoldStatusOff}})
			denied(name, "Legal Hold", err)
			denials++
			until := now.Add(3 * time.Hour)
			_, err = direct.PutObjectRetention(ctx, &sdk.PutObjectRetentionInput{Bucket: aws.String(bucket), Key: aws.String(key), VersionId: aws.String(version.VersionID), Retention: &types.ObjectLockRetention{Mode: types.ObjectLockRetentionModeCompliance, RetainUntilDate: &until}})
			denied(name, "retention", err)
			denials++
		}
		if name != "cleanup" {
			_, err := direct.DeleteObject(ctx, &sdk.DeleteObjectInput{Bucket: aws.String(bucket), Key: aws.String(mutableKey), VersionId: uploaded.VersionId})
			denied(name, "cleanup", err)
			denials++
		}
		scoped, err := s3.NewClient(s3.Config{Endpoint: fixture.Endpoint, CACertBundle: fixture.CA, Bucket: bucket, AccessKey: principal.AccessKey, SecretKey: principal.SecretKey})
		if err != nil {
			t.Fatal(err)
		}
		if err := scoped.VerifyTenantScope(ctx, archive.TenantPrefix(tenant)); err != nil {
			t.Fatalf("%s tenant isolation proof failed: %v", name, err)
		}
	}
	if err := client.Delete(ctx, mutableKey, aws.ToString(uploaded.VersionId)); err != nil {
		t.Fatalf("native cleaner positive failed: %v", err)
	}
	t.Logf("native cross-role IAM 403 AccessDenied samples=%d; positive upload/read/protection/cleanup and per-role tenant isolation verified", denials)

	// Compliance retention is a separate guard; the cleaner cannot remove a
	// protected object even with its correctly separated cleanup permission.
	if err := client.Delete(ctx, key, version.VersionID); err == nil {
		t.Fatal("cleaner bypassed compliance retention and Legal Hold")
	}
	t.Log("four distinct IAM principals: write/read/protect/cleanup; role routing positive, cross-role and cross-tenant denials, immutable protection verified")
}
