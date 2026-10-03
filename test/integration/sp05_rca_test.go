package integration

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"ops-platform/internal/auth"
	"ops-platform/internal/configregistry"
	"ops-platform/internal/datascope"
	"ops-platform/internal/evidence"
	"ops-platform/internal/finding"
	"ops-platform/internal/graph"
	"ops-platform/internal/incident"
	"ops-platform/internal/persistence"
	"ops-platform/internal/policy"
	"ops-platform/internal/rca"
	"testing"
	"time"
)

func TestSP05RCARegistryImmutableEvidenceAppendOnlyAndReplayRevocation(t *testing.T) {
	ctx, db, pool, b := sp05Database(t)
	fs := finding.Service{Pool: pool}
	f, _, err := fs.Ingest(ctx, b, sp05Envelope(b, "rca-event", "rca-occurrence"))
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
	var i incident.Incident
	if err := persistence.WithTenantTx(ctx, pool, b.TenantID, func(tx pgx.Tx) error {
		var err error
		i, err = incident.Load(ctx, tx, b.TenantID, id, false)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	recipe, _ := rca.Builtin("node-failure")
	clock := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	input := rca.Input{ResourceCanonicalID: f.ResourceCanonicalID, From: clock.Add(-time.Minute), To: clock, EvaluatedAt: clock, Graph: graph.Result{Partial: true, Freshness: "unavailable", DegradedSources: []string{"graph"}}}
	repo := rca.Repository{Pool: pool}
	if _, err := repo.Commit(ctx, b.TenantID, id, "deterministic-worker", "no-registry", i.Revision, 0, nil, recipe, input); !errors.Is(err, rca.ErrRecipe) {
		t.Fatalf("unpublished recipe accepted: %v", err)
	}
	apiPool, err := pgxpool.NewWithConfig(ctx, runtimePoolConfig(t, ctx, db, pool.Config().ConnConfig.ConnString(), "api_runtime_role"))
	if err != nil {
		t.Fatal(err)
	}
	defer apiPool.Close()
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.role_bindings(tenant_id,binding_id,subject,role_name) VALUES($1,$2,'sp05-rca-admin','platform_admin')`, b.TenantID, uuid.New()); err != nil {
		t.Fatal(err)
	}
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	trust := configregistry.Ed25519TrustStore{Keys: map[string]ed25519.PublicKey{"integration-key": pub}}
	compiler, err := policy.NewBundleCompiler(trust)
	if err != nil {
		t.Fatal(err)
	}
	service, err := configregistry.NewService(apiPool, trust, compiler)
	if err != nil {
		t.Fatal(err)
	}
	actor := auth.RequestContext{TenantID: b.TenantID, Subject: "sp05-rca-admin", Roles: []auth.Role{auth.PlatformAdmin}}
	content, _ := json.Marshal(recipe)
	draft, err := createRegistryDraft(ctx, apiPool, service, actor, configregistry.DraftCommand{Kind: configregistry.KindRecipe, LogicalName: recipe.Name, Content: content})
	if err != nil {
		t.Fatal(err)
	}
	version, err := signAndPublish(ctx, apiPool, service, actor, draft, 1, priv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := activateRegistryVersion(ctx, apiPool, service, actor, version, configregistry.KindRecipe, recipe.Name, configregistry.Scope{Type: configregistry.ScopeTenant}, 0); err != nil {
		t.Fatal(err)
	}
	repo.Trust = trust
	wrongPrimary := input
	wrongPrimary.ResourceCanonicalID = sp05Envelope(b, "other-primary", "other-primary").ResourceCanonicalID + "-other"
	if _, err := repo.Commit(ctx, b.TenantID, id, "worker", "wrong-primary", i.Revision, 0, &version.VersionID, recipe, wrongPrimary); !errors.Is(err, rca.ErrStale) {
		t.Fatalf("incident primary binding bypassed: %v", err)
	}
	data := []byte(`{"ready":false}`)
	e := evidence.Evidence{SchemaVersion: "evidence/v2", EvidenceID: uuid.NewString(), TenantID: b.TenantID.String(), ResourceCanonicalID: f.ResourceCanonicalID, Type: "resource_state", DataClass: "D0", SourceRegistrationID: b.SourceID.String(), SourceRevision: 1, SourceSystem: "kubernetes", BackendLogicalID: "sp05-native", QueryTemplateVersion: "sp05-signal/v1", QueryHash: finding.Hash("node-ready"), EffectiveScope: graph.Scope{Tenant: b.TenantID.String(), Cluster: b.ClusterUID, ClusterScoped: true, AuthorizationRevision: "fixture/v1"}, EvaluatedAt: clock, ObservedFrom: clock, ObservedTo: clock, SourceRetentionUntil: clock, TimeReliable: false, ReplayState: "archive_pending", IndependenceGroup: "native-node", DerivationEvidenceRefs: []string{}, ContentDigest: evidence.Digest(data), Data: data}
	e.SourceScopeDigest = evidence.BindingScopeDigest(evidence.Binding{Revision: 1, BackendLogicalID: "sp05-native", SourceType: "kubernetes", ScopeMapping: datascope.Mapping{Scopes: map[string][]string{"cluster": {b.ClusterUID}, "namespace": {"apps"}}}})
	raw, _ := json.Marshal(e)
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.evidence_metadata(tenant_id,evidence_id,source_id,canonical_id,namespace,metadata,content_digest,replay_state,retain_until) VALUES($1,$2,$3,$4,'',$5,$6,'archive_pending',$7)`, b.TenantID, e.EvidenceID, b.SourceID, e.ResourceCanonicalID, raw, e.ContentDigest, time.Now().Add(181*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	input.Evidence = []evidence.Evidence{e}
	input.Evidence[0].TimeReliable = true
	if _, err := repo.Commit(ctx, b.TenantID, id, "deterministic-worker", "forged-metadata", i.Revision, 0, &version.VersionID, recipe, input); !errors.Is(err, rca.ErrStale) {
		t.Fatalf("forged time reliability accepted: %v", err)
	}
	input.Evidence[0] = e
	first, err := repo.Commit(ctx, b.TenantID, id, "deterministic-worker", "stable-evaluation", i.Revision, 0, &version.VersionID, recipe, input)
	if err != nil {
		t.Fatal(err)
	}
	if first.Result.Status != "unresolved" || !first.Result.Partial {
		t.Fatal("archive pending/missing graph incorrectly confirmed")
	}
	input.EvaluatedAt = input.EvaluatedAt.Add(time.Second)
	retry, err := repo.Commit(ctx, b.TenantID, id, "deterministic-worker", "stable-evaluation", i.Revision, 0, &version.VersionID, recipe, input)
	if err != nil || retry.Revision != first.Revision {
		t.Fatalf("crash/retry appended duplicate: %d %v", retry.Revision, err)
	}
	historical, err := repo.Commit(ctx, b.TenantID, id, "deterministic-worker", "stale-base", i.Revision-1, first.Revision, &version.VersionID, recipe, input)
	if err != nil || !historical.Superseded {
		t.Fatalf("stale base not historical: %+v %v", historical, err)
	}
	var current int64
	if err := db.QueryRowContext(ctx, `SELECT current_rca_revision FROM incident.records WHERE incident_id=$1`, id).Scan(&current); err != nil || current != first.Revision {
		t.Fatal("stale evaluation overwrote current")
	}
	var refs int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM platform.evidence_retention_references WHERE tenant_id=$1 AND evidence_id=$2 AND retain_until>=clock_timestamp()+interval '364 days'`, b.TenantID, e.EvidenceID).Scan(&refs); err != nil || refs < 3 {
		t.Fatalf("365-day closure incomplete: %d %v", refs, err)
	}
	if err := persistence.WithTenantTx(ctx, pool, b.TenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE incident.rca_revisions SET actor='overwrite' WHERE tenant_id=$1`, b.TenantID)
		return err
	}); err == nil {
		t.Fatal("RCA revisions are mutable")
	}
	if _, err := db.ExecContext(ctx, `UPDATE platform.source_registrations SET status='disabled',revision=revision+1 WHERE source_id=$1`, b.SourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Commit(ctx, b.TenantID, id, "deterministic-worker", "stable-evaluation", i.Revision, first.Revision, &version.VersionID, recipe, input); !errors.Is(err, rca.ErrStale) {
		t.Fatalf("revoked source bypassed replay fence: %v", err)
	}
}
