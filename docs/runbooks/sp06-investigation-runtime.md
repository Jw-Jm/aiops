# SP06 nonvirtual investigation runtime

Enable SP06 only with the admitted Linux/arm64 Bundle, SP04 Resource/Evidence
and SP05 Finding/Incident/RCA already enabled. Apply forward-only migration
00034 with the migration identity before starting the updated API/Worker. Keep
ADR0008 virtualization and ADR0020 PyRCA disabled/unverified. This stage exposes
read-only investigation and advisory ActionPlan results; it grants no command,
SSH, exec, SQL or general URL capability.

## Deployment and admission

Verify the signed Bundle, exact image/source/notices and resolved core Profile
through the existing offline installation flow. The default core Profile/Chart
keeps SP06 disabled. For SP06, use the verified bundled platform Chart with
explicit operator values: `sp04.enabled`, `sp05.enabled`, `sp06.enabled`, an
explicit `sp06.tenants` list, the current published `sp06.policyName`, the admitted
`components.investigator.image` digest, identity/model Secret names and exact
model host `/32` or `/128` egress CIDRs/port. Do not enable the default component
with a mutable image tag. No additional Profile/v1 fields are introduced.

The only new resident process is `ops-investigator`. API serves the standard
MCP/Job API on 8083; Worker dispatches into investigator on 8090. The investigator
has no service-account token, DB login or source credentials. Its sole network
paths are API, the explicitly admitted model host and cluster DNS. Match model
DNS resolution to the egress host CIDR. The OrbStack development bridge uses
`http://host.docker.internal:11434/v1`; its DNS Host header is required, and its
resolved host address must match the explicit egress policy. This development
bridge is not a production model availability or capacity claim.

Provision the existing projected workload identities for `ops-api`, `ops-worker`
and `ops-investigator`; certificates must have the exact namespace/service-account
SPIFFE URI, current trusted issuer and fresh signed CRL. Investigator's expected
API DNS name and Worker SPIFFE URI must agree with the release namespace. Refresh
certificates/CRLs through the existing workload bootstrap and update projected
Secrets before expiry; a CRL older than 300 seconds fails closed. Secrets contain
TLS keys and the model credential only; never place them in Helm values, logs,
Bundle, prompts or model parameters.

After normal OpenBao bootstrap, `opsctl openbao configure --investigation-signing`
(with the existing resolved Profile, external CA and private recovery-file
arguments) creates the nonexportable ECDSA P-256 `investigation-signing` Transit
key. API/Worker projected policies can sign/read this key; investigator cannot.
The private key remains inside Transit. Mount no root/recovery token in production.
The local isolated acceptance fixture uses an external private mounted credential
and is separately labelled; it is not a production projected-authentication claim.
Keep old public verification versions until issued Contexts expire. A retired
key, stale Context/Lease generation or unavailable signer rejects admission.

## Policy, budget and model

Publish and activate a signed PolicyRegistry/v2 through the existing registry
workflow. A Job records the current policy/authorization revision and immutable
scope digest. Namespace policies must resolve to one homogeneous active policy
version; mixed versions fail closed rather than expand scope. Current role,
cluster, namespace, source and policy checks repeat on reads and before writes,
including cached tool results. Roles or sources withdrawn during SSE/tool/model
work invalidate subsequent exposure or commit.

The Model Contract has exactly `base_url`, `api_key_ref`, `model`, `timeout` and
`token_budget`. This admission is for Ollama `llama3.1:8b-16k`; the mounted Secret
reference is `secret://model/api-key`. Other model/context/cost profiles require
new admission. Timeout is 1–120 seconds; output reservation is at most 8192 tokens.
The approved local endpoint uses a nonempty local credential even when Ollama
has no server authentication. Probe the actual endpoint/model before enabling
dispatch; protocol compatibility is not RCA accuracy qualification.

The default Job limit is 600 seconds and 40 tools, plus explicit raw-query,
result-byte, graph/evidence and model/token limits in Chart values. Profile/Policy
can only lower those limits. The ledger reserves each call atomically before any
remote work, consumes nonce in that transaction and conservatively charges the
reserved upper bound when actual usage is unknown. Exhaustion immediately stops
work with honest partial/unresolved or failed status. Do not reset counters on
restart, retries or takeover. Remote tools/models never run inside DB transactions.

## Recovery and operator diagnostics

Job creation is idempotent for trusted tenant/subject/Incident/revision/trigger,
policy and scope. Dispatcher claims a fenced generation, heartbeats while the
resident executes, then final Go validation checks evidence/source/retention,
Recipe/candidate and advisory target gates. An interrupted process leaves durable
Steps. After the 45-second Lease expires, another Worker claims a newer generation,
charges unknown in-flight usage and resumes the next uncommitted call. Committed
successes are reused by exact call digest, not re-executed. Old generation writes
or old Contexts cannot complete/commit. Cancel/expire/complete races serialize in
PG. Keep the ledger and audits; never manually set a failed job to completed.

