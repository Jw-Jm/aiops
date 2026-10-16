# 修复方案（2026-10-16）— 修订版 r2

> 已按两轮复核意见修订（见 [corrections.md](corrections.md)）。主要更正：PWR-001／002 的风险与回滚措辞（原表述把危险行为称为"保守方向"且有回归风险）、PWR-005 的修法（改用顶层 `-skip`，明确不采用裸 `return`）、PWR-010 的方案强度、PWR-014／036 的定性与范围、以及缺陷／建议／待裁定的分类。**本轮不实施任何修复**，只给方案。

## 一、实施顺序（建议）

| 批次 | 内容 | 理由 |
|---|---|---|
| 第 1 批 | PWR-001、PWR-002 | 普通授权操作即可触发；前者使预算失效并放大来源负载，后者造成持久身份故障 |
| 第 2 批 | PWR-006（与 PWR-002 同轮设计、分别验证）、PWR-003 | 资源生命周期与跨语言合同；PWR-003 影响所有非 16K Profile（当前部署为 16K，故非阻塞但需两侧同版本） |
| 第 3 批 | PWR-007、PWR-009、PWR-019、PWR-016、PWR-018、PWR-020、PWR-011 | 中等缺陷：数据可信性、可用性、契约与前端 |
| 第 4 批 | PWR-005、PWR-004、PWR-013 | 门禁与物料：PWR-005 使集成验收无法判定，改动最小可先做；PWR-004 需发行/离线授权 |
| 第 5 批 | PWR-008、PWR-012、PWR-015、PWR-017 | 部署与仓库卫生 |
| 第 6 批 | PWR-010（可选，先做低成本契约测试）、PWR-021～PWR-039 | C 类建议，按常规技术债推进，不阻塞 |
| 待裁定 | PWR-014、PWR-030、PWR-033 | 需产品/有权限者裁定后再定方案 |

依赖关系：PWR-002 与 PWR-006 共用"可见性 vs 删除"的生命周期模型，应同轮设计、分别验证；PWR-004 的重签需在 PWR-002／006／007 的迁移定稿之后进行（否则要重签两次）。

---

## 二、运行时缺陷（A 类）

### PWR-001 / P1
- **目标行为**：任何来源展开路径的原始查询数在**发远程请求前**按实际展开量原子预留；不足则拒绝，账本预留与实际请求一致。
- **根因**：成本计算只在 `network.` 前缀特化，Victoria 展开路径（`logs.*/trend`、`*/compare`、`query_range`）未接入。
- **最小完整修复**：
  1. 在 `internal/analytics` 增加统一成本函数（例如 `SourceRequestCost(intent) (int, error)`），覆盖 Network 现有语义与 Victoria：`compare`/`compare_percent` = 2；日志 `trend` = `floor((to-from)/step)+1`；`query_range`/单点 = 1；无法静态确定的路径一律拒绝（`ErrInvalid`），不猜。
  2. `internal/investigation/mcp/server.go:127-141` 改为调用该函数并 `reserve.RawQueries = cost`（去掉 `network.` 前缀分支）。
  3. 结算沿用现有 `settleTx` 语义；实际调用少于预留时按既有保守结算处理。
- **关联修改**：Victoria 执行路径需能报告实际展开次数（一致性校验）；`api/analytics/catalog-v1.json` 的 `trend` 上限与步长复核；`test/integration` 补 MCP 预算用尽用例；`internal/investigation/mcp` 目前无测试文件（PWR-039），应同批补最小用例。
- **兼容与数据影响**：既有 Job 的预留值变大（更严格），不影响已完成 Job 的历史数据；不需要迁移。
- **验收**：①探针 `TestReviewVictoriaExpandedRawQueries` 改为断言"预留=实际请求数"；②Job 仅剩 1 次 rawQuery 时 `logs.records/trend`（50 点）与任一 `compare` 均在**来源调用前**返回 `ErrBudget`；③足额时账本预留/消费与内存 Transport 记录一致。
- **检查命令**：`go test ./internal/analytics ./internal/investigation ./internal/investigation/mcp`。
- **发布影响**：无迁移；行为更严格，长窗口趋势在预算不足时会明确失败——属预期。
- **风险与回滚**：
  - **风险方向是"高估成本导致合法请求被拒"（偏保守、可接受）**；**"低估成本"才是原缺陷**——那会少记预算、发出超出限额的来源请求，必须靠"预留 == 实际展开"的断言防止。
  - **不得"回滚到单一常量"**：单一常量正是 PWR-001 的成因，恢复它等于重新引入缺陷。若成本函数出现严重误判，正确回滚是恢复"按实际展开量预留"的**更严格**版本（例如对无法静态确定的路径一律拒绝），而不是放宽为常量。
