-- +goose Up
SET ROLE schema_owner;
ALTER TABLE finding.timeline DROP CONSTRAINT timeline_disposition_check;
ALTER TABLE finding.timeline ADD CONSTRAINT timeline_disposition_check CHECK(disposition IN('accepted','stale','duplicate'));
RESET ROLE;
