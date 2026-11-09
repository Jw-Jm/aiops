package assistant

import (
	"context"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/audit"
	"ops-platform/internal/graph"
	"ops-platform/internal/investigation"
	"ops-platform/internal/investigation/tools"
	"ops-platform/internal/persistence"
)

// TitleMaxLength bounds a personal conversation name.
const TitleMaxLength = 160

// NormalizeTitle trims and validates a personal conversation name. A name is
// metadata only: it never carries scope, and it never becomes model input.
func NormalizeTitle(raw string) (string, error) {
	title := strings.TrimSpace(raw)
	if title == "" || utf8.RuneCountInString(title) > TitleMaxLength {
		return "", investigation.ErrInvalid
	}
	for _, r := range title {
		if unicode.IsControl(r) {
			return "", investigation.ErrInvalid
		}
	}
	return title, nil
}

// CanonicalTitle is the single boundary for a persisted conversation name.
// It applies the same disclosure rules as the creation path
// (repository.Create redacts the title before it is stored) so that a rename
// can never persist in clear text what the creation path would have redacted.
// The redacted value is validated again: redaction must not produce an empty
// or oversized name.
func CanonicalTitle(raw string) (string, error) {
	name, err := NormalizeTitle(raw)
	if err != nil {
		return "", err
	}
	redacted := tools.RedactText(name)
	if _, err = NormalizeTitle(redacted); err != nil {
		return "", investigation.ErrInvalid
	}
	return redacted, nil
}

// Rename persists a new name for a personal conversation. The owner is
// re-verified, the current grant is re-checked and the expected revision
// fences a concurrent change. The replay of the same idempotency key returns
// the stored conversation instead of renaming it twice.
//
// Semantics:
//   - a different title bumps the revision and the update timestamp, and the
//     committed values are returned;
//   - the same title is a no-op: neither the revision nor the update timestamp
//     moves, only the idempotency ledger records the accepted request;
//   - the same key with the same payload replays without a second write;
//   - the same key with a different payload is rejected as a conflict, which
//     also covers a key that was already used by a different operation.
func (r Repository) Rename(ctx context.Context, tenant, id uuid.UUID, subject, key string, expected int64, title string) (Conversation, error) {
	var out Conversation
	name, err := CanonicalTitle(title)
	if err != nil {
		return out, err
	}
	if key == "" || len(key) > 128 || expected < 1 {
		return out, investigation.ErrInvalid
	}
	digest := graph.ScopeDigest(map[string]any{"operation": "assistant-conversation-rename", "conversationId": id.String(), "title": name, "expectedRevision": expected})
	err = persistence.WithTenantTx(ctx, r.Pool, tenant, func(tx pgx.Tx) error {
		var err error
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, tenant.String()+"|assistant-rename|"+subject+"|"+key); err != nil {
			return err
		}
		out, err = loadConversation(ctx, tx, tenant, id, subject, true)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return investigation.ErrDenied
			}
			return err
		}
		// The current grant is re-checked: a revoked binding must not be renamed.
		if _, err = authorizeConversation(ctx, tx, out); err != nil {
			return err
		}
		var priorID uuid.UUID
		var priorDigest string
		err = tx.QueryRow(ctx, `SELECT conversation_id,request_digest FROM investigation.assistant_binding_requests WHERE tenant_id=$1 AND owner_subject=$2 AND idempotency_key=$3`, tenant, subject, key).Scan(&priorID, &priorDigest)
		if err == nil {
			// The same key was already used. An identical payload replays and
			// returns the stored conversation; any other payload, including a
			// key that belongs to a different operation, is refused.
			if priorID != id || priorDigest != digest {
				return investigation.ErrConflict
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if out.Revision != expected {
			return ErrStale
		}
		noop := out.Title == name
		if !noop {
			// The committed values are returned, not the values that were read
			// before the write.
			if err = tx.QueryRow(ctx, `UPDATE investigation.assistant_conversations SET title=$3,revision=revision+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND conversation_id=$2 RETURNING title,revision,updated_at`, tenant, id, name).Scan(&out.Title, &out.Revision, &out.UpdatedAt); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(ctx, `INSERT INTO investigation.assistant_binding_requests(tenant_id,owner_subject,idempotency_key,conversation_id,request_digest,revision) VALUES($1,$2,$3,$4,$5,$6)`, tenant, subject, key, id, digest, out.Revision); err != nil {
			return err
		}
		_, err = audit.Append(ctx, tx, audit.Entry{TenantID: tenant, RecordID: uuid.Must(uuid.NewV7()), EntityID: id, EntityKind: "assistant_conversation", EventType: "assistant.conversation.renamed", Subject: subject, Payload: map[string]any{"revision": out.Revision, "titleLength": utf8.RuneCountInString(name), "noop": noop}})
		return err
	})
	if err != nil {
		return Conversation{}, err
	}
	return out, nil
}
