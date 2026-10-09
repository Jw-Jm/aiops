# SP07 one-shot Runner material admission PoC

The selected linux/arm64 OCI manifest is
`sha256:15ab0baf157d103f8333f6b19a7b48bf36b90c82881d33f385aef4f9976ecb94`.
Its config and every layer are verified against the OCI descriptors. The
pinned Python 3.12.14 Debian base and exact authenticated Debian packages are
installed offline; pip uses 14 publisher-checksummed wheels with hash checking,
no index and no Holmes/LLM packages. Bash, kubectl v1.35.6, OpenSSH, Ansible Core
2.21.5 and Ansible Runner 2.4.3 remain separate upstream executables.

`docs/evidence/sp07-20261009/runner-native-tools-r4.log` records versions in this
exact image, running without network, with read-only root, dropped capabilities,
nonroot UID 65532 and memory-backed temporary storage. The first local attempt
used a config digest rather than the imported image identity; its exit 125 is
retained in `runner-native-tools-red.log`. It did not execute a container.

The original source closure and checksums are in `runner-source-closure-r4.log`.
Corresponding source includes platform source and admitted Go modules, complete
publisher Python sdists, authenticated Debian sources and packaging patches,
original notices/common licenses, exact native wheel build-source superset,
kubectl vendor source and its own go1.25.11 standard library source. Every
source artifact and original notice is bound into the finite Runner admission.
No upstream container signature is asserted; internal signed bundle admission
is required for deployment and business execution.

This PoC qualifies material reuse and distribution only. Kubernetes execution,
SSH CA/principal boundaries, cancellation/recovery, output and Post-check are
separate SP07 business gates. AMD64 deployment remains unverified. Virtualization
and PyRCA remain excluded.
