# 问题台账（2026-10-16，基线 HEAD `4dd81de8`）— 修订版 r2

> 本文件已按两轮内部复核意见修订（见 [corrections.md](corrections.md)）：修正了 PWR-001／002 的风险与回滚措辞、PWR-005 的修复口径、PWR-032 的图例结论、PWR-010／014／017／036 的定性与范围，并把"待裁定"和"建议类"从已确认缺陷中分出。所有行号仍对齐 HEAD `4dd81de8e7a11b58daf6451af958ade6d6f7fd9f`。

## 0. 分类口径与计数

| 分类 | 含义 | 数量 | 编号 |
|---|---|---|---|
| **A 运行时行为缺陷** | 影响产品行为、数据、安全或可用性的缺陷；P1 三项、P2 八项 | 11 | 001、002、006；003、007、009、011、016、018、019、020 |
| **B 门禁／物料／部署缺陷** | 不影响运行时行为，但影响验收、发布、部署正确性；均为 P2 | 7 | 004、005、008、012、013、015、017 |
| **C 建议与低危项** | 防御增强、维护性、低危暴露、覆盖率改进；P3，不计入缺陷数 | 18 | 010、021～029、031、032、034～039 |
| **D 待裁定／需求歧义／设计取舍** | 需产品或有权限者裁定，不作缺陷统计 | 3 | 014、030、033 |
| **E 复核后否定** | 保留记录以避免重复调查 | 1 | `CreateJob` 重复 "queued" |

合计登记 **39 条 + 1 条否定**，其中**已确认缺陷 18 条**（A+B）。

优先级口径沿用：**P1** 主要功能、安全或数据可靠性严重受损；**P2** 明确影响用户或维护的缺陷；**P3** 影响较低的改进。定级按同一标尺校准：只阻塞门禁/验收、不影响运行时的不再计为 P1。

## 1. 摘要表

| 编号 | 分类 | 优先级 | 一句话 | 确认状态 |
|---|---|---|---|---|
| PWR-001 | A | P1 | 日志趋势/指标比较可把 1 次工具调用展开为 50/2 次来源请求，预算只预留 1 | 已复现（探针） |
| PWR-002 | A | P1 | 来源标签过滤导致的"缺失"被当作真实删除，永久注销身份并阻塞整类采集 | 已复现（探针） |
| PWR-006 | A | P1 | Worker 停机期间真实消失的资源永不注销（`previous` 为进程内 map） | 已复现（探针） |
| PWR-003 | A | P2 | v3 非 16K 模型 Profile 的 token 预留被 Go 准入硬编码 16384 拒绝 | 已复现（探针） |
| PWR-007 | A | P2 | Incident `started_at` 未受 `timeReliable` 保护，污染恢复时长统计 | 已确认（静态） |
| PWR-009 | A | P2 | Python worker 的 `gate.release()` 不在独立 `finally`，异常可永久泄漏并发槽位 | 已确认（静态） |
| PWR-011 | A | P2 | 助手能力接口失败被静默吞掉：无错误提示、创建按钮永久灰置 | 已确认（静态） |
| PWR-016 | A | P2 | 风险确认错误码与需求/OpenAPI 不一致（缺 `412 RISK_ACK_REQUIRED/EXPIRED`） | 已确认（静态） |
| PWR-018 | A | P2 | 身份冲突 Finding 以 `finding-envelope/v1` 入队，投递链只接受 `finding/v2` → 必然死信 | 已确认（静态） |
| PWR-019 | A | P2 | SP06/SP07 监听 goroutine 吞掉 `Serve` 错误，无日志/降级/退出 | 已确认（静态） |
| PWR-020 | A | P2 | 故障页 `rcaStatus` 过滤枚举错误：`conflicted` 永不匹配、缺真实值 `probable` | 已确认（静态） |
| PWR-004 | B | P2 | 迁移源码准入锁止于 `00037`，`--check` 在当前源码上实测失败 | 已复现（实测命令） |
| PWR-005 | B | P2 | 集成验收"零 skip"门禁被仓库内 helper 的 `t.Skip` 破坏 | 已复现（实测命令） |
| PWR-008 | B | P2 | api/worker 容器无任何健康探针，且进程无就绪端点、监听早于依赖初始化 | 已确认（静态） |
| PWR-012 | B | P2 | `ops-platform/values.schema.json` 只覆盖 `sp08`，Helm 校验形同虚设 | 已确认（静态） |
| PWR-013 | B | P2 | 生成的 `assistant-output.schema.json` 未纳入 `check-generated` | 已确认（静态） |
| PWR-015 | B | P2 | `artifacts/` 遗留可执行 `main` 包，含无护栏的破坏性清理 | 已确认（静态） |
| PWR-017 | B | P2 | web live e2e `session` fixture 在缺凭据时抛错，且未接入 make 目标 | 已确认（静态） |
| PWR-010 | C | P3 | 运行时未使用 OpenAPI 生成的绑定；缺"运行时路由 ↔ spec"的自动化校验 | 已确认（静态） |
| PWR-021 | C | P3 | 来源探针错误消息回显 `authRef`（内部 Secret 路径） | 已确认（静态） |
| PWR-022 | C | P3 | 审计"禁列"兜底漏 camelCase 密钥键名 | 已确认（静态） |
| PWR-023 | C | P3 | 只读投影的 label 过滤仅按键名子串 | 已确认（静态） |
| PWR-024 | C | P3 | 管理写操作的幂等重放不复验 step-up | 已确认（静态） |
| PWR-025 | C | P3 | SP05 列表游标未签名（其它 4 处游标均已签名） | 已确认（静态） |
| PWR-026 | C | P3 | SSH/硬件类命令 post-check 永远只能返回 `inconclusive` | 已确认（静态） |
| PWR-027 | C | P3 | Redfish 凭证启动时一次性读取，轮换需重启 Worker | 已确认（静态） |
| PWR-028 | C | P3 | SP06/SP07 worker 忽略清理错误；部分日志走包级 `slog` 绕过脱敏 | 已确认（静态） |
| PWR-029 | C | P3 | CLI `profile resolve` 允许 `virtualization/full`，运行时拒绝（延期边界不对称） | 已确认（静态） |
| PWR-031 | C | P3 | 测试与门禁的表述/覆盖改进组（5 条） | 已确认（静态） |
| PWR-032 | C | P3 | 前端 UI 缺陷与杂项组（7 条） | 已确认（静态） |
| PWR-034 | C | P3 | `assistant.NewContext` 未校验时间窗上界/纳秒（下游已兜底） | 已确认（静态） |
| PWR-035 | C | P3 | 死代码与重复实现组（9 条） | 已确认（静态） |
| PWR-036 | C | P3 | Worker 强依赖其未读取的 `OIDC_ISSUER_URL` | 已确认（静态） |
| PWR-037 | C | P3 | 契约严格性不均；metrics-server 以镜像摘要三元表达式推断架构 | 已确认（静态） |
| PWR-038 | C | P3 | Python 侧：预算初值偏乐观、任务分类依赖文本正则、摘要过度声明、未用导入 | 已确认（静态） |
| PWR-039 | C | P3 | 覆盖缺口：`internal/investigation/mcp` 无测试；前端图谱用例 mock canvas | 已确认（静态） |
| PWR-014 | D | — | 迁移仅前向（已有 runbook 说明）、无 PDB：属取舍，待评估回滚兼容性 | 待裁定 |
| PWR-030 | D | — | `platform.tenants` 无 RLS 且角色有 SELECT：是否为平台管理员有意设计 | 待裁定 |
| PWR-033 | D | — | 公开 steps 读取是否计入 Job 输出限额 | 需求歧义 |

