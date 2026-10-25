package integration

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"ops-platform/internal/graph"
	"ops-platform/internal/investigation"
	"ops-platform/internal/investigation/tools"
	"ops-platform/internal/persistence"
)

// A judgement of a committed metric query must be resolvable through the
// source-step reader: the reader only accepts a real committed step of the same
// job, and it refuses an unknown reference or a step of another job.
func TestNativeFaultChainSourceStepReadBack(t *testing.T) {
	f := newNativeJobFixture(t)
	seed := f.newIncident(t)
	account := f.newAccount(t, `"apps"`)
	f.clearLegacyBindings(t)
	defs, err := tools.CatalogFor("investigation-job/v2", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	job := f.newV2Job(t, account, seed.IncidentID, seed.Revision, tools.Digest(defs))

	stepID := uuid.Must(uuid.NewV7())
	otherStepID := uuid.Must(uuid.NewV7())
	result := map[string]any{
		"tool": "execute_analysis_query",
		"data": map[string]any{"canonicalId": seed.Canonical, "queryResultRefs": []string{}},
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	digest := graph.ScopeDigest(json.RawMessage(raw))
	insertStep := func(t *testing.T, id uuid.UUID, jobID uuid.UUID, body []byte) {
		t.Helper()
		if _, err := f.db.ExecContext(f.ctx, `INSERT INTO investigation.steps(tenant_id,job_id,step_id,tool_name,args_digest,state,lease_token,lease_generation,result,result_digest,error_code) VALUES($1,$2,$3,'execute_analysis_query','sha256:args','succeeded',$4,1,$5,$6,'')`, f.tenantID, jobID, id, uuid.Must(uuid.NewV7()), body, digest); err != nil {
			t.Fatalf("insert step: %v", err)
		}
		if _, err := f.db.ExecContext(f.ctx, `INSERT INTO investigation.admissions(tenant_id,job_id,admission_id,step_id,context_id,lease_generation,reservation,consumed,state,allowed_data_classes,call_jti) VALUES($1,$2,$3,$4,$5,1,'{}','{}','settled',$6,$7)`, f.tenantID, jobID, uuid.Must(uuid.NewV7()), id, uuid.Must(uuid.NewV7()), job.Budget.AllowedDataClasses, uuid.NewString()); err != nil {
			t.Fatalf("insert admission: %v", err)
		}
	}
	insertStep(t, stepID, job.JobID, raw)
	// A second operator owns a second job for the same Incident revision; the
	// reader must never cross the job border. The Incident revision is not
	// advanced on purpose: the second job differs by its own subject.
	secondAccount := f.newAccount(t, `"apps"`)
	second := f.newV2Job(t, secondAccount, seed.IncidentID, seed.Revision, tools.Digest(defs))
	if second.JobID == job.JobID {
		t.Fatal("the second operator reused the first job")
	}
	insertStep(t, otherStepID, second.JobID, raw)

	loaded, err := (investigation.Repository{Pool: f.apiPool}).Get(account.ctx, f.tenantID, job.JobID)
	if err != nil {
		t.Fatalf("read the job: %v", err)
	}
	reference := investigation.ResourceSourceStep{StepID: stepID, Tool: "execute_analysis_query", CanonicalID: seed.Canonical, ContentDigest: digest}
	if err := persistence1(f.ctx, f, loaded, reference); err != nil {
		t.Fatalf("the committed step was not readable: %v", err)
	}
	// An unknown reference is refused.
	unknown := reference
	unknown.StepID = uuid.Must(uuid.NewV7())
	if err := persistence1(f.ctx, f, loaded, unknown); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("unknown source step = %v", err)
	}
	// A step of another job is refused.
	foreign := reference
	foreign.StepID = otherStepID
	if err := persistence1(f.ctx, f, loaded, foreign); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("foreign source step = %v", err)
	}
	// A changed content digest is refused: the step result was replaced.
	tampered := reference
	tampered.ContentDigest = graph.ScopeDigest(json.RawMessage(`{"tool":"execute_analysis_query"}`))
	if err := persistence1(f.ctx, f, loaded, tampered); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("tampered source step = %v", err)
	}
}

// persistence1 runs the source-step read in the tenant transaction of the fixture.
func persistence1(ctx context.Context, f *nativeJobFixture, job investigation.Job, ref investigation.ResourceSourceStep) error {
	return persistence.WithTenantTx(ctx, f.apiPool, f.tenantID, func(tx pgx.Tx) error {
		_, err := investigation.ReadResourceSourceStepTx(ctx, tx, job, ref)
		return err
	})
}
