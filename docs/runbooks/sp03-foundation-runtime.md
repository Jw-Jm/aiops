# SP-03 foundation runtime

The 2026-10-01 specification synchronization is recorded in
[ADR-0017](../adr/0017-align-sp01-sp03-with-reviewed-specification.md). Apply
forward-only migration 00017 before starting the updated runtime. The source
registry accepts optional `backendLogicalId` and `dataScopeMapping` declarations
through its existing protected writes; legacy records remain readable with empty
mapping. A completed backend ID is immutable. Empty scope is no permission;
`queryCapability` stays `disabled/unverified` until actual SP-04 adapter admission.
An administrator cannot claim verification in a request.

New Worker audit segment/proof objects default to 365 days of retention and are
read back and verified before database completion. Provision corresponding
capacity and retain Transit key versions for the referenced lifetime. Existing
objects keep their stored retention; extending their lifetime requires the
backend's controlled retention extension, not a metadata-only change. A readback
failure preserves pending state for recovery. This change does not establish
production HA/capacity or implement future evidence Legal Hold/dependency flows.

The API process now serves only SP-03 tenant/role, Source/Cluster and signed configuration administration, plus `POST /api/v1/auth/step-up-sessions`. The latter accepts an empty object with an Idempotency-Key; identity and fresh LoA-2 claims come exclusively from the verified Keycloak access token. Domain ingestion, graph, Incident, Agent and Runner endpoints remain outside this runtime.

## Database identities

Provision separate LOGIN identities for API and Worker. Grant only `api_runtime_role` to the former and only `worker_runtime_role` to the latter. Both NOLOGIN roles remain NOBYPASSRLS. Runtime startup selects the expected role and rejects a login with superuser, BYPASSRLS, CREATEROLE, CREATEDB, REPLICATION, migration/owner membership or membership in the other process role. Bootstrap/migration credentials must never be used for runtime, even with SET ROLE.

The `ops-platform-runtime` Secret requires separate `apiDatabaseURL` and `workerDatabaseURL` keys. Web gets no DATABASE_URL or database Secret reference. Update deployment Secrets through the environment's normal credential provisioning process; this review does not change the installed core Secret or releases.

## API

Required process variables: `DATABASE_URL`, `OIDC_ISSUER_URL`, `PLATFORM_PROFILE`. `PLATFORM_API_ADDR` defaults to `:8080`; metrics default to `:9090`. Discovery and JWT verification use audience `ops-api` and explicit tenant role bindings. Supply `PLATFORM_REGISTRY_TRUST_FILE` as a JSON map of key ID to base64 Ed25519 public key. Missing/empty trust disables publication; keys embedded in requests cannot add trust. The chart mounts this public configuration at `/etc/ops/registry-trust.json` from `runtime.registryTrust`.

## Audit Worker

Required variables: distinct `DATABASE_URL`, `OIDC_ISSUER_URL`, `PLATFORM_PROFILE`, `OPENBAO_ADDR`, `OPENBAO_CA_FILE`, `OPENBAO_SERVICE_DOMAIN`, `S3_ENDPOINT`, `S3_CA_FILE`, `S3_BUCKET`, `S3_TENANT_CREDENTIALS_FILE`. The chart mounts only `archiveTenantCredentials` from the runtime Secret into the Worker. This strict JSON has schemaVersion `ops-archive-credentials/v1` and a `tenants` array of `{tenantId, accessKey, secretKey}` entries. UUIDs and access keys must be unique; the input is limited to 1 MiB and 4096 tenants. There is no global credential fallback. Public chart inputs are `runtime.openbaoAddress`, `runtime.openbaoCABundle`, `runtime.archiveEndpoint`, `runtime.archiveCABundle`, `runtime.archiveBucket`. Mount only independently verified public CA certificates, never TLS private keys. S3 requires HTTPS and verifies the endpoint hostname against its separate CA; HTTP is accepted only for literal loopback test fixtures.

`OPENBAO_PROJECTED_TOKEN_FILE` defaults to `/var/run/secrets/ops-platform/openbao/token`. The Worker reads this audience-bound projected JWT only for Kubernetes auth login/re-login as `ops-worker`; the returned OpenBao token remains in memory and is replaced at 2/3 lease TTL. No root token environment variable is accepted. Bootstrap must already have configured the runtime policy, `evidence-archive` encryption key and `audit-signing` key. Runtime startup does not configure OpenBao or create/change S3 bucket settings.

