package integration

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/finding"
	"ops-platform/internal/graph"
	"ops-platform/internal/incident"
	"ops-platform/internal/persistence"
	"sync"
	"testing"
	"time"
)

func TestSP05IncidentConcurrentCorrelationOverrideSplitMergeRecovery(t *testing.T) {
	ctx, db, pool, b := sp05Database(t)
	service := finding.Service{Pool: pool}
	ids := []string{}
	for n := 0; n < 8; n++ {
		e := sp05Envelope(b, fmt.Sprintf("finding-%d", n), fmt.Sprintf("occurrence-%d", n))
		f, _, err := service.Ingest(ctx, b, e)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, f.FindingID)
	}
	var wg sync.WaitGroup
	failures := make(chan error, 4)
	for n := 0; n < 4; n++ {
		wg.Add(1)
		go func() { defer wg.Done(); failures <- service.RelayPass(ctx, b.TenantID, incident.Consume, 50) }()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	var count int
	var id string
	if err := db.QueryRowContext(ctx, `SELECT count(*),min(incident_id::text) FROM incident.records`).Scan(&count, &id); err != nil || count != 1 {
		t.Fatalf("concurrent subject duplicated: %d %v", count, err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.role_bindings(tenant_id,binding_id,subject,role_name,cluster_scopes,namespace_scopes) VALUES($1,$2,'sp05-operator','operator',jsonb_build_array($3::text),'[]')`, b.TenantID, uuid.New(), b.ClusterID); err != nil {
		t.Fatal(err)
	}
	scope, err := (graph.Authorization{Pool: pool}).Effective(ctx, b.TenantID.String(), "sp05-operator", b.ClusterUID)
	if err != nil {
		t.Fatal(err)
	}
	load := func(id string) incident.Incident {
		var i incident.Incident
		if err := persistence.WithTenantTx(ctx, pool, b.TenantID, func(tx pgx.Tx) error {
			var err error
			i, err = incident.Load(ctx, tx, b.TenantID, id, false)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return i
	}
	mutations := incident.Service{Pool: pool}
	i := load(id)
	if _, err := mutations.Change(ctx, scope, "sp05-operator", id, incident.Change{ExpectedRevision: i.Revision - 1, State: "acknowledged", Reason: "stale operator"}); !errors.Is(err, incident.ErrRevision) {
		t.Fatalf("stale revision: %v", err)
	}
	until := time.Now().Add(time.Minute)
	if _, err := mutations.Change(ctx, scope, "sp05-operator", id, incident.Change{ExpectedRevision: i.Revision, State: "suppressed", SuppressedUntil: &until, Reason: "owned suppression"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE incident.records SET suppressed_until=clock_timestamp()-interval '1 second' WHERE incident_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err := mutations.RecoveryPass(ctx, b.TenantID, func(string) bool { return true }); err != nil {
		t.Fatal(err)
	}
	i = load(id)
	if i.State != "open" {
		t.Fatal("suppression never expired")
	}
	split, err := mutations.Split(ctx, scope, "sp05-operator", id, "owned split", i.Revision, ids[:2])
	if err != nil {
		t.Fatal(err)
	}
	i = load(id)
	merged, err := mutations.Merge(ctx, scope, "sp05-operator", id, split.IncidentID, "owned merge", i.Revision, split.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if !incident.Active(merged.State) || load(split.IncidentID).State != "closed" {
		t.Fatal("merge state")
	}
	// Every linked occurrence is resolved before the platform's knowledge clock
	// starts. The five-minute settle window is exercised through persisted clocks.
	for n := 0; n < 8; n++ {
		e := sp05Envelope(b, fmt.Sprintf("resolved-%d", n), fmt.Sprintf("occurrence-%d", n))
		e.State = "resolved"
		e.SourceSequence = 2
		e.ObservedAt = e.ObservedAt.Add(time.Second)
		if _, _, err := service.Ingest(ctx, b, e); err != nil {
			t.Fatal(err)
		}
	}
	if err := service.RelayPass(ctx, b.TenantID, incident.Consume, 50); err != nil {
		t.Fatal(err)
	}
	if err := mutations.RecoveryPass(ctx, b.TenantID, func(string) bool { return true }); err != nil {
		t.Fatal(err)
	}
	if load(id).State == "resolved" {
		t.Fatal("settle window bypassed")
	}
	if _, err := db.ExecContext(ctx, `UPDATE incident.records SET recovery_known_at=clock_timestamp()-interval '301 seconds' WHERE incident_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err := mutations.RecoveryPass(ctx, b.TenantID, func(string) bool { return false }); err != nil {
		t.Fatal(err)
	}
	if load(id).State == "resolved" {
		t.Fatal("unavailable graph counted healthy")
	}
	if _, err := db.ExecContext(ctx, `UPDATE platform.source_registrations SET revision=revision+1 WHERE source_id=$1`, b.SourceID); err != nil {
		t.Fatal(err)
	}
	if err := mutations.RecoveryPass(ctx, b.TenantID, func(string) bool { return true }); err != nil {
		t.Fatal(err)
	}
	if load(id).State == "resolved" {
		t.Fatal("rotated source counted healthy without current-revision observation")
	}
	// Withdrawal must fence a stale in-memory operator scope.
	if _, err := db.ExecContext(ctx, `UPDATE platform.role_bindings SET status='disabled' WHERE subject='sp05-operator'`); err != nil {
		t.Fatal(err)
	}
	i = load(id)
	if _, err := mutations.Change(context.WithoutCancel(ctx), scope, "sp05-operator", id, incident.Change{ExpectedRevision: i.Revision, State: "acknowledged", Reason: "stale grant"}); !errors.Is(err, graph.ErrScope) {
		t.Fatalf("withdrawn grant: %v", err)
	}
}
