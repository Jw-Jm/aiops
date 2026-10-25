# 修复进度（主分支评审 MR-001～MR-020）

基线：`a2fa14f419abdc4587f56a1cbae59eb6cbb48c4e`（分支 `fix/main-review-20261025`）。
只记录实际完成；原评审证据与 r2/r3 日志保留不动。

- `implemented`：产品/持久化/接口已接通并留 diff；
- `local_checked`：本地实际跑过的命令与结果；
- `codex_reviewed`：Codex 独立审核结论（**不自行填通过**）；
- `remote_verified`：`ssh mssc-direct` 真实验收（尚未开始）。

## 总表

| MR | 主题 | implemented | local_checked | codex_reviewed | remote_verified |
| --- | --- | --- | --- | --- | --- |
| MR-001 | 当前资源统计编译/预算合同 | **是**（r9 成本合同按来源） | r13 MCP→持久预留→Graph→结算/预算拒绝通过 | r13指定预算链通过 | r13远程受控E2E通过；安装态真实来源待验 |
| MR-002 | v2 调查 Job 原生授权来源 | **是**（r7 完整链路，r8 修集中反馈四项＋HTTP 创建证据） | 见 r7/r8（单元＋竞态通过，PG 仅编译） | 待审核 | 待真实 PG/API |
| MR-003 | 高权限命令 MFA 贯穿 | **是**（r8 闭环，r9 补会话有效性/真实 MFA HTTP/68 反例） | 见 r8/r9（单元＋竞态＋前端通过，PG 仅编译） | 待审核 | 待真实 PG/受控 Runner |
| MR-004 | 调查 SSE 原生 Cookie | **是**（r9 去除 Bearer 前置） | 见 r9（node+vitest 通过） | 待审核 | 待真实浏览器 |
| MR-005 | 多集群同名历史关系 | **是**（r10 精确身份，r11 cluster_id＋字面前缀＋真实存储夹具） | r13真实空库及精确关系正反例通过 | r11产品查询接受；r13夹具修复并实测通过 | r13真实PG通过 |
| MR-006 | 运行时源码闭包 | **是**（r11 来源锁续期；r12 补齐可重建的第一方输入与模块图元数据） | r12 两架构6个二进制空缓存离线重建，与仓库构建逐字节一致 | 本轮离线重建缺口已由 Codex 自行修复、实测通过 | 原生执行及完整 Bundle 验收未执行 |
| MR-007 | 迁移/发行基线统一 | **是**（r11 窄范围历史 Down 准入＋基线 69＋两架构锁续期） | r11静态/契约通过；r13空库及63→69实际前滚通过 | 静态复审通过；本轮前滚通过 | r13真实空库/63→69通过 |
| MR-008 | sqlc 生成物 | **是**（随 00068 重新生成；r11 复跑两次无变化） | `make check-generated` 通过两次 | 独立 check-generated 通过 | — |
| MR-009 | 密码散列并发上限 | **是**（已接受，未重做） | race 通过 | 已接受 | 待测 RSS/OOM |
| MR-010 | SP05 降级指标 | **是**（r10 业务健康，r11 注册随 Worker 生命周期） | 见 r10/r11（真实 startup 回归通过） | 生命周期修复独立复审通过；真实采集链路待验 | 待真实上报路径 |
| MR-011 | 助手切集群忙碌状态 | **是**（r9 busy 归属） | 见 r9（vitest 通过） | 待审核 | 待真实浏览器 |
| MR-012 | Incident 原生写授权 | **是**（r7 完整链路，r8 修并发撤权用例 context） | 见 r7/r8（单元＋竞态通过，PG 仅编译） | 待审核 | 待真实 PG/API |
| MR-013 | 故障链迟到响应归属 | **是**（r9/r10 请求归属，r11 父页归属绑定真实状态） | 见 r9～r11（真实 AssistantPage 回归通过） | 归属增量独立复审及19项定向前端测试通过 | 待真实浏览器 |
| MR-014 | MFA 重置密码版本完整性 | **是**（第二轮返修） | 见下 | 方向已接受；本轮增量**待复审** | 待真实 PG |
| MR-015 | OpenBao 不确定写入 | **是**（r10 远端确认，r11 待提交材料与 fail closed） | r13路径/私钥回归及远程OpenBao全包25项通过 | r11两项复现已修复；安全复核意见已落实并回归 | r13真实K8s回执丢失恢复、TLS握手/错误私钥反例通过 |
| MR-016 | PKI CRL 独立刷新 | **是**（r10 续签失败仍刷 CRL） | 见 r10（loopback 通过） | 待审核 | 待真实 CRL |
| MR-017 | API→Worker 原生身份贯通 | **是**（r7 修正两断点） | r13会话/任务Graph200及Evidence/Analysis/Audit原生RPC200通过 | r13指定RPC正向缺口已补齐 | r13远程PG+mTLS+正式入口通过；来源协议夹具边界见报告 |
| MR-018 | 资源指标 Step ID | **是**（r9 去除变量遮蔽，r10 修正夹具确定性） | r13指标适配器→提交→evaluator→答案→HTTP读回通过 | r13StepID引用全链通过；跨作业/篡改反例通过 | r13远程真实PG/正式HTTP通过（指标协议夹具） |
| MR-019 | Node Unknown 自动恢复 | **是**（r9 显式结清 Unknown） | r13 Unknown→True→Finding resolved→Incident settle通过 | r13指定业务链通过 | r13远程真实PG通过（Node/归档协议夹具） |
| MR-020 | 跨挑战失败额度 | **是**（第二轮返修） | 见下 | 方向已接受；本轮增量**待复审** | 待真实 PG/双实例 |

## 账户安全第二轮返修（对应增量复核四项）

1. **revision 只推进一次**：`step_up_attempt_begin` 不再写挑战行（预留＝账户行锁＋revision CAS），成功由 `step_up_challenge_add_method` 推进一次，失败由 `step_up_attempt_failed` 推进一次。`add_method` 不再补偿 attempts。既有夹具（一次外部校验 1→2）与生产调用链恢复一致。
2. **测试确定性**：冷却边界先建挑战再上锁（创建在冷却期正确 429，不再用 201 的 helper）；在途替换改用**错误码**并断言 `attempts=1/failed_count=1`（成功路径应为 0）；并发改密 vs MFA reset 的败者接受 `409` 或 `401`（被撤销会话先拒是合法合同），并断言最终版本/唯一 active 行/旧密码不复活。
3. **比较前边界直接可观测**：测试用 `observingSealer` 包装真实 `FileSecretSealer`，每个 handler 实例一个包装，`Open` 是 TOTP 密钥解封即比较前边界。首个请求持账户锁在 `Open` 处被阻塞，第二个请求确实被锁挡住（未完成），释放后断言败者 `Open` 计数为 0、赢家为 1；陈旧/过期/替换/无额度四类反例同样断言 `Open=0`。
4. **手册可执行**：恢复流程改为实际存在的 `user_administration` step-up（`administrative_write` 的 method/path/bodyDigest，`initialPassword` 以会话 CSRF 做 HMAC，写入带 `X-Ops-Step-Up-Challenge`），删除裸 `password_reset` 操作名；租户隔离核对绑定同一目标 user；明确密码重置只清登录失败计数，不清 step-up 因子冷却（迁移注释同步修正，未新增清除逻辑）。
5. 新增 `step_up_budget_factor_state` 在“窗口未过但计数已满”时也 fail closed（手工构造短冷却行不再出现 allowed+remaining=0）。

## MR-017 · 原生 API→Worker 可信身份贯通（首版）

**签名上下文（`internal/graph/protocol.go`）**

- `Claims` 新增显式 `source`：`legacy` / `native_interactive` / `native_task`，并配 `nativeProof{sessionId|jobId}`；`VerifyContext` 严格解码（拒绝未知字段）、校验来源枚举与证明字段形状，未知/缺失/不完整来源直接拒绝。
- 新增 `ResolveNativeClaimTx`：只把签名里的**引用**当索引，账户/会话/任务权威全部从数据库重读——交互式经新窄函数 `identity.native_internal_session_shared`（账户→会话锁序，校验账户 active、会话未撤销/未过期、credential/session 版本、subject）；任务来源读 `investigation.jobs.assistant_binding` 的持久化原生权威（`authorizationSource=native`、user、version、domain），并在重建前后各读一次要求一致。
- 新增 `RequireNativeRoleTx`：`platform.native_authority_shared` 复核当前 role 与授权版本，供审计 RPC 使用。
- 新增 `Authorization.EffectiveClaim`：legacy 保持旧 `role_bindings` 行为；原生来源用 `EffectivePersistedNativeTx`（既有共享锁/撤权复核）重建 scope，并要求重建结果与签名 scope 的摘要一致（或满足 `GrantSnapshotDigest` 子集）；audit 域任务拒绝 operator 业务 scope；原生来源**不回退** legacy。
- 迁移 `00066_native_internal_session_scope.sql` 只新增上述会话解析窄函数（schema_owner→api/worker/migration 授权，FORCE RLS 下按租户可见）。

