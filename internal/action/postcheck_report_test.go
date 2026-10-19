package action

import (
	"github.com/google/uuid"
	"ops-platform/internal/contract"
	"ops-platform/internal/resource"
	"strings"
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
	e.Binding.Target = resource.CanonicalID{Domain: "k8s", Tenant: tenant.String(), Scope: "cluster", APIGroup: "core", Kind: "Secret", StableID: "secret-uid"}.String()
	r = makePostCheckReport(e, r.Facts, completed)
	if r.Verdict != "inconclusive" || r.Reason != "target_unsupported" {
		t.Fatal("generic gate invented target support", r)
	}
	r = makePostCheckReport(e, PostCheckFacts{}, completed)
	if err := contract.Validate("https://ops.local/schemas/command-post-check/v1", Canonical(r)); err != nil {
		t.Fatal("missing facts must remain explicit and serializable", err)
	}
}

func TestRollbackGoalRequiresConfigurationAndHealthyFacts(t *testing.T) {
	completed := time.Now().UTC()
	tenant := uuid.New()
	goal := "sha256:" + strings.Repeat("a", 64)
	e := Execution{ID: uuid.New(), CompletedAt: &completed, Binding: Binding{TenantID: tenant, IncidentID: uuid.New(), ExecutionRelation: "rollback", RollbackSpecDigest: goal, TargetUID: "pod", Target: resource.CanonicalID{Domain: "k8s", Tenant: tenant.String(), Scope: "cluster", APIGroup: "core", Kind: "Pod", StableID: "pod"}.String()}}
	f := PostCheckFacts{Authorized: true, SourceAvailable: true, Complete: true, CollectedAfter: completed.Add(time.Second), CheckedAt: completed.Add(2 * time.Second), EvidenceRefs: []string{uuid.NewString()}, ObservedSpecDigest: goal, Adapter: "native-related-health/v1"}
	for _, tc := range []struct {
		name            string
		change          func(*Binding, *PostCheckFacts)
		verdict, reason string
	}{
		{"met", func(*Binding, *PostCheckFacts) {}, "resolved", "rollback_goal_met"},
		{"different_configuration", func(_ *Binding, f *PostCheckFacts) { f.ObservedSpecDigest = "sha256:" + strings.Repeat("b", 64) }, "not_resolved", "rollback_goal_not_met"},
		{"unhealthy", func(_ *Binding, f *PostCheckFacts) { f.FaultPresent = true }, "not_resolved", "recipe_fault_present"},
		{"new_warning", func(_ *Binding, f *PostCheckFacts) { f.NewAnomalies = []string{"FailedMount"} }, "not_resolved", "new_anomalies_observed"},
		{"old_rollback_without_goal", func(b *Binding, _ *PostCheckFacts) { b.RollbackSpecDigest = "" }, "inconclusive", "rollback_goal_missing"},
		{"missing_configuration", func(_ *Binding, f *PostCheckFacts) { f.ObservedSpecDigest = "" }, "inconclusive", "rollback_observation_missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, f := e, f
			tc.change(&e.Binding, &f)
			r := makePostCheckReport(e, f, completed)
			if r.Verdict != tc.verdict || r.Reason != tc.reason || validatePostCheckReport(r, e) != nil {
				t.Fatal(r)
			}
			if err := contract.Validate("https://ops.local/schemas/command-post-check/v1", Canonical(r)); err != nil {
				t.Fatal(err)
			}
		})
	}
	if !validRollbackGoal("rollback", goal) || validRollbackGoal("follow_up", goal) || validRollbackGoal("rollback", "") {
		t.Fatal("invalid goal accepted")
	}
	b := e.Binding
	before := b.Digest()
	b.RollbackSpecDigest = "sha256:" + strings.Repeat("b", 64)
	if b.Digest() == before {
		t.Fatal("goal not bound to confirmation")
	}
}

func TestArchivedProbeAndNativeFactsAreBothRequired(t *testing.T) {
	now := time.Now().UTC()
	claimed, completed := now.Add(-5*time.Second), now.Add(-2*time.Second)
	tenant := uuid.New()
	ns := "ns"
	parent := uuid.New()
	binding := Binding{DigestVersion: "execution-request/v4", TenantID: tenant, IncidentID: uuid.New(), Target: resource.CanonicalID{Domain: "k8s", Tenant: tenant.String(), Scope: "cluster", APIGroup: "core", Kind: "Service", StableID: "uid"}.String(), TargetUID: "uid", Namespace: &ns, RelatedExecutionID: &parent, ExecutionRelation: "follow_up", VerificationNamespace: "runners", Verification: &VerificationSpec{Kind: "http", Port: 80, Path: "/healthz", ExpectedStatus: 200}}
	e := Execution{ID: uuid.New(), Binding: binding, RequestDigest: binding.Digest(), StartedAt: &claimed, CompletedAt: &completed}
	result := VerificationResult{SchemaVersion: "command-verification/v1", ExecutionID: e.ID, RequestDigest: e.RequestDigest, TargetUID: "uid", Kind: "http", Vantage: "runner/runners", StartedAt: claimed.Add(time.Second), FinishedAt: completed.Add(-time.Second), Verdict: "resolved", Reason: "http_assertions_met", HTTPStatus: 200, BodyDigest: Digest([]byte("healthy"))}
	f := PostCheckFacts{Verification: &result, Adapter: "native-related-health/v1", Authorized: true, SourceAvailable: true, Complete: true, CollectedAfter: completed.Add(time.Second), CheckedAt: now, EvidenceRefs: []string{uuid.NewString()}}
	report := makePostCheckReport(e, f, completed)
	if report.Verdict != "resolved" || report.Reason != "verification_passed" || validatePostCheckReport(report, e) != nil {
		t.Fatal(report)
	}
	if err := contract.Validate("https://ops.local/schemas/command-post-check/v1", Canonical(report)); err != nil {
		t.Fatal(err)
	}
	result.Reason = "probe_unavailable"
	result.Verdict = "inconclusive"
	result.HTTPStatus = 0
	result.BodyDigest = ""
	if report.Facts.Verification.Reason != "http_assertions_met" {
		t.Fatal("frozen receipt changed through shared pointer")
	}
	if r := makePostCheckReport(e, f, completed); r.Verdict != "inconclusive" || r.Reason != "verification_unavailable" {
		t.Fatal(r)
	}
	f.Verification = nil
	if r := makePostCheckReport(e, f, completed); r.Verdict != "inconclusive" || r.Reason != "verification_missing" {
		t.Fatal(r)
	}
	f.Verification = report.Facts.Verification
	f.SourceAvailable = false
	if r := makePostCheckReport(e, f, completed); r.Verdict != "inconclusive" {
		t.Fatal("probe success bypassed native source", r)
	}
}
