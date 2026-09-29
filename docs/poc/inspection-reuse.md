# Inspection, incident and hardware reuse boundary

Task 2.9 non-virtual source, rule and model reviews are recorded in
[`inspection-reuse-lock.yaml`](inspection-reuse-lock.yaml). The lock fixes the
upstream source archive, commit, file-level license evidence, selected file
hashes, dependency inventory, platform mapping, disconnected replay and
consumer boundary for each capability.

The review covers eight non-virtual capabilities. Seven bounded upstream
surfaces have a passing source/model/rule PoC and a complete dependency license
review. K8sGPT's deterministic CLI path passes, but the 268-module Linux CLI
closure contains five Buf generated modules without file-level license
evidence, so K8sGPT remains disabled. Every corresponding Component Catalog
entry remains `candidate`; the contract test confirms a candidate is rejected
from a Bundle. `qualified` in this lock refers only to the bounded source or
fixture evidence listed for that capability. It does not enable a resident
runtime, hardware access, profile selection or Bundle inclusion.

## Non-virtual decisions

| Capability | Exact source | Locked review scope | Result and boundary |
| --- | --- | --- | --- |
| K8sGPT Analyzer | `v0.4.36`, `d33935a7b6f7039ec649925420d6b29c9b119567` | Analyzer tests, arm64 CLI build, deterministic Pod JSON output | CLI JSON is deterministic without an LLM or `--explain`; five dependency licenses remain unresolved, so capability disabled. |
| Node Problem Detector | `v1.35.3`, `e2b57e2f42052e4cf62cd2be1eb882f6a71a7804` | `kernel-monitor.json` rule matching against VictoriaLogs LogsQL | 28 positive/negative vectors match; only the rule mapping is selected. NPD daemon stays disabled. |
| Coroot Community | `v1.26.8`, `76fa0cff31c530d1a2b0e8adc662433eea2bfeef` | `Check`, `CheckConfig`, `AuditReport` selected model/evaluation slice | The selected Check/Audit boundary test passes. Coroot collectors, storage, watcher and full runtime are excluded. |
| Keep Community | `v0.54.3`, `118b2dc0c7a45f8a22b6317b983cdba6f5b54b5e` | Six selected Community model files and their Python import closure | The offline SQLite/DTO PoC passes. `ee/`, providers, API, UI and a Keep server are excluded; tenant identity is not projected as public DTO data. |
| Metal3 BMO | `v0.14.1`, `767846c3f3a8b2426cbc5d5fc3b0f4a8565fa440` | `metal3.io/v1alpha1` hardware resource model | API type tests pass; BMO controllers, Ironic and provisioning are excluded. |
| Gofish | `v0.26.0`, `af1cf20386d538767ed9b6dd3a770736ab90e2d0` | Redfish ComputerSystem and LogEntry schema decoding | Selected schema tests pass; BMC credentials, write operations and an exporter are excluded. |
| IPMI exporter | `v1.10.1`, `291d0107bdab44df5a7ff50ce552ee3ccc23b52a` | Upstream collector with a deterministic fake `ipmimonitoring` CSV command | Synthetic temperature, fan speed and warning metrics pass; no BMC, credentials, host device or native FreeIPMI installation is used. |
| smartctl exporter | `v0.14.0`, `ef5c03de02cb793e6a1540bef943fe6a167de635` | Upstream parser/collector with its checked-in JSON fixture | Device identity, SMART state and capacity metric assertions pass; no block device, host scan or smartctl process is used. |

## Reproducible evidence

- The initial reuse-lock contract failed because the dependency-closure
  inventory had not been created:
  `artifacts/test-reports/task-2.9-acceptance-red.log`. The final contract
  passes against the locked inventory, mappings, offline logs and candidate
  admission checks:
  `artifacts/test-reports/task-2.9-contract-green-final.log`.
- K8sGPT upstream Analyzer tests: `artifacts/test-reports/task-2.9-k8sgpt-offline-run.log`.
- K8sGPT Linux arm64 build: `artifacts/test-reports/task-2.9-k8sgpt-build-offline.log`.
- K8sGPT CLI JSON fixture, run twice with a loopback synthetic Kubernetes API and no provider configuration: `artifacts/test-reports/task-2.9-k8sgpt-cli-offline-run.log`. The locked command uses JSON output and contains no `--explain` argument.
- Dependency inventory and license evidence for Go and Python selected roots: `artifacts/test-reports/task-2.9-dependency-closure-offline.log` and `test/fixtures/upstream-inspection/dependency-closures.json`. Go import closure inventory ran with `GOPROXY=off` in a `--network none` container. Keep wheel hashes match its exact Poetry lock; package preparation is separate from the no-network PoC run.
- NPD/VictoriaLogs equivalence: `artifacts/test-reports/task-2.9-npd-victorialogs-final-offline-run.log`; the replay uses the locked VictoriaLogs `v1.52.0` image digest and compares the same RE2 pattern with an end anchor over 28 vectors.
- Coroot selected Check/Audit boundary: `artifacts/test-reports/task-2.9-coroot-final-offline-run.log`.
- Keep model/SQLite/DTO projection: `artifacts/test-reports/task-2.9-keep-model-final-offline-run.log`.
- Metal3 API model: `artifacts/test-reports/task-2.9-metal3-offline-run.log`.
- Gofish schema decoding: `artifacts/test-reports/task-2.9-gofish-offline-run.log`.
- IPMI synthetic collector: `artifacts/test-reports/task-2.9-ipmi-fixture-final-offline-run.log`.
- smartctl parser and fixture: `artifacts/test-reports/task-2.9-smartctl-fixture-offline-run.log`.

