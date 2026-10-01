# OrbStack core offline installation

Historical status: Task 2.7 passed on the source-bound OrbStack development
profile on 2026-09-29. That record does not accept subsequent SP-03 Chart or
runtime changes; the current Bundle needs its own affected installation proof.
KubeVirt/CDI development, deployment and runtime acceptance remain deferred
under [ADR-0008](../adr/0008-defer-kubevirt-cdi-development.md). This runbook
covers the core Profile only; virtual machine work is outside the current stage.

## Preconditions

1. Run profile detect/resolve against the actual OrbStack context. Resolution
   now probes the local Docker context and proves a live Kubernetes container is
   visible in that engine before recording orbstack_shared_store.
2. The resolved core Profile must be installable. Candidate components cannot
   be admitted by editing installable or admissionState: the independent embedded
   Component Catalog also rejects them during Bundle verification.
3. Supply a signed Bundle and a separately trusted public key. Include exact
   arm64 OCI images, their complete blobs, packaged local Charts, SBOMs and
   licenses. Include the qualified VictoriaMetrics/VictoriaLogs fallback
   materials even when the selected Profile reuses existing services.
4. Provide bootstrap Secrets outside Bundle/Git. Required secret keys are
   checked without printing their values. ops-platform-runtime/apiDatabaseURL
   and workerDatabaseURL must use distinct LOGIN roles granted only their
   respective runtime role; archiveAccessKey and archiveSecretKey are the S3
   credential references. Apply forward-only migrations with the isolated
   migration principal before starting these runtime processes. Dependency Chart credentials use the
   existingSecret names defined by that Chart. Existing external OpenBao retains
   its own bootstrap and unseal workflow.
5. The initial installer requires ops-system to exist and refuses adoption or
   overwrite of rendered resources. A cleanup or reinstall must target only
   this installation's identified release resources, never existing Victoria
   instances, external OpenBao, user namespaces or user data.
6. Provision the independent public ConfigMap `ops-platform-bootstrap` in
   ops-system before image import. Required entries are `openbao-ca.pem`
   (the independently verified public OpenBao bootstrap CA), `archive-bucket`
   (an existing S3 bucket with the required retention configuration), and
   `registry-trust.json` (a JSON map from trusted key IDs to base64 Ed25519
   public keys), `archive-ca.pem` (the independently verified public S3 TLS CA),
   and `oidc-ca.pem` (the independently verified public issuer
   TLS CA). The scratch API image has no implicit system CA store. The installer copies only these public values into the runtime
   ConfigMap and uses the OpenBao, SeaweedFS and Keycloak endpoints locked in
   the resolved Profile. It fails before import if required values are absent
   or malformed. Do not place root tokens, Shamir shares or private keys here.
7. Keycloak, OpenBao and S3 runtime endpoints must use HTTPS.
   Bundled SeaweedFS requires `ops-seaweedfs-tls` with `tls.crt` and `tls.key`
   supplied outside Git/Bundle and a certificate for its locked Service DNS.
   Port 8333 serves TLS only; there is no plaintext S3 listener. For bundled Keycloak,
   provide `ops-keycloak-tls` with `tls.crt` and `tls.key` outside the repository
   and Bundle, with a certificate valid for its Profile DNS identity. Its HTTPS
   Service uses port 8443. Import the frozen realm/PKCE/ACR configuration and
   complete bootstrap credential rotation per `keycloak-bootstrap.md`.
   An existing HTTP-only instance must not be overwritten by this installer;
   arrange an explicitly authorized TLS change or select an isolated instance.

## Commands

### Prepare and build a local Bundle

`make bundle-dev-arm64` now packages local inputs; it does not fetch upstream
artifacts, qualify candidates or produce a resolved Profile. Run it from the
platform repository with the locked toolchain:

```sh
make bundle-dev-arm64 BUNDLE_SPEC=/absolute/prepared/build.json \
  BUNDLE_DIR=/absolute/new-bundle SIGNING_KEY=/outside/repository/signing.pem
make verify-bundle BUNDLE_DIR=/absolute/new-bundle TRUST_KEY=/independently/trusted/public.pem
```

