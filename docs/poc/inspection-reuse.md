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

## Virtual capabilities deferred

KubeVirt observability rules, KubeVirt must-gather and the Kubernetes MCP
KubeVirt toolset remain explicitly `disabled` and deferred under
[`ADR-0008`](../adr/0008-defer-kubevirt-cdi-development.md). Existing evidence
is retained without additional virtual PoC, deployment or admission work. No
VM/VMI/DataVolume capability, privileged collector, or `virtualization/full`
profile is enabled by this review.
