-- name: ListRoleBindings :many
SELECT binding_id, subject, role_name, cluster_scopes, namespace_scopes, status, revision
FROM platform.role_bindings
WHERE tenant_id = sqlc.arg(tenant_id)
ORDER BY subject, binding_id;

-- name: ListSourceRegistrations :many
SELECT source_id, source_type, instance_key, cluster_id, status, credential_revision, revision
FROM platform.source_registrations
WHERE tenant_id = sqlc.arg(tenant_id)
ORDER BY source_type, instance_key;

-- name: ListPublishedRegistryVersions :many
SELECT version_id, kind, logical_name, version_number, digest, signer_key_id, published_at
FROM platform.registry_versions
WHERE tenant_id = sqlc.arg(tenant_id)
ORDER BY kind, logical_name, version_number DESC;
