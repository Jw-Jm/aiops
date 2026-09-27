# ADR-0005: Fully Offline Bundle

Status: Accepted

## Context and rationale

Installation targets may not have public network access, and mutable or undeclared downloads would make the installed system difficult to reproduce and verify. The artifact set must be explicit before it reaches a target.

## Decision

Package installation materials in a machine-readable Bundle Manifest that locks each image, chart, binary, and source archive to an exact digest/SHA-256. Verify the bundle signature and contents locally before import or installation; target installation and default runtime operation require no public network. Use the Sigstore/Cosign local-key model with the locked Go verification library embedded in `opsctl`. Deliver the bootstrap executable and trust-key fingerprint through an independent trusted channel or preinstallation. Keep development and production signing keys separate. The development bundle targets `linux/arm64`; `linux/amd64` uses a separate artifact set and digests. Model services use internal OpenAI-compatible endpoints, and model weights are excluded.

## Alternatives considered

- Download dependencies directly from targets during installation: rejected because targets must install fully offline.
- Use mutable tags, floating branches, or `latest`: rejected because they do not provide reproducible artifact identity.
- Depend on target-side `cosign`, Fulcio, or Rekor: rejected because verification must work without those executables or online services.

## Consequences

Bundle creation and release require a complete manifest, digest review, local signing, and independently provisioned trust material. Architecture-specific bundles cannot share digests unless the artifact bytes are identical. Bundle changes must be prepared on a connected packaging system and verified again offline.

## Implementation boundaries

- Do not fetch undeclared content at install time or access public services from the target as a verification fallback.
- Do not place the only trust root inside the bundle it verifies.
- Do not include model weights or claim production high availability, disaster recovery, or unresolved compatibility combinations as accepted bundle capabilities.

## Rollback conditions

Reconsider the trust or packaging design only if a documented security or target-platform requirement cannot be met with offline local-key verification. Any change requires a new accepted ADR and a tested replacement bootstrap and trust-provisioning path; an online verification bypass is not a rollback.
