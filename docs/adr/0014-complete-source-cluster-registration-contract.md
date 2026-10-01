# ADR-0014: Complete Source and Cluster Registration Bindings

Status: Accepted

## Context and rationale

The authoritative implementation plan section 6.2 requires SourceRegistration
to bind allowedSchemas, and ClusterRegistration to bind apiEndpointRef,
distribution, actualVersions and capabilities. The SP-03 implementation omitted
these fields. Envelope authentication therefore accepted an unregistered schema
version, and cluster registration could not retain the detected environment.
The user authorized correcting SP-01–SP-03 contracts and implementation on
2026-10-01; this decision implements the existing requirement without changing
the root specification or resuming virtualization under ADR-0008.

## Decision

Change the OpenAPI source first and regenerate both Go and TypeScript bindings.
Source registration requires an explicit schema whitelist. In the current
ingestion contract its only supported member is `finding-envelope/v1`;
additional versions require their own contract and compatibility gate.
Authentication checks the envelope schemaVersion against the stored whitelist.
Scope changes and rollback include this whitelist in revision history and
the same-transaction audit record. Legacy records acquire no permission during
migration; a platform_admin explicitly fills the whitelist through the existing
step-up, revision and idempotency protected update API.

Cluster registration requires the detected distribution, exact component
versions, capability flags and an OpenBao reference to its API endpoint
descriptor. The reference carries no credential or endpoint secret. The existing
POST route accepts expectedRevision for explicit metadata updates or legacy
completion; it preserves clusterUid/displayName, detects concurrent changes and
appends a revision and audit record. A duplicate without expectedRevision may
only replay an identical binding. Declared metadata is not itself a live PoC
or production compatibility proof. Deferred virtual capabilities cannot be
enabled by registration.

At the HTTP boundary the existing `sourceType`, `instanceKey` and `status`
names map to the specification's sourceSystem, sourceInstance and state
concepts. They retain their existing wire names; this mapping neither permits a
payload to change tenant/cluster nor delegates authority to source claims.

## Alternatives considered

- Infer a schema grant or actual cluster version while migrating: rejected
  because absent evidence cannot create a permission or compatibility fact.
- Store metadata only in extensions: rejected because the frozen bindings need
  independent validation, revision history and authentication enforcement.
- Add a second registry or direct SQL maintenance API: rejected in favor of the
  existing audited governance routes.

## Consequences and implementation boundaries

Migrations are forward-only and preserve existing identities and historical
revisions. Legacy missing metadata stays empty until explicit completion.
Registration metadata does not implement a collector, production graph service
or deferred VM/VMI/DataVolume capability. Runtime database logins must remain
limited to their actual API/Worker role; tests use an API-only LOGIN principal.

## Rollback conditions

Do not remove stored scope or rewrite historical revisions. A future supported
schema version extends the whitelist only with compatibility tests and explicit
operator grants. Any change to endpoint reference semantics requires a new ADR
and public contract update before use.
