package integration

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/archive"
	"ops-platform/internal/integrations/s3"
)

const archiveIAMFixtureImage = "docker.io/chrislusf/seaweedfs@sha256:d4cf67729aa8777e1a43a5b61d72e5b96179e4b7bac9a221cb14cbc2036cb32e"

type tenantS3Fixture struct {
	Endpoint, CAFile, CredentialFile string
	CA                               []byte
	Credentials                      s3.TenantCredentials
	Admin                            *s3.Client
}

func newTenantS3Fixture(t *testing.T, tenants []uuid.UUID, bucket string) tenantS3Fixture {
	return newTenantS3FixtureMode(t, tenants, bucket, false)
}
func newRoleTenantS3Fixture(t *testing.T, tenants []uuid.UUID, bucket string) tenantS3Fixture {
	return newTenantS3FixtureMode(t, tenants, bucket, true)
}
func newTenantS3FixtureMode(t *testing.T, tenants []uuid.UUID, bucket string, separated bool) tenantS3Fixture {
	t.Helper()
	if _, err := exec.CommandContext(t.Context(), "docker", "image", "inspect", archiveIAMFixtureImage).Output(); err != nil {
		t.Fatal("locked SeaweedFS 4.47 image must already be present; fixture never pulls")
	}
	dir := t.TempDir()
	secret := func() string {
		var b [24]byte
		if _, err := rand.Read(b[:]); err != nil {
			t.Fatal(err)
		}
		return hex.EncodeToString(b[:])
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "isolated archive IAM fixture"}, DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	certFile := filepath.Join(dir, "tls.crt")
	if os.WriteFile(certFile, ca, 0600) != nil || os.WriteFile(filepath.Join(dir, "tls.key"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}), 0600) != nil {
		t.Fatal("write outside-repository fixture TLS material")
	}
	adminAccess, adminSecret := secret(), secret()
	identities := []map[string]any{{"name": "isolated-bootstrap", "credentials": []map[string]string{{"accessKey": adminAccess, "secretKey": adminSecret}}, "actions": []string{"Admin", "Read", "Write", "List", "Tagging"}}}
	credentials := s3.TenantCredentials{SchemaVersion: "ops-archive-credentials/v1"}
	policies := []map[string]any{}
	if separated {
		credentials.SchemaVersion = "ops-archive-credentials/v2"
	}
	for _, tenant := range tenants {
		entry := s3.TenantCredential{TenantID: tenant}
		prefix := archive.TenantPrefix(tenant)
		if !separated {
			entry.AccessKey = secret()
			entry.SecretKey = secret()
			scope := bucket + "/" + prefix + "*"
			identities = append(identities, map[string]any{"name": "tenant-" + tenant.String(), "credentials": []map[string]string{{"accessKey": entry.AccessKey, "secretKey": entry.SecretKey}}, "actions": []string{"Read:" + scope, "Write:" + scope, "List:" + scope}})
		} else {
			roles := &s3.TenantRoles{}
			principals := map[string]*s3.RoleCredential{"write": &roles.Write, "read": &roles.Read, "protect": &roles.Protect, "cleanup": &roles.Cleanup}
			actions := map[string][]string{"write": {"s3:PutObject"}, "read": {"s3:GetObject", "s3:GetObjectVersion"}, "protect": {"s3:PutObjectRetention", "s3:PutObjectLegalHold"}, "cleanup": {"s3:DeleteObject", "s3:DeleteObjectVersion"}}
			for role, principal := range principals {
				principal.AccessKey = secret()
				principal.SecretKey = secret()
				name := "tenant-" + tenant.String() + "-" + role
				policy, _ := json.Marshal(map[string]any{"Version": "2012-10-17", "Statement": []map[string]any{
					{"Effect": "Allow", "Action": []string{"s3:ListBucket"}, "Resource": "arn:aws:s3:::" + bucket, "Condition": map[string]any{"StringLike": map[string]any{"s3:prefix": prefix + "*"}}},
					{"Effect": "Allow", "Action": actions[role], "Resource": "arn:aws:s3:::" + bucket + "/" + prefix + "*"},
				}})
				policies = append(policies, map[string]any{"name": name, "content": string(policy)})
				identities = append(identities, map[string]any{"name": name, "credentials": []map[string]string{{"accessKey": principal.AccessKey, "secretKey": principal.SecretKey}}, "policyNames": []string{name}})
			}
			entry.Roles = roles
		}
		credentials.Tenants = append(credentials.Tenants, entry)
	}
	config, _ := json.Marshal(map[string]any{"identities": identities, "policies": policies})
	raw, _ := json.Marshal(credentials)
	credentialFile := filepath.Join(dir, "tenant-credentials.json")
	if os.WriteFile(filepath.Join(dir, "s3.json"), config, 0600) != nil || os.WriteFile(credentialFile, raw, 0600) != nil {
		t.Fatal("write outside-repository IAM credentials")
	}
	label := uuid.NewString()
	name := "ops-archive-iam-" + label
	output, err := exec.CommandContext(t.Context(), "docker", "run", "-d", "--pull=never", "--name", name, "--label", "ops.sp03.archive-iam="+label, "-p", "127.0.0.1::8333", "-v", dir+":/fixture:ro", archiveIAMFixtureImage, "server", "-dir=/data", "-s3", "-s3.config=/fixture/s3.json", "-s3.cert.file=/fixture/tls.crt", "-s3.key.file=/fixture/tls.key", "-master.volumeSizeLimitMB=128").CombinedOutput()
	if err != nil {
		t.Fatalf("start owned TLS/IAM fixture: %v", err)
	}
	id := strings.TrimSpace(string(output))
	inspect := func() (map[string]any, error) {
		raw, err := exec.Command("docker", "inspect", id).Output()
		if err != nil {
			return nil, err
		}
		var objects []map[string]any
		if err = json.Unmarshal(raw, &objects); err != nil || len(objects) != 1 {
			return nil, errors.New("owned fixture identity invalid")
		}
		return objects[0], nil
	}
	t.Cleanup(func() {
		data, err := inspect()
		if err != nil {
			t.Error("owned fixture unavailable during cleanup")
			return
		}
		labels := data["Config"].(map[string]any)["Labels"].(map[string]any)
		if data["Id"] != id || labels["ops.sp03.archive-iam"] != label {
			t.Error("refuse cleanup with mismatched ownership")
			return
		}
		if err := exec.Command("docker", "rm", "-f", id).Run(); err != nil {
			t.Error("owned fixture cleanup failed")
		}
	})
	data, err := inspect()
	if err != nil {
		t.Fatal(err)
	}
	port := data["NetworkSettings"].(map[string]any)["Ports"].(map[string]any)["8333/tcp"].([]any)[0].(map[string]any)["HostPort"].(string)
	endpoint := "https://localhost:" + port
	admin, err := s3.NewClient(s3.Config{Endpoint: endpoint, CACertBundle: ca, Bucket: bucket, AccessKey: adminAccess, SecretKey: adminSecret})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
	defer cancel()
	for {
		err = admin.CreateBucket(ctx)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("owned TLS/IAM fixture did not become ready")
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Logf("owned TLS/IAM fixture image=%s id=%s bucket=%s tenants=%d", archiveIAMFixtureImage, id, bucket, len(tenants))
	return tenantS3Fixture{Endpoint: endpoint, CAFile: certFile, CredentialFile: credentialFile, CA: ca, Credentials: credentials, Admin: admin}
}

