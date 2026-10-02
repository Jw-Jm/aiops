# SP-04 nonvirtual acceptance ledger

Status: in progress; no acceptance or independent review pass claimed.

Baseline: platform Git HEAD `4738c1148c6bec7003dd85f1831a6059dfab86e1`.
Initial `git status --short` was empty. Parent ops directory is not a Git repository.
User modifications: none present at start. Root 00–10 remain unchanged.
Scope authority: user instruction 2026-10-01 and ADR-0018; virtualization deferred
under ADR-0008. Historical reports are references, not current run evidence.

## Task → specification → implementation → verification

- 4.1: 04 §5–6; 08 §4–5; 10 §4.1/14.2 → internal/resource/{canonical_id,entity,alias,resolver}.go → identity golden, ambiguity, tenant/cluster/recreation tests → implementation and current isolated verification exist; final repair-bound full gate and independent closure pending.
- 4.2: 10 §4.2/14.3; 08 §49.3 → internal/integrations/kubernetes/{informer,projector}.go and internal/resourcestore/{snapshot_scope,repository}.go → Fake API + owned OrbStack List/Watch, 410, tombstone, throttling, permission and silence tests → implementation and current isolated verification exist; final repair-bound full gate and independent closure pending.
- 4.3: 10 §4.3/14.3; 07 §30; 08 §49 → internal/graph, internal Graph OpenAPI, Worker routing → locked upstream conformance, Lease faults, scope/cursor/cache/fencing/generation/race + depth2/200 performance → implementation and current isolated verification exist; final repair-bound full gate and independent closure pending.
- 4.4: 10 §4.4/14.2; 08 §50 → internal/evidence and Victoria adapters → native account/mandatory label isolation, malicious selectors, discovery/top-k/cache/archive, outage, redaction, budget + live latency → implementation and current isolated verification exist; final repair-bound full gate and independent closure pending.
- 4.5: 10 §4.5; ADR-0006 → internal/integrations/deepflow → v7.2.0 frozen fixture + live if available, semantic operations, Canonical mapping, no ClickHouse/SQL, honest capabilities → implementation and current isolated verification exist; final repair-bound full gate and independent closure pending.
- 4.6: 10 §4.6 + inspection reuse lock → internal/integrations/redfish and inspection/hardware → three vendors, failure/partial/unknown identities and selected Coroot/NPD/Metal3 semantics → implementation and current isolated verification exist; final repair-bound full gate and independent closure pending.
- 4.7: 10 §4.7/14.4; 05 §25.2/25.5; 08 §51 → Evidence metadata/archive/retention, public API, startup + forward-only migration → crash on both sides, digests, key version, orphan recovery, hold race, reference closure, source expiry + online/offline E2E → implementation and current isolated verification exist; final repair-bound full gate and independent closure pending.

## Required gate ledger

- check-toolchain: preliminary command exit 0, versions match; final bound run pending.
- check-generated/make check: current candidate run passed (`make-check-next.log`), repairs after that run require final rerun. Security previous candidate passed; final repair-bound rerun pending. Locked upstream replay current run passed two tests, zero skips (`upstream-replay-restored.log`), exact source locks preserved.
- owned dependency integration/E2E, current-source live collector/evidence/API/Worker: pending.
- identity: zero false merges, ≥99.9% deterministic recall: pending.
- key relation Precision/Recall and Graph/Evidence latency (samples/scale/method): pending.
- affected source/license/runtime closure: selected original source/notices and provenance included; runtime closure generation/check passed (`runtime-source-selected-prepare.log`, `runtime-source-selected-check.log`). Current-source signed Bundle and actual isolated offline deployment/reinstall remain pending.
- Independent read-only reviewer `/root/sp04_independent_review`: first whole delivery FAIL, nine confirmed defects and two evidence gaps. Main repairs and regressions in progress; details in `independent-review.md`. No final review pass.

KubeVirt/CDI, VM/VMI/DataVolume exclusive work: deferred, disabled/unverified;
never counted as passed. Pending entries must not be replaced by skip, docs, PoC
or default unit-test success.
