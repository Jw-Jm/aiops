# ADR-0025: Freeze causal input and declare actual SP-05 response contracts

Status: Accepted under the user's SP-05 development and repair authorization.

The independent review of candidate 2523962336b5c205f7f55b46609aff13a6e40b34
found that an RCA manifest identified Evidence but omitted the linked Finding
versions. It also found that a Graph-only source could be revoked between a
fresh query and a confirmed commit. The process-local Graph lock cannot fence
persistent authorization changes.

New RCAInput/v2 freezes up to 200 linked Finding IDs, aggregate/applied revisions,
occurrence, semantic payload digest, event time, resource and source revision.
Pending unapplied correlation rejects publication. Commit locks the Incident,
refreezes the actual linked Finding rows FOR SHARE, and requires exact equality.
The digest includes this manifest. Old stored revisions remain append-only;
current eligibility additionally requires the current linked manifest to match.

Bounded Graph results freeze only contributing source IDs, registration revisions
and data-scope digests. A confirmed commit checks them through the existing narrow
SECURITY DEFINER source share-lock function in the same transaction. This adds no
runtime source-update privilege. Persistent revoke/rotation serializes with the
commit. Historical reading rechecks present source and resource authorization;
a frozen historical confirmed status does not imply present authorization or
current eligibility. Missing Graph uses a distinct partial/unavailable snapshot
schema, never a fabricated ResourceGraph/v2 success.

Additive Finding/v2 aggregate, Incident/v2, RCAInput/v2, RCARevision/v2 and typed
success/page/current response schemas declare actual wire output. Finding's body
continues to contain the inherited finding-envelope/v2 marker; the aggregate
schema's separate ID is finding/v2. No historical envelope is rewritten. Frozen
v1 OpenAPI types remain intact. SP-05 errors use the existing error-envelope/v2;
authentication middleware's frozen v1 remains explicitly admitted by the response
union. Its standalone validation schema mirrors the unchanged OpenAPI definition.
Empty/final pages omit nextCursor. Go and TypeScript consumers are regenerated.

Incident policy retains the formal initial 10-minute correlation and 30-minute
reopen defaults. Suppression expiry opens only with active linked Findings and
otherwise closes, releasing the active Incident hold through the shared retention
lock without shortening existing 365-day reference protection. Merge and split
recompute recovery on their resulting actual groups, including all-resolved groups.

Transport digests parse JSON with UseNumber. Object ordering and insignificant
whitespace normalize; exact numeric token spelling is significant (1 and 1.0
are different payloads). Integer/decimal precision is never rounded through a
float64 before conflict detection.

These repairs introduce no performance admission, virtualization development,
PyRCA runtime admission, investigator or additional service. Final acceptance
and full independent re-review are tracked separately in the evidence ledger.
