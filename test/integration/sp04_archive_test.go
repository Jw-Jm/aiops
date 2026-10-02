package integration

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"net/http"
	"net/http/httptest"
	"ops-platform/internal/archive"
	"ops-platform/internal/auth"
	"ops-platform/internal/contract"
	protect "ops-platform/internal/crypto"
	"ops-platform/internal/datascope"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/integrations/openbao"
	"ops-platform/internal/persistence"
	"ops-platform/internal/resource"
	"strings"
	"sync"
	"testing"
	"time"
)

// PostgreSQL runs production migrations/RLS/runtime roles. External calls are
// fault injected here; these tests are not live Transit/S3 qualification.
func TestSP04ArchiveRecoveryIntegrityAndRetention(t *testing.T) {
	ctx, db, dir, dsn := newMigrationDatabase(t)
	if err := goose.UpToContext(ctx, db, dir, 1); err != nil {
		t.Fatal(err)
	}
	if err := runRemainingMigrationsAsMigrationRole(t, ctx, db, dsn, dir); err != nil {
		t.Fatal(err)
	}
	tenant, other, source, cluster := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.tenants(tenant_id,slug,display_name) VALUES($1,'sp04-a','SP04 A'),($2,'sp04-b','SP04 B')`, tenant, other); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.cluster_registrations(tenant_id,cluster_id,cluster_uid,display_name) VALUES($1,$2,'cluster-a','Archive test')`, tenant, cluster); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.source_registrations(tenant_id,source_id,source_type,instance_key,auth_ref,backend_logical_id,cluster_id) VALUES($1,$2,'victoriametrics','sp04','openbao://test/sp04','metrics-sp04',$3)`, tenant, source, cluster); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, runtimePoolConfig(t, ctx, db, dsn, "worker_runtime_role"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	backend := &sp04FaultBackend{objects: map[string]archive.StoredObject{}, uncertain: true}
	store, err := archive.NewStore(backend, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	protector, err := protect.NewTransitProtector(sp04FaultTransit{}, "evidence-archive")
	if err != nil {
		t.Fatal(err)
	}
	service := &evidence.ArchiveService{Pool: pool, Store: store, Protector: protector, BackendLogicalID: "archive-sp04"}
	now := time.Now().UTC()
	plain := []byte(`[{"value":"bounded-sanitized-fact"}]`)
	canonical := resource.CanonicalID{Domain: "k8s", Tenant: tenant.String(), Scope: "cluster-a", APIGroup: "core", Kind: "Pod", StableID: "p1"}.String()
	e := evidence.Evidence{SourceScopeDigest: evidence.BindingScopeDigest(evidence.Binding{Revision: 1, BackendLogicalID: "metrics-sp04", SourceType: "victoriametrics"}), SchemaVersion: "evidence/v2", Type: "metric", DataClass: "D1", IndependenceGroup: source.String(), EvidenceID: uuid.NewString(), TenantID: tenant.String(), ResourceCanonicalID: canonical, SourceRegistrationID: source.String(), SourceRevision: 1, SourceSystem: "victoriametrics", BackendLogicalID: "metrics-sp04", QueryTemplateVersion: "pod-phase/v1", QueryHash: evidence.Digest([]byte("query")), EffectiveScope: graph.Scope{Tenant: tenant.String(), Cluster: "cluster-a", Namespaces: []string{"apps"}, AuthorizationRevision: "test-revision"}, EvaluatedAt: now, ObservedFrom: now.Add(-time.Minute), ObservedTo: now, SourceRetentionUntil: now, ContentDigest: evidence.Digest(plain), Data: plain, DerivationEvidenceRefs: []string{}}
	retain := now.Add(180 * 24 * time.Hour)
	id := uuid.MustParse(e.EvidenceID)
	if err = service.Capture(ctx, e, "apps", retain); err == nil {
		t.Fatal("uncertain upload falsely committed")
	}
	var state string
	if err = db.QueryRowContext(ctx, `SELECT replay_state FROM platform.evidence_metadata WHERE tenant_id=$1 AND evidence_id=$2`, tenant, id).Scan(&state); err != nil || state != "archive_pending" {
		t.Fatalf("pending state %s %v", state, err)
	}
	if err = service.Recover(ctx, tenant, id); err != nil {
		t.Fatal(err)
	}
	got, ref, err := service.Read(ctx, tenant, id)
	if err != nil || !bytes.Equal(got, plain) || ref.Object.VersionID == "" || ref.EncryptionKeyVersion != "1" {
		t.Fatalf("recover/read %v %+v", err, ref)
	}
	if backend.puts != 1 {
		t.Fatalf("uncertain upload repeated %d puts", backend.puts)
	}
	if _, _, err = service.Read(ctx, other, id); err == nil {
		t.Fatal("cross tenant replay succeeded")
	}
	t.Run("source-withdrawal-between-collection-and-intent", func(t *testing.T) {
		mapping := datascope.Mapping{Scopes: map[string][]string{"cluster": {"cluster-a"}, "namespace": {"apps"}}}
		id := uuid.New()
		raw, _ := json.Marshal(mapping)
		if _, err := db.ExecContext(ctx, `INSERT INTO platform.source_registrations(tenant_id,source_id,source_type,instance_key,auth_ref,backend_logical_id,cluster_id,data_scope_mapping) VALUES($1,$2,'victoriametrics','withdrawal-race','openbao://test/vm','metrics-sp04',$3,$4)`, tenant, id, cluster, raw); err != nil {
			t.Fatal(err)
		}
		collected := e
		collected.SourceRegistrationID = id.String()
		collected.EvidenceID = uuid.NewString()
		collected.SourceScopeDigest = evidence.BindingScopeDigest(evidence.Binding{Revision: 1, BackendLogicalID: "metrics-sp04", SourceType: "victoriametrics", ScopeMapping: mapping})
		if _, err := db.ExecContext(ctx, `UPDATE platform.source_registrations SET data_scope_mapping=jsonb_set(data_scope_mapping,'{scopes,namespace}','["withdrawn"]') WHERE tenant_id=$1 AND source_id=$2`, tenant, id); err != nil {
			t.Fatal(err)
		}
		if err := service.Prepare(ctx, collected, "apps", now.Add(181*24*time.Hour)); !errors.Is(err, evidence.ErrScopeUnverified) {
			t.Fatalf("withdrawn collection rebound to archive-time grant: %v", err)
		}
		var count int
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM platform.evidence_metadata WHERE tenant_id=$1 AND evidence_id=$2`, tenant, collected.EvidenceID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("withdrawn fact created metadata/intent: %d %v", count, err)
		}
	})
	t.Run("concurrent-first-resource-observations", func(t *testing.T) {
		projectionSource := uuid.New()
		if _, err := db.ExecContext(ctx, `INSERT INTO platform.source_registrations(tenant_id,source_id,source_type,instance_key,auth_ref,backend_logical_id,cluster_id) VALUES($1,$2,'kubernetes','projection-race','openbao://test/collector','collector-race',$3)`, tenant, projectionSource, cluster); err != nil {
			t.Fatal(err)
		}
		projection := e
		projection.SourceRegistrationID = projectionSource.String()
		projection.SourceSystem = "kubernetes"
		projection.BackendLogicalID = "collector-race"
		projection.SourceScopeDigest = evidence.BindingScopeDigest(evidence.Binding{Revision: 1, BackendLogicalID: "collector-race", SourceType: "kubernetes"})
		projection.Type = "resource_state"
		projection.DataClass = "D0"
		projection.QueryTemplateVersion = "kubernetes-projection/v1"
		projection.EvidenceID = uuid.NewString()
		projection.IndependenceGroup = projectionSource.String()
		projection.EffectiveScope.AuthorizationRevision = "collector/" + projectionSource.String() + "/1"
		var workers sync.WaitGroup
		failures := make(chan error, 16)
		start := make(chan struct{})
		for i := 0; i < 16; i++ {
			workers.Add(1)
			go func(index int) {
				defer workers.Done()
				<-start
				fact := projection
				fact.EvaluatedAt = now.Add(time.Duration(index) * time.Millisecond)
				fact.ObservedFrom = fact.EvaluatedAt
				fact.ObservedTo = fact.EvaluatedAt
				fact.SourceRetentionUntil = fact.EvaluatedAt
				failures <- service.Prepare(ctx, fact, "apps", fact.EvaluatedAt.Add(181*24*time.Hour))
			}(i)
		}
		close(start)
		workers.Wait()
		close(failures)
		for err := range failures {
			if err != nil {
				t.Fatalf("same immutable UID/RV/content first-observation race not idempotent: %v", err)
			}
		}
		original, err := (evidence.Repository{Pool: pool}).Get(ctx, tenant, uuid.MustParse(projection.EvidenceID), projection.EffectiveScope)
		if err != nil {
			t.Fatal(err)
		}
		changed := projection
		changed.Data = []byte(`{"changed":true}`)
		changed.ContentDigest = evidence.Digest(changed.Data)
		if err := service.Prepare(ctx, changed, "apps", retain); err == nil {
			t.Fatal("projection race normalization accepted a changed payload")
		}
		later := projection
		later.EvaluatedAt = now.Add(time.Second)
		later.ObservedFrom = later.EvaluatedAt
		later.ObservedTo = later.EvaluatedAt
		later.SourceRetentionUntil = later.EvaluatedAt
		if err := service.Prepare(ctx, later, "apps", later.EvaluatedAt.Add(181*24*time.Hour)); err != nil {
			t.Fatal(err)
		}
		after, err := (evidence.Repository{Pool: pool}).Get(ctx, tenant, uuid.MustParse(projection.EvidenceID), projection.EffectiveScope)
		if err != nil || !after.EvaluatedAt.Equal(original.EvaluatedAt) || !after.ObservedFrom.Equal(original.ObservedFrom) {
			t.Fatal("later query renewed immutable observation")
		}
	})
	// Even retries of verified metadata must recheck ciphertext/plaintext digests.
	backend.mu.Lock()
	original := backend.objects[ref.Object.Key]
	corrupt := original
	corrupt.Body = []byte("damaged")
	backend.objects[ref.Object.Key] = corrupt
	backend.mu.Unlock()
	if err = service.Recover(ctx, tenant, id); err == nil {
		t.Fatal("verified corrupted object accepted")
	}
	backend.mu.Lock()
	backend.objects[ref.Object.Key] = original
	backend.mu.Unlock()
	changed := e
	changed.SourceRegistrationID = uuid.NewString()
	if err = service.Capture(ctx, changed, "apps", retain); err == nil {
		t.Fatal("idempotent ID rebound provenance")
	}
	if err = persistence.WithTenantTx(ctx, pool, tenant, func(tx pgx.Tx) error { return evidence.SetLegalHold(ctx, tx, tenant, id, true, "sp04-test") }); err != nil {
		t.Fatal(err)
	}
	if err = service.Cleanup(ctx, tenant, id); err == nil {
		t.Fatal("held object deleted")
	}
	if err = persistence.WithTenantTx(ctx, pool, tenant, func(tx pgx.Tx) error { return evidence.SetLegalHold(ctx, tx, tenant, id, false, "sp04-test") }); err != nil {
		t.Fatal(err)
	}
	// An expired 180-day base must still preserve its 365-day audit dependency.
	if _, err = db.ExecContext(ctx, `UPDATE platform.evidence_metadata SET retain_until=clock_timestamp()-interval '1 day' WHERE tenant_id=$1 AND evidence_id=$2`, tenant, id); err != nil {
		t.Fatal(err)
	}
	if err = service.Cleanup(ctx, tenant, id); err == nil {
		t.Fatal("audit dependency deleted")
	}
	var count int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM platform.evidence_retention_references WHERE tenant_id=$1 AND evidence_id=$2 AND reference_kind='audit'`, tenant, id).Scan(&count); err != nil || count != 3 {
		t.Fatalf("audit closure %d %v", count, err)
	}
	t.Run("recover-original-query-even-when-source-is-partial", func(t *testing.T) {
		pending := e
		pending.EvidenceID = uuid.NewString()
		pending.SourceRetentionUntil = time.Now().Add(time.Hour)
		q := evidence.Query{ResourceCanonicalID: canonical, Namespace: "apps", Template: "pod-phase/v1", From: e.ObservedFrom, To: e.ObservedTo, Limit: 7, MaxBytes: 8192, TimeoutMillis: 700, Scope: e.EffectiveScope}
		service.Protector = sp04SealUnavailable{protector}
		if err := service.CaptureQuery(ctx, pending, "apps", retain, q); err == nil {
			t.Fatal("Transit outage committed")
		}
		service.Protector = protector
		replay := &sp04ReplayAdapter{Expected: q, Fact: pending}
		if err := service.RecoverWithSources(ctx, tenant, map[string]evidence.Adapter{source.String(): replay}); err != nil {
			t.Fatal(err)
		}
		if replay.Calls != 1 {
			t.Fatalf("source replay calls=%d", replay.Calls)
		}
		if _, _, err := service.Read(ctx, tenant, uuid.MustParse(pending.EvidenceID)); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("source-expired-before-encryption", func(t *testing.T) {
		pending := e
		pending.EvidenceID = uuid.NewString()
		pending.SourceRetentionUntil = time.Now().Add(-time.Second)
		service.Protector = sp04SealUnavailable{protector}
		if err := service.Capture(ctx, pending, "apps", retain); err == nil {
			t.Fatal("Transit outage committed")
		}
		service.Protector = protector
		if err := service.RecoverWithSources(ctx, tenant, nil); err != nil {
			t.Fatal(err)
		}
		var state, reason string
		if err := db.QueryRowContext(ctx, `SELECT m.replay_state,i.last_error FROM platform.evidence_metadata m JOIN platform.evidence_archive_intents i USING(tenant_id,evidence_id) WHERE tenant_id=$1 AND evidence_id=$2`, tenant, pending.EvidenceID).Scan(&state, &reason); err != nil || state != "unavailable" || reason != "source_expired_before_encryption" {
			t.Fatalf("expired source state=%s reason=%s error=%v", state, reason, err)
		}
		if _, _, err := service.Read(ctx, tenant, uuid.MustParse(pending.EvidenceID)); err == nil {
			t.Fatal("expired pending read claimed archive")
		}
	})

	t.Run("legal-hold-pagination-and-mutation-fencing", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			fact := e
			fact.EvidenceID = uuid.NewString()
			if err := service.Capture(ctx, fact, "apps", retain); err != nil {
				t.Fatal(err)
			}
			if err := persistence.WithTenantTx(ctx, pool, tenant, func(tx pgx.Tx) error {
				return evidence.SetLegalHold(ctx, tx, tenant, uuid.MustParse(fact.EvidenceID), true, "admin-fixture")
			}); err != nil {
				t.Fatal(err)
			}
		}
		apiPool, err := pgxpool.NewWithConfig(ctx, runtimePoolConfig(t, ctx, db, dsn, "api_runtime_role"))
		if err != nil {
			t.Fatal(err)
		}
		defer apiPool.Close()
		_, key, _ := ed25519.GenerateKey(rand.Reader)
		h := httpapi.SP04Handlers{Pool: apiPool, SigningKey: key}
		call := func(tid uuid.UUID, cursor string) (int, []byte) {
			r := httptest.NewRequest(http.MethodGet, "/api/v1/admin/legal-holds?limit=1&cursor="+cursor, nil)
			r = r.WithContext(auth.WithRequestContext(r.Context(), auth.RequestContext{TenantID: tid, Subject: "admin-fixture", Roles: []auth.Role{auth.PlatformAdmin}, RequestID: uuid.NewString()}))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			return w.Code, w.Body.Bytes()
		}
		status, raw := call(tenant, "")
		if status == 200 {
			if err := contract.Validate("https://ops.local/schemas/legal-hold-page/v1", raw); err != nil {
				t.Fatal(err)
			}
		}
		var first struct {
			Data []struct{ EvidenceID string }
			Meta struct{ NextCursor string }
		}
		if status != 200 || json.Unmarshal(raw, &first) != nil || len(first.Data) != 1 || first.Meta.NextCursor == "" {
			t.Fatalf("hold page status=%d %s", status, raw)
		}
		if status, _ := call(other, first.Meta.NextCursor); status != 409 {
			t.Fatal("foreign tenant reused hold cursor")
		}
		status, raw = call(tenant, first.Meta.NextCursor)
		if status != 200 {
			t.Fatalf("second hold page %d %s", status, raw)
		}
		if err := persistence.WithTenantTx(ctx, pool, tenant, func(tx pgx.Tx) error {
			return evidence.SetLegalHold(ctx, tx, tenant, uuid.MustParse(first.Data[0].EvidenceID), false, "admin-fixture")
		}); err != nil {
			t.Fatal(err)
		}
		if status, _ := call(tenant, first.Meta.NextCursor); status != 409 {
			t.Fatal("hold mutation did not invalidate cursor")
		}
	})

	t.Run("database-commit-response-lost", func(t *testing.T) {
		fact := e
		fact.EvidenceID = uuid.NewString()
		before := backend.puts
		fault := &sp04CommitFault{TxBeginner: pool}
		service.Pool = fault
		if err := service.Capture(ctx, fact, "apps", retain); err == nil {
			t.Fatal("lost commit response claimed success")
		}
		service.Pool = pool
		if !fault.fired {
			t.Fatal("metadata commit fault not exercised")
		}
		if err := service.Recover(ctx, tenant, uuid.MustParse(fact.EvidenceID)); err != nil {
			t.Fatal(err)
		}
		if backend.puts != before+1 {
			t.Fatal("uncertain database commit duplicated upload")
		}
		if _, _, err := service.Read(ctx, tenant, uuid.MustParse(fact.EvidenceID)); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("closure-active-incident-hold-race-and-delete-crash", func(t *testing.T) {
		a, b := e, e
		a.EvidenceID = uuid.NewString()
		b.EvidenceID = uuid.NewString()
		b.DerivationEvidenceRefs = []string{a.EvidenceID}
		for _, fact := range []evidence.Evidence{a, b} {
			if err := service.Capture(ctx, fact, "apps", retain); err != nil {
				t.Fatal(err)
			}
		}
		aid, bid := uuid.MustParse(a.EvidenceID), uuid.MustParse(b.EvidenceID)
		// Fault-fixture time travel: expire metadata, references AND fake backend
		// protection together. No live backend retention is weakened.
		expire := func(id uuid.UUID) {
			_, ref, err := service.Read(ctx, tenant, id)
			if err != nil {
				t.Fatal(err)
			}
			past := time.Now().Add(-time.Hour).UTC()
			ref.Object.RetainUntil = past
			raw, _ := json.Marshal(ref)
			for _, sql := range []string{`UPDATE platform.evidence_metadata SET retain_until=clock_timestamp()-interval '1 hour',legal_hold=false WHERE tenant_id=$1 AND evidence_id=$2`, `UPDATE platform.evidence_retention_references SET retain_until=clock_timestamp()-interval '1 hour',active=false WHERE tenant_id=$1 AND evidence_id=$2`} {
				if _, err := db.ExecContext(ctx, sql, tenant, id); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.ExecContext(ctx, `UPDATE platform.evidence_archive_intents SET object_ref=$3 WHERE tenant_id=$1 AND evidence_id=$2`, tenant, id, raw); err != nil {
				t.Fatal(err)
			}
			backend.mu.Lock()
			obj := backend.objects[ref.Object.Key]
			obj.Metadata["retain-until"] = past.Format(time.RFC3339Nano)
			obj.RetainUntil = past
			backend.objects[ref.Object.Key] = obj
			backend.mu.Unlock()
		}
		expire(aid)
		if err := service.Cleanup(ctx, tenant, aid); !errors.Is(err, evidence.ErrProtected) {
			t.Fatalf("live referrer closure bypassed: %v", err)
		}
		expire(bid)
		incident := uuid.New()
		if err := persistence.WithTenantTx(ctx, pool, tenant, func(tx pgx.Tx) error {
			return evidence.Protect(ctx, tx, tenant, aid, incident, "incident", time.Now().Add(-time.Second), true)
		}); err != nil {
			t.Fatal(err)
		}
		if err := service.Cleanup(ctx, tenant, aid); !errors.Is(err, evidence.ErrProtected) {
			t.Fatalf("active Incident bypassed: %v", err)
		}
		if err := persistence.WithTenantTx(ctx, pool, tenant, func(tx pgx.Tx) error {
			return evidence.Protect(ctx, tx, tenant, aid, incident, "incident", time.Now().Add(-time.Second), false)
		}); err != nil {
			t.Fatal(err)
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id',$1,true)`, tenant.String()); err != nil {
			t.Fatal(err)
		}
		if err := evidence.SetLegalHold(ctx, tx, tenant, aid, true, "race-admin"); err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() { result <- service.Cleanup(ctx, tenant, aid) }()
		select {
		case err := <-result:
			t.Fatalf("cleanup bypassed uncommitted Hold: %v", err)
		case <-time.After(30 * time.Millisecond):
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if err := <-result; !errors.Is(err, evidence.ErrProtected) {
			t.Fatalf("committed Hold lost race: %v", err)
		}
		expire(aid)
		backend.mu.Lock()
		backend.deleteStarted = make(chan struct{}, 1)
		backend.deleteResume = make(chan struct{})
		backend.deleteUncertain = true
		started, resume := backend.deleteStarted, backend.deleteResume
		backend.mu.Unlock()
		go func() { result <- service.Cleanup(ctx, tenant, aid) }()
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("delete intent not reached")
		}
		if err := persistence.WithTenantTx(ctx, pool, tenant, func(tx pgx.Tx) error { return evidence.SetLegalHold(ctx, tx, tenant, aid, true, "late-hold") }); err == nil {
			t.Fatal("late Hold accepted after deleting commit")
		}
		close(resume)
		if err := <-result; err == nil {
			t.Fatal("lost delete response claimed completion")
		}
		if _, _, err := service.Read(ctx, tenant, aid); err == nil {
			t.Fatal("deleting object still replayable")
		}
		backend.mu.Lock()
		backend.deleteStarted = nil
		backend.deleteResume = nil
		backend.mu.Unlock()
		if err := service.Cleanup(ctx, tenant, aid); err != nil {
			t.Fatal(err)
		}
		var status string
		if err := db.QueryRowContext(ctx, `SELECT status FROM platform.evidence_archive_intents WHERE tenant_id=$1 AND evidence_id=$2`, tenant, aid).Scan(&status); err != nil || status != "deleted" {
			t.Fatalf("delete recovery status=%s %v", status, err)
		}
	})

}

