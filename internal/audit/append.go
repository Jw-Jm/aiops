package audit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/bundle"
)

type Entry struct {
	TenantID   uuid.UUID
	RecordID   uuid.UUID
	EventType  string
	EntityKind string
	EntityID   uuid.UUID
	Subject    string
	Payload    map[string]any
	CreatedAt  time.Time
}

type Sequence struct {
	Audit  int64
	Tenant int64
}

func Append(ctx context.Context, tx pgx.Tx, entry Entry) (Sequence, error) {
	if entry.TenantID == uuid.Nil || entry.RecordID == uuid.Nil || entry.EventType == "" ||
		entry.EntityKind == "" || entry.Subject == "" || entry.Payload == nil {
		return Sequence{}, errors.New("audit entry is incomplete")
	}
	record, err := json.Marshal(entry.Payload)
	if err != nil {
		return Sequence{}, fmt.Errorf("encode audit payload: %w", err)
	}
	// Inspect precisely the JSON tree that will be stored, including typed
	// structures and custom marshalers. Normalize once so the digest cannot
	// observe a different result from a stateful marshaler.
	record, err = bundle.CanonicalizeJSON(record)
	if err != nil {
		return Sequence{}, fmt.Errorf("canonicalize audit payload: %w", err)
	}
	entry.Payload = nil
	decoder := json.NewDecoder(bytes.NewReader(record))
	decoder.UseNumber()
	if err := decoder.Decode(&entry.Payload); err != nil {
		return Sequence{}, fmt.Errorf("decode audit payload: %w", err)
	}
	if containsSecretField(entry.Payload) {
		return Sequence{}, errors.New("audit payload contains a prohibited secret or raw command field")
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	entry.CreatedAt = entry.CreatedAt.UTC().Truncate(time.Microsecond)
	canonical, err := canonicalEntry(entry)
	if err != nil {
		return Sequence{}, fmt.Errorf("encode canonical audit event: %w", err)
	}
	digest := sha256.Sum256(canonical)
	var sequence Sequence
	err = tx.QueryRow(ctx, "SELECT audit_seq, tenant_seq "+
		"FROM audit.append_record_v2($1, $2, $3, $4, $5, $6, $7::jsonb, $8, $9)",
		entry.TenantID, entry.RecordID, entry.EventType, entry.EntityKind, entry.EntityID,
		entry.Subject, record, digest[:], entry.CreatedAt).Scan(&sequence.Audit, &sequence.Tenant)
	if err != nil {
		return Sequence{}, fmt.Errorf("append audit record: %w", err)
	}
	return sequence, nil
}

type canonicalRecord struct {
	TenantID   uuid.UUID      `json:"tenantId"`
	RecordID   uuid.UUID      `json:"recordId"`
	EventType  string         `json:"eventType"`
	EntityKind string         `json:"entityKind"`
	EntityID   *uuid.UUID     `json:"entityId"`
	Subject    string         `json:"subject"`
	Record     map[string]any `json:"record"`
	CreatedAt  string         `json:"createdAt"`
}

func canonicalEntry(entry Entry) ([]byte, error) {
	var entityID *uuid.UUID
	if entry.EntityID != uuid.Nil {
		id := entry.EntityID
		entityID = &id
	}
	encoded, err := json.Marshal(canonicalRecord{
		TenantID: entry.TenantID, RecordID: entry.RecordID, EventType: entry.EventType,
		EntityKind: entry.EntityKind, EntityID: entityID, Subject: entry.Subject,
		Record: entry.Payload, CreatedAt: entry.CreatedAt.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return nil, err
	}
	return bundle.CanonicalizeJSON(encoded)
}

func containsSecretField(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			name := strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(key))
			if name == "token" || strings.HasSuffix(name, "token") || name == "secret" || strings.HasSuffix(name, "secret") ||
				name == "password" || strings.HasSuffix(name, "password") || strings.Contains(name, "credential") ||
				name == "authorization" || name == "ciphertext" || name == "command" || name == "prompt" ||
				name == "stdout" || name == "stderr" || rawTextField(name) {
				return true
			}
			if containsSecretField(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if containsSecretField(child) {
				return true
			}
		}
	}
	return false
}

func rawTextField(name string) bool {
	if strings.HasSuffix(name, "digest") || strings.HasSuffix(name, "id") || strings.HasSuffix(name, "ref") || strings.HasSuffix(name, "version") {
		return false
	}
	return strings.Contains(name, "command") || strings.Contains(name, "prompt")
}