**调用方**

- API 签名：`callWithGrantDigest` 增加显式 proof；交互式请求从可信 `NativeSessionContext` 生成 `native_interactive`，assistant 工具/源能力从持久化 job 的原生权威生成 `native_task`，审计验证同样按来源签名，游标显式 `legacy`。不转发 Cookie/密码/可重用凭据，令牌仍 30 秒内有效并绑定 body digest 与 mTLS。
- Worker 校验：`graph.InternalHandler`、`sp04_runtime`（3 处）、`assistant_analysis`（4 处）改用 `EffectiveClaim`；`sp08_audit_verify` 按来源分别用 legacy `RequireAdmin` 或 `RequireNativeRoleTx(platform_admin)`，审计成功不顺带授予 operator。

**测试**

- `internal/graph/protocol_claims_test.go`：来源枚举/缺字段/未知值/形状错配、payload 替换、租户替换、audience、过期、超 30 秒、伪造签名、注入未知字段全部拒绝。
- `test/integration/native_internal_authority_test.go`：真实 PG 下原生 operator 会话重建 scope 成功；签名 scope 被放宽拒绝；无证明/跨租户拒绝；legacy 上下文不借原生授权；撤权、重新授权（新版本）、会话撤销、账户停用后拒绝；原生 `platform_admin` 复核成功且审计来源不被当作 operator scope。（编译通过，未执行 → remote_pending。）

**第二轮续修（对应 MR-017 复核五项）**

1. Worker 可用性：`00066` 增补 `GRANT USAGE ON SCHEMA identity TO worker_runtime_role`（只给 schema 名解析，不给任何 identity 表权）；新增 worker pool 与真实 worker 角色用例，断言 worker 不能直读 `identity.accounts` 但可调窄函数。
2. 任务权威：新增 `00067 platform.native_internal_task_shared`，在账户→grants→job 共享锁序下重证 v3 任务的生命周期（queued/running、未过期、未取消）、原生来源/版本、冻结 grant 与 execution scope 摘要、conversation/turn 归属与 input/context 绑定；`ResolveNativeClaimTx` 改为调用它并按 domain 限制角色（operational≠admin、audit≠operator），不再用两次相邻 SELECT 当栅栏。
3. 调用方：新增 `graph.WithTaskAuthority/TaskAuthorityFromContext`，`Authorization.Effective` 在有任务上下文时改走任务重证，因此 assistantGraphReader 的发送前检查、InvestigationTools.read 的 SP04 资源/Evidence 前置与最终授权、analytics/finding/sp05 的复核与签名全部按任务来源执行；签名统一用 `internalProofFromContext`（会话→交互式、任务→persisted task、否则 legacy），未知/缺证明不回退。
4. 合同与生成物：`api/internal/graph-v1.yaml` 新增 `SignedContextEnvelope`（source 枚举、nativeProof、失败关闭与不回退规则），并重新生成 `gen/internalgraph/openapi.gen.go`；`make check-generated` 通过。
5. 新增 `test/integration/native_internal_rpc_test.go`：真实 Sign/Verify + 自签 CA/mTLS 客户端证书 + 真实 `graph.InternalHandler` + worker pool，覆盖交互式 operator 通过授权边界、无客户端证书 403、legacy 不借权、加宽 scope 拒绝、跨租户拒绝、持久任务通过重证、任务取消后 403，以及 worker 无 identity 表直读权。测试编译通过，未执行（remote_pending）。

**仍存缺口（诚实记录）**

- 受控 RPC 用例的"正向 200"未断言：本测试只证明授权边界通过后到达 lease 阶段（返回 GRAPH_NOT_READY），完整 200 需要真实发布图状态，属远程验收。
- 审计 RPC（`serveWorkerAudit`）的 mTLS 正向未纳入该测试（函数未导出），目前由 `sp08` 白盒测试与 `RequireNativeRoleTx` 覆盖；真实审计 Worker 链路待远程。
- 账户安全：观察器改为增量断言（fixture 历史 Open 不计入），hold 后注册 cleanup 释放；冷却测试改为先建挑战再写入锁定行。

## 本轮运行过的命令与结果

日志：[evidence/r5/](evidence/r5/)（r2/r3/r4 保留）。

| 命令 | 结果 |
| --- | --- |
| `go build ./...` / `go vet ./...` | 通过（[vet.log](evidence/r5/vet.log)） |
| `go test ./internal/...` | 仅既有 4 项 Bundle 失败（`opa-sdk-source`，MR-006）（[go-unit-internal.log](evidence/r5/go-unit-internal.log)） |
| `go test -race ./internal/identity ./internal/httpapi` | 通过（[race.log](evidence/r5/race.log)） |
| `go test -race ./internal/graph -run TestVerifyContext` | 通过（[race-graph.log](evidence/r5/race-graph.log)） |
| `go test ./test/integration -run '^$'` | 编译通过（[integration-compile.log](evidence/r5/integration-compile.log)） |
| `go generate`（仅 internalgraph）+ `make check-generated` | 通过；`gen/internalgraph/openapi.gen.go` 已按新 header 合同重新生成（[check-generated.log](evidence/r5/check-generated.log)） |
| `go test ./test/contract` | 仅既有 `TestDeliveryPreparationSourceBaseline`（MR-007，[contract.log](evidence/r5/contract.log)） |
| 迁移静态门禁 | 仍 17 个历史 Down（00047–00063）；00064/65/66/67 未列（[forward-migration-static.log](evidence/r5/forward-migration-static.log)） |
| `gofmt -l` / `git diff --check` | 仅既有未格式化文件；diff 检查干净 |

## 明确未完成 / 待接续

- 全部真实 PG/RPC 用例未执行（无本地 PG，未启动 Docker/OrbStack）：账户安全、MR-017 交互式/任务授权与 mTLS RPC、迁移 64–67 前滚与 RLS 均 remote_pending。
- MR-017 受控 RPC 用例正向上限为"通过授权后到 lease 阶段"，完整 200 与审计 RPC 的真实 Worker 链待远程。
- 前端本轮未改；`make test-web` 未重跑（上轮 153/154，唯一失败为仓库外夹具）。
- MR-009 RSS/OOM、MR-006 闭包、MR-007 发行基线均未处理。
- 其余 MR-001/002/003/004/005/010/011/012/013/015/016/018/019 待修；本节点不是总体完成，未提交/推送/合并/部署。

## r6（MR-017 收口尝试 + MR-002/012 未开始）

本轮实际完成（本地代码审核待定）：

- `internal/graph/protocol.go`：拆出 `resolveTaskAuthorityTx`，本地 `effectiveTask` 不再用“空骨架 claims”走外部签名校验（修正“必然摘要拒绝”）；本地路径改为读取持久事实 + `EffectivePersistedNativeTx` 重建当前权限，并限制不得超出任务冻结执行范围。
- `coversScope` 按既有 `ScopeCoversMappedOwner` 语义重写（grant 资源为空=不限；受限 grant 不覆盖空资源；namespace 必须包含），删除原先反转的资源判断。
- 外部签名路径改为：请求 scope 必须落在任务冻结 `effective_scope` 内（不再只校验总 grant），`grantSnapshotDigest` 必须与持久摘要一致。
- `00067` 增补：`job.requesting_subject = account.subject`、`platform.sp06_lock_policy(...)` 策略有效性、以及 `Valid` 要求的 catalog/prompt/model/dataset/result schema 字段。
- RPC 用例：`queryKind` 改为合法值 `entity`；把持久任务正向前置为显式 `t.Skip`（缺真实 policy registry 链），不再以手写无效任务充当正向。

未完成（诚实记录，不视为 ready）：

- MR-017 仍缺：可执行 200 的图/Lease/来源正向、Evidence/Analysis/Audit 正向与中途撤权、真实 API 签名调用方链路；当前 RPC 用例只覆盖授权边界的部分负例。
- MR-002（v2 持久原生来源 + migration 68 + 全链路复核 + 验收用例）未开始。
- MR-012（Incident 写事务原生授权）未开始。
- 本机未启动 PG/Docker/K8s；所有 PG/RPC 用例未执行。

证据：`evidence/r6/`（vet/unit/integration-compile/check-generated/contract/forward-migration-static）。

## r7（MR-017 两处断点收口 + MR-002/MR-012 完整业务链）

### MR-017 遗留断点（按上轮复核两项纠正，另附可执行本地单元）

