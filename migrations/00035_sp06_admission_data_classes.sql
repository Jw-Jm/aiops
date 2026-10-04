-- +goose Up
SET ROLE schema_owner;
-- Historical admissions without a signed restriction snapshot are narrowed to
-- D0. Never infer a broader permission from the Job's original ceiling.
ALTER TABLE investigation.admissions ADD COLUMN allowed_data_classes text[] NOT NULL DEFAULT ARRAY['D0']::text[];
ALTER TABLE investigation.admissions ADD CONSTRAINT admission_data_classes CHECK(cardinality(allowed_data_classes) BETWEEN 1 AND 2 AND allowed_data_classes <@ ARRAY['D0','D1']::text[]);
RESET ROLE;
