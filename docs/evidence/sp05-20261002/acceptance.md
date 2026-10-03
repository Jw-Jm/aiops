# SP-05 nonvirtual execution acceptance — PASS

All authorized business implementation and necessary current execution gates are
complete. Both read-only independent reviewers explicitly gave final complete
PASS; their decisions and closed findings are in
[final-independent-review.md](final-independent-review.md). Historical SP01–04
reports are context only; the commands and raw evidence below ran in this SP05 session.

## Source and scope

Actual initial baseline: c2068d5912a4383c85e9f5e2733d2b53ef185aed, branch main,
clean worktree, no pre-existing user modifications (baseline.json).
Parent `/Users/mssc/Documents/Code/ops` is not a Git repository.
First development candidate: 2523962336b5c205f7f55b46609aff13a6e40b34.
Business repair: 64a1d23d6a9619cc06feeb4be5cd4f395b699283.
Final tested/source-bound material: c1cbcaa4b02c7ca9df45d9fa6e1e1dfac0967049.
111fc5a and c1cbcaa only extend strict test consumers/review records; equality
of api/internal/migrations/deploy/build/scripts/bundle/gen/web to business repair
64a1d23 was verified with `git diff --exit-code`, exit 0. Final evidence-only
commit is recorded after reviewer decisions; it must not claim a new runtime build.
Root formal 00–10 documents were not edited. ADR0019 supersedes the SP02 stage
limit under explicit user authorization; ADR0008 virtualization deferral remains.

Changes include forward-only migrations 00024–00033, Finding/Incident/RCA
runtime packages, official/K8sGPT/NPD adapters, selected licensed upstream models,
versioned Recipe/output contracts and generated Go/TypeScript consumers,
API/Worker startup, signed Chart/image/offline materials, regression Fixtures,
Runbooks and platform/docs evidence/ADRs 0019–0025. No investigator, LLM main
chain, general command execution, SP06–09 implementation or third resident
production service was introduced.

## Authorized Task outcomes

- **5.1 implemented and validated**: trusted tenant/source binding, tenant+
  sourceFingerprint+occurrence uniqueness and event/key transport ledger;
  same transaction inbox claim/Finding mutation/timeline/outbox. Server receivedAt
  distinct from event time. Actual Worker consumer/retry/quarantine/lease recovery.
  Tests cover firing/duplicate/update/resolved/resolved-first/late/replay/digest
  conflicts/poison/concurrent input/transaction and delivery faults, real SIGKILL.
  Precision repair preserves integer/decimal tokens; 1 and 1.0 are intentionally
  distinct payload spellings, key order/whitespace normalize. v1 history remains
  preserved/unverified and explicitly quarantined; additive v2 is current ingress.
- **5.2 implemented and validated**: selected Community MIT value-model facts and
  licensed minimal state machine, subject locking/CAS/Timeline/override audit,
  exact resource+family+symptom+policy fingerprint. Formal 10m correlation/30m
  reopen defaults; suppression expiry chooses activity-aware open/closed,
  merge/split recompute actual group recovery and preserve full settle.
  Service and actual OIDC HTTP mutation tests cover current/old revision,
  linked scope, cached replay, revocation, suppression/recovery and crashes.
- **5.3 implemented and validated within declared input coverage**: official
  Conditions/Events/Metrics/probe first, unchanged locked no-LLM CLI via static
  argv/JSON/time and resource limits/finite read-only native Pod/read-only broker,
  selected NPD kernel results. Frozen 19-case official set and 22-case
  Metrics/control-plane set: Node TP5, scheduling TP1, storage TP4, Metrics TP2,
  control-plane TP4; each domain FP=FN=0, precision=recall=100%, satisfying
  95%/90% on these small independent fixed samples. Native OrbStack positive
  Conditions/Events/CLI and controlled probe failure/degradation/durable archive
  verified. Native Metrics-server API absent 404: positive Metrics remains a
  protocol Fixture plus actual native Node UID/authorization/archive chain.
  No positive live Metrics-server or production accuracy claim.