1. **完整 grant 与冻结 execution 倒置**：`Authorization.effectiveTask` 改为先重证 live scope 的**完整 grant 摘要**等于冻结 `GrantSnapshotDigest`，再要求冻结执行 scope 被该 grant 覆盖（`taskAuthorityHolds`，纯函数）。原实现用冻结的窄执行 scope 反比完整 grant，正常任务必然被拒。`full grant vs 窄执行 scope`、资源受限 grant、namespace 收缩、摘要缺失均有用例。
2. **00067 集群键**：`job.effective_scope->>'cluster'` 改为 `>'clusterUid'`（`graph.Scope` 的 wire 键）。新增静态守卫 `TestNativeTaskProjectionsUseScopeWireKeys` 同时校验 00067/00068 使用真实键并调用 `sp06_lock_policy`。
3. **可执行本地单元**：`internal/graph/protocol_task_test.go` 用脚本化 `pgx.Tx` 覆盖 v2/v3 投影选择、摘要篡改、执行 scope 越界、未知 task kind；`taskAuthorityHolds` 的真实资源收窄正向在无数据库下可跑。
4. `graph.EffectiveTx` 拆出 `EffectiveLegacyTx`（显式旧表分支）与 `ScopeCovers`（跨身份系统的无 revision 包含规则），供 v2 Job 与 Incident 复用。

### MR-002 · v2 持久调查来源（完整链路）

- **迁移 `00068_native_incident_job_authority.sql`**：`investigation.jobs` 新增 `authorization_source/authorization_user_id/authorization_version`（CHECK：NULL 仅历史 legacy、`legacy` 无 user/version、`native` 必须有 user 且 version≥1；NOT VALID→VALIDATE）；新增 `platform.native_incident_task_shared`（账户→grant→job 共享锁、生命周期/过期/来源/版本/subject 绑定/冻结 scope 摘要/策略有效性）与 `platform.native_incident_job_subject_shared`（终态读取仍要求 active 账户与 subject 绑定）。函数只授予 api/worker/migration。
- **持久化**：`Job` 增加非序列化 `authority`（v2 外部 JSON 不变）；`load` 从新列恢复并拒绝未知/不完整三元组；`CreateJob` 只从可信认证 context 捕获来源（native 会话→native，否则 legacy），与 Job/队列/事件审计同事务写入；幂等键重放与 ON CONFLICT 命中均要求来源一致，否则 `ErrConflict`。
- **全链路复核**：`current` 按持久来源分派——native 走 `native_incident_job_subject_shared` + `graph.EffectivePersistedNativeTx`（共享锁、account/version/撤权复核）并与冻结 digest 比对；legacy 走 `EffectiveLegacyTx`（保留 `sp06_lock_authority`）；未知来源拒绝且**不 fallback**。`admit` 为入库前同规则证明。覆盖 Claim/Heartbeat/RegisterContext 续签/工具 BeginCall/恢复读取/Complete（均经 `withLease→checkLease→current`）。
- **工具装配**：`InvestigationTools.read` 对 v2 native Job 也注入 `graph.WithTaskAuthority`，`resolveTaskAuthorityTx` 按 schema_version 选择 v3/v2 投影，因此无 Session 的嵌套 SP04/SP05 读取与签名 RPC 都按持久权威复核。
- **用例**（PG，编译通过，未执行）：`test/integration/native_incident_job_test.go` —— native operator + 旧表清空下 Create→无 Session Claim→Heartbeat→Context 续签→Authenticate→`get_incident_context`（Incident+关联 Finding 实读）与 `get_recent_changes`（证据实读/结算保留）→Complete；Worker 接管/过期；撤权、重授、停用、收窄、跨租户、历史 NULL 行、源 revision 变化、中途撤权均拒绝且不复活的负例。
- **本地单元**：`internal/investigation/authority_test.go`（来源捕获、编码合法性、未知来源/未落库拒绝）。

### MR-012 · Incident 写事务原生授权（完整链路）

- `internal/incident/authority.go`：新增 `authorizeTx`/`operationAuthority`。按可信 ctx 分派：**native** 先 `authz.NativeRoleAuthorized`（会话绑定、撤权/停用/版本），再 `graph.EffectivePersistedNativeTx` 在同一业务事务内重建当前 operator scope（账户→grant 共享锁），要求覆盖请求 scope 与主资源；**legacy** 保持 `platform.sp05_lock_operator_scope` 原语义与锁序。
- `Authorize`/`AuthorizeAll` 改为单次证明 + 逐关联资源 `authorizeResource`（关联 Finding、每条人工 Evidence 精确校验 tenant/cluster/namespace/resource；native 由已重建 grant 覆盖，legacy 仍逐条加锁）。`Change`/`Merge`/`Split`/`manualEvidence` 全部接入；保留 source revision 锁、可信时间、归档/retention 校验与时间线/审计写入，未改 00028、未镜像旧表。
- **用例**（PG，编译通过，未执行）：`test/integration/native_incident_mutation_test.go` —— 真 SP05 HTTP（路径改写+幂等事务）下确认/缓解（含人工证据保留）/抑制/关闭、确认与合并/拆分的幂等重放、resolved 需归档证据；拒绝：租户/集群/namespace/资源越界、收窄 grant、停用、撤权后重放、撤权后新写；并发撤权被共享锁阻塞（55P03）。本地单元 `internal/incident/authority_test.go` 覆盖纯包含判定与前置拒绝。

### 生成物与发行

- Schema 变更后重新生成 sqlc 模型：`internal/persistence/dbgen/models.go`（`make check-generated` 通过）。
- 迁移 00064–00068 未触发静态门禁新增失败；`00047–00063` 的 17 个历史 Down 仍失败（MR-007，未处理）。

### 本轮本地实际运行（`evidence/r7/`）

| 命令 | 结果 |
| --- | --- |
| `go build ./...` / `go vet ./...` | 通过（build.log/vet.log） |
| `go test ./internal/...` | 仅既有 4 项 `internal/bundle` 失败（MR-006，go-unit-internal.log） |
| `go test -race ./internal/incident ./internal/investigation ./internal/httpapi` | 通过（race.log） |
| `go test -race ./internal/graph -run 'TestTaskAuthority|TestResolveTaskAuthority|TestVerifyContext'` | 通过（race-graph.log） |
| `go test ./internal/{graph,incident,investigation,httpapi}` | 通过（go-unit-touched.log） |
| `go test ./test/integration -run '^$'` | 编译通过（integration-compile.log） |
| `make check-generated` | 通过（check-generated.log） |
| `go test ./test/contract` | 仅既有 `TestDeliveryPreparationSourceBaseline`（MR-007，contract.log） |
| `TestNativeTaskProjectionsUseScopeWireKeys` | 通过 |
| 迁移静态 `TestMigrationScriptsAreForwardOnlyAndContiguous` | 仍 17 个历史 Down（forward-migration-static.log） |
| `git diff --check` / 触及文件 `gofmt -l` | 干净（diff-check.log/gofmt.log；仅既有未格式化 `native_action_chain_test.go`） |

### 明确未完成（诚实记录）

- 本机未启动 PostgreSQL/Docker/K8s/模型/浏览器：MR-002 与 MR-012 的 PG/HTTP 用例**只完成编译**，未执行；真实前滚、RLS、锁序与正向 200 待 `mssc-direct` 集中运行。
- MR-017 完整 RPC 正向夹具（图/Lease/source 200、Evidence/Analysis/Audit、真实 API 签名调用方、策略 fixture）仍缺，保留为最终集中验收缺口，未以 `t.Skip` 冒充通过。
- MR-007（迁移/发行基线）与 MR-006（运行时闭包）仍失败；MR-008 生成物已随本轮 Schema 同步。
- 其余 MR-001/003/004/005/010/011/013/015/016/018/019 未修；本节点不是总体完成，未提交/推送/合并/部署。

## r8（集中反馈四项 + MR-003 原生高权限命令完整闭环）

### A. 集中复核四项（MR-002/012）

