# SP-03 Task 3.9 Evidence

Date: 2026-09-30

## Scope

Added JSON structured stdout logging with redaction for secret-like fields and embedded credentials; safe request IDs carried through OIDC authorization errors and normal HTTP responses; W3C trace context propagation and optional OTLP/HTTP export; Prometheus HTTP/process/operation metrics with bounded labels; a shared API/worker metrics listener and conditional VMServiceScrape / ServiceMonitor rendering when the corresponding CRD is present.

Metrics contain no tenant, resource, path, query, or credential labels. Trace configuration/export failures are reported as degraded and do not fail API or worker requests. The HTTP metrics middleware preserves http.Flusher for the repository's existing SSE handlers. No SP-04 or later business implementation was added. No VM/VMI/DataVolume adapter, virtualization identity, or virtualization/full profile was added.

The SP-03 exit-condition review also found Source registrations had immutable history but no rollback operation. Commit 380d0b2 adds the OpenAPI-first rollback endpoint: a new registration starts active; an administrator may restore a prior immutable registration revision, which appends a new audited revision. Restoring an older auth reference increments the credential generation monotonically, so an identity from before the rollback stays invalid.

## Test commands and results

The first run of the new Source rollback integration test was deliberately red before the service implementation:

~~~sh
SP03_TEST_DATABASE_URL='postgres://postgres@127.0.0.1:32768/postgres?sslmode=disable' \
  go test ./test/integration -run '^TestSourceRegistrationsAreTenantBoundRevisionedAndAudited$' -count=1
~~~

Exit code: 1 (rollback API/service were not implemented yet). After implementation, the same isolated PostgreSQL test passed with exit code 0; it verifies registration, concurrent revision conflict, credential rotation, disable, rollback to revision 1, monotonic credential generation, tenant isolation, audit history, and idempotent HTTP replay.

The stream-writer regression test also followed red/green:

~~~sh
go test ./internal/httpapi -run '^TestObservabilityMiddlewarePreservesStreamingFlush$' -count=1
~~~

Before the fix, exit code: 1 (instrumentation removed http.Flusher; response was not flushed). After the minimal writer wrapper fix, exit code: 0.

Final locked checks:

| Command | Exit code |
| --- | ---: |
| make check-toolchain | 0 |
| make check-generated | 0 |
| make check | 0 |
| go test ./test/security -count=1 -v | 0 |
| Full isolated go test ./test/integration -count=1 -v | 0 |
| Real Victoria smoke test below | 0 |
| git diff --check | 0 |

The full isolated integration command was run with only the test endpoints and ephemeral credentials loaded from the mode-0600 file outside the repository:

~~~sh
set -a
. /tmp/codex-sp03-audit.Vx0PzA/credentials.env
. /tmp/codex-sp03-audit.Vx0PzA/keycloak-test.env
set +a
export SP03_KEYCLOAK_TEST_ISSUER='http://127.0.0.1:32775/realms/ops'
export SP03_TEST_OPENBAO_URL='https://127.0.0.1:32771'
export SP03_TEST_OPENBAO_CA_FILE='/tmp/codex-sp03-audit.Vx0PzA/openbao-ca.pem'
export SP03_TEST_S3_ENDPOINT='http://127.0.0.1:32772'
export SP03_TEST_DATABASE_URL='postgres://postgres@127.0.0.1:32768/postgres?sslmode=disable'
export SP03_TEST_VICTORIA_METRICS_URL='http://127.0.0.1:32774'
export SP03_TEST_VICTORIA_LOGS_URL='http://127.0.0.1:32773'
go test ./test/integration -count=1 -v
~~~

The full-suite run also sourced keycloak-test.env and set SP03_KEYCLOAK_TEST_ISSUER to http://127.0.0.1:32775/realms/ops. Exit code: 0. It includes the live Keycloak PKCE and TOTP flow, real Source registration rollback; Policy, Recipe, and Tool signed publish/activate/rollback; pool reuse across tenants; OIDC role binding and step-up persistence; idempotency; migration/role boundaries; OpenBao Transit plus SeaweedFS S3 archival; OpenBao workload PKI issue/revoke; actual VictoriaMetrics scrape and VictoriaLogs query. Runtime UPDATE/DELETE/INSERT attempts against append-only audit state were rejected.

The standalone live Keycloak test also passed with exit code 0:

~~~sh
set -a
. /tmp/codex-sp03-audit.Vx0PzA/keycloak-test.env
set +a
export SP03_KEYCLOAK_TEST_ISSUER='http://127.0.0.1:32775/realms/ops'
export SP03_TEST_DATABASE_URL='postgres://postgres@127.0.0.1:32768/postgres?sslmode=disable'
go test ./test/integration -run '^TestKeycloakAuthorizationCodePKCEAndStepUp$' -count=1 -v
~~~

The temporary Keycloak used the repository's realm-ops.json, created a disposable user with required TOTP enrollment, completed PKCE login and a fresh LoA-2 step-up, verified tenant/subject/SID/ACR/auth_time claims, persisted the step-up session, and confirmed idle expiry. The isolated admin password and generated user password stayed in temporary runtime state; neither was logged or recorded here.

For the actual metrics/logs service smoke test:

