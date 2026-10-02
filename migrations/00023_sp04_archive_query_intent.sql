-- +goose Up
SET ROLE schema_owner;
-- Only bounded semantic query arguments, never raw metrics/logs/flow facts.
-- NULL identifies collection facts that cannot be re-queried after source expiry.
ALTER TABLE platform.evidence_archive_intents ADD COLUMN query_args jsonb CHECK(query_args IS NULL OR jsonb_typeof(query_args)='object');
RESET ROLE;
