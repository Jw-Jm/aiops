package integration

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"ops-platform/internal/finding"
	"ops-platform/internal/incident"
	"ops-platform/internal/source"
	"testing"
)

// Called with the real OIDC HTTP API listener from the native live gate. These
// requests execute Foundation dispatch, mutation path rewriting, the shared
// idempotency transaction, persistent authorization and actual typed responses.
func sp05ReviewActualHTTPMutations(t *testing.T, ctx context.Context, db *sql.DB, pool *pgxpool.Pool, b source.BoundSourceContext, subject string, base finding.Envelope, call func(string, string, []byte, string) (int, []byte)) {
	t.Helper()
	fs := finding.Service{Pool: pool}
	revision := func(id string) int64 {
		var n int64
		if err := db.QueryRowContext(ctx, `SELECT revision FROM incident.records WHERE tenant_id=$1 AND incident_id=$2`, b.TenantID, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	create := func(n int) (string, []string) {
		family := "http-review-" + uuid.NewString()
		ids := []string{}
		for j := 0; j < n; j++ {
			e := base
			e.EventID = uuid.NewString()
			e.IdempotencyKey = e.EventID
			e.OccurrenceID = uuid.NewString()
			e.RuleFamily = family
			e.PayloadDigest = ""
			raw, _ := json.Marshal(e)
			status, body := call("POST", "/api/v2/findings:ingest", raw, e.EventID)
			if status != 200 {
				t.Fatalf("mutation setup actual ingress: %d %s", status, body)
			}
			var out struct {
				Data struct{ Finding finding.Finding }
			}
			if err := json.Unmarshal(body, &out); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, out.Data.Finding.FindingID)
		}
		if err := fs.RelayPass(ctx, b.TenantID, incident.Consume, 50); err != nil {
			t.Fatal(err)
		}
		var id string
		if err := db.QueryRowContext(ctx, `SELECT incident_id FROM incident.finding_links WHERE tenant_id=$1 AND finding_id=$2`, b.TenantID, ids[0]).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id, ids
	}
	type replay struct {
		path, key      string
		body, response []byte
	}
	replays := []replay{}
	post := func(path string, body any, want int, key string) []byte {
		raw, _ := json.Marshal(body)
		status, result := call("POST", path, raw, key)
		if status != want {
			t.Fatalf("actual mutation %s expected %d got %d %s", path, want, status, result)
		}
		return result
	}
	success := func(path string, body any) []byte {
		key := uuid.NewString()
		raw, _ := json.Marshal(body)
		response := post(path, body, 200, key)
		again := post(path, body, 200, key)
		if !bytes.Equal(response, again) {
			t.Fatal("mutation replay changed frozen response")
		}
		replays = append(replays, replay{path, key, raw, response})
		return response
	}
	a, fa := create(2)
	z, fz := create(1)
	transition := "/api/v1/incidents/" + a + ":transition"
	body := map[string]any{"expectedRevision": revision(a), "state": "acknowledged", "reason": "actual HTTP review"}
	success(transition, body)
	post(transition, body, 409, uuid.NewString())
	// A historical linked row may have a namespace no longer granted even when
	// the Incident primary remains visible. Current authorization must inspect all
	// linked resources before mutation or replay, not just the primary.
	if _, err := db.ExecContext(ctx, `UPDATE finding.records SET namespace='withdrawn' WHERE tenant_id=$1 AND finding_id=$2`, b.TenantID, fz[0]); err != nil {
		t.Fatal(err)
	}
	merge := map[string]any{"sourceIncidentId": z, "targetIncidentId": a, "expectedRevision": revision(z), "targetRevision": revision(a), "reason": "actual authorized merge"}
	post("/api/v1/incidents:merge", merge, 403, uuid.NewString())
	if _, err := db.ExecContext(ctx, `UPDATE finding.records SET namespace=$3 WHERE tenant_id=$1 AND finding_id=$2`, b.TenantID, fz[0], base.Namespace); err != nil {
		t.Fatal(err)
	}
	beforeMerge := revision(a)
	success("/api/v1/incidents:merge", merge)
	if revision(a) != beforeMerge+1 {
		t.Fatal("merge replay applied twice")
	}
	post("/api/v1/incidents:merge", merge, 409, uuid.NewString())
	split := map[string]any{"expectedRevision": revision(a), "findingIds": []string{fa[0]}, "reason": "actual authorized split"}
	splitPath := "/api/v1/incidents/" + a + ":split"
	beforeSplit := revision(a)
	success(splitPath, split)
	if revision(a) != beforeSplit+1 {
		t.Fatal("split replay applied twice")
	}
	post(splitPath, split, 409, uuid.NewString())
	// The same merge key is no longer readable after a linked namespace withdraw.
	if _, err := db.ExecContext(ctx, `UPDATE finding.records SET namespace='withdrawn' WHERE tenant_id=$1 AND finding_id=$2`, b.TenantID, fz[0]); err != nil {
		t.Fatal(err)
	}
	r := replays[1]
	status, _ := call("POST", r.path, r.body, r.key)
	if status != 403 {
		t.Fatal("merge replay leaked linked withdrawn scope", status)
	}
	if _, err := db.ExecContext(ctx, `UPDATE finding.records SET namespace=$3 WHERE tenant_id=$1 AND finding_id=$2`, b.TenantID, fz[0], base.Namespace); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE platform.role_bindings SET status='disabled',revision=revision+1 WHERE tenant_id=$1 AND subject=$2`, b.TenantID, subject); err != nil {
		t.Fatal(err)
	}
	for _, r := range replays {
		status, _ := call("POST", r.path, r.body, r.key)
		if status != 403 {
			t.Fatalf("revoked %s transport replay leaked: %d", r.path, status)
		}
	}
	if _, err := db.ExecContext(ctx, `UPDATE platform.role_bindings SET status='active',revision=revision+1 WHERE tenant_id=$1 AND subject=$2`, b.TenantID, subject); err != nil {
		t.Fatal(err)
	}
	t.Log("actual OIDC API HTTP transition/merge/split: typed success/error contracts, stale revisions, byte-identical replay and single mutation; linked namespace and role withdrawal deny cached replay")
}
