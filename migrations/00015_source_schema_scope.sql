-- +goose Up
SET ROLE schema_owner;

-- Existing registrations remain untrusted for ingestion until an operator
-- explicitly grants a schema through the audited revision API. Do not infer
-- a permission from historical data or widen it during migration.
ALTER TABLE platform.source_registrations
  ADD COLUMN allowed_schemas text[] NOT NULL DEFAULT '{}',
  ADD CONSTRAINT source_allowed_schemas CHECK (
    cardinality(allowed_schemas) <= 1 AND
    allowed_schemas <@ ARRAY['finding-envelope/v1']::text[]
  );

RESET ROLE;
