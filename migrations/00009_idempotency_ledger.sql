-- +goose Up
SET ROLE schema_owner;

CREATE TABLE platform.idempotency_request_ledger (
  tenant_id uuid NOT NULL REFERENCES platform.tenants (tenant_id),
  ledger_id uuid NOT NULL,
  subject text NOT NULL CHECK (length(subject) BETWEEN 1 AND 512),
  operation text NOT NULL CHECK (length(operation) BETWEEN 1 AND 512),
  idempotency_key_digest text NOT NULL CHECK (idempotency_key_digest ~ '^sha256:[0-9a-f]{64}$'),
  request_digest text NOT NULL CHECK (request_digest ~ '^sha256:[0-9a-f]{64}$'),
  execution_once boolean NOT NULL DEFAULT false,
  state text NOT NULL CHECK (state IN ('in_progress', 'retryable', 'completed', 'failed', 'execution_unknown')),
  lease_token uuid,
  lease_expires_at timestamptz,
  attempt integer NOT NULL DEFAULT 1 CHECK (attempt > 0),
  response_status integer,
  response_content_type text,
  response_headers jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(response_headers) = 'object'),
  response_body bytea,
  response_expires_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
  PRIMARY KEY (tenant_id, ledger_id),
  UNIQUE (tenant_id, subject, operation, idempotency_key_digest),
  CHECK (
    (state = 'in_progress' AND lease_token IS NOT NULL AND lease_expires_at IS NOT NULL)
    OR (state <> 'in_progress' AND lease_token IS NULL AND lease_expires_at IS NULL)
  ),
  CHECK (
    (state = 'completed' AND response_status BETWEEN 100 AND 599 AND response_content_type = 'application/json' AND response_body IS NOT NULL AND response_expires_at IS NOT NULL)
    OR (state <> 'completed' AND response_status IS NULL AND response_content_type IS NULL AND response_body IS NULL AND response_expires_at IS NULL)
  )
);
CREATE INDEX idempotency_expiration_idx ON platform.idempotency_request_ledger (tenant_id, response_expires_at)
  WHERE state = 'completed';
CREATE INDEX idempotency_failed_expiration_idx ON platform.idempotency_request_ledger (tenant_id, updated_at)
  WHERE state = 'failed';
CREATE INDEX idempotency_lease_idx ON platform.idempotency_request_ledger (tenant_id, lease_expires_at)
  WHERE state = 'in_progress';

ALTER TABLE platform.idempotency_request_ledger ENABLE ROW LEVEL SECURITY;
ALTER TABLE platform.idempotency_request_ledger FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON platform.idempotency_request_ledger
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- +goose StatementBegin
CREATE FUNCTION platform.delete_expired_idempotency(p_tenant_id uuid, p_before timestamptz, p_limit integer)
RETURNS integer
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog, platform
AS $$
DECLARE
  expected_tenant uuid;
  removed integer;
BEGIN
  expected_tenant := NULLIF(current_setting('app.tenant_id', true), '')::uuid;
  IF expected_tenant IS NULL OR expected_tenant <> p_tenant_id THEN
    RAISE EXCEPTION 'idempotency tenant context mismatch' USING ERRCODE = '42501';
  END IF;
  IF p_before IS NULL OR p_before > clock_timestamp() OR p_limit < 1 OR p_limit > 10000 THEN
    RAISE EXCEPTION 'invalid idempotency cleanup range' USING ERRCODE = '22023';
  END IF;

  WITH expired AS (
    SELECT ctid
    FROM platform.idempotency_request_ledger
    WHERE tenant_id = p_tenant_id
      AND (
        (state = 'completed' AND NOT execution_once AND response_expires_at < LEAST(p_before, clock_timestamp()))
        OR (state = 'failed' AND updated_at < LEAST(p_before, clock_timestamp() - interval '90 days'))
      )
    ORDER BY COALESCE(response_expires_at, updated_at)
    LIMIT p_limit
  )
  DELETE FROM platform.idempotency_request_ledger AS ledger USING expired
  WHERE ledger.ctid = expired.ctid;
  GET DIAGNOSTICS removed = ROW_COUNT;
  RETURN removed;
END
$$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION platform.delete_expired_idempotency(uuid, timestamptz, integer) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION platform.delete_expired_idempotency(uuid, timestamptz, integer) TO api_runtime_role, worker_runtime_role;
GRANT SELECT, INSERT, UPDATE ON platform.idempotency_request_ledger TO api_runtime_role, worker_runtime_role;
RESET ROLE;