type sp04FaultTransit struct{}

func (sp04FaultTransit) TransitEncrypt(_ context.Context, _ string, plain, aad []byte) (openbao.TransitCiphertext, error) {
	return openbao.TransitCiphertext{Ciphertext: "vault:v1:" + base64.StdEncoding.EncodeToString(append(append([]byte{}, aad...), plain...)), KeyVersion: "1"}, nil
}
func (sp04FaultTransit) TransitDecrypt(_ context.Context, _ string, cipher string, aad []byte) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(cipher[len("vault:v1:"):])
	if err != nil || !bytes.HasPrefix(raw, aad) {
		return nil, errors.New("AAD mismatch")
	}
	return raw[len(aad):], nil
}

type sp04FaultBackend struct {
	mu              sync.Mutex
	objects         map[string]archive.StoredObject
	uncertain       bool
	puts            int
	deleteStarted   chan struct{}
	deleteResume    chan struct{}
	deleteUncertain bool
}

func (b *sp04FaultBackend) Put(_ context.Context, key string, body []byte, metadata map[string]string) (archive.Version, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.puts++
	b.objects[key] = archive.StoredObject{Body: append([]byte{}, body...), Metadata: metadata, VersionID: "version-1"}
	if b.uncertain {
		b.uncertain = false
		return archive.Version{}, errors.New("upload committed but response lost")
	}
	return archive.Version{VersionID: "version-1"}, nil
}
func (b *sp04FaultBackend) Get(_ context.Context, key, version string) (archive.StoredObject, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	v, ok := b.objects[key]
	if !ok {
		return v, archive.ErrObjectNotFound
	}
	return v, nil
}
func (b *sp04FaultBackend) Head(ctx context.Context, key, version string) (archive.StoredObject, error) {
	return b.Get(ctx, key, version)
}
func (b *sp04FaultBackend) Delete(_ context.Context, key, version string) error {
	b.mu.Lock()
	started, resume, uncertain := b.deleteStarted, b.deleteResume, b.deleteUncertain
	b.deleteUncertain = false
	b.mu.Unlock()
	if started != nil {
		started <- struct{}{}
	}
	if resume != nil {
		<-resume
	}
	b.mu.Lock()
	delete(b.objects, key)
	b.mu.Unlock()
	if uncertain {
		return errors.New("delete committed but response lost")
	}
	return nil
}

