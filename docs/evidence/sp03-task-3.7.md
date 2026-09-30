# SP-03 Task 3.7 Evidence

Date: 2026-09-30

## Scope

Validated tenant-scoped audit append, RFC 8785 digests, bounded signed segments, encrypted S3 archives, Transit key rotation, tamper detection, pending-segment recovery, and runtime append-only permissions. The integration tests created temporary databases and deleted them at test cleanup. Test containers used separate names, an isolated Docker network, temporary configuration, and no persistent volumes. No shared PostgreSQL, SeaweedFS, OpenBao, PVC, or release was used.

## Test commands and results

```sh
go test ./internal/audit ./internal/archive ./internal/crypto ./internal/integrations/openbao ./internal/integrations/s3 -count=1
```

Exit code: `0`.

```sh
SP03_TEST_DATABASE_URL='postgres://postgres@127.0.0.1:32768/postgres?sslmode=disable' \
  go test ./test/integration -run '^(TestMigrationScriptsAreForwardOnlyAndContiguous|TestMigrationsAndRoles|TestMigrationsUpgradeVersionOneData|TestWithTenantTxIsolatesTenantsAndUsesAppendOnlyAudit|TestAuditSegmentsAppendConcurrentlyRecoverAndVerifyTenantScoped)$' -count=1 -v
```

Exit code: `0`. This covered migration 00013, RLS and role privileges, global tenant-safe sequences, 20 concurrent writers, archive tampering, S3 upload and signing failure recovery, and a 10,001-record case split into segments of 10,000 and 1 records.

```sh
set -a
. /tmp/codex-sp03-audit.Vx0PzA/credentials.env
set +a
export SP03_TEST_OPENBAO_URL='https://127.0.0.1:32771'
export SP03_TEST_OPENBAO_CA_FILE='/tmp/codex-sp03-audit.Vx0PzA/openbao-ca.pem'
export SP03_TEST_S3_ENDPOINT='http://127.0.0.1:32772'
SP03_TEST_DATABASE_URL='postgres://postgres@127.0.0.1:32768/postgres?sslmode=disable' \
  go test ./test/integration -run '^TestRealOpenBaoTransitAndSeaweedS3AuditArchive$' -count=1 -v
```

Exit code: `0`. The credentials file held only ephemeral test credentials, was outside the repository with mode `0600`, and was not copied into this evidence. The test verified old Transit ciphertext after key rotation, signature acceptance and tamper rejection, versioned Object Lock writes, idempotent S3 upload retry, retention delete rejection, PostgreSQL append, encrypted segment archival, tenant scoping, and end-to-end signature verification after another Transit key rotation.

## Isolated object identities

| Service | Identity | Result |
| --- | --- | --- |
| PostgreSQL test container | `sp03-postgres-test`, container `4de3eaf39f0aafb099672ce97913dc2e3ad601b0308ed3f284a77f462cfd80d7`, image `postgres@sha256:75731e2765e7d0c8bb7dea960ef3bdcde68d16314991ab2057a2a74ea0fff257` | Disposable databases were created for the test cases and dropped on cleanup. |
| OpenBao test container | `sp03-openbao-audit-20260930`, container `de954ba2a08905a9bb6186176a2fa6eb5150ac3a40c554449199467db9d945b9`, image `ghcr.io/openbao/openbao@sha256:4ca9310dd2a50c746d4227f44058088ee0470a8470031ee3f09cc8b1a69dd7f6` | Health API reported version `2.7.0`, initialized and unsealed. |
| SeaweedFS test container | `sp03-seaweed-audit-20260930`, container `d6b532a392027d614534c63a5c4564baaa82079f5b1e9d6fa25df3fdd616aeb1`, image `docker.io/chrislusf/seaweedfs@sha256:d4cf67729aa8777e1a43a5b61d72e5b96179e4b7bac9a221cb14cbc2036cb32e` | `weed version` reported `4.47`, commit `c50733600`, `linux arm64`. |
| S3 integration object | Bucket `sp03-2245d144`, object `50ac8519-bda9-4294-b043-581ec07f37ea` | Upload/read/retry succeeded. Compliance Object Lock rejected early version deletion. |
| Signed audit segment | Tenant `27ca47fa-149e-4c91-8a1c-8d7533b9e393`, segment `3bb2c84a-4ff2-41c1-b252-8b6409a3ee30`, audit sequence `1` | Encrypted archive and Transit signature verified before and after Transit key rotation. |

The successful end-to-end run printed only non-secret object IDs, a SHA-256 digest of the synthetic S3 fixture, and migration status. No token, S3 credential, recovery material, certificate private key, or plaintext credential was recorded.