- **工作量**：M。

### PWR-002 / P1
- **目标行为**：来源可见性变化（标签不匹配）与原生 UID 删除彻底分离；同一 UID 重新进入来源范围后不触发身份冲突；真实删除仍保持不可复活。
- **根因**：`FilterSnapshot` 先于 `SyncSnapshot`，导致"过滤后缺失"被当作删除；且"来源不可见"与"原生删除"折叠进单一 `deleted_at`。
- **最小完整修复**：
  1. `internal/app/sp04_collector.go` 保留**过滤前**的完整原生 UID 集合（`s.State.LastListCompletedAt` 非零时才有效），传给 `SyncSnapshot` 用于"真实消失"判定；过滤后集合只用于 `current` 可见性与图发布。
  2. `internal/resourcestore/repository.go` 区分两种状态：原生删除写终态墓碑；来源不可见写可恢复状态（新增列或独立表，例如 `visibility='not_selected'`），`Resolve` 允许 `not_selected → active` 的复活，但拒绝 `deleted` 复活。
  3. `Retire` 对目标行缺失不再返回致命错误（避免整类同步中止），改为按缺失语义处理并记录。
- **关联修改**：迁移（新增列/表 + 索引 + RLS，并与 PWR-004 的准入重签同批）；`snapshot_scope.go` 输入契约；`observations.go`（观察覆盖）；图发布前的可见性过滤；相关 fixture 与集成测试。
- **兼容与数据影响**：需要前向迁移；历史 `deleted_at` 语义不变（仍是终态），新列默认"active"，对既有数据无破坏。不可逆部分：无法从旧数据区分"曾经因过滤而注销"的行，需一次性判定或接受其保持终态并在文档中说明。
- **验收**：①同 UID 标签 匹配→不匹配→匹配：期间图只移除该对象、`resource_entities` 不被墓碑化，恢复后重新发布且无 `ErrIdentity`；②真实 DELETE：保持终态，撤权隔离不向图发布范围外对象；③`Retire` 目标行缺失时不再中断该 GVR 的其余对象。
- **检查命令**：`go test ./internal/resourcestore ./internal/app ./internal/graph`；集成用例（需数据库）。
- **发布影响**：迁移 + 采集行为变化，需 Worker 与 API 同版本升级；升级期间旧 Worker 不写新列（默认 active），不会阻塞。
- **风险与回滚**：
  - 本修复的核心目的就是消除"把来源不可见误判为删除"。**任何把不可见重新当作删除的回归都不可接受**（那会产生持久身份故障、图长期不可用、且不可自愈），必须由验收用例①显式拒绝。
  - 另一侧风险是**漏判真实删除**：必须严格以"完整 List 成功 + 来源 active + 非过滤后缺失"为前提；此类漏判可由验收用例②与"来源失败不注销"用例覆盖。
  - 回滚需保留新增列（旧代码忽略它），**不得删除迁移表或墓碑数据**；回滚后是否停写新列需评估，但"恢复旧判定逻辑"不可作为回滚手段。
- **工作量**：L。

### PWR-006 / P1
- **目标行为**：在完整原生 List 成功的边界，按（tenant、source、cluster、GVR）用**持久**身份集合与当前快照求差，注销确实消失的来源关系。
- **最小完整修复**：`SyncSnapshot` 的 `previous` 改为来自持久查询（`resource_entities` 的 active + 来源归属）；仅当 `s.State.LastListCompletedAt` 非零、来源 active、非过滤后缺失时执行注销；保持注销单调性。
- **关联修改**：与 PWR-002 共用"可见性 vs 删除"模型；`internal/resource/resolver.go:108` 的别名冲突路径需在旧身份注销后再解析新对象；集成用例覆盖停机删除/重建、同 providerID 新 UID、多来源、新旧快照交错。
- **验收**：①停机期间删除 Node（新 UID 沿用 providerID）→ 重启后旧身份被注销且新 Node 解析成功；②部分快照/来源失败/Standby 旧快照**不**触发全局注销；③空 GVR 不误删（现有探针）。
- **检查命令**：`go test ./internal/resourcestore ./internal/resource ./internal/graph`；集成（需库）相关用例。
- **工作量**：L（与 PWR-002 同轮）。