The preparation JSON has `schemaVersion: 1`, `bundleId`, `platformVersion`,
`architecture: "linux/arm64"`, `files` and `materials`. Each file declares
`path`, `source`, `digest`, `size`, and `kind`; `path` is its approved payload
path and `source` is a local file, relative to the preparation JSON or absolute.
Digests and sizes must be explicitly locked before packaging. Each material
uses the BundleLock material fields, including `payloadRef`, `sbomRef`,
`licenseRef`, and `installAfter`. All files must be referenced by a material.
Exact upstream releases such as `17.11` and `4.47` remain unchanged under
[ADR-0009](../adr/0009-exact-upstream-bundle-versions.md).

The signing key must be an existing unencrypted PKCS#8 PEM private key, mode
0600 or stricter, outside the repository and output Bundle. The builder does
not generate a key or include it in the payload. Deliver and trust its public
key separately. The build report's fingerprint is an identity for comparison,
not authorization to trust that key.

The output parent must exist and the output directory must be new. Packaging
rejects candidates, unknown materials, undeclared files, changed digest/size,
and incomplete or mismatched OCI images. It writes the fixed four-file Bundle
layout, verifies its own signature and complete payload before publication,
and leaves no published output after a rejected build. File order, timestamps,
UID/GID and permissions are fixed; payload and lock bytes are reproducible.
Ed25519 signatures are also deterministic; other supported signers may use
randomized signatures. Reproducible Fixture tests prove packaging mechanics,
not qualification or a real core offline installation.

### Detect, import and install

```sh
go run ./cmd/opsctl profile detect --context orbstack -o artifacts/profiles/core-detected-deployment-profile.yaml
go run ./cmd/opsctl profile resolve -f artifacts/profiles/core-detected-deployment-profile.yaml -o artifacts/profiles/core-resolved-deployment-profile.yaml
opsctl bundle import --profile artifacts/profiles/core-resolved-deployment-profile.yaml --bundle /absolute/bundle --key /outside/bundle/trust.pem
opsctl install --profile core --resolved artifacts/profiles/core-resolved-deployment-profile.yaml --bundle /absolute/bundle --key /outside/bundle/trust.pem --offline
```

Import checks the signature, the complete payload inventory and every OCI
manifest/config/layer before any runtime call, including standard fallback
materials for external components. External fallbacks are verified but not
imported into the shared engine; existing external services and their cached
images are preserved. The empty-cache assertion checks both the exact repository
reference and manifest digest for every selected install image. It does not
claim the engine's underlying layer/BuildKit cache, pre-existing Kubernetes
infrastructure or reused external services are empty. It uses docker image load on the
explicit OrbStack context, then verifies the exact repository digest. A runtime
that loses that digest is rejected; there is no pull or tag substitution.
Unimplemented runtime drivers return CAPABILITY_DISABLED.

Install renders only verified local Charts, validates their image references
and IfNotPresent policy, checks required Secrets and refuses existing resource
adoption before import. It invokes Helm without repository updates or dependency
downloads. Errors identify the last checkpoint and material/release; retrying
requires checking the actual resource state. No automatic deletion or rollback
is performed.

The locked Victoria Charts construct `repository:tag` and do not use an
independent `image.digest` field. The installer supplies the exact
`version@digest` tag. Its local `opsctl helm-render-owned` post-renderer accepts
only that planned image, emits the verified `repository@digest` reference, adds
release labels to resources, Pods and volume claim templates, and disables Pod
token automounting. Preflight and Helm installation use the same transformation;
the upstream Chart archives and their hashes remain unchanged.

## Acceptance evidence

The CLI's --offline flag prevents installer download paths. It is not proof that
the target network denies public access. The original OrbStack environment did
not enforce NetworkPolicy denial. A separately prepared policy-only controller
has now passed the isolated core Chart probe described below. A passing core acceptance
run still requires an effective network block, positive reachability to internal
services, negative public connection probes, absence of the tested image digests
before import, installation solely from the signed Bundle, and health/capability
checks afterward.

