package integration

import (
	"encoding/json"
	"github.com/google/uuid"
	"ops-platform/internal/investigation"
	"strings"
	"testing"
	"time"
)

func TestSP08SafeInputSummaryPersistence(t *testing.T) {
	ctx, _, repo, req := sp06JobFixture(t)
	j, err := repo.CreateJob(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	l, err := repo.Claim(ctx, j.TenantID, "summary-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	args := json.RawMessage(`{"incidentId":"public-incident","limit":0,"prompt":"sensitive-prompt","token":"secret-token"}`)
	c := investigation.Call{StepID: uuid.New(), ContextID: uuid.New(), JTI: uuid.NewString(), Name: "get_incident_context", Arguments: args, ArgsDigest: investigation.ArgumentsDigest(args), Reserve: investigation.Usage{ToolCalls: 1, ResultBytes: 1024}}
	if _, err = repo.BeginCall(ctx, l, c); err != nil {
		t.Fatal(err)
	}
	steps, err := repo.ReadSteps(ctx, j.TenantID, j.JobID, j.Subject)
	if err != nil || len(steps) != 1 {
		t.Fatalf("read steps: %v %+v", err, steps)
	}
	s := string(steps[0].ArgsSummary)
	var summary map[string]any
	if err := json.Unmarshal(steps[0].ArgsSummary, &summary); err != nil {
		t.Fatal(err)
	}
	if summary["limit"] != float64(0) || summary["incidentId"] != "public-incident" || strings.Contains(s, "sensitive") || strings.Contains(s, "secret") {
		t.Fatalf("unsafe or missing persisted summary: %s", s)
	}
}
