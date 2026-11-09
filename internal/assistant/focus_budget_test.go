package assistant

import (
	"encoding/json"
	"errors"
	"ops-platform/internal/graph"
	"ops-platform/internal/investigation"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestNormalizeFocusAcceptsUserIntentAndRejectsControlInput(t *testing.T) {
	focus, err := NormalizeFocus("  只看最近一次重启  ")
	if err != nil || focus != "只看最近一次重启" {
		t.Fatalf("focus was not normalized: %q %v", focus, err)
	}
	if empty, err := NormalizeFocus("   "); err != nil || empty != "" {
		t.Fatalf("an empty focus must stay empty: %q %v", empty, err)
	}
	if _, err = NormalizeFocus("line\nbreak"); !errors.Is(err, investigation.ErrInvalid) {
		t.Fatalf("a control character was accepted: %v", err)
	}
	if _, err = NormalizeFocus(strings.Repeat("查", 501)); !errors.Is(err, investigation.ErrInvalid) {
		t.Fatalf("an oversized focus was accepted: %v", err)
	}
	if _, err = NormalizeFocus(strings.Repeat("查", 500)); err != nil {
		t.Fatalf("a focus at the limit was rejected: %v", err)
	}
}

// TestNormalizeFocusRevalidatesAfterRedaction covers the boundary the
// independent probe found: redaction can grow the focus, and the persisted
// value must still fit the column. An oversized redacted focus is refused
// instead of being truncated silently.
func TestNormalizeFocusRevalidatesAfterRedaction(t *testing.T) {
	// 492 characters plus a secret assignment: the redaction replaces the
	// assignment with a longer marker and the result exceeds the limit.
	oversized := strings.Repeat("查", 492) + "token=x"
	redacted, err := NormalizeFocus(oversized)
	if !errors.Is(err, investigation.ErrInvalid) {
		t.Fatalf("an oversized redacted focus was accepted: %q %v", redacted, err)
	}
	if redacted != "" {
		t.Fatalf("a refused focus must not be returned: %q", redacted)
	}
	// A focus that stays within the limit after the redaction is accepted and
	// carries the redacted marker.
	fitting := strings.Repeat("查", 480) + "token=x"
	redacted, err = NormalizeFocus(fitting)
	if err != nil {
		t.Fatalf("a focus within the limit after the redaction was rejected: %v", err)
	}
	if utf8.RuneCountInString(redacted) > FocusMaxLength {
		t.Fatalf("the redacted focus exceeds the limit: %d", utf8.RuneCountInString(redacted))
	}
	if strings.Contains(redacted, "token=x") {
		t.Fatalf("the secret assignment was not redacted: %q", redacted)
	}
}

// TestSubmitMessageRejectsInvalidBudgetTypes covers the contract of the
// requested narrowing: the object is optional, but an explicit null, a
// fraction, a string, a boolean or an unknown dimension is refused instead of
// being read as "not requested".
func TestSubmitMessageRejectsInvalidBudgetTypes(t *testing.T) {
	valid := []string{
		`{"message":"m","expectedConversationRevision":1}`,
		`{"message":"m","expectedConversationRevision":1,"budgetTightening":{}}`,
		`{"message":"m","expectedConversationRevision":1,"budgetTightening":{"toolCalls":0}}`,
		`{"message":"m","expectedConversationRevision":1,"budgetTightening":{"toolCalls":2,"modelRequests":1}}`,
	}
	for _, raw := range valid {
		var q SubmitMessage
		if err := json.Unmarshal([]byte(raw), &q); err != nil {
			t.Fatalf("a valid request was refused: %s %v", raw, err)
		}
	}
	invalid := []string{
		`{"message":"m","expectedConversationRevision":1,"budgetTightening":null}`,
		`{"message":"m","expectedConversationRevision":1,"budgetTightening":{"toolCalls":null}}`,
		`{"message":"m","expectedConversationRevision":1,"budgetTightening":{"toolCalls":1.5}}`,
		`{"message":"m","expectedConversationRevision":1,"budgetTightening":{"toolCalls":"2"}}`,
		`{"message":"m","expectedConversationRevision":1,"budgetTightening":{"toolCalls":true}}`,
		`{"message":"m","expectedConversationRevision":1,"budgetTightening":{"toolCalls":2,"unknown":1}}`,
		`{"message":"m","expectedConversationRevision":1,"unknown":1}`,
	}
	for _, raw := range invalid {
		var q SubmitMessage
		if err := json.Unmarshal([]byte(raw), &q); !errors.Is(err, investigation.ErrInvalid) {
			t.Fatalf("an invalid request was accepted: %s %v", raw, err)
		}
	}
}

func TestFocusAndEffectiveBudgetAreFrozenIntoTheInputDigest(t *testing.T) {
	base := TurnInput{Message: "统计最近一小时", SchemaVersion: "assistant-input/v1"}
	budget := investigation.DefaultBudget()
	withFocus := base
	withFocus.Focus = "关注重启"
	withFocus.EffectiveBudget = &budget
	digest, err := InputDigest(withFocus)
	if err != nil {
		t.Fatal(err)
	}
	changed := withFocus
	changed.Focus = "关注网络"
	if InputDigestMatches(changed, digest) {
		t.Fatal("a changed focus was admitted as the same input")
	}
	narrowed := withFocus
	less := investigation.DefaultBudget()
	less.ToolCalls = 5
	narrowed.EffectiveBudget = &less
	if InputDigestMatches(narrowed, digest) {
		t.Fatal("a changed effective budget was admitted as the same input")
	}
	// A turn that was admitted before the focus and the effective budget
	// existed keeps its own digest: the legacy summary still matches.
	legacy := graph.ScopeDigest(base)
	if !InputDigestMatches(base, legacy) {
		t.Fatal("the existing admitted input was rejected")
	}
	// The stored JSONB of an old turn carries neither field and must still
	// produce the legacy digest.
	raw, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	var decoded TurnInput
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if !InputDigestMatches(decoded, legacy) {
		t.Fatal("the stored JSONB of an old turn no longer matches its digest")
	}
}
