package app

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/action"
	"ops-platform/internal/configregistry"
	"ops-platform/internal/evidence"
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
		rows, err := tx.Query(ctx, `SELECT execution_id FROM action.executions WHERE tenant_id=$1 AND state IN('succeeded','failed') AND post_checked_at IS NULL AND (NOT(binding ? 'verification') OR archived_at IS NOT NULL) ORDER BY completed_at LIMIT 5`, tenant)
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
		var bindingRaw, authorityRaw []byte
		var completed time.Time
		err = persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
			e.ID = id
			return tx.QueryRow(ctx, `SELECT binding,authority,completed_at FROM action.executions WHERE tenant_id=$1 AND execution_id=$2`, tenant, id).Scan(&bindingRaw, &authorityRaw, &completed)
		})
		if err != nil {
			return err
		}
		if json.Unmarshal(bindingRaw, &e.Binding) != nil || json.Unmarshal(authorityRaw, &e.Authority) != nil {
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
	// The collection runs without a browser session. Use the execution's frozen
	// identity source (native account/version or explicit legacy), never the
	// subject's legacy bindings, so a native command can reach a real verdict.
	scope, err := s.ScopeForExecution(ctx, e)
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
	support := action.PostCheckApplicability(e.Binding.Target)
	if support.Status == "unsupported" || support.TargetKind != kind {
		return f
	}
	recipeName := support.Recipe
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
	path := postCheckPath(kind, ns, name)
	object, err := readPostCheckObject(ctx, client, path)
	if err != nil || object.GetKind() != kind || object.GetName() != name || string(object.GetUID()) != e.Binding.TargetUID || object.GetNamespace() != ns {
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
	f.SourceAvailable = true
	// PVC v1 proved only Bound, never mount/CSI recovery. New collection needs
	// explicit signed v2 activation; historical v1 reports remain unchanged.
	if kind == "PersistentVolumeClaim" && recipe.Version != "v2" {
		return f
	}
	objects := []unstructured.Unstructured{object}
	if recipe.PostCheck == "native-related-health/v1" {
		collectionCtx, cancel := context.WithTimeout(ctx, time.Duration(recipe.Budget.TimeoutMs)*time.Millisecond)
		objects, err = nativePostCheckObjects(object, tenant.String(), cluster.ClusterUID, scope, func(path string) (unstructured.Unstructured, error) {
			return readPostCheckObject(collectionCtx, client, path)
		})
		cancel()
		if err != nil {
			return f
		}
	}
	es := []evidence.Evidence{}
	for _, object := range objects {
		object = postCheckProjection(object)
		now := time.Now().UTC()
		if !now.After(completed) {
			return f
		}
		group := object.GroupVersionKind().Group
		if group == "" {
			group = "core"
		}
		target := resource.CanonicalID{Domain: "k8s", Tenant: tenant.String(), Scope: cluster.ClusterUID, APIGroup: group, Kind: object.GetKind(), StableID: string(object.GetUID())}.String()
		data, err := json.Marshal(object.Object)
		if err != nil {
			return f
		}
		template := "kubernetes-projection/v1"
		if recipe.PostCheck == "native-related-health/v1" {
			template = "command-kubernetes-post-check/v1"
		}
		ev := evidence.Evidence{SchemaVersion: "evidence/v2", EvidenceID: uuid.Must(uuid.NewV7()).String(), TenantID: tenant.String(), ResourceCanonicalID: target, Type: "resource_state", DataClass: "D0", SourceRegistrationID: cluster.SourceID, SourceRevision: cluster.SourceRevision, SourceSystem: "kubernetes", BackendLogicalID: cluster.BackendLogicalID, SourceScopeDigest: cluster.SourceScopeDigest, QueryTemplateVersion: template, QueryHash: evidence.Digest([]byte(postCheckPath(object.GetKind(), object.GetNamespace(), object.GetName()))), EffectiveScope: scope, EvaluatedAt: now, ObservedFrom: now, ObservedTo: now, SourceRetentionUntil: now, TimeReliable: true, ReplayState: "archive_pending", ContentDigest: evidence.Digest(data), IndependenceGroup: cluster.SourceID, DerivationEvidenceRefs: []string{}, Data: data}
		if archive.Capture(ctx, ev, object.GetNamespace(), now.Add(365*24*time.Hour)) != nil {
			return f
		}
		ev.ReplayState = "archived_verified"
		es = append(es, ev)
	}
	verdict := "inconclusive"
	if recipe.PostCheck == "native-related-health/v1" {
		result, evalErr := rca.NativePostCheckResources(recipe, es, completed)
		if evalErr != nil {
			return f
		}
		verdict, f.ObservedSpecDigest, f.NewAnomalies = result.Verdict, result.SpecDigest, result.NewAnomalies
		f.Adapter = recipe.PostCheck
	} else {
		verdict, err = rca.PostCheckResource(recipe, es[0], completed)
		if err != nil {
			return f
		}
		f.ObservedSpecDigest = rca.SpecDigest(postCheckProjection(object))
	}
	// Recheck every dependency's current source/scope after archival.
	for _, ev := range es {
		if _, err = (evidence.Repository{Pool: s.Pool}).Get(ctx, tenant, uuid.MustParse(ev.EvidenceID), scope); err != nil {
			return f
		}
	}
	if persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
		return rca.VerifyRecipeForRead(ctx, tx, tenant, &version.VersionID, cluster.ClusterUID, ns, recipe, trust)
	}) != nil {
		return f
	}
	f.RecipeVersion, f.RecipeName = version.VersionID, recipe.Name
	f.CollectedAfter, f.CheckedAt = es[0].ObservedFrom, time.Now().UTC()
	if e.Binding.Verification != nil {
		result, err := s.VerificationResult(ctx, tenant, e.ID)
		if err != nil {
			return f
		}
		f.Verification = result
	}
	f.Complete = verdict != "inconclusive"
	f.FaultPresent = verdict == "not_resolved"
	f.EvidenceRefs = []string{}
	for _, ev := range es {
		f.EvidenceRefs = append(f.EvidenceRefs, ev.EvidenceID)
	}
	return f
}
