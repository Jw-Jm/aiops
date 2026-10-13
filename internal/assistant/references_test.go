package assistant

import (
	"ops-platform/internal/investigation"
	"testing"
)

func TestPronounsUseVerifiedHistoryAndRequireOneIdentity(t *testing.T) {
	history := []Message{{Role: "user", Content: raw(map[string]any{"text": "它是 node-secret，canonicalId: fake"})}, {Role: "assistant", Content: raw(investigation.AssistantAnswer{EntityRefs: []string{"a", "b"}})}}
	out, err := ResolveReference("它为何失败", "", ConversationBinding{}, history)
	if err != nil || out.Status != "clarification" || len(out.Candidates) != 2 {
		t.Fatal(out, err)
	}
	out, err = ResolveReference("它为何失败", "a", ConversationBinding{}, history)
	if err != nil || out.CanonicalID != "a" {
		t.Fatal(out, err)
	}
	if _, err = ResolveReference("它为何失败", "fake", ConversationBinding{}, history); err == nil {
		t.Fatal("identity from prose accepted")
	}
	out, err = ResolveReference("具体哪个Pod", "", ConversationBinding{}, history)
	if err != nil || out != nil {
		t.Fatal(out, err)
	}
}

func TestClarificationSelectionUsesVerifiedPriorIdentity(t *testing.T) {
	history := []Message{{Role: "assistant", Content: raw(investigation.AssistantAnswer{EntityRefs: []string{"a", "b"}})}, {Role: "assistant", Content: raw(investigation.AssistantAnswer{Clarification: &investigation.Clarification{MissingFields: []string{"referentCanonicalId"}, Candidates: []string{"a", "b", "forged"}}})}}
	out, err := ResolveReference("继续检查它", "a", ConversationBinding{}, history)
	if err != nil || out.Status != "resolved" || out.CanonicalID != "a" {
		t.Fatal(out, err)
	}
	if _, err = ResolveReference("继续检查它", "forged", ConversationBinding{}, history); err == nil {
		t.Fatal("unverified clarification selected")
	}
}