## 2. 已确认缺陷（A：运行时行为）

### PWR-001 / P1：日志趋势与指标/日志比较可以突破来源原始查询预算

- **类别**：预算、资源消耗、运行机制｜**置信度**：高｜**状态**：已复现（探针，当前 HEAD）
- **位置**：`internal/investigation/mcp/server.go:127-141`；`internal/analytics/log_trend.go:38-52`；`internal/analytics/comparison.go:27,32`；`internal/analytics/network.go:136-144`；结算沿用低估值：`internal/investigation/repository.go:505-597`
- **证据**：`server.go:127-141` 只在 `strings.HasPrefix(args.Intent.DatasetID, "network.")` 时按 `NetworkRawQueryCost` 预留，其余 `RawQuery` 路径一律 `reserve.RawQueries = 1`；`log_trend.go` 在窗口内逐点执行 `v.Execute`；`comparison.go` 执行 baseline/current 两次。探针 `TestReviewVictoriaExpandedRawQueries` 本轮重跑通过：`logs.records/trend` 展开 **50** 次来源 HTTP 请求、`metrics.pod-memory/compare` 展开 **2** 次，而账本只预留/结算 1 次。日志见 [evidence/review-probes-rerun.txt](evidence/review-probes-rerun.txt)。
- **触发**：已授权调查调用 `execute_analysis_query`（`dataset="logs.records"`、`operation="trend"`、60s 步长、约 50 分钟窗口）或任一 `compare/compare_percent`；Job 仅剩 1 次 rawQuery 预算时仍会发出 50 次来源请求。
- **期望依据**：09 §51.3 要求先原子预留"调用数、结果字节、原始查询数"，再发远程请求。
- **影响**：账本显示在限额内，实际来源负载可达声明上限的多倍；共享来源（VictoriaLogs/Metrics）承受非预期压力，预算失去约束力。
- **根因**：成本计算只对 network 前缀特化，Victoria 展开路径未接入统一成本函数；结算 `actual = *used` 直接沿用低估的预留值。
- **未验证**：未搭建数据库/MCP 服务做"预算耗尽后拒绝并结算一致"的端到端复现（探针为进程内真实 `Compile/Execute` + 内存 Transport，不连外部服务）。
- **最小修复方向**：把 `NetworkRawQueryCost` 泛化为统一的"来源请求数"成本函数（Victoria + Network 共用：比较=2、日志趋势=步数+1、`query_range`=1；无法静态确定的路径一律拒绝而非猜测），在 `server.go` 预留处按同一结果原子预留；**不提高总预算**，也不在调用完成后补记。

### PWR-002 / P1：来源 `RequiredLabels` 过滤导致的"缺失"被当作真实删除，永久注销身份并阻塞采集

- **类别**：资源生命周期、来源隔离、可用性｜**置信度**：高｜**状态**：已复现（探针，当前 HEAD）
- **位置**：`internal/resourcestore/snapshot_scope.go:45-59` → `internal/app/sp04_collector.go:48,54` → `internal/resourcestore/repository.go:225-235` → `repository.go:180` → `repository.go:140-157`
- **证据**：`SyncSnapshot` 用**过滤后**的 `current` 与进程内 `previous` 求差，缺失即 `Retire` 写不可复活墓碑；同一 UID 标签恢复后 `Resolve` 的 upsert 命中 `WHERE resource_entities.deleted_at IS NULL` 失败，`RowsAffected()==0` 且 `deleted != nil` 时返回 `ErrIdentity`，`SyncSnapshot` 随之失败、`collectorSink` 置 `LastError` 且**不发布图**（`sp04_collector.go:55-58`），该 GVR 长期 `GRAPH_NOT_READY`。探针 `TestReviewFilteredAbsenceBecomesTerminalRetirement` 本轮重跑通过。
- **触发**：来源配置了 `RequiredLabels`（普通授权操作）→ 某 Pod 标签被临时修改（UID 未变、Pod 未删除）。
- **期望依据**：05 §24.1（来源数据范围与当前有效性）、08 §49.1（图重建清除"不再存在"的实体）；来源可见性变化与原生 UID 删除必须区分。
- **影响**：**把"来源不可见"误判为"删除"会产生持久身份故障**——一次正常标签维护即可让该 GVR 的图谱持续不可用，且重启不能自愈（墓碑在数据库中）。
- **根因**：过滤发生在身份同步之前，且"来源不可见"与"原生删除"被折叠进单一 `deleted_at`。
- **最小修复方向**：以**过滤前**、已完成同步的原生 UID 集合判定真实消失；过滤后集合只用于当前可见性与发布；如需持久化不可见状态，须支持同一 UID 重入来源范围（建议拆 `visible/deleted` 两态），不能简单放开所有 `deleted_at` 复活。

