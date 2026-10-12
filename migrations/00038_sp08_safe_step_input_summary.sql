-- +goose Up
SET ROLE schema_owner;
ALTER TABLE investigation.steps ADD COLUMN args_summary jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(args_summary)='object' AND octet_length(args_summary::text)<=4096);
RESET ROLE;

-- +goose Down
SET ROLE schema_owner;
ALTER TABLE investigation.steps DROP COLUMN args_summary;
RESET ROLE;
