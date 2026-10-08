# ADR-0029: Exact Metrics-server distribution and source obligations

Status: Accepted

## Scope and decision

R3 authorizes Metrics-server v0.8.0 at source commit
`d66279c6426c6581d9656fe3d42bc52db7c29597`, with the unchanged Linux/arm64
manifest `sha256:8f49cf1b0688bb0eae18437882dbf6de2c7a2baac71b1492bc4eca25439a1bf2`.
The upstream index is separately retained; an index digest is not substituted
for the selected architecture manifest. This decision approves the exact
reviewed distribution terms, not full R3 acceptance or future upgrades.

The measured image contains one ELF, Metrics-server, built with Go 1.24.4 and
CGO disabled. Its 95 external Go modules match the image's h1 checksums and
their exact Git source files. The Go compiler source matches the official
release checksum and tag. Full original application/module/compiler source and
notices accompany the binary; publisher module metadata needed for legacy ZIPs
is included. A real build from those inputs passed with networking disabled and
an initially empty build cache. Its explicit new build timestamp does not imply
byte identity with the unchanged upstream image.

The three distributed Debian packages are base-files 12.4+deb12u11, netbase 6.4
and tzdata 2025b-0+deb12u1. Exact copyright files, original license texts, source
packages, packaging patches and build instructions accompany them. The 142
copied CA blocks and copyright were compared byte for byte with
ca-certificates 20230311+deb12u1; that package's original Mozilla input and
generation/packaging source also accompany the image. No CA update executable
or additional native ELF was found in the measured image.

Retain GPL source and original wording, including public-domain declarations;
do not relabel public domain as CC0 or apply Metrics-server's Apache license to
independent components. MPL source is supplied directly. Package notice
inventories preserve each original text and its file-specific scope. Go license
conjunctions describe the package's compiled permission inventory, without
relicensing independent files. Compiler-only vendor notices remain in complete
compiler source and are not described as linked Metrics-server code.

The four mixed/native publisher notice inventories use a separate finite
compiled registry. Each record binds the component/version, architecture image
digest, exact package/version and source checksum, corresponding-source bundle,
original notice checksum, and this Accepted ADR checksum. A different image,
source, package, notice or ADR fails admission. An arbitrary Catalog LicenseRef
cannot extend these permissions. Complete source is delivered with binaries;
there is no online-only source offer or extra EULA restricting original rights.

## Runtime and acceptance boundary

Use the existing metrics.k8s.io API, strict kubelet certificate verification,
webhook authentication and the reviewed minimal upstream RBAC. The platform
Chart supplies a pinned serving identity/CA and exact discovered Node/API-server
addresses. It does not enable kubelet-insecure-tls or insecureSkipTLSVerify.
The upstream release manifest and the platform Chart's transformation are
retained independently. No performance, capacity or RCA accuracy PASS follows
from an APIService or Pod becoming Ready.

Native APIService/kubelet TLS/Node/owned-Pod reads and the existing Node
Inspection archive/decrypt path have separate actual evidence. Formal signed
addon installation, cold import, source authorization/revocation, complete Pod
identity association, and finite symptom/recovery gates remain R3/R4 work.
Metrics are symptoms and samples, not confirmed causation. Retain previous
failures and all protected fixture data. Reject incomplete material closure;
do not change versions or relax admission to bypass a failed gate.
