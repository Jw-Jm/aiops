# SP-03 foundation runtime

The API process now serves only SP-03 tenant/role, Source/Cluster and signed configuration administration, plus `POST /api/v1/auth/step-up-sessions`. The latter accepts an empty object with an Idempotency-Key; identity and fresh LoA-2 claims come exclusively from the verified Keycloak access token. Domain ingestion, graph, Incident, Agent and Runner endpoints remain outside this runtime.

## Database identities

Provision separate LOGIN identities for API and Worker. Grant only `api_runtime_role` to the former and only `worker_runtime_role` to the latter. Both NOLOGIN roles remain NOBYPASSRLS. Runtime startup selects the expected role and rejects a login with superuser, BYPASSRLS, CREATEROLE, CREATEDB, REPLICATION, migration/owner membership or membership in the other process role. Bootstrap/migration credentials must never be used for runtime, even with SET ROLE.

The `ops-platform-runtime` Secret requires separate `apiDatabaseURL` and `workerDatabaseURL` keys. Web gets no DATABASE_URL or database Secret reference. Update deployment Secrets through the environment's normal credential provisioning process; this review does not change the installed core Secret or releases.

## API

Required process variables: `DATABASE_URL`, `OIDC_ISSUER_URL`, `PLATFORM_PROFILE`. `PLATFORM_API_ADDR` defaults to `:8080`; metrics default to `:9090`. Discovery and JWT verification use audience `ops-api` and explicit tenant role bindings. Supply `PLATFORM_REGISTRY_TRUST_FILE` as a JSON map of key ID to base64 Ed25519 public key. Missing/empty trust disables publication; keys embedded in requests cannot add trust. The chart mounts this public configuration at `/etc/ops/registry-trust.json` from `runtime.registryTrust`.

## Audit Worker

Required variables: distinct `DATABASE_URL`, `OIDC_ISSUER_URL`, `PLATFORM_PROFILE`, `OPENBAO_ADDR`, `OPENBAO_CA_FILE`, `OPENBAO_SERVICE_DOMAIN`, `S3_ENDPOINT`, `S3_BUCKET`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`. The chart reads the two S3 credentials from `archiveAccessKey` and `archiveSecretKey` in the runtime Secret. Public chart inputs are `runtime.openbaoAddress`, `runtime.openbaoCABundle`, `runtime.archiveEndpoint`, `runtime.archiveBucket`. Mount only the bootstrap CA certificate, never the OpenBao TLS private key.

`OPENBAO_PROJECTED_TOKEN_FILE` defaults to `/var/run/secrets/ops-platform/openbao/token`. The Worker reads this audience-bound projected JWT only for Kubernetes auth login/re-login as `ops-worker`; the returned OpenBao token remains in memory and is replaced at 2/3 lease TTL. No root token environment variable is accepted. Bootstrap must already have configured the runtime policy, `evidence-archive` encryption key and `audit-signing` key. Runtime startup does not configure OpenBao or create/change S3 bucket settings.

Every minute, the Worker retries pending segments and drains bounded batches per tenant. A segment has at most 10,000 records and a five-minute timestamp span. Upload/signature failures keep immutable pending state for retry. Tenant failures are isolated. Monitor `platform_audit_signing_delay_seconds`; a value above 600 seconds violates the development acceptance gate. An unavailable signing backend is reported in structured logs and degradation state; it must not be treated as successful signing. After recovery, verify that every pending segment reaches signed and verify archived ranges.

Initialize the dedicated S3 bucket using the controlled bootstrap adapter's Object Lock/versioning setup, then provision its runtime credentials. Do not call the bucket bootstrap operation on a shared bucket during review.

KubeVirt/CDI remain deferred and unverified. This runtime does not install or enable virtualization profiles.
