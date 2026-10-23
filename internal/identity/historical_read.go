package identity

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrHistoricalReadUnavailable is returned when a reviewed mapping cannot be
// listed for the current tenant. It is a denial, never a fallback to matching
// historical rows by name, subject prefix or the legacy binding table.
var ErrHistoricalReadUnavailable = errors.New("historical ownership mapping is unavailable")

// LegacySubjectMapping is one explicitly reviewed mapping from a pre-native
// subject to a native account. It authorises nothing by itself: callers must
// still re-prove the account's current native role, cluster and namespace
// authority in the same transaction.
type LegacySubjectMapping struct {
	MappingID      uuid.UUID
	LegacySubject  string
	EvidenceDigest string
}

// MappedLegacySubjectsTx lists the mappings that let this account read history
// still owned by a pre-native subject. The database function enforces the
// tenant transaction, an active tenant and account, the verified_inventory
// basis and a referenced reviewed inventory; it exposes no credential data.
func MappedLegacySubjectsTx(ctx context.Context, tx pgx.Tx, tenant, userID uuid.UUID) ([]LegacySubjectMapping, error) {
	if tx == nil || tenant == uuid.Nil || userID == uuid.Nil {
		return nil, ErrHistoricalReadUnavailable
	}
	rows, err := tx.Query(ctx, `SELECT mapping_id,legacy_subject,evidence_digest FROM identity.read_mapped_legacy_subjects($1,$2)`, tenant, userID)
	if err != nil {
		return nil, ErrHistoricalReadUnavailable
	}
	out := []LegacySubjectMapping{}
	for rows.Next() {
		var mapping LegacySubjectMapping
		var digest []byte
		if err := rows.Scan(&mapping.MappingID, &mapping.LegacySubject, &digest); err != nil {
			rows.Close()
			return nil, ErrHistoricalReadUnavailable
		}
		if mapping.MappingID == uuid.Nil || mapping.LegacySubject == "" || len(digest) != 32 {
			rows.Close()
			return nil, ErrHistoricalReadUnavailable
		}
		mapping.EvidenceDigest = "sha256:" + DigestHex(digest)
		out = append(out, mapping)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, ErrHistoricalReadUnavailable
	}
	rows.Close()
	return out, nil
}
