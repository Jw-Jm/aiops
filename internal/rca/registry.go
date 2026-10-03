package rca

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"ops-platform/internal/configregistry"
	"strings"
)

func (r Repository) verifyRecipe(ctx context.Context, tx pgx.Tx, tenant uuid.UUID, vid *uuid.UUID, cluster, namespace string, recipe Recipe) error {
	if vid == nil || *vid == uuid.Nil {
		return ErrRecipe
	}
	var clusterID uuid.UUID
	if err := tx.QueryRow(ctx, `SELECT cluster_id FROM platform.cluster_registrations WHERE tenant_id=$1 AND cluster_uid=$2 AND status='active'`, tenant, cluster).Scan(&clusterID); err != nil {
		return ErrRecipe
	}
	// Same activation locks as the registry, including a not-yet-created higher
	// priority namespace activation. Retirement also waits on the version fence.
	for _, scope := range []configregistry.Scope{{Type: configregistry.ScopeTenant}, {Type: configregistry.ScopeCluster, ClusterID: clusterID}, {Type: configregistry.ScopeNamespace, ClusterID: clusterID, Namespace: namespace}} {
		if scope.Type == configregistry.ScopeNamespace && namespace == "" {
			continue
		}
		key := strings.Join([]string{tenant.String(), "recipe", recipe.Name, string(scope.Type), scope.ClusterID.String(), scope.Namespace}, "|")
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(hashtextextended($1,0))`, key); err != nil {
			return err
		}
	}
	var valid bool
	if err := tx.QueryRow(ctx, `SELECT platform.sp05_lock_recipe($1,$2)`, tenant, vid).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return ErrRecipe
	}
	var raw, signature []byte
	var digest, key string
	if err := tx.QueryRow(ctx, `SELECT content,digest,signature,signer_key_id FROM platform.registry_versions WHERE tenant_id=$1 AND version_id=$2 AND kind='recipe' AND logical_name=$3`, tenant, vid, recipe.Name).Scan(&raw, &digest, &signature, &key); err != nil {
		return ErrRecipe
	}
	actual, err := DecodeRecipe(raw)
	if err != nil || !sameJSON(actual, recipe) {
		return ErrRecipe
	}
	message, expected, err := configregistry.SigningPayload(tenant, configregistry.KindRecipe, recipe.Name, json.RawMessage(raw))
	if err != nil || expected != digest || r.Trust.Verify(ctx, key, message, signature) != nil {
		return ErrRecipe
	}
	var current uuid.UUID
	err = tx.QueryRow(ctx, `SELECT version_id FROM platform.registry_activations WHERE tenant_id=$1 AND kind='recipe' AND logical_name=$2 AND (scope_type='tenant' OR (cluster_id=$3 AND (scope_type='cluster' OR namespace=$4))) ORDER BY CASE scope_type WHEN 'namespace' THEN 3 WHEN 'cluster' THEN 2 ELSE 1 END DESC LIMIT 1`, tenant, recipe.Name, clusterID, namespace).Scan(&current)
	if err != nil || current != *vid {
		return ErrRecipe
	}
	return nil
}