Replay commands, exit codes, container image digests, and log hashes are
machine checked by `test/contract/upstream_reuse_test.go`. The platform mapping
documents and their index are hash-checked with their source and conformance
fixtures. The complete lock is also checked against `third_party/manifest.yaml`
and the Component Catalog; these eight entries remain candidates and cannot
enter a Bundle.

## Executable non-virtual replay

The opt-in `TestInspectionNonVirtualUpstreamReplay` reconstructs the frozen
Git archives in disposable directories and invokes
`test/fixtures/upstream-inspection/replay-nonvirtual.py`. It checks archive,
selected-file, Fixture, license-evidence and Keep wheel hashes before using
them. Its containers use the explicit OrbStack context, digest-locked images,
`--network none`, `--pull=never`, read-only sources/module cache and
`GOPROXY=off`. Keep installs only the exact wheel inventory with
`--no-index --require-hashes --no-deps`; there is no runtime download fallback.

```sh
OPS_INSPECTION_REPLAY=1 \
OPS_INSPECTION_SOURCE_DIR=/absolute/prepared/upstream \
OPS_INSPECTION_KEEP_WHEELS=/absolute/prepared/keep/wheelhouse \
OPS_INSPECTION_REPLAY_OUTPUT=/absolute/empty/evidence-directory \
GOPROXY=off GOSUMDB=off \
go test ./test/contract -run '^TestInspectionNonVirtual(ReplayEntryPoint|UpstreamReplay)$' \
  -count=1 -timeout=30m -v
```

The source directory contains the eight Git repositories named in the script;
only their frozen commits are used, regardless of checkout changes. Replays
never promote a capability or Component Catalog entry. The default Contract
test rejects virtual capability names and missing source; the actual upstream
replay is skipped unless explicitly enabled. K8sGPT's license blocker and the
Fixture-only hardware boundaries still apply after a passing replay.

### Verification on 2026-09-29

The final complete replay exited 0 in 175.577 seconds. Its command, source
parent and exact runner/Contract-test/lock hashes are in
`artifacts/test-reports/task-2.9-completion-live-v3.log`. All eight capability
logs and their hashes are in
`artifacts/test-reports/task-2.9-completion-replay-v3/summary.json`.

K8sGPT Analyzer tests, a fresh arm64 CLI build and repeated deterministic JSON
output passed. NPD's upstream `TestPush`/`TestMatch` source tests and all 28
VictoriaLogs equivalence vectors passed. Coroot Check/Audit, Keep SQLite/DTO,
Metal3 API types, Gofish schemas, synthetic IPMI metrics and SMART fake-data
metrics also passed without network or image pulls.

Two runner preparation failures are preserved in the earlier completion logs.
The first selected the unneeded NPD daemon package and failed because its
extra dependencies were absent in the read-only cache. The runner now tests
the selected upstream matcher files. The second omitted the SMART fake-data
reader's `debug/sdr.json` path. The runner now binds that path to the locked
upstream JSON Fixture. Neither correction changes upstream versions, source,
licenses, runtime admission or hardware validation claims.

`task-2.9-completion-check-final.log` records exit 0 for the locked toolchain,
uncached reuse/graph/projection Contract tests, `make check`, generated-file
drift checks and `git diff --check`. The original eight Component Catalog
candidates, disabled K8sGPT license gate and deferred virtual capabilities are
unchanged.

### Exit and disable conditions

Future consumers must use the locked selected surfaces and public platform
projections. Source/hash/license drift, a changed import closure or a failed
offline Fixture stops that capability until its lock and evidence are reviewed.
No replay enables a full Coroot/Keep/Metal3 runtime, a hardware exporter or
privileged collector. Preserve disabled status when required evidence is
missing; do not substitute a generic in-house Inspection framework.

K8sGPT can leave disabled status only after the five frozen Buf dependency
licenses are established and its CLI closure and no-LLM JSON tests are rerun.
NPD rule reuse still requires real journald/kernel ingestion to preserve
message, node and timestamp semantics in the later adapter task; a failed
mapping must follow the plan's NPD admission path. Keep remains a selected
Community model boundary; any broader extraction must close its dependencies
and exclude `ee/` before SP-05 consumes it. Hardware Fixture results do not
authorize device access or establish live hardware compatibility.

## Virtual capabilities deferred

KubeVirt observability rules, KubeVirt must-gather and the Kubernetes MCP
KubeVirt toolset remain explicitly `disabled` and deferred under
[`ADR-0008`](../adr/0008-defer-kubevirt-cdi-development.md). Existing evidence
is retained without additional virtual PoC, deployment or admission work. No
VM/VMI/DataVolume capability, privileged collector, or `virtualization/full`
profile is enabled by this review.
