package finding

import (
	"encoding/json"
	"testing"
)

func TestSemanticDigestPreservesJSONNumberPrecision(t *testing.T) {
	for _, pair := range [][2]string{{`{"counter":9007199254740992}`, `{"counter":9007199254740993}`}, {`{"value":0.123456789012345678901}`, `{"value":0.123456789012345678902}`}} {
		if SemanticDigest(Envelope{Payload: json.RawMessage(pair[0])}) == SemanticDigest(Envelope{Payload: json.RawMessage(pair[1])}) {
			t.Errorf("distinct exact numeric payloads collided: %s %s", pair[0], pair[1])
		}
	}
	if SemanticDigest(Envelope{Payload: json.RawMessage(`{"b":2,"a":1}`)}) != SemanticDigest(Envelope{Payload: json.RawMessage(`{ "a":1, "b":2 }`)}) {
		t.Fatal("object key order or whitespace changed digest")
	}
}
