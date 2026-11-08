package assistant

import (
	"encoding/json"
	"ops-platform/internal/graph"
	"strings"
	"testing"
)

func TestInputDigestSurvivesJSONBHistoryReorderingAndCompaction(t *testing.T) {
	input := TurnInput{Message: "follow up", History: []Message{{Role: "assistant", Content: json.RawMessage(`{"summary":"observed","partial":true,"revision":771}`)}}}
	digest, err := InputDigest(input)
	if err != nil {
		t.Fatal(err)
	}
	legacy := graph.ScopeDigest(input)
	if !InputDigestMatches(input, legacy) {
		t.Fatal("existing admitted input rejected")
	}
	input.History[0].Content = json.RawMessage(`{"partial":true,"summary":"observed","revision":771}`)
	if !InputDigestMatches(input, digest) {
		t.Fatal("JSONB key order invalidated input")
	}
	input.History[0].Content = json.RawMessage(`{"partial":false,"summary":"observed","revision":771}`)
	if InputDigestMatches(input, digest) {
		t.Fatal("changed history admitted")
	}
	messages := []Message{}
	for i := 0; i < 6; i++ {
		messages = append(messages, Message{Role: "user", Content: json.RawMessage(`"question"`)}, Message{Role: "assistant", Content: raw(map[string]any{"schemaVersion": "assistant-answer/v1", "summary": strings.Repeat("事实", 500), "partial": true})})
	}
	input.History, err = prepareHistory(messages)
	if err != nil {
		t.Fatal(err)
	}
	digest, err = InputDigest(input)
	if err != nil {
		t.Fatal(err)
	}
	// Model PostgreSQL's object normalization with a decode/re-encode of each raw object.
	for i := range input.History {
		var value any
		if err = json.Unmarshal(input.History[i].Content, &value); err != nil {
			t.Fatal(err)
		}
		input.History[i].Content = raw(value)
	}
	if !InputDigestMatches(input, digest) {
		t.Fatal("six-round compacted history rejected")
	}
}
