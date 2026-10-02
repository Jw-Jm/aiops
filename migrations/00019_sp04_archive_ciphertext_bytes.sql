-- +goose Up
SET ROLE schema_owner;
-- JSONB normalizes key order and whitespace, so it cannot preserve the bytes
-- authenticated by the ciphertext digest. Keep previous pending JSONB intents
-- unverified; their source capture must be retried to populate exact bytes.
ALTER TABLE platform.evidence_archive_intents ADD COLUMN envelope_bytes bytea CHECK (octet_length(envelope_bytes)<=131072);
RESET ROLE;