- **5.5 implemented and validated**: three nonvirtual versioned signed Recipes,
  entry/depth/evidence/predicates/allowlist/budget/postcheck, actual Registry
  publish/activate/consumer path. Supporting/Contradicting/Missing/DegradedSources/
  Ranked use locked ontology Evidence/Provenance/Conflict/RankedEvidence/Subgraph
  core. No generic ranking/conflict replacement or LLM scoring. All valid/conflict/
  missing/degraded variants; duplicate/derived evidence cannot double-weight.
  Historical Node Recipe v1 stays executable; v2 adds native hardware alternatives.
- **5.6 implemented and validated**: deterministic candidates, confirmation
  predicates, probable/unresolved, append-only RCAInput/Revision v2 with actor,
  source/provenance, true Evidence IDs and archived authorized readback.
  Frozen sorted bounded Finding revisions and contributing Graph source revisions/
  scope digests fence publication; actual PG share locks serialize withdrawal.
  Ranking, CPU utilization and recent change alone never confirm; competing root
  hypotheses, absent causal proof, required-source degradation remain unresolved.
  Impact uses the existing bounded Ariadne/ontology graph/subgraph query, with
  direct/indirect/path/provenance/freshness and partial/unavailable semantics.
  Current eligibility additionally checks current Finding/Graph/Recipe/authority;
  authorized history survives runtime outage, withdrawal denies read. SP04 shared
  365-day dependency/Legal Hold/archive protection regression passed.
- **5.7 three nonvirtual golden slices validated**: DIMM/PVC-CSI/Node frozen
  versioned manifests/SHA256/clock and independently authored full expected
  Entity/Relation/Finding/Incident/Evidence/Candidate/RCA/Impact. Twelve variants,
  plus four Node hardware-upstream variants, actual production inspection/
  ingestion/reducer/Graph/Registry/PG/Transit/TLS-IAM archive paths, full second
  replay semantics and physical retention dependency checks. Missing/conflict/
  degraded cases remain unresolved/partial. Hardware/CPER faults are fixed
  protocol/kernel Fixtures, not physical injected hardware faults. Three Runbooks
  reside in docs/runbooks; native live PVC/scheduling verifies actual entry chain.
- **5.8 evaluated, disabled/excluded**: unchanged locked Salesforce PyRCA selected
  EpsilonDiagnosis PoC, locked dependency/fixture train/holdout hashes, same frozen
  nine-case holdout against deterministic Candidate/Metric Evidence baseline.
  Baseline Top3=9/9, PyRCA=6/9, absolute gain −33.333 percentage points, zero
  false confirmed; PoC only adjusts rank, never confirms. Full formal dependency,
  resource/latency and real production admission unproven. ADR0020 keeps disabled;
  no Python/PyRCA PoC dependencies in API/Worker runtime images or selected Bundle.
- **5.4 and VM launch/network parts of 5.7 deferred/unverified**, not PASS;
  source/Fixtures/failure history retained. No KubeVirt/CDI resource development.
- **Performance exempt by user**, not PASS: sustained Finding load, dedicated
  performance/capacity tests, repeated latency sampling/P95 not executed. Ordinary
  correctness deadlines/observability durations are not performance admission.

## Current execution proof

Machine-readable complete commands, exits and source binding:
[final-gates.json](final-gates.json), [commands](current-run/commands.json).
All necessary final command exits are **0**. Raw failures and subsequent success
remain in current-run and earlier development records; setup failures, exhausted
host disk and harness defects are explicitly distinguished from product failures.

- check-toolchain, check-generated, check-runtime-source, make check,
  test-security: [current complete log](current-run/final-reviewed-common-gates.log).
  Required security report 11 pass/0 skip. General unit/default suites may contain
  opt-in/helper skips; they do not substitute for explicit actual gates below.
