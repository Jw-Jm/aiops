package integration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"ops-platform/internal/datascope"
	"ops-platform/internal/evidence"
	"ops-platform/internal/finding"
	"ops-platform/internal/graph"
	"ops-platform/internal/persistence"
)

// These wrappers coordinate two real PostgreSQL transactions after their actual
// SQL has acquired its locks. They do not replace any query or returned value.
type evidenceLockPool struct {
	persistence.TxBeginner
	afterExec func(context.Context, string) error
}

func (p evidenceLockPool) Begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := p.TxBeginner.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return evidenceLockTx{Tx: tx, afterExec: p.afterExec}, nil
}

type evidenceLockTx struct {
	pgx.Tx
	afterExec func(context.Context, string) error
}

func (tx evidenceLockTx) Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	tag, err := tx.Tx.Exec(ctx, query, args...)
	if err == nil && tx.afterExec != nil {
		err = tx.afterExec(ctx, query)
	}
	return tag, err
}

func TestPreSP07FindingEvidenceReferenceAndLegalHoldLockOrder(t *testing.T) {
	ctx, db, pool, b := sp05Database(t)
	event := sp05Envelope(b, "retention-lock-event", "retention-lock-occurrence")
	data := []byte(`{"ready":false}`)
	id := uuid.New()
	e := evidence.Evidence{SchemaVersion: "evidence/v2", EvidenceID: id.String(), TenantID: b.TenantID.String(), ResourceCanonicalID: event.ResourceCanonicalID, Type: "resource_state", DataClass: "D0", SourceRegistrationID: b.SourceID.String(), SourceRevision: 1, SourceSystem: "kubernetes", BackendLogicalID: "sp05-native", QueryTemplateVersion: "sp05-signal/v1", QueryHash: finding.Hash("node-ready"), EffectiveScope: graph.Scope{Tenant: b.TenantID.String(), Cluster: b.ClusterUID, ClusterScoped: true, AuthorizationRevision: "fixture/v1"}, EvaluatedAt: event.ObservedAt, ObservedFrom: event.ObservedAt, ObservedTo: event.ObservedAt, SourceRetentionUntil: event.ObservedAt, ReplayState: "archive_pending", IndependenceGroup: "native-node", DerivationEvidenceRefs: []string{}, ContentDigest: evidence.Digest(data)}
	e.SourceScopeDigest = evidence.BindingScopeDigest(evidence.Binding{Revision: 1, BackendLogicalID: "sp05-native", SourceType: "kubernetes", ScopeMapping: datascope.Mapping{Scopes: map[string][]string{"cluster": {b.ClusterUID}, "namespace": {"apps"}}}})
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO platform.evidence_metadata(tenant_id,evidence_id,source_id,canonical_id,namespace,metadata,content_digest,replay_state,retain_until) VALUES($1,$2,$3,$4,'',$5,$6,'archive_pending',$7)`, b.TenantID, id, b.SourceID, event.ResourceCanonicalID, raw, e.ContentDigest, time.Now().Add(181*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	event.EvidenceRefs = []string{id.String()}
	raceCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	fkWritten, retentionAcquired := make(chan struct{}), make(chan struct{})
	findingPool := evidenceLockPool{TxBeginner: pool, afterExec: func(ctx context.Context, query string) error {
		if strings.HasPrefix(query, "INSERT INTO finding.evidence_refs") {
			close(fkWritten)
			select {
			case <-retentionAcquired:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}}
	holdPool := evidenceLockPool{TxBeginner: pool, afterExec: func(ctx context.Context, query string) error {
		if strings.Contains(query, "pg_advisory_xact_lock") && strings.Contains(query, ",180)") {
			select {
			case <-retentionAcquired:
			default:
				close(retentionAcquired)
			}
		}
		return nil
	}}
	findingDone := make(chan error, 1)
	go func() {
		_, _, err := (finding.Service{Pool: findingPool}).Ingest(raceCtx, b, event)
		findingDone <- err
	}()
	select {
	case <-fkWritten:
	case err := <-findingDone:
		t.Fatalf("finding did not acquire real Evidence FK lock: %v", err)
	case <-raceCtx.Done():
		t.Fatal(raceCtx.Err())
	}
	holdErr := persistence.WithTenantTx(raceCtx, holdPool, b.TenantID, func(tx pgx.Tx) error {
		return evidence.SetLegalHold(raceCtx, tx, b.TenantID, id, true, "retention-race-test")
	})
	findingErr := <-findingDone
	if holdErr != nil || findingErr != nil {
		t.Fatalf("real Finding/Legal Hold lock cycle: hold=%v finding=%v", holdErr, findingErr)
	}
	var held, deleting bool
	var digest string
	if err := db.QueryRowContext(ctx, `SELECT legal_hold,deleting,content_digest FROM platform.evidence_metadata WHERE tenant_id=$1 AND evidence_id=$2`, b.TenantID, id).Scan(&held, &deleting, &digest); err != nil || !held || deleting || digest != e.ContentDigest {
		t.Fatalf("Evidence identity or Hold lost: held=%t deleting=%t digestMatches=%t err=%v", held, deleting, digest == e.ContentDigest, err)
	}
	var refs, retained int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM finding.evidence_refs WHERE tenant_id=$1 AND evidence_id=$2`, b.TenantID, id).Scan(&refs); err != nil || refs != 1 {
		t.Fatalf("committed Finding reference: count=%d err=%v", refs, err)
	}
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM platform.evidence_retention_references WHERE tenant_id=$1 AND evidence_id=$2 AND retain_until>=clock_timestamp()+interval '364 days'`, b.TenantID, id).Scan(&retained); err != nil || retained != 2 {
		t.Fatalf("365-day Finding/Audit closure: count=%d err=%v", retained, err)
	}
}
