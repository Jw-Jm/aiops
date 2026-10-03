package integration

import (
	"context"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/persistence"
	"time"

	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http/httptest"
	"ops-platform/internal/app"
	"ops-platform/internal/auth"
	"ops-platform/internal/configregistry"
	"ops-platform/internal/evidence"
	"ops-platform/internal/finding"
	"ops-platform/internal/graph"
	"ops-platform/internal/httpapi"
	"ops-platform/internal/incident"
	"ops-platform/internal/rca"
	"ops-platform/internal/source"
	"testing"
)

func TestSP05GoldenGraphOnlySourceWithdrawAndFindingRevisionFence(t *testing.T) {
	sp05GoldenVertical(t, "dimm-failure", "valid", true)
}
func scopeForGolden(in sp05GoldenInput) graph.Scope {
	return graph.Scope{Tenant: in.Tenant, Cluster: in.Cluster, ClusterScoped: true, Namespaces: []string{in.Namespace}, AuthorizationRevision: "sp05-worker/" + in.Sources["kubernetes"]}
}
func sp05GoldenReviewFences(t *testing.T, ctx context.Context, db *sql.DB, pool *pgxpool.Pool, archive *evidence.ArchiveService, registry *configregistry.Service, trust configregistry.Ed25519TrustStore, g *graph.Graph, cluster app.SP04Cluster, b source.BoundSourceContext, scope graph.Scope, primary string) {
	t.Helper()
	_ = registry
	var raw []byte
	var versionID uuid.UUID
	if err := db.QueryRowContext(ctx, `SELECT r.result,r.recipe_version_id FROM incident.rca_revisions r JOIN incident.records i USING(tenant_id,incident_id) WHERE i.tenant_id=$1 AND i.resource_canonical_id=$2 AND r.revision=i.current_rca_revision`, b.TenantID, primary).Scan(&raw, &versionID); err != nil {
		t.Fatal(err)
	}
	var stored rca.Revision
	if json.Unmarshal(raw, &stored) != nil || stored.Result.Status != "confirmed" {
		t.Fatal("review fixture is not actual confirmed DIMM")
	}
	recipe, _ := rca.Builtin("dimm-failure")
	repo := rca.Repository{Pool: pool, Trust: trust, Archive: archive, Graph: g, GraphScope: scope}
	for _, e := range stored.InputManifest.Evidence {
		if e.SourceRegistrationID == b.SourceID.String() {
			t.Fatal("fixture does not exercise Graph-only source")
		}
	}
	// Facts intentionally do not live in the public persisted metadata manifest.
	// Exercise the production commit's confirmed branch with authenticated reads.
	for n, e := range stored.InputManifest.Evidence {
		data, ref, err := archive.Read(ctx, b.TenantID, uuid.MustParse(e.EvidenceID))
		if err != nil {
			t.Fatal(err)
		}
		stored.InputManifest.Evidence[n].Data = data
		stored.InputManifest.Evidence[n].ArchiveRef = &ref
	}
	if preview, err := rca.Evaluate(recipe, stored.InputManifest); err != nil || preview.Status != "confirmed" {
		t.Fatal("review must exercise actual confirmed branch", err)
	}
	for _, mutation := range []string{`UPDATE platform.source_registrations SET status='disabled' WHERE source_id=$1`, `UPDATE platform.source_registrations SET revision=revision+1 WHERE source_id=$1`} {
		if _, err := db.ExecContext(ctx, mutation, b.SourceID); err != nil {
			t.Fatal(err)
		}
		_, err := repo.Commit(ctx, b.TenantID, stored.IncidentID, "review-worker", uuid.NewString(), stored.BaseIncidentRevision, stored.Revision, &versionID, recipe, stored.InputManifest)
		if !errors.Is(err, rca.ErrStale) {
			t.Fatalf("withdrawn Graph-only authority still confirmed: %v", err)
		}
		h := httpapi.SP05Handlers{Pool: pool, Enabled: true}
		request := httptest.NewRequest("GET", "/api/v1/incidents/"+stored.IncidentID+"/rca/revisions/1", nil).WithContext(auth.WithRequestContext(ctx, auth.RequestContext{TenantID: b.TenantID, Subject: "golden-admin", Roles: []auth.Role{auth.PlatformAdmin}, RequestID: uuid.NewString()}))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, request)
		if w.Code != 403 {
			t.Fatalf("historical Graph-only withdrawal leaked: %d", w.Code)
		}
		if _, err := db.ExecContext(ctx, `UPDATE platform.source_registrations SET status='active',revision=1 WHERE source_id=$1`, b.SourceID); err != nil {
			t.Fatal(err)
		}
	}
	// Advance a real linked Finding, preserving its exact Evidence references. The
	// prior frozen manifest may not publish again while correlation is pending.
	var findingPayload []byte
	if err := db.QueryRowContext(ctx, `SELECT payload FROM finding.records WHERE finding_id=$1`, stored.InputManifest.FindingRevisions[0].FindingID).Scan(&findingPayload); err != nil {
		t.Fatal(err)
	}
	var f finding.Finding
	if json.Unmarshal(findingPayload, &f) != nil {
		t.Fatal("finding decode")
	}
	bound := b
	bound.SourceID = uuid.MustParse(f.SourceRegistrationID)
	e := f.Envelope
	e.EventID = uuid.NewString()
	e.IdempotencyKey = e.EventID
	e.SourceSequence++
	e.ObservedAt = e.ObservedAt.Add(1)
	e.PayloadDigest = ""
	fs := finding.Service{Pool: pool}
	if _, _, err := fs.Ingest(ctx, bound, e); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Commit(ctx, b.TenantID, stored.IncidentID, "review-worker", "pending-finding", stored.BaseIncidentRevision, stored.Revision, &versionID, recipe, stored.InputManifest); !errors.Is(err, rca.ErrStale) {
		t.Fatalf("unconsumed Finding revision was not fenced: %v", err)
	}
	if err := fs.RelayPass(ctx, b.TenantID, incident.Consume, 50); err != nil {
		t.Fatal(err)
	}
	input := stored.InputManifest
	var err error
	input.FindingRevisions, err = repo.FreezeFindings(ctx, b.TenantID, stored.IncidentID)
	if err != nil {
		t.Fatal(err)
	}
	if rca.InputDigest(input, stored.Result) == stored.InputDigest {
		t.Fatal("new Finding snapshot does not change RCA digest")
	}
	var base int64
	if err := db.QueryRowContext(ctx, `SELECT revision FROM incident.records WHERE incident_id=$1`, stored.IncidentID).Scan(&base); err != nil {
		t.Fatal(err)
	}
	next, err := repo.Commit(ctx, b.TenantID, stored.IncidentID, "review-worker", "new-finding", base, stored.Revision, &versionID, recipe, input)
	if err != nil || next.Revision != stored.Revision+1 {
		t.Fatalf("changed same-Evidence input did not append: %v", err)
	}
	old, err := repo.Read(ctx, b.TenantID, stored.IncidentID, stored.Revision)
	if err != nil || old.InputDigest != stored.InputDigest || old.InputManifest.FindingRevisions[0].AggregateRevision != stored.InputManifest.FindingRevisions[0].AggregateRevision {
		t.Fatal("historical Finding input changed", err)
	}
	t.Log("actual archive/Graph confirmed DIMM: Graph-only revoke/rotate is fenced, historical read denied; unapplied Finding revision rejected, same Evidence with new Finding snapshot appends, old input remains immutable")
}

