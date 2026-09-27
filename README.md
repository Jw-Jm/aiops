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

## Commands

- `make bootstrap` installs and verifies the pinned toolchain.
- `make generate` runs available Go generators.
- `make check` checks generated Go output, runs `go vet`, and runs unit and currently available Contract tests.
- `make fmt` formats Go sources.
- `make test-unit` and `make test-contract` run their respective test suites.
- `make test-integration`, `make test-replay`, `make test-e2e`, and `make test-security` are stable suite entry points for the later implementation tasks.
- `make bundle-dev-arm64` and `make verify-bundle` are stable bundle entry points for the later implementation tasks.
