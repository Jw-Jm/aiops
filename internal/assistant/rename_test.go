package assistant

import (
	"errors"
	"strings"
	"testing"

	"ops-platform/internal/investigation"
)

func TestNormalizeTitle(t *testing.T) {
	if _, err := NormalizeTitle("   "); !errors.Is(err, investigation.ErrInvalid) {
		t.Fatalf("blank title accepted: %v", err)
	}
	if _, err := NormalizeTitle(strings.Repeat("会", 161)); !errors.Is(err, investigation.ErrInvalid) {
		t.Fatalf("oversized title accepted: %v", err)
	}
	if _, err := NormalizeTitle("标题\n注入"); !errors.Is(err, investigation.ErrInvalid) {
		t.Fatalf("control character accepted: %v", err)
	}
	title, err := NormalizeTitle("  生产 Pod 排查  ")
	if err != nil || title != "生产 Pod 排查" {
		t.Fatalf("title not normalized: %q %v", title, err)
	}
}

func TestConversationFilterNormalize(t *testing.T) {
	query, archived, err := ConversationFilter{}.Normalize()
	if err != nil || query != "" || archived != "exclude" {
		t.Fatalf("default filter: %q %q %v", query, archived, err)
	}
	if _, _, err = (ConversationFilter{Archived: "everything"}).Normalize(); !errors.Is(err, investigation.ErrInvalid) {
		t.Fatalf("unknown archive mode accepted: %v", err)
	}
	if _, _, err = (ConversationFilter{Query: strings.Repeat("x", 161)}).Normalize(); !errors.Is(err, investigation.ErrInvalid) {
		t.Fatalf("oversized query accepted: %v", err)
	}
	if _, _, err = (ConversationFilter{Archived: "only"}).Normalize(); err != nil {
		t.Fatalf("archive mode only rejected: %v", err)
	}
}

// The search term reaches the database as data, never as a pattern.
func TestEscapeLike(t *testing.T) {
	if got := escapeLike("100%_x"); got != `100\%\_x` {
		t.Fatalf("wildcards not escaped: %q", got)
	}
	if got := escapeLike(`a\b`); got != `a\\b` {
		t.Fatalf("escape character not escaped: %q", got)
	}
}