API/Worker are the SP-01 process skeletons. Their Deployment rollout availability
is process readiness only; it does not prove business HTTP readiness or a
completed Web UI. The Web/investigator/command-runner workloads are not activated.

The live E2E verifies every selected install image reference is absent before
import. External fallback images remain in the verified Bundle but are not
imported or removed from the existing services' shared image store. Its egress check runs as a one-shot Kubernetes Pod in `ops-system`
with the same `ops.platform.io/component=api` selector used by the core network
policies. It must connect to the internal DNS Service and fail TCP connections
to the listed public IPv4 and IPv6 endpoints. A policy object or a host-network
probe alone does not pass this check.

The live test installs once, checks process availability, then removes only the
Helm releases created by that invocation. Before Helm uninstall it checks every
recorded resource and every live object for the expected release label. PVCs,
PV resources, existing Victoria resources, existing OpenBao resources, and user
namespaces are not cleanup targets. It verifies the pre-existing PVC identities,
reinstalls from the same signed Bundle, then checks process readiness, in-cluster
egress policy, and protected resource identities again. If an install or check
fails after an install command succeeds, the test attempts the same
release-scoped cleanup and removes only image references that it proved absent
before importing. If the install command itself fails, it preserves imported
images and cluster state for checkpoint-based inspection because the installer
may have stopped after creating only part of a release.

Historical blocker, superseded 2026-09-29: earlier profile detection and
resolution proved `orbstack_shared_store`, but `installable` was false because
the core catalog entries were candidates. No qualified signed Bundle or
external trust-root key was then available. The `ops-system` namespace also
contained the existing `ops-core` Helm release, its OpenBao StatefulSet/PVC,
and connection resources without the new per-release label; the installer
correctly refused to adopt or remove that release. Those attempts did not pass
core installation, cleanup, or reinstallation.

The historical host-network probe recorded in
`artifacts/test-reports/task-2.7-orbstack-network-probe.log` reached
`1.1.1.1:443` after its internal positive control and exited 1. It used a
candidate PostgreSQL image and is not proof of the new Kubernetes-Pod probe or
of Bundle qualification. The new in-cluster probe could not run during those
earlier attempts because no qualified Bundle was available.

The live acceptance entry point is:

```sh
OPS_OFFLINE_CORE_INSTALL=1 \
OPS_OFFLINE_BUNDLE_DIR=/absolute/qualified-bundle \
OPS_OFFLINE_TRUST_KEY=/outside/bundle/trust.pem \
OPS_OFFLINE_RESOLVED_PROFILE=/absolute/core-resolved.yaml \
go test ./test/e2e -run '^TestCoreOfflineInstallation$' -count=1 -timeout=20m -v
```

It verifies the signed material plan and empty image cache, imports without
pulling, installs, performs a release-scoped clean reinstall that preserves PVCs,
and checks process readiness plus internal/public TCP connectivity from a core
policy-selected Kubernetes Pod. It leaves the second successful core deployment
available in `ops-system`. Missing qualified inputs, image presence, resource
conflicts, public TCP reachability, or a failed internal control make the live
test fail. These checks cover the exercised destinations and do not claim that
every protocol and destination has been audited.

## Current execution record (2026-09-28)

- `make check-toolchain`: exit 0; the repository-locked Go, Node, pnpm, Python,
  and uv versions are active.
- `go test ./internal/bundle ./internal/bundle/drivers ./cmd/opsctl ./test/e2e
  -count=1`: exit 0. `make check-generated`, `make check`, both core Chart
  `helm lint` commands, and `git diff --check` also exit 0. The default `make
  check` does not enable the live OrbStack installation test.
- The explicit live gate exits 1 before reading the Bundle or changing cluster
  state: the resolved core Profile is `installable: false` because bundled
  PostgreSQL, Keycloak, SeaweedFS, and vmalert remain `candidate`. The command
  output is in
  `artifacts/test-reports/task-2.7-live-gate-after-cleanup.log`.