func TestRealArchiveTenantIAMTLS(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	bucket := "sp03-iam-" + uuid.NewString()[:8]
	fixture := newTenantS3Fixture(t, []uuid.UUID{a, b}, bucket)
	raw, err := os.ReadFile(fixture.CredentialFile)
	if err != nil {
		t.Fatal(err)
	}
	backend, err := s3.NewTenantClient(s3.Config{Endpoint: fixture.Endpoint, CACertBundle: fixture.CA, Bucket: bucket}, raw)
	if err != nil {
		t.Fatal(err)
	}
	store, err := archive.NewStore(backend, 0)
	if err != nil {
		t.Fatal(err)
	}
	refs := map[uuid.UUID]archive.ObjectRef{}
	for _, tenant := range []uuid.UUID{a, b} {
		ref, err := store.Put(t.Context(), archive.ObjectDescriptor{TenantID: tenant, ObjectID: uuid.New(), Category: "evidence", ContentType: "application/octet-stream", RetainUntil: time.Now().Add(time.Hour)}, strings.NewReader("TLS IAM protected evidence"))
		if err != nil {
			t.Fatal(err)
		}
		refs[tenant] = ref
		if _, err := store.Get(t.Context(), tenant, ref); err != nil {
			t.Fatal(err)
		}
	}
	credential := fixture.Credentials.Tenants[0]
	principal, err := s3.NewClient(s3.Config{Endpoint: fixture.Endpoint, CACertBundle: fixture.CA, Bucket: bucket, AccessKey: credential.AccessKey, SecretKey: credential.SecretKey})
	if err != nil {
		t.Fatal(err)
	}
	if err := principal.VerifyTenantScope(t.Context(), archive.TenantPrefix(a)); err != nil {
		t.Fatal("actual IAM own-list / foreign-list/get denial gate failed")
	}
	if _, err := principal.Get(t.Context(), refs[b].Key, refs[b].VersionID); err == nil || errors.Is(err, archive.ErrObjectNotFound) {
		t.Fatal("server did not deny access to the existing foreign tenant object")
	}
	if _, err := fixture.Admin.Get(t.Context(), refs[b].Key, refs[b].VersionID); err != nil {
		t.Fatal("foreign object positive control is missing")
	}
	if err := fixture.Admin.VerifyTenantScope(t.Context(), archive.TenantPrefix(a)); !errors.Is(err, s3.ErrTenantIAM) {
		t.Fatal("bootstrap/global principal passed runtime IAM admission")
	}
	if _, err := store.Get(t.Context(), a, refs[b]); !errors.Is(err, archive.ErrTenantMismatch) {
		t.Fatal("application tenant boundary failed")
	}
	t.Log("HTTPS, independent CA, separate tenant principals, own-prefix access, foreign list/get rejection, global-principal rejection passed")
}
