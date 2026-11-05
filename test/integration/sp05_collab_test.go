package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"ops-platform/internal/finding"
	"ops-platform/internal/graph"
	"ops-platform/internal/incident"
	"ops-platform/internal/persistence"
	"ops-platform/internal/source"
)

// Collaboration mutations run on the API runtime role in production; the test
// drives them through the same grants instead of the worker pool.
func sp05CollabDatabase(t *testing.T) (context.Context, *sql.DB, *pgxpool.Pool, *pgxpool.Pool, source.BoundSourceContext) {
	ctx, db, dir, dsn := newMigrationDatabaseWithTimeout(t, 5*time.Minute)
	if err := goose.UpToContext(ctx, db, dir, 1); err != nil {
		t.Fatal(err)
	}
	var database string
	if err := db.QueryRowContext(ctx, `SELECT current_database()`).Scan(&database); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `GRANT CREATE ON DATABASE "`+database+`" TO schema_owner`); err != nil {
		t.Fatalf("grant schema duty on the disposable database: %v", err)
	}
	if err := runRemainingMigrationsAsMigrationRole(t, ctx, db, dsn, dir); err != nil {
		t.Fatal(err)
	}
	b := source.BoundSourceContext{TenantID: uuid.New(), SourceID: uuid.New(), ClusterID: uuid.New(), ClusterUID: "sp05-cluster", RegistrationRevision: 1, CredentialRevision: 1}
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.tenants(tenant_id,slug,display_name) VALUES($1,'sp05','SP05')`, b.TenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.cluster_registrations(tenant_id,cluster_id,cluster_uid,display_name) VALUES($1,$2,$3,'SP05')`, b.TenantID, b.ClusterID, b.ClusterUID); err != nil {
		t.Fatal(err)
	}
	mapping, _ := json.Marshal(map[string]any{"scopes": map[string][]string{"cluster": {b.ClusterUID}, "namespace": {"apps"}}})
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.source_registrations(tenant_id,source_id,cluster_id,source_type,instance_key,auth_ref,allowed_schemas,backend_logical_id,data_scope_mapping) VALUES($1,$2,$3,'kubernetes','sp05','openbao://sp05/readonly',ARRAY['finding-envelope/v2'],'sp05-native',$4)`, b.TenantID, b.SourceID, b.ClusterID, mapping); err != nil {
		t.Fatal(err)
	}
	worker, err := pgxpool.NewWithConfig(ctx, runtimePoolConfig(t, ctx, db, dsn, "worker_runtime_role"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(worker.Close)
	api, err := pgxpool.NewWithConfig(ctx, runtimePoolConfig(t, ctx, db, dsn, "api_runtime_role"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(api.Close)
	return ctx, db, worker, api, b
}

// Collaboration writes keep the incident revision untouched for metadata-only
// operations, take the full CAS path for manual alert links, and never let a
// transfer change recovery facts.
func TestSP05IncidentCollaborationClaimTransferCommentFollowUps(t *testing.T) {
	ctx, db, worker, api, b := sp05CollabDatabase(t)
	if testing.Short() {
		t.Skip("integration")
	}
	service := finding.Service{Pool: worker}
	if _, _, err := service.Ingest(ctx, b, sp05Envelope(b, "collab-e1", "collab-o1")); err != nil {
		t.Fatal(err)
	}
	if err := service.RelayPass(ctx, b.TenantID, incident.Consume, 10); err != nil {
		t.Fatal(err)
	}
	var incidentID string
	if err := db.QueryRowContext(ctx, `SELECT incident_id::text FROM incident.records`).Scan(&incidentID); err != nil {
		t.Fatal(err)
	}
	for _, subject := range []string{"sp05-operator", "sp05-operator-2"} {
		if _, err := db.ExecContext(ctx, `INSERT INTO platform.role_bindings(tenant_id,binding_id,subject,role_name,cluster_scopes,namespace_scopes) VALUES($1,$2,$3,'operator',jsonb_build_array($4::text),'[]')`, b.TenantID, uuid.New(), subject, b.ClusterID); err != nil {
			t.Fatal(err)
		}
	}
	scopeOf := func(subject string) graph.Scope {
		scope, err := (graph.Authorization{Pool: api}).Effective(ctx, b.TenantID.String(), subject, b.ClusterUID)
		if err != nil {
			t.Fatalf("scope for %s: %v", subject, err)
		}
		return scope
	}
	load := func() incident.Incident {
		var i incident.Incident
		if err := persistence.WithTenantTx(ctx, api, b.TenantID, func(tx pgx.Tx) error {
			var err error
			i, err = incident.Load(ctx, tx, b.TenantID, incidentID, false)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return i
	}
	timelineCount := func(kind string) int {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM incident.timeline WHERE incident_id=$1 AND kind=$2`, incidentID, kind).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	auditCount := func(eventType string) int {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM audit.records WHERE entity_id=$1 AND event_type=$2`, incidentID, eventType).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	incidents := incident.Service{Pool: api}
	base := load()

	// Claim: assignment lands, revision and state stay untouched.
	claimed, err := incidents.Claim(ctx, scopeOf("sp05-operator"), "sp05-operator", incidentID, base.Revision, "first responder")
	if err != nil {
		t.Fatal(err)
	}
	if claimed.AssigneeSubject != "sp05-operator" || claimed.AssignedAt == nil {
		t.Fatalf("claim not stored: %+v", claimed)
	}
	if claimed.Revision != base.Revision || claimed.State != base.State {
		t.Fatalf("claim changed revision/state: %d/%s -> %d/%s", base.Revision, base.State, claimed.Revision, claimed.State)
	}
	if timelineCount("assignment") != 1 || auditCount("incident.assignment") != 1 {
		t.Fatal("claim missing timeline/audit records")
	}
	// Claiming again as the assignee is an idempotent no-op.
	if _, err := incidents.Claim(ctx, scopeOf("sp05-operator"), "sp05-operator", incidentID, claimed.Revision, ""); err != nil {
		t.Fatal(err)
	}
	if timelineCount("assignment") != 1 {
		t.Fatal("idempotent self-claim duplicated timeline records")
	}
	// A different operator cannot steal the assignment.
	if _, err := incidents.Claim(ctx, scopeOf("sp05-operator-2"), "sp05-operator-2", incidentID, claimed.Revision, ""); !errors.Is(err, incident.ErrAssigned) {
		t.Fatalf("expected assignment conflict, got %v", err)
	}
	// Stale expected revision is refused even for the assignee.
	if _, err := incidents.Claim(ctx, scopeOf("sp05-operator"), "sp05-operator", incidentID, base.Revision-1, ""); !errors.Is(err, incident.ErrRevision) {
		t.Fatalf("expected stale revision, got %v", err)
	}

	// Transfer: recipient must be eligible; state and recovery facts never move.
	before := load()
	transferred, err := incidents.Transfer(ctx, scopeOf("sp05-operator"), "sp05-operator", incidentID, "sp05-operator-2", "shift handover", before.Revision, false)
	if err != nil {
		t.Fatal(err)
	}
	if transferred.AssigneeSubject != "sp05-operator-2" || transferred.Revision != before.Revision || transferred.State != before.State ||
		transferred.ResolvedAt != before.ResolvedAt || transferred.RecoveryKnownAt != before.RecoveryKnownAt || transferred.SuppressedUntil != before.SuppressedUntil {
		t.Fatalf("transfer changed facts: %+v -> %+v", before, transferred)
	}
	if timelineCount("assignment") != 2 || auditCount("incident.assignment") != 2 {
		t.Fatal("transfer missing timeline/audit records")
	}
	// The previous assignee can no longer transfer it.
	if _, err := incidents.Transfer(ctx, scopeOf("sp05-operator"), "sp05-operator", incidentID, "sp05-operator-2", "take back", load().Revision, false); !errors.Is(err, graph.ErrScope) {
		t.Fatalf("expected non-assignee refusal, got %v", err)
	}
	// A subject without an operator binding is not an eligible recipient.
	if _, err := incidents.Transfer(ctx, scopeOf("sp05-operator-2"), "sp05-operator-2", incidentID, "sp05-stranger", "outsider", load().Revision, false); !errors.Is(err, incident.ErrTransition) {
		t.Fatalf("expected ineligible recipient refusal, got %v", err)
	}

	// Comments are append-only on the timeline; the reference form also carries
	// the business impact, stored on the comment payload.
	entry, err := incidents.Comment(ctx, scopeOf("sp05-operator-2"), "sp05-operator-2", incidentID, "已联系存储负责人", "网关可用性下降约 5%，订单创建偶发超时")
	if err != nil {
		t.Fatal(err)
	}
	if entry["kind"] != "comment" || timelineCount("comment") != 1 || auditCount("incident.comment") != 1 {
		t.Fatalf("comment not recorded: %+v", entry)
	}
	var impact string
	if err := db.QueryRowContext(ctx, `SELECT payload->>'impact' FROM incident.timeline WHERE incident_id=$1 AND kind='comment'`, incidentID).Scan(&impact); err != nil || impact == "" {
		t.Fatalf("comment impact not persisted: %q %v", impact, err)
	}
	if load().Revision != transferred.Revision {
		t.Fatal("comment must not change the incident revision")
	}

	// Follow-ups persist, survive unrelated writes and complete with a note;
	// the reference form assigns a responsible owner who may also complete it.
	followUp, err := incidents.CreateFollowUp(ctx, scopeOf("sp05-operator-2"), "sp05-operator-2", incidentID, "补充存储侧复盘", "确认 CSI 供应商日志保留 30 天", "")
	if err != nil {
		t.Fatal(err)
	}
	if followUp.State != "open" || followUp.OwnerSubject != "sp05-operator-2" {
		t.Fatalf("follow-up created in wrong state: %+v", followUp)
	}
	owned, err := incidents.CreateFollowUp(ctx, scopeOf("sp05-operator-2"), "sp05-operator-2", incidentID, "网络侧确认重试策略", "", "sp05-operator")
	if err != nil {
		t.Fatal(err)
	}
	if owned.OwnerSubject != "sp05-operator" {
		t.Fatalf("follow-up owner not stored: %+v", owned)
	}
	if _, err := incidents.CreateFollowUp(ctx, scopeOf("sp05-operator-2"), "sp05-operator-2", incidentID, "指派给无权人员", "", "sp05-stranger"); !errors.Is(err, incident.ErrTransition) {
		t.Fatalf("expected ineligible owner refusal, got %v", err)
	}
	if _, err := incidents.CompleteFollowUp(ctx, scopeOf("sp05-operator"), "sp05-operator", incidentID, followUp.FollowUpID, "not the creator or assignee", false); !errors.Is(err, graph.ErrScope) {
		t.Fatalf("expected completion refusal, got %v", err)
	}
	done, err := incidents.CompleteFollowUp(ctx, scopeOf("sp05-operator-2"), "sp05-operator-2", incidentID, followUp.FollowUpID, "存储侧确认完成", false)
	if err != nil {
		t.Fatal(err)
	}
	if done.State != "done" || done.CompleterSubject != "sp05-operator-2" || done.ResolutionNote != "存储侧确认完成" {
		t.Fatalf("follow-up completion wrong: %+v", done)
	}
	if timelineCount("followup") != 3 {
		t.Fatal("follow-up timeline records missing")
	}
	var followUpState string
	if err := db.QueryRowContext(ctx, `SELECT state FROM incident.followups WHERE followup_id=$1`, followUp.FollowUpID).Scan(&followUpState); err != nil || followUpState != "done" {
		t.Fatalf("follow-up not persisted: %s %v", followUpState, err)
	}
	// The responsible owner completes their own todo; reopening returns it to
	// open with completion facts cleared.
	if _, err := incidents.CompleteFollowUp(ctx, scopeOf("sp05-operator"), "sp05-operator", incidentID, owned.FollowUpID, "网络重试已确认", false); err != nil {
		t.Fatalf("owner completion failed: %v", err)
	}
	reopened, err := incidents.ReopenFollowUp(ctx, scopeOf("sp05-operator"), "sp05-operator", incidentID, owned.FollowUpID, false)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.State != "open" || reopened.OwnerSubject != "sp05-operator" {
		t.Fatalf("reopen wrong: %+v", reopened)
	}
	var completionGone bool
	if err := db.QueryRowContext(ctx, `SELECT completed_at IS NULL AND completer_subject IS NULL AND resolution_note='' FROM incident.followups WHERE followup_id=$1`, owned.FollowUpID).Scan(&completionGone); err != nil || !completionGone {
		t.Fatalf("completion facts not cleared on reopen: %v %v", completionGone, err)
	}
}

// Manual alert links change the evidence set: the revision CAS applies, the
// recovery clock is recomputed and cross-incident links stay exclusive.
func TestSP05IncidentCollaborationAlertLinks(t *testing.T) {
	ctx, db, worker, api, b := sp05CollabDatabase(t)
	if testing.Short() {
		t.Skip("integration")
	}
	service := finding.Service{Pool: worker}
	for _, event := range []string{"alert-e1", "alert-e2"} {
		if _, _, err := service.Ingest(ctx, b, sp05Envelope(b, event, event+"-o")); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.RelayPass(ctx, b.TenantID, incident.Consume, 10); err != nil {
		t.Fatal(err)
	}
	// One more finding that correlation has not linked yet.
	unlinked, _, err := service.Ingest(ctx, b, sp05Envelope(b, "alert-free", "alert-free-o"))
	if err != nil {
		t.Fatal(err)
	}
	var incidentID string
	if err := db.QueryRowContext(ctx, `SELECT incident_id::text FROM incident.records ORDER BY incident_id LIMIT 1`).Scan(&incidentID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.role_bindings(tenant_id,binding_id,subject,role_name,cluster_scopes,namespace_scopes) VALUES($1,$2,'sp05-operator','operator',jsonb_build_array($3::text),'[]')`, b.TenantID, uuid.New(), b.ClusterID); err != nil {
		t.Fatal(err)
	}
	scope, err := (graph.Authorization{Pool: api}).Effective(ctx, b.TenantID.String(), "sp05-operator", b.ClusterUID)
	if err != nil {
		t.Fatal(err)
	}
	load := func() incident.Incident {
		var i incident.Incident
		if err := persistence.WithTenantTx(ctx, api, b.TenantID, func(tx pgx.Tx) error {
			var err error
			i, err = incident.Load(ctx, tx, b.TenantID, incidentID, false)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return i
	}
	incidents := incident.Service{Pool: api}
	base := load()

	linked, err := incidents.AlertLinks(ctx, scope, "sp05-operator", incidentID, "link", []string{unlinked.FindingID}, "same node fault window", base.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if linked.Revision != base.Revision+1 {
		t.Fatalf("manual link must advance revision: %d -> %d", base.Revision, linked.Revision)
	}
	var linkCount int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM incident.finding_links WHERE incident_id=$1 AND finding_id=$2`, incidentID, unlinked.FindingID).Scan(&linkCount); err != nil || linkCount != 1 {
		t.Fatalf("link not stored: %d %v", linkCount, err)
	}
	// A finding that does not exist is refused instead of silently linked.
	if _, err := incidents.AlertLinks(ctx, scope, "sp05-operator", incidentID, "link", []string{uuid.New().String()}, "missing finding", load().Revision); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("expected missing finding refusal, got %v", err)
	}
	// Stale revision is refused.
	if _, err := incidents.AlertLinks(ctx, scope, "sp05-operator", incidentID, "unlink", []string{unlinked.FindingID}, "stale", base.Revision); !errors.Is(err, incident.ErrRevision) {
		t.Fatalf("expected stale revision, got %v", err)
	}
	// Unlink works with the current revision and is recorded.
	current := load()
	if _, err := incidents.AlertLinks(ctx, scope, "sp05-operator", incidentID, "unlink", []string{unlinked.FindingID}, "wrong association", current.Revision); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM incident.finding_links WHERE incident_id=$1 AND finding_id=$2`, incidentID, unlinked.FindingID).Scan(&linkCount); err != nil || linkCount != 0 {
		t.Fatalf("unlink not applied: %d %v", linkCount, err)
	}
	var timelineLinks int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM incident.timeline WHERE incident_id=$1 AND kind='alert_link'`, incidentID).Scan(&timelineLinks); err != nil || timelineLinks != 2 {
		t.Fatalf("alert_link timeline records wrong: %d %v", timelineLinks, err)
	}
	var auditLinks int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM audit.records WHERE entity_id=$1 AND event_type='incident.alert_link'`, incidentID).Scan(&auditLinks); err != nil || auditLinks != 2 {
		t.Fatalf("alert_link audit records wrong: %d %v", auditLinks, err)
	}
}
