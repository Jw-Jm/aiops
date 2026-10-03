package app

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"ops-platform/internal/configregistry"
	"ops-platform/internal/datascope"
	"ops-platform/internal/evidence"
	"ops-platform/internal/finding"
	"ops-platform/internal/graph"
	"ops-platform/internal/incident"
	"ops-platform/internal/observability"
	"ops-platform/internal/persistence"
	"ops-platform/internal/policy"
	"ops-platform/internal/rca"
	"ops-platform/internal/resource"
	"os"
	"sync"
	"time"
)

func startSP05Worker(ctx context.Context, pool *pgxpool.Pool, archive *evidence.ArchiveService, c *SP04Config, handlers map[string]graph.InternalHandler, adapters map[string]evidence.Adapter, runtime *observability.Runtime, group *sync.WaitGroup) error {
	if c.SP05 == nil || !c.SP05.Enabled {
		return nil
	}
	trust, err := RegistryTrustFromFile(os.Getenv("PLATFORM_REGISTRY_TRUST_FILE"))
	if err != nil {
		return err
	}
	compiler, err := policy.NewBundleCompiler(trust)
	if err != nil {
		return err
	}
	registry, err := configregistry.NewService(pool, trust, compiler)
	if err != nil {
		return err
	}
	for _, cluster := range c.Clusters {
		cluster := cluster
		startSP05Analyzer(ctx, archive, cluster, handlers[cluster.Tenant+"|"+cluster.ClusterUID], c.SP05.Analyzer, runtime, group)
		group.Add(1)
		go func() {
			defer group.Done()
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			round := 0
			for ctx.Err() == nil {
				pass, cancel := context.WithTimeout(ctx, 15*time.Second)
				tenant := uuid.MustParse(cluster.Tenant)
				relayErr := (finding.Service{Pool: pool}).RelayPass(pass, tenant, incident.Consume, 50)
				npdCtx, stopNPD := context.WithTimeout(pass, 4*time.Second)
				npdErr := sp05NPDPass(npdCtx, pool, archive, cluster, handlers[cluster.Tenant+"|"+cluster.ClusterUID], c.Sources, adapters, round)
				stopNPD()
				round++
				recoveryErr := (incident.Service{Pool: pool}).RecoveryPass(pass, tenant, func(uid string) bool {
					h, ok := handlers[cluster.Tenant+"|"+uid]
					return ok && h.Graph != nil && h.Graph.Qualified(time.Now())
				})
				rcaErr := sp05RCAPass(pass, pool, archive, cluster, handlers[cluster.Tenant+"|"+cluster.ClusterUID], registry, trust)
				err := errors.Join(relayErr, npdErr, recoveryErr, rcaErr)
				cancel()
				runtime.Metrics.SetComponentDegraded("sp05", err != nil)
				if err != nil {
					runtime.Logger.WarnContext(ctx, "SP05 durable reducer pass unavailable", "errorClass", collectionErrorClass(err))
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
	}
	return nil
}
func sp05RCAPass(ctx context.Context, pool persistence.TxBeginner, archive *evidence.ArchiveService, cluster SP04Cluster, h graph.InternalHandler, registry *configregistry.Service, trust configregistry.Ed25519TrustStore) error {
	return (SP05Reducer{Pool: pool, Archive: archive, Cluster: cluster, Handler: h, Registry: registry, Trust: trust}).Pass(ctx)
}

// SP05Reducer is the existing Worker's deterministic reduction entry point.
// Its optional trusted constructor clock supports immutable protocol Fixtures;
// it is not exposed through runtime JSON, environment or HTTP request fields.
type SP05Reducer struct {
	Pool     persistence.TxBeginner
	Archive  *evidence.ArchiveService
	Cluster  SP04Cluster
	Handler  graph.InternalHandler
	Registry *configregistry.Service
	Trust    configregistry.Ed25519TrustStore
	Clock    func() time.Time
}

func (r SP05Reducer) Pass(ctx context.Context) error {
	pool, archive, cluster, h, registry, trust := r.Pool, r.Archive, r.Cluster, r.Handler, r.Registry, r.Trust
	clock := r.Clock
	if clock == nil {
		clock = time.Now
	}
	tenant := uuid.MustParse(cluster.Tenant)
	items := []incident.Incident{}
	err := persistence.WithTenantTx(ctx, pool, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT incident_id FROM incident.records WHERE tenant_id=$1 AND cluster_uid=$2 AND state<>'closed' AND (last_rca_checked_at IS NULL OR last_rca_checked_at<clock_timestamp()-interval '30 seconds' OR EXISTS(SELECT 1 FROM incident.outbox o WHERE o.tenant_id=records.tenant_id AND o.incident_id=records.incident_id AND o.state='pending')) ORDER BY COALESCE(last_rca_checked_at,created_at),incident_id LIMIT 20`, tenant, cluster.ClusterUID)
		if err != nil {
			return err
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, id := range ids {
			i, err := incident.Load(ctx, tx, tenant, id, false)
			if err != nil {
				return err
			}
			items = append(items, i)
		}
		return nil
	})
	if err != nil {
		return err
	}
	var failures []error
	for _, i := range items {
		if err := sp05RCAIncident(ctx, pool, archive, cluster, h, registry, trust, i, clock().UTC()); err != nil {
			failures = append(failures, err)
		}
		if ctx.Err() != nil {
			break
		}
	}
	return errors.Join(failures...)
}
func sp05RCAIncident(ctx context.Context, pool persistence.TxBeginner, archive *evidence.ArchiveService, cluster SP04Cluster, h graph.InternalHandler, registry *configregistry.Service, trust configregistry.Ed25519TrustStore, i incident.Incident, now time.Time) error {
	tenant := uuid.MustParse(cluster.Tenant)

	recipeName := "node-failure"
	if id, err := resourceKind(i.ResourceCanonicalID); err == nil {
		switch id {
		case "PersistentVolumeClaim", "PersistentVolume", "StorageClass":
			recipeName = "pvc-csi-failure"
		case "DIMM", "PhysicalServer":
			recipeName = "dimm-failure"
		}
	}
	clusterID, err := clusterContextID(ctx, pool, tenant, cluster.ClusterUID)
	if err != nil {
		return err
	}
	scope := configregistry.Scope{Type: configregistry.ScopeCluster, ClusterID: clusterID}
	if i.Namespace != "" {
		scope.Type = configregistry.ScopeNamespace
		scope.Namespace = i.Namespace
	}
	// Recipe activation is processing-time authorization, independently of the
	// immutable native input/evaluation clock used by protocol Fixtures.
	version, err := registry.ResolveActive(ctx, tenant, configregistry.KindRecipe, recipeName, scope, time.Now())
	if err != nil {
		return rca.ErrRecipe
	}
	message, digest, err := configregistry.SigningPayload(tenant, configregistry.KindRecipe, recipeName, version.Content)
	if err != nil || digest != version.Digest || trust.Verify(ctx, version.SignerKeyID, message, version.Signature) != nil {
		return rca.ErrRecipe
	}
	recipe, err := rca.DecodeRecipe(version.Content)
	if err != nil {
		return err
	}
	graphScope, err := sp05WorkerGraphScope(ctx, pool, cluster)
	if err != nil {
		return err
	}
	input := rca.Input{ResourceCanonicalID: i.ResourceCanonicalID, From: now.Add(-5 * time.Minute), To: now, EvaluatedAt: now, Evidence: []evidence.Evidence{}, Graph: graph.Result{SchemaVersion: "rca-graph-unavailable/v2", Freshness: "unavailable", Partial: true, DegradedSources: []string{"graph"}, Nodes: []resource.Entity{}, Edges: []resource.Relation{}}}
	input.SchemaVersion = "rca-input/v2"
	input.FindingRevisions, err = (rca.Repository{Pool: pool}).FreezeFindings(ctx, tenant, i.IncidentID)
	if err != nil {
		return err
	}
	input.GraphSources = []graph.SourceAuthority{}
	q := graph.Query{CanonicalID: i.ResourceCanonicalID, Scope: graphScope}
	result, queryErr := rca.QueryImpact(ctx, h.Graph, recipe, q)
	if queryErr == nil {
		input.Graph = result
		input.GraphSources = rca.SelectGraphSources(h.SourceAuthorities, result)
	}
	refs := []string{}
	entities := []string{i.ResourceCanonicalID}
	for _, node := range input.Graph.Nodes {
		entities = append(entities, node.CanonicalID)
	}
	err = persistence.WithTenantTx(ctx, pool, tenant, func(tx pgx.Tx) error {
		// Metadata and archive identity only. Facts are read from authenticated
		// Evidence archives below; neighboring objects come from the bounded graph.
		rows, err := tx.Query(ctx, `SELECT evidence_id::text FROM platform.evidence_metadata WHERE tenant_id=$1 AND canonical_id=ANY($2::text[]) AND NOT deleting AND (metadata->>'observedTo')::timestamptz>=$3 ORDER BY evidence_id LIMIT $4`, tenant, entities, input.From, recipe.Budget.MaxEvidence+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var ref string
			if err := rows.Scan(&ref); err != nil {
				return err
			}
			refs = append(refs, ref)
		}
		return rows.Err()
	})
	if err != nil {
		return err
	}
	if len(refs) > recipe.Budget.MaxEvidence {
		refs = refs[:recipe.Budget.MaxEvidence]
		input.Graph.Partial = true
		input.Graph.DegradedSources = append(input.Graph.DegradedSources, "evidence-budget-exhausted")
	}
	for _, ref := range refs {
		refID, parseErr := uuid.Parse(ref)
		if parseErr != nil {
			return finding.ErrInvalid
		}
		metadata, err := (evidence.Repository{Pool: pool}).Get(ctx, tenant, refID, graphScope)
		if err != nil {
			input.Graph.DegradedSources = append(input.Graph.DegradedSources, "evidence/"+ref)
			continue
		}
		if metadata.ReplayState == "archived_verified" {
			if archive == nil {
				input.Graph.DegradedSources = append(input.Graph.DegradedSources, "archive/"+ref)
				continue
			}
			data, _, err := archive.Read(ctx, tenant, refID)
			if err != nil {
				input.Graph.DegradedSources = append(input.Graph.DegradedSources, "archive/"+ref)
				continue
			}
			metadata.Data = data
		}
		input.Evidence = append(input.Evidence, metadata)
	}

	// This key freezes the input bundle rather than delivery time. A repeated
	// incident event with unchanged aggregate evidence cannot append duplicates.
	preview, err := rca.Evaluate(recipe, input)
	if err != nil {
		return err
	}
	key := finding.Hash([]any{i.Revision, version.Digest, rca.InputDigest(input, preview)})
	_, err = (rca.Repository{Pool: pool, Trust: trust, Archive: archive, Graph: h.Graph, GraphScope: graphScope}).Commit(ctx, tenant, i.IncidentID, "deterministic-worker", key, i.Revision, i.CurrentRCARevision, &version.VersionID, recipe, input)
	if err != nil {
		return err
	}
	return nil
}
func clusterContextID(ctx context.Context, pool persistence.TxBeginner, tenant uuid.UUID, uid string) (uuid.UUID, error) {
	var id uuid.UUID
	err := persistence.WithTenantTx(ctx, pool, tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT cluster_id FROM platform.cluster_registrations WHERE tenant_id=$1 AND cluster_uid=$2 AND status='active'`, tenant, uid).Scan(&id)
	})
	return id, err
}

// Namespace access comes from the current collector binding, including for a
// cluster-scoped Node. It never inherits all namespaces from cluster permission.
func sp05WorkerGraphScope(ctx context.Context, pool persistence.TxBeginner, cluster SP04Cluster) (graph.Scope, error) {
	scope := graph.Scope{Tenant: cluster.Tenant, Cluster: cluster.ClusterUID, ClusterScoped: true, AuthorizationRevision: "sp05-worker/" + cluster.SourceID}
	err := persistence.WithTenantTx(ctx, pool, uuid.MustParse(cluster.Tenant), func(tx pgx.Tx) error {
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT data_scope_mapping FROM platform.source_registrations WHERE tenant_id=$1 AND source_id=$2 AND revision=$3 AND status='active'`, cluster.Tenant, cluster.SourceID, cluster.SourceRevision).Scan(&raw); err != nil {
			return err
		}
		var mapping datascope.Mapping
		if json.Unmarshal(raw, &mapping) != nil || len(mapping.Scopes["cluster"]) != 1 || mapping.Scopes["cluster"][0] != cluster.ClusterUID {
			return finding.ErrUnauthorized
		}
		scope.Namespaces = append([]string{}, mapping.Scopes["namespace"]...)
		return nil
	})
	return scope, err
}

func resourceKind(raw string) (string, error) {
	id, err := resource.ParseCanonicalID(raw)
	return id.Kind, err
}
