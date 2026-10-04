package evidence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/graph"
	"ops-platform/internal/persistence"
)

type Repository struct{ Pool persistence.TxBeginner }

func (r Repository) Authorize(ctx context.Context, b Binding) error {
	id, err := uuid.Parse(b.Tenant)
	if err != nil {
		return ErrScopeUnverified
	}
	raw, _ := json.Marshal(b)
	return persistence.WithTenantTx(ctx, r.Pool, id, func(tx pgx.Tx) error {
		if err := checkBinding(ctx, tx, b); err != nil {
			return err
		}
		var valid bool
		err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform.source_registrations s JOIN platform.adapter_scope_verifications v USING(tenant_id,source_id) WHERE s.tenant_id=$1 AND s.source_id=$2 AND s.revision=$3 AND s.status='active' AND v.source_revision=s.revision AND v.binding_digest=$4 AND v.expires_at>clock_timestamp() AND v.adapter_version=$5)`, id, b.SourceID, b.Revision, Digest(raw), "sp04/"+b.SourceType+"/v1").Scan(&valid)
		if err != nil || !valid {
			return ErrScopeUnverified
		}
		return nil
	})
}
func (r Repository) Get(ctx context.Context, tenant, id uuid.UUID, scope graph.Scope) (Evidence, error) {
	var e Evidence
	err := persistence.WithTenantTx(ctx, r.Pool, tenant, func(tx pgx.Tx) error {
		var err error
		e, err = GetTx(ctx, tx, tenant, id, scope)
		return err
	})
	return e, err
}

// GetTx revalidates evidence and its current source binding inside the caller's authority-fenced transaction.
func GetTx(ctx context.Context, tx pgx.Tx, tenant, id uuid.UUID, scope graph.Scope) (e Evidence, err error) {
	var raw []byte
	var ns, state string
	var archiveRaw []byte
	err = tx.QueryRow(ctx, `SELECT m.metadata,m.namespace,m.replay_state,i.object_ref FROM platform.evidence_metadata m LEFT JOIN platform.evidence_archive_intents i USING(tenant_id,evidence_id) WHERE m.tenant_id=$1 AND m.evidence_id=$2 AND NOT m.deleting`, tenant, id).Scan(&raw, &ns, &state, &archiveRaw)
	if err != nil {
		return e, err
	}
	if json.Unmarshal(raw, &e) != nil {
		return e, errors.New("invalid evidence metadata")
	}
	digest, err := evidenceSourceDigest(ctx, tx, e)
	if err != nil || e.SourceScopeDigest == "" || e.SourceScopeDigest != digest {
		return e, ErrScopeUnverified
	}
	e.ReplayState = state
	if state == "archived_verified" {
		var ref ArchiveRef
		if json.Unmarshal(archiveRaw, &ref) != nil {
			return e, errors.New("invalid archive metadata")
		}
		e.ArchiveRef = &ref
	}
	if !scope.Allows(e.ResourceCanonicalID, ns) {
		return e, graph.ErrScope
	}
	return e, nil
}

// CheckBinding is also used before source isolation verification. Declarative
// config is accepted only while it exactly matches an active registration.
func (r Repository) CheckBinding(ctx context.Context, b Binding) error {
	tenant, err := uuid.Parse(b.Tenant)
	if err != nil {
		return ErrScopeUnverified
	}
	return persistence.WithTenantTx(ctx, r.Pool, tenant, func(tx pgx.Tx) error { return checkBinding(ctx, tx, b) })
}
func checkBinding(ctx context.Context, tx pgx.Tx, b Binding) error {
	mapping, err := json.Marshal(b.ScopeMapping)
	if err != nil {
		return err
	}
	var valid bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM platform.source_registrations s JOIN platform.tenants t USING(tenant_id) LEFT JOIN platform.cluster_registrations c USING(tenant_id,cluster_id) WHERE s.tenant_id=$1 AND s.source_id=$2 AND s.revision=$3 AND s.status='active' AND t.status='active' AND s.source_type=$4 AND s.backend_logical_id=$5 AND s.data_scope_mapping=$6::jsonb AND (c.cluster_id IS NULL OR c.status='active'))`, b.Tenant, b.SourceID, b.Revision, b.SourceType, b.BackendLogicalID, mapping).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return ErrScopeUnverified
	}
	return nil
}

