-- +goose Up
-- A command execution outlives the browser session that admitted it: the
-- post-check and the execution audit read it later without a session. Persist
-- which identity system proved the command authority, at which version, so an
-- asynchronous reader can re-prove native authority from the account and live
-- grants instead of falling back to the legacy binding table.
--
-- Empty stays valid for rows written before this column existed and is read as
-- legacy, exactly like investigation.jobs.assistant_binding.
SET ROLE schema_owner;
ALTER TABLE action.executions
  ADD COLUMN authority jsonb NOT NULL DEFAULT '{}'::jsonb
  CHECK (jsonb_typeof(authority) = 'object');
RESET ROLE;

-- +goose Down
-- Forward-only.
