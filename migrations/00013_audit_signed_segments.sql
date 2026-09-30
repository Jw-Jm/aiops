-- +goose Up
SET ROLE schema_owner;

-- The existing tenant_seq is retained for compatibility. New rows use the
-- global audit identity for both sequence columns, avoiding a tenant-head row
-- lock on every append. Existing rows and their digests remain untouched.
SELECT setval(
  pg_get_serial_sequence('audit.records', 'audit_seq'),
  COALESCE((SELECT max(audit_seq) FROM audit.records), 1),
  EXISTS (SELECT 1 FROM audit.records)
);

CREATE TABLE audit.signed_segments (
  tenant_id uuid NOT NULL REFERENCES platform.tenants (tenant_id),
  segment_id uuid NOT NULL,
  first_audit_seq bigint NOT NULL CHECK (first_audit_seq > 0),
  last_audit_seq bigint NOT NULL CHECK (last_audit_seq >= first_audit_seq),
  record_count integer NOT NULL CHECK (record_count BETWEEN 1 AND 10000),
  merkle_root text NOT NULL CHECK (merkle_root ~ '^sha256:[0-9a-f]{64}$'),
  signature text,
  signing_key_version text,
  object_ref jsonb,
  object_digest text CHECK (object_digest IS NULL OR object_digest ~ '^sha256:[0-9a-f]{64}$'),
  status text NOT NULL DEFAULT 'pending_signature' CHECK (status IN ('pending_signature', 'signed')),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  signed_at timestamptz,
  PRIMARY KEY (tenant_id, segment_id),
  UNIQUE (tenant_id, first_audit_seq),
  CHECK ((status = 'pending_signature' AND signature IS NULL AND signing_key_version IS NULL AND object_ref IS NULL AND object_digest IS NULL AND signed_at IS NULL)
      OR (status = 'signed' AND signature IS NOT NULL AND signing_key_version IS NOT NULL AND object_ref IS NOT NULL AND object_digest IS NOT NULL AND signed_at IS NOT NULL))
);
CREATE INDEX audit_signed_segments_range_idx ON audit.signed_segments (tenant_id, first_audit_seq, last_audit_seq);

ALTER TABLE audit.signed_segments ENABLE ROW LEVEL SECURITY;
ALTER TABLE audit.signed_segments FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON audit.signed_segments
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- +goose StatementBegin
CREATE FUNCTION audit.append_record_v2(
  p_tenant_id uuid,
  p_record_id uuid,
  p_event_type text,
  p_entity_kind text,
  p_entity_id uuid,
  p_subject text,
  p_record jsonb,
  p_canonical_digest bytea,
  p_created_at timestamptz
) RETURNS TABLE(audit_seq bigint, tenant_seq bigint)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, audit, platform
AS $$
DECLARE
  expected_tenant uuid;
  assigned_seq bigint;
BEGIN
  expected_tenant := NULLIF(current_setting('app.tenant_id', true), '')::uuid;
  IF expected_tenant IS NULL OR expected_tenant <> p_tenant_id THEN
    RAISE EXCEPTION 'audit tenant context mismatch' USING ERRCODE = '42501';
  END IF;
  IF p_record_id IS NULL OR p_event_type IS NULL OR length(p_event_type) NOT BETWEEN 1 AND 200
     OR p_entity_kind IS NULL OR length(p_entity_kind) NOT BETWEEN 1 AND 100
     OR p_subject IS NULL OR jsonb_typeof(p_record) <> 'object'
     OR octet_length(p_canonical_digest) <> 32 OR p_created_at IS NULL THEN
    RAISE EXCEPTION 'invalid audit record' USING ERRCODE = '22023';
  END IF;

  -- Appends share this lock. A segment boundary takes the matching exclusive
  -- lock while it snapshots its range, without serializing ordinary appends.
  PERFORM pg_advisory_xact_lock_shared(770037, hashtext(p_tenant_id::text));
  assigned_seq := nextval(pg_get_serial_sequence('audit.records', 'audit_seq'));
  RETURN QUERY
  INSERT INTO audit.records AS r
    (audit_seq, tenant_id, tenant_seq, record_id, event_type, entity_kind, entity_id, subject, record, canonical_digest, created_at)
  OVERRIDING SYSTEM VALUE
  VALUES
    (assigned_seq, p_tenant_id, assigned_seq, p_record_id, p_event_type, p_entity_kind, p_entity_id, p_subject, p_record, p_canonical_digest, p_created_at)
  RETURNING r.audit_seq, r.tenant_seq;
