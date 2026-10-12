# SP07 command runner native amd64 material

The frozen one-shot Runner distribution was built on 192.168.0.63 from retained authenticated amd64 Python 3.12.14 base, Debian packages, fourteen locked Python wheels, kubectl v1.35.6 and offline-built first-party runner. Docker build used `--network=none --pull=false`.

CFFI 2.1.1 was rebuilt from its authenticated original publisher source against Debian libffi 3.4.8-2. Pure Python and publisher notice members match the original wheel. Native function calls and callbacks passed.

The exact exported OCI index is `sha256:d755f1b67fba76588c7e08d22cdcceacc02b0aeead09bbbbe1f3c9660eacac5a`. Its measured configuration is `sha256:c6f746d1112e14253c64da2fa1ff23091cd490907c66dc22f4f851e2466e391c`.

Evidence: [native runtime probe](../evidence/amd64-deployment-20261009/runner-native-runtime-r2-result.json), [Debian source comparison](../evidence/amd64-deployment-20261009/runner-debian-amd64-comparison.json), and [CFFI review](../../third_party/admission/sp06-cffi-amd64-rebuilt-wheel.json).

The probe ran with network disabled, a read-only root filesystem and UID 65532. Python, Ansible Runner/Core, cryptography/OpenSSL, PyYAML and native kubectl version/import checks passed. This admission concerns distribution material and native tooling; Kubernetes/SSH command lifecycle and complete platform business acceptance remain separate and unverified.
