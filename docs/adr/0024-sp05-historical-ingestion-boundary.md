# ADR-0024: Preserve historical ingestion without inventing current RCA proof

Status: Accepted

The frozen SP01 FindingEnvelope/v1 is retained. It does not carry the new SP05
native predicate, rule-family, source-sequence, timing reliability or evidence
independence semantics. No trusted source binding or required causal proof may
be manufactured while upgrading an old transport event.

SP05 FindingEnvelope/v2 ingestion uses the additive `/api/v2/findings:ingest`
endpoint, with verified subject -> tenant/source registration and credential
revision. The historical `/api/v1/findings:ingest` definition keeps its exact
v1 body and is deprecated; the current runtime reports CAPABILITY_DISABLED.
Before SP05 the Foundation router did not implement this public ingest route.
The source-service v1 persistence fixtures/history remain intact. New SP05
source registrations must explicitly allow finding-envelope/v2; migrations
never widen an existing source's schema grant automatically. OpenAPI's additive
revision retains existing frozen schemas and regenerates both consumer methods.

The actual Worker claims all pending/expired-claim outbox schemas. A historical
schema is explicitly quarantined as deadletter with
UNSUPPORTED_HISTORICAL_SCHEMA, retaining its payload, IDs, timestamps and prior
records. It is never marked delivered or applied to the new Incident/RCA state
machine. Invalid current payloads similarly remain deadletter; transient source,
transaction and service failures retain retry/recovery semantics. Quarantine
errors are surfaced by the Worker, and subsequent current events continue on
later bounded passes. This is unverified history, not a successful new delivery.
A trusted publisher may issue a new native v2 observation; it cannot rewrite the
old one. No automatic causal reinterpretation or synthetic native evidence is
introduced.

Regression: historical outbox previously stayed silently pending and could not
be claimed (legacy-outbox-quarantine-red.log, exit 1). The real PostgreSQL test
checks original payload preservation, exact reason, absence of consumer calls,
and successful delivery of the following v2 event. The stage acceptance ledger
records current verification, independently of the preserved SP01 fixtures.
