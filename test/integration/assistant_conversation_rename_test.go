package integration

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"ops-platform/internal/analytics"
	"ops-platform/internal/assistant"
	"ops-platform/internal/auth"
	"ops-platform/internal/graph"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/investigation"
	"ops-platform/internal/resource"
	"ops-platform/internal/source"
)

// The cases below need a real PostgreSQL. Without one they are skipped, they
// are never reported as passed.
func assistantConversationFixture(t *testing.T, title string) (context.Context, *sql.DB, *pgxpool.Pool, source.BoundSourceContext, assistant.Repository, investigation.Repository) {
	t.Helper()
	ctx, db, worker, b, repo, jobs := assistantExtendedFixture(t)
	c, err := repo.Create(ctx, assistant.Conversation{TenantID: b.TenantID, OwnerSubject: "assistant-owner", Title: title, Mode: "data_query", AuthorizationDomain: "operational", ClusterUID: b.ClusterUID, Binding: assistant.ConversationBinding{Timezone: "UTC", Selection: analytics.Selection{Namespaces: []string{"apps"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Submit(ctx, b.TenantID, c.ConversationID, c.OwnerSubject, "first", assistant.SubmitMessage{Message: "统计过去一小时", ExpectedConversationRevision: 1}); err != nil {
		t.Fatal(err)
	}
	return ctx, db, worker, b, repo, jobs
}

// assistantTurnID reads the turn of the conversation that the fixture
// submitted. The fixture submits exactly one turn.
func assistantTurnID(t *testing.T, ctx context.Context, db *sql.DB, tenant uuid.UUID, conversation uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := db.QueryRowContext(ctx, `SELECT turn_id FROM investigation.assistant_turns WHERE tenant_id=$1 AND conversation_id=$2 ORDER BY created_at LIMIT 1`, tenant, conversation).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// finishAssistantJob ends the queued fixture job through the ordinary Job
// repository lifecycle (claim and complete). The product busy guard is never
// bypassed: the operation is only attempted after the job really ended.
func finishAssistantJob(t *testing.T, ctx context.Context, jobs investigation.Repository, tenant uuid.UUID, answer []byte) {
	t.Helper()
	lease, err := jobs.Claim(ctx, tenant, "fixture", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = jobs.Complete(ctx, lease, answer); err != nil {
		t.Fatal(err)
	}
}

func TestAssistantRenamePersistsAndAudits(t *testing.T) {
	ctx, db, _, b, repo, _ := assistantConversationFixture(t, "原始名称")
	page, err := repo.ListPage(ctx, b.TenantID, "assistant-owner", time.Time{}, uuid.Nil, 10, assistant.ConversationFilter{})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("list = %+v, %v", page.Items, err)
	}
	before := page.Items[0]
	out, err := repo.Rename(ctx, b.TenantID, before.ConversationID, "assistant-owner", "rename-key-1", before.Revision, "新的名称")
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if out.Title != "新的名称" || out.Revision != before.Revision+1 {
		t.Fatalf("rename result = %+v", out)
	}
	// The returned metadata is the committed one, not the value that was read
	// before the write.
	var storedTitle string
	var storedRevision int64
	var storedUpdated time.Time
	if err = db.QueryRowContext(ctx, `SELECT title,revision,updated_at FROM investigation.assistant_conversations WHERE tenant_id=$1 AND conversation_id=$2`, b.TenantID, before.ConversationID).Scan(&storedTitle, &storedRevision, &storedUpdated); err != nil {
		t.Fatal(err)
	}
	if storedTitle != "新的名称" || storedRevision != out.Revision || !storedUpdated.Equal(out.UpdatedAt) {
		t.Fatalf("stored = %q/%d/%s, returned = %+v", storedTitle, storedRevision, storedUpdated, out)
	}
	var audited int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM audit.records WHERE tenant_id=$1 AND event_type='assistant.conversation.renamed' AND entity_id=$2`, b.TenantID, before.ConversationID).Scan(&audited); err != nil {
		t.Fatal(err)
	}
	if audited != 1 {
		t.Fatalf("rename audit records = %d", audited)
	}
	// The rename is refused when the caller expected another revision.
	if _, err = repo.Rename(ctx, b.TenantID, before.ConversationID, "assistant-owner", "rename-key-2", before.Revision, "过期版本"); !errors.Is(err, assistant.ErrStale) {
		t.Fatalf("stale rename error = %v", err)
	}
	// The replay of the same key returns the stored conversation and does not
	// rename it a second time.
	replay, err := repo.Rename(ctx, b.TenantID, before.ConversationID, "assistant-owner", "rename-key-1", before.Revision, "新的名称")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if replay.Title != "新的名称" || replay.Revision != out.Revision {
		t.Fatalf("replay = %+v", out)
	}
	var ledger int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM investigation.assistant_binding_requests WHERE tenant_id=$1 AND owner_subject='assistant-owner' AND idempotency_key='rename-key-1'`, b.TenantID).Scan(&ledger); err != nil {
		t.Fatal(err)
	}
	if ledger != 1 {
		t.Fatalf("idempotency ledger rows = %d", ledger)
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM audit.records WHERE tenant_id=$1 AND event_type='assistant.conversation.renamed' AND entity_id=$2`, b.TenantID, before.ConversationID).Scan(&audited); err != nil {
		t.Fatal(err)
	}
	if audited != 1 {
		t.Fatalf("replay wrote a second audit record: %d", audited)
	}
	// The same key with another payload is refused.
	if _, err = repo.Rename(ctx, b.TenantID, before.ConversationID, "assistant-owner", "rename-key-1", before.Revision, "另一个名称"); !errors.Is(err, investigation.ErrConflict) {
		t.Fatalf("conflicting payload error = %v", err)
	}
}

func TestAssistantRenameNoopKeepsRevisionAndTimestamp(t *testing.T) {
	ctx, db, _, b, repo, _ := assistantConversationFixture(t, "保持不变")
	page, err := repo.ListPage(ctx, b.TenantID, "assistant-owner", time.Time{}, uuid.Nil, 10, assistant.ConversationFilter{})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("list = %+v, %v", page.Items, err)
	}
	before := page.Items[0]
	out, err := repo.Rename(ctx, b.TenantID, before.ConversationID, "assistant-owner", "rename-key-noop", before.Revision, "保持不变")
	if err != nil {
		t.Fatalf("noop rename: %v", err)
	}
	if out.Revision != before.Revision || !out.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("noop moved the conversation: %+v", out)
	}
	var storedRevision int64
	var storedUpdated time.Time
	if err = db.QueryRowContext(ctx, `SELECT revision,updated_at FROM investigation.assistant_conversations WHERE tenant_id=$1 AND conversation_id=$2`, b.TenantID, before.ConversationID).Scan(&storedRevision, &storedUpdated); err != nil {
		t.Fatal(err)
	}
	if storedRevision != before.Revision || !storedUpdated.Equal(before.UpdatedAt) {
		t.Fatalf("noop wrote to the conversation: %d/%s", storedRevision, storedUpdated)
	}
}

func TestAssistantRenameAppliesTheCreationRedaction(t *testing.T) {
	ctx, _, _, b, repo, _ := assistantConversationFixture(t, "原始名称")
	page, err := repo.ListPage(ctx, b.TenantID, "assistant-owner", time.Time{}, uuid.Nil, 10, assistant.ConversationFilter{})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("list = %+v, %v", page.Items, err)
	}
	out, err := repo.Rename(ctx, b.TenantID, page.Items[0].ConversationID, "assistant-owner", "rename-key-secret", page.Items[0].Revision, "token=abcdefgh")
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if strings.Contains(out.Title, "abcdefgh") {
		t.Fatalf("rename persisted a secret in clear text: %q", out.Title)
	}
	if !strings.Contains(out.Title, "REDACTED") {
		t.Fatalf("rename did not apply the creation redaction: %q", out.Title)
	}
}

func TestAssistantRenameRefusesForeignOwnerAndRevokedGrant(t *testing.T) {
	ctx, db, _, b, repo, _ := assistantConversationFixture(t, "原始名称")
	page, err := repo.ListPage(ctx, b.TenantID, "assistant-owner", time.Time{}, uuid.Nil, 10, assistant.ConversationFilter{})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("list = %+v, %v", page.Items, err)
	}
	id := page.Items[0].ConversationID
	before := page.Items[0]
	// Another account of the same tenant does not own the conversation.
	if _, err = repo.Rename(ctx, b.TenantID, id, "other-owner", "rename-key-other", before.Revision, "别人的名称"); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("foreign owner rename error = %v", err)
	}
	// The grant is revoked through the supported state of the platform table.
	// The frozen migration and its constraint are not changed and no state is
	// invented: 'disabled' is the way the platform withdraws a binding.
	if _, err = db.ExecContext(ctx, `UPDATE platform.role_bindings SET status='disabled',updated_at=clock_timestamp() WHERE tenant_id=$1 AND subject='assistant-owner'`, b.TenantID); err != nil {
		t.Fatal(err)
	}
	// The expected revision is the current one: the only reason for the refusal
	// is the withdrawn grant.
	if _, err = repo.Rename(ctx, b.TenantID, id, "assistant-owner", "rename-key-revoked", before.Revision, "撤权后的名称"); !errors.Is(err, investigation.ErrDenied) {
		t.Fatalf("revoked grant rename error = %v", err)
	} else if errors.Is(err, assistant.ErrStale) {
		t.Fatal("the refusal was a version conflict, not the withdrawn grant")
	}
	// Nothing was written by the refused rename: the title, the revision, the
	// idempotency ledger and the audit trail are untouched.
	var title string
	var revision int64
	if err = db.QueryRowContext(ctx, `SELECT title,revision FROM investigation.assistant_conversations WHERE tenant_id=$1 AND conversation_id=$2`, b.TenantID, id).Scan(&title, &revision); err != nil {
		t.Fatal(err)
	}
	if title != before.Title || revision != before.Revision {
		t.Fatalf("the refused rename wrote to the conversation: %q/%d", title, revision)
	}
	var ledger int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM investigation.assistant_binding_requests WHERE tenant_id=$1 AND owner_subject='assistant-owner' AND idempotency_key='rename-key-revoked'`, b.TenantID).Scan(&ledger); err != nil {
		t.Fatal(err)
	}
	if ledger != 0 {
		t.Fatalf("the refused rename wrote an idempotency ledger row: %d", ledger)
	}
	var audited int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM audit.records WHERE tenant_id=$1 AND event_type='assistant.conversation.renamed' AND entity_id=$2`, b.TenantID, id).Scan(&audited); err != nil {
		t.Fatal(err)
	}
	if audited != 0 {
		t.Fatalf("the refused rename wrote an audit record: %d", audited)
	}
}

// The idempotency ledger of the binding requests is shared by the rebind and
// the rename operation. A key of one operation is never accepted by the other
// one, and the ledger of one tenant never reaches another tenant.
func TestAssistantRenameRejectsAForeignLedgerKey(t *testing.T) {
	ctx, db, _, b, repo, jobs := assistantConversationFixture(t, "账本碰撞")
	page, err := repo.ListPage(ctx, b.TenantID, "assistant-owner", time.Time{}, uuid.Nil, 10, assistant.ConversationFilter{})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("list = %+v, %v", page.Items, err)
	}
	id := page.Items[0].ConversationID
	current := page.Items[0]
	// The fixture job has to be idle before the binding may change.
	finishAssistantJob(t, ctx, jobs, b.TenantID, assistantEmptyAnswer())
	current, err = repo.Get(ctx, b.TenantID, id, "assistant-owner")
	if err != nil {
		t.Fatal(err)
	}
	// A rebind registers the key in the shared ledger.
	if _, err = repo.Rebind(ctx, b.TenantID, id, "assistant-owner", "shared-key", current.Revision, analytics.Selection{Namespaces: []string{"apps"}}, nil, nil, graph.ScopeDigest("rebind-fixture")); err != nil {
		t.Fatalf("rebind: %v", err)
	}
	current, err = repo.Get(ctx, b.TenantID, id, "assistant-owner")
	if err != nil {
		t.Fatal(err)
	}
	// The same key with the rename operation is refused: the key of one
	// operation is not the key of another one.
	if _, err = repo.Rename(ctx, b.TenantID, id, "assistant-owner", "shared-key", current.Revision, "撞键的名称"); !errors.Is(err, investigation.ErrConflict) {
		t.Fatalf("cross operation ledger collision = %v", err)
	}
	// The refused rename wrote nothing.
	var title string
	if err = db.QueryRowContext(ctx, `SELECT title FROM investigation.assistant_conversations WHERE tenant_id=$1 AND conversation_id=$2`, b.TenantID, id).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "账本碰撞" {
		t.Fatalf("the refused rename wrote the title: %q", title)
	}
	// Another tenant owns its own ledger: the same key in another tenant is a
	// different request and is not a collision.
	other := uuid.New()
	otherCluster := uuid.New()
	if _, err = db.ExecContext(ctx, `INSERT INTO platform.tenants(tenant_id,slug,display_name) VALUES($1,'sp05-other','SP05 other')`, other); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO platform.cluster_registrations(tenant_id,cluster_id,cluster_uid,display_name) VALUES($1,$2,'sp05-other-cluster','SP05 other')`, other, otherCluster); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `INSERT INTO platform.role_bindings(tenant_id,binding_id,subject,role_name,cluster_scopes,namespace_scopes) VALUES($1,$2,'assistant-owner','operator',$3,$4)`, other, uuid.New(), `["`+otherCluster.String()+`"]`, `[{"clusterId":"`+otherCluster.String()+`","namespace":"apps"}]`); err != nil {
		t.Fatal(err)
	}
	otherConversation, err := repo.Create(ctx, assistant.Conversation{TenantID: other, OwnerSubject: "assistant-owner", Title: "另一个租户", Mode: "data_query", AuthorizationDomain: "operational", ClusterUID: "sp05-other-cluster", Binding: assistant.ConversationBinding{Timezone: "UTC", Selection: analytics.Selection{Namespaces: []string{"apps"}}}})
	if err != nil {
		t.Fatal(err)
	}
	otherOut, err := repo.Rename(ctx, other, otherConversation.ConversationID, "assistant-owner", "shared-key", otherConversation.Revision, "另一个租户的新名称")
	if err != nil {
		t.Fatalf("the ledger of another tenant collided: %v", err)
	}
	if otherOut.Title != "另一个租户的新名称" {
		t.Fatalf("other tenant rename = %+v", otherOut)
	}
}

func TestAssistantSearchFiltersAndCursorStayConsistent(t *testing.T) {
	ctx, _, _, b, repo, _ := assistantConversationFixture(t, "Pod 排查")
	titles := []string{"Pod 排查", "节点磁盘 100%", "节点磁盘 100_x", "路径 C:\\temp", "完全无关"}
	ids := []uuid.UUID{}
	for _, title := range titles[1:] {
		c, err := repo.Create(ctx, assistant.Conversation{TenantID: b.TenantID, OwnerSubject: "assistant-owner", Title: title, Mode: "data_query", AuthorizationDomain: "operational", ClusterUID: b.ClusterUID, Binding: assistant.ConversationBinding{Timezone: "UTC", Selection: analytics.Selection{Namespaces: []string{"apps"}}}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, c.ConversationID)
	}
	// The wildcard of the search term is a literal, not a pattern.
	page, err := repo.ListPage(ctx, b.TenantID, "assistant-owner", time.Time{}, uuid.Nil, 10, assistant.ConversationFilter{Query: "100%"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Title != "节点磁盘 100%" {
		t.Fatalf("literal search = %+v", page.Items)
	}
	// The underscore is a literal too: it never matches another character.
	page, err = repo.ListPage(ctx, b.TenantID, "assistant-owner", time.Time{}, uuid.Nil, 10, assistant.ConversationFilter{Query: "100_x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Title != "节点磁盘 100_x" {
		t.Fatalf("underscore search = %+v", page.Items)
	}
	page, err = repo.ListPage(ctx, b.TenantID, "assistant-owner", time.Time{}, uuid.Nil, 10, assistant.ConversationFilter{Query: "100X"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("the underscore was a wildcard: %+v", page.Items)
	}
	// The backslash is a literal and not the escape character of the query.
	page, err = repo.ListPage(ctx, b.TenantID, "assistant-owner", time.Time{}, uuid.Nil, 10, assistant.ConversationFilter{Query: "C:\\temp"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Title != "路径 C:\\temp" {
		t.Fatalf("backslash search = %+v", page.Items)
	}
	// A title that only contains the term in the other case is still found.
	page, err = repo.ListPage(ctx, b.TenantID, "assistant-owner", time.Time{}, uuid.Nil, 10, assistant.ConversationFilter{Query: "pod"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Title != "Pod 排查" {
		t.Fatalf("case insensitive search = %+v", page.Items)
	}
	// The filter and the pagination belong together.
	first, err := repo.ListPage(ctx, b.TenantID, "assistant-owner", time.Time{}, uuid.Nil, 1, assistant.ConversationFilter{Query: "节点磁盘"})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || !first.HasMore {
		t.Fatalf("first page = %+v", first)
	}
	second, err := repo.ListPage(ctx, b.TenantID, "assistant-owner", first.Before, first.BeforeID, 1, assistant.ConversationFilter{Query: "节点磁盘"})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Items[0].ConversationID == first.Items[0].ConversationID {
		t.Fatalf("second page = %+v", second)
	}
	// The archived conversation is only visible in the archive views.
	if err = repo.Archive(ctx, b.TenantID, first.Items[0].ConversationID, "assistant-owner"); err != nil {
		t.Fatal(err)
	}
	page, err = repo.ListPage(ctx, b.TenantID, "assistant-owner", time.Time{}, uuid.Nil, 10, assistant.ConversationFilter{Query: "节点磁盘"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("archived conversation left the default view: %+v", page.Items)
	}
	page, err = repo.ListPage(ctx, b.TenantID, "assistant-owner", time.Time{}, uuid.Nil, 10, assistant.ConversationFilter{Query: "节点磁盘", Archived: "only"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].ConversationID != first.Items[0].ConversationID {
		t.Fatalf("archive view = %+v", page.Items)
	}
	page, err = repo.ListPage(ctx, b.TenantID, "assistant-owner", time.Time{}, uuid.Nil, 10, assistant.ConversationFilter{Query: "节点磁盘", Archived: "include"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("include view = %+v", page.Items)
	}
	// An invalid filter is refused.
	if _, err = repo.ListPage(ctx, b.TenantID, "assistant-owner", time.Time{}, uuid.Nil, 10, assistant.ConversationFilter{Archived: "everything"}); !errors.Is(err, investigation.ErrInvalid) {
		t.Fatalf("invalid archive filter error = %v", err)
	}
	// The archive did not delete the history of the conversation.
	readable, err := repo.GetReadable(ctx, b.TenantID, first.Items[0].ConversationID, "assistant-owner")
	if err != nil {
		t.Fatalf("archived conversation is not readable any more: %v", err)
	}
	if readable.ArchivedAt == nil {
		t.Fatal("archived conversation lost its archive marker")
	}
}

// The list endpoint binds the filter to the cursor. Two requests that carry the
// same normalized filter continue the same page; a request that really changes
// the filter is refused.
func TestAssistantListCursorUsesTheNormalizedFilter(t *testing.T) {
	ctx, _, _, b, repo, _ := assistantConversationFixture(t, "Pod 排查")
	for _, title := range []string{"Pod 节点", "节点磁盘 100%"} {
		if _, err := repo.Create(ctx, assistant.Conversation{TenantID: b.TenantID, OwnerSubject: "assistant-owner", Title: title, Mode: "data_query", AuthorizationDomain: "operational", ClusterUID: b.ClusterUID, Binding: assistant.ConversationBinding{Timezone: "UTC", Selection: analytics.Selection{Namespaces: []string{"apps"}}}}); err != nil {
			t.Fatal(err)
		}
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	handler := &httpapi.AssistantHandlers{Repository: repo, CursorKey: key}
	type pageEnvelope struct {
		Data []struct {
			Title string `json:"title"`
		} `json:"data"`
		Meta struct {
			NextCursor string `json:"nextCursor"`
		} `json:"meta"`
	}
	call := func(query string) (int, pageEnvelope, string) {
		r := httptest.NewRequest("GET", "/api/v1/assistant/conversations"+query, nil).WithContext(auth.WithRequestContext(context.Background(), auth.RequestContext{TenantID: b.TenantID, Subject: "assistant-owner", Roles: []auth.Role{auth.Operator}}))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		var envelope pageEnvelope
		json.Unmarshal(w.Body.Bytes(), &envelope)
		return w.Code, envelope, w.Body.String()
	}
	// The first page carries the whitespace around the term and omits the
	// archive state.
	status, first, body := call("?q=%20Pod%20&limit=1")
	if status != 200 {
		t.Fatalf("list = %d %s", status, body)
	}
	if len(first.Data) != 1 || first.Meta.NextCursor == "" {
		t.Fatalf("the first page returned no cursor: %s", body)
	}
	// The same filter, written differently, is the same filter: the cursor is
	// the same cursor.
	status, normalized, body := call("?q=Pod&archived=exclude&limit=1")
	if status != 200 {
		t.Fatalf("normalized list = %d %s", status, body)
	}
	if normalized.Meta.NextCursor != first.Meta.NextCursor {
		t.Fatalf("the normalized filter produced another cursor: %q / %q", first.Meta.NextCursor, normalized.Meta.NextCursor)
	}
	// The page continues with the same filter.
	status, second, body := call("?q=Pod&archived=exclude&limit=1&cursor=" + first.Meta.NextCursor)
	if status != 200 {
		t.Fatalf("continued page = %d %s", status, body)
	}
	if len(second.Data) != 1 || second.Data[0].Title == first.Data[0].Title {
		t.Fatalf("continued page body = %s", body)
	}
	// A really different filter is refused.
	status, _, body = call("?q=%E8%8A%82%E7%82%B9&archived=exclude&limit=1&cursor=" + first.Meta.NextCursor)
	if status != 403 {
		t.Fatalf("foreign filter = %d %s", status, body)
	}
}

func TestAssistantArchiveKeepsTheFixedReferences(t *testing.T) {
	ctx, db, _, b, repo, jobs := assistantConversationFixture(t, "归档保留引用")
	page, err := repo.ListPage(ctx, b.TenantID, "assistant-owner", time.Time{}, uuid.Nil, 10, assistant.ConversationFilter{})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("list = %+v, %v", page.Items, err)
	}
	id := page.Items[0].ConversationID
	turnID := assistantTurnID(t, ctx, db, b.TenantID, id)
	// The product busy guard is kept: a conversation with an unfinished turn
	// cannot be archived. The guard is not bypassed, the fixture job ends.
	if err = repo.Archive(ctx, b.TenantID, id, "assistant-owner"); !errors.Is(err, assistant.ErrBusy) {
		t.Fatalf("archive of a busy conversation = %v", err)
	}
	// The fixture commits one real reference to a platform resource and one
	// summary, and the job ends through the ordinary Job repository lifecycle.
	target := resource.CanonicalID{Domain: "k8s", Tenant: b.TenantID.String(), Scope: b.ClusterUID, APIGroup: "core", Kind: "Pod", StableID: "archived-fixed-reference"}.String()
	if _, err = db.ExecContext(ctx, `INSERT INTO platform.resource_entities(tenant_id,canonical_id,cluster_id,kind,namespace,name,metadata,observed_at) VALUES($1,$2,$3,'Pod','apps','archived-fixed-reference','{}',clock_timestamp())`, b.TenantID, target, b.ClusterID); err != nil {
		t.Fatal(err)
	}
	lease, err := jobs.Claim(ctx, b.TenantID, "fixture", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	stepResult, _ := json.Marshal(map[string]any{"tool": "resolve_resources", "data": map[string]any{"candidates": []map[string]string{{"canonicalId": target}}}})
	if _, err = db.ExecContext(ctx, `INSERT INTO investigation.steps(tenant_id,job_id,step_id,tool_name,args_digest,state,lease_token,lease_generation,result,started_at) VALUES($1,$2,$3,'resolve_resources',$4,'succeeded',$5,1,$6,clock_timestamp())`, b.TenantID, assistantJobID(t, ctx, db, b.TenantID, id), uuid.New(), graph.ScopeDigest("fixture"), lease.Token, stepResult); err != nil {
		t.Fatal(err)
	}
	summary := "归档前提交的原始摘要"
	answer, _ := json.Marshal(investigation.AssistantAnswer{SchemaVersion: "assistant-answer/v1", AnswerKind: "unsupported", Summary: summary, FactRefs: []investigation.FactRef{}, QueryResultRefs: []uuid.UUID{}, EvidenceRefs: []string{}, EntityRefs: []string{target}, MissingEvidence: []string{"fixture_no_observations"}, ScopeSummary: "operational", AsOf: time.Now().UTC(), DegradedSources: []string{}, Warnings: []string{}, Hypotheses: []investigation.Hypothesis{}, NextChecks: []string{}})
	if err = jobs.Complete(ctx, lease, answer); err != nil {
		t.Fatal(err)
	}
	// The committed turn is read back before the archive.
	before, err := repo.TurnReadable(ctx, b.TenantID, turnID, "assistant-owner")
	if err != nil {
		t.Fatalf("turn read back: %v", err)
	}
	var beforeAnswer investigation.AssistantAnswer
	if json.Unmarshal(before.Answer, &beforeAnswer) != nil {
		t.Fatal("answer of the fixture")
	}
	if beforeAnswer.Summary != summary || len(beforeAnswer.EntityRefs) != 1 || beforeAnswer.EntityRefs[0] != target {
		t.Fatalf("the fixture did not commit the reference: %+v", beforeAnswer)
	}
	var beforeDigest string
	if err = db.QueryRowContext(ctx, `SELECT input_digest FROM investigation.assistant_turns WHERE tenant_id=$1 AND turn_id=$2`, b.TenantID, turnID).Scan(&beforeDigest); err != nil {
		t.Fatal(err)
	}
	// The archive now succeeds: the job really ended.
	if err = repo.Archive(ctx, b.TenantID, id, "assistant-owner"); err != nil {
		t.Fatalf("archive: %v", err)
	}
	// The fixed reference survived the archive unchanged: the committed answer,
	// its summary and its reference to the platform resource are the same.
	after, err := repo.TurnReadable(ctx, b.TenantID, turnID, "assistant-owner")
	if err != nil {
		t.Fatalf("the archived turn is not readable any more: %v", err)
	}
	if !bytes.Equal(before.Answer, after.Answer) {
		t.Fatalf("the archive changed the committed answer:\n%s\n%s", before.Answer, after.Answer)
	}
	var afterAnswer investigation.AssistantAnswer
	if json.Unmarshal(after.Answer, &afterAnswer) != nil {
		t.Fatal("answer of the archived turn")
	}
	if afterAnswer.Summary != summary || len(afterAnswer.EntityRefs) != 1 || afterAnswer.EntityRefs[0] != target {
		t.Fatalf("the archive changed the summary or the reference: %+v", afterAnswer)
	}
	var afterDigest string
	if err = db.QueryRowContext(ctx, `SELECT input_digest FROM investigation.assistant_turns WHERE tenant_id=$1 AND turn_id=$2`, b.TenantID, turnID).Scan(&afterDigest); err != nil {
		t.Fatal(err)
	}
	if afterDigest != beforeDigest {
		t.Fatal("the archive rewrote the frozen input of the turn")
	}
	// The reference still resolves on the platform and the messages of the
	// conversation are still readable.
	var namespace string
	if err = db.QueryRowContext(ctx, `SELECT namespace FROM platform.resource_entities WHERE tenant_id=$1 AND canonical_id=$2 AND deleted_at IS NULL`, b.TenantID, target).Scan(&namespace); err != nil {
		t.Fatalf("the fixed reference does not resolve any more: %v", err)
	}
	if namespace != "apps" {
		t.Fatalf("the fixed reference points to %q", namespace)
	}
	messages, err := repo.Messages(ctx, b.TenantID, id, "assistant-owner")
	if err != nil {
		t.Fatalf("the messages of the archived conversation are not readable: %v", err)
	}
	if len(messages) < 2 {
		t.Fatalf("the archive cascaded into the messages: %+v", messages)
	}
	if _, err = repo.GetReadable(ctx, b.TenantID, id, "assistant-owner"); err != nil {
		t.Fatalf("archived conversation is not readable any more: %v", err)
	}
}

// assistantJobID reads the job of the turn that the fixture submitted.
func assistantJobID(t *testing.T, ctx context.Context, db *sql.DB, tenant uuid.UUID, conversation uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := db.QueryRowContext(ctx, `SELECT job_id FROM investigation.assistant_turns WHERE tenant_id=$1 AND conversation_id=$2 ORDER BY created_at LIMIT 1`, tenant, conversation).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
