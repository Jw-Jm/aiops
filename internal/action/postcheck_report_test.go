package action

import (
	"github.com/google/uuid"
	"ops-platform/internal/contract"
	"ops-platform/internal/resource"
	"testing"
	"time"
)

func TestPostCheckDecisionExplainsMissingPrerequisites(t *testing.T) {
	completed := time.Now().UTC()
	base := PostCheckFacts{Authorized: true, SourceAvailable: true, Complete: true, CollectedAfter: completed.Add(time.Second), CheckedAt: completed.Add(2 * time.Second), EvidenceRefs: []string{uuid.NewString()}}
	for _, tc := range []struct {
		name            string
		change          func(*PostCheckFacts, *time.Time)
		verdict, reason string
	}{
		{"complete", func(*PostCheckFacts, *time.Time) {}, "resolved", "recipe_fault_absent"},
		{"fault", func(f *PostCheckFacts, _ *time.Time) { f.FaultPresent = true }, "not_resolved", "recipe_fault_present"},
		{"completion", func(_ *PostCheckFacts, t *time.Time) { *t = time.Time{} }, "inconclusive", "completion_time_missing"},
		{"authorization", func(f *PostCheckFacts, _ *time.Time) { f.Authorized = false }, "inconclusive", "authorization_unavailable"},
		{"source", func(f *PostCheckFacts, _ *time.Time) { f.SourceAvailable = false }, "inconclusive", "source_unavailable"},
		{"partial", func(f *PostCheckFacts, _ *time.Time) { f.Complete = false }, "inconclusive", "facts_incomplete"},
		{"no_evidence", func(f *PostCheckFacts, _ *time.Time) { f.EvidenceRefs = nil }, "inconclusive", "evidence_missing"},
		{"old_evidence", func(f *PostCheckFacts, t *time.Time) { f.CollectedAfter = *t }, "inconclusive", "observation_not_after_execution"},
		{"future_evidence", func(f *PostCheckFacts, _ *time.Time) { f.CheckedAt = f.CollectedAfter.Add(-time.Second) }, "inconclusive", "check_precedes_observation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, end := base, completed
			tc.change(&f, &end)
			v, r := postCheckDecision(f, end)
			if v != tc.verdict || r != tc.reason {
				t.Fatal(v, r)
			}
		})
	}
}

func TestFrozenPostCheckReportBindsExecutionIdentityAndOnlyRecipeCoverage(t *testing.T) {
	completed := time.Now().UTC()
	tenant := uuid.New()
	id := resource.CanonicalID{Domain: "k8s", Tenant: tenant.String(), Scope: "cluster", APIGroup: "core", Kind: "Node", StableID: "node-uid"}.String()
	e := Execution{ID: uuid.New(), CompletedAt: &completed, Binding: Binding{TenantID: tenant, IncidentID: uuid.New(), Target: id, TargetUID: "node-uid"}}
	f := PostCheckFacts{Authorized: true, SourceAvailable: true, Complete: true, CollectedAfter: completed.Add(time.Second), CheckedAt: completed.Add(2 * time.Second), EvidenceRefs: []string{uuid.NewString()}}
	r := makePostCheckReport(e, f, completed)
	if r.Coverage != "recipe_target_only" || r.Verdict != "resolved" || validatePostCheckReport(r, e) != nil {
		t.Fatal(r)
	}
	if err := contract.Validate("https://ops.local/schemas/command-post-check/v1", Canonical(r)); err != nil {
		t.Fatal(err)
	}
	oldUnsupported := r
	oldUnsupported.Verdict, oldUnsupported.Reason = "inconclusive", "target_unsupported"
	if validatePostCheckReport(oldUnsupported, e) != nil {
		t.Fatal("current adapter support reinterpreted historical coverage")
	}
	f.EvidenceRefs[0] = "mutated"
	if r.Facts.EvidenceRefs[0] == "mutated" {
		t.Fatal("frozen report shares mutable evidence list")
	}
	for _, change := range []func(*PostCheckReport){func(r *PostCheckReport) { r.ExecutionID = uuid.New() }, func(r *PostCheckReport) { r.IncidentID = uuid.New() }, func(r *PostCheckReport) { r.TargetUID = "recreated" }, func(r *PostCheckReport) { r.CompletedAt = completed.Add(time.Second) }, func(r *PostCheckReport) { r.Verdict = "not_resolved" }, func(r *PostCheckReport) { r.Coverage = "all_faults" }} {
		bad := r
		change(&bad)
		if validatePostCheckReport(bad, e) == nil {
			t.Fatal("cross-execution or fabricated report accepted", bad)
		}
	}
	e.Binding.Target = resource.CanonicalID{Domain: "k8s", Tenant: tenant.String(), Scope: "cluster", APIGroup: "core", Kind: "Pod", StableID: "pod-uid"}.String()
	r = makePostCheckReport(e, r.Facts, completed)
	if r.Verdict != "inconclusive" || r.Reason != "target_unsupported" {
		t.Fatal("generic gate invented target support", r)
	}
	r = makePostCheckReport(e, PostCheckFacts{}, completed)
	if err := contract.Validate("https://ops.local/schemas/command-post-check/v1", Canonical(r)); err != nil {
		t.Fatal("missing facts must remain explicit and serializable", err)
	}
}
