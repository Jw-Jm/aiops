package integration

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"ops-platform/internal/graph"
	"ops-platform/internal/incident"
)

func TestNativeIncidentCaseDraftPersistsAndReauthorizes(t *testing.T) {
	f := newNativeJobFixture(t)
	account := f.newAccount(t, `"apps"`)
	seed := f.newIncident(t)
	scope, err := (graph.Authorization{Pool: f.apiPool}).Effective(account.ctx, f.tenantID.String(), account.subject, f.clusterUID)
	if err != nil {
		t.Fatal(err)
	}
	var revision int64
	if err = f.db.QueryRowContext(f.ctx, `SELECT revision FROM incident.records WHERE tenant_id=$1 AND incident_id=$2`, f.tenantID, seed.IncidentID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	in := incident.CaseDraftInput{Kind: "case", IncidentID: seed.IncidentID.String(), ExpectedRevision: revision, Title: "真实存储案例", Summary: "固定故障来源", Content: "已知事实与未知恢复情况"}
	service := incident.Service{Pool: f.apiPool}
	bad := in
	bad.ExpectedRevision++
	if _, err = service.CreateCaseDraft(account.ctx, scope, account.subject, bad); !errors.Is(err, incident.ErrRevision) {
		t.Fatalf("stale draft: %v", err)
	}
	saved, err := service.CreateCaseDraft(account.ctx, scope, account.subject, in)
	if err != nil {
		t.Fatal(err)
	}
	read, err := service.ReadCaseDrafts(account.ctx, scope, account.subject, in.IncidentID, saved.DocumentID, uuid.Nil)
	if err != nil || len(read) != 1 || read[0] != saved {
		t.Fatalf("draft read mismatch: %+v %v", read, err)
	}
	var after int64
	if err = f.db.QueryRowContext(f.ctx, `SELECT revision FROM incident.records WHERE tenant_id=$1 AND incident_id=$2`, f.tenantID, seed.IncidentID).Scan(&after); err != nil || after != revision {
		t.Fatalf("incident facts changed: %d %v", after, err)
	}
	if _, err = f.apiPool.Exec(f.ctx, `UPDATE incident.case_drafts SET title='overwrite' WHERE tenant_id=$1 AND document_id=$2`, f.tenantID, saved.DocumentID); err == nil {
		t.Fatal("runtime role overwrote immutable version")
	}
	foreign := scope
	foreign.Namespaces = []string{"other-namespace"}
	foreign.ClusterScoped = false
	if rows, err := service.ReadCaseDrafts(account.ctx, foreign, account.subject, in.IncidentID, saved.DocumentID, uuid.Nil); err == nil || len(rows) != 0 {
		t.Fatalf("foreign scope read: %+v %v", rows, err)
	}
	if _, err = f.db.ExecContext(f.ctx, `UPDATE identity.role_grants SET status='revoked',revoked_at=clock_timestamp(),revoked_reason='case draft revoke',authorization_version=authorization_version+1 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, account.userID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.ExecContext(f.ctx, `UPDATE identity.accounts SET authorization_version=authorization_version+1 WHERE tenant_id=$1 AND user_id=$2`, f.tenantID, account.userID); err != nil {
		t.Fatal(err)
	}
	if rows, err := service.ReadCaseDrafts(account.ctx, scope, account.subject, in.IncidentID, saved.DocumentID, uuid.Nil); err == nil || len(rows) != 0 {
		t.Fatalf("revoked read: %+v %v", rows, err)
	}
	if _, err := service.CreateCaseDraft(account.ctx, scope, account.subject, in); err == nil {
		t.Fatal("revoked draft write")
	}
}
