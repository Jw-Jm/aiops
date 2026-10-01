# ADR-0017: Align SP-01–SP-03 with the reviewed specification

Status: Accepted

## Context

On 2026-10-01 the user explicitly authorized synchronizing the SP-01, SP-02 and
SP-03 code with the revised root specifications and repeatedly reviewing and
fixing the result. This authorization supersedes the earlier SP-02-only stage
scope for this synchronization. ADR-0008 still defers all KubeVirt/CDI work.

The revised source registration boundary requires stable backend identity and
declared backend scopes. The existing runtime stored neither. The development
template still requested virtualization detection, the idempotency middleware
emitted an obsolete conflict alias, and audit archive retention defaulted to
30 days instead of the specified 365 days. A successful upload response alone
was sufficient for the archive store to return a reference.

## Decision

- OpenAPI remains the public contract source. Its compatible revision is 1.1.0;
  product version and `/api/v1` paths stay unchanged. New registration request
  fields are optional so existing callers remain supported. Responses describe
  the actual registration fields, including a read-only query capability.
  `sourceId` retains its existing wire name and represents the specification's
  stable sourceRegistrationId; `instanceKey` remains a display-independent
  source identity key within a tenant.
- SourceRegistration stores `backendLogicalId` and `dataScopeMapping` in
  explicit columns through new forward-only migration 00017. Existing records
  retain identities, credentials, revisions and history and acquire no backend
  query grant. The mapping declares nativeTenant, exact account/project/
  organization/team/cluster/namespace sets and required equality labels. Empty
  sets represent no permission; unknown dimensions, wildcards, regex-like
  selectors, null bindings, control characters and duplicated literals fail.
- A completed backendLogicalId cannot be rebound or erased, including by
  rollback or direct runtime SQL. Legacy records can complete the identity once
  through the existing revision, step-up and idempotency protected API. Changes
  and rollback preserve mapping snapshots and same-transaction audits.
  Credential rotation preserves sourceId, backend identity and declared scope.
- Registry declarations are not isolation verification. The public
  queryCapability stays `disabled/unverified` with reason
  `adapter_scope_verification_pending`. No caller may submit a verification
  claim. SP-04 must introduce its verified adapter contract and perform actual
  backend isolation checks before enabling queries; it must not infer a grant
  from this metadata. Finding ingestion's existing allowedSchemas and identity
  checks are retained. BoundSourceContext additionally carries registration
  revision for subsequent consumers to recheck at their transaction boundary.
- Conflicting idempotency keys emit `IDEMPOTENCY_CONFLICT`. The legacy
  `IDEMPOTENCY_KEY_REUSED` enum member remains available to old consumers, but
  the runtime no longer emits it. In-progress replies include Retry-After.
  Existing authorization-before-replay and no-command-redispatch safeguards
  remain required and covered by regression tests.
- Archive Put reads back the addressed version and checks stored bytes,
  identity, digest, size, content type and declared retention before returning
  a usable reference. A failed check leaves the existing pending/recovery path
  available; it never deletes a possibly committed object. Delete honors both
  backend metadata retention and the supplied reference retention. Worker audit
  objects default to 365 days. Previously signed references and retention locks
  are not rewritten; this default is not a retroactive extension or production
  capacity claim.
- Generated-file checks regenerate from empty output directories in an isolated
  copy of the current Git-visible source, compare with the working files and
  leave those files available on success, failure or interruption. Correct
  uncommitted output is reviewable; stale, missing and extra output fails.
- The core development template explicitly disables KubeVirt/CDI with
  `development_deferred`, retaining candidate matrices, profiles and fixtures.

## Stage boundary and validation

This change completes the affected foundation behavior. It does not implement
SP-04–SP-09 Graph/Job/SSE, collector scope injection, Finding/Incident reducers,
RCA CAS, command execution, evidence dependency retention or Legal Hold business
flows. Their required fields and closed-enum semantics require new versioned
contracts and forward-only migrations when those stages are authorized; existing
frozen domain v1 schemas are not silently granted new mandatory semantics.

Use isolated PostgreSQL upgrade/runtime tests, source revision and HTTP tests,
archive corruption/recovery and real tenant IAM/TLS fixtures, generated-output
regression tests, the security suite and make check. Record actual executions
separately from skipped live tests and historical SP-02 installation evidence.

## Recovery

Do not downgrade schema, rewrite histories, rebind backend identity or enable
queries to bypass verification. Archive readback failures remain pending for
verified recovery. Deployments using the new API/Worker must apply migration
00017 first and provision capacity for the new retention policy; existing
objects needing longer retention require the backend's controlled extension
procedure rather than changing a database date alone.