### PWR-006 / P1：Worker 停机期间真实消失的资源不会被注销

- **类别**：重启恢复、持久身份一致性｜**置信度**：高｜**状态**：已复现（探针，当前 HEAD）
- **位置**：`internal/app/sp04_collector.go:20`；`internal/resourcestore/repository.go:225-235`；`internal/resource/resolver.go:108`
- **证据**：注销判决只依据易失的进程内 map，首轮完整快照不会与持久集合求差；全仓未发现启动时按（tenant, source, cluster, GVR）对持久身份做缺失清理的补偿路径。探针 `TestReviewFirstEmptySnapshotDoesNotReconcilePersistentInventory` 本轮重跑通过（零次持久化读写）。若旧 Node 的 providerID 被新 UID 复用，旧别名成为候选后触发 `ErrIdentity`，与 PWR-002 表现叠加。
- **触发**：该 GVR 的全部采集进程停止期间，Node 被删除并以新 UID 重建（沿用 providerID）。
- **期望依据**：08 §49（数据库保留实体/别名、图按当前原生快照重建，重启后应重新一致）。
- **影响**：实时图与数据库"仍存在"判定长期漂移；Node 重建可能持续触发身份冲突。
- **最小修复方向**：在**完整原生 List 成功且来源有效**的边界，用持久 UID 集合与快照求差；不能在部分快照、来源失败、过滤后缺失或 Standby 旧快照上全局注销。与 PWR-002 同轮设计但需**分别验证**两类场景。

### PWR-003 / P2：v3 非 16K 模型 Profile 的 token 预留被 Go 准入硬编码 16384 拒绝

- **类别**：跨语言契约、功能正确性｜**置信度**：高｜**状态**：已复现（探针，当前 HEAD）
- **位置**：`internal/investigation/allocation.go:93`；Python 侧 `services/investigator/src/investigator/budget.py:58-61`、`model_profile.py:67-69`；Profile 校验 `internal/modelprofile/profile.go:45-49`（允许 1024–65536）
- **证据**：
  ```
  if reserve.ModelRequests != 1 || ... || reserve.InputTokens != 16384 || ... { return out, ErrBudget }
  ```
  Python 按当前准入 Profile 的 `contextWindow` 预留（如 32768），Go 在读取 Job 前即返回 `ErrBudget` → HTTP 429 → `RuntimeError("JOB_API_REJECTED_429")` → worker 归为 `INVESTIGATION_FAILED`。探针 `TestReviewAccepted32KProfileRejectedBeforeModelAdmission` 本轮重跑通过。
- **触发**：任一 v3 助手任务的 `assistantModelProfile.contextWindow != 16384`（8K/32K/64K 等合法配置）。
- **期望依据**：09 §51.3（token 上界由受控请求构造 + 锁定 Profile 证据共同验证）；ADR-0038（v2 16K 与 v3 Profile 分离）。**按 Profile 的 `contextWindow` 预留本身符合设计**（Python 侧期望 8K/32K 可用是合理实现），缺陷在 Go 侧以常量代替 Profile 解析。
- **影响**：Profile 校验、能力展示与实际调用不一致；任何非 16K 的模型变更会表现为预算耗尽/调查失败，而非配置错误。当前部署配 16384（`runbooks/sp06-investigation-runtime.md`），故默认配置不受影响。
- **根因**：Job 内只有 Profile 引用，准入未按引用解析 token 上界；两侧各自维护"输入上界"常量。
- **未验证**：`holmesgpt==0.42.0` 源码不在仓库，`max_context_size` 属性是否真实暴露未能在本地证实；未做跨语言联调。
- **最小修复方向**：服务端以已冻结且可信的 Profile 解析 `contextWindow/outputTokenLimit` 作为预留上界，再叠加 Job 剩余预算校验；保留 v2 的 16384 规则，不接受客户端任意上界，也不只删除比较条件。

### PWR-007 / P2：Incident `started_at` 未受 `timeReliable` 保护，污染恢复时长统计

- **类别**：统计语义、业务正确性｜**置信度**：高｜**状态**：已确认（静态）
- **位置**：`internal/incident/correlator.go:109-115`（新建路径未检查 `f.TimeReliable`；关联/重开 `:98,:102` 已检查）；`split.go:79`；下游 `internal/analytics/controlplane.go:159`、`statistics.go:9`；合同 `api/analytics/catalog-v1.json` 的 `incidents.recovery-duration`
- **证据**：`finding.Envelope` 允许 `timeReliable=false`（`internal/finding/domain.go:40`）；统计 SQL 只要求 `started_at IS NOT NULL AND resolved_at >= started_at`，missing 只在时间为空时计数 → 顺序合法即可输出 `Exact=true` 的平均恢复时长。
- **触发**：来源发布 `timeReliable=false` 的 firing Finding → 新建 Incident → 后续 resolved → 进入精确统计。
- **期望依据**：合同要求"`started_at` 与 `resolved_at` 可靠且顺序有效"；04 §33.3"时钟不可信时不据此重开"；PRD FR-044/ACC-005～006。
- **最小修复方向**：Incident 持久化 `started_at` 的可靠性/证据，统计时排除未验证样本并计入缺口；旧记录无证据保持未知。