// VerifyVictoria runs the actual bounded adapter against a known positive
// canary and verifies rejected caller expansions. Empty data is not a proof.
func (r Repository) VerifyVictoria(ctx context.Context, a *Victoria, probe Query) error {
	b := a.binding
	if err := r.CheckBinding(ctx, b); err != nil {
		return err
	}
	candidate := *a
	candidate.authorize = r.CheckBinding
	result, err := candidate.Query(ctx, probe)
	if err != nil || result.Partial || len(result.Evidence) == 0 {
		return fmt.Errorf("%w: canary error=%v partial=%t evidence=%d warnings=%v", ErrScopeUnverified, err, result.Partial, len(result.Evidence), result.Warnings)
	}
	for _, template := range []string{"labels", "series", "topk(5,up)", `resource-logs/v1 OR *`, `pod-phase/v1{tenant=~".*"}`} {
		bad := probe
		bad.Template = template
		if _, err := candidate.Query(ctx, bad); err == nil {
			return ErrScopeUnverified
		}
	}
	bad := probe
	bad.Namespace = "__sp04_ungranted_namespace__"
	if _, err := candidate.Query(ctx, bad); err == nil {
		return ErrScopeUnverified
	}
	bad = probe
	bad.Scope.Tenant = "__sp04_other_tenant__"
	if _, err := candidate.Query(ctx, bad); err == nil {
		return ErrScopeUnverified
	}
	raw, _ := json.Marshal(b)
	report, _ := json.Marshal(map[string]any{"schemaVersion": "source-isolation-proof/v1", "bindingDigest": Digest(raw), "queryHash": result.QueryHash, "contentDigest": result.Evidence[0].ContentDigest, "adapterVersion": "sp04/" + b.SourceType + "/v1", "negativeCallerExpansions": 7, "positiveCanary": true})
	tenant := uuid.MustParse(b.Tenant)
	return persistence.WithTenantTx(ctx, r.Pool, tenant, func(tx pgx.Tx) error {
		if err := checkBinding(ctx, tx, b); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO platform.adapter_scope_verifications(tenant_id,source_id,source_revision,binding_digest,adapter_version,verified_at,expires_at,report_digest) VALUES($1,$2,$3,$4,$5,clock_timestamp(),clock_timestamp()+interval '24 hours',$6) ON CONFLICT(tenant_id,source_id,source_revision,binding_digest) DO UPDATE SET verified_at=EXCLUDED.verified_at,expires_at=EXCLUDED.expires_at,report_digest=EXCLUDED.report_digest,adapter_version=EXCLUDED.adapter_version`, tenant, b.SourceID, b.Revision, Digest(raw), "sp04/"+b.SourceType+"/v1", Digest(report))
		return err
	})
}

// VerifyAdapter records only the bounded, nonempty current adapter contract.
// Its caller supplies a candidate using CheckBinding solely for verification;
// runtime adapters always use Authorize and cannot self-grant query permission.
func (r Repository) VerifyAdapter(ctx context.Context, b Binding, candidate Adapter, probe Query) error {
	if candidate == nil || candidate.Name() != b.SourceType || (b.SourceType != "deepflow" && b.SourceType != "redfish") {
		return ErrScopeUnverified
	}
	if err := r.CheckBinding(ctx, b); err != nil {
		return err
	}
	result, err := candidate.Query(ctx, probe)
	if err != nil || len(result.Evidence) == 0 || result.Freshness == "unavailable" {
		return ErrScopeUnverified
	}
	for _, e := range result.Evidence {
		if e.TenantID != b.Tenant || e.SourceRegistrationID != b.SourceID || e.SourceRevision != b.Revision || !probe.Scope.Allows(e.ResourceCanonicalID, probe.Namespace) {
			return ErrScopeUnverified
		}
	}
	capabilities, err := candidate.Capabilities(ctx)
	if err != nil {
		return err
	}
	if b.SourceType == "deepflow" && (capabilities["trace"] != "CAPABILITY_DISABLED" || (capabilities["network"] != "fixture_only" && capabilities["network"] != "live")) {
		return ErrScopeUnverified
	}
	bindingRaw, _ := json.Marshal(b)
	report, _ := json.Marshal(map[string]any{"schemaVersion": "source-isolation-proof/v1", "queryHash": result.QueryHash, "contentDigest": result.Evidence[0].ContentDigest, "capabilities": capabilities, "partial": result.Partial, "warnings": result.Warnings})
	tenant := uuid.MustParse(b.Tenant)
	return persistence.WithTenantTx(ctx, r.Pool, tenant, func(tx pgx.Tx) error {
		if err := checkBinding(ctx, tx, b); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO platform.adapter_scope_verifications(tenant_id,source_id,source_revision,binding_digest,adapter_version,verified_at,expires_at,report_digest) VALUES($1,$2,$3,$4,$5,clock_timestamp(),clock_timestamp()+interval '1 hour',$6) ON CONFLICT(tenant_id,source_id,source_revision,binding_digest) DO UPDATE SET verified_at=EXCLUDED.verified_at,expires_at=EXCLUDED.expires_at,report_digest=EXCLUDED.report_digest,adapter_version=EXCLUDED.adapter_version`, tenant, b.SourceID, b.Revision, Digest(bindingRaw), "sp04/"+b.SourceType+"/v1", Digest(report))
		return err
	})
}
