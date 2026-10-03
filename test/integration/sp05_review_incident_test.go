package integration

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/evidence"
	"ops-platform/internal/finding"
	"ops-platform/internal/graph"
	"ops-platform/internal/incident"
	"ops-platform/internal/persistence"
	"strings"
	"testing"
	"time"
)

func TestSP05SuppressionRecoveryAndMergeSplitReviewRegression(t *testing.T) {
	ctx, db, pool, b := sp05Database(t)
	fs := finding.Service{Pool: pool}
	s := incident.Service{Pool: pool}
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.role_bindings(tenant_id,binding_id,subject,role_name,cluster_scopes,namespace_scopes) VALUES($1,$2,'review-operator','operator',jsonb_build_array($3::text),'[]')`, b.TenantID, uuid.New(), b.ClusterID); err != nil {
		t.Fatal(err)
	}
	scope, err := (graph.Authorization{Pool: pool}).Effective(ctx, b.TenantID.String(), "review-operator", b.ClusterUID)
	if err != nil {
		t.Fatal(err)
	}
	load := func(id string) incident.Incident {
		var raw []byte
		if err := db.QueryRowContext(ctx, `SELECT jsonb_build_object('state',state,'revision',revision,'recoveryKnownAt',recovery_known_at,'suppressedUntil',suppressed_until) FROM incident.records WHERE incident_id=$1`, id).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var i incident.Incident
		if json.Unmarshal(raw, &i) != nil {
			t.Fatal("decode")
		}
		return i
	}
	create := func(prefix string, n int) (string, []string) {
		ids := []string{}
		for j := 0; j < n; j++ {
			e := sp05Envelope(b, prefix+uuid.NewString(), prefix+uuid.NewString())
			e.RuleFamily = prefix
			f, _, err := fs.Ingest(ctx, b, e)
			if err != nil {
				t.Fatal(err)
			}
			ids = append(ids, f.FindingID)
		}
		if err := fs.RelayPass(ctx, b.TenantID, incident.Consume, 50); err != nil {
			t.Fatal(err)
		}
		var id string
		if err := db.QueryRowContext(ctx, `SELECT incident_id FROM incident.finding_links WHERE finding_id=$1`, ids[0]).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id, ids
	}
	resolve := func(ids []string) {
		for _, id := range ids {
			var payload []byte
			if err := db.QueryRowContext(ctx, `SELECT payload FROM finding.records WHERE finding_id=$1`, id).Scan(&payload); err != nil {
				t.Fatal(err)
			}
			var f finding.Finding
			if json.Unmarshal(payload, &f) != nil {
				t.Fatal("decode finding")
			}
			e := f.Envelope
			e.EventID = uuid.NewString()
			e.IdempotencyKey = e.EventID
			e.State = "resolved"
			e.SourceSequence++
			e.ObservedAt = e.ObservedAt.Add(time.Second)
			if _, _, err := fs.Ingest(ctx, b, e); err != nil {
				t.Fatal(err)
			}
		}
		if err := fs.RelayPass(ctx, b.TenantID, incident.Consume, 50); err != nil {
			t.Fatal(err)
		}
	}
	suppress := func(id string) {
		until := time.Now().Add(time.Minute)
		if _, err := s.Change(ctx, scope, "review-operator", id, incident.Change{ExpectedRevision: load(id).Revision, State: "suppressed", SuppressedUntil: &until, Reason: "review suppression"}); err != nil {
			t.Fatal(err)
		}
	}
	expire := func(id string) {
		if _, err := db.ExecContext(ctx, `UPDATE incident.records SET suppressed_until=clock_timestamp()-interval '1 second' WHERE incident_id=$1`, id); err != nil {
			t.Fatal(err)
		}
		if err := s.RecoveryPass(ctx, b.TenantID, func(string) bool { return true }); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("active-expiry", func(t *testing.T) {
		id, _ := create("active", 1)
		suppress(id)
		expire(id)
		if load(id).State != "open" {
			t.Fatal("active suppressed did not open")
		}
	})
	t.Run("recovered-expiry", func(t *testing.T) {
		id, ids := create("recovered", 1)
		suppress(id)
		resolve(ids)
		before := load(id)
		ref := uuid.New()
		until := time.Now().Add(366 * 24 * time.Hour)
		if _, err := db.ExecContext(ctx, `INSERT INTO platform.evidence_metadata(tenant_id,evidence_id,source_id,canonical_id,namespace,metadata,content_digest,replay_state,retain_until) VALUES($1,$2,$3,$4,'','{}',$5,'source_available',clock_timestamp()-interval '1 day')`, b.TenantID, ref, b.SourceID, sp05Envelope(b, "", "").ResourceCanonicalID, "sha256:"+strings.Repeat("0", 64)); err != nil {
			t.Fatal(err)
		}
		if err := persistence.WithTenantTx(ctx, pool, b.TenantID, func(tx pgx.Tx) error {
			return evidence.Protect(ctx, tx, b.TenantID, ref, uuid.MustParse(id), "incident", until, true)
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Change(ctx, scope, "review-operator", id, incident.Change{ExpectedRevision: before.Revision, State: "open", Reason: "invalid no-active reopen"}); !errors.Is(err, incident.ErrTransition) {
			t.Errorf("manual reopen without activity: %v", err)
		}
		expire(id)
		after := load(id)
		var active bool
		var retained time.Time
		if err := db.QueryRowContext(ctx, `SELECT active,retain_until FROM platform.evidence_retention_references WHERE tenant_id=$1 AND reference_id=$2 AND evidence_id=$3`, b.TenantID, id, ref).Scan(&active, &retained); err != nil || active || retained.Before(until.Add(-time.Microsecond)) {
			t.Fatal("closure must release active hold without shortening 365-day dependency", active, retained, err)
		}
		if after.State != "closed" {
			t.Errorf("no-active expired suppression=%s want closed", after.State)
		}
		var n int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM incident.timeline WHERE incident_id=$1 AND kind='suppression_expired'`, id).Scan(&n); err != nil || n != 1 {
			t.Fatal("expiry timeline", n, err)
		}
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM incident.outbox WHERE incident_id=$1 AND revision=$2`, id, after.Revision).Scan(&n); err != nil || n != 1 {
			t.Fatal("expiry outbox", n, err)
		}
	})
	for _, moveResolved := range []bool{true, false} {
		t.Run(map[bool]string{true: "split-moved-resolved", false: "split-left-resolved"}[moveResolved], func(t *testing.T) {
			id, ids := create(uuid.NewString(), 2)
			resolve(ids[:1])
			moving := ids[:1]
			if !moveResolved {
				moving = ids[1:]
			}
			child, err := s.Split(ctx, scope, "review-operator", id, "review split", load(id).Revision, moving)
			if err != nil {
				t.Fatal(err)
			}
			resolved, active := id, child.IncidentID
			if moveResolved {
				resolved, active = child.IncidentID, id
			}
			if load(resolved).RecoveryKnownAt == nil {
				t.Errorf("resolved split lost recovery clock")
			}
			if load(active).RecoveryKnownAt != nil {
				t.Error("firing split has recovery clock")
			}
			if _, err := db.ExecContext(ctx, `UPDATE incident.records SET recovery_known_at=clock_timestamp()-interval '301 seconds' WHERE incident_id=$1 AND recovery_known_at IS NOT NULL`, resolved); err != nil {
				t.Fatal(err)
			}
			if err := s.RecoveryPass(ctx, b.TenantID, func(string) bool { return false }); err != nil {
				t.Fatal(err)
			}
			if load(resolved).State == "resolved" {
				t.Error("unavailable graph bypassed")
			}
			if err := s.RecoveryPass(ctx, b.TenantID, func(string) bool { return true }); err != nil {
				t.Fatal(err)
			}
			if load(resolved).State != "resolved" {
				t.Errorf("resolved split never recovered: %s", load(resolved).State)
			}
		})
	}
	t.Run("merge-all-resolved", func(t *testing.T) {
		a, fa := create("merge-a", 1)
		z, fz := create("merge-z", 1)
		resolve(append(fa, fz...))
		_, err := s.Merge(ctx, scope, "review-operator", a, z, "review merge", load(a).Revision, load(z).Revision)
		if err != nil {
			t.Fatal(err)
		}
		if load(a).RecoveryKnownAt == nil {
			t.Fatal("merge cleared all-resolved recovery clock")
		}
		if _, err := db.ExecContext(ctx, `UPDATE incident.records SET recovery_known_at=clock_timestamp()-interval '301 seconds' WHERE incident_id=$1`, a); err != nil {
			t.Fatal(err)
		}
		if err := s.RecoveryPass(ctx, b.TenantID, func(string) bool { return true }); err != nil {
			t.Fatal(err)
		}
		if load(a).State != "resolved" {
			t.Fatal("merge never recovered")
		}
	})
}

func TestSP05ExactNumericTransportConflictAndFormalWindowBoundaries(t *testing.T) {
	ctx, db, pool, b := sp05Database(t)
	fs := finding.Service{Pool: pool}
	for n, pair := range [][2]string{{`{"counter":9007199254740992}`, `{"counter":9007199254740993}`}, {`{"value":0.123456789012345678901}`, `{"value":0.123456789012345678902}`}} {
		e := sp05Envelope(b, uuid.NewString(), uuid.NewString())
		e.Payload = json.RawMessage(pair[0])
		e.RuleFamily = fmt.Sprint("precision", n)
		if _, _, err := fs.Ingest(ctx, b, e); err != nil {
			t.Fatal(err)
		}
		e.Payload = json.RawMessage(pair[1])
		if _, _, err := fs.Ingest(ctx, b, e); !errors.Is(err, finding.ErrConflict) {
			t.Fatal("numeric transport conflict lost", err)
		}
	}
	for _, reopen := range []bool{false, true} {
		first := sp05Envelope(b, uuid.NewString(), uuid.NewString())
		first.RuleFamily = fmt.Sprint("formal-window", reopen)
		f, _, err := fs.Ingest(ctx, b, first)
		if err != nil {
			t.Fatal(err)
		}
		if err := fs.RelayPass(ctx, b.TenantID, incident.Consume, 50); err != nil {
			t.Fatal(err)
		}
		var old string
		if err := db.QueryRowContext(ctx, `SELECT incident_id FROM incident.finding_links WHERE finding_id=$1`, f.FindingID).Scan(&old); err != nil {
			t.Fatal(err)
		}
		after := 11 * time.Minute
		if reopen {
			after = 31 * time.Minute
			if _, err := db.ExecContext(ctx, `UPDATE incident.records SET state='resolved',resolved_at=$2 WHERE incident_id=$1`, old, first.StartsAt); err != nil {
				t.Fatal(err)
			}
		}
		next := first
		next.EventID = uuid.NewString()
		next.IdempotencyKey = next.EventID
		next.OccurrenceID = uuid.NewString()
		next.StartsAt = next.StartsAt.Add(after)
		next.ObservedAt = next.StartsAt
		f, _, err = fs.Ingest(ctx, b, next)
		if err != nil {
			t.Fatal(err)
		}
		if err := fs.RelayPass(ctx, b.TenantID, incident.Consume, 50); err != nil {
			t.Fatal(err)
		}
		var id string
		if err := db.QueryRowContext(ctx, `SELECT incident_id FROM incident.finding_links WHERE finding_id=$1`, f.FindingID).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if id == old {
			t.Fatalf("formal independent 11m/31m boundary incorrectly correlated: reopen=%t", reopen)
		}
	}
}
