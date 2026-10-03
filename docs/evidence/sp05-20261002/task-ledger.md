# SP-05 Task → specification → implementation → test → evidence

State: development in progress; no acceptance or independent PASS yet.
Baseline and user modifications: baseline.json. ADR-0019 records authorization.

- 5.1 — 04§33.1–2,05§25,10§14.5 → internal/finding, Finding v2,
  migration00024, HTTP ingest, Worker relay → reducer and real PostgreSQL
  duplicate/order/concurrency/rollback/recovery tests → pending.
- 5.2 — 04§33.3,06§6,10§14.5, Keep selected closure → internal/incident,
  same-transaction correlation inbox/subject lock/timeline, state CAS,
  recovery/reopen/merge/split → unit/integration/runtime → pending.
- 5.3 — 10Task5.3, inspection-reuse-lock/ADR0013 → official signal adapter,
  fixed no-LLM K8sGPT and NPD → frozen labels by domain and real OrbStack → pending.
- 5.5 — 06§36/41,08 diagnostic semantics, locked ontology → versioned
  recipes and evidence projection/confirmation predicates → valid/conflict/
  missing/degraded/schema/registry/consumer fixtures → pending.
- 5.6 — 06§12/14/15/41,08§51 → deterministic candidates, immutable RCA
  input/revisions and current CAS, existing bounded Graph impact, retention
  references → causal counterexamples, permission/archive/expiry/Hold → pending.
- 5.7 — 06§20–22,10Task5.7 → frozen manifest/clock/expected full-chain
  DIMM/PVC-CSI/Node inputs → red before implementation, twice replay plus
  missing/conflict/degraded → pending. VM start/network deferred/unverified.
- 5.8 — 10§3.10/Task5.8,06§10 → frozen holdout and optional PoC adapter,
  explicit admission decision → same holdout Top3/false-confirmation comparison;
  no runtime admission without all conditions → pending; disabled by default.
- 5.4 — deferred under ADR0008; existing source/fixtures/failures retained.
- Performance — user waived; no sustained throughput/P95/capacity proof.

Required current gates: check-toolchain, check-generated, make check,
test-security, original upstream replay, selected SP01–04 shared correctness,
SP05 PostgreSQL and real API/Worker/OrbStack integration/E2E, affected
runtime source/license and offline regression, final independent full review.

## 2026-10-03 continuing implementation (not final acceptance)

- Actual native golden entry now calls the production `InspectSP05Snapshot`,
  `SubmitSP05Candidate`, `SubmitSP05KernelEvidence` and `SP05Reducer.Pass`.
  The Node kernel input is queried through the current authorized VictoriaLogs
  adapter with positive and negative source-isolation probes; no synthetic
  Evidence object substitutes for that query.
- `golden-valid-impact-protection.log`: exit 0, three valid cases, twice replay.
- `golden-native-all-variants.log`: exit 0, all 12 nonvirtual scenario/variant
  combinations, real isolated PostgreSQL, signed Recipe publication/activation,
  real persistent OpenBao Transit and TLS/IAM S3, immutable archive readback,
  effective 365-day dependency reference and physical object protection.
  Hardware and Node failure inputs are protocol Fixtures, not physical live faults.
- Event -> subject graph references incorrectly propagated impact to observations;
  `impact-native-event-red.log` reproduced the product defect (exit 1). The
  corrected reader preserves diagnostic Event references while excluding Event
  impact propagation. `impact-native-event-targeted-green.log`: exit 0.
- `impact-native-event-green.log` is an interrupted broad graph conformance
  invocation (exit 1, SIGQUIT during 20k snapshot construction), **not PASS**.
  It performed no P95 assertions; only the targeted regression is current proof.
- Native Node query replay already passed without a replay mutation defect:
  `node-native-query-replay-red.log` has a historical name but **exit 0**. Do not
  count its filename as failed evidence.
- CLI caller-selected digest acceptance was reproduced in
  `analyzer-binary-admission-native-complete-red.log` (exit 1). Runtime/Chart now
  require the fixed original binary hash; `analyzer-fixed-runtime-hash-green.log`
  exit 0. Earlier config-test compilation/setup failures remain preserved.
- CLI corresponding source package authenticates all 233 exact publisher module
  zips, complete MPL module source, original licenses/notices, original upstream
  tar, generated SDK provenance and Go 1.27.1 license. Current package result is
  `k8sgpt-runtime-source-go-notice.log`, exit 0. Candidate runtime admission is
  still pending Worker startup, distribution and affected offline gates.
- `worker-offline-material-first.log`: exit 0, actual no-network/no-download
  locked Go/CLI rebuild and scratch Worker image. Later source changes require
  rebuilding this material before final gates.
- Node hardware upstream/competing root hypotheses are being implemented with a
  new signed Node Recipe version; `node-hardware-causal-red.log` is the initial
  business failure. No independent review or final delivery PASS exists yet.
- `toolchain-20261003.log`: exit 0. Performance-specific gates remain waived.

## 2026-10-03 further correctness implementation, still not final acceptance