### PWR-009 / P2：Python worker 的 `gate.release()` 不在独立 `finally`

- **类别**：错误处理、资源释放、可用性｜**置信度**：高｜**状态**：已确认（静态）
- **位置**：`services/investigator/src/investigator/worker.py:95-98`（闸门 `BoundedSemaphore(10)`，`worker.py:31`）
- **证据**：`job_api.close()` / `bridge.close()` 抛异常时，后续语句（含 `gate.release()`）被跳过。
- **影响**：累积 10 次后 `gate.acquire(blocking=False)` 永远失败，全部请求返回 `429 INVESTIGATOR_BUSY`，直到进程重启。
- **最小修复方向**：`gate.release()` 放入更内层 `try/finally`（或 `ExitStack`）；close 失败只记录静态类别，不影响槽位回收；补 close 抛异常的回归用例。

### PWR-011 / P2：助手能力接口失败被静默吞掉

- **类别**：前端错误处理、用户可见后果｜**置信度**：高｜**状态**：已确认（静态）
- **位置**：`web/src/pages/AssistantPage.tsx:129-146`（无 `enabled`、无错误消费）；`:317-323`、`:643-648`（仅在拿到 `data` 时渲染）；`:629-638`（按钮 `disabled`）
- **触发**：`GET /api/v1/assistant/capabilities` 返回 4xx/5xx/网络失败 → 两个 Alert 均不出现，创建按钮永久灰置且无原因；`:665-674` 还会把"取不到"显示为"没有准入证明"。
- **最小修复方向**：接入既有 `QueryState`（loading/error/retry），区分"接口失败"与"功能未启用"；禁用态给可聚焦原因文本。

### PWR-016 / P2：风险确认错误码与需求/OpenAPI 不一致

- **类别**：契约一致性、错误码｜**置信度**：高｜**状态**：已确认（静态）
- **位置**：`internal/httpapi/action_handlers.go:41-42`；领域层单一错误返回点 `internal/action/service.go:212,234,256,260,277,301`
- **证据**：`case errors.Is(err, action.ErrAcknowledgement): status, code = 409, "RISK_ACK_COMMAND_MISMATCH"`，无法区分缺失/过期/摘要不符。
- **期望依据**：07 要求缺确认 → `412 RISK_ACK_REQUIRED`、期限失效 → `412 RISK_ACK_EXPIRED`、范围不符 → `409 RISK_ACK_SCOPE_MISMATCH`；`api/openapi/platform-v1.yaml:810-813` 枚举含 `RISK_ACK_REQUIRED`。
- **影响**：客户端只能一律按"命令不匹配"处理；契约枚举中的 `RISK_ACK_REQUIRED` 永不产生（`STEP_UP_EXPIRED` 同样从未产生）。
- **最小修复方向**：拆分领域错误类型并映射到 412/409 与对应枚举值。

### PWR-018 / P2：身份冲突 Finding 以 `finding-envelope/v1` 入队，投递链只接受 `finding/v2`

- **类别**：跨模块合同不一致、死信｜**置信度**：中高｜**状态**：已确认（静态）
- **位置**：`internal/resourcestore/identity_finding.go:25,30,33,37`；门槛 `internal/finding/outbox.go:24,42-50`；消费方 `internal/incident/correlator.go:25`
- **证据**：payload 为 `schemaVersion: "finding-envelope/v1"` 且 **不含 `findingId`**；`RelayPass` 对非 `finding/v2` 直接返回 `errHistoricalSchema` → 死信。
- **影响**：身份冲突永远进不了统一关联/展示链，只留下死信噪声；同时 `finding.records` 中已有一条 v1 记录。
- **最小修复方向**：改用 v2 信封（含 `findingId`、`timeReliable`）并复用 SP05 candidate 路径；或明确仅生产者侧可见并**不写 outbox**。

### PWR-019 / P2：SP06/SP07 监听 goroutine 吞掉 `Serve` 错误

- **类别**：错误处理、可观测性｜**置信度**：高｜**状态**：已确认（静态）
- **位置**：`internal/app/sp06_runtime.go:345`、`internal/app/sp07_runtime.go:149`；对比 `internal/app/sp04_runtime.go:693-699`（有记录与 `cancel()`）
- **影响**：`/mcp`、investigations、（SP07）内部 runner 端点因 accept/TLS 失败不可用时，进程仍"正常运行"，无日志、无指标、无降级。
- **最小修复方向**：errgroup/错误通道捕获，记录日志并 `SetComponentDegraded`，必要时触发关闭。

### PWR-020 / P2：故障页 `rcaStatus` 过滤枚举错误

- **类别**：前后端契约一致性、交互｜**置信度**：高｜**状态**：已确认（静态）
- **位置**：`web/src/pages/FindingsPage.tsx:88-103`（`["confirmed","unresolved","conflicted"]`）；`internal/httpapi/sp05_list.go:52-53`；真实枚举 `web/src/api/generated/models/rCAEvaluationV2Status.ts:11-15` = `confirmed | probable | unresolved`
- **影响**：选"证据冲突(conflicted)"永远返回空列表；真实状态 `probable` 无法筛选。
- **最小修复方向**：改为 `["confirmed","probable","unresolved"]` 并补 `probable` 文案。

## 3. 门禁、物料与部署缺陷（B）

### PWR-004 / P2：迁移源码准入锁过期，`--check` 实测失败

