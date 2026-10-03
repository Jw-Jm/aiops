-- +goose Up
SET ROLE schema_owner;
ALTER TABLE incident.records ADD COLUMN started_at timestamptz,ADD COLUMN last_observed_at timestamptz;
-- No inferred observation time for legacy rows. Their existing payload survives.
RESET ROLE;
