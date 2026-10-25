-- +goose Up
-- MR-017: a trusted persisted task is not "any v3 job row that happens to name a
-- user". The Worker must re-prove the whole frozen task authority: lifecycle
-- (queued/running, not expired, not cancelled), the persisted native source and
-- version, the frozen tenant-wide grant snapshot and execution scope digests,
-- and the conversation/turn ownership plus input/context binding. This function
-- performs that proof in one place and shares the account -> grants -> job lock
-- order used by administrative revocation, so a cancel/revoke waits for the
-- consuming transaction instead of slipping between the checks and the RPC.
SET ROLE schema_owner;

-- +goose StatementBegin
CREATE FUNCTION platform.native_internal_task_shared(
  p_tenant_id uuid,
  p_job_id uuid
) RETURNS TABLE (
  out_user_id uuid,
  out_subject text,
  out_authorization_version integer,
  out_domain text,
  out_task_kind text,
  out_effective_scope jsonb,
  out_grant_snapshot jsonb,
  out_grant_digest text,
  out_effective_digest text,
  out_input_digest text,
  out_context_digest text,
  out_state text,
  out_expires_at timestamptz
)
LANGUAGE plpgsql
VOLATILE
SECURITY DEFINER
SET search_path = pg_catalog, platform, identity, investigation
AS $$
DECLARE
  frozen_user uuid;
  frozen_version integer;
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'job_authority', true);
  IF p_tenant_id IS NULL OR p_job_id IS NULL THEN
    RETURN;
  END IF;
  -- Unlocked read only to learn which account to lock first.
  SELECT (job.assistant_binding->>'authorizationUserId')::uuid,
         (job.assistant_binding->>'authorizationVersion')::integer
  INTO frozen_user, frozen_version
  FROM investigation.jobs AS job
  WHERE job.tenant_id = p_tenant_id AND job.job_id = p_job_id
    AND job.schema_version = 'investigation-job/v3';
  IF frozen_user IS NULL OR frozen_version IS NULL OR frozen_version < 1 THEN
    RETURN;
  END IF;
  PERFORM 1 FROM identity.accounts AS account
  WHERE account.tenant_id = p_tenant_id AND account.user_id = frozen_user
  FOR SHARE;
  PERFORM 1 FROM identity.role_grants AS grant_row
  WHERE grant_row.tenant_id = p_tenant_id AND grant_row.user_id = frozen_user
  ORDER BY grant_row.binding_id
  FOR SHARE;
  PERFORM 1 FROM investigation.jobs AS job
  WHERE job.tenant_id = p_tenant_id AND job.job_id = p_job_id
  FOR SHARE;
  RETURN QUERY
  SELECT account.user_id,
         job.requesting_subject,
         account.authorization_version,
         job.assistant_binding->>'authorizationDomain',
         job.assistant_binding->>'taskKind',
         job.effective_scope,
         job.assistant_binding->'grantSnapshot',
         job.assistant_binding->>'grantSnapshotDigest',
         job.effective_scope_digest,
         job.assistant_binding->>'inputDigest',
         job.assistant_binding->>'contextDigest',
         job.state,
         job.expires_at
  FROM investigation.jobs AS job
  JOIN identity.accounts AS account
    ON account.tenant_id = job.tenant_id
   AND account.user_id = frozen_user
  JOIN investigation.assistant_conversations AS conversation
    ON conversation.tenant_id = job.tenant_id
   AND conversation.conversation_id = job.assistant_conversation_id
  JOIN investigation.assistant_turns AS turn
    ON turn.tenant_id = job.tenant_id
   AND turn.turn_id = job.assistant_turn_id
  WHERE job.tenant_id = p_tenant_id
    AND job.job_id = p_job_id
    AND job.schema_version = 'investigation-job/v3'
    AND job.state IN ('queued', 'running')
    AND job.expires_at > clock_timestamp()
    AND job.assistant_binding->>'authorizationSource' = 'native'
    AND job.assistant_binding->>'authorizationUserId' = account.user_id::text
    AND job.assistant_binding->>'authorizationVersion' = account.authorization_version::text
    AND job.assistant_binding->>'authorizationDomain' IN ('operational', 'audit')
    AND job.assistant_binding->>'taskKind' IN ('data_query', 'incident_followup', 'resource_troubleshooting')
    AND jsonb_typeof(job.assistant_binding->'grantSnapshot') = 'object'
    AND COALESCE(job.assistant_binding->>'grantSnapshotDigest', '') <> ''
    AND COALESCE(job.effective_scope_digest, '') <> ''
    AND account.status = 'active'
    AND conversation.owner_subject = job.requesting_subject
    AND conversation.authorization_domain = job.assistant_binding->>'authorizationDomain'
    AND turn.conversation_id = conversation.conversation_id
    AND turn.job_id = job.job_id
    AND turn.state IN ('queued', 'running')
    AND turn.input_digest = job.assistant_binding->>'inputDigest'
    AND turn.context_digest = job.assistant_binding->>'contextDigest'
    -- The persisted task must bind the same native identity: the requesting
    -- subject is the account's own subject, never a prefix-derived user.
    AND job.requesting_subject = account.subject
    -- The task must have been admitted under a policy that is still the
    -- effective one for this tenant/cluster and still covers its namespaces.
    AND COALESCE(job.policy_version, '') <> ''
    AND platform.sp06_lock_policy(
          p_tenant_id,
          job.policy_version::uuid,
          -- graph.Scope serializes its cluster identity as "clusterUid".
          COALESCE(job.effective_scope->>'clusterUid', ''),
          COALESCE(
            ARRAY(SELECT jsonb_array_elements_text(job.effective_scope->'namespaces')),
            ARRAY[]::text[]))
    -- The same required metadata that investigation.AssistantTask.Valid enforces;
    -- a task the application could never create can never authorize a read.
    AND job.assistant_binding->>'catalogVersion' IN ('assistant-tools/v1', 'assistant-tools/v2')
    AND job.assistant_binding->>'promptVersion' = 'assistant/v1'
    AND COALESCE(job.assistant_binding->>'modelProfileVersion', '') <> ''
    AND job.assistant_binding->>'datasetCatalogVersion' = 'analysis-catalog/v1'
    AND COALESCE(job.assistant_binding->>'datasetCatalogDigest', '') <> ''
    AND job.assistant_binding->>'resultSchemaVersion' = 'assistant-answer/v1';
  PERFORM set_config('app.identity_preauth', 'disabled', true);
END;
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION platform.native_internal_task_shared(uuid, uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION platform.native_internal_task_shared(uuid, uuid)
  TO api_runtime_role, worker_runtime_role, migration_role;
RESET ROLE;