- `golden-all-frozen-rules-provenance.log`: exit 0, all 12 golden combinations,
  now additionally checking frozen rules, lifecycle states and independent versus
  derived Evidence counts. Existing same-ID second replay still needs a complete
  post-check replay test; a 30-second reducer scheduling gate is not that proof.
- Signed Node Recipe v2 retains executable historical v1; its alternative requires
  located native fatal CPER memory, current uncorrectable ECC and the exact
  hardware -> Node path. Ranking uses locked ontology. Competing runtime/hardware
  causes are unresolved. `node-hardware-native-cause-green.log`,
  `node-v2-registry-executable-green.log`, `cper-fatal-memory-section-green.log`
  each exit 0; their original business failures remain preserved.
- Current RCA now requires current signed activation, fresh owned matching Graph
  and reference/source authority. Historical stored confirmed is not automatically
  currentEligible. `current-api-orbstack-regression.log`: exit 0; further endpoint
  eligibility/revocation integration remains necessary.
- `kernel-transport-native-complete-red.log`: exit 1 reproduces stdout/journal/
  missing transport interpreted as kernel proof. Initial red log also contains a
  test setup canonical-ID error, not that business defect. CPER malformed-boundary
  red is in `kernel-transport-boundary-red.log`. Fixed adapter, Inspector and RCA
  checks: `kernel-transport-boundary-green.log`, exit 0.
- Node v4 input origin and SHA-256 manifest were frozen before its first vertical
  run; original v1-v3 remain. `node-native-origin-v4-green.log`: exit 0, all four
  variants with real DB/archives and unresolved missing/conflict/degraded results.
- First healthy polls previously rolled back all state, allowing old firing to
  reopen. `first-healthy-poll-complete-red.log`: exit 1 genuine business failure.
  `first-healthy-poll-red.log`: exit 1 invalid test flag, preserved as setup failure.
  Durable inactive tombstones also advance on later healthy samples. Targeted
  ingestion/precision/restart/late replay: `first-healthy-poll-green.log`, exit 0.
- Official observations: 22 manually frozen supplemental labels precede code.
  `official-observations-prefrozen-red.log`: exit 1 missing implementation;
  `official-observations-prefrozen-green.log`: exit 0. Positive Metrics are protocol
  Fixtures, not a performance test or live Metrics-server admission.
- `official-observations-native-api-worker-green.log`: exit 0 actual OrbStack
  official reads, controlled 503/403, native recovery, withdrawn binding with no
  subsequent native reads, plus actual API/Worker/archive/restart regression.
  Real Metrics API is absent and honestly degraded; no shared cluster mutation.
  Initial native test logs preserve list decode and SQL test assertion errors,
  not reported as product regression or PASS.
- Locked original Analyzer's GetParent needs exact native owner reads for normal
  workload Pods. `analyzer-native-owner-red.log`: exit 1 genuine known-owner 403.
  Bounded UID-fenced, namespace/label/revocation-checked parent GET adapter and
  counterexamples: `analyzer-native-owner-green.log`, exit 0. No general discovery
  or workload execution is introduced. Full shipped Worker/Analyzer startup and
  distribution/offline gates remain pending.

## 2026-10-03 durable native Worker and complete replay continuation

- `make check` with `OPS_PERFORMANCE_EXEMPTION=sp05-user-20261002` exited 0 (`check-current-correctness-first.log`); SP04 archive, scoped Victoria and live Worker correctness/recovery remain executed. Dedicated repeated P95 sample loops and dense capacity/P95 paths are explicitly omitted; their original assertions remain in the non-waiver branch. This is a development snapshot, not the final source gate.
- `TestSP05AnalyzerNative...` native Deployment -> ReplicaSet -> Pod original CLI parent lookup exited 0 (`analyzer-native-deployment-owner-green.log`). Parent reads require observed exact owner UID, current source scope, fixed GET endpoints, depth/size/time bounds, and return metadata only.
- `TestSP05NativeWorkerAnalyzerDurable...` exited 0 (`native-worker-analyzer-durable-complete-first.log`): actual Linux Worker startup in an owned finite-resource, read-only-root OrbStack Pod runs the exact no-explain CLI and persists Finding, Incident and immutable Evidence. Temporary verification image `sha256:508bd6d02a4d42b3a1ecfa4cf0daf4a65a73b45a00044a5edee5e874d5b02f5b` is not the shipped Worker image or a third resident service.
- Identical external graph overlays previously advanced generation (`overlay-exact-replay-red.log`, exit 1). Canonical exact relation replay now preserves revision; changed clocks, provenance, TTL or relation fields still invalidate it (`overlay-canonical-replay-green.log`, exit 0).
- Second golden replay now forces the actual post-check reducer to execute again by making only its scheduling marker due. The first complete second-reducer run (`golden-complete-second-reducer-replay.log`, exit 1) exposed a harness that rewound current native facts to historical transport events. Historical events are still re-ingested; current Graph stays at the latest native observation. Conflict-only regression exited 0 (`golden-conflict-complete-postcheck-green.log`). No expected result, status, ID or revision assertion was weakened.
- Full DIMM/PVC/Node twelve variants and the manually prefrozen additional Node hardware upstream four variants exited 0 (`golden-full-reducer-hardware-upstream-first.log`, 110.757s). Actual native adapters, PostgreSQL identity/reducer, bounded Graph, signed registry and role-separated TLS/IAM OpenBao/SeaweedFS archives are exercised twice including full RCA post-check. Hardware remains protocol Fixture validation, not physical BMC/Node fault injection. Missing ECC, competing native kernel cause and unavailable required source do not confirm Node root cause.
- Worker material now includes original K8sGPT dependency notices plus a full corresponding-source distribution pointer. Current build (`worker-notices-owner-material.log`, exit 0) predates later changes and must be rebuilt for final delivery; catalog/distribution/offline admission remains pending.

