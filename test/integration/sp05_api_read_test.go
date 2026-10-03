package integration

import (
	"encoding/json"
	"github.com/google/uuid"
	"net/http/httptest"
	"ops-platform/internal/auth"
	"ops-platform/internal/contract"
	"ops-platform/internal/finding"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/incident"
	"strings"
	"testing"
)

func TestSP05APIListFiltersAndAllLinkedNamespaceAuthorization(t *testing.T) {
	ctx, db, pool, b := sp05Database(t)
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.role_bindings(tenant_id,binding_id,subject,role_name,cluster_scopes,namespace_scopes) VALUES($1,$2,'read-operator','operator',jsonb_build_array($3::text),'[]')`, b.TenantID, uuid.New(), b.ClusterID); err != nil {
		t.Fatal(err)
	}
	fs := finding.Service{Pool: pool}
	e := sp05Envelope(b, "warning", "o1")
	f, _, err := fs.Ingest(ctx, b, e)
	if err != nil {
		t.Fatal(err)
	}
	e.EventID, e.IdempotencyKey, e.OccurrenceID, e.Severity = "critical", "critical", "o2", "critical"
	if _, _, err = fs.Ingest(ctx, b, e); err != nil {
		t.Fatal(err)
	}
	if err = fs.RelayPass(ctx, b.TenantID, incident.Consume, 10); err != nil {
		t.Fatal(err)
	}
	h := httpapi.SP05Handlers{Pool: pool, Enabled: true}
	request := func(path string) ([]json.RawMessage, int) {
		r := httptest.NewRequest("GET", path, nil).WithContext(auth.WithRequestContext(ctx, auth.RequestContext{TenantID: b.TenantID, Subject: "read-operator", RequestID: uuid.NewString(), Roles: []auth.Role{auth.Operator}}))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var out struct{ Data []json.RawMessage }
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if w.Code >= 400 {
			if err := contract.Validate("https://ops.local/schemas/error-envelope/v2", w.Body.Bytes()); err != nil {
				t.Errorf("SP05 error Contract: %v", err)
			}
		}
		if w.Code == 200 {
			schema := "https://ops.local/schemas/finding-page/v2"
			if strings.HasPrefix(path, "/api/v1/incidents") {
				schema = "https://ops.local/schemas/incident-page/v2"
			}
			if err := contract.Validate(schema, w.Body.Bytes()); err != nil {
				t.Errorf("SP05 actual page Contract: %v", err)
			}

			var page map[string]json.RawMessage
			_ = json.Unmarshal(w.Body.Bytes(), &page)
			var meta map[string]json.RawMessage
			_ = json.Unmarshal(page["meta"], &meta)
			if v, exists := meta["nextCursor"]; exists && string(v) == "null" {
				t.Error("last/empty page violates string-only cursor Contract")
			}
		}
		return out.Data, w.Code
	}
	items, status := request("/api/v1/findings?clusterUid=" + b.ClusterUID + "&severity=critical")
	if status != 200 || len(items) != 1 {
		t.Fatalf("severity filter count %d status %d", len(items), status)
	}
	var got finding.Finding
	if json.Unmarshal(items[0], &got) != nil || got.Severity != "critical" {
		t.Fatal("wrong severity")
	}
	items, status = request("/api/v1/findings?clusterUid=" + b.ClusterUID + "&sourceSystem=redfish")
	if status != 200 || len(items) != 0 {
		t.Fatal("source system filter ignored")
	}
	items, status = request("/api/v1/findings?clusterUid=" + b.ClusterUID + "&observedFrom=2026-10-03T00:00:00Z")
	if status != 200 || len(items) != 0 {
		t.Fatal("observation clock filter ignored")
	}
	_, status = request("/api/v1/findings?clusterUid=" + b.ClusterUID + "&observedFrom=invalid")
	if status != 400 {
		t.Fatal("invalid clock accepted")
	}
	var incidentID string
	if err = db.QueryRowContext(ctx, `SELECT incident_id FROM incident.finding_links WHERE finding_id=$1`, f.FindingID).Scan(&incidentID); err != nil {
		t.Fatal(err)
	}
	// Simulate an existing deliberate merge spanning another namespace. A primary
	// cluster-scoped resource must not grant visibility to every linked Finding.
	if _, err = db.ExecContext(ctx, `UPDATE finding.records SET namespace='restricted' WHERE finding_id=$1`, f.FindingID); err != nil {
		t.Fatal(err)
	}
	items, status = request("/api/v1/incidents?clusterUid=" + b.ClusterUID)
	if status != 200 || len(items) != 0 {
		t.Fatalf("linked namespace leaked list %d status %d", len(items), status)
	}
	_, status = request("/api/v1/incidents/" + incidentID)
	if status != 403 {
		t.Fatal("linked namespace leaked detail")
	}
}
