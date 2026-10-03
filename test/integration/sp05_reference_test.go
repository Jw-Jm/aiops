package integration

import (
	"errors"
	"github.com/google/uuid"
	"ops-platform/internal/finding"
	"testing"
)

func TestSP05FindingRejectsMissingEvidenceWithoutPartialCommit(t *testing.T) {
	ctx, db, pool, b := sp05Database(t)
	e := sp05Envelope(b, "missing-reference", "o1")
	e.EvidenceRefs = []string{uuid.NewString()}
	_, _, err := (finding.Service{Pool: pool}).Ingest(ctx, b, e)
	if !errors.Is(err, finding.ErrUnauthorized) {
		t.Fatalf("unverified evidence reference accepted: %v", err)
	}
	for _, table := range []string{"finding.records", "finding.inbox", "finding.outbox", "finding.timeline"} {
		var n int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("partial reference commit %s=%d: %v", table, n, err)
		}
	}
}
