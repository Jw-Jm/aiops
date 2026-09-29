# ADR-0009: Preserve exact upstream release versions in Bundle locks

Status: Accepted

## Context and rationale

Task 2.7 must package the exact versions selected by the Component Catalog.
The catalog locks PostgreSQL to `17.11` and SeaweedFS to `4.47`; its admission
validator accepts exact two-segment release versions. The original Bundle
validator and Schema only accept three-segment versions or immutable commits.
This prevents faithfully packaging those releases even after qualification.

## Decision

Extend Bundle material version validation to accept exact two-segment numeric
release versions, with the existing optional `v`, prerelease and build suffixes,
as well as the existing three-segment versions and immutable commits. Preserve
the upstream version verbatim. Change the Bundle Schema and its Go validator
together and test both acceptance and rejection. Keep schemaVersion 1: the
change broadens validation and retains the existing field layout and every
previously valid lock. Platform version syntax remains unchanged.

An exact version still requires the qualified catalog's source, commit, digest,
license closure and capability evidence. Numeric version acceptance alone does
not qualify a component. Candidate components remain prohibited in a Bundle.
Publish the updated bootstrap verifier alongside Bundles using these versions;
an older verifier can reject a newly accepted version string.

## Alternatives considered

- Invent `17.11.0` or `4.47.0`: rejected because it alters the catalog's exact
  upstream version and can fail the image/version contract.
- Use a source commit in place of the release: rejected for these container
  materials because the resolved Profile and catalog are locked to releases.
- Permit arbitrary tags: rejected because aliases and version ranges are not
  immutable version locks.

## Consequences

PostgreSQL and SeaweedFS can retain their genuine release identifiers throughout
catalog, Profile and Bundle. Floating aliases, wildcards, ranges, single numeric
versions and incomplete version identifiers remain invalid. Existing signed
locks keep their exact canonical bytes and signatures.

## Rollback conditions

Revert the expanded acceptance only through a replacement ADR and coordinated
Schema/validator tests if a published contract requires a different exact
upstream version representation. Do not normalize upstream versions or relax
candidate, digest, signature or qualification checks to accommodate packaging.