Use the authenticated Investigation API for create/get/steps/cancel. SSE uses
fetch with a Bearer token, a signed durable Last-Event-ID and monotonic event IDs;
it reconstructs all events from PG in bounded batches. A slow writer is closed by
the configured write deadline. Reconnect with the last accepted cursor, ignore
exact duplicates and fail closed on gaps, withdrawal, or expired cursor (410).
The API and main Finding/Incident/Graph/Evidence/RCA chain remain independently
available when the model/investigator fails. `/api/v1/capabilities` reports current
API and investigation state; supply an authorized `clusterUid` to observe Graph
readiness/source degradation. Without that scope these fields are null, not an
invented healthy value. Completed Jobs do not prove current model readiness.

Check current Job errorCode/ledger/audit, workload identity/CRL, current source and
policy version, API↔investigator reachability, model endpoint and reserved budget.
Do not bypass source archive permission, retention dependencies or Legal Hold to
recover an investigation. Evidence already referenced in audit retains the full
365-day protection closure and required Transit key versions. Recommendations
cannot manufacture confirmed RCA or an execution handle.

## Source materials and acceptance

ADR0027 and `docs/poc/holmes-investigator-reuse-lock.yaml` bind exact unmodified
Holmes 0.42.0, official provider/core extensions and MCP 2025-06-18 SDKs. The isolated
platform wrapper is GPL-3.0-or-later; each upstream package retains its own original
license. Distribute the exact corresponding-source archive and notices with every
investigator image. Python wheels, Debian sources, declared Cargo source superset,
C native source and offline rebuild receipts have distinct measured scopes.

Preparation authenticates source via `prepare-investigator-{base,rust,native}-source.py`
and `prepare-investigator-material.py`; `build-investigator-{tiktoken,crc32c}.sh`
run in the recorded compiler environments with network disabled. Compiler-image
preparation uses the pinned Python base, `build-essential=12.12`,
`pkg-config=1.8.1-4`, `ca-certificates=20250419` and, for CRC, `cmake=3.31.6-2`.
These are build-only prerequisites. The final investigator Dockerfile installs
only hash-checked runtime wheels without network. Preserve all original notices
and exact platform-native build locks; do not claim publisher-wheel equivalence
for tiktoken/CRC replacements.

Current mandatory command results and source bindings belong to
`docs/evidence/sp06-20261003`. Historical SP01–05 reports are not current evidence.
Read the Task ledger and final gates before making an acceptance claim. Dedicated
performance/load/capacity/P95 are user-waived and never marked passed; virtualization
and RCA accuracy on the small protocol model remain unverified. Only delete test
resources whose namespace/labels, image identities and lack of shared consumers
prove this run's ownership. Shared services, existing data and unrelated releases
are preserved.

## Isolated acceptance dependency recovery

The SP06 fixture accepts `--container-suffix` and `--private-root`. After a host
restart clears temporary material, keep existing owned containers/data and use
a fresh suffix with a private directory outside Git (mode 0700); do not remove
shared services. Recover the owned PostgreSQL connection only from its verified
container identity and private environment, never from printed credentials.
`/tmp/ops-sp06-services-current-dir` points to the generated private configuration;
recreate only that pointer if the private directory survives. Restore locked
source commits, original publisher artifacts and signing trust before rerunning
mandatory gates. A replacement acceptance signing key changes only this isolated
Bundle fixture; it does not change production trust.

2026-10-04 独立审核后的边界：迁移 00035 将每次准入的数据级别快照写入 admissions；已验签并注册的 Context 可继续收窄，不能由原 Job 的上限恢复权限。缓存工具/模型结果也须满足原准入快照及当前 Context、Lease、源权限。内部 Steps、成功 Step 恢复和缓存模型输出在持有 Job 锁时记入 `result_read` Ledger/Audit 及 ResultBytes；不增加工具或模型调用次数，耗尽同事务转为 partial/unresolved 并 fencing。新工具的第一次输出由原预算预留和结算承担，后续恢复读取另行记账。SSE 在每条事件发送前重查当前授权及 token 到期，并将写截止限制在 token 有效期内。创建及取消幂等键绑定主体、租户、目标和请求语义；冲突为 409 IDEMPOTENCY_CONFLICT，预算不足为 429 BUDGET_EXHAUSTED，MCP nonce 重放仍为 REPLAY_REJECTED。
