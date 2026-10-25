-- +goose Up
-- MR-002: a v2 Incident investigation Job outlives the browser session that
-- admitted it, so the asynchronous Worker/recovery paths need the exact identity
-- system that proved the operator. The provenance is persisted next to the Job
-- (never accepted from a request body) and the external investigation-job/v2 JSON
-- stays unchanged.
--
--   authorization_source  in ('legacy','native'); NULL is the historical encoding
--                         for rows written before these columns existed. Those
--                         rows could only have been admitted through
--                         platform.role_bindings, because the native v2 path did
--                         not exist, so NULL must never be read as native.
--   authorization_user_id / authorization_version
--                         the native account and its authorization version when
--                         the Job was admitted. Both are frozen with the Job.
--
-- A re-check dispatches on the persisted source:
--   * native  -> account -> operator grants -> job, shared locks, scope rebuilt
--                from identity.role_grants (platform.native_incident_task_shared)
--   * legacy  -> the unchanged platform.sp06_lock_authority/role_bindings path
-- There is no fallback between the two identity systems.
SET ROLE schema_owner;

ALTER TABLE investigation.jobs
  ADD COLUMN authorization_source text,
  ADD COLUMN authorization_user_id uuid,
  ADD COLUMN authorization_version integer;

-- Only three shapes are accepted: a complete native provenance, a complete legacy
-- provenance without an account, and the all-NULL historical encoding. The whole
-- disjunction is wrapped in "IS TRUE" because a CHECK constraint accepts a NULL
-- result: without it, source='native' with a NULL version (or source NULL with a
-- user) would make one branch NULL and slip through.
ALTER TABLE investigation.jobs
  ADD CONSTRAINT investigation_jobs_authorization_shape CHECK ((
    (authorization_source IS NULL AND authorization_user_id IS NULL AND authorization_version IS NULL)
    OR (authorization_source = 'legacy' AND authorization_user_id IS NULL AND authorization_version IS NULL)
    OR (authorization_source = 'native' AND authorization_user_id IS NOT NULL AND authorization_version IS NOT NULL AND authorization_version >= 1)
  ) IS TRUE) NOT VALID;
ALTER TABLE investigation.jobs VALIDATE CONSTRAINT investigation_jobs_authorization_shape;

COMMENT ON COLUMN investigation.jobs.authorization_source IS
  'Identity system that proved this v2 Job: legacy or native. NULL only for rows written before the column existed (legacy).';
COMMENT ON COLUMN investigation.jobs.authorization_user_id IS
  'Frozen native account that admitted this v2 Job; NULL for legacy and historical rows.';
COMMENT ON COLUMN investigation.jobs.authorization_version IS
  'Frozen native authorization version of the admitting account; a later version never revives the Job.';

