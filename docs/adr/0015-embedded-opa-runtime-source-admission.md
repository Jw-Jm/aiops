# ADR-0015: Embedded OPA and exact first-party Go source admission

Status: Accepted

## Context and rationale

The frozen implementation plan specifies Embedded OPA/Rego SDK in its stack,
architecture and Task 3.6. The existing `opa` Catalog entry describes a pending
standalone image. SP-03 actually links OPA v1.21.0 into the API. A service's
candidate entry cannot qualify those linked files or the changed first-party
images. The old SP-02 process stubs and their SBOMs are historical evidence.

## Decision

Select OPA SDK v1.21.0, full upstream commit
`dc6269f2c648bbbece4b76fa1fc3dbb7b61cc7b6`, as `opa-sdk`, separately from
the retained candidate standalone `opa` service. The SDK is required by the
frozen architecture; an independent OPA deployment is not required or admitted.
The standalone entry stays candidate and cannot enter Bundle.

Qualify only the exact Linux/arm64, CGO-disabled Go 1.27.1 runtime import closure
of API, Worker and opsctl. Every selected file is byte-matched to the publisher
module zip authenticated by tracked go.sum. Full repository commits and publisher archive SHA-256 identify every
dependency. Missing proxy Git metadata is resolved against primary publisher
commit/tag records and confirmed by selected source byte comparisons. Retain file-scoped and nested notices and the original license
texts. Do not distribute unselected module tests/docs/assets in this closure.

The SDK and shared runtime closure contain 79 module artifacts, 2,419 selected
module files and 1,332 selected standard library files. Runtime source preparation
uses reviewed exact notice hashes, rather than inferring permission from scanner
labels. The standard library bytes match the pinned Go 1.27.1 Linux image.

Bundle verification requires the qualified SDK and its exact
`opa-sdk-source` material for API, Worker or opsctl materials. This applies before
payload extraction or import. Source material hashes, versions, architecture and
candidate rejection remain enforced. The first-party name allowlist cannot
omit the SDK/source license gate. `make check` and Bundle preparation check the
current dependency/source closure against its qualified lock with networking off.

## Alternatives considered

- Qualify the standalone service based on linked SDK tests: rejected; they are
  different deployment and artifact scopes.
- Treat first-party SBOM/root notices as qualification of every linked module:
  rejected; source and license scopes differ between dependencies.
- Require a full Git commit for every publisher archive: rejected under
  ADR-0011, which preserves exact archive identities without fabrication.

## Consequences

This closes the SDK source/functional qualification for the stated development
target. Non-root, no-network upstream SDK execution and an empty-module-cache
rebuild establish that narrow scope. They do not qualify another architecture,
production Kubernetes compatibility, HA, or the current Bundle's live offline
installation. Old Bundle artifacts remain preserved but cannot stand for SP-03
delivery; the updated verifier rejects their missing runtime source material.

## Implementation boundaries

No standalone OPA service, new graph engine, or virtualization capability is added.

## Rollback conditions

Changed modules, versions, selected source bytes, notice hashes, target or
decision behavior require renewed evidence and Catalog/source material locks.
Unknown licensing or failed deterministic/fail-closed decisions stop admission.
Keep the previous valid signed policy and fail closed; do not download a remote
policy, admit a candidate image, remove failed evidence or lower source gates.