- SP05 explicit mandatory integration: 47 pass events/0 fail/0 skip,
  [raw](current-run/post-review-complete-integration.jsonl). Actual OIDC HTTP three
  mutation additions: 1/0/0, [raw](current-run/br05-actual-http-mutation-contract.jsonl).
  Real SIGKILL parent executes its child; child-only helper is not a standalone
  skipped mandatory gate. Business code is equal to final c1cbcaa as stated above.
- Current upstream replay: 2/0/0, [raw](current-run/post-review-offline-upstream-replay-recovered.log).
  Exact original Go/Python tool images prepared first; runtime replay uses
  `network=none/pull=never` with verified original commits/files/dependency caches.
  Host transport fallback imported the original publisher OCI root digest and
  authenticated native closure; only local reference annotation matched the lock.
- Selected SP01–03 shared regression: 13/0/0,
  [raw](current-run/shared-sp01-sp03-current-regression-complete.jsonl).
  Tenant/RLS, source revision/scope migration, Registry, transaction/audit,
  idempotency, real IAM/TLS and Transit/SeaweedFS archive.
- Selected SP04 shared regression: 19/0/0,
  [raw](current-run/shared-sp04-current-regression-complete.jsonl).
  Native Lease wire/recovery/failover, audit Worker, fixture adapters, Victoria
  source isolation/revocation, archive integrity/historical keys/Hold/365-day
  protection and actual OIDC API/Worker/source outage chain.
- Shared nonvirtual E2E: 22/0/0,
  [raw](current-run/shared-nonvirtual-current-e2e.jsonl): real OrbStack List/Watch,
  core S3/OIDC/Bootstrap and bounded DeepFlow context contracts. Future standalone
  DeepFlow/VM admission is not implied.
- Source-bound signed Bundle preparation/build/verification all exit 0,
  [source binding](current-run/final-source-binding.json),
  [full signed manifest](current-run/final-bundle.lock.json),
  [verification](current-run/final-reviewed-signed-bundle-verify.log).
  24 materials/72 authenticated files, complete selected source/license closure.
  Runtime Go source 19b0c348...207ed, unchanged CLI source 2e342248...7e92 and binary
  6f9152ff...af53 remain separate bindings; original publisher notices preserved.
- Actual native signed Chart/offline gate: 1/0/0,
  [raw](current-run/final-reviewed-native-signed-chart.jsonl). Current source images,
  signed three Recipes, real Worker main/JWT bootstrap/fixed CLI finite Pod,
  verified Finding/Incident/archive/causal unresolved RCA and typed OIDC API;
  CNI positive-public-before/negative-public-after, no pull/Never; selected API/
  Worker cache cold import, release uninstall/reinstall. Shared dependency services
  and data were preserved; this is selected first-party cold regression, not a
  claim that every shared core dependency was wiped or physically reinstalled.
- Frozen quality and PyRCA: [quality](current-run/current-frozen-domain-quality.log),
  [disabled comparison](current-run/current-pyrca-disabled-evaluation.log).

## Independent repair loop and final PASS

[First full reports](independent-review-round1.md),
[repair and additional evidence record](review-fixes-in-progress.md).
Independent reviewers `/root/sp05_full_independent_review` and
`/root/sp05_runtime_boundary_review` did not implement anything. IR01–06 and
BR01–05 have been accepted closed after actual fixes/regressions and final
evidence verification. Both reviewed the complete scope again and explicitly
gave final PASS, independently executing final signed Bundle verification as
well. No remaining in-scope confirmed defect or necessary evidence gap remains.
See [final independent decisions](final-independent-review.md).

Host capacity exhaustion interrupted intermediate Bundle/replay/common checks.
Only this run's superseded temporary materials were cleaned with signed metadata
retained. Original owned container IDs, shared services and Bao persistent Raft
state were preserved; actual changed ephemeral ports were rediscovered. User
released more disk space. Subsequent successful gates above replace the failed
attempts as execution proof; earlier failures remain evidence, not rewritten.