// Only encryption is fault injected; original metadata/intent use real PostgreSQL.
type sp04SealUnavailable struct{ protect.Protector }

func (p sp04SealUnavailable) Seal(context.Context, uuid.UUID, uuid.UUID, []byte) (protect.Envelope, error) {
	return protect.Envelope{}, errors.New("injected Transit unavailable")
}

type sp04ReplayAdapter struct {
	Expected evidence.Query
	Fact     evidence.Evidence
	Calls    int
}

func (a *sp04ReplayAdapter) Name() string { return "victoriametrics" }
func (a *sp04ReplayAdapter) Capabilities(context.Context) (evidence.CapabilitySet, error) {
	return evidence.CapabilitySet{}, nil
}
func (a *sp04ReplayAdapter) Query(_ context.Context, q evidence.Query) (evidence.Result, error) {
	a.Calls++
	if q.Template != a.Expected.Template || q.ResourceCanonicalID != a.Expected.ResourceCanonicalID || q.Namespace != a.Expected.Namespace || !q.From.Equal(a.Expected.From) || !q.To.Equal(a.Expected.To) || q.Limit != a.Expected.Limit || q.MaxBytes != a.Expected.MaxBytes || q.TimeoutMillis != a.Expected.TimeoutMillis || graph.ScopeDigest(q.Scope) != graph.ScopeDigest(a.Expected.Scope) {
		return evidence.Result{}, errors.New("original query parameters changed")
	}
	return evidence.Result{Partial: true, Evidence: []evidence.Evidence{a.Fact}, Warnings: []string{"another row unavailable"}}, nil
}

// Intercept only the final verified metadata transaction; all SQL/RLS/audit
// execute on the real database and Commit succeeds before its response is lost.
type sp04CommitFault struct {
	persistence.TxBeginner
	fired bool
}

func (f *sp04CommitFault) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := f.TxBeginner.Begin(ctx)
	return &sp04CommitFaultTx{Tx: tx, parent: f}, err
}

type sp04CommitFaultTx struct {
	pgx.Tx
	parent   *sp04CommitFault
	verified bool
}

func (tx *sp04CommitFaultTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if strings.Contains(sql, "evidence_archive_intents SET status='verified'") {
		tx.verified = true
	}
	return tx.Tx.Exec(ctx, sql, args...)
}
func (tx *sp04CommitFaultTx) Commit(ctx context.Context) error {
	err := tx.Tx.Commit(ctx)
	if err == nil && tx.verified && !tx.parent.fired {
		tx.parent.fired = true
		return errors.New("injected database commit response lost")
	}
	return err
}
