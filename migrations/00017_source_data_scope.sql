-- +goose Up
SET ROLE schema_owner;
ALTER TABLE platform.source_registrations
    ADD COLUMN backend_logical_id text NOT NULL DEFAULT '',
    ADD COLUMN data_scope_mapping jsonb NOT NULL DEFAULT '{}',
    ADD CONSTRAINT source_backend_logical_id_bounded CHECK (length(backend_logical_id) <= 512),
    ADD CONSTRAINT source_data_scope_mapping_object CHECK (jsonb_typeof(data_scope_mapping) = 'object' AND octet_length(data_scope_mapping::text) <= 524288);

-- +goose StatementBegin
CREATE FUNCTION platform.keep_source_backend_immutable() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.backend_logical_id <> '' AND NEW.backend_logical_id IS DISTINCT FROM OLD.backend_logical_id THEN
        RAISE EXCEPTION 'backend_logical_id is an immutable source identity' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER source_backend_immutable
    BEFORE UPDATE OF backend_logical_id ON platform.source_registrations
    FOR EACH ROW EXECUTE FUNCTION platform.keep_source_backend_immutable();
RESET ROLE;

-- Existing rows acquire no backend permission. An API declaration cannot mark
-- a mapping verified; adapter verification belongs to SP-04.