1. **创建事务内重证当前原生会话**：`internal/investigation/authority.go` 新增 `reproveNativeSessionTx`。`CreateJob` 的三条路径都在**写事务内**用当前可信会话重证：新建（`admit`→native 分支）、幂等键重放（`request_keys` 命中后）、同身份唯一键冲突（`ON CONFLICT` 命中后）。重证内容＝`identity.authorize_native_session_role`：tenant/subject/user/会话 ID、full 模式、账户 active 与密码状态、account 的 credential/authorization/session 三版本与签发版本一致、会话未撤销未过期、operator grant 版本一致。会话撤销、密码重置（credential_version 轮换）、session_version 轮换、账户停用都拒绝且**不写 Job/queue/audit**。已持久化的 Worker 侧仍走 `current` 的持久身份分支，不重新依赖浏览器。
2. **历史 NULL 来源语义兼容**：`jobAuthority.same` 按**语义身份**比较——历史空串与显式 `legacy` 是同一身份系统（可幂等重放返回同一 Job），`native` 永不等于 `legacy`，非法编码永不等于任何来源；历史行字段**不回填**，`same` 之外仍由 `valid()` 与 00068 约束失败关闭。回归覆盖：历史 NULL 行重放返回同一 Job 且编码不变；native 与 legacy 跨来源借用仍 `ErrConflict`。
3. **00068 CHECK 三值逻辑漏洞**：约束改为整表达式 `IS TRUE`，并显式枚举三种形状——完整 native（source=native 且 user 非空且 version 非空且 ≥1）、完整 legacy（source=legacy 且 user/version 全空）、全 NULL 历史形状。部分 NULL 组合不再漏网。68 未应用，直接修正原文件，未新增复制迁移。
4. **并发撤权用例 context**：`TestNativeIncidentMutationLocksSerializeRevocation` 的业务授权改用 `account.ctx`（真正进入 native 分支并取得 account→grant 共享锁），撤权仍用无 native 的独立管理连接 + `lock_timeout` 验证等待（55P03）、提交后撤权成功、之后请求 403。
5. **真实 HTTP 创建证据**：新增 `test/integration/native_incident_job_create_test.go`，用真实 `httpapi.InvestigationHandlers` POST `/api/v2/incidents/{id}/investigations` 断言持久化 provenance（native/用户/版本=1）、worker_queue 与审计行，相同幂等键重放返回同一 Job 且不新增行，会话撤销后新建与重放都 403 且无残留；另加 Repository 级机制用例（前置 scope 有效→会话撤销/凭据或会话版本轮换→`CreateJob` 拒绝）与历史 legacy 幂等回归。**证据范围诚实**：HTTP 创建只覆盖 API 层；Worker/Graph/Evidence 跨 RPC 联调仍属 MR-017 最终集中验收缺口。

### B. MR-003 原生高权限命令完整闭环

**服务端精确 intent（不信任客户端 target）**
- `internal/action/native_stepup.go`：`commandStepUpContext` 用**服务端**的 assessment 派生 `NativeCommandStepUpTarget(binding, assessmentId)`（实际命令摘要、profile/版本、policy 版本、目标 canonical/uid/namespace/revision、risk 版本、incident、executionRequestDigest 全部绑定），`AcknowledgeRisk` 与 `PrepareCommandExecution` 使用**同一**派生，客户端只能提供 `X-Ops-Step-Up-Challenge` 引用。
- `Authorize`/`Prepare` 的 step-up 触摸只接受 `operation=command_execution` 且绑定摘要逐字节匹配的证明；客户端自填 target/operation 一律不参与判定。

**Prepare 冻结可异步复核的 proof（迁移 00069）**
- `action.executions` 新增 `step_up_session_id / step_up_proof_id / step_up_operation / step_up_binding_digest / step_up_expires_at`，与 execution 同事务写入；只存证明引用、操作、绑定摘要和到期时间，**不存 Cookie/密码/TOTP/Bearer**；形状由 `IS TRUE` 约束限定（全空或全非空且摘要 32 字节）。
- `Execution` 的冻结字段为**非导出**字段，action JSON 合同不变（`contract` 校验与外部严格客户端不受影响）。

**异步 Dispatch/Claim 按持久 authority.Source 分派**
- `identity.native_command_proof_recheck`（前向迁移 69，仅授予 api/worker）：按 account→session→proof 顺序加共享锁，核对 proof 所属 user/session、`operation=command_execution`、绑定摘要逐字节一致、`verified_methods ⊇ {password,totp}`、未撤销、idle/absolute 有效期、账户与凭据/会话/授权版本与 proof 签发版本一致、账户 active、租户 active，并返回 proof 的三个签发版本。
- `BeginDispatch`/`ClaimWithKey`：native 高权限执行先用持久证明重证，再用重证出的版本在 Go 侧**重新派生**该命令的绑定摘要并与冻结摘要常量时间比较——他命令/他会话/他版本的证明无法冒充；通过后以事务内标记 `WithVerifiedNativeStepUp` 让同一决策断言 step-up，**没有**任何 `StepUpVerified=true` 的直接赋值。legacy 执行保留原 Keycloak SID/ACR 逻辑；namespace profile 不触发 step-up。
- 无证明或证明过期的历史 execution 不被“复活”派发：派发拒绝并落 `failed`＋`command.dispatch_denied` 审计。

**前端**
- `CommandComposer`：风险确认请求携带 `X-Ops-Step-Up-Challenge`（与服务端同一 challenge 引用），提交请求仅在确有挑战时携带该头（不再发送空值）；命令/目标/profile/policy/assessment 变化或授权事件时清除挑战与风险确认状态。
- 合同：`commands:risk-acknowledge` 与 `command-executions` 声明可选 `X-Ops-Step-Up-Challenge` header（`api/openapi/platform-v1.yaml`），`gen/api/openapi.gen.go` 同步；`web/src/api/generated` 无需变更（orval 结果逐字节一致）。

**用例**
- Go 单元：`internal/action/native_stepup_test.go`（intent 派生一致性、无 native 会话不绑定 intent、已重证证明的操作/摘要/期限校验、无证明/异操作/过期/legacy 一律拒绝）、`internal/auth/native_stepup_test.go`（标记与 intent 不可由请求伪造）。
- 前端：`web/tests/native_command_stepup.test.tsx`——确认请求带挑战头；无已验证挑战时确认按钮禁用（不会发出空引用）。
- 真实 PG/API（**已编译、未执行**）：`test/integration/native_command_stepup_chain_test.go`——真实账户、真实 TOTP 因子、真实 step-up challenge/proof、集群级 grant、k8s_cluster 高权限 profile：Assess→（无挑战拒绝）→MFA 挑战→Ack→Prepare→（校验冻结 proof 列）→无会话 Dispatch→Claim→状态 running；反例：账户停用/凭据轮换/会话版本轮换/会话撤销/证明撤销五种撤权后派发必须拒绝且状态 failed；命令字节改变、异 assessment 证明、重复 Ack/Prepare 拒绝。旧 execution（无证明）、namespace profile、legacy 对照保持原语义。

### 本轮本地实际运行（`evidence/r8/`）

| 命令 | 结果 |
| --- | --- |
| `go build ./...` / `go vet ./...` | 通过（build.log/vet.log） |
| `go test ./internal/... -count=1` | 仅既有 4 项 `internal/bundle` 失败（MR-006，go-unit-internal.log） |
| `go test -race ./internal/action ./internal/auth ./internal/investigation ./internal/incident ./internal/httpapi` | 通过（race.log） |
| `go test ./test/integration -run '^$'` | 编译通过（integration-compile.log） |
| `make check-generated` | 通过（check-generated.log） |
| `go test ./test/contract` | 仅既有 `TestDeliveryPreparationSourceBaseline`（MR-007，contract.log） |
| 迁移静态 `TestMigrationScriptsAreForwardOnlyAndContiguous` | 仍 17 个历史 Down（00047–00063）；00064–00069 无新增失败（forward-migration-static.log） |
| `web`: `npx tsc --noEmit` | 通过（web-tsc.log） |
| `web`: `npx vitest run` | 37 文件/155 用例通过；1 例失败为**既有环境问题**（`tests/v12/p1_shell.test.tsx` 读取工作树外的冻结原型路径，不存在）（web-vitest.log） |
| `web`: `node --test ./test/*.test.mjs` | 6/6 通过（web-contract.log） |

### 明确未完成（诚实记录）

- 本机未启动 PostgreSQL/Docker/K8s/模型/浏览器：MR-002/003/012 的真实 PG/HTTP 用例**只完成编译**，未执行；迁移 00069 前滚、RLS、锁序、证明重证与受控 Runner 协议均标记 remote_pending。
- MR-017 完整 Graph/Evidence/Analysis/Audit RPC 200 与来源 fixture 仍缺，保留最终集中验收，未以报告“业务链通过”提前关闭。
- MR-006 运行时闭包、MR-007 迁移/发行基线仍失败，原因保留，未降门禁。
- 其余 MR-001/004/005/010/011/013/015/016/018/019 未修；本节点不是总体完成，未提交/推送/合并/发布。

## r9（集中返修四项 + MR-001/004/011/013/018/019 完整链路）

### A. 集中返修四点（MR-003 会话有效性 / 真实 MFA HTTP / 68 约束反例）

