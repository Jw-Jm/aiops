-- +goose Up
-- Clear an expired lock when starting a new failure window; preserve live locks.
-- Applied 00065 remains unchanged. One timestamp keeps the threshold decisions atomic.
SET ROLE schema_owner;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION identity.step_up_budget_fail(
  p_tenant_id uuid,
  p_user_id uuid,
  p_factor text
) RETURNS TABLE (out_remaining integer, out_retry_at timestamptz)
LANGUAGE plpgsql
VOLATILE
SECURITY DEFINER
SET search_path = pg_catalog, identity
AS $$
DECLARE
  attempt_at timestamptz := clock_timestamp();
  next_count integer;
  next_window timestamptz;
  next_lock timestamptz;
BEGIN
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  PERFORM set_config('app.identity_preauth', 'stepup_verify', true);
  IF p_factor IS NULL OR p_factor NOT IN ('password', 'totp') THEN
    RAISE EXCEPTION 'invalid step-up budget factor' USING ERRCODE = '22023';
  END IF;
  IF NOT identity.step_up_budget_factor_in_scope(p_tenant_id, p_user_id, p_factor) THEN
    PERFORM set_config('app.identity_preauth', 'disabled', true);
    RETURN QUERY SELECT 5, NULL::timestamptz;
    RETURN;
  END IF;
  INSERT INTO identity.step_up_failures AS failures
    (tenant_id, user_id, factor, failed_count, window_started_at, locked_until, updated_at)
  VALUES
    (p_tenant_id, p_user_id, p_factor, 1, attempt_at, NULL, attempt_at)
  ON CONFLICT (tenant_id, user_id, factor) DO UPDATE
    SET failed_count = CASE
          WHEN failures.window_started_at <= attempt_at - interval '15 minutes'
            THEN 1
          ELSE failures.failed_count + 1
        END,
        window_started_at = CASE
          WHEN failures.window_started_at <= attempt_at - interval '15 minutes'
            THEN attempt_at
          ELSE failures.window_started_at
        END,
        locked_until = CASE
          WHEN (CASE
                  WHEN failures.window_started_at <= attempt_at - interval '15 minutes'
                    THEN 1
                  ELSE failures.failed_count + 1
                END) >= 5
            THEN GREATEST(failures.locked_until, attempt_at + interval '15 minutes')
          WHEN failures.window_started_at <= attempt_at - interval '15 minutes'
               AND (failures.locked_until IS NULL OR failures.locked_until <= attempt_at)
            THEN NULL
          ELSE failures.locked_until
        END,
        updated_at = attempt_at
  RETURNING failures.failed_count, failures.window_started_at, failures.locked_until
  INTO next_count, next_window, next_lock;
  PERFORM set_config('app.identity_preauth', 'disabled', true);
  IF next_lock IS NOT NULL AND next_lock > attempt_at THEN
    RETURN QUERY SELECT 0, next_lock;
    RETURN;
  END IF;
  IF next_window <= attempt_at - interval '15 minutes' THEN
    RETURN QUERY SELECT 5, NULL::timestamptz;
    RETURN;
  END IF;
  RETURN QUERY SELECT GREATEST(5 - next_count, 0), NULL::timestamptz;
END;
$$;
-- +goose StatementEnd
RESET ROLE;
