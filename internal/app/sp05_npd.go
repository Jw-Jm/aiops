package app

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/evidence"
	"ops-platform/internal/finding"
	"ops-platform/internal/graph"
	"ops-platform/internal/inspection/npd"
	"ops-platform/internal/persistence"
	"time"
)

// Only active Node symptoms trigger the fixed native log read. Other sources,
// free-form queries and commands are not admitted by this Inspector.
func sp05NPDPass(ctx context.Context, pool persistence.TxBeginner, archive *evidence.ArchiveService, cluster SP04Cluster, h graph.InternalHandler, sources []SP04Source, adapters map[string]evidence.Adapter, round int) error {
	tenant := uuid.MustParse(cluster.Tenant)
	ids := []string{}
	err := persistence.WithTenantTx(ctx, pool, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `WITH subjects AS (SELECT DISTINCT resource_canonical_id FROM incident.records WHERE tenant_id=$1 AND cluster_uid=$2 AND state IN('open','acknowledged','mitigating','suppressed') AND resource_canonical_id LIKE '%/Node/%'), numbered AS (SELECT resource_canonical_id,row_number() OVER(ORDER BY resource_canonical_id)-1 AS position,count(*) OVER() AS total FROM subjects) SELECT resource_canonical_id FROM numbered WHERE position=mod($3::bigint,total) LIMIT 1`, tenant, cluster.ClusterUID, round)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	})
	if err != nil {
		return err
	}
	scope, err := sp05WorkerGraphScope(ctx, pool, cluster)
	if err != nil {
		return err
	}
	if len(ids) > 0 {
		ids = []string{ids[round%len(ids)]}
	}
	var failures []error
	for _, source := range sources {
		if source.Name != "victorialogs" || source.Binding.Tenant != cluster.Tenant {
			continue
		}
		adapter, ok := adapters[source.Binding.SourceID]
		if !ok {
			continue
		}
		binding := source.Binding
		binding.SourceType = source.Name
		for _, id := range ids {
			failureCount := len(failures)
			to := time.Now().UTC().Truncate(30 * time.Second)
			result, err := adapter.Query(ctx, evidence.Query{ResourceCanonicalID: id, Template: "node-kernel-logs/v1", From: to.Add(-5 * time.Minute), To: to, Limit: 64, Scope: scope, MaxBytes: 64 << 10, TimeoutMillis: 3000})
			if err != nil || result.Partial || result.Freshness != "fresh" {
				failures = append(failures, evidence.ErrScopeUnverified)
				if h.Graph != nil {
					h.Graph.SetSourceDegraded(binding.SourceID+"/npd", "node_kernel_source_unavailable")
				}
				continue
			}
			for _, native := range result.Evidence {
				if err := SubmitSP05KernelEvidence(ctx, archive, binding, native); err != nil {
					failures = append(failures, err)
				}
			}
			if h.Graph != nil {
				reason := ""
				if len(failures) > failureCount {
					reason = "node_kernel_archive_or_ingestion_unavailable"
				}
				h.Graph.SetSourceDegraded(binding.SourceID+"/npd", reason)
			}
			if ctx.Err() != nil {
				return errors.Join(append(failures, ctx.Err())...)
			}
		}
	}
	return errors.Join(failures...)
}

// SubmitSP05KernelEvidence admits native fixed-template log results through the
// same archive and Finding ingestion path used by the polling Worker.
func SubmitSP05KernelEvidence(ctx context.Context, archive *evidence.ArchiveService, binding evidence.Binding, native evidence.Evidence) error {
	if native.SourceRegistrationID != binding.SourceID || native.TenantID != binding.Tenant || native.QueryTemplateVersion != "node-kernel-logs/v1" {
		return evidence.ErrArgument
	}
	identity := finding.Hash([]string{binding.SourceID, native.ResourceCanonicalID, native.QueryTemplateVersion, native.QueryHash, native.ContentDigest})
	native.EvidenceID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(identity)).String()
	native.IndependenceGroup = binding.SourceID + "/" + native.ResourceCanonicalID + "/kernel"
	candidates, err := npd.NativeKernelCandidates(native)
	if err != nil || len(candidates) == 0 {
		return err
	}
	if err := archive.Capture(ctx, native, "", time.Now().Add(181*24*time.Hour)); err != nil {
		return err
	}
	for _, candidate := range candidates {
		if err := SubmitSP05Candidate(ctx, archive, binding, candidate); err != nil {
			return err
		}
	}
	return nil
}