1. **会话有效性条件补全**（`migrations/00069_action_command_step_up_proof.sql`）：`identity.native_command_proof_recheck` 现在与 `authorize_native_session_role`/`mfa_session_proof` 使用同一组会话与账户条件——会话必须仍在 `idle_expires_at` 与 `absolute_expires_at` 两个窗口内、未撤销、`mode='full'`、签发版本与证明一致；账户必须 active、密码状态 active、未要求改密、**且 `NOT mfa_reset_required`**（管理员重置第二因素后，旧的证明不再构成权限）。证明本身的未撤销、`verified_methods ⊇ {password,totp}` 与两个到期窗口继续校验。未扩大 Worker 的表权限：仍是一个只授予 api/worker 的窄函数。
2. **真实 MFA HTTP 流程**：原 `native_command_stepup_chain_test.go` 的夹具只调用 `VerifyPasswordStep`/`VerifyTOTPStep` 而没有铸造证明（真实 `MintStepUp` 只在 HTTP `VerifyChallenge` 中调用），且复用了同一 TOTP 时间步命中重放保护。该文件已删除，由 `test/integration/native_command_stepup_http_test.go` 取代：登录、因子注册、挑战创建与**密码+TOTP 验证**全部走真实 HTTP 处理器（`NativeMFAHandlers` + 原生认证中间件：Cookie、Origin/`Sec-Fetch-Site`、CSRF、`X-Ops-Step-Up-Challenge`），证明由真实 `MintStepUp` 铸造；不同验证使用不同操作者/不同密钥，因此不触碰 TOTP 重放保护，也不使用长 sleep。
3. **Dispatch 与 Claim 各自的失效反例**：新增“Prepare 后派发前失效”与“派发后 Claim 前会话失效”两条真实 PG 用例；撤权矩阵覆盖账户停用、凭据轮换、会话版本轮换、会话撤销、**会话 idle 到期**、**会话 absolute 到期**、grant 撤销、`mfa_reset_required`、证明撤销，共九种，并断言拒绝时 `state='failed'` 且 `action.attempts` 仍为 0。
4. **68 CHECK 数据库正反例**：新增 `test/integration/native_incident_provenance_constraint_test.go`，对真实 Job 行写入合法 native/legacy/全 NULL 历史形状，并用 SQLSTATE **23514** 断言部分 NULL 组合（native 缺版本、native 缺账户、未知 source、legacy 带账户、空 source 带账户）与非法 INSERT 均被数据库拒绝。

### B. 六项功能修复（完整链路）

1. **MR-001 当前资源问数预算**：`internal/analytics/cost.go` 先解析数据集与合法操作，再**按来源**判断窗口——`graph` 来源的当前资源聚合是即时查询（无窗口，预留 1 次），时间序列来源继续严格校验窗口与展开成本；不允许为满足预算虚构时间窗（编译器仍拒绝带窗口的 `resources.current`）。保留 VictoriaLogs 含末端 50 点、compare 2 次、网络半开桶、预算不足与来源失败语义。回归：`internal/analytics/cost_current_test.go` 与 `internal/investigation/mcp/current_resource_test.go` 用**同一条真实输入**跨 `analytics.Compile` → `toolReservation`，断言编译成功且预留恰好 1 次原始查询，并再次确认时序输入仍预留 50 次。
2. **MR-004 调查 SSE 原生 Cookie**：`web/src/api/sse-reader.ts` 删除 `streamInvestigationEventsWithResume` 的 Bearer-only 本地前置；认证只由共享 `requestOptions`（原生 same-origin Cookie / OIDC Bearer）与服务端决定，本地不再伪装“已授权”。游标、`Last-Event-ID`、eventId/seq 去重、断序失败、撤权中止与 401/403/410 处理保持不变。测试同步改写：`web/test/sse.test.mjs`（原生 Cookie 真实发出 fetch 并按序收事件、401 由服务端拒绝、410 仍失败关闭）与 `web/tests/investigation.test.tsx`、`web/tests/investigation_stream.test.tsx`（原生会话 `credentials=same-origin`、无 Authorization 头、事件按序到达）。
3. **MR-011 Assistant 范围切换 busy**：`web/src/pages/AssistantPage.tsx` 用 `busyEpoch` 给 busy 状态一个归属：范围切换时旧请求让出 busy 并复位，新请求开始后旧 `finally` 不得解除新请求的 loading。回归：`web/tests/v12/p1_assistant_scope.test.tsx` 覆盖旧请求 pending→切集群→旧请求 resolve/reject/仍 pending 三种，新范围按钮不再 `ant-btn-loading` 且可以再次创建。
4. **MR-013 故障链迟到响应归属**：`FaultChainCollector` 与 `FaultChainGapCheck` 自建 `AbortController`，卸载或会话变化即取消请求，取消后不再回调父页；`AssistantPage` 新增 `acceptFaultChainTurn`，在写入当前轮次前复核**实时** scope 与**实时**会话 id（用 ref 而非渲染值），三处 `onSubmitted` 全部改用它。回归：`web/tests/fault_chain_late_response.test.tsx` 覆盖 A pending→B→A 迟到（补查与收集各一条），B 的当前轮次不被污染且父页不收到回调。
5. **MR-018 指标判断 Step ID**：`internal/investigation/fault_chain_committed.go` 的 `execute_analysis_query` 分支中，内层 `for _, id := range data.QueryResultRefs` 遮蔽了外层已提交 Step id；改为 `queryResultID`，`AssessResourceQuery` 现在收到**外层 Step id**，`FactRef` 继续绑定 QueryResult id。回归：`internal/investigation/resource_query_assessment_test.go`（判断的 supports/conflicts 是 Step id、事实引用是 QueryResult id、健康观测成为同一 Step 的反证）与 `test/integration/native_fault_chain_source_step_test.go`（真实 Job 的已提交 Step 可被 `ReadResourceSourceStepTx` 读回，未知 id、他作业 id 与被篡改摘要均被拒绝）。
6. **MR-019 NodeReadyUnknown 恢复**：`internal/inspection/kubernetes/official.go` 在 Ready=True 时额外产生 `NodeReadyUnknown` 的 **resolved** 候选，与 NotReady/resolved 并列；Unknown occurrence 保持原 SignalKey 身份并由正常 resolved 路径结清，Unknown 仍不等于健康，不批量改库。回归：`internal/inspection/kubernetes/node_ready_test.go` 覆盖 Unknown→True、False→Unknown→True、重复健康与 UID 替换；来源中断与乱序样本由 `internal/finding/poll.go` 的既有防陈旧规则处理（未在健康时把未知当健康）。

### 本轮本地实际运行（`evidence/r9/`）

| 命令 | 结果 |
| --- | --- |
| `go build ./...` / `go vet ./...` | 通过（build.log/vet.log） |
| `go test ./internal/... -count=1` | 仅既有 4 项 `internal/bundle` 失败（MR-006，go-unit-internal.log） |
| `go test -race ./internal/{action,auth,investigation,incident,httpapi,analytics,inspection/kubernetes}` | 通过（race.log） |
| `go test ./test/integration -run '^$'` | 编译通过（integration-compile.log） |
| `make check-generated` | 通过（check-generated.log） |
| `go test ./test/contract` | 仅既有 `TestDeliveryPreparationSourceBaseline`（MR-007，contract.log） |
| 迁移静态 `TestMigrationScriptsAreForwardOnlyAndContiguous` | 仍 17 个历史 Down（00047–00063）；00064–00069 无新增失败（forward-migration-static.log） |
| `web`: `npx tsc --noEmit` | 通过（web-tsc.log） |
| `web`: `npx vitest run` | 38 文件/161 用例通过；1 例失败为既有环境问题（`tests/v12/p1_shell.test.tsx` 读取工作树外的冻结原型）（web-vitest.log） |
| `web`: `node --experimental-strip-types --test ./test/*.test.mjs` | 6/6 通过（web-contract.log） |
| `git diff --check` / 触及文件 `gofmt -l` | 干净（diff-check.log/gofmt.log；仅既有未格式化的 `native_action_chain_test.go`） |

### 明确未完成（诚实记录）

- 本机未启动 PostgreSQL/Docker/K8s/模型/浏览器：本轮新增的 PG 用例（原生 HTTP 命令闭环、九种撤权、会话过期、68 约束 23514、故障链来源读回）**只完成编译**，未执行；迁移 00069 前滚、RLS、锁序与受控 Runner 协议全部 remote_pending。
- **更正 r9 的验收覆盖误述**：MR-019 的 SP05 Candidate→Finding reducer→Incident settle 夹具在 r9 **尚未编写**（不是“只完成编译”）。r9 只有 signals 层单元（`internal/inspection/kubernetes/node_ready_test.go`）；该 PG 夹具列入后续集中 fixture 阶段。同样地，MR-018 的 PG 用例只覆盖来源步骤读回，**没有**覆盖 `EvaluateCommittedFaultChainTx` 的 evaluator→API 全链；evaluator 夹具列入后续集中阶段。
- MR-017 完整 Graph/Evidence/Analysis/Audit RPC 200 与来源 fixture 仍缺，保留最终集中验收，未视为已完成。
- MR-001 的“预算先扣留再查询”目前由跨层单元（MCP 预留 + 结算路径）覆盖，真实来源调用的先预留后执行仍待真实 PG/MCP 联调。
- MR-006 运行时闭包、MR-007 迁移/发行基线仍失败，原因保留，未降门禁。
- 其余 MR-005/010/015/016 与尚未开始的 MR 项未在本轮处理；本节点不是总体完成，未提交/推送/合并/发布。