-- The frozen Job subject must be the account's own subject, never a name derived
-- from a prefix or a caller field. This projection locks account -> grants -> job
-- in the same order as the administrative write and the Worker re-proof, and
-- returns the account subject only for a live native v2 Job. Terminal jobs are
-- included: reading a completed Job still requires a valid account.
-- +goose StatementBegin
CREATE FUNCTION platform.native_incident_job_subject_shared(
  p_tenant_id uuid,
  p_job_id uuid
) RETURNS text
LANGUAGE plpgsql
VOLATILE
SECURITY DEFINER
SET search_path = pg_catalog, platform, identity, investigation
AS $$
DECLARE
  frozen_user uuid;
  frozen_version integer;
  account_subject text;
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'job_authority', true);
  IF p_tenant_id IS NULL OR p_job_id IS NULL THEN
    RETURN NULL;
  END IF;
  SELECT job.authorization_user_id, job.authorization_version
  INTO frozen_user, frozen_version
  FROM investigation.jobs AS job
  WHERE job.tenant_id = p_tenant_id AND job.job_id = p_job_id
    AND job.schema_version = 'investigation-job/v2'
    AND job.authorization_source = 'native';
  IF frozen_user IS NULL OR frozen_version IS NULL OR frozen_version < 1 THEN
    RETURN NULL;
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
  SELECT account.subject INTO account_subject
  FROM investigation.jobs AS job
  JOIN identity.accounts AS account
    ON account.tenant_id = job.tenant_id
   AND account.user_id = frozen_user
  WHERE job.tenant_id = p_tenant_id
    AND job.job_id = p_job_id
    AND job.schema_version = 'investigation-job/v2'
    AND job.authorization_source = 'native'
    AND job.authorization_user_id = account.user_id
    AND job.authorization_version = account.authorization_version
    AND account.status = 'active';
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  RETURN account_subject;
END;
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION platform.native_incident_job_subject_shared(uuid, uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION platform.native_incident_job_subject_shared(uuid, uuid)
  TO api_runtime_role, worker_runtime_role, migration_role;

-- One persisted v2 Job is exactly one Incident investigation admitted by one
-- native operator. The projection re-proves lifecycle (queued/running, not
-- expired), the persisted source/version against the live account and grants, the
-- account subject binding, the frozen scope digest, the still-effective policy and
-- the tenant/cluster revisions. It reads no signed claim; the Worker supplies only
-- the tenant and job reference.
-- +goose StatementBegin
CREATE FUNCTION platform.native_incident_task_shared(
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
  frozen_policy text;
  frozen_cluster text;
  frozen_namespaces text[];
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'job_authority', true);
  IF p_tenant_id IS NULL OR p_job_id IS NULL THEN
    RETURN;
  END IF;
  -- Unlocked read only to learn which account to lock first and to validate the
  -- frozen policy before any cast; the authoritative projection below repeats it.
  SELECT job.authorization_user_id,
         job.authorization_version,
         job.policy_version,
         COALESCE(job.effective_scope->>'clusterUid', ''),
         COALESCE(
           CASE WHEN jsonb_typeof(job.effective_scope->'namespaces') = 'array'
                THEN ARRAY(SELECT jsonb_array_elements_text(job.effective_scope->'namespaces'))
           END,
           ARRAY[]::text[])
  INTO frozen_user, frozen_version, frozen_policy, frozen_cluster, frozen_namespaces
  FROM investigation.jobs AS job
  WHERE job.tenant_id = p_tenant_id AND job.job_id = p_job_id
    AND job.schema_version = 'investigation-job/v2'
    AND job.authorization_source = 'native';
  IF frozen_user IS NULL OR frozen_version IS NULL OR frozen_version < 1 THEN
    RETURN;
  END IF;
  IF frozen_policy IS NULL
     OR frozen_policy !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
     OR frozen_cluster = '' THEN
    RETURN;
  END IF;
  -- Same order as identity.admin_account_write and the native job authority:
  -- account row, then grant rows, then the job row.
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
         'operational',
         'incident_followup',
         job.effective_scope,
         job.effective_scope,
         job.effective_scope_digest,
         job.effective_scope_digest,
         '',
         '',
         job.state,
         job.expires_at
  FROM investigation.jobs AS job
  JOIN identity.accounts AS account
    ON account.tenant_id = job.tenant_id
   AND account.user_id = frozen_user
  WHERE job.tenant_id = p_tenant_id
    AND job.job_id = p_job_id
    AND job.schema_version = 'investigation-job/v2'
    AND job.state IN ('queued', 'running')
    AND job.expires_at > clock_timestamp()
    AND job.authorization_source = 'native'
    AND job.authorization_user_id = account.user_id
    AND job.authorization_version = account.authorization_version
    AND job.incident_id IS NOT NULL
    AND COALESCE(job.trigger_revision, 0) > 0
    AND job.requesting_subject = account.subject
    AND account.status = 'active'
    AND jsonb_typeof(job.effective_scope) = 'object'
    AND jsonb_typeof(job.effective_scope->'namespaces') = 'array'
    AND COALESCE(job.effective_scope_digest, '') <> ''
    -- The admitted policy must still be the effective one for this cluster and
    -- still cover the frozen namespaces.
    AND platform.sp06_lock_policy(
          p_tenant_id,
          frozen_policy::uuid,
          frozen_cluster,
          frozen_namespaces);
  PERFORM set_config('app.identity_preauth', 'disabled', true);
END;
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION platform.native_incident_task_shared(uuid, uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION platform.native_incident_task_shared(uuid, uuid)
  TO api_runtime_role, worker_runtime_role, migration_role;

RESET ROLE;
