# 智能运维平台 1.0

`platform/` is the platform's standalone Go monorepo. The repository keeps the API and worker as separate processes over one Go module.

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

## Commands

- `make bootstrap` installs and verifies the pinned toolchain.
- `make generate` runs available Go generators.
- `make check` checks generated Go output, runs `go vet`, and runs unit and currently available Contract tests.
- `make fmt` formats Go sources.
- `make test-unit` and `make test-contract` run their respective test suites.
- `make test-integration`, `make test-replay`, `make test-e2e`, and `make test-security` are stable suite entry points for the later implementation tasks.
- `make bundle-dev-arm64` and `make verify-bundle` are stable bundle entry points for the later implementation tasks.