## r10（集中返修：MR-013 请求归属 + MR-003/018 夹具确定性；MR-005/010/015/016 产品链）

### A. 集中返修

**A1 · MR-013 请求归属（复核第 1 项）**

- `web/src/features/assistant/FaultChainGapCheck.tsx` 与 `FaultChainCollector.tsx`：**在 await 之前捕获本次 controller**，await 之后只用捕获的 controller 判断归属（`!controller.signal.aborted && pending.current === controller`），不再读取可能已被替换的 `pending.current`；`useEffect` 在会话或页面 epoch 变化时创建新 controller 并 abort 旧的，同时复位 busy/submitted/error（状态属于挂载的作用域）。
- 新增**页面 epoch**：`FaultChainCheckControls` 与 `FaultChainCollector` 接受 `epoch`，`AssistantPage` 用 `submissionEpoch` ref 在会话/范围变化时推进；子组件在请求开始时捕获 epoch，成功/失败/finally 都要求 `epoch === epochRef.current`（组件内实时值），因此旧请求的成功、旧错误、旧 busy 都不会污染新状态。
- `AssistantPage.acceptFaultChainTurn` 追加**提交归属**校验：`submittedEpoch === submissionEpoch.current`＋实时 scope＋实时会话 id 三者同时成立才接纳该 turn。仅比较可能变回的会话 id 已被 epoch 取代（A→B→A 也能拒绝）。
- `Collector` 的资源查找请求（`resolveAssistantResource`）纳入同一归属与取消保护（此前它既不带 signal 也不校验归属）。
- 回归：`web/tests/fault_chain_late_response.test.tsx` 覆盖卸载迟到、**A→B→A 同会话再来**、**旧失败（scope 已推进）不显示错误**、收集器卸载迟到；`web/tests/v12/p1_assistant_scope.test.tsx` 新增“新请求 pending 期间旧 finally 不得清掉新请求 busy”。

**A2 · MR-003 HTTP 夹具的确定性错误（复核第 2 项）**

- `sp06Policy` 会写入夹具作者的 legacy 管理员 binding：夹具在发布策略后**清除该 binding**再断言租户内无 legacy 镜像。
- 登录路由：改用真实组合 `NewFoundationHandlerWithNativeMFA`（NativeAuthHandlers 承担 `/api/v1/auth/login`，NativeMFAHandlers 承担挑战与因子），不再手工注入已认证 context。
- 合法撤销与过期窗口：会话撤销同时写 `revoked_reason`（表约束要求成对）；会话过期改写 `issued_at/last_seen_at/idle_expires_at`（absolute>idle、issued<idle、last_seen∈[issued,idle] 全部成立）；grant 撤销补 `revoked_reason`；删除与约束冲突的“只改 absolute”用例。
- 真实 challenge/proof 生命周期：不再发明 `state='revoked'`（该状态与 `verified_at` 冲突），改为**未验证的挑战没有证明**与**已铸造证明被撤销**两种真实语义。
- `commandChallengeFor` 校验 verify 的**实际结果**（`verified=true` 且返回 `stepUpSession`），不再只看 HTTP 200。
- 补齐缺失证据：目标 revision 变化、profile 版本变化、策略版本变化、Prepare 同键重放（同一 execution、不新增行）与并发准备。

**A3 · MR-018 夹具确定性（复核第 3 项）**

- `test/integration/native_fault_chain_source_step_test.go` 的第二个 Job 原用 `seed.Revision+1` 而未推进 incident revision（CreateJob 会先 ErrConflict）：改为**另一主体的第二个 Job**（同一 Incident revision、不同 subject），保持真实合法；跨 Job 的步骤读回反例仍然成立。
- 该用例的真实覆盖范围（来源步骤读回：同作业可读、未知 id 拒绝、他作业 id 拒绝、篡改摘要拒绝）已在 fix-progress 中写明；**evaluator→API 全链夹具列入后续集中阶段**（见 A4 的更正）。

**A4 · 验收覆盖更正**

- fix-progress 与 2588b4d1 的报告已更正：MR-019 的 SP05 Candidate→Finding reducer→Incident settle 夹具在 r9 **尚未编写**（不是“只完成编译”）；MR-018 的 PG 用例**没有**覆盖 `EvaluateCommittedFaultChainTx`。两者列入后续集中 fixture 阶段，不以“已覆盖”表述。

### B. 四项后端功能链

**B1 · MR-005 observedRelations 的精确身份**

- `internal/resourcestore/observations.go`：候选 SQL 在 LIMIT/唯一性判定**之前**就限定 anchor 的精确集群与 API group——有 owner UID 时按**完整 canonical 身份**（cluster＋group＋kind＋UID）精确查询；只有名称时按 canonical 前缀（cluster＋group＋kind）+ kind/namespace/name 查询并 LIMIT 2。因此另一集群的同名对象、另一 API group 的同名对象不再消耗唯一性预算；删除重建（UID 变化）不会被串连；同集群真歧义仍然拒绝；`observed_at` 时间上界保留，历史缺口不会被当前状态回填为确定事实。
- 夹具：`test/integration/sp05_observed_relations_test.go`（**已编译、未执行**）——两集群同名 Node/ReplicaSet、UID 替换、晚于观测对象的节点，断言冻结关系只指向 anchor 集群的精确身份。

**B2 · MR-010 SP05 降级状态**

- 根因：`SetComponentDegraded` 使用固定位图（traces/metrics_listener/runtime/audit_signing），SP05 的 `sp05`、`sp05-analyzer/<source>`、`sp05-control-plane/<source>`、`sp05-metrics/<source>`、`sp05-pod-metrics/<source>` 全部不在其中，状态被静默丢弃。
- `internal/observability/metrics.go`：区分**业务健康**与**observability export 健康**——export 的固定组件与语义完全不变；新增显式登记的业务健康入口 `RegisterBusinessComponent`/`UnregisterBusinessComponent`/`SetBusinessDegraded`/`BusinessDegraded`，聚合语义为 any，**一个来源恢复不会清除其他来源的故障**；键空间有界（256）且必须是稳定来源标识（正则校验），运行期不能自造标签。
- `internal/app/sp05_worker.go` 等调用方：Worker 在启动时按配置来源登记业务健康键、退出时注销，并把四类采集失败写入对应键；`audit_signing`/`traces`/export 的原语义保持不变。
- 回归：`internal/observability/business_health_test.go`（登记、any 聚合、部分恢复、未登记键拒绝、键空间有界、export 语义不变）。

**B3 · MR-015 EnsureBootstrapTLS**

- 根因：`kubectl apply` 可覆盖同名 Secret；写请求已发出但结果不明时直接删除 CA 信任文件，导致可能已落地的 Secret 失去唯一信任锚；CA 存在而 Secret 缺失时永远 `TRUST_DRIFT`。
- `internal/integrations/openbao/bootstrap.go`：改用 `kubectl create`（同名 Secret 永不被覆盖）；创建失败后**读取远端确认本次身份**——远端存在且证书由该 CA 签发即视为已落地并保留唯一 CA 信任；远端存在但身份不同则报告漂移并保留信任锚（未知证书绝不被信任）；远端不可读则返回“结果不明”并**保留**唯一 CA 信任；远端确认确实未落地才删除信任锚以便干净重试。CA 存在而 Secret 缺失时替换为新的唯一 CA（不再永久漂移）。
- 回归：`internal/integrations/openbao/bootstrap_tls_test.go` 使用 **fake kubectl**（不是真实 K8s）覆盖：提交成功、提交成功但响应丢失、真正未提交、远端不明、同名异源、缺失 Secret 后的恢复、0600 权限。

**B4 · MR-016 证书续签与 CRL 独立**

