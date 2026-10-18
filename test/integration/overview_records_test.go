package integration

import (
	"encoding/json"
	"github.com/google/uuid"
	"net/http/httptest"
	"ops-platform/internal/auth"
	"ops-platform/internal/finding"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/incident"
	"testing"
)

func TestOverviewCountsAndPriorityRequireEveryLinkedNamespace(t *testing.T) {
	ctx, db, pool, b := sp05Database(t)
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.role_bindings(tenant_id,binding_id,subject,role_name,cluster_scopes,namespace_scopes) VALUES($1,$2,'overview-operator','operator',jsonb_build_array($3::text),'[]')`, b.TenantID, uuid.New(), b.ClusterID); err != nil {
		t.Fatal(err)
	}
	fs := finding.Service{Pool: pool}
	e := sp05Envelope(b, "overview", "one")
	e.Severity = "critical"
	f, _, err := fs.Ingest(ctx, b, e)
	if err != nil {
		t.Fatal(err)
	}
	if err = fs.RelayPass(ctx, b.TenantID, incident.Consume, 10); err != nil {
		t.Fatal(err)
	}
	var incidentID string
	if err = db.QueryRowContext(ctx, `SELECT incident_id FROM incident.finding_links WHERE tenant_id=$1 AND finding_id=$2`, b.TenantID, f.FindingID).Scan(&incidentID); err != nil {
		t.Fatal(err)
	}
	for i, job := range []struct {
		subject    string
		namespaces []string
	}{{"overview-operator", []string{}}, {"other-operator", []string{}}, {"overview-operator", []string{"restricted"}}} {
		raw, _ := json.Marshal(map[string]any{"tenantId": b.TenantID.String(), "clusterUid": b.ClusterUID, "clusterScoped": true, "namespaces": job.namespaces, "resources": []string{}})
		if _, err = db.ExecContext(ctx, `INSERT INTO investigation.jobs(tenant_id,job_id,incident_id,trigger_revision,state,schema_version,requesting_subject,effective_scope) VALUES($1,$2,$3,$4,'running','investigation-job/v2',$5,$6)`, b.TenantID, uuid.New(), incidentID, i+1, job.subject, raw); err != nil {
			t.Fatal(err)
		}
	}
	h := httpapi.SP04Handlers{Pool: pool}
	read := func() (map[string]float64, []json.RawMessage) {
		r := httptest.NewRequest("GET", "/api/v1/overview?clusterUid="+b.ClusterUID, nil).WithContext(auth.WithRequestContext(ctx, auth.RequestContext{TenantID: b.TenantID, Subject: "overview-operator", Roles: []auth.Role{auth.Operator}, RequestID: uuid.NewString()}))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("overview %d %s", w.Code, w.Body.String())
		}
		var body struct {
			Data struct {
				Counts            map[string]float64
				PriorityIncidents []json.RawMessage
			}
		}
		if json.Unmarshal(w.Body.Bytes(), &body) != nil {
			t.Fatal(w.Body.String())
		}
		return body.Data.Counts, body.Data.PriorityIncidents
	}
	counts, items := read()
	if counts["finding"] != 1 || counts["activeFinding"] != 1 || counts["openIncident"] != 1 || counts["criticalIncident"] != 1 || counts["runningInvestigation"] != 1 || len(items) != 1 {
		t.Fatal(counts, len(items))
	}
	if _, err = db.ExecContext(ctx, `UPDATE incident.records SET state='resolved' WHERE tenant_id=$1`, b.TenantID); err != nil {
		t.Fatal(err)
	}
	counts, items = read()
	if counts["incident"] != 1 || counts["openIncident"] != 0 || len(items) != 0 {
		t.Fatal("resolved record counted as open", counts, len(items))
	}
	if _, err = db.ExecContext(ctx, `UPDATE incident.records SET state='open' WHERE tenant_id=$1`, b.TenantID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, `UPDATE finding.records SET namespace='restricted' WHERE finding_id=$1`, f.FindingID); err != nil {
		t.Fatal(err)
	}
	counts, items = read()
	if counts["finding"] != 0 || counts["incident"] != 0 || counts["runningInvestigation"] != 0 || len(items) != 0 {
		t.Fatal("linked namespace leaked", counts, len(items))
	}
}