- **类别**：供应链准入、构建门禁｜**置信度**：高｜**状态**：已复现（实测命令）
- **位置**：`scripts/prepare-migration-source.py:56-65`；`Makefile:44-46,89`；锁文件 `bundle/evidence/third_party/admission/migration-tool-go.lock.json:7469`、`-amd64.lock.json:7574`
- **证据**：两个锁文件 `firstPartyFiles` 末项均为 `migrations/00037_sp07_worker_authority.sql`，磁盘含 `00038`～`00043`；实测 `ValueError: migration source/notice closure has drifted`（[evidence/migration-source-check.txt](evidence/migration-source-check.txt)）。
- **影响**：`make check` / `check-runtime-source` 失败；历史合格迁移包无法证明新表结构可由该包安装。**不影响运行时行为**，故为 P2（若组织把发行门禁视为最高级，可单列为"门禁级 P1"，但不应与运行时缺陷混计）。
- **最小修复方向**：按现有准入流程重新准备并重签（lock/source JSON 与 `sourceBundleSHA256`，补前向迁移证据）；不得只复制新摘要、关闭检查或借用旧二进制通过。

### PWR-005 / P2：集成验收"零 skip"门禁被仓库内 helper 的 `t.Skip` 破坏

- **类别**：测试门禁真实性｜**置信度**：高｜**状态**：已复现（实测命令）
- **位置**：`test/integration/assistant_process_recovery_test.go:36-40`；门禁 `Makefile:69-71` → `scripts/check-test-report.py:11-17`
- **证据**：helper 在未设置 `OPS_ASSISTANT_CRASH_CHILD_CONFIG` 时 `t.Skip`；`check-test-report.py` 只要出现 `Action=="skip"` 即 `exit 1`。实测：
  ```
  $ go test ./test/integration -run '^TestAssistantCrashWorkerChild$' -count=1 -json | grep skip
  {"Action":"skip","Test":"TestAssistantCrashWorkerChild",...}
  $ python3 scripts/check-test-report.py <report>   → exit 1
  ```
  日志见 [evidence/skip-gate.txt](evidence/skip-gate.txt)。
- **最小修复方向（已实测可行）**：在 `Makefile:71` 的验收调用上加精确 `-skip '^TestAssistantCrashWorkerChild$'`，helper 即被整体排除、**不产生 skip 事件也不产生 run 事件**（实测 0/0）；父测试仍显式启动子进程（`assistant_process_recovery_test.go:133-138` 自行传入 `-test.run ^TestAssistantCrashWorkerChild$` 并注入环境变量，顶层 `-skip` **不会**传播给它），因此子进程路径不受影响。`Makefile:67` 的 `test-e2e` 已有同样先例。
- **不推荐**：把 helper 改成裸 `return`——它会让外层套件的通过数包含一个空执行的辅助入口（父亲测试的实质验证在父测试内部完成）；也不要求改造成独立子命令（可行但改动更大）。

### PWR-008 / P2：api/worker 容器无健康探针，进程无就绪端点

- **类别**：部署可用性、就绪语义｜**置信度**：高（Chart/启动顺序静态确认）；K8s 侧影响待运行验证
- **位置与范围（已收窄）**：`deploy/charts/ops-platform/templates/deployments.yaml` 渲染的 **api/worker** 容器无任何探针；该模板仅在其 **`web` 渲染路径**（即 `sp08.enabled=false` 时，见 `deployments.yaml:9`）才会渲染 web，该路径同样无探针；**`sp08.enabled=true` 时 web 由 `sp08.yaml` 渲染并已有 readiness/liveness**（`sp08.yaml:50-51`）。另有 `cmd/platform-api/main.go:41-48`（先 `net.Listen`）与 `internal/app/api_runtime.go:87-148`（DB→OIDC→trust→SP04/06/07→handler 后才 `Serve`）；全仓 `internal/` 无 `healthz/readyz/livez`。
- **影响**：api/worker 在初始化完成前可能被计入 Ready（滚动升级/配置变更期间请求超时）；僵死容器不会被重启；`helm upgrade` 的就绪判断无意义。
- **最小修复方向**：为 api/worker 增加不携带业务数据的就绪/存活端点（复用 `Metrics` 降级位图与依赖探测），慢初始化配 `startupProbe`；不要用提前绑定的 TCP 端口或 metrics 端点代替业务就绪。

### PWR-012 / P2：`ops-platform/values.schema.json` 覆盖不足

- **位置**：`deploy/charts/ops-platform/values.schema.json:1-88`（仅声明 `properties.sp08`，无 `additionalProperties:false`、无 `required`）；模板消费 `workloadsEnabled/global/networkPolicy/observability/workloadIdentity/components.*/runtime.*/sp04~sp08.*`
- **影响**：除 `sp08` 外的键完全不被校验，拼写错误静默通过。
- **最小修复方向**：补全 schema 并加顶层 `additionalProperties:false`。

### PWR-013 / P2：生成的 `assistant-output.schema.json` 未纳入 `check-generated`

- **位置**：`api/generate.go:7` → `scripts/generate-investigation-provider-schema.py:20-23` 写入该文件；`Makefile:39` 的跟踪集不含它
- **影响**：该生成物可与生成源漂移而不被任何门禁发现。
- **最小修复方向**：加入 `check-generated.py` 参数列表。

### PWR-015 / P2：`artifacts/` 遗留可执行 `main` 包，含无护栏的破坏性清理

- **位置**：`artifacts/task27-cleanup.go:13`（`package main`）与 `:23`（`bundle.CleanupRelease(context.Background(), p, r, drivers.Run)`，无 `--apply` 规划/确认）；`artifacts/pre-sp07/*/main.go` 同类；这些文件未被 `.gitignore` 忽略，会进入 `Makefile:49,53` 的 `go vet ./...` 与 `go test ./...`
- **对比**：`cmd/opsctl/current_cleanup.go:23,78` 默认只做只读规划
- **影响**：误 `go run artifacts/task27-cleanup.go` 会删除发布；构建/vet 覆盖非产品代码。
- **最小修复方向**：移出模块或加 `//go:build ignore`，复用 `current_cleanup.go` 的安全规划路径。