- The original `make bundle-dev-arm64` placeholder exited 2. The target now
  calls the local packaging CLI and requires `BUNDLE_SPEC`, `BUNDLE_DIR` and
  external `SIGNING_KEY`. No qualified core material set is available; this
  implementation does not turn candidate artifacts into a signed core release
  or demonstrate installation.
- Follow-up packaging validation: `make bundle-dev-arm64` against a temporary
  first-party packaging Fixture exits 0. `make verify-bundle` with the separately
  supplied temporary public key exits 0 (3 files, 108 uncompressed bytes).
  The same build entry point with a PostgreSQL `17.11` candidate material exits
  2 with `candidate component "postgresql" cannot enter a Bundle`; no rejected
  output directory is published. The temporary private key has been removed.
  These are packaging Fixture results, not a qualified core Bundle.
- The follow-up `go test ./internal/bundle ./internal/bundle/drivers
  ./cmd/opsctl ./test/e2e -count=1`, `make check`, and `git diff --check` exit 0.
  The CLI round-trip test checks the actual four-file output against the
  independent public-key verification command; no live installation is enabled.
- VictoriaMetrics, VictoriaLogs and vmalert root LICENSE files were retrieved
  at their existing catalog commits. Their exact source URLs, sizes and SHA-256
  values are recorded in `third_party/licenses/victoria-root-license-lock.json`.
  Image/Chart file-license inventories, transitive dependencies and applicable
  source/notice obligations remain unconfirmed. Catalog `fileLicenses` and
  `dependencyClosure` stay pending and all three components stay candidate.
  Under final plan section 12, these unresolved obligations prevent release
  admission and live installation; collecting a root LICENSE cannot satisfy
  those gates.
- The existing host-network probe reached `1.1.1.1:443`; see
  `artifacts/test-reports/task-2.7-orbstack-network-probe.log`. The OrbStack
  cluster has no NetworkPolicy enforcement component in `kube-system`, and no
  core-policy-selected Pod egress denial has been demonstrated. Do not treat
  the successful code checks as the offline or clean-reinstall acceptance.

## Follow-up execution record (2026-09-28)

- PostgreSQL now mounts a writable `/var/run/postgresql`. Keycloak performs
  its local PostgreSQL/health build in an init container, starts optimized,
  and probes the management port 9000. The immutable distribution image and
  read-only root remain in use. Run
  `python3 test/fixtures/offline-core/dependency-startup.py` with the two pinned
  images already cached. It passes SQL `SELECT 1` and Keycloak management
  readiness with Docker networking disabled; its temporary credentials and
  owned containers are removed. This is an isolated startup PoC.
- The development environment has a policy-only kube-router `v2.11.1`
  controller, upstream commit
  `61066dc66a535f94761f447a90874d0a6839d0d3`, pinned arm64 digest
  `sha256:fec5ac13d36a812636d545263fda75e5b729ac9dac624f1f19f1170d3372324b`.
  Its exact manifest is `deploy/dev-network/kube-router-policy-only.yaml`.
  This is an explicitly prepared cluster prerequisite outside the core
  Bundle; the core installer does not download or install it. It runs only
  firewall policy enforcement, with no CNI installer, routing controller or
  service proxy. It uses host networking and `NET_ADMIN`/`NET_RAW`; its API
  permissions are read-only and do not include Secrets. Before reuse of this
  manifest, confirm its named resources are absent or owned by this preparation
  release; do not adopt existing network-controller resources.
- Run `python3 test/fixtures/offline-core/network-policy.py` in this prepared
  environment. The real core Chart blocks tested public TCP access for managed
  platform/dependency Pods while preserving DNS and the observed Victoria
  Service. A simulated external OpenBao release remains outside the policy
  target. The fixture owns and removes only its fresh namespace. Its output is
  `artifacts/test-reports/task-2.7-core-network-policy-poc.json`; the
  `coreOfflineInstallationPassed` field is false. The core policy now selects
  only this install's releases, and external egress is restricted to observed
  Service selectors/target ports.
