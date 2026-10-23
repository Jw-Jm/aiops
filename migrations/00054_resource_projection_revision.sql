-- +goose Up
-- A real persisted revision for operation-bound resource confirmations. The
-- previous projection only had observed_at, which is not an object revision.
SET ROLE schema_owner;
ALTER TABLE platform.resource_entities ADD COLUMN resource_revision integer NOT NULL DEFAULT 1
 CHECK (resource_revision > 0);
-- +goose StatementBegin
CREATE FUNCTION platform.advance_resource_revision() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog,platform AS $$
BEGIN
 IF (NEW.cluster_id,NEW.kind,NEW.namespace,NEW.name,NEW.metadata,NEW.deleted_at)
    IS DISTINCT FROM (OLD.cluster_id,OLD.kind,OLD.namespace,OLD.name,OLD.metadata,OLD.deleted_at) THEN
   NEW.resource_revision:=OLD.resource_revision+1;
 ELSE NEW.resource_revision:=OLD.resource_revision;
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION platform.advance_resource_revision() FROM PUBLIC;
CREATE TRIGGER resource_projection_revision BEFORE UPDATE ON platform.resource_entities
 FOR EACH ROW EXECUTE FUNCTION platform.advance_resource_revision();
RESET ROLE;
-- +goose Down
-- Forward-only; do not discard revisions referenced by assessments/proofs.
