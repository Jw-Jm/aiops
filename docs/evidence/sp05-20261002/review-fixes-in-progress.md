# Independent review and repair record — not final acceptance

Candidate: `2523962336b5c205f7f55b46609aff13a6e40b34`.
Reviewers: `/root/sp05_full_independent_review` and
`/root/sp05_runtime_boundary_review`; neither participated in implementation.
First decisions: REQUEST_CHANGES and NEEDS FIXES, final evidence pending.
Current uncommitted repairs are implemented by the main agent. No final PASS.

- IR-01 / P2: suppressed expiry opened all-resolved groups; explicit transition
  whitelist and expiry now use actual linked activity, close inactive groups,
  release active hold and preserve retention. Real PG branch/audit/outbox proof.
- IR-02 / P2: RCA input lacked Finding versions. Bounded frozen versions,
  applied-revision fence, exact digest and current/historical semantics added.
- IR-03 / P2: actual responses lacked typed versioned public contracts.
  Additive schemas and regenerated consumers cover wire responses; live validation
  retains strict legacy/v2 errors. New schema validation exposed nil ontology
  slices and currentGraphRevision omissions; fixed DTO/schema definitions.
- IR-04 / P2: float64 digest rounded distinct precise numeric payloads.
  UseNumber preserves tokens; unit red/green and PG conflict regression.
- IR-05 / P2: merge/split lost all-resolved recovery clocks. Actual resulting
  linked groups recompute recovery, with missing-Graph recovery fencing.
- IR-06 / necessary evidence gap: final current source-bound gates, Bundle,
  offline Chart and complete re-review still pending. Not waived or closed.
- BR-01 / medium: terminal/empty page nextCursor:null violated string schema.
  Omit the absent cursor; strict actual page validation.
- BR-02 / high: Graph-only source revoke/rotation bypassed confirmed commit.
  Frozen authority revision/scope digest plus same-TX FOR SHARE and historical
  authorization; real confirmed DIMM source withdrawal and row-lock regressions.
- BR-03 / medium: STALE_CONTEXT and SOURCE_CAPABILITY_UNAVAILABLE were emitted
  under a closed old error declaration. SP05 emits versioned v2 standard errors
  with explicit legacy union and actual response validation.
- BR-04 / medium: 30m correlation/1h reopen deviated from formal 10m/30m.
  Formal defaults restored; independent fixed 11m/31m PG counterexamples.

Current original failures and subsequent successes are preserved; filenames that
contain red do not alone identify a business failure. Setup failures and harness
corrections must be distinguished from product defects. The original golden
expected manifests were not regenerated from current output.

VM work remains deferred/unverified; performance is user-exempt, not PASS;
PyRCA remains disabled/excluded. None is a review defect or admitted capability.
