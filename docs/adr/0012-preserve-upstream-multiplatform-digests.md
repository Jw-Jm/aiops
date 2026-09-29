# ADR-0012: Preserve upstream multi-platform image identities

Status: Accepted

## Decision and rationale

Some locked Victoria image digests identify upstream manifest lists. Docker's
`image save --platform linux/arm64` exports the native child and drops the list,
so the exported child digest differs from the original runtime image reference.
Keep the exact original list bytes and digest, plus the selected arm64 manifest,
config and every layer. Retrieve missing original list metadata by immutable
digest during connected preparation, verify SHA-256 and store it in the local
OCI archive. No installer registry call is allowed.

Verification authenticates the list and requires exactly one matching declared
Bundle platform, a present child manifest with the declared size, matching
config architecture, and all native layer hashes/sizes. Recursive/ambiguous or
missing native content is rejected. Non-selected descriptors are preserved
metadata; their binaries are not included, imported or declared supported.

## Alternatives considered

- Rename the native child to the original list digest: rejected; it fabricates
  identity and breaks content-addressed verification.
- Silently change frozen catalog/profile image digests: rejected; the observed
  external services use the original immutable identities.
- Package every architecture: unnecessary for this declared arm64 development
  Bundle and would require those foreign binary source/license closures.

## Consequences

Catalog/runtime references remain unchanged. Bundle architecture identifies the
one supported content closure. Metadata preservation does not qualify another
architecture, production environment or HA. The real engine must restore and
inspect the original repository digest before installation proceeds.

## Rollback conditions

If the runtime cannot import this identity or the selected closure cannot be
verified, reject installation and preserve the evidence; do not substitute an
alias, pull online or weaken admission.
