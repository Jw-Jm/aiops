package investigation

import (
	"testing"

	"github.com/google/uuid"
	"ops-platform/internal/graph"
)

func TestNativeAuditRevisionChangesWhenAuthorityChanges(t *testing.T) {
	userID := uuid.MustParse("01a1627d-4cd4-77b9-bacc-ed412395c861")
	grantA := uuid.MustParse("01a1627d-4cd4-77b9-bacc-ed412395c870")
	grantB := uuid.MustParse("01a1627d-4cd4-77b9-bacc-ed412395c871")
	base := NativeAuditRevision(12, "user:"+userID.String(), userID, 4, []uuid.UUID{grantA, grantB})
	if base != NativeAuditRevision(12, "user:"+userID.String(), userID, 4, []uuid.UUID{grantB, grantA}) {
		t.Fatal("grant order must not change the digest")
	}
	for name, changed := range map[string]string{
		"authorization version bumped by revoke and re-grant": NativeAuditRevision(12, "user:"+userID.String(), userID, 5, []uuid.UUID{grantA, grantB}),
		"grant replaced":        NativeAuditRevision(12, "user:"+userID.String(), userID, 4, []uuid.UUID{grantB}),
		"tenant revision moved": NativeAuditRevision(13, "user:"+userID.String(), userID, 4, []uuid.UUID{grantA, grantB}),
		"other subject":         NativeAuditRevision(12, "user:someone-else", userID, 4, []uuid.UUID{grantA, grantB}),
		"other account":         NativeAuditRevision(12, "user:"+userID.String(), uuid.MustParse("01a1627d-4cd4-77b9-bacc-ed412395c899"), 4, []uuid.UUID{grantA, grantB}),
	} {
		if changed == base {
			t.Fatalf("%s did not change the audit authorization revision", name)
		}
	}
}

func TestAssistantTaskRequiresExplicitAuditAuthority(t *testing.T) {
	userID := uuid.MustParse("01a1627d-4cd4-77b9-bacc-ed412395c861")
	tenantID := uuid.MustParse("293812d4-deb8-4f15-aaa2-5aa05e6e3b00")
	scope := graph.Scope{
		Tenant: tenantID.String(), Namespaces: []string{}, Resources: []string{},
		AuthorizationRevision: "sha256:test",
	}
	job := Job{
		TenantID:             tenantID,
		Scope:                scope,
		EffectiveScopeDigest: graph.ScopeDigest(scope),
	}
	base := AssistantTask{
		TaskKind: "data_query", AuthorizationDomain: "audit",
		ConversationID: uuid.MustParse("01a1627d-4cd4-77b9-bacc-ed412395c880"),
		TurnID:         uuid.MustParse("01a1627d-4cd4-77b9-bacc-ed412395c881"),
		InputDigest:    "sha256:in", ContextDigest: "sha256:ctx",
		GrantSnapshot: scope, GrantSnapshotDigest: graph.ScopeDigest(scope),
		DatasetCatalogVersion: "analysis-catalog/v1", DatasetCatalogDigest: "sha256:catalog",
		CatalogVersion: "assistant-tools/v2", PromptVersion: "assistant/v1",
		ModelProfileVersion: "profile/v1", ResultSchemaVersion: "assistant-answer/v1",
	}
	for name, mutate := range map[string]func(*AssistantTask){
		"native without user":        func(a *AssistantTask) { a.AuthorizationSource = AuditSourceNative; a.AuthorizationVersion = 3 },
		"native without version":     func(a *AssistantTask) { a.AuthorizationSource = AuditSourceNative; a.AuthorizationUserID = userID },
		"legacy carrying a user":     func(a *AssistantTask) { a.AuthorizationSource = AuditSourceLegacy; a.AuthorizationUserID = userID },
		"unknown source":             func(a *AssistantTask) { a.AuthorizationSource = "oidc" },
		"empty source with a user":   func(a *AssistantTask) { a.AuthorizationUserID = userID },
		"empty source with a verson": func(a *AssistantTask) { a.AuthorizationVersion = 2 },
	} {
		t.Run(name, func(t *testing.T) {
			task := base
			mutate(&task)
			if task.Valid(job) {
				t.Fatalf("%s must not be a valid audit job", name)
			}
		})
	}
	for name, mutate := range map[string]func(*AssistantTask){
		"native": func(a *AssistantTask) {
			a.AuthorizationSource = AuditSourceNative
			a.AuthorizationUserID = userID
			a.AuthorizationVersion = 3
		},
		"legacy":  func(a *AssistantTask) { a.AuthorizationSource = AuditSourceLegacy },
		"history": func(a *AssistantTask) {},
	} {
		t.Run(name, func(t *testing.T) {
			task := base
			mutate(&task)
			if !task.Valid(job) {
				t.Fatalf("%s audit job must stay valid", name)
			}
		})
	}
}
