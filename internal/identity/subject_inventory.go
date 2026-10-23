package identity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/audit"
	"ops-platform/internal/bundle"
)

type SubjectInventoryEntry struct {
	LegacySubject string    `json:"legacySubject"`
	UserID        uuid.UUID `json:"userId"`
}
type SubjectInventory struct {
	SchemaVersion string                  `json:"schemaVersion"`
	TenantID      uuid.UUID               `json:"tenantId"`
	ReviewedBy    string                  `json:"reviewedBy"`
	Entries       []SubjectInventoryEntry `json:"entries"`
}

// Parsing validates an already reviewed document, not a claim that its owner
// identities were verified by the software. The controlled operator owns that review.
func ParseSubjectInventory(raw []byte) (SubjectInventory, []byte, error) {
	var inventory SubjectInventory
	if len(raw) == 0 || len(raw) > 1<<20 {
		return inventory, nil, ErrSubjectMappingInvalid
	}
	canonical, err := bundle.CanonicalizeJSON(raw)
	if err != nil {
		return inventory, nil, ErrSubjectMappingInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&inventory) != nil || decoder.Decode(new(any)) != io.EOF {
		return inventory, nil, ErrSubjectMappingInvalid
	}
	if inventory.SchemaVersion != "native-subject-inventory/v1" || inventory.TenantID == uuid.Nil || strings.TrimSpace(inventory.ReviewedBy) == "" || strings.ContainsRune(inventory.ReviewedBy, 0) || utf8.RuneCountInString(inventory.ReviewedBy) > 256 || len(inventory.Entries) < 1 || len(inventory.Entries) > 1000 {
		return inventory, nil, ErrSubjectMappingInvalid
	}
	seen := map[string]bool{}
	for _, entry := range inventory.Entries {
		if validateSubject(entry.LegacySubject) != nil || strings.TrimSpace(entry.LegacySubject) == "" || strings.ContainsRune(entry.LegacySubject, 0) || entry.UserID == uuid.Nil || seen[entry.LegacySubject] {
			return inventory, nil, ErrSubjectMappingInvalid
		}
		seen[entry.LegacySubject] = true
	}
	return inventory, canonical, nil
}

type SubjectInventoryReceipt struct {
	TenantID       uuid.UUID `json:"tenantId"`
	EvidenceDigest string    `json:"evidenceDigest"`
	Entries        int       `json:"entries"`
	Imported       bool      `json:"imported"`
}

func (r *Repository) ImportSubjectInventory(ctx context.Context, raw []byte) (SubjectInventoryReceipt, error) {
	inventory, canonical, err := ParseSubjectInventory(raw)
	if err != nil {
		return SubjectInventoryReceipt{}, err
	}
	if r == nil || r.pool == nil {
		return SubjectInventoryReceipt{}, ErrRepositoryRequired
	}
	if err = RequireNativeBootstrapDuty(ctx, r.pool); err != nil {
		return SubjectInventoryReceipt{}, err
	}
	receipt := SubjectInventoryReceipt{TenantID: inventory.TenantID, EvidenceDigest: "sha256:" + DigestHex(DigestSHA256(canonical)), Entries: len(inventory.Entries)}
	err = r.withBootstrapTx(ctx, FirstAdministratorInput{TenantID: inventory.TenantID}, func(tx pgx.Tx, tenant uuid.UUID) error {
		if err := tx.QueryRow(ctx, `SELECT identity.import_subject_inventory($1,$2)`, tenant, string(canonical)).Scan(&receipt.Imported); err != nil {
			return fmt.Errorf("import reviewed subject inventory: %w", err)
		}
		if !receipt.Imported {
			return nil
		}
		_, err := audit.Append(ctx, tx, audit.Entry{TenantID: tenant, RecordID: uuid.Must(uuid.NewV7()), EventType: "identity.subject_inventory.imported", EntityKind: "subject_inventory", Subject: "migration:subject-inventory", Payload: map[string]any{"evidenceDigest": receipt.EvidenceDigest, "entries": receipt.Entries, "reviewedBy": inventory.ReviewedBy}})
		return err
	})
	if err != nil {
		return SubjectInventoryReceipt{}, errors.New("reviewed inventory was not imported; check migration duty, tenant history, targets and conflicting approvals")
	}
	return receipt, nil
}