### PWR-003 / P2
- **目标行为**：v3 的模型输入预留上界来自服务端已冻结且可信的 Profile；v2 保持 16384；预算不足仍拒绝。
- **根因**：`AllocateModel` 硬编码 `reserve.InputTokens != 16384`；Job 内只有 Profile 引用，未解析。**Python 侧按 Profile 预留是正确的**，缺陷在 Go 侧以常量代替 Profile 解析。
- **最小完整修复**：
  1. `internal/investigation/allocation.go:93` 去掉常量比较，改为：按 Job 的 Profile 引用（服务端已冻结）解析 `contextWindow`、`outputTokenLimit`，校验 `reserve.InputTokens == contextWindow` 且 `reserve.OutputTokens <= outputTokenLimit`；v2 Job 保留 16384。
  2. 解析所需的 Profile 材料必须来自服务端已准入的 `modelprofile` 记录，**不接受客户端上界**。
  3. 两侧口径对齐：Python 的预留上界继续取 Profile 的 `contextWindow`（无需改动其设计），仅需确保与 Go 解析结果一致；现有 `services/investigator/tests/test_model_profile.py`、`test_initial_tool_choice_unittest.py` 对 32K 的期望**保持不变**即可，不需被改写。
- **关联修改**：`internal/investigation/repository.go`（Job 装载/引用解析）、`internal/httpapi/investigation_handlers.go:479-496`、`internal/modelprofile`、`test/fixtures/assistant/model-profile.fixture.json`。
- **兼容与数据影响**：不改预算总量；旧 v2 Job 行为不变；需两侧同版本发布。
- **验收**：①8K/16K/32K 合法预留通过；②伪造预留（与服务端解析不符）被拒；③Job 总预算不足仍 `ErrBudget`；④v2 16K 兼容；⑤探针 `TestReviewAccepted32KProfileRejectedBeforeModelAdmission` 转为**反向**断言（32K 现在应通过）。
- **检查命令**：`go test ./internal/investigation ./internal/modelprofile ./internal/assistant`；`cd services/investigator && pytest tests`（锁定环境）。
- **工作量**：M。

### PWR-007 / P2
- **目标行为**：恢复时长统计只使用时钟可靠性可证明的样本；否则计入缺口。
- **最小完整修复**：Incident 持久化 `started_at` 的可靠性来源（新增列或不可变 Finding 依据引用）；`correlator.go:109-115` 按 `f.TimeReliable` 写入；`analytics/controlplane.go:159` 与 `statistics.go:9` 排除不可靠样本并计入 missing；`split.go:79` 同步。
- **验收**：①混合可靠/不可靠样本时降级或缺口；②全无可靠样本返回 `no_data`；③保留租户/关联 Finding/namespace 授权过滤。
- **工作量**：M。

### PWR-009 / P2
- **目标行为**：并发槽位在任何异常路径下都被归还。
- **最小完整修复**：`worker.py:95-98` 改为嵌套 `try/finally`（或 `ExitStack`），确保 `gate.release()` 无条件执行；`close()` 失败仅记录静态类别。
- **验收**：新增用例：让 `job_api.close()` 抛异常，断言槽位被归还且后续请求仍可受理。
- **工作量**：S。

### PWR-011 / P2
- **目标行为**：能力接口失败时显示错误与重试，且不把"取不到"表述为"没有准入证明"。
- **最小完整修复**：`AssistantPage.tsx` 引入 `QueryState`（loading/error/retry）；区分 `error` 与 `enabled=false`；禁用态挂 `aria-describedby` 说明。
- **验收**：`web/tests` 新增用例：mock 能力接口 500 → 出现错误提示与重试，禁用原因可见。
- **工作量**：S。