- `internal/integrations/openbao/pki.go`：`rotationLoop` 在续签到期时，**续签失败仍尝试可达 CRL 刷新**——有效 leaf 保留，续签错误仍是本轮报告的失败，CRL 的可达性独立于签发；CRL 刷新不弱化 freshness/revoked/leaf expiry 规则，`refreshCRL` 只替换 CRL、保留当前信任，且与续签共用同一把 mutex（无死锁、无旧 CRL 覆盖新 trust）。轮询周期改为可注入字段（生产值不变），使**实际循环分支**可被测试。
- 回归：`internal/integrations/openbao/pki_rotation_test.go` 使用 loopback TLS OpenBao 与可控时钟覆盖：签发失败但 CRL 可达（CRL 仍刷新、leaf 保留）、登录失败同样成立、CRL 自身失败不破坏 leaf、恢复后重新签发、循环随 context 停止。

### 本轮本地实际运行（`evidence/r10/`）

| 命令 | 结果 |
| --- | --- |
| `go build ./...` / `go vet ./...` | 通过（build.log/vet.log） |
| `go test ./internal/... -count=1` | 仅既有 4 项 `internal/bundle` 失败（MR-006，go-unit-internal.log） |
| `go test -race ./internal/{observability,resourcestore,integrations/openbao,app,action,auth,investigation}` | 通过（race.log） |
| `go test ./test/integration -run '^$'` | 编译通过（integration-compile.log） |
| `make check-generated` | 通过（check-generated.log） |
| `go test ./test/contract` | 仅既有 `TestDeliveryPreparationSourceBaseline`（MR-007，contract.log） |
| 迁移静态 `TestMigrationScriptsAreForwardOnlyAndContiguous` | 仍 17 个历史 Down（00047–00063）；00064–00069 无新增失败（forward-migration-static.log） |
| `web`: `npx tsc --noEmit` | 通过（web-tsc.log） |
| `web`: `npx vitest run` | 38 文件/164 用例通过；1 例失败为既有环境问题（`tests/v12/p1_shell.test.tsx` 读取工作树外的冻结原型）（web-vitest.log） |
| `web`: `node --experimental-strip-types --test ./test/*.test.mjs` | 6/6 通过（web-contract.log） |
| `git diff --check` / 触及文件 `gofmt -l` | 干净（diff-check.log/gofmt.log；未格式化项均为本轮未触及的既有文件） |

### 明确未完成（诚实记录）

- 本机未启动 PostgreSQL/Docker/K8s/模型/浏览器：本轮新增的 PG 夹具（MR-005 observedRelations 精确身份、MR-003 绑定变化/重放/并发、MR-018 跨作业读回）**只完成编译**，未执行；迁移 00069 前滚、RLS、锁序与受控 Runner 协议仍为 remote_pending。
- MR-015 的 fake kubectl 只证明客户端的分支行为，**真实集群回执留远程最后验证**；MR-016 的 loopback OpenBao 只证明循环分支，真实 CRL 行为留远程。
- MR-001 持久预留→来源调用顺序、MR-019 SP05 Candidate→Finding→Incident settle、MR-018 evaluator→API 全链、MR-017 完整 RPC 200/来源 fixture 仍列入后续集中 fixture 阶段。
- MR-006 运行时闭包、MR-007 迁移/发行基线仍失败，原因保留，未降门禁。
- 其余 MR 未在本轮处理；本节点不是总体完成，未提交/推送/合并/发布。

## r11（集中返修四项 + MR-007/008/006 交付闭包）

### A. 集中返修（严格按最新复核四项）

**A1 · MR-010 注册生命周期（复核第 1 项）**

- 根因：`startSP05Worker` 登记键后 `defer` 注销，但该函数只启动 goroutine 随即返回 → 全部键当场被注销，之后所有 `SetBusinessDegraded` 又被当作未登记键丢弃。
- `internal/app/sp05_worker.go`：登记改为**随 Worker goroutine 生命周期**——`RegisterBusinessComponents` 在启动前一次性校验并登记全部配置键（身份与容量全部通过后才登记，失败则一个都不登记、不启动任何 goroutine），注销在该来源的 Worker goroutine 结束时执行（`defer` 在 goroutine 内，不再在启动函数内）。
- `internal/observability/metrics.go`：**重复登记不再清除已有故障**（已登记的键保留当前状态）；新增批量登记入口（先校验全部键再登记）；键空间上限由 256 提升到 4096，避免用固定小容量破坏合法配置规模。
- 回归（真实 startup，非孤立 helper）：`internal/app/sp05_health_lifecycle_test.go` 启动真实 SP05 Worker（真实信任文件、真实 registry 服务、不可达数据源）→ 采集失败 → 指标为 1 → 一个来源恢复仍为 1 → 全部恢复为 0 → 停止后注销 → 迟到上报被拒。

**A2 · MR-013 父页归属（复核第 2 项）**

- `web/src/pages/AssistantPage.tsx`：归属由页面**真实状态**派生——会话 id、范围、会话绑定 revision、当前轮次 id 合成 `ownershipKey`；epoch 在归属变化时推进（React 的 render 阶段状态调整模式，提交渲染前即完成），因此**渲染给子组件的 epoch 与父页实时验收的 epoch 恒等**，不再出现“effect 之后 ref 变回、与 props 不同”的窗口。
- 父页接纳 turn 需同时满足：提交的 epoch == 实时 epoch、实时 scope、实时会话 id；A→B→A 由单调 epoch 拒绝（同会话 id 变回也不能复活旧回调）。
- 回归（真实 AssistantPage，不用 Host 手工传 epoch）：`web/tests/v12/p1_assistant_faultchain_ownership.test.tsx` —— 当前状态的提交被接纳并触发重载；上一 scope 的提交被丢弃且不触发重载；同会话绑定 revision 变化后的旧提交被丢弃。

**A3 · MR-015 未知提交仍销毁 CA（复核第 3 项）**

- 根因：create 失败后一次空 GET 被当作“确定未写入”→ 删除 CA 信任锚；而服务端可能仍在处理已发出的 create，稍后落地即产生“有 Secret 无 CA”的死局。
- `internal/integrations/openbao/bootstrap.go`：提交前把**本次待提交身份**（叶证书与私钥，绑定 CA 摘要与 service/namespace/Secret 名，不含 CA 私钥）以 0600 写在受保护的外部位置（`<ca>.pending`，路径经 `validateExternalPath` 保证在仓库之外）；未知结果或暂时读不到 Secret 时**保留 CA 与待提交材料**，只重试**同一身份**的 create；收敛要求远端**证书与私钥都匹配**；已有 CA 但无可验证待提交材料时 **fail closed**（`OPENBAO_TLS_TRUST_UNRECOVERABLE`），绝不自动销毁用户的信任锚；远端同名异源或 key 不匹配一律拒绝且不覆盖。
- 回归：`internal/integrations/openbao/bootstrap_tls_test.go`（fake kubectl）——提交成功、结果不明保留 CA 与待提交材料、旧 create 稍后落地后按同一身份收敛且 CA 字节不变、同名异源拒绝、key 不匹配拒绝、无待提交材料的 CA fail closed、0600 权限、私钥不入报告与日志。

**A4 · MR-005 精确查询与实际存储夹具（复核第 4 项）**

- `internal/resourcestore/observations.go`：候选查询改为**精确 cluster_id 限制**（由 anchor 的 scope 解析出集群注册），canonical 前缀用**字面比较**（`left(canonical_id,length($3))=$3`，不再使用未转义 LIKE；scope 中的 `_`/`%` 是身份的一部分，不是通配符）；UID 分支仍按完整 canonical 精确匹配；`observed_at` 时间上界保留。
- `internal/resource/canonical_id.go`：新增 `CanonicalID.Prefix()`，与 `String()` 同一套编码，避免手拼第二套 canonical 编码。
- 夹具 `test/integration/sp05_observed_relations_test.go`（**已编译、未执行**）修掉三处确定性错误：cluster_id 按集群写入、owner 的 APIGroup 用 `apps`、关系从**实际存储** `platform.resource_observations.projection` 读取；覆盖 owner/Node/PVC/PV、同集群同名歧义、不同 API group、UID 替换、晚于观测对象与通配 scope 的正反例。

### B. MR-007 / MR-008 / MR-006 交付闭包

**B1 · MR-007 迁移准入与发行基线**

