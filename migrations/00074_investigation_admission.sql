-- +goose Up
SET ROLE schema_owner;

-- Investigation workbench (P4-05): a manual investigation may tighten the
-- server policy budget and carry an operator focus. The identity of an
-- admission is therefore no longer just (incident, revision, scope): the same
-- revision admitted under a different tightening or focus is a NEW job, so the
-- unique identity index gains admission_digest (sha256 over the effective
-- budget and focus, computed by the API). shortcut: pre-00074 rows keep '',
-- so one identical re-admission right after the upgrade may create a new job
-- instead of deduplicating against a pre-upgrade row; idempotency-key replay
-- (investigation.request_keys) is unaffected and stays authoritative.
ALTER TABLE investigation.jobs
  ADD COLUMN focus text NOT NULL DEFAULT '' CHECK (char_length(focus) <= 500),
  ADD COLUMN admission_digest text NOT NULL DEFAULT '' CHECK (admission_digest = '' OR admission_digest ~ '^sha256:[0-9a-f]{64}$');

DROP INDEX investigation.investigation_job_identity;
CREATE UNIQUE INDEX investigation_job_identity ON investigation.jobs (tenant_id,incident_id,policy_version,trigger_revision,trigger_kind,requesting_subject,effective_scope_digest,admission_digest) WHERE schema_version IN('investigation-job/v1','investigation-job/v2');

RESET ROLE;