### PWR-016 / P2
- **目标行为**：客户端可区分"需确认/确认过期/范围不匹配/命令不匹配"。
- **最小完整修复**：`internal/action` 拆分错误类型（含 `ErrStepUpExpired`），`action_handlers.go:41-42` 与 `stepup.go` 映射到 412/409 与契约枚举；`api/openapi/platform-v1.yaml` 对应 responses 显式声明 412。
- **关联修改**：前端文案映射与契约测试。
- **验收**：四种场景分别返回预期状态码与 code。
- **工作量**：M。

### PWR-018 / P2
- **目标行为**：身份冲突 Finding 的处置路径明确且可达。
- **最小完整修复**：改用 `finding/v2` 信封（含 `findingId`、`timeReliable`、`observedAt`）并复用 `SubmitSP05Candidate`；或明确"仅生产者侧可见"并**不写 outbox**（补注释与测试）。
- **验收**：`RelayPass` 不再产生该 schema 的死信；若走 v2 则 `/findings` 可见且可关联 Incident。
- **工作量**：M。

### PWR-019 / P2
- **目标行为**：后台监听失败可观测并触发关闭/降级。
- **最小完整修复**：SP06/SP07 用 errgroup 或错误通道捕获 `Serve/ServeTLS` 错误，记录 `runtime.Logger` 并 `SetComponentDegraded`，必要时 `cancel()`。
- **验收**：注入监听失败后出现错误日志与降级指标，进程不再"静默存活"。
- **工作量**：S。

### PWR-020 / P2
- **目标行为**：故障页 RCA 状态过滤与真实枚举一致。
- **最小完整修复**：`FindingsPage.tsx:90-91` 改为 `["confirmed","probable","unresolved"]`，`stateLabels` 补 `probable`。
- **验收**：三种状态各能筛出结果。
- **工作量**：S。

## 三、门禁、物料与部署缺陷（B 类）

### PWR-005 / P2（改动最小，可优先）
- **目标行为**：`make test-integration` 在环境齐备时可通过"零 skip"门禁，且辅助入口既不计入 skip 也不虚增通过数。
- **最小完整修复**：在 `Makefile:71` 的验收调用上加精确排除：
  ```
  go test ./test/integration -count=1 -json -timeout=10m -skip '^TestAssistantCrashWorkerChild$'
  ```
  依据：①`assistant_process_recovery_test.go:133-138` 的父测试**自行**用 `os.Executable()` 启动子进程并显式传 `-test.run ^TestAssistantCrashWorkerChild$` 与环境变量，顶层 `-skip` **不会**传播到子进程，辅助入口的实质作用不受影响；②实测该 `-skip` 下报告内 skip 事件 0 个、run 事件 0 个；③`Makefile:67` 的 `test-e2e` 已有 `-skip` 先例。
- **明确不采用**：把 helper 改成裸 `return`（会让外层套件通过数包含一个空执行的辅助入口，且与父测试内的实质验证混淆）；改造成独立子命令可行但非必需。
- **验收**：`check-test-report.py` 在带环境的完整运行中退出 0；报告内无 `Action=="skip"`。
- **工作量**：S（需一次带数据库的运行确认）。

### PWR-004 / P2
- **目标行为**：`make check` / `check-runtime-source` 在当前源码上通过。
- **最小完整修复**：按现有准入流程重新准备（`prepare-migration-source.py --out`）→ 更新 `migration-tool-go{,-amd64}.lock.json`、`migration-tool-source{,-amd64}.json`（含 `sourceBundleSHA256`、`firstPartySourceSHA256`）→ 锁定 go1.27.1 下用离线闭包重建 db-migrate → 补从 `00037` 之后的真实前滚证据。
- **依赖**：应在 PWR-002／006／007 的迁移定稿后执行，避免重复重签。
- **验收**：两个架构的 `--check` 均退出 0；离线重建的 db-migrate 能在空库迁移到最新并幂等重跑。
- **发布影响**：需发行/离线授权与物料重建；未经该授权不得更新锁（否则形成自签自证）。
- **工作量**：M。

