# SP-03 Task 3.8 Evidence

Date: 2026-09-30

## Scope

Implemented short-lived workload mTLS identities from OpenBao PKI, Kubernetes projected ServiceAccount token exchange, in-memory ECDSA private keys, exact SPIFFE and DNS SAN validation, certificate renewal, independent CRL refresh, fail-closed stale/revoked certificate handling, and workload-only projected token mounts. Runtime workload policies can sign only for the matching ServiceAccount PKI role. No VM/VMI/DataVolume identity or virtualization profile was added.

## Test commands and results

The first security-test run preceded the implementation and failed to compile because the workload identity API was intentionally not present yet (exit code `1`).

```sh
go test ./internal/auth ./internal/integrations/openbao ./test/security ./test/contract ./test/e2e -run 'Test(OpenBaoBootstrap|CertReloader|WorkloadIdentity|UserToken|WorkloadsProjectOpenBaoTokenOnlyToServiceProcesses)' -count=1
```

Exit code: `0`. This covered OpenBao bootstrap role/policy contracts, in-memory key creation, projected-token login, PKI sign requests, renewal, mTLS client identity, audience-bound Helm token projection, exact SAN/allowlist checks, user-token separation, dynamic CRL refresh, and stale/revoked certificate rejection.

```sh
go test ./internal/integrations/openbao ./test/security -run 'Test(CertReloader|WorkloadIdentity|UserToken)' -count=1
```

Exit code: `0`.

```sh
set -a
. /tmp/codex-sp03-audit.Vx0PzA/credentials.env
set +a
export SP03_TEST_OPENBAO_URL='https://127.0.0.1:32771'
export SP03_TEST_OPENBAO_CA_FILE='/tmp/codex-sp03-audit.Vx0PzA/openbao-ca.pem'
go test ./test/integration -run '^TestRealOpenBaoWorkloadPKIIssuanceAndRevocation$' -count=1 -v
```

Exit code: `0`. The test configured workload PKI roles and ServiceAccount-bound OpenBao auth policies, issued an exact `ops-api` workload certificate from a P-256 CSR, rejected a CSR with a different namespace, revoked the issued serial, and verified the signed CRL contains it. The isolated run reported role `platform-workload-ops-api`, certificate serial `3b837be24ebf5815e24be23d12c21be8a8773dc6`, and an effective lifetime of about one hour (OpenBao backdates `NotBefore`).

The OpenBao root token and bootstrap CA were supplied from an ephemeral mode-`0600` file outside the repository. The token, key material, and credentials were not written to logs or this report.

## Isolated service identity

The integration used only the temporary OpenBao container `sp03-openbao-audit-20260930`, container ID `de954ba2a08905a9bb6186176a2fa6eb5150ac3a40c554449199467db9d945b9`, image `ghcr.io/openbao/openbao@sha256:4ca9310dd2a50c746d4227f44058088ee0470a8470031ee3f09cc8b1a69dd7f6` (OpenBao `2.7.0`). No shared OpenBao, Kubernetes cluster, persistent volume, or release was changed.

## Evidence gap

An attempt to run the full production OpenBao bootstrap against the standalone isolated container could not configure Kubernetes auth: OpenBao reported that `/var/run/secrets/kubernetes.io/serviceaccount/ca.crt` was absent. The container had no Kubernetes service-account CA or TokenReview endpoint. The isolated test therefore exercised actual PKI issuance, revocation, and auth-role/policy configuration, but did not claim a successful live Kubernetes TokenReview/login. Projected-token exchange and role binding were covered by local HTTP contract tests. A real isolated Kubernetes TokenReview remains unverified.
