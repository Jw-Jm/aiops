-- +goose Up
SET ROLE schema_owner;
ALTER TABLE finding.poll_occurrences ADD COLUMN last_resolved_at timestamptz;
UPDATE finding.poll_occurrences p SET last_resolved_at=(SELECT max(f.observed_at) FROM finding.records f WHERE f.tenant_id=p.tenant_id AND f.source_id=p.source_id AND f.occurrence_id=p.occurrence_id::text AND f.lifecycle_state='resolved');
RESET ROLE;
