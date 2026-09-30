package audit

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCanonicalEntryUsesRFC8785AndStableUTCPrecision(t *testing.T) {
	entry := Entry{
		TenantID:  uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987201"),
		RecordID:  uuid.MustParse("018f0f2b-91c2-7d42-a8dc-f719c5987202"),
		EventType: "policy.evaluated", EntityKind: "policy", Subject: "operator-1",
		Payload:   map[string]any{"z": 1.0, "a": "ok"},
		CreatedAt: time.Date(2026, 9, 30, 1, 2, 3, 123456789, time.FixedZone("UTC+8", 8*60*60)),
	}
	entry.CreatedAt = entry.CreatedAt.UTC().Truncate(time.Microsecond)
	canonical, err := canonicalEntry(entry)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(canonical, &got); err != nil {
		t.Fatal(err)
	}
	if string(canonical) != `{"createdAt":"2026-09-29T17:02:03.123456Z","entityId":null,"entityKind":"policy","eventType":"policy.evaluated","record":{"a":"ok","z":1},"recordId":"018f0f2b-91c2-7d42-a8dc-f719c5987202","subject":"operator-1","tenantId":"018f0f2b-91c2-7d42-a8dc-f719c5987201"}` {
		t.Fatalf("canonical entry = %s", canonical)
	}
}

func TestAuditPayloadRejectsSensitiveValuesButAllowsDigestsAndReferences(t *testing.T) {
	for _, payload := range []map[string]any{
		{"refresh_token": "not-logged"},
		{"nested": map[string]any{"password": "not-logged"}},
		{"command": "kubectl get secrets"},
		{"ciphertext": "vault:v1:abc="},
	} {
		if !containsSecretField(payload) {
			t.Fatalf("sensitive payload was accepted: %s", mustJSON(payload))
		}
	}
	if containsSecretField(map[string]any{"authRef": "secret-store/object/1", "commandDigest": "sha256:abcd"}) {
		t.Fatal("opaque references and digests should be permitted")
	}
}

func TestLegacyAuditDigestRemainsVerifiableAndIsCanonicalizedForSegments(t *testing.T) {
	entry := Entry{TenantID: uuid.New(), RecordID: uuid.New(), EventType: "source.updated", EntityKind: "source", EntityID: uuid.New(), Subject: "legacy", Payload: map[string]any{"rate": float64(1e20)}}
	legacy := struct {
		TenantID   uuid.UUID
		RecordID   uuid.UUID
		EventType  string
		EntityKind string
		EntityID   uuid.UUID
		Subject    string
		Payload    map[string]any
	}{entry.TenantID, entry.RecordID, entry.EventType, entry.EntityKind, entry.EntityID, entry.Subject, entry.Payload}
	oldBytes, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(oldBytes)
	row := recordRow{Entry: entry, CreatedAt: time.Date(2025, 1, 2, 3, 4, 5, 123000000, time.UTC), Digest: digest[:]}
	row.Entry.CreatedAt = row.CreatedAt
	if err := digestCanonicalRow(&row); err != nil {
		t.Fatalf("verify historical record digest: %v", err)
	}
	canonical, err := canonicalEntry(row.Entry)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(row.Canonical, canonical) {
		t.Fatal("historical row was not normalized to RFC 8785 bytes for segment hashing")
	}
}

func mustJSON(value any) string {
	encoded, _ := json.Marshal(value)
	return strings.TrimSpace(string(encoded))
}
