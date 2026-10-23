package investigation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/analytics"
	"ops-platform/internal/contract"
	"ops-platform/internal/resource"
)

func grantContext(grant historicalReadGrant) context.Context {
	return context.WithValue(context.Background(), historicalReadGrantKey{}, grant)
}

// A mapped read grant may only re-authorize the exact Job it was proven for.
// The checks below fail before any database work, which is why a nil tx is safe:
// a foreign tenant, Job or Subject must never reach currentAssistantAs.
func TestReadGrantDoesNotAuthorizeAnotherJob(t *testing.T) {
	tenant, job := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	ctx := grantContext(historicalReadGrant{
		tenant: tenant,
		job:    job,
		read:   HistoricalRead{OwnerSubject: "legacy-owner", CurrentSubject: "user:native", Mapped: true, MappingID: uuid.Must(uuid.NewV7())},
	})
	cases := []struct {
		name string
		job  Job
	}{
		{"other tenant", Job{TenantID: uuid.Must(uuid.NewV7()), JobID: job, Subject: "legacy-owner"}},
		{"other job", Job{TenantID: tenant, JobID: uuid.Must(uuid.NewV7()), Subject: "legacy-owner"}},
		{"other subject", Job{TenantID: tenant, JobID: job, Subject: "another-owner"}},
	}
	for _, test := range cases {
		if err := CheckAssistantJobTx(ctx, nil, test.job); !errors.Is(err, ErrDenied) {
			t.Fatalf("%s: grant error = %v", test.name, err)
		}
	}
}

// An owner-exact read never produces a mapped grant, and a grant that claims to
// be unmapped is refused instead of falling back to the frozen subject.
func TestReadGrantRequiresMappedRead(t *testing.T) {
	tenant, job := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	ctx := grantContext(historicalReadGrant{
		tenant: tenant,
		job:    job,
		read:   HistoricalRead{OwnerSubject: "legacy-owner", CurrentSubject: "legacy-owner"},
	})
	if err := CheckAssistantJobTx(ctx, nil, Job{TenantID: tenant, JobID: job, Subject: "legacy-owner"}); !errors.Is(err, ErrDenied) {
		t.Fatalf("unmapped grant error = %v", err)
	}
}

// The fixture used by the mapped read integration test must be a context the
// production evaluator actually produces and the client schema accepts.
func TestMinimalFaultChainContextIsSchemaValid(t *testing.T) {
	from := time.Date(2026, 10, 23, 1, 0, 0, 0, time.UTC)
	tenant := uuid.Must(uuid.NewV7())
	target := resource.CanonicalID{Domain: "k8s", Tenant: tenant.String(), Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: "apps/web-0"}
	request := FaultChainRequest{
		SchemaVersion:     "fault-chain-request/v1",
		TargetCanonicalID: target.String(),
		Fault:             analytics.QueryTimeRange{From: from, To: from.Add(5 * time.Minute), Timezone: "UTC"},
		MaxEdges:          4,
	}
	out, err := EvaluateFaultChain(request, nil, nil, from.Add(10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if out.SchemaVersion != "fault-chain-context/v1" || len(out.Edges) != 0 {
		t.Fatalf("unexpected fixture context %+v", out)
	}
	if err := contract.Validate("https://ops.local/schemas/fault-chain-context/v1", raw(out)); err != nil {
		t.Fatalf("minimal fault chain context is not schema valid: %v", err)
	}
}