## 2026-10-03 distribution and compatibility fixes

- New Worker material admission accepted a missing CLI source closure (`worker-source-admission-red.log`, exit 1). Versioned SP05 Worker material now requires qualified fixed CLI and its exact source tar; both omission and altered digest fail. Locked 233 artifacts and all reviewed publisher notices are represented in the Catalog and embedded evidence. `worker-source-admission-complete-green.log` exited 0. An earlier `worker-source-admission-green.log` exited 1 because the Catalog lacked the previously reviewed SPDX Unlicense identifier; it is not PASS evidence. Exact publisher notice and SPDX reference are recorded in docs/poc/sp05-k8sgpt-runtime-admission.md. Full signed current Bundle/Chart/offline proof is still pending.
- Native-format positive Metrics transport, schema drift, healthy recovery, real Finding/Incident and archive readback passed (`official-metrics-archive-chain-green.log`, exit 0). The first enhanced test used the wrong SQL rule-family filter (`official-metrics-archive-hardware-path-first.log`, exit 1); this was a test defect, not a Metrics business failure. Real Node reads/UID/source authorization remain native; positive Metrics API response is explicitly a protocol Fixture because this OrbStack cluster has no Metrics-server. Hardware upstream native path/provenance checks in that same first run passed all four variants.
- Historical schema events were silently unclaimed (`legacy-outbox-quarantine-red.log`, exit 1). Actual Worker delivery now preserves and quarantines them with UNSUPPORTED_HISTORICAL_SCHEMA, never synthesizing current evidence or marking delivered. Following v2 events continue. New ingestion is additive /api/v2/findings:ingest, while frozen v1 body/definition stays deprecated and disabled; source grants are never automatically widened. ADR-0024 records the exact boundary. Native OIDC API/replay/Worker restart, real SIGKILL claim recovery and legacy quarantine passed (`versioned-ingestion-legacy-recovery-green.log`, exit 0). Generated consumers and Contract tests passed (`versioned-ingestion-generated-check.log`, `versioned-contract-regression.log`, exits 0).
- The newly exercised actual current RCA API found a real missing required relationKinds array in its internal Graph query (`current-rca-real-api-outage-first.log`, exit 1): healthy current proof was always rejected by the internal Contract. Repair and real current -> stopped Worker -> authorized historical read -> restored Worker regression are in progress; no current capability PASS is claimed yet.

## 2026-10-03 current RCA and typed Metric Evidence

- `current-rca-real-api-outage-green.log` exited 0 after adding the required empty relationKinds array. Real OIDC API, signed internal mTLS Graph request, active Recipe, current Evidence references and routing/owner revision are checked. Current eligibility becomes false after stopping the actual Worker; authorized frozen revision remains readable; restarting rechecks a new owner before restoring eligibility. This does not promote the honest unresolved PVC result to confirmed.
- `native-metric-evidence-type-red.log` exited 1: official native-format Metrics were incorrectly archived as resource_state. The fixed admitted Metrics template and native Redfish current ECC now produce type=metric; caller fields cannot pick the type. `native-metric-evidence-type-green.log` exited 0 for actual ingestion/immutable readback, all DIMM variants, and all Node hardware upstream variants with native relation provenance and exact Pod impact. High CPU remains context and cannot satisfy a causal Recipe predicate.
- Current native signed Chart gate is extended to enable the new explicit source-schema grant, signed Recipe registry and fixed Analyzer hash. It will execute actual platform-worker main/JWT bootstrap and API/Evidence/RCA consumers after current signed image/source material preparation; its compilation alone (`native-chart-source-compile.log`) is not an executed live acceptance.

## Current immutable delivery candidate preparation

- Current runtime source closure check exited 0 (`runtime-source-current-final-preparation.log`): 95 modules, 2943 selected module files, 1377 Go standard-library files, SHA256 19b0c348370e339f8cb0c94276c17a88e9aede393269934b561b4938ede207ed.
- Upstream replay prerequisite preparation preserves both failures: absent tool image (`upstream-replay-restored-current.log`, exit 2) and initial preparer treating deferred entries as replayable (`locked-replay-image-preparation-current.log`, exit 1). Retry uses only declared nonvirtual offlineReplay images, exact publisher digests. Network is used for prerequisite preparation only; replay remains network=none/pull=never.
- First-party source binding records runtime Go source and unchanged CLI source/binary separately. Final source-bound material gates, full live acceptance and independent full review remain pending.
