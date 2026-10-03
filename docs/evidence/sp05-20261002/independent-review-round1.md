# First independent review — REQUEST_CHANGES / NEEDS FIXES

Immutable candidate: 2523962336b5c205f7f55b46609aff13a6e40b34.
Reviewers: `/root/sp05_full_independent_review` (full delivery) and
`/root/sp05_runtime_boundary_review` (actual runtime/authority/contracts).
Neither participated in implementation. They reviewed the full authorized
SP-05 scope, not merely the final diff. Locations below refer to that candidate.
This is the reviewers' first findings as retransmitted in the current session.

## IR-01 — P2, actual lifecycle defect

`internal/incident/state_machine.go:148`, `internal/incident/domain.go:27`.
Trigger: all linked Findings resolve while Incident is suppressed, then expiry.
Impact: unconditional open, absent suppressed→closed whitelist, active hold persists.
Violates 04§11.4,05§5.5,04§33.3/Task5.2. Reproduce in real PG with active and
all-resolved groups; verify state/revision/Timeline/Outbox/reference active flag,
and reject manual open without an active Finding.

## IR-02 — P2, required frozen historical input gap

`internal/rca/engine.go:18`, `internal/app/sp05_worker.go:167`.
Trigger: Finding updates, delayed correlation, multi-Finding merge/split.
Impact: Evidence-only manifest lacks Finding ID/revision/occurrence/digest; cannot
explain the analysis input. Violates 06§41.2/Task5.6. Verify actual Finding
revision change with same Evidence: unapplied input cannot publish, consumed
input appends with different digest, old manifest immutable, concurrent fence.

## IR-03 — P2, required public output contract gap

`internal/finding/domain.go:44`, `internal/incident/correlator.go:174`,
`internal/rca/engine.go:60`, `api/openapi/platform-v1.yaml:3842`.
Trigger: consumers use Finding/Incident/current or historical RCA routes.
Impact: generic data and no versioned output shape; generated clients cannot
validate business responses. Violates ADR0017/18/19 and public Contract version
rules. Actual Finding wire marker is inherited finding-envelope/v2; the initial
review shorthand finding/v2 did not describe an existing wire literal.
Verify real ingestion/list/detail/mutation/current/history plus missing/conflict/
degraded output against strict schemas, preserve v1, regenerate Go/TS and run
check-generated. No SP08 UI is required.

## IR-04 — P2, actual transport digest collision

`internal/finding/domain.go:82`.
Trigger: same eventId/key with integers 9007199254740992 and 9007199254740993,
or adjacent high-precision decimals. float64 rounding makes distinct payloads
duplicate rather than conflict. Violates Task5.1/04§33.1–2. First reproduce
摘要 collision, then real Ingest requires ErrConflict/409; ordering/whitespace
remain equivalent and numeric spelling policy is explicit.

## IR-05 — P2, actual merge/split recovery loss

`internal/incident/split.go:79`, `:90`, `internal/incident/state_machine.go:243`.
Trigger: moved or remaining split group all resolved; merge two all-resolved
settling groups. NULL/cleared recovery clock prevents future automatic recovery
without another delivery, leaving open forever. Violates Task5.2/04§33.3.
Verify moved/left/merged real PG groups, full settle clock, active group has no
clock, unavailable Graph blocks recovery, restored Graph allows resolved.

## IR-06 — necessary evidence gap, pending

`docs/evidence/sp05-20261002/task-ledger.md:1`.
Trigger: declare completion before final fixes/source freeze/actual gates.
Impact: older development snapshots or materials do not prove final delivery.
Violates user completion criteria/ADR0019. Need current source-bound material,
signed Bundle, native Chart/offline, required current checks/integration/E2E and
shared regressions with commands/exits/raw redacted output. Live zero skips,
final complete independent review. Cannot waive with performance or VM deferral.

## BR-01 — medium, actual pagination contract defect

`internal/httpapi/sp05_handlers.go:128`; OpenAPI:1402–1403.
Trigger: empty/final Finding/Incident/Timeline/Evidence/RCA revisions page.
meta.nextCursor:null violates string-only PageEnvelope/typed client. Violates
user III.3/4,10§13.2/07. Verify actual first/middle/final/empty pages; absent
cursor should be omitted.

## BR-02 — high, actual Graph-only source authority gap

`internal/rca/repository.go:86–119`,`:190–193`,
`internal/graph/response.go:49`, `internal/app/sp05_worker.go` query/commit.
Trigger: fresh DIMM graph uses Kubernetes hosts/scheduled_on while Evidence is
Redfish; revoke/rotate Kubernetes after graph query, before confirmed commit.
Impact: local graph lock does not protect persistent source, confirmed persists
and historical provenance remains readable after source withdrawal. Violates
Task5.6/user III.4/10§14.4/06§41. Verify actual confirmed DIMM with authenticated
archive Data and Graph-only revoke/rotation; freeze exact authorities/scope,
same-TX share lock, missing/old authority rejection, historical deny, real
concurrent withdrawal waits or withdrawal-first commit rejects.

## BR-03 — medium, actual closed error contract violation

`internal/httpapi/sp05_handlers.go:43`,`:52`,`:56`; OpenAPI:701–739.
Trigger: old transition revision/changed authorization cursor/persistence failure.
STALE_CONTEXT or SOURCE_CAPABILITY_UNAVAILABLE emitted under old closed enum.
Impact: actual error and generated-client shape disagree. Violates III.3/4,
07 standard error/10Contract. Verify stale revision/cursor/faults against actual
route declaration; use versioned error/v2 plus legacy middleware union and
regenerate all consumers.

## BR-04 — medium, actual initial policy deviation

`internal/incident/domain.go:14–15` (reviewer corrected earlier 17–18 typo).
Formal 04§11.3 initial defaults are 10m correlation/30m reopen; implementation
30m/1h overaggregates/reopens older incidents without an authorized adjustment.
Violates 04§11.3/10§14.5/III.1. Independent real PG 11m and 31m new-occurrence
counterexamples must create new Incident; tests based on constants are insufficient.

## Intermediate code re-review, still NOT final PASS

Both reviewers accepted repair direction for lifecycle, precision, frozen input,
Graph-source fence, defaults and typed contracts; actual final evidence remains
pending. Full reviewer independently ran selected finding/incident/rca unit
regressions, exit 0. Boundary reviewer independently ran the formal-window unit
regression and current diff whitespace check, exit 0. These do not replace live
acceptance. No VM, performance waiver or PyRCA disabled item was misclassified
as a defect or passing capability. Reviewers remain independent/read-only.
