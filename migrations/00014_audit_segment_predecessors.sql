-- +goose Up
SET ROLE schema_owner;

-- Preserve existing signed manifests unchanged. Only new segments use v2.
ALTER TABLE audit.signed_segments
  ADD COLUMN format_version text NOT NULL DEFAULT 'audit-segment/v1',
  ADD COLUMN previous_segment_id uuid,
  ADD CONSTRAINT signed_segment_format CHECK (format_version IN ('audit-segment/v1', 'audit-segment/v2')),
  ADD CONSTRAINT signed_segment_predecessor_fk FOREIGN KEY (tenant_id, previous_segment_id)
    REFERENCES audit.signed_segments (tenant_id, segment_id);
ALTER TABLE audit.signed_segments ALTER COLUMN format_version SET DEFAULT 'audit-segment/v2';

GRANT CREATE ON SCHEMA audit TO audit_append_owner;
SET ROLE audit_append_owner;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION audit.begin_signed_segment(
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
  previous_id uuid;
  previous_status text;
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
  SELECT last_audit_seq, segment_id, status INTO previous_last, previous_id, previous_status
    FROM audit.signed_segments WHERE tenant_id = p_tenant_id ORDER BY last_audit_seq DESC LIMIT 1;
  IF previous_id IS NOT NULL AND previous_status <> 'signed' THEN
    RAISE EXCEPTION 'previous audit segment is unsigned' USING ERRCODE = '22023';
  END IF;
  SELECT min(audit_seq) INTO next_first
    FROM audit.records WHERE tenant_id = p_tenant_id AND audit_seq > COALESCE(previous_last, 0);
  IF observed_count <> p_record_count OR observed_first <> p_first_audit_seq OR observed_last <> p_last_audit_seq
     OR next_first IS DISTINCT FROM p_first_audit_seq
     OR (previous_last IS NOT NULL AND p_first_audit_seq <= previous_last) THEN
    RAISE EXCEPTION 'audit segment range changed or overlaps a previous segment' USING ERRCODE = '22023';
  END IF;
  INSERT INTO audit.signed_segments (tenant_id, segment_id, first_audit_seq, last_audit_seq, record_count, merkle_root, previous_segment_id)
    VALUES (p_tenant_id, p_segment_id, p_first_audit_seq, p_last_audit_seq, p_record_count, p_merkle_root, previous_id);
END
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION audit.begin_signed_segment(uuid, uuid, bigint, bigint, integer, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION audit.begin_signed_segment(uuid, uuid, bigint, bigint, integer, text) TO worker_runtime_role;
RESET ROLE;
SET ROLE schema_owner;
REVOKE CREATE ON SCHEMA audit FROM audit_append_owner;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION audit.guard_signed_segment_update() RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog, audit
AS $$
BEGIN
  IF OLD.format_version <> NEW.format_version OR OLD.previous_segment_id IS DISTINCT FROM NEW.previous_segment_id
     OR OLD.tenant_id <> NEW.tenant_id OR OLD.segment_id <> NEW.segment_id
     OR OLD.first_audit_seq <> NEW.first_audit_seq OR OLD.last_audit_seq <> NEW.last_audit_seq
     OR OLD.record_count <> NEW.record_count OR OLD.merkle_root <> NEW.merkle_root
     OR OLD.created_at <> NEW.created_at OR OLD.status <> 'pending_signature' OR NEW.status <> 'signed' THEN
    RAISE EXCEPTION 'signed audit segment identity is immutable' USING ERRCODE = '42501';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
RESET ROLE;