~~~sh
SP03_TEST_VICTORIA_METRICS_URL='http://127.0.0.1:32774' \
SP03_TEST_VICTORIA_LOGS_URL='http://127.0.0.1:32773' \
  go test ./test/integration -run '^TestRealVictoria(MetricsScrapesPlatformProcessMetric|LogsAcceptsStructuredPlatformStdout)$' -count=1 -v
~~~

Exit code: 0. A newly started API process on port 19393 and worker on 19394 both exposed platform_process_up 1 and platform_observability_degraded 0. The isolated VictoriaMetrics scraper queried the API process metric. The VictoriaLogs test pushed a scrubbed structured stdout fixture, queried it back, and confirmed its synthetic bearer value was absent. This validates the endpoint and ingestion/query paths; it does not claim an in-cluster stdout forwarder was installed.

## Isolated object identities

| Service | Identity | Result |
| --- | --- | --- |
| PostgreSQL | sp03-postgres-test, container 4de3eaf39f0aafb099672ce97913dc2e3ad601b0308ed3f284a77f462cfd80d7, image postgres@sha256:75731e2765e7d0c8bb7dea960ef3bdcde68d16314991ab2057a2a74ea0fff257 | Integration cases created and dropped disposable test databases. Migrations reached version 13. |
| OpenBao | sp03-openbao-audit-20260930, container de954ba2a08905a9bb6186176a2fa6eb5150ac3a40c554449199467db9d945b9, image ghcr.io/openbao/openbao@sha256:4ca9310dd2a50c746d4227f44058088ee0470a8470031ee3f09cc8b1a69dd7f6 | Transit archive/signature flow passed; PKI role platform-workload-ops-api issued and revoked certificate serial 5102727437775b8f0deda918a7f7ae9ce91e6055 with a one-hour lifetime; CRL verification passed. |
| Keycloak | sp03-keycloak-test-20260930, container 24b7f3b04787b56319d3962fccbaf1bbdecc1f6805e600f0802275624fc27153, image quay.io/keycloak/keycloak@sha256:1f91ac24e8d68b8189d5d53a8381464c1db0fcff479348d5de973a86b63d621c | Disposable start-dev container imported the platform realm; live PKCE and TOTP step-up integration passed. No persistent volume or shared Keycloak instance was used. |
| SeaweedFS | sp03-seaweed-audit-20260930, container d6b532a392027d614534c63a5c4564baaa82079f5b1e9d6fa25df3fdd616aeb1, image docker.io/chrislusf/seaweedfs@sha256:d4cf67729aa8777e1a43a5b61d72e5b96179e4b7bac9a221cb14cbc2036cb32e | Versioned isolated S3 object storage used by the audit archive test. |
| Archive integration object | Bucket sp03-218c3a66, object d55cc371-2f00-4a41-af73-599c4fc314fd | Encrypted object digest verified after archive and signature verification. |
| Signed audit segment | Tenant 39d28b26-3102-450c-896a-5f36c0b2331a, segment 4bff0a59-f9d2-4e27-9d19-102c29174b5e, sequence 1 | Signature and archived segment verification passed. |
| VictoriaMetrics | sp03-victoria-metrics-observability-20260930, container e17684d2a96501ad6620bb647b2d92e39884ecb4285d68ab443848430bef0363, image victoriametrics/victoria-metrics@sha256:b10c78f4bd9b52554b7f863ff416e480d931b1811f591049d166eea1fb247638 | Scraped the isolated platform API process. |
| VictoriaLogs | sp03-victoria-logs-observability-20260930, container 2b8cca5aab69336567c8b2dde9a4438e2067f25347375e4a7367d9c4e7fe9df7, image victoriametrics/victoria-logs@sha256:47b820890d64c4575a2a0a46415dcd8a4fd59a0f1fcd6a377693d7aea639442e | Accepted and returned the scrubbed structured stdout fixture. |

## SP-03 exit conditions and evidence gaps

- Tenant pool reuse: with a single-connection runtime pool, tenant B saw zero tenant A rows, could not insert a tenant A row, and no app.tenant_id value remained after returning the connection to the pool.
- Non-inheriting roles: both operator -> platform_admin and platform_admin -> operator returned forbidden in security tests. Operator resource access also requires explicit scope.
- Source and configuration lifecycle: Source registration creates an active revision and supports audited rollback to a selected immutable revision. Signed Policy/Recipe/Tool versions publish, activate, roll back by reactivating an earlier version, and preserve immutable history.
- Audit immutability: API/Worker runtime roles could append through the fixed function but UPDATE, DELETE, and direct INSERT against audit records failed.
- Trace export: propagation, trace creation, exporter connection refusal, degraded reporting, and request continuity are covered by local tests; no live OTLP collector was provisioned.
- Kubernetes/OpenBao TokenReview: no isolated Kubernetes API/TokenReview was provisioned; the standalone OpenBao PKI issue/revoke path passed, but a live projected-token login remains unverified as recorded in Task 3.8 evidence.
- Helm scrape resources were rendered against VMServiceScrape, ServiceMonitor, and neither-CRD cases. No chart was applied to the shared Kubernetes cluster; its pre-existing services and releases were left untouched.

No root 00–10 formal design document was changed. No secret, token, recovery value, private key, or plaintext credential was written to Git or this evidence.
