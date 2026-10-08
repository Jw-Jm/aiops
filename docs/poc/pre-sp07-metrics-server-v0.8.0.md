# Metrics-server v0.8.0 exact material and native PoC

This report covers the unchanged arm64 image, exact source/license closure and
the observed native API path. It does not declare full R3, cold installation,
capacity, performance or RCA accuracy acceptance.

The primary locks are commit `d66279c6426c6581d9656fe3d42bc52db7c29597`, source
archive SHA256 `4dc2060e33613c35ae0f7c48aba1b813d986d82049f641966fd9eb5eefc49864`,
and selected image manifest
`sha256:8f49cf1b0688bb0eae18437882dbf6de2c7a2baac71b1492bc4eca25439a1bf2`.
The release index, source, upstream manifest, actual-image SBOM and original
notices are retained in the private prepared public-material directory and will
be included in the signed payload. No secrets or model weights belong there.

Actual commands and results are retained in
`docs/evidence/pre-sp07-20261004/metrics-admission-commands.json`. The initial
APIService was absent. The dedicated native PoC installed a pinned serving CA,
strict independently obtained kubelet CA, exact discovered endpoint addresses
and upstream read-only/delegated-auth RBAC. The live APIService was Available;
real Node and owned-Pod timestamp/window/Quantity reads are retained in
`metrics-apiservice-live.json`, `metrics-nodes-live.json` and
`metrics-owned-pods-live.json`. TLS bypass flags were absent.

`metrics-real-ingestion-r4.log` and its exit file record exit 0 for the actual
Node Inspection/Finding/archive/decrypt path. Controlled refusal, degradation,
stale/wrong identity and threshold cases are distinguished from real source
samples. Ordinary healthy metrics are not a real high-utilization positive.
Formal Pod association and the full source revoke/recover/addon path remain
acceptance work. The earlier failed ingestion and identity records are retained.

The current source checks are in
`docs/evidence/pre-sp07-20261005/metrics-go-image-source-binding-r6.json` (95
exact external modules), `metrics-toolchain-source-binding-r6.json`, and the
CA byte-comparison evidence from 20261004. Exact Debian source packages and
publisher notices accompany the original binary. The whole rootfs ELF inventory
contains only Metrics-server; CGO is disabled.

`metrics-offline-source-build-r6.json`, `.log` and `.exit` record a real exit 0
using the locked official Go 1.24.4 Linux/arm64 tool, read-only original source,
restored exact modules, `GOPROXY=off`, `GOSUMDB=off`, `GOTOOLCHAIN=local` and
Docker `--network=none` with an empty build cache. Missing legacy module
metadata, absent make, and insufficient ephemeral build storage failed first;
their records were preserved. The successful invocation executes the upstream
build recipe directly with explicit release flags. No runtime image or source
was changed to obtain success.

ADR-0029 records the finite distribution decision. Material qualification is
separate from signed offline installation and the remaining R3/R4 live gates.
