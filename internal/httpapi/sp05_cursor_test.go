package httpapi

import (
	"crypto/ed25519"
	"crypto/rand"
	"github.com/google/uuid"
	"net/http/httptest"
	"ops-platform/internal/auth"
	"ops-platform/internal/graph"
	"ops-platform/internal/incident"
	"ops-platform/internal/rca"
	"testing"
)

func TestIncidentPageRejectsForeignSubjectCursorBeforeDatabase(t *testing.T) {
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	actor := auth.RequestContext{TenantID: uuid.New(), Subject: "other"}
	scope := graph.Scope{Tenant: actor.TenantID.String()}
	token, err := encodeCursor(key, graphCursor{ScopeDigest: graph.ScopeDigest([]any{actor.TenantID.String(), "owner", scope}), QueryDigest: "query", After: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	h := SP05Handlers{GraphAPI: &SP04Handlers{SigningKey: key}}
	_, _, err = h.incidentPage(t.Context(), actor, scope, incident.Incident{}, httptest.NewRequest("GET", "/api/v1/incidents/id/timeline?cursor="+token, nil), "timeline", 10)
	if err != rca.ErrStale {
		t.Fatal("foreign cursor reached persistence", err)
	}
}
