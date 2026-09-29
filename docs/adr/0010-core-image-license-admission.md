# ADR-0010: Core image distribution and source obligations

Status: Accepted

## Context and rationale

Task 2.7 requires qualified core and Victoria fallback materials. Root project
licenses do not describe the entire pinned container contents. The preparation
inventory in `third_party/admission/task-2.7-core-image-sbom-audit.json` records
actual image packages and hashes the raw SPDX/Syft reports. Scanner gaps are not
proof that the packages are unlicensed; broad scanner matches can include
license-test data and must be checked against the distributed files.

The existing catalog and final plan require a specialized ADR for GPL/AGPL
dependencies and reject unknown licenses. This decision applies only to the
exact reviewed image, package/source identity and original notice hashes. It
does not qualify a component by itself.

## Decision

Keep the selected release versions, source commits and arm64 image digests.
Use reviewed distributed image contents, including OS/JVM packages and bundled
JAR/native libraries. Record license choices and exceptions at file/package
scope, exact notices, corresponding source requirements, source artifact hashes,
build/relink instructions where required, and the matching SBOM and runtime
Fixture. Source hashes must not be placed in a field claiming a Git commit.

Include complete corresponding source/notice materials in the offline payload
and signed inventory, bound by `correspondingSourceBundleSHA256`. Supply source
directly with binaries, without depending on an online offer. Keep candidates
out of Bundle until source, license, dependency, PoC and Fixture evidence is
complete. Acceptance of this policy does not qualify an image by itself.

Unchanged OS packages retain their publisher copyright files and original source
packages, packaging patches and build/relink instructions. GPL tools/libraries
remain inside upstream component processes; API/Worker does not copy or
statically link their code. LGPL libraries/JARs can be replaced or relinked. The
platform imposes no additional EULA or restriction on modification, reverse
engineering for debugging modified libraries, or redistribution under original
terms. EPL/MPL source is included. Sleepycat source accompanies complete source
for distributed programs using Berkeley DB, not just its library.

Mixed native publisher notices that cannot truthfully be reduced to one standard
SPDX expression use finite, scoped SPDX `LicenseRef` records in the compiled
native review registry. They identify known reviewed terms, not a fallback for
scanner gaps. Each binds component image digest, package/version, source archive
checksum, original notice hashes and this ADR. A different scope, changed
notice/source, absent corresponding source, missing Accepted ADR or unregistered
reference rejects admission. Public-domain declarations retain publisher wording
and are not rewritten as CC0. File-specific exceptions remain in their scopes.

Retain applicable BSD attribution/no-endorsement/advertising conditions; source
instructions include the acknowledgements. License documents and RFC reference
texts are supplied verbatim. Perl's Artistic/FSFAP/Hsieh and public-domain notices,
PostgreSQL's Spencer/Tcl/Unicode/contributed file notices, and native compiler/build
exceptions stay in original source/copyright inventories. License definitions
without any matching file assignment are not treated as compiled code. Do not
assign the application's root license to independently licensed files.

Keycloak's Nashorn and classfile-backport retain their GPL-2.0 Classpath exception;
MySQL Connector/J retains Universal FOSS permission and accompanies complete
connector and Keycloak source. MariaDB LGPL and Jakarta/Parsson/Expressly EPL
source obligations use matched source JARs. Native code in JNA, Netty, JLine,
Byte Buddy and Brotli4j includes full exact project source and build files; Java
source JARs alone are insufficient. JLine's BSD-4 label is corrected to its actual
three-clause text; the original notice is retained.

Go review uses compiled upstream package reports and exact source notices.
OpenBao's boltdb name points to its local MPL stub. Flatbuffers and reedsolomon
supplementary notices bind byte-identical source files to upstream permission.
VictoriaLogs' versioned VictoriaMetrics module uses its own source identity,
not the independent VictoriaMetrics image's release. A test/license catalog
match is not a compiled dependency without file/package evidence.

## Alternatives considered

- Qualify with a root LICENSE and an automatically generated SBOM: rejected
  because it leaves file mappings and dependency/source obligations unresolved.
- Suppress scanner gaps or label every dependency with the application's root
  license: rejected because the image contains separately licensed software.
- Switch release versions or replace the required components: rejected as a
  workaround; any such choice requires its own supported-version evaluation.

## Consequences

This decision is limited to pinned development arm64 images and exact reviewed
source/notice closures. It does not approve future upgrades or establish
production compatibility/HA. Source/notice closure, code checks and isolated
PoCs do not establish Task 2.7: a qualified signed Bundle and real offline
install/reinstall with health/capability checks remain mandatory.

## Rollback conditions

If an obligation cannot be met for a pinned artifact, retain candidate status
and stop its admission. Preserve the existing external OpenBao/Victoria services
and user data. Submit the specific evidence and replacement decision before
changing versions or the component choice; do not bypass the Bundle admission
validator.
