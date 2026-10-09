package app

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"io"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"net/url"
	"ops-platform/internal/action"
	"ops-platform/internal/configregistry"
	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/incident"
	"ops-platform/internal/persistence"
	"ops-platform/internal/policy"
	"ops-platform/internal/rca"
	"ops-platform/internal/resource"
	"os"
	"time"
)

func commandPostCheckPass(ctx context.Context, s action.Service, archive *evidence.ArchiveService, tenant uuid.UUID) error {
	ids := []uuid.UUID{}
	err := persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT execution_id FROM action.executions WHERE tenant_id=$1 AND state IN('succeeded','failed') AND post_checked_at IS NULL ORDER BY completed_at LIMIT 5`, tenant)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			if err = rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	})
	if err != nil {
		return err
	}
	c, err := loadSP04()
	if err != nil || c == nil {
		return err
	}
	trust, err := RegistryTrustFromFile(os.Getenv("PLATFORM_REGISTRY_TRUST_FILE"))
	if err != nil {
		return err
	}
	compiler, err := policy.NewBundleCompiler(trust)
	if err != nil {
		return err
	}
	registry, err := configregistry.NewService(s.Pool, trust, compiler)
	if err != nil {
		return err
	}
	for _, id := range ids {
		var e action.Execution
		var bindingRaw []byte
		var completed time.Time
		err = persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
			e.ID = id
			return tx.QueryRow(ctx, `SELECT binding,completed_at FROM action.executions WHERE tenant_id=$1 AND execution_id=$2`, tenant, id).Scan(&bindingRaw, &completed)
		})
		if err != nil {
			return err
		}
		if json.Unmarshal(bindingRaw, &e.Binding) != nil {
			return action.ErrInvalid
		}
		facts := action.PostCheckFacts{CheckedAt: time.Now().UTC(), EvidenceRefs: []string{}}
		// Missing or revoked native source is an explicit inconclusive verdict.
		for _, cluster := range c.Clusters {
			if cluster.Tenant != tenant.String() || cluster.ClusterUID != e.Binding.ClusterUID {
				continue
			}
			facts = collectCommandPostCheck(ctx, s, archive, registry, trust, cluster, e, completed)
		}
		if err = s.RecordPostCheck(ctx, tenant, id, facts); err != nil {
			return err
		}
	}
	return nil
}
func collectCommandPostCheck(ctx context.Context, s action.Service, archive *evidence.ArchiveService, registry *configregistry.Service, trust configregistry.Ed25519TrustStore, cluster SP04Cluster, e action.Execution, completed time.Time) action.PostCheckFacts {
	f := action.PostCheckFacts{CheckedAt: time.Now().UTC(), EvidenceRefs: []string{}}
	tenant := e.Binding.TenantID
	scope, err := (graph.Authorization{Pool: s.Pool}).Effective(ctx, tenant.String(), e.Binding.Subject, cluster.ClusterUID)
	if err != nil {
		return f
	}
	ns := ""
	if e.Binding.Namespace != nil {
		ns = *e.Binding.Namespace
	}
	if !scope.Allows(e.Binding.Target, ns) {
		return f
	}
	f.Authorized = true
	binding, err := (evidence.Repository{Pool: s.Pool}).RegisteredBinding(ctx, evidence.Binding{Tenant: cluster.Tenant, SourceID: cluster.SourceID, Revision: cluster.SourceRevision, SourceType: "kubernetes", BackendLogicalID: cluster.BackendLogicalID})
	if err != nil {
		return f
	}
	cluster.SourceScopeDigest = evidence.BindingScopeDigest(binding)

	var i incident.Incident
	var name, kind string
	var clusterID uuid.UUID
	err = persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
		var err error
		i, err = incident.Load(ctx, tx, tenant, e.Binding.IncidentID.String(), false)
		if err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT name,kind,cluster_id FROM platform.resource_entities WHERE tenant_id=$1 AND canonical_id=$2 AND deleted_at IS NULL`, tenant, e.Binding.Target).Scan(&name, &kind, &clusterID)
	})
	if err != nil || i.ResourceCanonicalID != e.Binding.Target {
		return f
	}
	plural, recipeName := "", ""
	switch kind {
	case "PersistentVolumeClaim":
		plural, recipeName = "persistentvolumeclaims", "pvc-csi-failure"
	case "Node":
		plural, recipeName = "nodes", "node-failure"
	default:
		return f
	}
	activationScope := configregistry.Scope{Type: configregistry.ScopeCluster, ClusterID: clusterID}
	if ns != "" {
		activationScope.Type = configregistry.ScopeNamespace
		activationScope.Namespace = ns
	}
	version, err := registry.ResolveActive(ctx, tenant, configregistry.KindRecipe, recipeName, activationScope, time.Now())
	if err != nil {
		return f
	}
	recipe, err := rca.DecodeRecipe(version.Content)
	if err != nil {
		return f
	}
	client, err := clusterClient(cluster)
	if err != nil {
		return f
	}
	path := "/api/v1/"
	if ns != "" {
		path += "namespaces/" + url.PathEscape(ns) + "/"
	}
	path += plural + "/" + url.PathEscape(name)
	response, err := client.Do(ctx, "GET", path, nil)
	if err != nil {
		return f
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return f
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 {
		return f
	}
	var object unstructured.Unstructured
	if json.Unmarshal(raw, &object.Object) != nil || string(object.GetUID()) != e.Binding.TargetUID || object.GetNamespace() != ns {
		return f
	}
	now := time.Now().UTC()
	if !now.After(completed) {
		return f
	}
	group := object.GroupVersionKind().Group
	if group == "" {
		group = "core"
	}
	canonical := resource.CanonicalID{Domain: "k8s", Tenant: tenant.String(), Scope: cluster.ClusterUID, APIGroup: group, Kind: kind, StableID: string(object.GetUID())}
	if canonical.String() != e.Binding.Target {
		return f
	}
	object.SetAnnotations(nil)
	data, err := json.Marshal(object.Object)
	if err != nil {
		return f
	}
	ev := evidence.Evidence{SchemaVersion: "evidence/v2", EvidenceID: uuid.Must(uuid.NewV7()).String(), TenantID: tenant.String(), ResourceCanonicalID: e.Binding.Target, Type: "resource_state", DataClass: "D0", SourceRegistrationID: cluster.SourceID, SourceRevision: cluster.SourceRevision, SourceSystem: "kubernetes", BackendLogicalID: cluster.BackendLogicalID, SourceScopeDigest: cluster.SourceScopeDigest, QueryTemplateVersion: "kubernetes-projection/v1", QueryHash: evidence.Digest([]byte(path)), EffectiveScope: scope, EvaluatedAt: now, ObservedFrom: now, ObservedTo: now, SourceRetentionUntil: now, TimeReliable: true, ReplayState: "archive_pending", ContentDigest: evidence.Digest(data), IndependenceGroup: cluster.SourceID, DerivationEvidenceRefs: []string{}, Data: data}
	if archive.Capture(ctx, ev, ns, now.Add(365*24*time.Hour)) != nil {
		return f
	}
	ev.ReplayState = "archived_verified"
	verdict, err := rca.PostCheckResource(recipe, ev, completed)
	if err != nil || verdict == "inconclusive" {
		return f
	}
	// Recheck current source and Recipe after the live read and archive commit.
	if _, err = (evidence.Repository{Pool: s.Pool}).Get(ctx, tenant, uuid.MustParse(ev.EvidenceID), scope); err != nil {
		return f
	}
	if persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
		return rca.VerifyRecipeForRead(ctx, tx, tenant, &version.VersionID, cluster.ClusterUID, ns, recipe, trust)
	}) != nil {
		return f
	}
	f.RecipeVersion = version.VersionID
	f.RecipeName = recipe.Name
	f.CollectedAfter = now
	f.CheckedAt = time.Now().UTC()
	f.SourceAvailable = true
	f.Complete = true
	f.FaultPresent = verdict == "not_resolved"
	f.EvidenceRefs = []string{ev.EvidenceID}
	return f
}