### PWR-017 / P2：web live e2e 的 fixture 在缺凭据时抛错，且未接入 make 目标

- **位置**：`web/tests/e2e/session.ts:22-27`（缺 `OPS_WEB_QA_INPUT` 抛 `Error`）、`:69-73`（缺 `OPS_WEB_QA_CDP` 抛 `Error`）；`web/package.json:16` 提供 `"test:e2e": "playwright test"`
- **说明**：live 用例在缺凭据/浏览器环境时失败是可辩护的验收入口设计，`pnpm test:e2e` 已是明确入口；本条保留的事实是"**仓库内没有 make 目标包装它**"与"**fixture 缺失时表现为 error 而非显式跳过**"，因此它影响的是仓库内自证能力与失败可读性，不是"没有可复现入口"。
- **最小修复方向**：live spec 顶层 `test.skip(!process.env.OPS_WEB_QA_INPUT, ...)` 以区分"未配置"与"失败"；如需与 Go 侧对齐，再补 `make` 目标。

## 4. 建议与低危项（C，P3）

| 编号 | 内容与证据 | 建议 |
|---|---|---|
| PWR-010 | 生产入口 `internal/app/api_runtime.go:125` → `internal/httpapi/foundation.go:80-114` 使用手写前缀分发；`api/oapi-codegen.yaml` 配置 `strict-server: true`，生成的 `ServerInterface`（`gen/api/openapi.gen.go:7356`）与 `StrictServerInterface`（`:16698`）并存，`HandlerFromMux`（`:12669`）只接受非 strict 接口，需 `NewStrictHandler`（`:16984`）才启用 strict 校验；ADR-0007 只要求 OpenAPI 为合同权威，未要求实现 strict 接口 | 保留为**合同偏差 + 验证缺口**：运行时与 spec 的路径/方法/字段/错误体缺自动化校验，新增 operation 遗忘登记会静默 404。可选低成本改法是新增"路由 ↔ spec"契约测试；可选彻底改法才涉及挂载生成的服务器（需明确区分 `ServerInterface` 与 `StrictServerInterface`，不主张必须重构） |
| PWR-021 | `internal/integrations/probe.go:59,63` 把 `authRef`（`openbao://host/path` 内部引用）写进错误串 | 只保留状态码或摘要前缀；需先确认上游是否把它写入长期日志 |
| PWR-022 | `internal/audit/append.go:107-137`（读路径同逻辑 `segments.go:595`）键名后缀匹配漏 `secretKey/apiKey/accessKey/privateKey/passwd`，也不检查值 | 改敏感子串匹配或允许列表；当前调用方未写入此类键，属防御纵深 |
| PWR-023 | `internal/integrations/kubernetes/projector.go:22-27`、`projection_nested.go:19-32` 仅按键名子串过滤 label | 扩展拒绝集合；整体白名单路径表已排除 Secret payload/env/command/annotations |
| PWR-024 | `internal/httpapi/idempotency.go:105-108` 的 `DecisionReplay` 直接回放、不进入内层 step-up 中间件（对比 `tenant_handlers.go:46-57` 的 `AuthorizeTx` 写法） | 统一采用 step-up-in-`AuthorizeTx`；角色吊销仍由每请求 OIDC 绑定捕获，可利用面窄 |
| PWR-025 | `internal/httpapi/sp05_handlers.go:329-335` 游标为 `base64(json)` 未签名（其它 4 处已签名） | 纳入统一签名游标；SQL 恒带 tenant/cluster/namespace 过滤，无跨租户读取 |
| PWR-026 | `internal/app/sp07_postcheck.go:123-131` 只为 PVC/Node 设 recipe，`default: return f` 使 `Complete` 保持 false；`internal/action/postcheck.go:29-37` 恒返回 `inconclusive` | 为硬件目标补 recipe 或显式声明 post-check 不适用 |
| PWR-027 | `internal/app/sp04_runtime.go:540` 一次性读取 Redfish 凭证（对比 K8s token `:255-264` 每请求读取） | 改为按需读取，支持凭证轮换 |
| PWR-028 | `internal/app/sp06_runtime.go:453` 忽略 `ExpirePass` 错误、`:431-451` 仅 `slog.Error`；`sp06_runtime.go:432,440,449,459`、`sp07_worker.go:53,56,59`、`assistant_analysis.go:132,138,150` 走包级 `slog`，绕过 `observability/logging.go:40-46` 的脱敏 | 注入 `runtime.Logger` 或统一设置默认 logger |
| PWR-029 | `internal/profile/resolve.go:36-44,70-77` 允许 `virtualization/full`，`internal/app/profile.go:18` 只接纳 `core\|deepflow` | 按 ADR-0008 在 resolve 阶段返回 `CAPABILITY_DISABLED`，保留矩阵与 fixture |
| PWR-031 | 测试与门禁表述/覆盖改进（见下方细项） | 见下 |
| PWR-032 | 前端 UI 缺陷与杂项（见下方细项） | 见下 |
| PWR-034 | `internal/assistant/resource_context.go:227-229` 未校验窗口上界/纳秒，`FaultChainRequest.Valid`（`internal/investigation/fault_chain_context.go:99`）已拒绝 | 复用统一窗口校验；下游已兜底，无绕过路径 |
| PWR-035 | 死代码与重复实现：`evidenceNamespace`（`sp05_handlers.go:377-382`）、`var _ = strings.Compare`（`sp05_reads.go:194`）、`var _ = json.Unmarshal`（`app/sp05_config.go:30`）、未消费的 `internal/persistence/dbgen/**`、三处 token RoundTripper（`app/sp04_runtime.go:250-264`、`opsctl/graph_recovery.go:24-41`、`app/source_credentials.go:18-56`）、CRL 校验重复（`auth/workload.go:92-124` 与 `openbao/pki.go:545-549,602-645`）、不可达的 `LiveMapper.ByBackend`（`deepflow/live.go:106-124`）、无路由的 `tenant.UpdateCurrentTenant`（`tenant/service.go:67`）、`auth/oidc.go:91-101` 与 `:134-142` 重复解析 claims、`ontology` 缺 ctx 入口（`service/diagnostic/service.go:74-76`）、Python 未用导入（`worker.py:2-13`、`job_api.py:1`、`holmes_adapter.py:45`） | 逐项清理或标注；不改变行为 |
| PWR-036 | `internal/app/bootstrap.go:64` 对 API/Worker 都要求 `OIDCIssuerURL`，而读取点只有 `internal/app/api_runtime.go:103` 与 `internal/app/profile.go:36`（API 分支），Worker 从不读取 | 把该必填项降为 API 专属 |
| PWR-037 | `api/schemas/error-envelope-v2.schema.json` 严格，但 `api/openapi/platform-v1.yaml` 若干 schema 较松（`:34`、`:71-76`）；`deploy/charts/ops-metrics-server/templates/metrics.yaml:79` 以镜像摘要三元表达式选架构 | 关键响应补 `additionalProperties:false`；架构从 values 显式传入 |
| PWR-038 | `services/investigator/src/investigator/fault_chain.py:13,23-24` 用预算上限初始化本地剩余；`assistant_binding.py:118-139` 用文本正则判定是否需要数值查询/取证；`fault_chain.py:236` 摘要声称观察可追溯（空 edges 时仍成立）；`workload_identity.py:160-167` 续签失败静默降级 | 预算与服务端对齐或把 429 转软缺口；任务类型由服务端绑定下发；摘要按实际内容生成；续签失败记录静态类别 |
| PWR-039 | 覆盖缺口：`internal/investigation/mcp`（预算预留/结算核心）**无任何测试文件**（`grep` 0 命中）——这正是 PWR-001 长期存在的原因之一；`internal/investigation` 的 `allocation.go`、`assistant_budget.go`、`lifecycle.go`、`dispatcher.go`、`repository.go`、`recovery.go`、`result.go`、`assistant_formal.go`、`fault_chain_committed.go` 无包内单测；`web/tests/graph_view.test.tsx` 以 `vi.mock("cytoscape")` 替代真实 canvas；`internal/assistant/resource_context_test.go:150-154` 的撤权为桩；评分器不校验摘要中的数值文本 | 为 mcp 与关键共享路径补最小单测；标注 mock 用例的真实覆盖边界 |

