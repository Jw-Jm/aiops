package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"ops-platform/internal/finding"
	"ops-platform/internal/incident"
	"ops-platform/internal/persistence"
	"ops-platform/internal/resource"
	"ops-platform/internal/source"
	"sync"
	"testing"
	"time"
)

type sp05Seed struct {
	ClusterUID, Namespace, Backend string
	Tenant, Source                 uuid.UUID
}

func sp05Database(t *testing.T, seed ...sp05Seed) (context.Context, *sql.DB, *pgxpool.Pool, source.BoundSourceContext) {
	ctx, db, dir, dsn := newMigrationDatabaseWithTimeout(t, 3*time.Minute)
	if err := goose.UpToContext(ctx, db, dir, 1); err != nil {
		t.Fatal(err)
	}
	if err := runRemainingMigrationsAsMigrationRole(t, ctx, db, dsn, dir); err != nil {
		t.Fatal(err)
	}
	b := source.BoundSourceContext{TenantID: uuid.New(), SourceID: uuid.New(), ClusterID: uuid.New(), ClusterUID: "sp05-cluster", RegistrationRevision: 1, CredentialRevision: 1}
	namespace, backend := "apps", "sp05-native"
	if len(seed) == 1 {
		b.ClusterUID = seed[0].ClusterUID
		b.TenantID = seed[0].Tenant
		b.SourceID = seed[0].Source
		namespace = seed[0].Namespace
		backend = seed[0].Backend
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.tenants(tenant_id,slug,display_name) VALUES($1,'sp05','SP05')`, b.TenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.cluster_registrations(tenant_id,cluster_id,cluster_uid,display_name) VALUES($1,$2,$3,'SP05')`, b.TenantID, b.ClusterID, b.ClusterUID); err != nil {
		t.Fatal(err)
	}
	mapping, _ := json.Marshal(map[string]any{"scopes": map[string][]string{"cluster": {b.ClusterUID}, "namespace": {namespace}}})
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.source_registrations(tenant_id,source_id,cluster_id,source_type,instance_key,auth_ref,allowed_schemas,backend_logical_id,data_scope_mapping) VALUES($1,$2,$3,'kubernetes','sp05','openbao://sp05/readonly',ARRAY['finding-envelope/v2'],$5,$4)`, b.TenantID, b.SourceID, b.ClusterID, mapping, backend); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, runtimePoolConfig(t, ctx, db, dsn, "worker_runtime_role"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return ctx, db, pool, b
}
func sp05Envelope(b source.BoundSourceContext, event, occurrence string) finding.Envelope {
	clock := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	id := resource.CanonicalID{Domain: "k8s", Tenant: b.TenantID.String(), Scope: b.ClusterUID, APIGroup: "core", Kind: "Node", StableID: "node-a"}.String()
	return finding.Envelope{SchemaVersion: "finding-envelope/v2", EventID: event, IdempotencyKey: event, ResourceCanonicalID: id, RuleID: "node-ready/v1", RuleFamily: "node", NormalizedSymptom: "NotReady", OccurrenceID: occurrence, StartsAt: clock, ObservedAt: clock, SourceSequence: 1, TimeReliable: true, State: "firing", Severity: "warning", Payload: json.RawMessage(`{"ready":false}`), EvidenceRefs: []string{}}
}
func TestSP05FindingAtomicConcurrentTransportAndRecovery(t *testing.T) {
	ctx, db, pool, b := sp05Database(t)
	service := finding.Service{Pool: pool}
	e := sp05Envelope(b, "e1", "o1")
	// Rollback at the outbox boundary must erase every earlier mutation.
	if _, err := db.ExecContext(ctx, `CREATE FUNCTION finding.sp05_fault() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'owned transaction fault'; END $$; CREATE TRIGGER sp05_fault BEFORE INSERT ON finding.outbox FOR EACH ROW EXECUTE FUNCTION finding.sp05_fault()`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Ingest(ctx, b, e); err == nil {
		t.Fatal("transaction fault not observed")
	}
	for _, table := range []string{"finding.records", "finding.inbox", "finding.timeline", "finding.transport_keys", "finding.outbox"} {
		var n int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("partial commit %s=%d %v", table, n, err)
		}
	}
	if _, err := db.ExecContext(ctx, `DROP TRIGGER sp05_fault ON finding.outbox; DROP FUNCTION finding.sp05_fault()`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	ids := make(chan string, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); f, _, err := service.Ingest(ctx, b, e); errs <- err; ids <- f.FindingID }()
	}
	wg.Wait()
	close(errs)
	close(ids)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	first := ""
	for id := range ids {
		if first == "" {
			first = id
		}
		if first != id {
			t.Fatal("duplicate aggregate")
		}
	}
	updated := e
	updated.EventID = "e2"
	updated.IdempotencyKey = "k2"
	updated.SourceSequence = 2
	updated.Payload = json.RawMessage(`{"ready":false,"reason":"unreachable"}`)
	f, dis, err := service.Ingest(ctx, b, updated)
	if err != nil || dis != finding.Accepted || f.AggregateRevision != 2 {
		t.Fatalf("update %+v %s %v", f, dis, err)
	}
	conflict := updated
	conflict.State = "resolved"
	if _, _, err := service.Ingest(ctx, b, conflict); !errors.Is(err, finding.ErrConflict) {
		t.Fatal("digest conflict", err)
	}
	alias := updated
	alias.EventID = "e2-alias"
	if _, dis, err := service.Ingest(ctx, b, alias); err != nil || dis != finding.Duplicate {
		t.Fatal("transport alias", dis, err)
	}
	if _, _, err := service.Ingest(ctx, b, conflict); !errors.Is(err, finding.ErrConflict) {
		t.Fatal(err)
	}
	// Claim, abandon and recover unchanged identity; failed consumer rolls back inbox.
	d, err := service.Claim(ctx, b.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE finding.outbox SET claimed_at=clock_timestamp()-interval '31 seconds' WHERE tenant_id=$1 AND outbox_id=$2`, b.TenantID, d.EventID); err != nil {
		t.Fatal(err)
	}
	recovered, err := service.Claim(ctx, b.TenantID)
	if err != nil || recovered.EventID != d.EventID || recovered.Token == d.Token {
		t.Fatal("claim recovery", err)
	}
	if err := service.Deliver(ctx, d, incident.Consume); err == nil {
		t.Fatal("old claim accepted")
	}
	if err := service.Deliver(ctx, recovered, func(ctx context.Context, tx pgx.Tx, d finding.Delivery) error {
		if err := incident.Consume(ctx, tx, d); err != nil {
			return err
		}
		return errors.New("owned post-consumer fault")
	}); err == nil {
		t.Fatal("fault accepted")
	}
	var n int
	_ = db.QueryRowContext(ctx, `SELECT count(*) FROM incident.records`).Scan(&n)
	if n != 0 {
		t.Fatal("consumer half commit")
	}
	_, err = db.ExecContext(ctx, `UPDATE finding.outbox SET next_attempt_at=clock_timestamp() WHERE tenant_id=$1`, b.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RelayPass(ctx, b.TenantID, incident.Consume, 100); err != nil {
		t.Fatal(err)
	}
	_ = db.QueryRowContext(ctx, `SELECT count(*) FROM incident.records`).Scan(&n)
	if n != 1 {
		t.Fatal("missing/duplicate Incident", n)
	}
	// Ingestion revocation applies before transport replay.
	if _, err := db.ExecContext(ctx, `UPDATE platform.source_registrations SET status='disabled',revision=revision+1 WHERE tenant_id=$1`, b.TenantID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Ingest(ctx, b, e); !errors.Is(err, finding.ErrUnauthorized) {
		t.Fatal("revoked source replay", err)
	}
}
func TestSP05ResolvedBeforeFiringAndTenantIsolation(t *testing.T) {
	ctx, db, pool, b := sp05Database(t)
	s := finding.Service{Pool: pool}
	e := sp05Envelope(b, "resolved", "o1")
	e.State = "resolved"
	e.SourceSequence = 3
	f, _, err := s.Ingest(ctx, b, e)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RelayPass(ctx, b.TenantID, incident.Consume, 20); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = db.QueryRowContext(ctx, `SELECT count(*) FROM incident.records`).Scan(&n)
	if n != 0 {
		t.Fatal("resolved-first opened Incident")
	}
	late := e
	late.EventID = "late"
	late.IdempotencyKey = "late"
	late.State = "firing"
	late.SourceSequence = 4
	f2, dis, err := s.Ingest(ctx, b, late)
	if err != nil || dis != finding.Stale || f2.State != "resolved" || f.AggregateRevision != f2.AggregateRevision {
		t.Fatal("late revival", dis, err)
	}
	foreign := b
	foreign.TenantID = uuid.New()
	if _, _, err := s.Ingest(ctx, foreign, e); !errors.Is(err, finding.ErrUnauthorized) {
		t.Fatal("cross tenant", err)
	}
	if err := persistence.WithTenantTx(ctx, pool, foreign.TenantID, func(tx pgx.Tx) error {
		_, err := finding.Load(ctx, tx, foreign.TenantID, f.FindingID)
		if !errors.Is(err, pgx.ErrNoRows) {
			return errors.New("RLS bypass")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	poison := e
	poison.EventID = "poison"
	poison.Payload = json.RawMessage(`{"x":`)
	if _, _, err := s.Ingest(ctx, b, poison); !errors.Is(err, finding.ErrInvalid) {
		t.Fatal("poison accepted", err)
	}
}
