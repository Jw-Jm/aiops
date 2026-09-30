package audit

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Entry struct {
	TenantID   uuid.UUID
	RecordID   uuid.UUID
	EventType  string
	EntityKind string
	EntityID   uuid.UUID
	Subject    string
	Payload    map[string]any
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
	canonical, err := json.Marshal(entry)
	if err != nil {
		return Sequence{}, fmt.Errorf("encode canonical audit event: %w", err)
	}
	digest := sha256.Sum256(canonical)
	var sequence Sequence
	err = tx.QueryRow(ctx, "SELECT audit_seq, tenant_seq "+
		"FROM audit.append_record($1, $2, $3, $4, $5, $6, $7::jsonb, $8)",
		entry.TenantID, entry.RecordID, entry.EventType, entry.EntityKind, entry.EntityID,
		entry.Subject, record, digest[:]).Scan(&sequence.Audit, &sequence.Tenant)
	if err != nil {
		return Sequence{}, fmt.Errorf("append audit record: %w", err)
	}
	return sequence, nil
}
