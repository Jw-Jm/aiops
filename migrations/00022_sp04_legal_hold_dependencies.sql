-- +goose Up
SET ROLE schema_owner;
-- The Legal Hold mutation records its Audit dependency in the same tenant TX.
GRANT INSERT,UPDATE ON platform.evidence_retention_references TO api_runtime_role;
GRANT UPDATE(protection_synced_at) ON platform.evidence_metadata TO api_runtime_role;
RESET ROLE;
