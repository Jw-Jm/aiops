package assistant

import (
	"encoding/json"
	"github.com/google/uuid"
	"ops-platform/internal/investigation"
	"strings"
	"testing"
	"time"
)

func TestHistoryCompactsTextWithoutLosingFactsOrTime(t *testing.T) {
	ref := uuid.New()
	observed := time.Now().UTC().Truncate(time.Second)
	answer := investigation.AssistantAnswer{SchemaVersion: "assistant-answer/v1", Summary: strings.Repeat("历史说明", 500), QueryResultRefs: []uuid.UUID{ref}, AsOf: observed}
	messages := []Message{}
	for i := 0; i < 6; i++ {
		messages = append(messages, Message{Role: "user", Content: raw(map[string]string{"message": "旧问题"})}, Message{Role: "assistant", Content: raw(answer)})
	}
	out, err := prepareHistory(messages)
	if err != nil || len(out) != 10 {
		t.Fatalf("history length=%d err=%v", len(out), err)
	}
	var summary investigation.AssistantAnswer
	if json.Unmarshal(out[0].Content, &summary) != nil || len([]rune(summary.Summary)) != 801 || summary.QueryResultRefs[0] != ref || !summary.AsOf.Equal(observed) {
		t.Fatal("structured summary lost identity, time or references")
	}
	if string(out[2].Content) != string(messages[4].Content) || string(out[3].Content) != string(messages[5].Content) {
		t.Fatal("recent messages changed")
	}
	messages[1].Content = []byte(`{"summary":`)
	if _, err = prepareHistory(messages); err == nil {
		t.Fatal("malformed answer accepted")
	}
}