- Actual OCI export/load with a fresh first-party arm64 image proved absence
  before import and retained the exact repository digest after load. The
  preparation record is
  `artifacts/task27-materials/oci-driver-fixture/report.json`. This demonstrates
  the engine's digest import path, not full core installation.
- A real OCI packaging Fixture initially failed verification with `window size
  exceeded`. The builder now uses a 1 MiB Zstd window; the verifier's existing
  memory bounds are unchanged. The regression
  `TestBuildPayloadLargerThanOneMiBRoundTripsWithinDecoderLimit` failed before
  the fix and passes after it. `make bundle-dev-arm64` and `make verify-bundle`
  then both exited 0 for the real OCI Fixture with a separately supplied public
  key. Its temporary private key was removed. Results are in
  `artifacts/test-reports/task-2.7-real-oci-bundle-fixture.json`; this is not a
  qualified core release.
- Existing Victoria discovery and the three isolated local Chart installs
  pass with the live flags enabled. Evidence:
  `artifacts/test-reports/task-2.7-victoria-poc.log`. Their health/version/source
  capabilities were checked; the fixture namespace was removed.
- `TestLockedVictoriaChartsRenderForOfflineInstaller` renders all three actual
  pinned archives with installer values and verifies images and ownership.
  Actual Helm `--post-renderer` execution also passes; see
  `artifacts/test-reports/task-2.7-vmalert-post-rendered.yaml`.
- Fresh profile detect/resolve succeed and prove the shared engine. The output
  is `artifacts/profiles/task-2.7-current-resolved.yaml`, with `installable:
  false`. Core bundled entries remain candidate.
- The explicitly enabled `TestCoreOfflineInstallation` still exits 1 at the
  Profile installability gate, before cluster changes. The current failure is
  recorded in `artifacts/test-reports/task-2.7-live-gate-current.log`.
  Full core import, installation, clean reinstall and post-install health checks
  did not execute. Code checks and targeted Go tests pass independently of this
  live gate.
- The image preparation scan now covers all seven pinned core/fallback images.
  Raw SPDX/Syft files stay in ignored `artifacts/task27-materials/sbom/`;
  package inventories and report hashes are in
  `third_party/admission/task-2.7-core-image-sbom-audit.json`. The tool is pinned
  to Syft `v1.52.0` and its official archive checksum. Remote metadata enrichment
  occurred during preparation; it is not an application runtime dependency.
  The raw Keycloak scan has 154 packages without license metadata. File mapping,
  notices and corresponding source obligations have not been fully confirmed;
  scanner success cannot qualify these images.
- OpenBao's reedsolomon module release lacks a LICENSE file, but its upstream
  license commit adds MIT without changing any of the ten release files.
  `third_party/licenses/openbao/reedsolomon-license-lock.json` records both
  exact commits, byte hashes and the supplemental license text. This closes
  that specific metadata gap and does not qualify the image's entire closure.
- The source/license admission decision is recorded as Proposed in
  [ADR-0010](../adr/0010-core-image-license-admission.md). Final plan section 12
  prevents emitting a qualified core release while those obligations cannot
  be confirmed. Full signed-Bundle install/reinstall and post-install core
  health/capability acceptance remain unpassed; Task 2.7 is not committed.

## Accepted execution record (2026-09-29)

- Resolved Profile: `artifacts/profiles/task-2.7-qualified-resolved.yaml`,
  `installable: true` on the OrbStack `linux/arm64` development cluster. Core
  PostgreSQL 17.11, Keycloak 26.7.4, SeaweedFS 4.47, vmalert v1.116.0 and the
  platform images are bundled. Existing OpenBao 2.7.0 and VictoriaMetrics /
  VictoriaLogs services are reused as external components. KubeVirt and CDI
  remain disabled, deferred and unverified under ADR-0008.
- The independently supplied trust root verified Bundle
  `task27-core-arm64-20260929-r5`: payload digest
  `sha256:12e1e8f8b16c2d061a8daad302520bb7fec931bdd3788541ead498d9b4eea38b`,
  66 payload files and 3,593,486,674 uncompressed bytes. Signature and full
  payload digest verification passed. The private signing key remained outside
  the repository and Bundle.
