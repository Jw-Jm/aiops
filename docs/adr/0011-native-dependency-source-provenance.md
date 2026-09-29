# ADR-0011: Exact source archives for native image dependencies

Status: Accepted

## Decision and rationale

The application component itself continues to require its full upstream Git
commit. A dependency inside an unchanged upstream image may instead come from
a versioned Debian/PGDG source package, source RPM or Maven source JAR. Those
publishers identify exact source archives by version and checksums; an archive
checksum must never be presented as a Git commit.

Extend the internal Component Catalog dependency record with `sourceType`:
`git` (the default) or `archive`. Git records keep the existing full-commit and
exact-release requirements. Archive records require an HTTPS source URL, a
non-floating publisher version preserved verbatim, and
`sourceArchiveSHA256`. They must omit `commit`. The existing dependency
`digest`, license, closure, PoC and Fixture requirements still apply. Metadata
alone does not qualify a component. The separate license decision in ADR-0010
defines the reviewed distribution and file/package obligations.

For image distribution, `correspondingSourceBundleSHA256` binds the prepared
source/notice closure archive to the reviewed component. A Bundle must include
the exact `<component>-source` material (kind `source`, component version and
that digest) whenever this field is declared. A source material cannot use the
first-party allowlist or bypass candidate rejection. This closure archive is a
local distribution artifact, distinct from the publisher's application source
archive and every dependency's publisher source checksum.

Alpine dependencies with a recorded aports commit may use a Git record, but
their native package version must be represented as an archive record when it
does not meet the Git release grammar. Its exact APKBUILD commit and all source
SHA-512/SHA-256 values remain in the source preparation inventory.

## Alternatives

- Invent SemVer aliases or put SHA-256 in `commit`: rejected; both misrepresent
  the publisher's identity.
- Exclude OS/JVM/JAR dependencies from closure: rejected; they are present in
  the distributed image.
- Require every distributor to supply a Git repository: rejected; an exact
  publisher source archive is an immutable source identity.

## Consequences

The preparer can retain native epochs, revisions and platform classifiers.
Unknown licenses, candidates, missing source checksums and incomplete source
or license inventories continue to reject Bundle admission. Production
compatibility and HA are outside this decision.

The archive grammar preserves publisher `v` and Go toolchain `go` prefixes,
epochs and native revisions. It does not permit ranges, branches or aliases.
Catalog licenses may use reviewed SPDX `AND`, `OR` and explicitly paired
`WITH` exceptions. This represents separately licensed files and publisher
choices without discarding terms. Unknown identifiers/exceptions remain
rejected. Copyleft terms anywhere in an expression retain the specialized ADR
gate; LGPL, EPL and Sleepycat also require that review. ADR-0010 describes the
scoped native publisher notice references; an unregistered reference is unknown
and rejected, and a registered one must match its exact source/image/file scope.

## Rollback conditions

Reject the affected dependency if its publisher version, archive identity or
correspondence to the pinned image cannot be established. Keep the component
candidate; do not fall back to a branch, latest artifact or synthesized commit.