// This observes a real PostgreSQL transaction lock; no latency or throughput
// criterion is used. Only this test's private source registration is mutated.
func TestSP05GraphSourceAuthoritySerializesRevocation(t *testing.T) {
	ctx, db, pool, b := sp05Database(t)
	var raw []byte
	binding := evidence.Binding{Tenant: b.TenantID.String(), SourceID: b.SourceID.String(), Revision: 1}
	if err := db.QueryRowContext(ctx, `SELECT source_type,backend_logical_id,data_scope_mapping FROM platform.source_registrations WHERE source_id=$1`, b.SourceID).Scan(&binding.SourceType, &binding.BackendLogicalID, &raw); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &binding.ScopeMapping); err != nil {
		t.Fatal(err)
	}
	authorities := []graph.SourceAuthority{{SourceRegistrationID: b.SourceID.String(), Revision: 1, ScopeDigest: evidence.BindingScopeDigest(binding)}}
	result := graph.Result{}
	acquired, release, lockedDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		lockedDone <- persistence.WithTenantTx(ctx, pool, b.TenantID, func(tx pgx.Tx) error {
			if err := rca.CheckGraphSources(ctx, tx, b.TenantID, authorities, result); err != nil {
				return err
			}
			close(acquired)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	select {
	case <-acquired:
	case err := <-lockedDone:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	revokeDone := make(chan error, 1)
	marker := "sp05_owned_revoke_" + uuid.NewString()
	go func() {
		_, err := db.ExecContext(ctx, `UPDATE platform.source_registrations SET status='disabled' WHERE source_id=$1 /* `+marker+` */`, b.SourceID)
		revokeDone <- err
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var blocked bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND query LIKE $1 AND wait_event_type='Lock' AND wait_event='transactionid')`, "%"+marker+"%").Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case err := <-revokeDone:
			t.Fatal("revocation escaped confirmed transaction fence", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("did not observe actual source-row revocation lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(release)
	released = true
	if err := <-lockedDone; err != nil {
		t.Fatal(err)
	}
	if err := <-revokeDone; err != nil {
		t.Fatal(err)
	}
	err := persistence.WithTenantTx(ctx, pool, b.TenantID, func(tx pgx.Tx) error { return rca.CheckGraphSources(ctx, tx, b.TenantID, authorities, result) })
	if !errors.Is(err, rca.ErrStale) {
		t.Fatal("committed revocation not enforced", err)
	}
	t.Log("actual Graph-source share lock blocked concurrent revocation until RCA transaction committed; next confirmation rejected")
}
