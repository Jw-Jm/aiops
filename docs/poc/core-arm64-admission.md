# Pinned core linux/arm64 component admission

## Scope

This records the exact seven development core/fallback distributions selected
by ADR-0009. It establishes component source/notice closure and isolated PoCs,
not full core offline install/reinstall, production compatibility or HA.
KubeVirt/CDI remain deferred, candidate and excluded under ADR-0008.

## Source and license review

ADR-0010 is Accepted for the exact unchanged image contents. ADR-0011 records
native archive identities and the paired corresponding-source contract.
`third_party/admission/core-arm64/*.json` binds each image and root commit to
the original source SHA-256, complete source Bundle SHA-256 and source inventory.
The Component Catalog includes the file notices and transitive native, Go,
Java and native JAR source identities. Native mixed terms use only the finite
compiled review registry; a changed package/image/source/notice/ADR is rejected.

All corresponding-source archives include original packaging sources/patches,
unmodified full notices and BUILD-AND-RELINK.md. They accompany binaries as
separately authenticated material, including fallback binaries. Java source
JARs are supplemented by the exact native C/C++ project sources. Publisher
packaging differences and generated Java sources remain in source inventories.
Raw Syft results and earlier preparation `qualificationPassed: false` fields
are retained as historical inputs and are not rewritten as live acceptance.

Preparation scripts in `test/fixtures/offline-core/` download and hash sources
only before packaging. `freeze-core-admission.py` requires PyYAML 6.0.3 and
existing reviewed inputs/actual PoC results; it is not run during installation.
Install uses only the signed local Bundle and independently supplied public key.

## Actual component evidence

- PostgreSQL 17.11 and Keycloak 26.7.4: digest-pinned Docker startup with network
  disabled, non-root processes, read-only roots, SQL SELECT 1 and management
  readiness. Original report: `core-arm64/task-2.7-dependency-startup.json`.
- SeaweedFS 4.47: exact pinned image on an internal Docker network; signed S3
  object round-trip and anonymous rejection. Actual test output:
  `core-arm64/task-2.7-seaweed-s3-poc.log`.
- VictoriaMetrics v1.116.0, VictoriaLogs v1.52.0 and vmalert v1.116.0: actual
  local upstream Chart installs, digest-only post-rendering and capability
  checks in an isolated namespace. Actual test output:
  `core-arm64/task-2.7-victoria-poc.log`. This checks vmalert health/rule API;
  Task 2.9 rule/equivalence development is a separate scope.
- OpenBao 2.7.0: existing instance reused with independent CA verification,
  initialized/unsealed state and original protected PVC identity. Routine
  recovery-share unseal did not initialize or reconfigure the instance.
  `core-arm64/task-2.7-openbao-live-health.json` records the real state. The
  bundled fallback's persistent bootstrap is covered by Task 2.3 fixtures;
  this existing-service observation does not establish a new live bootstrap.

The paths above are relative to `third_party/admission/`. These reports are
mirrored into `bundle/evidence/` for the same offline catalog evidence checks.

## Separate Task 2.7 gate

Only a later successful explicit `TestCoreOfflineInstallation` with the
qualified signed core Bundle, effective public egress denial, initially absent
selected images, release-scoped cleanup/reinstall, unchanged protected storage,
and post-install health/capabilities establishes Task 2.7 acceptance.