- The explicit `TestCoreOfflineInstallation` passed on the actual OrbStack
  cluster with `OPS_OFFLINE_CORE_INSTALL=1`, the qualified resolved Profile,
  the signed Bundle and the separately trusted public key. Before import, the
  test confirmed all six selected install image references and their manifest
  digests were absent from the OrbStack image store. Import verified the signed
  material plan and loaded the images without pull fallback.
- The test installed core, passed process checks and these live capabilities:
  SQL, OIDC discovery/JWKS, authenticated S3 object round-trip, Victoria source
  APIs, and unsealed TLS-verified OpenBao. It then removed only the releases
  created by that test after checking release ownership, preserved protected
  PVC identities, reinstalled from the same Bundle, and repeated the process
  and capability checks successfully.
- The core-policy-selected in-cluster probe passed its internal DNS control and
  its public IPv4/IPv6 TCP denial assertion after policy convergence. The
  assertion preserves earlier retry output and succeeds only when the final
  attempt reaches internal DNS while all tested public endpoints are denied.
  This establishes the tested destinations on this OrbStack setup; it is not a
  claim about every protocol or destination.
- Existing `ops-core` and its OpenBao PVC remained untouched. The PVC UIDs after
  the test were unchanged: OpenBao
  `c6d6fd30-b259-4edd-ab4d-457a86c9680f`, PostgreSQL
  `fa694502-a948-4fa7-b072-46eb2f4ce8bb`, and SeaweedFS
  `cacf92e6-8352-47e0-913c-4f9ff641eb34`.
- `make check-toolchain`, `make check-generated`, `make check`,
  `helm lint deploy/charts/ops-dependencies`, and
  `helm lint deploy/charts/ops-platform` passed from the Task 2.7 isolated
  source snapshot. The explicit live test took 255.77 seconds and exited 0.
- This acceptance is limited to the exercised OrbStack arm64 development
  profile. It does not qualify production HA or general Kubernetes
  compatibility. KubeVirt/CDI development and VM lifecycle acceptance remain
  deferred and unverified.

## Accepted source-bound replay (2026-09-29)

- The offline install/reinstall acceptance was replayed from clean source
  commit `f3ef3c4949cd24ac669a3c5c642d832a0cbd6e3a` after the contract test was
  made self-contained. The source snapshot's component catalog SHA-256 is
  `c481983181ae9ddd51a916f595f41f8fe48d6820749548dc5e44469e4d780700`; the
  resolved Profile, Bundle lock and Bundle payload digests are recorded with
  the command output in
  `artifacts/test-reports/task-2.7-live-core-r5-replay.log`.
- The first replay attempt exited 1 before Bundle import or cluster changes
  because the pre-existing OpenBao instance was sealed after its prior restart.
  It was unsealed through the documented external recovery workflow; recovery
  material was not recorded. The second attempt used the same source, resolved
  Profile, signed Bundle and independently supplied trust root and exited 0
  (`TestCoreOfflineInstallation`, 200.478 seconds).
- The successful run verified all six selected image references absent before
  import, verified the signed Bundle, imported without pull fallback, passed
  SQL/OIDC/S3/Victoria/OpenBao checks, completed release-scoped cleanup and
  clean reinstall, and passed internal DNS plus public IPv4/IPv6 TCP denial
  probes. Its post-run snapshot confirms `ops-dependencies`, `ops-platform`
  and `vmalert` deployed; pre-existing `ops-core`, `holmes` and `vm` remained
  deployed; and all protected PVC UIDs remained unchanged. See the raw,
  post-run-bound evidence log above for the full command, exit code and object
  identities. Its recorded SHA-256 is
  `e084099d0d77d92995079dafdcc6f160ef2d8102954dfd251b80eea39fff6f41`.
- `make check-toolchain`, `make check-generated` and `make check` passed on the
  same clean source snapshot. This replay covers only the OrbStack arm64
  development Profile; production HA and general Kubernetes compatibility
  remain unverified. KubeVirt/CDI remain deferred and unverified under ADR-0008.