Every minute, the Worker retries pending segments and drains bounded batches per tenant. A segment has at most 10,000 records and a five-minute timestamp span. Upload/signature failures keep immutable pending state for retry. Tenant failures are isolated. Monitor `platform_audit_signing_delay_seconds`; a value above 600 seconds violates the development acceptance gate. An unavailable signing backend is reported in structured logs and degradation state; it must not be treated as successful signing. After recovery, verify that every pending segment reaches signed and verify archived ranges.

Initialize the dedicated S3 bucket using the controlled bootstrap adapter's Object Lock/versioning setup, then provision a separate IAM principal for each tenant, limited to its deterministic `tenants/<sha256 of ops-archive/v1|tenant UUID>/` prefix. Configure read/write/list only under that prefix, never the entire bucket. The runtime requires own-prefix listing and AccessDenied/403 for unscoped/foreign listing and foreign reads. An unconfigured or overprivileged principal keeps pending state and fails closed. Update both server IAM and the external runtime Secret for tenant creation/rotation, restart the Worker, verify pending recovery, then retire the old key. Do not call the bucket bootstrap operation on a shared bucket during review.

KubeVirt/CDI remain deferred and unverified. This runtime does not install or enable virtualization profiles.

## Signed audit proof format

Migration 00014 preserves previously signed v1 manifests and uses v2 for new segments. The v2 Transit-signed manifest binds the previous segment ID (or null for the first segment). An immutable `audit-proof` S3 object archives the manifest, signature and key version beside the encrypted `audit-segment` object. A retry reuses an existing verified proof, including after signing-key rotation, before completing the database transition.

`VerifyRange` requires its endpoints to be audit sequences belonging to the requested tenant. Empty/missing coverage is an error. It verifies the archived bytes, archived signature proof and signed predecessor chain; removing an entire middle segment cannot become an apparently valid range. Historical v1 manifests remain untouched but have no signed predecessor proof, so ranges relying on their chain fail closed. Do not rewrite historical signatures or report legacy verification as passing; migration of a historical proof boundary requires a separately approved design.
## 核心业务指标补充（2026-10-18）

指标继续由进程私有 Prometheus registry 导出，沿用现有 metrics 访问边界；没有新增租户、资源、命令或主体标签。

- `platform_work_duration_seconds{area}`：`evidence_query`/`evidence_read` 覆盖实际 HTTP Evidence 操作；`incident_aggregation` 覆盖实际聚合处理，不包含队列等待及外层事务提交，不是端到端 SLO。
- `platform_queue_depth{queue}`：Finding pending（含 claimed）/deadletter、Incident pending、Investigation queued/running 的当前数据库计数。
- `platform_recent_results{area,state}`：最近五分钟启动的工具 Step、创建的工具 admission 和更新过的命令执行记录的当前状态计数。`area` 为 `tool`/`tool_admission`/`action`，排除 model Step；这是 Gauge，不能按累计成功事件 Counter 计算增长率。
- `platform_investigation_terminal_mean_seconds`：最近五分钟更新且当前为终态的调查记录，其 `updated_at-created_at` 均值；没有样本为 NaN。该值含排队时间，可能受终态后更新影响，不代表模型耗时或 P95。
- `platform_database_up`：Worker 最近一次有界 Ping 是否成功；进程初始尚未采样时为 NaN。Ping 成功不证明业务表权限和全部统计可读。
- `platform_business_snapshot_available` 与 `platform_business_snapshot_timestamp_seconds`：本轮全量业务统计是否完成、最后一次完整采样时间。

数据库快照复用 Worker 现有每分钟循环，总预算两秒，逐租户使用现有 RLS 事务和已有权限。任一读取失败或超时，available=0、队列/结果/调查均值置 NaN；不能把失败解释为空队列。API 等未运行采样循环的进程保持快照不可用。多个 Worker 观测同一数据库总体，按实例过滤或取 max，不能相加；同时检查 available 和采样时间，不能沿用过期值冒充实时值。本地记录/导出测试不证明远程吞吐、时延或容量达标。
