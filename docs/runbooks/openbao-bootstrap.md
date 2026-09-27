# OpenBao bootstrap and recovery (development profile)

This runbook applies to the single-replica development OpenBao release. It uses integrated Raft storage on a PVC, TLS from an explicitly trusted bootstrap CA, and Shamir sealing with three shares and a threshold of two. The pod is not privileged and does not use `hostPath`; `disable_mlock=true` is set for the constrained development runtime.

Production IPC locking, HA, auto-unseal, backup recovery objectives, and cross-failure-domain behavior remain production release gates. This runbook is not evidence that those production properties have been accepted.

## Prepare external operator files

Choose a private directory outside the Git checkout and every Bundle. Protect the directory and keep the files in operator-controlled storage:

```sh
install -d -m 700 /secure/operator/ops-openbao
```

Use a resolved Deployment Profile that selects bundled OpenBao. The `init` command creates the bootstrap CA public certificate and one-time Shamir recovery file with mode `0600`. It never prints their contents. The recovery file contains the three Shamir shares and the initial root token; protect it as a secret. The CA private key is not retained after issuing the OpenBao server certificate.

```sh
opsctl openbao init --profile /path/to/resolved-deployment-profile.yaml \
  --ca-file /secure/operator/ops-openbao/bootstrap-ca.pem \
  --recovery-file /secure/operator/ops-openbao/recovery.json
```

On a new installation, run `init` first: it creates the bootstrap TLS Secret required for the OpenBao pod to start, then performs one-time initialization. After that, `status` may run without a recovery file; it reports the seal state and explicitly says configuration drift was not checked. To verify configuration after initialization, pass the recovery file:

```sh
opsctl openbao status --profile /path/to/resolved-deployment-profile.yaml \
  --ca-file /secure/operator/ops-openbao/bootstrap-ca.pem \
  --recovery-file /secure/operator/ops-openbao/recovery.json
```

## Human-controlled unseal and configuration

After initialization, an authorized operator explicitly submits two different shares. Each invocation handles one share and reports the remaining threshold progress. Do not copy recovery values into command arguments, tickets, terminals saved to logs, or test reports.

```sh
opsctl openbao unseal --profile /path/to/resolved-deployment-profile.yaml \
  --ca-file /secure/operator/ops-openbao/bootstrap-ca.pem \
  --recovery-file /secure/operator/ops-openbao/recovery.json --share-index 0
opsctl openbao unseal --profile /path/to/resolved-deployment-profile.yaml \
  --ca-file /secure/operator/ops-openbao/bootstrap-ca.pem \
  --recovery-file /secure/operator/ops-openbao/recovery.json --share-index 1
opsctl openbao configure --profile /path/to/resolved-deployment-profile.yaml \
  --ca-file /secure/operator/ops-openbao/bootstrap-ca.pem \
  --recovery-file /secure/operator/ops-openbao/recovery.json
```

`configure` refuses uninitialized or sealed instances. It enables Kubernetes auth with the OpenBao service account's TokenReview identity, creates the platform Transit key, a constrained PKI role and SSH CA role, and verifies those settings. It records the observed OpenBao version in the resolved profile. Keep the initial root token only for this bootstrap and recovery procedure; platform workloads use the configured least-privilege Kubernetes auth roles.

## Restart and recovery

A StatefulSet pod restart reuses its Raft PVC and returns OpenBao to `sealed`. Check `opsctl openbao status`, then have two authorized operators submit their shares using the `unseal` command above. Check status again with the recovery file to verify the persisted configuration. If the PVC is missing, the CA trust does not match the mounted TLS certificate, the recovery file is unavailable, or configuration reports drift, stop and restore through the approved operator recovery process; do not initialize over existing data.

When OpenBao is unavailable or sealed, operations requiring Transit decryption, PKI issuance, or SSH signing must fail closed. Read-only investigation paths that do not require those secrets are intended to continue. Never put recovery material into a Kubernetes Secret, Bundle, repository, platform database, or log.

## Task 2.3 OrbStack verification record

Verified on 2026-09-27 against OrbStack Kubernetes `v1.35.6+orb1` on `linux/arm64`, using OpenBao `v2.7.0` image digest `sha256:4ca9310dd2a50c746d4227f44058088ee0470a8470031ee3f09cc8b1a69dd7f6`. The isolated `ops-system` namespace was absent before this task. The `ops-dependencies` release rendered PostgreSQL, Keycloak, and SeaweedFS as `external`; only the OpenBao workload was created. The API server accepted the rendered StatefulSet, PVC, ServiceAccount, and TokenReview ClusterRoleBinding. The PVC used the OrbStack default `local-path` StorageClass with a 2 GiB request.

The operator recovery files are retained outside the repository and Bundle at `/Users/mssc/.local/share/ops-platform/task-2.3-openbao/bootstrap-ca.pem` and `/Users/mssc/.local/share/ops-platform/task-2.3-openbao/recovery.json`. The parent directory is `0700`; both files are `0600`.

Observed results:

- `opsctl openbao init` created the bootstrap TLS Secret, initialized once, and reported `state=sealed`; the external recovery file and CA file were both mode `0600`. The recovery file was not printed or copied to Kubernetes. Re-running initialization is covered by the automated test and refuses to overwrite that file.
- `opsctl openbao configure` while sealed exited `1` with `OPENBAO_SEALED`. A direct wrong-share API probe returned HTTP `400`; the instance remained sealed with progress `0`.
- The first authorized share returned `sealed` with progress `1`; the second returned `ready` with progress `0`. `configure` completed against the actual OpenBao API and `status` verified the Transit key, PKI issuer/role, SSH CA/role, Kubernetes auth, policies, and roles at `2.7.0`.
- The PKI mount `max_lease_ttl` was `31,536,000` seconds before root generation; the issuer certificate was valid from `2026-09-27 11:36:26 UTC` through `2027-09-27 11:36:56 UTC`.
- A short-lived token minted for a temporary `ops-api` ServiceAccount authenticated through Kubernetes auth with HTTP `200`. The OpenBao TokenReview identity can create TokenReviews and cannot read Kubernetes Secrets. The input token and returned OpenBao client token were held only in process memory and were not recorded. The temporary ServiceAccount was deleted after the check.
- Deleting the `ops-worker` ACL policy in this isolated test instance made `status` return `configuration_drift`; `configure` restored the missing policy and status returned `ready`.
- Deleting and recreating `ops-openbao-0` changed the Pod UID from `80d29881-e5a4-400e-906a-3c6b222099c2` to `90f6098c-456f-4d54-bb19-b1583effd417`, while the PVC UID remained `c6d6fd30-b259-4edd-ab4d-457a86c9680f`. The restarted instance reported `sealed`, then returned to `ready` after two shares; status re-verified the persisted configuration.

This is a single-node development persistence and recovery check. It does not qualify production HA, auto-unseal, IPC locking, backup recovery objectives, or application-level read/RCA and cryptographic failover behavior. The latter application paths are not implemented in Task 2.3 and remain later runtime gates.