- 已应用 1～63 的 SQL 字节未改；历史 Down 段落改为**窄范围、有证据的兼容准入**：`test/integration/migrations_test.go` 记录 18 个历史文件（00038 与 00047～00063）的**精确 SHA-256** 与分类（`comments_only` / `raise_exception_guard` / `drop_column` / `drop_read_function`）；文件被编辑即摘要不符而失败，新文件带 Down 一律拒绝。分类证据仍在 `docs/reviews/main-2026-10-23/evidence/down-sections.json`。
- 迁移 CLI 只前进：新增 `TestMigrationCLIHasNoDownPath` 断言 `cmd/db-migrate` 无 `goose.Down/DownTo/Reset` 与任何 down/rollback 入口，只使用 `Up/UpTo`。
- `docs/release/delivery-baseline.json` 的 `migrations.latest/count` 由 57 更新为**实际的连续文件集 69**；`TestDeliveryPreparationSourceBaseline` 由此通过（`go test ./test/contract` 全绿）。
- 迁移源码闭包与两架构锁已按真实内容重建：`prepare-migration-source.py --check` 在 arm64/amd64 均通过；`bundle/evidence/third_party/admission/migration-tool-go{,-amd64}.lock.json` 与准入证据（`third_party/admission/` 与 `bundle/evidence/third_party/admission/` 同步）更新为真实摘要，范围文本如实写明“闭包含 00001-00069，而原生前滚与离线重建证据仍覆盖 00001-00043，本环境未重跑”。

**B2 · MR-008 生成物**

- `make check-generated` 通过，且**第二次运行无任何新变化**（dbgen/gen/web 生成物与工作树一致）；本轮未手写任何 generated 文件，敏感 dbgen 字段未进入 HTTP 输出（HTTP 合同仍由既有视图与契约测试约束）。

**B3 · MR-006 运行时源码闭包**

- 运行时源码闭包按真实内容重建并续期：`prepare-runtime-source.py --check` 在 arm64/amd64 均通过；`platform-runtime-go{,-amd64}.lock.json` 与 `bundle/component-catalog{,-amd64}.yaml` 的 `correspondingSourceBundleSHA256` 更新为真实摘要。此前因闭包漂移而失败的 4 项 `internal/bundle` 运行时用例（导入校验、镜像清单、OCI 归档、不完整 Bundle 不触发运行时）**现在全部通过**。
- 迁移 CLI 的**真实离线重建**已重跑（`GOOS=linux GOARCH=arm64|amd64 CGO_ENABLED=0 GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local`、空模块缓存、从续期后的源码归档构建）：两架构产物与已准入二进制**逐字节一致**（arm64 `sha256:5e9a23…78c79a`、amd64 `sha256:d8360d…35c8`），证据写入 `docs/evidence/migration-renew-20261025/offline-rebuild-{arm64,amd64}.json` 并由准入证据引用。
- **仍未闭合的具体项**：运行时源码归档携带的是上游运行时组件的 99 个模块闭包，而第一方平台构建需要 148 个模块（缺 49 个），因此**平台二进制**无法仅凭该归档离线重建；补齐需要把第一方模块闭包与其许可证复核纳入同一准入流程。这是具体的外部/范围条件，不是笼统阻塞；迁移 CLI 的离线重建已真实完成。

### 本轮本地实际运行（`evidence/r11/`）

| 命令 | 结果 |
| --- | --- |
| `go build ./...` / `go vet ./...` | 通过（build.log/vet.log） |
| `go test ./internal/... -count=1` | **全绿**（此前 4 项 bundle 运行时失败随闭包续期消除）（go-unit-internal.log） |
| `go test -race ./internal/{observability,resourcestore,integrations/openbao,app,action,auth,investigation,bundle}` | 通过（race.log） |
| `go test ./test/integration -run '^$'` | 编译通过（integration-compile.log） |
| `make check-generated`（连续两次） | 通过且第二次无新变化（check-generated.log / check-generated-second.log） |
| `go test ./test/contract` | **全绿**（MR-007 的 `TestDeliveryPreparationSourceBaseline` 随基线更新通过）（contract.log） |
| 迁移静态 `TestMigrationScriptsAreForwardOnlyAndContiguous` / `TestMigrationCLIHasNoDownPath` | 通过（此前 17 项历史 Down 失败以窄范围准入解决）（forward-migration-static.log） |
| `prepare-runtime-source.py --check`（arm64/amd64） | 通过（runtime-source-*.log） |
| `prepare-migration-source.py --check`（arm64/amd64） | 通过（migration-source-*.log） |
| `web`: `npx tsc --noEmit` | 通过（web-tsc.log） |
| `web`: `npx vitest run` | 39 文件/167 用例通过；1 例失败为既有环境问题（读取工作树外的冻结原型）（web-vitest.log） |
| `web`: `node --experimental-strip-types --test ./test/*.test.mjs` | 6/6 通过（web-contract.log） |
| `git diff --check` / `gofmt -l` | 干净（diff-check.log/gofmt.log；未格式化项均为本轮未触及的既有文件） |

### 明确未完成（诚实记录）

- 本机未启动 PostgreSQL/Docker/K8s/模型/浏览器：本轮新增的 PG 夹具（MR-005 精确身份的正反例）**只完成编译**，未执行；迁移 00069 前滚、RLS、锁序与受控 Runner 协议仍为 remote_pending。
- 平台二进制的离线重建仍缺第一方模块闭包（缺 49 个模块），需与许可证复核一并纳入准入流程；原生执行、完整 Bundle 离线安装与公网阻断保持 pending。
- MR-017 完整 Graph/Evidence/Analysis/Audit RPC 200、MR-018 evaluator→API、MR-019 SP05→Finding→Incident settle、MR-001 持久预留→来源调用顺序仍列入后续集中 fixture 阶段。
- 其余 MR 未在本轮处理；本节点不是总体完成，未提交/推送/合并/发布。


## r12（Codex 自行补齐 MR-006，完成后停止）

- 用户授权自行修复本轮离线重建缺口；本轮没有调用 CodeBuddy。完整说明见 `codex-r12-runtime-fix.md`。
- 独立复现确认：r11 的“缺49个编译模块”判断不准确；原99个模块覆盖运行时编译，构建还需要经 go.sum 认证的模块图元数据，以及完整的第一方编译/嵌入输入。原失败日志保留在 `evidence/r12/`。
- 新增 `scripts/rebuild-platform-runtime.py`，沿用已准入的架构源码归档，组合5486个第一方输入文件与325份已认证 go.mod 元数据；不修改 go.mod/go.sum、应用Go代码、迁移或许可证决策。
- Linux arm64/amd64 API、Worker、opsctl：共6个二进制均从空任务模块缓存、空编译缓存重建，Go网络获取关闭，与仓库相同参数的构建产物逐字节一致。两份实测JSON及产物路径见 `evidence/r12/`。
- Python边界回归2/2、bundle/opsctl两个Go包、交付合同、双架构runtime-source检查均通过；归档和6个二进制落盘后重新核对SHA-256通过。
- 仅关闭本轮MR-006离线重建缺口；其他待补业务fixture、原生执行与远程验收均未执行。按用户要求到此停止。

## Codex 独立复审（2026-10-25，r11增量）

完整结论与最小修改建议见 [independent-r11-review.md](independent-r11-review.md)，证据见 `evidence/independent-r11-review/`。结论未通过：MR-015 两项 OpenBao 问题通过独立探针复现；MR-005 夹具缺失管理员版本1初始化及新库 CREATE 授权，静态确认、未运行 PG。其他本轮已审增量和待远程边界已在总表区分；旧轮次自述作为历史保留，不取代本次结论。MR-010 startup 测试后续使用手动健康上报，不足以证明真实采集失败/恢复全链。

本轮未修改产品代码、未调用 CodeBuddy、未执行远程验收。按用户要求完成独立复审后停止，不自动返修。

## r13（Codex修复独立复审3项，并补齐指定端到端）

完整说明见 [codex-r13-three-fixes-e2e.md](codex-r13-three-fixes-e2e.md)，回执和最终/历史日志见 `evidence/r13/`。MR-015 pending路径及错误私钥两项、MR-005测试初始化已修复；MR-001预算先于来源、MR-017原生会话/持久任务及四类RPC、MR-018指标判断到API读回、MR-019 Unknown到Incident结清，均在mssc-direct的隔离环境实际执行通过。另完成真实空库和63→69前滚。

最终结果：远程集成8项、原生RPC 1项（3子项）、OpenBao全包25项均通过且无跳过；本地四包race、CLI及vet通过。实际PG/RLS/签名/mTLS/HTTP入口与受控来源/Lease/Transit/S3/Audit verifier的边界见报告，不冒充安装态全量验收。其他MR的原有待审核/待验收状态继续保留。

本轮未调用CodeBuddy、未提交/推送/合并/发布；完成本轮后停止。

## 主分支合入与r15部署（2026-10-25）

用户随后授权将全部修改合入主分支、推送GitHub，并明确更新r15(API18089)。代码提交 `16858c1` 已快进合入并推送main；前端全量174项测试、构建、internal/cmd/contract全量Go测试通过。r15已切换至主分支API/前端及隔离69版本数据库副本，原共用57版本库和其他预览保留。详见 [main-integration-and-r15-deployment.md](main-integration-and-r15-deployment.md) 及对应上线回执。这次授权取代此前“未提交/部署”的停止边界；原各轮停止记录保留为历史。
