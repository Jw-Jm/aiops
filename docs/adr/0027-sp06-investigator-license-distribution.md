# ADR-0027: investigator runtime and corresponding-source distribution

Status: Accepted
Date: 2026-10-03

The authorized SP06 investigator uses the unmodified HolmesGPT 0.42.0 release
at bfd33247f154484bc2f734a0da709da133c6a434 (Apache-2.0). Its full publisher
Python dependency closure includes bashlex 0.18 under GPL-3.0-or-later. The
platform Python wrapper is distributed under GPL-3.0-or-later, with complete
source and build instructions. This decision applies to this isolated Python
process; the Go API, Worker, web client and unchanged upstream packages retain
their own licenses. No upstream investigation loop/provider is copied.

The Linux/arm64 runtime locks all 175 runtime Python distributions, the pinned
CPython 3.12.14/pip 25.0.1 image and 87 Debian binary packages. Preserve every
original notice, exact publisher source artifact, Debian signed source index,
build-only setuptools source and wrapper/build source. The original notice
texts govern each component. Do not relabel the dependency closure Apache-2.0.
MPL packages keep original notices and complete source; text-unidecode permits
Artistic-1.0 or GPL-2.0-or-later. Mixed Debian copyright notices use finite,
image/version/source/notice-bound LicenseRef records, following the existing
native material policy; these are exact publisher texts, not blanket SPDX
guesses. They grant no permission outside that immutable material scope.

Every distributed investigator image must accompany the exact reviewed
corresponding-source Bundle material and original notices. The image also
carries that archive, locks and notices for offline recipients. No omission of
source, unreviewed architecture, changed image/source hash or arbitrary native
LicenseRef is admitted. The clickhouse-sqlalchemy source wheel rebuild uses
the original unmodified sdist and locked build tool; offline reproduction
compares every ZIP payload member against the prepared wheel.

Native Python extensions also distribute the complete authenticated Cargo lock
superset: 767 exact crate versions, including conditional build/test/target
dependencies. This inventory does not assert that all crates are linked in the
runtime. Original Cargo.toml license expressions and publisher notices are
preserved verbatim and bound through the finite native registry. Conditional
CDDL or alternative copyleft declarations are retained, never replaced with a
blanket permissive expression. The image SBOM and this declared source inventory
have distinct scopes.

The tiktoken 0.14.0 publisher sdist lacks Cargo.lock. Its unmodified source is
rebuilt with the explicit platform Cargo lock, authenticated Rust 1.99.0
toolchain and full vendored dependencies. The replaced wheel is a platform
build, not claimed byte-identical to the publisher wheel. An independent
network-disabled rebuild compares every payload member. Complete Rust library
source, toolchain publisher notices and exact build instructions accompany it.
Build-only compiler prerequisites do not become investigator runtime packages.
The actual cryptography extension reports OpenSSL 4.0.3; its publisher source
and original notices are included in addition to Debian OpenSSL source.
The measured confluent-kafka extension reports librdkafka 2.15.1. Its exact
publisher commit and locked OpenSSL 3.5.7, zlib 1.3.2, zstd 1.5.7 and curl 8.21.0
build dependencies are hash-verified against the immutable build modules.
The jq sdist contains complete jq 1.8.2/Oniguruma source, verified against the
original native publisher archive. These full source supersets include optional
build/test files; every original notice governs its own files. Finite native
references bind the complete texts rather than relabel mixed source packages.

The google-crc32c 1.9.0 wheel does not establish an exact native source commit.
Replace it with a platform wheel built from the unmodified publisher sdist and
CRC32C commit 02e65f4fd3065d27b2e29324800ca6d04df16126. Two independent offline
builds compare every payload member and run the native C implementation against
the published CRC32C known vector. Explicit compiler/linker settings are part
of the source bundle; no original-wheel linkage equivalence is claimed. These
three rebuilt wheels change packaging/native build inputs, not Holmes core or
provider source. Build-only tests/benchmarks of CRC32C are disabled; this is
source/build correctness verification, not performance acceptance.

The component Catalog admission concerns this exact reuse/distribution closure.
Full SP06 acceptance, independent review and final delivery remain separate
mandatory gates; this ADR does not mark those gates passed. Ollama
llama3.1:8b-16k establishes protocol/tool/budget behavior, not RCA accuracy.
PyRCA remains excluded; KubeVirt/CDI and VM tools remain disabled/unverified.
