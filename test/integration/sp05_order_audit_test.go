package integration

import (
	"encoding/json"
	"errors"
	"ops-platform/internal/finding"
	"testing"
)

func TestSP05EqualSequenceConflictAndTransportAliasAreAudited(t *testing.T) {
	ctx, db, pool, b := sp05Database(t)
	service := finding.Service{Pool: pool}
	e := sp05Envelope(b, "original", "birth")
	if _, _, err := service.Ingest(ctx, b, e); err != nil {
		t.Fatal(err)
	}
	alias := e
	alias.EventID = "alias"
	if _, dis, err := service.Ingest(ctx, b, alias); err != nil || dis != finding.Duplicate {
		t.Fatalf("alias %s: %v", dis, err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM finding.inbox WHERE event_id='alias'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("alias inbox %d: %v", n, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM finding.timeline WHERE event_id='alias' AND disposition='duplicate'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("alias timeline %d: %v", n, err)
	}
	ambiguous := e
	ambiguous.EventID, ambiguous.IdempotencyKey = "ambiguous", "ambiguous"
	ambiguous.Payload = json.RawMessage(`{"ready":false,"reason":"different"}`)
	for range 2 {
		if _, _, err := service.Ingest(ctx, b, ambiguous); !errors.Is(err, finding.ErrConflict) {
			t.Fatalf("ambiguous sequence accepted: %v", err)
		}
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM finding.rejections WHERE error_code='IDEMPOTENCY_CONFLICT'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("conflict audit %d: %v", n, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM finding.outbox`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("conflict mutated business data %d: %v", n, err)
	}
}
