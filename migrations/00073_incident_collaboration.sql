-- +goose Up
SET ROLE schema_owner;

-- Incident collaboration (P4-04): assignment lives on the incident row but is
-- deliberately outside the revision CAS. Assignment is operator metadata, not an
-- incident fact: bumping the revision would invalidate the current RCA view
-- (rca BaseIncidentRevision equality) and open investigations' triggerRevision
-- without any evidence change. Concurrency is guarded by the assignee columns
-- themselves (saveAssignment compares the previous assignee).
ALTER TABLE incident.records
  ADD COLUMN assignee_subject text CHECK (assignee_subject IS NULL OR length(btrim(assignee_subject)) BETWEEN 1 AND 256),
  ADD COLUMN assigned_at timestamptz;
CREATE INDEX incident_records_assignee_idx ON incident.records (tenant_id, assignee_subject) WHERE assignee_subject IS NOT NULL;

-- Retrospective follow-up todos survive resolution and never rewrite incident
-- facts; completion keeps who closed the item and a bounded resolution note.
CREATE TABLE incident.followups (
  tenant_id uuid NOT NULL REFERENCES platform.tenants (tenant_id),
  incident_id uuid NOT NULL,
  followup_id uuid NOT NULL,
  title text NOT NULL CHECK (length(btrim(title)) BETWEEN 1 AND 200),
  detail text NOT NULL DEFAULT '' CHECK (length(detail) <= 4000),
  state text NOT NULL CHECK (state IN ('open','done','cancelled')),
  creator_subject text NOT NULL CHECK (length(creator_subject) BETWEEN 1 AND 256),
  owner_subject text CHECK (owner_subject IS NULL OR length(btrim(owner_subject)) BETWEEN 1 AND 256),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  completed_at timestamptz,
  completer_subject text CHECK (completer_subject IS NULL OR length(completer_subject) BETWEEN 1 AND 256),
  resolution_note text NOT NULL DEFAULT '' CHECK (length(resolution_note) <= 1000),
  PRIMARY KEY (tenant_id, followup_id),
  FOREIGN KEY (tenant_id, incident_id) REFERENCES incident.records (tenant_id, incident_id),
  CHECK (
    (state = 'open' AND completed_at IS NULL AND completer_subject IS NULL)
    OR (state IN ('done','cancelled') AND completed_at IS NOT NULL AND completer_subject IS NOT NULL)
  )
);
CREATE INDEX incident_followups_open_idx ON incident.followups (tenant_id, incident_id, state, followup_id);

-- +goose StatementBegin
DO $$
DECLARE item text;
BEGIN
 FOREACH item IN ARRAY ARRAY['incident.followups'] LOOP
 EXECUTE format('ALTER TABLE %s ENABLE ROW LEVEL SECURITY',item);
 EXECUTE format('ALTER TABLE %s FORCE ROW LEVEL SECURITY',item);
 EXECUTE format('CREATE POLICY tenant_isolation ON %s USING(tenant_id=NULLIF(current_setting(''app.tenant_id'',true),'''')::uuid) WITH CHECK(tenant_id=NULLIF(current_setting(''app.tenant_id'',true),'''')::uuid)',item);
 END LOOP;
END $$;
-- +goose StatementEnd
-- Manual alert unlink is a deliberate operator correction on the API path;
-- it stays fenced by the revision CAS, timeline and audit records.
GRANT SELECT,INSERT,UPDATE ON incident.followups TO api_runtime_role;
GRANT SELECT ON incident.followups TO worker_runtime_role;
GRANT DELETE ON incident.finding_links TO api_runtime_role;

-- Transfer eligibility needs the recipient's persisted operator authority
-- without inventing a session for someone who is not the caller. Identity
-- tables stay readable only inside this SECURITY DEFINER helper: a native
-- recipient must hold an active operator grant covering the cluster and
-- namespace, a legacy recipient keeps the legacy operator binding fence.
-- +goose StatementBegin
CREATE FUNCTION incident.transfer_eligible(p_tenant_id uuid, p_subject text, p_cluster_uid text, p_resource_canonical_id text, p_namespace text)
RETURNS boolean
LANGUAGE plpgsql
VOLATILE
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE has_native boolean;
BEGIN
  SELECT EXISTS(SELECT 1 FROM identity.accounts AS account
                WHERE account.tenant_id = p_tenant_id AND account.subject = p_subject) INTO has_native;
  IF has_native THEN
    RETURN EXISTS (
      SELECT 1
      FROM identity.accounts AS account
      JOIN LATERAL platform.native_operator_scope_shared(p_tenant_id, account.user_id) AS shared ON true
      WHERE account.tenant_id = p_tenant_id
        AND account.subject = p_subject
        AND account.status = 'active'
        AND shared.scope -> 'clusterUids' ? p_cluster_uid
        AND (jsonb_array_length(shared.scope -> 'namespaces') = 0 OR shared.scope -> 'namespaces' ? p_namespace)
    );
  END IF;
  RETURN platform.sp05_lock_operator_scope(p_tenant_id, p_subject, p_cluster_uid, p_namespace);
END;
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION incident.transfer_eligible(uuid, text, text, text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION incident.transfer_eligible(uuid, text, text, text, text) TO api_runtime_role, worker_runtime_role;
RESET ROLE;
