package integration

import (
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"ops-platform/internal/evidence"
	"ops-platform/internal/investigation"
	"ops-platform/internal/persistence"
	"testing"
	"time"
)

func TestSP06ActualArchiveRetentionHoldAndReferenceRevocation(t *testing.T) {
	ctx, db, repo, req := sp06JobFixture(t)
	pool := repo.Pool.(*pgxpool.Pool)
	archives := sp05GoldenArchive(t, ctx, pool, req.TenantID)
	var canonical, sourceID, backend string
	if err := db.QueryRowContext(ctx, `SELECT f.resource_canonical_id,f.source_id,s.backend_logical_id FROM finding.records f JOIN platform.source_registrations s USING(tenant_id,source_id) WHERE f.tenant_id=$1 LIMIT 1`, req.TenantID).Scan(&canonical, &sourceID, &backend); err != nil {
		t.Fatal(err)
	}
	binding, err := (evidence.Repository{Pool: pool}).RegisteredBinding(ctx, evidence.Binding{Tenant: req.TenantID.String(), SourceID: sourceID, Revision: 1, SourceType: "kubernetes", BackendLogicalID: backend})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	data := []byte(`{"ready":false}`)
	primary := evidence.Evidence{SchemaVersion: "evidence/v2", EvidenceID: uuid.NewString(), TenantID: req.TenantID.String(), ResourceCanonicalID: canonical, Type: "resource_state", DataClass: "D0", SourceRegistrationID: sourceID, SourceRevision: 1, SourceScopeDigest: evidence.BindingScopeDigest(binding), SourceSystem: "kubernetes", BackendLogicalID: backend, QueryTemplateVersion: "sp05-signal/v1", QueryHash: evidence.Digest(data), EffectiveScope: req.Scope, EvaluatedAt: now, ObservedFrom: now.Add(-time.Hour), ObservedTo: now.Add(-time.Hour + time.Minute), SourceRetentionUntil: now.Add(-time.Minute), TimeReliable: true, ReplayState: "archive_pending", IndependenceGroup: "native-node", DerivationEvidenceRefs: []string{}, ContentDigest: evidence.Digest(data), Data: data}
	if err = archives.Capture(ctx, primary, "", now.Add(181*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	derived := primary
	derived.EvidenceID = uuid.NewString()
	derived.DerivationEvidenceRefs = []string{primary.EvidenceID}
	derived.Data = []byte(`{"derived":true}`)
	derived.ContentDigest = evidence.Digest(derived.Data)
	if err = archives.Capture(ctx, derived, "", now.Add(181*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	j, err := repo.CreateJob(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	l, err := repo.Claim(ctx, j.TenantID, "archive-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	call := investigation.Call{StepID: uuid.New(), ContextID: uuid.New(), JTI: uuid.NewString(), Name: "get_ranked_evidence", ArgsDigest: investigation.ArgumentsDigest([]byte(`{}`)), Reserve: investigation.Usage{ToolCalls: 1, ResultBytes: 65536, EvidenceItems: 100}}
	if _, err = repo.BeginCall(ctx, l, call); err != nil {
		t.Fatal(err)
	}
	result, _ := json.Marshal(map[string]any{"evidenceRefs": []string{derived.EvidenceID}})
	if err = repo.CompleteStep(ctx, l, call.StepID, result, investigation.Usage{ToolCalls: 1, ResultBytes: int64(len(result)), EvidenceItems: 1}); err != nil {
		t.Fatal(err)
	}
	proposal, _ := json.Marshal(investigation.Proposal{SchemaVersion: "investigation-result/v1", Status: "unresolved", Summary: "Historical immutable evidence is available; no current causal conclusion.", EvidenceRefs: []string{derived.EvidenceID}, CandidateUpdates: []investigation.CandidateSuggestion{}, ActionPlans: []json.RawMessage{}, Partial: false, DegradedSources: []string{}})
	if err = repo.Complete(ctx, l, proposal); err != nil {
		t.Fatal(err)
	}
	if err = persistence.WithTenantTx(ctx, pool, j.TenantID, func(tx pgx.Tx) error {
		return evidence.SetLegalHold(ctx, tx, j.TenantID, uuid.MustParse(derived.EvidenceID), true, j.Subject)
	}); err != nil {
		t.Fatal(err)
	}
	if err = archives.ReconcileTenantProtection(ctx, j.TenantID); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{primary.EvidenceID, derived.EvidenceID} {
		data, stored, err := archives.Read(ctx, j.TenantID, uuid.MustParse(ref))
		if err != nil || len(data) == 0 || stored.Object.RetainUntil.Before(now.Add(364*24*time.Hour)) {
			t.Fatalf("actual 365-day dependency protection: %+v %v", stored, err)
		}
		if err = archives.Cleanup(ctx, j.TenantID, uuid.MustParse(ref)); !errors.Is(err, evidence.ErrProtected) {
			t.Fatalf("hold/retention cleanup was admitted: %v", err)
		}
	}
	// A committed tool/model result is still subject to current source authority.
	if _, err = db.ExecContext(ctx, `UPDATE platform.source_registrations SET status='disabled',revision=revision+1 WHERE source_id=$1`, sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Get(ctx, j.TenantID, j.JobID); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("revoked final result leaked: %v", err)
	}
	if _, _, err = repo.Events(ctx, j.TenantID, j.JobID, j.Subject, 0); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("revoked archived SSE leaked: %v", err)
	}
	t.Log("real non-exportable Transit archive encryption, IAM S3 ObjectLock, derived Evidence 365-day dependency closure, Legal Hold and current source withdrawal; historical age does not manufacture a current RCA")
}
