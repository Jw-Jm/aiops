-- +goose Up
SET ROLE schema_owner;
-- Forward-only safe observations. Existing current entities are never backfilled.
CREATE TABLE platform.resource_observations (
 tenant_id uuid NOT NULL, observation_id uuid NOT NULL,
 canonical_id text NOT NULL, namespace text NOT NULL, source_id uuid NOT NULL,
 source_revision bigint NOT NULL CHECK(source_revision>0), source_scope_digest text NOT NULL,
 resource_version text NOT NULL, observed_at timestamptz NOT NULL,
 event_at timestamptz, coverage text NOT NULL CHECK(coverage IN('initial_list','continuous_watch','coverage_gap')),
 projection jsonb NOT NULL CHECK(octet_length(projection::text)<=65536),
 retain_until timestamptz NOT NULL,
 PRIMARY KEY(tenant_id,observation_id),
 UNIQUE(tenant_id,source_id,source_revision,canonical_id,resource_version),
 FOREIGN KEY(tenant_id,source_id) REFERENCES platform.source_registrations(tenant_id,source_id),
 FOREIGN KEY(tenant_id,canonical_id) REFERENCES platform.resource_entities(tenant_id,canonical_id)
);
CREATE INDEX resource_observation_window ON platform.resource_observations(tenant_id,canonical_id,observed_at DESC);
CREATE INDEX resource_observation_event_window ON platform.resource_observations(tenant_id,source_id,event_at) WHERE event_at IS NOT NULL;
ALTER TABLE platform.resource_observations ENABLE ROW LEVEL SECURITY;
ALTER TABLE platform.resource_observations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON platform.resource_observations USING(tenant_id=NULLIF(current_setting('app.tenant_id',true),'')::uuid) WITH CHECK(tenant_id=NULLIF(current_setting('app.tenant_id',true),'')::uuid);
GRANT SELECT ON platform.resource_observations TO api_runtime_role,worker_runtime_role;
GRANT INSERT ON platform.resource_observations TO worker_runtime_role;
RESET ROLE;
SET ROLE schema_owner;
-- Old dispatchers never see unclaimed/expired v2 work, so they cannot consume
-- it as a revoked v1 job. New Claim declares its protocol inside the transaction.
CREATE POLICY assistant_catalog_claim ON investigation.worker_queue AS RESTRICTIVE FOR SELECT TO worker_runtime_role
 USING (lease_expires_at>clock_timestamp() OR current_setting('app.assistant_catalog',true)='assistant-tools/v2' OR NOT EXISTS(SELECT 1 FROM investigation.jobs j WHERE j.tenant_id=worker_queue.tenant_id AND j.job_id=worker_queue.job_id AND j.assistant_binding->>'catalogVersion'='assistant-tools/v2'));
RESET ROLE;
SET ROLE schema_owner;
-- +goose StatementBegin
CREATE FUNCTION platform.resource_history_cleanup(tid uuid,batch_limit integer) RETURNS integer
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,platform,investigation AS $$
DECLARE deleted integer;
BEGIN
 IF tid IS DISTINCT FROM NULLIF(current_setting('app.tenant_id',true),'')::uuid OR batch_limit NOT BETWEEN 1 AND 100 THEN RAISE EXCEPTION 'invalid resource history cleanup'; END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended(tid::text,180));
 DELETE FROM platform.resource_observations o WHERE o.tenant_id=tid AND o.observation_id IN (
  SELECT h.observation_id FROM platform.resource_observations h WHERE h.tenant_id=tid AND h.retain_until<clock_timestamp()
  AND NOT EXISTS(SELECT 1 FROM investigation.steps s WHERE s.tenant_id=tid AND s.state='succeeded' AND s.tool_name IN('resource_history','resource_changes') AND s.result::text LIKE '%'||h.observation_id::text||'%')
  ORDER BY h.observed_at,h.observation_id LIMIT batch_limit
 );
 GET DIAGNOSTICS deleted=ROW_COUNT;
 RETURN deleted;
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION platform.resource_history_cleanup(uuid,integer) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION platform.resource_history_cleanup(uuid,integer) TO worker_runtime_role;
RESET ROLE;
SET ROLE schema_owner;
CREATE TABLE platform.resource_observation_coverage (
 tenant_id uuid NOT NULL,source_id uuid NOT NULL,source_revision bigint NOT NULL,
 gvr text NOT NULL,observed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 last_list_at timestamptz,watch_connected boolean NOT NULL,watch_continuous boolean NOT NULL,has_error boolean NOT NULL,
 PRIMARY KEY(tenant_id,source_id,source_revision,gvr,observed_at),
 FOREIGN KEY(tenant_id,source_id) REFERENCES platform.source_registrations(tenant_id,source_id)
);
ALTER TABLE platform.resource_observation_coverage ENABLE ROW LEVEL SECURITY;
ALTER TABLE platform.resource_observation_coverage FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON platform.resource_observation_coverage USING(tenant_id=NULLIF(current_setting('app.tenant_id',true),'')::uuid) WITH CHECK(tenant_id=NULLIF(current_setting('app.tenant_id',true),'')::uuid);
GRANT SELECT ON platform.resource_observation_coverage TO api_runtime_role,worker_runtime_role;
GRANT INSERT ON platform.resource_observation_coverage TO worker_runtime_role;
RESET ROLE;
SET ROLE schema_owner;
-- Coverage transitions share 30d minimum retention. Retained history Steps carry
-- their frozen transition receipts, independent of this rolling source index.
-- +goose StatementBegin
CREATE FUNCTION platform.resource_coverage_cleanup(tid uuid,batch_limit integer) RETURNS integer
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,platform AS $$
DECLARE deleted integer;
BEGIN
 IF tid IS DISTINCT FROM NULLIF(current_setting('app.tenant_id',true),'')::uuid OR batch_limit NOT BETWEEN 1 AND 100 THEN RAISE EXCEPTION 'invalid coverage cleanup'; END IF;
 DELETE FROM platform.resource_observation_coverage c WHERE c.tenant_id=tid AND (c.source_id,c.source_revision,c.gvr,c.observed_at) IN (
  SELECT source_id,source_revision,gvr,observed_at FROM platform.resource_observation_coverage WHERE tenant_id=tid AND observed_at<clock_timestamp()-interval '30 days' ORDER BY observed_at LIMIT batch_limit
 );
 GET DIAGNOSTICS deleted=ROW_COUNT;RETURN deleted;
END $$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION platform.resource_coverage_cleanup(uuid,integer) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION platform.resource_coverage_cleanup(uuid,integer) TO worker_runtime_role;
RESET ROLE;
