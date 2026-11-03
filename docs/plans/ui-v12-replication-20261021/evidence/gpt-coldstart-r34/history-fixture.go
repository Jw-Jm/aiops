//go:build ignore

// Controlled migration history only, on the task's dedicated new database.
package main

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"ops-platform/internal/app"
	"ops-platform/internal/assistant"
	"ops-platform/internal/graph"
	"os"
	"time"
)

func main() {
	const root = "/data/ops-v12-native-cold-r34-20261027/"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	raw, err := os.ReadFile(root + "private/database-urls.json")
	check(err)
	urls := map[string]string{}
	check(json.Unmarshal(raw, &urls))
	raw, err = os.ReadFile(root + "evidence/first-tenant.json")
	check(err)
	var tenant struct {
		TenantID uuid.UUID `json:"tenantId"`
	}
	check(json.Unmarshal(raw, &tenant))
	pool, err := app.OpenRuntimePool(ctx, urls["api"], "api_runtime_role")
	check(err)
	defer pool.Close()
	repo := assistant.Repository{Pool: pool}
	var c assistant.Conversation
	if len(os.Args) == 2 && os.Args[1] == "inspect" {
		previous, err := os.ReadFile(root + "evidence/history-fixture.json")
		check(err)
		var expected struct {
			ConversationID uuid.UUID `json:"conversationId"`
			BindingDigest  string    `json:"bindingDigest"`
			Revision       int64     `json:"revision"`
		}
		check(json.Unmarshal(previous, &expected))
		c, err = repo.Get(ctx, tenant.TenantID, expected.ConversationID, "r34.legacy.owner")
		check(err)
		if c.OwnerSubject != "r34.legacy.owner" || graph.ScopeDigest(c.Binding) != expected.BindingDigest || c.Revision != expected.Revision {
			panic("frozen historical ownership or binding changed")
		}
	} else {
		c, err = repo.Create(ctx, assistant.Conversation{TenantID: tenant.TenantID, OwnerSubject: "r34.legacy.owner", Title: "受控迁移历史：只读归属验证（非真实旧库）", Mode: "data_query", AuthorizationDomain: "audit", Binding: assistant.ConversationBinding{Timezone: "Asia/Shanghai"}})
	}
	check(err)
	check(json.NewEncoder(os.Stdout).Encode(map[string]any{"controlledFixture": true, "tenantId": tenant.TenantID, "conversationId": c.ConversationID, "legacySubject": c.OwnerSubject, "bindingDigest": graph.ScopeDigest(c.Binding), "revision": c.Revision, "domain": c.AuthorizationDomain, "note": "no synthetic model answer, no new Job, no production history migration"}))
}
func check(err error) {
	if err != nil {
		panic("controlled historical fixture failed; no connection or credential is logged")
	}
}