### PWR-008 / P2
- **目标行为**：K8s 只把"可提供业务请求"的实例计入 Ready；僵死实例被重启。
- **最小完整修复**：为 **api/worker** 增加 `/healthz`（存活）与 `/readyz`（就绪，DB 池可用、OIDC discovery 完成、必需组件已启动），在 `deployments.yaml` 配置 readiness/liveness（慢初始化配 `startupProbe`）。注意范围：`sp08.enabled=true` 时 web 由 `sp08.yaml:50-51` 渲染并已有探针；只有 `deployments.yaml` 的 web 渲染路径（`sp08.enabled=false`）缺探针，可一并补齐。
- **验收**：`helm template` 静态确认探针存在；隔离环境延迟 OIDC/DB 的 rollout 验证（需授权）。
- **工作量**：M。

### PWR-012 / P2
- **目标行为**：模板读取的键都有 schema 声明，拼写错误在 `helm template/lint` 阶段失败。
- **最小完整修复**：补全 `values.schema.json`（`workloadsEnabled/global/networkPolicy/observability/workloadIdentity/components/runtime/sp04~sp08`）并加顶层 `additionalProperties:false`。
- **验收**：未知键报错；既有 `values.yaml` 与 `deploy/profiles/*` 渲染结果不变。
- **工作量**：M。

### PWR-013 / P2
- **最小完整修复**：`Makefile:39` 参数列表加入 `services/investigator/src/investigator/assistant-output.schema.json`。
- **验收**：手动改动该文件后 `make check-generated` 失败。
- **工作量**：S。

### PWR-015 / P2
- **最小完整修复**：为 `artifacts/task27-cleanup.go`、`artifacts/pre-sp07/*/main.go` 加 `//go:build ignore`（或移至 `tools/` 并加 build tag），清理逻辑复用 `cmd/opsctl/current_cleanup.go` 的规划+`--apply` 语义。
- **验收**：`go vet ./...` 不再编译这些文件；清理默认只规划。
- **工作量**：S。

### PWR-017 / P2
- **目标行为**：区分"未配置 QA 环境"与"验收失败"，并让浏览器验收入口可发现。
- **最小完整修复**：live spec 顶层 `test.skip(!process.env.OPS_WEB_QA_INPUT, ...)`；可选地在 `Makefile` 增加 `test-web-e2e` 包装 `pnpm --dir web test:e2e`（`package.json:16` 已是可用入口）。
- **工作量**：S～M。

## 四、建议类（C 类，P3）要点

