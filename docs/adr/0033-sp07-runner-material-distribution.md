# ADR-0033: SP-07 one-shot Runner material and corresponding source

Status: Accepted
Date: 2026-10-09
Scope: ADR-0032 nonvirtual command remediation.

The Runner combines the platform's independently compiled Go executable with
unmodified Bash, kubectl, OpenSSH, Ansible Core and Ansible Runner executables.
Only the locked Ansible transport invokes Ansible Runner. This is process
composition; it introduces neither an investigation loop nor an LLM client.

Ansible Core and Bash are GPL-covered. Their complete exact publisher sources,
Debian packaging patches, original notices and common license texts accompany
the Runner distribution, together with the exact dependency source closures.
Each publisher's original notice governs its own files; no aggregate notice
relicenses those files. CPython, pip, Python wheels, native libraries, Rust
publisher source and kubectl vendor source are retained independently. The
native wheel build source superset is explicitly distinguished from libraries
actually shipped. The platform Go source and its admitted module closure are
also distributed with the binary, preserving all original module notices.

Admission binds the selected native OCI manifest, every source/archive digest,
original notices and this ADR. New finite LicenseRef records may identify exact
publisher notices, never a wildcard exception. Signed bundle verification is
mandatory before business execution. Publisher HTTPS checksums and authenticated
Debian Release indexes are source evidence; they are not described as verified
upstream container signatures. Internal bundle signatures are verified through
the existing admitted verifier.

The isolated Job remains noninteractive, with read-only root filesystem,
memory-backed temporary data, fixed digest and profile-specific network access.
The SSH host key and CA, principal and expiry are independently pinned. Missing
material, signature, host admission or credentials reject execution. ARM64
native admission does not assert AMD64 deployment acceptance.
