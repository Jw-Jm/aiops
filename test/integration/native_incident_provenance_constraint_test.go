package integration

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// The provenance columns of a v2 Job accept exactly three shapes: a complete
// native provenance, a complete legacy provenance without an account, and the
// all-NULL historical encoding of a row written before the columns existed. A
// partial shape must be refused by the database, not only by the application.
func TestNativeIncidentJobProvenanceConstraint(t *testing.T) {
	f := newNativeJobFixture(t)
	seed := f.newIncident(t)
	account := f.newAccount(t, `"apps"`)
	f.clearLegacyBindings(t)
	job := f.newV2Job(t, account, seed.IncidentID, seed.Revision, "sha256:catalog")

	update := func(t *testing.T, statement string) error {
		t.Helper()
		_, err := f.db.ExecContext(f.ctx, statement, f.tenantID, job.JobID)
		return err
	}
	assertRejected := func(t *testing.T, name, statement string) {
		t.Helper()
		err := update(t, statement)
		if err == nil {
			t.Fatalf("%s was accepted", name)
		}
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) {
			t.Fatalf("%s failed with %v, want a constraint violation", name, err)
		}
		if pgErr.Code != "23514" {
			t.Fatalf("%s failed with SQLSTATE %s, want 23514", name, pgErr.Code)
		}
	}
	// The native row the product wrote is accepted and complete.
	var source string
	var userID any
	var version any
	if err := f.db.QueryRowContext(f.ctx, `SELECT authorization_source,authorization_user_id,authorization_version FROM investigation.jobs WHERE tenant_id=$1 AND job_id=$2`, f.tenantID, job.JobID).Scan(&source, &userID, &version); err != nil {
		t.Fatal(err)
	}
	if source != "native" || version == nil {
		t.Fatalf("persisted provenance = %s/%v", source, version)
	}
	// A native provenance without its version is a partial shape.
	assertRejected(t, "native without version", `UPDATE investigation.jobs SET authorization_version=NULL WHERE tenant_id=$1 AND job_id=$2`)
	// A native provenance without its account is a partial shape.
	assertRejected(t, "native without account", `UPDATE investigation.jobs SET authorization_user_id=NULL WHERE tenant_id=$1 AND job_id=$2`)
	// An unknown source is refused.
	assertRejected(t, "unknown source", `UPDATE investigation.jobs SET authorization_source='keycloak' WHERE tenant_id=$1 AND job_id=$2`)
	// A legacy source that still carries an account is a partial shape.
	assertRejected(t, "legacy with account", `UPDATE investigation.jobs SET authorization_source='legacy' WHERE tenant_id=$1 AND job_id=$2`)
	// An empty source with an account is a partial shape.
	assertRejected(t, "empty source with account", `UPDATE investigation.jobs SET authorization_source=NULL WHERE tenant_id=$1 AND job_id=$2`)
	// The complete legacy shape and the all-NULL historical shape are accepted.
	if err := update(t, `UPDATE investigation.jobs SET authorization_source='legacy',authorization_user_id=NULL,authorization_version=NULL WHERE tenant_id=$1 AND job_id=$2`); err != nil {
		t.Fatalf("legacy provenance was refused: %v", err)
	}
	if err := update(t, `UPDATE investigation.jobs SET authorization_source=NULL,authorization_user_id=NULL,authorization_version=NULL WHERE tenant_id=$1 AND job_id=$2`); err != nil {
		t.Fatalf("historical provenance was refused: %v", err)
	}
	var nulls int
	if err := f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM investigation.jobs WHERE tenant_id=$1 AND job_id=$2 AND authorization_source IS NULL AND authorization_user_id IS NULL AND authorization_version IS NULL`, f.tenantID, job.JobID).Scan(&nulls); err != nil {
		t.Fatal(err)
	}
	if nulls != 1 {
		t.Fatal("the historical encoding was not stored")
	}
	// A new row cannot be inserted with a partial shape either.
	if err := insertPartialJob(t, f.db, f.tenantID, uuid.New(), "native", nil, nil); err == nil {
		t.Fatal("a partial native row was inserted")
	} else {
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
			t.Fatalf("partial insert failed with %v, want SQLSTATE 23514", err)
		}
	}
	if err := insertPartialJob(t, f.db, f.tenantID, uuid.New(), "native", &userID, nil); err == nil {
		t.Fatal("a partial native row with an account was inserted")
	}
}

// insertPartialJob writes one v2 Job row with the given provenance shape. It
// reuses the columns of an existing row so the check under test is the only
// difference.
func insertPartialJob(t *testing.T, db *sql.DB, tenant uuid.UUID, jobID uuid.UUID, source string, account, version any) error {
	t.Helper()
	_, err := db.ExecContext(t.Context(), `INSERT INTO investigation.jobs
		(tenant_id,job_id,incident_id,trigger_revision,state,schema_version,requesting_subject,trigger_kind,policy_version,
		 effective_scope,effective_scope_digest,tool_catalog_digest,budget,budget_reserved,budget_consumed,expires_at,
		 authorization_source,authorization_user_id,authorization_version)
		SELECT tenant_id,$2,incident_id,trigger_revision+1000,state,schema_version,requesting_subject,trigger_kind,policy_version,
		 effective_scope,effective_scope_digest,tool_catalog_digest,budget,budget_reserved,budget_consumed,expires_at,
		 $3,$4,$5
		FROM investigation.jobs WHERE tenant_id=$1 AND job_id=(SELECT job_id FROM investigation.jobs WHERE tenant_id=$1 ORDER BY created_at LIMIT 1)`,
		tenant, jobID, source, account, version)
	return err
}
