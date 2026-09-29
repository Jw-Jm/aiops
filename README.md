# 智能运维平台 1.0

`platform/` is the platform's standalone Go monorepo. The repository keeps the API and worker as separate processes over one Go module.

## 当前开发范围

**KubeVirt 与 CDI 的开发、部署和运行验收延期。本轮先完成 SP-02 非虚拟化部分。** 编码前必须阅读 [AGENTS.md](AGENTS.md) 和 [ADR-0008](docs/adr/0008-defer-kubevirt-cdi-development.md)。

Task 2.6、Task 2.8/2.9 的虚拟化分项，以及后续 VM 功能和 virtualization/full Profile 不在本轮范围内。core 中两组件保持 disabled，运行兼容性为 unverified；已有证据保留。只有用户明确恢复虚拟化阶段后才重新开发。

## Development toolchain

Exact development versions are pinned in `.tool-versions`:

- Go 1.27.1
- Node.js 24.21.0
- pnpm 12.7.0
- Python 3.12.14
- uv 0.12.17

Install these versions with an `.tool-versions` compatible manager, then run `make bootstrap` to install when a compatible manager is available and verify every version.

## Process configuration

Both process skeletons require these environment variables before startup:

- `DATABASE_URL`
- `OIDC_ISSUER_URL`
- `PLATFORM_PROFILE` (path to the selected deployment profile)

Missing values return a structured configuration error before either process starts.

## Architecture decisions

The accepted architecture decisions are recorded in [docs/adr](docs/adr/):

- [ADR-0001: Modular Monolith](docs/adr/0001-modular-monolith.md) — one Go module, separate API and worker processes, internal Incident module.
- [ADR-0002: Two-Role Authorization Model](docs/adr/0002-two-role-model.md) — `operator` and `platform_admin`, with no role inheritance.
- [ADR-0003: Operator-Entered Command Remediation](docs/adr/0003-manual-command-remediation.md) — operators enter and confirm the actual command; isolated execution is policy-gated.
- [ADR-0004: SeaweedFS Evidence Archive](docs/adr/0004-seaweedfs-object-storage.md) — SeaweedFS is the default bundled S3-compatible archive.
- [ADR-0005: Fully Offline Bundle](docs/adr/0005-airgap-bundle.md) — installation artifacts are digest-locked and verified offline.
- [ADR-0006: DeepFlow as a Profile-Selected Add-on](docs/adr/0006-deepflow-boundary.md) — DeepFlow stays behind an adapter and its live capability remains PoC-gated.
- [ADR-0007: OpenAPI as the Public API Source of Truth](docs/adr/0007-openapi-source-of-truth.md) — OpenAPI 3.1 is the source for generated public API bindings.
- [ADR-0008: Defer KubeVirt/CDI development and acceptance](docs/adr/0008-defer-kubevirt-cdi-development.md) — KubeVirt/CDI development and acceptance are deferred; the current stage covers non-virtualization work.
- [ADR-0009: Exact upstream Bundle versions](docs/adr/0009-exact-upstream-bundle-versions.md) — preserve exact two-segment upstream releases in Bundle material locks.

- [ADR-0010: Core image license admission](docs/adr/0010-core-image-license-admission.md) — exact native/Go/JVM notice and corresponding-source obligations for the reviewed arm64 distributions.
- [ADR-0011: Native dependency source provenance](docs/adr/0011-native-dependency-source-provenance.md) — publisher archive checksums and paired source materials.
- [ADR-0012: Preserve upstream multi-platform digests](docs/adr/0012-preserve-upstream-multiplatform-digests.md) — authenticate original indices and the complete selected platform closure.
- [ADR-0013: Licensed K8sGPT CLI baseline](docs/adr/0013-lock-licensed-k8sgpt-cli.md) — freeze the reviewed upstream no-LLM CLI and preserve rejected license evidence.

## Commands

- `make bootstrap` installs and verifies the pinned toolchain.
- `make generate` runs available Go generators.
- `make check` checks generated Go output, runs `go vet`, and runs unit and currently available Contract tests.
- `make fmt` formats Go sources.
- `make test-unit` and `make test-contract` run their respective test suites.
- `make test-e2e` runs the current suite and prints skips; live tests require their explicit environment flags and prerequisites. A skipped live test is not acceptance evidence.
- `make test-integration`, `make test-replay`, and `make test-security` are stable suite entry points for the later implementation tasks.
- `make verify-bundle BUNDLE_DIR=/absolute/bundle TRUST_KEY=/outside/bundle/trust.pem` verifies a signed local Bundle against an independently trusted key.
- `make bundle-dev-arm64 BUNDLE_SPEC=/absolute/build.json BUNDLE_DIR=/absolute/new-bundle SIGNING_KEY=/outside/repository/signing.pem` packages already prepared, qualified local inputs.
- Core import/install commands, prerequisites and actual acceptance evidence are in [the offline install runbook](docs/runbooks/dev-offline-install.md). Component admission and code checks alone do not establish a live install/reinstall pass.