END
$$;
-- +goose StatementEnd

GRANT CREATE ON SCHEMA audit TO audit_append_owner;
SET ROLE audit_append_owner;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION audit.append_record(
  p_tenant_id uuid,
  p_record_id uuid,
  p_event_type text,
  p_entity_kind text,
  p_entity_id uuid,
  p_subject text,
  p_record jsonb,
  p_canonical_digest bytea
) RETURNS TABLE(audit_seq bigint, tenant_seq bigint)
LANGUAGE sql
SECURITY DEFINER
SET search_path = pg_catalog, audit, platform
AS $$
  SELECT * FROM audit.append_record_v2(
    p_tenant_id, p_record_id, p_event_type, p_entity_kind, p_entity_id,
    p_subject, p_record, p_canonical_digest, clock_timestamp()
  )
$$;
-- +goose StatementEnd
RESET ROLE;
SET ROLE schema_owner;

-- +goose StatementBegin
CREATE FUNCTION audit.begin_signed_segment(
  p_tenant_id uuid,
  p_segment_id uuid,
  p_first_audit_seq bigint,
  p_last_audit_seq bigint,
  p_record_count integer,
  p_merkle_root text
) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, audit, platform
AS $$
DECLARE
  expected_tenant uuid;
  observed_count bigint;
  observed_first bigint;
  observed_last bigint;
  previous_last bigint;
  next_first bigint;
BEGIN
  expected_tenant := NULLIF(current_setting('app.tenant_id', true), '')::uuid;
  IF expected_tenant IS NULL OR expected_tenant <> p_tenant_id THEN
    RAISE EXCEPTION 'audit tenant context mismatch' USING ERRCODE = '42501';
  END IF;
  IF p_segment_id IS NULL OR p_record_count NOT BETWEEN 1 AND 10000 OR p_first_audit_seq <= 0
     OR p_last_audit_seq < p_first_audit_seq OR p_merkle_root !~ '^sha256:[0-9a-f]{64}$' THEN
    RAISE EXCEPTION 'invalid signed audit segment' USING ERRCODE = '22023';
  END IF;
  PERFORM pg_advisory_xact_lock(770037, hashtext(p_tenant_id::text));
  SELECT count(*), min(audit_seq), max(audit_seq)
    INTO observed_count, observed_first, observed_last
    FROM audit.records WHERE tenant_id = p_tenant_id AND audit_seq BETWEEN p_first_audit_seq AND p_last_audit_seq;
  SELECT max(last_audit_seq) INTO previous_last
    FROM audit.signed_segments WHERE tenant_id = p_tenant_id;
  SELECT min(audit_seq) INTO next_first
    FROM audit.records WHERE tenant_id = p_tenant_id AND audit_seq > COALESCE(previous_last, 0);
  IF observed_count <> p_record_count OR observed_first <> p_first_audit_seq OR observed_last <> p_last_audit_seq
     OR next_first IS DISTINCT FROM p_first_audit_seq
     OR (previous_last IS NOT NULL AND p_first_audit_seq <= previous_last) THEN
    RAISE EXCEPTION 'audit segment range changed or overlaps a previous segment' USING ERRCODE = '22023';
  END IF;
  INSERT INTO audit.signed_segments (tenant_id, segment_id, first_audit_seq, last_audit_seq, record_count, merkle_root)
    VALUES (p_tenant_id, p_segment_id, p_first_audit_seq, p_last_audit_seq, p_record_count, p_merkle_root);
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION audit.finish_signed_segment(
  p_tenant_id uuid,
  p_segment_id uuid,
  p_signature text,
  p_signing_key_version text,
  p_object_ref jsonb,
  p_object_digest text
) RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, audit, platform
AS $$
DECLARE
  expected_tenant uuid;