### PWR-031 细项（测试与门禁表述）

1. `test/integration/sp05_process_crash_test.go:18-21` 的裸 `return` 使辅助入口在**外层套件**贡献一个空执行 PASS，应表述为"通过数包含辅助入口"；父测试仍在内部完成实质验证。修法应与 PWR-005 统一（顶层 `-skip` 排除），不应把它说成"父测试验收造假"。
2. `web/tests/e2e/model_unavailable.spec.ts:2-9` 的 `test.skip` 写在测试函数体内：当 `session.ts` 的 `config`/`page` fixture 因缺少 `OPS_WEB_QA_INPUT`/`OPS_WEB_QA_CDP` 抛错时，skip **不会被求值**（测试报错）；但环境齐备而 `OPS_WEB_QA_MODEL_OUTAGE != "1"` 时 skip 正常工作。守卫应上移到模块顶层。
3. `web/tests/e2e/assistant_runtime.spec.ts:141` 允许 `partial` 记为通过——是否可接受取决于该用例的验收目标；`:200-204` 的 `every` 对空数组恒真，应在断言前要求 `datasets.length > 0`；`:151` 使用子串断言。可保留"空数组断言不足"这一定性。
4. `internal/graph/graph_test.go:225-227,246-258` 的性能豁免可整体跳过；`upstream_conformance_test.go:93-96` 在豁免时把 20k 对象裁到 12 个，容量维度不再被测。建议把豁免路径显式标注为"非验收"。
5. 集成测试在断言后写 `{"passed": true}` 回执（`assistant_fault_chain_test.go:555-558` 等）——回执由**测试自身**写出，不是被测系统自证；风险仅在于下游把它当作独立验证，建议加 `selfAttested` 标记或改由退出码派生。
6. `test/integration/migrations_test.go:430-447,456-480` 故意保留含审计/证据的数据库（合规），但缺少"保留库清单 + 显式清理"步骤；`sp05_golden_vertical_test.go:388-394` 对 OpenBao 缺失 `t.Fatalf` 而 DB 缺失 `t.Skip`，门禁边界不一致。

### PWR-032 细项（前端）

1. **图例与实现不一致**：`web/src/features/graph/legend.tsx:18` 写"动态关系：点划线"，实现是 `edge[relationClass="dynamic"] → line-style: "dashed"`（`GraphView.tsx:136-138`）。**撤回**原先"同一线型承载互斥语义/无法区分"的判断：`layout.ts:5-10` 的 `edgeExpired()` 同时依据 `validTo` 与 `validTo`/ttl，说明**关系类别与过期状态是可叠加的两个维度**（`ttl=0` 且 `validTo` 已过的边即"过期但非动态"），且关系表已分别呈现 `观测时效`（`GraphView.tsx:458-463`，用 `edgeExpired` 渲染 `Status`）与 `动态有效期`（`:464-467`）。保留的问题只是**图例用词与实现不符**，以及 `GraphView.tsx:293`"虚线：TTL / 有效期已过期"这句暗示"虚线⇔已过期"，而**未过期的动态边同样是虚线**。
2. `web/src/features/graph/GraphView.tsx:246-255` 在 `stale` 时已明确返回告警"收到过期图谱版本，请重新查询"，**因此不存在"界面上看不到过期提示"的问题**；`:259-265` 的重复 `{stale && <Alert/>}` 是不可达的冗余代码（建议删除即可，不代表用户缺失提示）。
3. `web/src/components/Status.tsx:40-47` 的 `off` 集合含 `"null"`，而 `data.ts:10-11` 的 `text(null)` 返回"空值（null）"，该分支永不匹配。
4. `web/src/features/assistant/LegalHoldControl.tsx:52-66`："解除保留"无 loading 态、无二次确认（"设置保留"有 loading）。
5. `web/src/pages/AssistantPage.tsx:429-441,444-452,453-458`：授权域/集群/命名空间控件缺可访问名称。
6. `web/src/features/assistant/useConversationStream.ts:71-72` 固定 2 秒重连、无上限与退避（`useInvestigationStream` 有指数退避）。
7. `web/src/pages/admin/AdminPage.tsx:36` 未使用的 `JSONView` 导入；`ConversationContext.tsx:3-7` 的 `object()` 与 `components/data.ts:1-5` 的 `record()` 等价。

