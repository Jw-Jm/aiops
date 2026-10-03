# ADR-0019: SP-05 nonvirtual development authorization

Status: Accepted scope; implementation acceptance pending

On 2026-10-02 the user explicitly authorized Tasks 5.1, 5.2, 5.3, 5.5,
5.6, 5.8 and the DIMM/PVC-CSI/Node slices of 5.7. This supersedes the old
SP-02 stage restriction in AGENTS.md. ADR-0008 remains effective. Root 00–10
specifications are unchanged. Task 5.4 and VM start/network slices remain
deferred, disabled and unverified. SP-06–09, LLM investigation, command
execution and a third resident service are not authorized.

Dedicated performance, capacity, sustained load and P95 tests are waived by
the user, including 100 Findings/s for 15 minutes. This is not a passing
performance gate. Correctness, concurrency, security, crash recovery, real
OrbStack Inspection and affected offline runtime checks remain required.
PyRCA requires every original admission condition; absent complete proof it
stays disabled and excluded from runtime dependencies.

Implement 10 Tasks5.1–5.8/§14.4–14.5, 04§33, 05§25, 06§41, 07 contracts
and 08§51 using forward-only migrations and versioned public contracts.
The existing API and Worker own all runtime paths. Independently review the
complete authorized delivery after implementation; fix confirmed defects and
required evidence gaps, then repeat review until explicit final PASS.
Evidence is recorded in docs/evidence/sp05-20261002/ and cannot reuse historical
reports as current execution proof. Tests own isolated resources and preserve
shared services/data. Hardware fixtures do not establish physical BMC testing.
