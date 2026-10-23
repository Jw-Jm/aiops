package httpapi

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/action"
	"ops-platform/internal/auth"
)

func TestExecutionProfilesPageKeepsVersionsAndRejectsHiddenAnchors(t *testing.T) {
	id := uuid.New()
	items := []action.Profile{{ID: id, Version: 1}, {ID: id, Version: 2}, {ID: uuid.New(), Version: 1}}
	first, next, err := paginateExecutionProfiles(items, "", 1)
	if err != nil || len(first) != 1 || first[0].Version != 1 || next == nil {
		t.Fatalf("first page: %v %v", first, err)
	}
	second, next2, err := paginateExecutionProfiles(items, *next, 1)
	if err != nil || len(second) != 1 || second[0].Version != 2 || next2 == nil {
		t.Fatalf("version skipped: %v %v", second, err)
	}
	last, end, err := paginateExecutionProfiles(items, *next2, 100)
	if err != nil || len(last) != 1 || last[0].ID != items[2].ID || end != nil {
		t.Fatalf("last page: %v %v", last, err)
	}
	if _, _, err := paginateExecutionProfiles(items[1:], *next, 1); !errors.Is(err, action.ErrInvalid) {
		t.Fatal("removed or unauthorized cursor anchor accepted")
	}
	for _, cursor := range []string{"invalid", strings.Repeat("A", 129), base64.RawURLEncoding.EncodeToString([]byte(id.String() + "@0")), base64.RawURLEncoding.EncodeToString([]byte(uuid.NewString() + "@1"))} {
		if _, _, err := paginateExecutionProfiles(items, cursor, 50); err == nil {
			t.Fatal("invalid cursor accepted")
		}
	}
	for _, limit := range []int{-1, 0, 201} {
		if _, _, err := paginateExecutionProfiles(items, "", limit); err == nil {
			t.Fatal("invalid limit accepted")
		}
	}
	empty, end, err := paginateExecutionProfiles([]action.Profile{}, "", 50)
	if err != nil || empty == nil || len(empty) != 0 || end != nil {
		t.Fatal("empty result must stay a JSON array")
	}
}

func TestExecutionProfileInvalidLimitRejectedBeforeDatabase(t *testing.T) {
	ctx := auth.WithRequestContext(context.Background(), auth.RequestContext{TenantID: uuid.New(), Subject: "fixture", Roles: []auth.Role{auth.PlatformAdmin}, TokenExpiresAt: time.Now().Add(time.Minute)})
	for _, limit := range []string{"0", "201", "bad"} {
		r := httptest.NewRequest("GET", "/api/v1/admin/execution-profiles?limit="+limit, nil).WithContext(ctx)
		w := httptest.NewRecorder()
		(&ActionHandlers{}).ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("limit=%s status=%d", limit, w.Code)
		}
	}
}