## 5. 待裁定／需求歧义／设计取舍（D）

### PWR-014：迁移仅前向、无 PDB、回滚兼容性
`docs/runbooks/database-migrations.md:3` 明确"forward-only; no migration contains a `Down` section"，并给出失败恢复与"不得运行 `goose down`"的操作约定——**这是已文档化的设计取舍，不构成缺陷**。需要另外评估的是"应用回滚后 schema 领先代码"的兼容性（属运维取舍）。`deploy/**` 无 `PodDisruptionBudget`：`deployments.yaml:20` 在未启用 SP04 时 api/web/worker 均为 1 副本，**启用 SP04 时 worker 取 `sp04.workerReplicas`，默认值为 2**（`values.yaml:38,40`）。是否引入 PDB 需结合副本数与排空策略另行裁定；**注意 `minAvailable: 0` 不提供任何保护**，不应作为方案。

### PWR-030：`platform.tenants` 无 RLS
`migrations/00008_rls.sql:15` 显式排除 `tenants`；`00001_roles_and_schemas.sql:65` 授予 `api_runtime_role` SELECT，`00010_tenant_provisioning.sql:4` 只回收写权限。数据库角色确实具备查询能力，但**可达接口受角色门禁保护**：`internal/httpapi/tenant_handlers.go:34-38` 的租户/角色端点均挂 `auth.RequireRole(auth.PlatformAdmin)`，并有事务内 `authorizeAdmin`。因此"任何已认证 API 用户都能跨租户枚举"**缺少可达证据**，需裁定该设计是否有意（platform_admin 本就是租户管理员）。

### PWR-033：公开 steps 读取是否计入 Job 输出限额
`internal/investigation/recovery.go:122-139`（公开 `ReadSteps`，无 `chargeRead`）与 `internal/investigation/repository.go:252-269`（内部 `Steps`，有 `chargeRead`）口径不同；09 §51.3 只规定"恢复读取计入读取限额"。属需求歧义，需产品裁定后统一。

## 6. 复核后否定（保留记录）

- **候选：`CreateJob` 在自然键冲突时无条件补发 "queued" 事件。** 复核 `internal/investigation/repository.go:158-186` 后否定：`tag` 取自 **`worker_queue` 的 `INSERT ... ON CONFLICT DO NOTHING`**（`:162`），而 `worker_queue` 主键为 `(tenant_id, job_id)`（`migrations/00005_investigation.sql:19-30`）且认领时只 UPDATE 不删除，命中既有 Job 时 `RowsAffected()==0`，不会重复发事件。

## 7. 延期、阻塞与环境限制（不作为缺陷）

| 项 | 性质 |
|---|---|
| KubeVirt/CDI 未实现与未验收 | 延期（ADR-0008）；边界完好，未发现误开启；CLI 侧不对称见 PWR-029 |
| Trace / PyRCA 未实现 | 延期（ADR-0040/0020）；契约保持 `CAPABILITY_DISABLED` |
| 助手中文解释质量未通过（2 PASS / 3 partial FAIL） | 需求未满足（非代码缺陷）；人工评审须真人填写 |
| 真实五来源联合闭环、Node/PVC 覆盖、来源退化与预算收口 | 待运行验证（需 `192.168.0.63` 与授权） |
| 离线 Bundle 重建、干净重装、升级/回滚演练 | 待授权/环境 |
| 部署环境 18443/18643 观察与差异 | 未执行（无凭据/授权） |
| 依赖漏洞公告核对 | 未执行 |
| 第三方实现逐行审计 | 明确排除 |
| 本机 Python 调查服务测试 | 环境阻塞（缺 pytest，28 项中 9 项导入失败） |
| 本机部分 Go 测试首次构建 | 环境阻塞（根卷剩 485MB 导致链接失败，已重跑通过） |

## 8. 历史问题复核

上一轮 `docs/reviews/2026-10-15-project-review/`（HEAD `56946d32`）7 项问题在本基线逐条复核：**全部仍存在**。对应关系与定级变化：

| 上一轮 | 本基线结论 | 本轮编号与定级 |
|---|---|---|
| REV-001 / P1 | 仍存在（探针复现） | PWR-001 / P1 |
| REV-002 / P1 | 仍存在（探针复现） | PWR-002 / P1 |
| REV-003 / P2 | 仍存在（探针复现）；缺陷在 Go 侧常量 | PWR-003 / P2 |
| REV-004 / P2 | 仍存在（探针复现） | PWR-006 / **P1**（持久身份不一致，需设计改造） |
| REV-005 / P2 | 仍存在 | PWR-007 / P2 |
| REV-006 / P2 | 仍存在，范围收窄至 api/worker（web 在 sp08 路径已有探针） | PWR-008 / P2 |
| REV-007 / P2 | 仍存在并扩大（`00038`～`00043`），门禁实测失败 | PWR-004 / P2 |
