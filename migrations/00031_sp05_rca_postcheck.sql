-- +goose Up
SET ROLE schema_owner;
ALTER TABLE incident.records ADD COLUMN last_rca_checked_at timestamptz;
RESET ROLE;
