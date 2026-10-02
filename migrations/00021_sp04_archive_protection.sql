-- +goose Up
SET ROLE schema_owner;
ALTER TABLE platform.evidence_metadata ADD COLUMN protection_synced_at timestamptz;
-- NULL means the database protection has not yet been verified at the object source.
CREATE INDEX evidence_protection_pending ON platform.evidence_metadata(tenant_id,protection_synced_at,evidence_id) WHERE replay_state='archived_verified' AND NOT deleting;
RESET ROLE;
