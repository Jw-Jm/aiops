package integration

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/archive"
	"ops-platform/internal/integrations/s3"
)

// This is a real isolated PostgreSQL lifecycle check. The first red run cannot
// compile without the guard, so it never intentionally drops protected data.
func TestIntegrationFixtureRetainsAuditDatabaseAfterCleanup(t *testing.T) {
	if os.Getenv("SP03_TEST_DATABASE_URL") == "" {
		t.Skip("actual isolated PostgreSQL required")
	}
	var retainedURL string
	t.Run("owned_audit_database", func(t *testing.T) {
		ctx, db, _, dsn := newMigrationDatabase(t)
		retainedURL = dsn
		// A minimal synthetic row suffices to exercise cleanup protection; this
		// is not a product bootstrap, Evidence or Audit cryptographic gate.
		if _, err := db.ExecContext(ctx, `CREATE SCHEMA audit; CREATE TABLE audit.records (fixture_marker text NOT NULL); INSERT INTO audit.records VALUES ('retention-lifecycle-proof')`); err != nil {
			t.Fatal(err)
		}
		if retain, reason := integrationDatabaseRetention(ctx, db); !retain || reason != "audit.records" {
			t.Fatal("persisted Audit database is not fenced from cleanup")
		}
	})
	if retainedURL == "" {
		t.Fatal("actual lifecycle target missing")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := sql.Open("pgx", retainedURL)
	if err != nil {
		t.Fatal("retained PostgreSQL unavailable")
	}
	defer conn.Close()
	var marker string
	if err := conn.QueryRowContext(ctx, `SELECT fixture_marker FROM audit.records`).Scan(&marker); err != nil || marker != "retention-lifecycle-proof" {
		t.Fatal("cleanup destroyed an Audit database")
	}
	t.Log("actual owned PostgreSQL Audit row/database remains after test cleanup; synthetic lifecycle probe, not full protected historical restore")
}

func TestIntegrationArchiveFixtureSurvivesCleanupAndActualRestart(t *testing.T) {
	var fixture tenantS3Fixture
	var object archive.ObjectRef
	tenant := uuid.New()
	body := []byte("retained immutable integration lifecycle fact")
	t.Run("owned_compliance_object", func(t *testing.T) {
		fixture = newRoleTenantS3Fixture(t, []uuid.UUID{tenant}, "retention-lifecycle-"+uuid.NewString()[:8])
		store, err := archive.NewStore(fixture.Admin, 65536)
		if err != nil {
			t.Fatal(err)
		}
		object, err = store.Put(t.Context(), archive.ObjectDescriptor{TenantID: tenant, ObjectID: uuid.New(), Category: "evidence", ContentType: "application/octet-stream", RetainUntil: time.Now().Add(365 * 24 * time.Hour)}, bytes.NewReader(body))
		if err != nil {
			t.Fatal("actual compliance object creation failed")
		}
		if err := fixture.Admin.ProtectObject(t.Context(), object.Key, object.VersionID, object.RetainUntil, true); err != nil {
			t.Fatal("actual lifecycle Legal Hold initialization failed")
		}
	})
	if fixture.ContainerID == "" {
		t.Fatal("actual retained Archive identity missing")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	inspect := func() map[string]any {
		raw, err := exec.CommandContext(ctx, "docker", "inspect", fixture.ContainerID).Output()
		var containers []map[string]any
		if err != nil || json.Unmarshal(raw, &containers) != nil || len(containers) != 1 || containers[0]["Id"] != fixture.ContainerID {
			t.Fatal("owned Archive container removed by cleanup")
		}
		labels := containers[0]["Config"].(map[string]any)["Labels"].(map[string]any)
		if labels["ops.platform.test.retention"] != "protected-archive-fixture" {
			t.Fatal("retained container protection label changed")
		}
		return containers[0]
	}
	container := inspect()
	if container["State"].(map[string]any)["Running"] != false {
		t.Fatal("fixture cleanup did not stop the owned process")
	}
	for _, name := range []string{"tls.crt", "tls.key", "s3.json", "tenant-credentials.json", "fixture-identity.json", "data"} {
		if _, err := os.Stat(filepath.Join(fixture.PrivateDirectory, name)); err != nil {
			t.Fatal("private identity or storage removed by cleanup")
		}
	}
	if exec.CommandContext(ctx, "docker", "start", fixture.ContainerID).Run() != nil {
		t.Fatal("exact owned retained Archive restart failed")
	}
	defer func() {
		inspect()
		if exec.Command("docker", "stop", fixture.ContainerID).Run() != nil {
			t.Error("owned Archive stop failed; protected materials retained")
		}
	}()
	container = inspect()
	ports := container["NetworkSettings"].(map[string]any)["Ports"].(map[string]any)["8333/tcp"].([]any)
	port := ports[0].(map[string]any)["HostPort"].(string)
	var identity struct {
		Identities []struct {
			Name        string
			Credentials []struct {
				AccessKey string `json:"accessKey"`
				SecretKey string `json:"secretKey"`
			}
		}
	}
	raw, err := os.ReadFile(filepath.Join(fixture.PrivateDirectory, "s3.json"))
	if err != nil || json.Unmarshal(raw, &identity) != nil || len(identity.Identities) == 0 || identity.Identities[0].Name != "isolated-bootstrap" || len(identity.Identities[0].Credentials) != 1 {
		t.Fatal("retained independent private IAM material invalid")
	}
	credential := identity.Identities[0].Credentials[0]
	bucket := ""
	var metadata struct {
		Bucket string `json:"bucket"`
	}
	raw, err = os.ReadFile(filepath.Join(fixture.PrivateDirectory, "fixture-identity.json"))
	if err != nil || json.Unmarshal(raw, &metadata) != nil || metadata.Bucket == "" {
		t.Fatal("retained source bucket identity unavailable")
	}
	bucket = metadata.Bucket
	client, err := s3.NewClient(s3.Config{Endpoint: "https://localhost:" + port, CACertBundle: fixture.CA, Bucket: bucket, AccessKey: credential.AccessKey, SecretKey: credential.SecretKey})
	if err != nil {
		t.Fatal("retained TLS/IAM client invalid")
	}
	for {
		result, err := client.Get(ctx, object.Key, object.VersionID)
		if err == nil {
			if !bytes.Equal(result.Body, body) || result.VersionID != object.VersionID || !result.LegalHold || result.RetainUntil.Before(time.Now().Add(364*24*time.Hour)) {
				t.Fatal("actual restarted object/version/digest/365-day/Legal Hold changed")
			}
			break
		}
		if ctx.Err() != nil {
			t.Fatal("retained actual object/version cannot be read after restart")
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Logf("actual Archive container=%s object=%s version=%s retained through cleanup and restart; matching TLS/IAM private inputs, 365-day protection and Legal Hold unchanged; controlled storage retained", fixture.ContainerID, object.Key, object.VersionID)
}
