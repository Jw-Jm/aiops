-- +goose Up
SET ROLE schema_owner;

-- Preserve existing stable cluster identities and historical revisions.
-- Legacy metadata stays empty rather than inventing a detected version.
ALTER TABLE platform.cluster_registrations
  ADD COLUMN api_endpoint_ref text NOT NULL DEFAULT '',
  ADD COLUMN distribution text NOT NULL DEFAULT '',
  ADD COLUMN actual_versions jsonb NOT NULL DEFAULT '{}'::jsonb,
  ADD COLUMN capabilities jsonb NOT NULL DEFAULT '{}'::jsonb,
  ADD CHECK (length(api_endpoint_ref) <= 1024),
  ADD CHECK (length(distribution) <= 64),
  ADD CHECK (jsonb_typeof(actual_versions) = 'object' AND octet_length(actual_versions::text) <= 16384),
  ADD CHECK (jsonb_typeof(capabilities) = 'object' AND octet_length(capabilities::text) <= 16384);

ALTER TABLE platform.cluster_registration_revisions
  ADD COLUMN api_endpoint_ref text NOT NULL DEFAULT '',
  ADD COLUMN distribution text NOT NULL DEFAULT '',
  ADD COLUMN actual_versions jsonb NOT NULL DEFAULT '{}'::jsonb,
  ADD COLUMN capabilities jsonb NOT NULL DEFAULT '{}'::jsonb;

RESET ROLE;