BEGIN
  expected_tenant := NULLIF(current_setting('app.tenant_id', true), '')::uuid;
  IF expected_tenant IS NULL OR expected_tenant <> p_tenant_id THEN
    RAISE EXCEPTION 'audit tenant context mismatch' USING ERRCODE = '42501';
  END IF;
  IF p_signature !~ '^vault:v[1-9][0-9]*:[A-Za-z0-9+/=]+$' OR p_signing_key_version !~ '^[1-9][0-9]*$'
     OR jsonb_typeof(p_object_ref) <> 'object' OR p_object_digest !~ '^sha256:[0-9a-f]{64}$'
     OR p_object_ref->>'tenantId' <> p_tenant_id::text OR p_object_ref->>'objectId' <> p_segment_id::text
     OR p_object_ref->>'category' <> 'audit-segment' OR p_object_ref->>'digest' <> p_object_digest THEN
    RAISE EXCEPTION 'invalid signed audit segment seal' USING ERRCODE = '22023';
  END IF;
  UPDATE audit.signed_segments
    SET signature = p_signature, signing_key_version = p_signing_key_version,
        object_ref = p_object_ref, object_digest = p_object_digest,
        status = 'signed', signed_at = clock_timestamp()
    WHERE tenant_id = p_tenant_id AND segment_id = p_segment_id AND status = 'pending_signature';
  IF NOT FOUND THEN
    RAISE EXCEPTION 'pending audit segment does not exist' USING ERRCODE = '22023';
  END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION audit.guard_signed_segment_update() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, audit
AS $$
BEGIN
  IF OLD.tenant_id <> NEW.tenant_id OR OLD.segment_id <> NEW.segment_id
     OR OLD.first_audit_seq <> NEW.first_audit_seq OR OLD.last_audit_seq <> NEW.last_audit_seq
     OR OLD.record_count <> NEW.record_count OR OLD.merkle_root <> NEW.merkle_root
     OR OLD.created_at <> NEW.created_at OR OLD.status <> 'pending_signature' OR NEW.status <> 'signed' THEN
    RAISE EXCEPTION 'signed audit segment identity is immutable' USING ERRCODE = '42501';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER signed_segment_immutable BEFORE UPDATE ON audit.signed_segments
  FOR EACH ROW EXECUTE FUNCTION audit.guard_signed_segment_update();

GRANT SELECT ON audit.signed_segments TO api_runtime_role, worker_runtime_role;
REVOKE INSERT, UPDATE, DELETE, TRUNCATE ON audit.signed_segments FROM PUBLIC, api_runtime_role, worker_runtime_role, ops_readonly_role;
GRANT SELECT, INSERT, UPDATE ON audit.signed_segments TO audit_append_owner;
GRANT SELECT ON audit.records TO audit_append_owner;
GRANT USAGE, SELECT ON SEQUENCE audit.records_audit_seq_seq TO audit_append_owner;
REVOKE ALL ON audit.tenant_heads FROM audit_append_owner;
GRANT EXECUTE ON FUNCTION audit.append_record_v2(uuid, uuid, text, text, uuid, text, jsonb, bytea, timestamptz) TO api_runtime_role, worker_runtime_role;
GRANT EXECUTE ON FUNCTION audit.begin_signed_segment(uuid, uuid, bigint, bigint, integer, text) TO worker_runtime_role;
GRANT EXECUTE ON FUNCTION audit.finish_signed_segment(uuid, uuid, text, text, jsonb, text) TO worker_runtime_role;
REVOKE ALL ON FUNCTION audit.append_record_v2(uuid, uuid, text, text, uuid, text, jsonb, bytea, timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION audit.begin_signed_segment(uuid, uuid, bigint, bigint, integer, text) FROM PUBLIC;
REVOKE ALL ON FUNCTION audit.finish_signed_segment(uuid, uuid, text, text, jsonb, text) FROM PUBLIC;
ALTER FUNCTION audit.append_record_v2(uuid, uuid, text, text, uuid, text, jsonb, bytea, timestamptz) OWNER TO audit_append_owner;
ALTER FUNCTION audit.append_record(uuid, uuid, text, text, uuid, text, jsonb, bytea) OWNER TO audit_append_owner;
ALTER FUNCTION audit.begin_signed_segment(uuid, uuid, bigint, bigint, integer, text) OWNER TO audit_append_owner;
ALTER FUNCTION audit.finish_signed_segment(uuid, uuid, text, text, jsonb, text) OWNER TO audit_append_owner;
GRANT CREATE ON SCHEMA audit TO audit_append_owner;
GRANT USAGE ON SCHEMA audit, platform TO audit_append_owner;
REVOKE CREATE ON SCHEMA audit FROM audit_append_owner;

RESET ROLE;