| 编号 | 修复要点 | 验收 | 量级 |
|---|---|---|---|
| PWR-010 | 保留"运行时 ↔ spec"契约偏差。低成本改法：新增自动化契约测试，逐个 operation 比对 spec 的路径/方法/错误体（可复用 `test/contract` 既有风格）。彻底改法（可选、不做强制）：挂载生成的服务器；**须区分 `ServerInterface`（`gen/api/openapi.gen.go:7356`）与 `StrictServerInterface`（:16698）**——仅 `HandlerFromMux` 不启用 strict 校验，需 `NewStrictHandler`（:16984）包装。ADR-0007 未要求实现 strict 接口，不据此要求大规模重构 | 契约测试能发现"spec 有而运行时未登记"的路由 | 低成本 M / 彻底 L |
| PWR-021 | 移除 `probe.go:59,63` 的 `authRef` 输出，仅留状态码或摘要前缀 | 错误串不含 `openbao://` | S |
| PWR-022 | 审计禁列匹配改敏感子串 + `passwd`，或改允许列表 | 单测覆盖新键名 | S |
| PWR-023 | 只读投影扩展拒绝键名集合 | 单测覆盖 | S |
| PWR-024 | 统一 step-up-in-`AuthorizeTx` 的幂等中间件 | 单测：step-up 过期后重放被拒 | S |
| PWR-025 | SP05 游标改用签名游标 | 单测：篡改被拒 | S |
| PWR-026 | 为硬件目标补 post-check recipe 或显式"不适用" | 测试得到确定判定 | M |
| PWR-027 | Redfish 凭证按需读取 | 轮换文件后无需重启 | S |
| PWR-028 | 注入 `runtime.Logger`；不再丢弃 `ExpirePass` 错误 | 清理失败可见 | S |
| PWR-029 | `profile resolve` 对 `virtualization/full` 返回 `CAPABILITY_DISABLED` | 契约测试 | S |
| PWR-031 | 按细项：①辅助入口表述统一为"通过数包含辅助入口"（修法与 PWR-005 一致）；②e2e `test.skip` 上移模块顶层；③`every` 前断言非空（允许 `partial` 与否由该用例目标决定）；④性能豁免标注"非验收"；⑤回执加 `selfAttested` 或改由退出码派生（回执由测试写出，非被测系统自证）；⑥保留库加清单+清理步骤、统一 OpenBao/DB 门禁语义 | 各条对应测试 | M |
| PWR-032 | 按细项：①图例"点划线"与实际 `dashed` 对齐；`GraphView.tsx:293` 文案不应暗示"虚线⇔已过期"（未过期动态边也是虚线）。**保留 `:246-255` 的 stale 告警**（它已明确提示"收到过期图谱版本，请重新查询"），仅删除 `:259-265` 不可达冗余分支；②其余：`Status` 的 `null` 判定、法律保留 loading/确认、助手控件 a11y 名称、会话 SSE 退避、未用导入与重复 util | 前端单测 + 静态检查 | M |
| PWR-034 | `NewContext` 复用统一窗口校验 | 单测：未来时间/纳秒被拒 | S |
| PWR-035 | 删除死代码/占位；抽取共享 token RoundTripper 与 CRL 校验；`ByBackend` 删除或标注；`UpdateCurrentTenant` 接路由或删除；避免重复解析 claims；`ontology` 暴露 ctx 入口；清理 Python 未用导入 | 现有测试保持通过 | M |
| PWR-036 | Worker 校验去掉 `OIDCIssuerURL` 必填（`bootstrap.go:64`），改为 API 专属 | 单测 | S |
| PWR-037 | 关键响应补 `additionalProperties:false`；metrics-server 架构改为 values 显式传入 | 契约测试 + helm 渲染 | M |
| PWR-038 | `fault_chain.py` 起始预算与服务端对齐或把 429 转软缺口；任务类型由服务端绑定下发；摘要按实际内容生成；续签失败记录静态类别 | Python 测试 | M |
| PWR-039 | 为 `internal/investigation/mcp` 与关键共享路径补最小单测；标注 mock 用例的真实覆盖边界 | 新增测试 | M |

## 五、待裁定项

| 编号 | 待裁定问题 | 裁定后方案方向 |
|---|---|---|
| PWR-014 | 是否需要在"仅前向迁移"（`docs/runbooks/database-migrations.md:3` 已文档化）之外提供回滚兼容策略；是否为 api/web/worker 引入 PDB（注意 SP04 启用时 worker=2） | 若需要，采用扩展-收缩式变更并记录兼容矩阵；PDB 的 `minAvailable` 必须 ≥1 才有保护意义（**`minAvailable: 0` 无保护作用，不可采用**） |
| PWR-030 | `platform.tenants` 无 RLS、`api_runtime_role` 有 SELECT 是否有意（可达接口目前受 `RequireRole(PlatformAdmin)` 保护） | 若为过度授权，改为 SECURITY DEFINER 函数按 actor 过滤；若有意，在 ADR/文档记录 |
| PWR-033 | 公开 steps 读取是否计入 Job 输出限额 | 裁定后统一 `ReadSteps` 与 `Steps` 的计费口径并补断言 |

## 六、跨问题注意事项

1. **不要为了让检查通过而放宽门禁**：PWR-004／005 的修复方向是补齐物料与精确排除辅助入口，不是关闭 `check-test-report.py` 或跳过准入比对。
2. **不要提高默认预算**：PWR-001／003 应通过"精确成本/精确上界"解决。
3. **不要伪造时间或历史**：PWR-007 应通过冻结可靠性证据解决，而非用当前快照回填。
4. **回滚语义**：任何"恢复旧的宽松/错误判定"都不是安全回滚（PWR-001 恢复常量、PWR-002 恢复"过滤即删除"、PWR-006 恢复进程内 map 均属重新引入缺陷）；回滚应保留新增数据结构并保持更严格的约束。
5. **迁移与 RLS 联动**：PWR-002／006／007／030 若新增列或表，必须同时补 RLS 策略（参考 `00008_rls.sql` 及后续迁移模式），并触发 PWR-004 的准入重签。
6. **发布与授权边界**：PWR-004 涉及发行物料；PWR-008／010／014 的部分验收需要隔离环境；生产发布不在本轮范围。
