-- +goose Up
SET ROLE schema_owner;

CREATE TABLE audit.tenant_heads (
  tenant_id uuid PRIMARY KEY REFERENCES platform.tenants (tenant_id),
  last_tenant_seq bigint NOT NULL DEFAULT 0 CHECK (last_tenant_seq >= 0)
);

CREATE TABLE audit.records (
  audit_seq bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
  tenant_id uuid NOT NULL REFERENCES platform.tenants (tenant_id),
  tenant_seq bigint NOT NULL CHECK (tenant_seq > 0),
  record_id uuid NOT NULL,
  event_type text NOT NULL CHECK (length(event_type) BETWEEN 1 AND 200),
  entity_kind text NOT NULL CHECK (length(entity_kind) BETWEEN 1 AND 100),
  entity_id uuid,
  subject text NOT NULL,
  record jsonb NOT NULL CHECK (jsonb_typeof(record) = 'object'),
  canonical_digest bytea NOT NULL CHECK (octet_length(canonical_digest) = 32),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (tenant_id, tenant_seq),
  UNIQUE (tenant_id, record_id)
);
CREATE INDEX audit_records_tenant_time_idx ON audit.records (tenant_id, created_at DESC, tenant_seq DESC);
CREATE INDEX audit_records_entity_idx ON audit.records (tenant_id, entity_kind, entity_id, tenant_seq);

CREATE TABLE audit.segments (
  tenant_id uuid NOT NULL REFERENCES platform.tenants (tenant_id),
  segment_id uuid NOT NULL,
  first_tenant_seq bigint NOT NULL CHECK (first_tenant_seq > 0),
  last_tenant_seq bigint NOT NULL CHECK (last_tenant_seq >= first_tenant_seq),
  merkle_root text NOT NULL CHECK (merkle_root ~ '^sha256:[0-9a-f]{64}$'),
  signature bytea,
  signing_key_version text,
  object_ref text,
  status text NOT NULL DEFAULT 'pending_signature' CHECK (status IN ('pending_signature', 'signed', 'failed')),
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  signed_at timestamptz,
  PRIMARY KEY (tenant_id, segment_id),
  UNIQUE (tenant_id, first_tenant_seq),
  CHECK ((status = 'pending_signature' AND signature IS NULL AND signed_at IS NULL)
      OR (status = 'signed' AND signature IS NOT NULL AND signing_key_version IS NOT NULL AND object_ref IS NOT NULL AND signed_at IS NOT NULL)
      OR status = 'failed')
);

-- +goose StatementBegin
CREATE FUNCTION audit.append_record(
  p_tenant_id uuid,
  p_record_id uuid,
  p_event_type text,
  p_entity_kind text,
  p_entity_id uuid,
  p_subject text,
  p_record jsonb,
  p_canonical_digest bytea
) RETURNS TABLE(audit_seq bigint, tenant_seq bigint)
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, audit, platform
AS $$
DECLARE
  expected_tenant uuid;
  assigned_tenant_seq bigint;
BEGIN
  expected_tenant := NULLIF(current_setting('app.tenant_id', true), '')::uuid;
  IF expected_tenant IS NULL OR expected_tenant <> p_tenant_id THEN
    RAISE EXCEPTION 'audit tenant context mismatch' USING ERRCODE = '42501';
  END IF;
  IF p_record_id IS NULL OR p_event_type IS NULL OR p_entity_kind IS NULL OR p_subject IS NULL
     OR jsonb_typeof(p_record) <> 'object' OR octet_length(p_canonical_digest) <> 32 THEN
    RAISE EXCEPTION 'invalid audit record' USING ERRCODE = '22023';
  END IF;

  INSERT INTO audit.tenant_heads AS h (tenant_id, last_tenant_seq)
  VALUES (p_tenant_id, 1)
  ON CONFLICT (tenant_id) DO UPDATE SET last_tenant_seq = h.last_tenant_seq + 1
  RETURNING last_tenant_seq INTO assigned_tenant_seq;

  RETURN QUERY
  INSERT INTO audit.records AS r
    (tenant_id, tenant_seq, record_id, event_type, entity_kind, entity_id, subject, record, canonical_digest)
  VALUES
    (p_tenant_id, assigned_tenant_seq, p_record_id, p_event_type, p_entity_kind, p_entity_id, p_subject, p_record, p_canonical_digest)
  RETURNING r.audit_seq, r.tenant_seq;
END
$$;
-- +goose StatementEnd

GRANT EXECUTE ON FUNCTION audit.append_record(uuid, uuid, text, text, uuid, text, jsonb, bytea) TO api_runtime_role, worker_runtime_role;
REVOKE ALL ON FUNCTION audit.append_record(uuid, uuid, text, text, uuid, text, jsonb, bytea) FROM PUBLIC;
GRANT CREATE ON SCHEMA audit TO audit_append_owner;
ALTER FUNCTION audit.append_record(uuid, uuid, text, text, uuid, text, jsonb, bytea) OWNER TO audit_append_owner;
REVOKE CREATE ON SCHEMA audit FROM audit_append_owner;
GRANT USAGE ON SCHEMA audit TO api_runtime_role, worker_runtime_role, audit_append_owner;
GRANT USAGE ON SCHEMA platform TO audit_append_owner;
GRANT SELECT, INSERT ON audit.records TO audit_append_owner;
GRANT USAGE, SELECT ON SEQUENCE audit.records_audit_seq_seq TO audit_append_owner;
GRANT SELECT, INSERT, UPDATE ON audit.tenant_heads TO audit_append_owner;
RESET ROLE;
