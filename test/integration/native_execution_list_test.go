package integration

import (
	"errors"
	"ops-platform/internal/action"
	"strings"
	"testing"
)

func TestNativeExecutionSearchAndRevocation(t *testing.T) {
	f := newNativeActionFixture(t)
	e := f.prepare(t, "search")
	for _, q := range []string{e.ID.String(), f.incidentID.String(), f.target, f.subject, strings.ToUpper(f.target)} {
		page, _, err := f.service.ListExecutions(f.accountCtx, f.actor(), nil, "prepared", q, 5)
		if err != nil || len(page) != 1 || page[0].ID != e.ID {
			t.Fatalf("native scoped search %q: %d %v", q, len(page), err)
		}
	}
	missing, _, err := f.service.ListExecutions(f.accountCtx, f.actor(), nil, "", "%_", 5)
	if err != nil || len(missing) != 0 {
		t.Fatalf("search wildcard: %d %v", len(missing), err)
	}
	f.revoke(t)
	page, _, err := f.service.ListExecutions(f.accountCtx, f.actor(), nil, "", e.ID.String(), 5)
	if len(page) > 0 || (err != nil && !errors.Is(err, action.ErrDenied)) {
		t.Fatalf("revoked search: %d %v", len(page), err)
	}
}
