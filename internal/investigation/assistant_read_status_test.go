package investigation

import "testing"

func TestAssistantReadStatusRetainsFailedAndPartialCollection(t *testing.T) {
	a := AssistantAnswer{Warnings: []string{}, MissingEvidence: []string{"causality_unknown"}}
	assistantReadStatus(&a, "resource_findings", "succeeded", false, "", "")
	if a.Partial {
		t.Fatal("unknown causality alone changed execution state")
	}
	assistantReadStatus(&a, "resource_history", "succeeded", true, "", "")
	assistantReadStatus(&a, "query_logs", "failed", false, "SOURCE_DEGRADED", "")
	assistantReadStatus(&a, "query_logs", "failed", false, "SOURCE_DEGRADED", "")
	if !a.Partial || len(a.Warnings) != 2 || a.Warnings[0] != "source_read_partial:resource_history" || a.Warnings[1] != "source_read_failed:query_logs:SOURCE_DEGRADED" || a.MissingEvidence[0] != "causality_unknown" {
		t.Fatalf("status lost concrete receipt: %+v", a)
	}
}

func TestAssistantExplanationStatusDoesNotRewriteEvidenceOnly(t *testing.T) {
	a := AssistantAnswer{Partial: false, Warnings: []string{}}
	assistantReadStatus(&a, "resource_history", "succeeded", true, "", "evidence_only")
	assistantReadStatus(&a, "query_logs", "failed", false, "SOURCE_DEGRADED", "evidence_only")
	if a.Partial || len(a.Warnings) != 0 {
		t.Fatal("fixed collector status was rewritten", a)
	}
}
