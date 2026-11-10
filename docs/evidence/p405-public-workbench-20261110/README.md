# P4-05 公开工作台 W1–W5 返修与 26 动作逐条清单（2026-11-10，含同日第二轮返修更正）

本轮是 P4-05 集中返修（Codex 独立审核指出的 W1–W5）的**第二轮返修**。总体 P4-05 / P5-01 / P5-02 / P6-01 仍然完整，本轮不声明任何整项完成、不提升 visual 与整片六维状态。M1（Chart 与内存配置）已由 Codex 单独提交 `d7789c5` 正式接受，本轮未重做。

> 更正说明（第二轮返修，保留上一轮的历史失败回执）：
> 上一轮 README 中"远程最终实跑因 VPN 掉线未完成""用例数 4/5/9 例""全量 web 310 例""T-D4 称重试但未点击""旧轮次一律拒绝停止"等表述，与本轮真实执行结果不一致。下文按真实执行结果更正，历史失败回执原样保留在第 3.2 节，不做删除。

## 1. 本轮实际改动与已跑验证

### W1 详情加载与运行刷新（第二轮返修：步骤刷新由真实作业状态驱动）
- 实现：`web/src/features/assistant/UnifiedInvestigationDetail.tsx`
  - 上一轮保留：`QueryState loading` 由 `turn.isPending || job.isPending` 改为 `isLoading`；详情不自拼 scopeKey，改由父页传入可信 `scopeKey`；只读 GET，不创建 Job。
  - 本轮修复：上一轮 steps 的 `refetchInterval` 从**步骤数组**读取 `.state`，步骤载荷不带作业状态，取值恒为 `undefined`，运行中的步骤永远只有首次 GET。改由**作业的真实状态**驱动：作业状态为 `queued|running|cancel_requested` 时步骤每 2s 刷新一次，作业进入终态即停止轮询。
  - 本轮修复：作业进入终态时**额外取一次最终 steps**（`finalStepsRead` 按作业号去重），保证终态的步骤账本与 `budgetConsumed` 都是最终值，而不是最后一次轮询的中间值。
  - 本轮修复：上一轮"重试"同时调用 `turn.refetch()` 与 `job.refetch()`，已知 jobId 时会强行发出 turnId 为 `undefined` 的请求。改为**只重试真正发起过的查询**（`turnEnabled` / `resolvedJobId`），已知作业号的详情重试不再产生 turn 请求。
- 已跑（本轮新增 2 例，共 11 例全通过）：`web/tests/unified_investigation.test.tsx`
  - `refreshes the steps of a running job and shows the step that appeared`：作业先 `running` 后 `succeeded`，断言**运行中出现的新步骤真的出现在页面**（`查询监控指标`），断言终态作业给出最终消耗（`工具调用：3 次`），并断言作业终态后不再发起步骤读取。
  - `retries the job of a known job id without reading a turn`：点击"重新加载"后断言 `getInvestigation` 恰好 2 次且 `getAssistantTurn` **0 次**。

### W2 生命周期与确认请求围栏（第二轮返修：旧链接消费 + 迟到结算围栏）
- 实现：`web/src/pages/AssistantPage.tsx`、`web/src/features/assistant/InvestigationStopConfirm.tsx`
  - 上一轮保留：scopeKey/会话变化时清空 `investigationDetail`/`stopTarget`/`stopError`/`stopBusy`；`StopTarget` 冻结 `idempotencyKey`；`confirmStop` 用 epoch + 会话 + scope 围栏；401/403 只清当前 scope 缓存；409 不自动换 revision 重试；`openStopConfirm` 不回退到当前 active 轮次。
  - 本轮修复：上一轮 `detailLinkRef` 在第一次 scope 变更时被置 `undefined`，第二次 scope 变更时**旧 URL 参数被重新应用**，A→B→A 会自动重开原旧详情。改**消费式链接**：离开授权上下文时把该链接记入 `consumedDetailRef` 并关闭详情，后续新代次不得自动重开；正常新链接（新作业号）仍可打开；读者主动关闭后重新给出同一链接仍可再次打开。
  - 本轮修复：scope/会话变化时 `stopEpochRef` 递增一格，旧作用域的在途请求其迟到 success/error/finally 不得写入新详情、不得清掉新作用域正在进行的确认 busy 状态。
- **口径更正**：上一轮"详情轮次与活动轮次不一致时拒绝开确认"与代码实际行为冲突。代码实际允许**同一会话内精确指定的旧轮次**（用例 `stops exactly the turn of the opened detail` 证明停止的是所打开详情的那一轮），只拒绝**属于其他会话**的详情（用例 `refuses to stop a detail that belongs to another conversation`）。本轮以代码实际行为为准。
- 已跑（本轮新增 6 例，共 12 例全通过，真实挂载父页）：`web/tests/unified_investigation_lifecycle.test.tsx`
  - `does not reopen the old detail after the scope went away and came back`：A→B→A 后旧详情不再打开，且旧作业不再被读取；随后一条**新链接**仍能打开精确作业。
  - `does not open the old detail for a new account`：账号代次变化后旧链接不打开旧详情。
  - `does not open the old detail in a new session of the same account`：会话变化后旧会话的链接不在新会话里重开旧详情。
  - `keeps the new confirmation busy when the older answer settles late`：旧作用域请求迟到结算后，新确认窗口仍在、仍处于 busy，重复确认 0 额外请求。
  - `drops the cache of the refused scope and never retries a conflict`：403 拒绝只清当前 scope 缓存且只发 1 次请求。
  - `reports a conflict of the stop request without retrying it`：409 报错、不重试、不清缓存。

### W3 同一公开投影用于故障历史
- 实现沿用上一轮已保存增量：`internal/httpapi/investigation_history.go` 的 `out` 为 `[]publicInvestigationJob`，逐行调用 `publicInvestigationJobView`；`internal/httpapi/investigation_handlers.go` 的 GET job 同样走 `publicInvestigationJobView`。本轮未重写该文件。
- 已跑：`go test ./internal/httpapi/... -count=1` 全包通过（含 `PublicInvestigationJobView`）；真实 PG 响应证据见 W5。

### W4 同故障 v3 历史前端接线（第二轮返修：主入口即统一只读详情）
- 实现：`web/src/features/investigation/InvestigationPanel.tsx`、`web/src/pages/AssistantPage.tsx`
  - 上一轮保留：故障页历史标题改为"同一故障的历史调查"；父页从链接参数打开详情，scope 变化时关闭；关闭时清除链接参数。
  - 本轮修复：上一轮 v3 行的**主按钮**仍是 `choose(rowJobId)`，进入旧结果视图，统一详情只是**额外**的一个按钮。主按钮现按行性质直接进入统一只读详情：v3 行精确打开该会话/turn/作业的 `/assistant?conversation=…&investigationJob=…&investigationTurn=…`；v2 行保持原有结果视图。既有分页（更早调查 / nextCursor）保留，未新增任何全局跨故障页面。
  - 本轮修复：上一轮用展示占位串判断"是否 v3"，占位串恒为真，导致 v2 行也被判为 v3。改按原始值判空。
- 已跑（本轮新增 1 例，共 6 例全通过）：`web/tests/investigation_history.test.tsx`
  - `opens the unified detail of a v3 history entry of the same incident`：点击**真实主历史条目**后地址为统一详情链接，不含 `jobId=`，且 `api.job`/`api.create` 均 0 次（不触发旧结果视图、不新建作业、不调模型）。
  - `keeps the legacy result view for a v2 history entry of the same incident`：v2 行主条目仍进旧结果视图且不含 `conversation=`。

### W5 真实原生 PG 正反例与 26 动作清单
- 见本文第 3 节与第 4 节。本轮已在远程 QA 真实 PostgreSQL 上取得 PASS，证据日志路径见第 3 节。

## 2. 本轮实际执行的检查

| 命令 | 结果 |
| --- | --- |
| `go build ./...` | 通过 |
| `go test ./internal/httpapi/... ./internal/investigation/... ./internal/assistant/... ./internal/graph/...` | 全部 ok：httpapi 5.9s、investigation 3.2s、investigation/mcp 1.3s、investigation/tools 2.5s、assistant 5.1s、graph 210.9s |
| `go vet ./test/integration/` | 通过 |
| `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c ./test/integration -o /tmp/w5-public-projection.test` | 编译通过（linux/amd64 测试二进制） |
| `cd web && npx tsc --noEmit` | 通过（无输出，退出码 0） |
| `cd web && npx vite build` | 通过（✓ built in 412ms） |
| `cd web && npx vitest run` | **61 文件 / 320 用例全部通过**（本轮新增 9 例：详情 2、生命周期 6、历史面板 1；上一轮 README 写的"310 例"与实际用例数差 1 例，以本轮实跑 320 为准） |
| 远程原生 PG 实跑（`mssc-direct` QA 容器，DSN 仅读入 `SP03_TEST_DATABASE_URL`，未打印） | **通过**，见第 3 节 |

## 3. 远程原生 PG 证据（W5）

### 3.1 本轮真实实跑（最终状态：通过）
- 允许写范围：`mssc-direct:/mnt/ops-sdb/ops-p502-qa-20261109`（容器 `ops-p502-qa-20261109`，标签 `ops.platform.io/task=p502-qa-20261109`，127.0.0.1:15584）。DSN 只由 `private/dsn` 读入 `SP03_TEST_DATABASE_URL`，未打印、未外传。
- 用例：`test/integration/native_public_projection_boundary_test.go`（`TestNativePublicInvestigationProjectionBoundary`）。本机 `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c ./test/integration` 编译，`COPYFILE_DISABLE=1` 复制到 QA 目录，SSH 内用 Python 读取 DSN 并在容器内 `/root/repo/test/integration` 执行二进制（migrations 1..75 已在容器内 `/root/repo/migrations`）。
- 本轮修复的两处 fixture 缺陷：
  1. 上一轮 `graph.Authorization.Effective` 用的是 `f.ctx`（**不带 native 会话**），解析不到原生授权，落到 legacy 分支直接 `effective scope: FORBIDDEN`。改为使用带 native 会话的 `account.ctx`，这是常规 fixture 修正，不新增 API、不新增迁移。
  2. 上一轮统一入口作业是 `data_query` 会话产生的任务，`investigation.jobs` 的 `assistant_job_version` 约束不允许把它挂到故障上（`incident_followup` 必须带 incident，非 `incident_followup` 必须不带 incident）。改为用真实的 `troubleshoot` 会话 + 故障绑定，走会话侧正规路径产出带故障与故障版本号的 `investigation-job/v3`。
- 取消边界用例重写：上一轮 `call` 用 nil body 且无 `Idempotency-Key`，`POST :cancel` 在进入 Stop 前就被 `INVALID_ARGUMENT` 拒绝，而断言只要求 `status != 200`（400/500 也算通过），根本没有触达 foreign owner 边界。本轮改为**合法 JSON `{}` + 稳定有效幂等键**，并对确切状态码断言。
- 本轮真实用例覆盖（真实 PostgreSQL + API 运行角色 + 真实 native 会话上下文）：
  1. 本人读 GET job：200，响应含 `conversationId`/`turnId`/`schemaVersion=investigation-job/v3`；库中该作业确实带 `assistant_binding`，摘要与上下文摘要落在 `investigation.assistant_turns`。
  2. 本人读 GET steps：200。
  3. 同一故障历史：24 条 v2 + 1 条 v3 共 25 条，第一页 20 条并带 `nextCursor`，第二页 5 条，两页无重复且并集等于全部 25 条（24 条 v2 各为一次独立准入，靠不同 focus 得到不同准入摘要，避免被唯一索引合并为一条）。
  4. v3 行携带**精确跨会话链接**：`conversationId`/`turnId` 指向产出该作业的那一个会话与那一轮，且与同一所有者的另一个会话不同。
  5. 响应体不含内部绑定：正文不得出现 `assistant_binding`/`grantSnapshot`/`inputDigest`/`contextDigest`/`authorization_source`/`identity_source` 等键（GET job、GET steps、历史第一页与第二页均断言）。
  6. 同租户他人 operator 读该作业：拒绝且正文不含该作业号；跨租户主体读：拒绝且正文不含该作业号。
  7. 取消边界（每条请求都带合法 body 与幂等键）：无 body → 400；无幂等键 → 400；**他人取消 → 恰好 403**；作业不存在 → **恰好 404**；本人取消 → 200 且状态 `cancelled`、`error_code=CANCELLED`、保留 `assistant-answer/v1` 部分答复且带 `CANCELLED` 警告、事件账本恰有 1 条 `cancelled`、幂等账本恰有 1 行绑定该作业；同一幂等键换作业 → 409。
  8. 撤权（`role_grants` 置 `revoked` 并写 `revoked_at`/`revoked_reason`）、禁用（账号 `disabled`）、授权版本失效（账号 `authorization_version+1`）三种情况下：GET job / GET steps / 历史 / 取消**全部 403**，且作业状态、预算账本、事件账本、幂等账本、审计计数在前后完全一致（0 状态/0 账本/0 审计写入）。三种情形都用真实运行角色与真实 native 会话，未用 legacy 绑定补漏。
- 真实日志（远程 QA 目录，本轮生成）：
  - `/mnt/ops-sdb/ops-p502-qa-20261109/p405-r3-public-projection.log`：`--- PASS: TestNativePublicInvestigationProjectionBoundary`，三个子用例 `a_withdrawn_grant` / `a_disabled_account` / `a_stale_authorization_version` 全部 PASS。
  - `/mnt/ops-sdb/ops-p502-qa-20261109/p405-r3-regression.log`：本用例 + 相关回归共 11 个顶层用例全部 PASS（含 `TestAssistantCancellationAndBudgetKeepCommittedNumbers`（本人取消/预算/模型输出 3 个子用例）、`TestAssistantPartialReasonsFollowCommittedReads`、`TestAssistantFocusAndBudgetTighteningInPostgreSQL`、`TestAssistantInvalidFocusAndBudgetWriteNothingInPostgreSQL`、`TestAssistantHTTPRejectsInvalidBudgetTypesInPostgreSQL`、`TestNativeAssistantContextRefusesRevocationAndCancel`、`TestNativeAdminAssistantLegalHoldPersistsAndAudits`、`TestNativeAssistantLegalHoldRefusesWithoutAuthority`、`TestNativeAssistantLegalHoldKeepsTheRuntimeRoleFenced`、`TestSP06CancelExpireAndCurrentRevocation`、`TestSP06CancelIdempotencyBindsRequest`）。
  - 受审计测试库按规则保留，不删除；未访问 `ops_current`、业务库、部署、账号或环境。
- **历史证明未达成的部分**：本人 GET job/steps 与历史在本轮才首次走到 200，上一轮及更早没有 200 记录，因此"历史已证明通过"在本轮之前不成立，本轮以实跑日志为准。

### 3.2 上一轮的历史失败回执（原样保留，不删除）
- 第一次远程实跑已进入真实库并完成 1..75 迁移，因用例自身 SQL 取了错误列（`input_digest` 在 `investigation.assistant_turns` 而非 `investigation.jobs`）失败。
- 第二次远程实跑在 VPN 隧道掉线期间未能完成：`bind 10.10.10.1: Can't assign requested address`、`ssh: connect to host 10.10.10.2 port 22: failure`，绕过 `BindAddress` 后为 `connect to host 10.10.10.2 port 22: Operation timed out`，`netstat -rn` 中已无 `10.10.10.0/24` 路由，08:52–09:15 之间 41 次重连尝试全部失败。旧日志 `native-public-projection.log` 记录的是修正前那次真实运行。
- 本轮网络恢复后用同一条路径重跑，取得第 3.1 节的最终 PASS。

## 4. P4-05 主切片 26 动作逐条清单

来源：`docs/plans/ui-v12-replication-20261021/acceptance.csv` 中 `primary_task=P4-05` 的 26 行（未重扫全 458 片）。
"本轮实际已跑测试"一列只登记**可定位的单点用例**；凡本轮没有单点用例的动作，明确写"本轮无单点用例"，不用"全量测试"冒充该动作行为已验。

| slice | 动作 | 实现路径 | 本轮实际已跑测试（可定位） | 可复用旧回执 | 待 Codex 现场 |
| --- | --- | --- | --- | --- | --- |
| SL-076 | nav;case-from-incident;incident-state;incident-tab;v11-incident-comment;v11-incident-task;v11-incident-claim;v11-incident-assign;investigate-create;investigation-tab;investigate-detail;ai-incident | `components/IncidentRecord.tsx`、`features/collab/IncidentCollaboration.tsx`；P4-05 专属 `features/investigation/InvestigationPanel.tsx`、`features/assistant/UnifiedInvestigationDetail.tsx`、`pages/AssistantPage.tsx` | 详情：`web/tests/unified_investigation.test.tsx` 的 `loads the detail of a job id without waiting for a disabled turn query`；面板：`web/tests/investigation_history.test.tsx` 的 `opens the unified detail of a v3 history entry of the same incident`。共享协作动作（comment/task/claim/assign）本轮**无单点用例**，仅核对调用点 | `docs/evidence/scope-history-combinations-20261109/ledger-coverage.json` | 真实浏览器中的调查运行态与共享协作动作视觉未逐项现场验收 |
| SL-077 | 同上（完成态） | 同上 | `unified_investigation.test.tsx` 的 `refreshes the steps of a running job and shows the step that appeared`（终态详情与最终消耗） | 同上 | 终态视觉与协作动作现场未验收 |
| SL-078 | close;nav;evidence;investigate-create | `UnifiedInvestigationDetail`（Drawer close/evidence）、`InvestigationPanel`（create） | `unified_investigation.test.tsx` 的 `resolves the job of a turn id and shows the public steps`（证据/步骤区渲染）；关闭动作本轮无单点用例 | `docs/evidence/scope-history-combinations-20261109/ledger-coverage.json` | 抽屉各动作视觉未逐项现场验收 |
| SL-253 | nav;case-from-incident;…;investigate-create;investigation-tab;evidence;ai-incident;q;type | `AssistantPage`（q/type 提问输入）、`UnifiedInvestigationHistory`、`InvestigationPanel` | `unified_investigation.test.tsx` 的 `lists the investigations of the conversation and opens one on request`；`unified_investigation_lifecycle.test.tsx` 的 `opens the unified detail of the same-incident history link` | `docs/evidence/p405-focus-budget-20261109/README.md` | 故障详情内提问到调查证据的端到端视觉未现场验收 |
| SL-264 | 同 SL-253 | 同上 | 同上 | 同上 | 同上 |
| SL-275 | 同 SL-253 | 同上 | 同上 | 同上 | 同上 |
| SL-303 | close;nav;copy | `UnifiedInvestigationDetail`（复制作业号 / 关闭） | 详情渲染：`unified_investigation.test.tsx` 的 `loads the detail of a job id without waiting for a disabled turn query`；**复制与关闭动作本轮无单点用例** | `docs/evidence/p405-focus-budget-20261109/README.md` | 复制后的真实剪贴板与视觉未现场验收 |
| SL-310 | close;model;steps;timeout;question;scope | `AssistantPage`（模型 Profile、工具调用上限、模型请求上限、时长收紧、提问、scope 绑定） | 本轮无单点用例（收紧与重点预算另有既有用例 `web/tests/assistant_focus_budget.test.tsx`，不作为本轮动作证据） | `docs/evidence/p405-focus-budget-20261109/README.md`（F1–F4/R0 重点与预算已验） | 模型 Profile 与收紧项在真实会话中的现场复验 |
| SL-311 | nav;…;investigate-detail;ai-incident;rca-version | `UnifiedInvestigationDetail` 的固定结论版本区（`FormalReferenceView`） | `unified_investigation.test.tsx` 的 `loads the detail of a job id without waiting for a disabled turn query`（详情渲染含固定结论区）；rca-version 字段断言本轮无单点用例 | `docs/evidence/p405-focus-budget-20261109/README.md`（固定 RCA 读取 200） | 真实会话中的固定 RCA 视觉未现场验收 |
| SL-312 | nav;…;graph-zoom;graph-reset;resource-drawer;ai-incident | 共享图与资源：`pages/TopologyPage.tsx`、`pages/ResourcesPage.tsx`、`pages/ResourceDetailPage.tsx`、`features/investigation/StepLedger.tsx` | 步骤账本渲染：`unified_investigation.test.tsx` 的 `refreshes the steps of a running job and shows the step that appeared`；图缩放/重置与资源抽屉本轮无单点用例 | `docs/evidence/scope-history-combinations-20261109/ledger-coverage.json` | 关联关系图缩放/重置与资源抽屉的现场视觉未逐项验收 |
| SL-313 | close;nav;investigation-stop | `AssistantPage.openStopConfirm/confirmStop` + `InvestigationStopConfirm` | `unified_investigation_lifecycle.test.tsx` 的 `sends exactly one stop request for the frozen confirmation`、`stops exactly the turn of the opened detail`、`refuses to stop a detail that belongs to another conversation`、`keeps the new confirmation busy when the older answer settles late` | `docs/evidence/p405-focus-budget-20261109/README.md` | 真实浏览器中的停止确认交互未现场复验 |
| SL-314 | close;nav;copy | `UnifiedInvestigationDetail` 证据区复制与关闭 | 同 SL-303（复制与关闭本轮无单点用例） | 同 SL-303 | 同 SL-303 |
| SL-400 | close;model;steps;timeout;question;scope | 同 SL-310 | 本轮无单点用例 | 同 SL-310 | 同 SL-310 |
| SL-401 | nav;…;investigate-detail;ai-incident | 同 SL-076/SL-253 | 同 SL-076/SL-253 | 同上 | 同上 |
| SL-402 | nav;…;graph-zoom;graph-reset;resource-drawer;ai-incident | 同 SL-312 | 同 SL-312 | 同 SL-312 | 同 SL-312 |
| SL-403 | nav;…;investigate-tab;ai-incident（调查记录） | `InvestigationPanel` 同一故障历史 + `UnifiedInvestigationHistory` | `investigation_history.test.tsx` 的 `opens the unified detail of a v3 history entry of the same incident`、`keeps the legacy result view for a v2 history entry of the same incident`；分页保留由 `routes new incident investigations to the bound assistant and retains legacy history without admitting a v2 job` 覆盖 | `docs/evidence/p405-focus-budget-20261109/README.md` | 调查记录在真实故障页的跨会话跳转未现场复验 |
| SL-407 | close;model;steps;timeout;question;scope | 同 SL-310 | 本轮无单点用例 | 同 SL-310 | 同 SL-310 |
| SL-408 | nav;…;investigate-detail;ai-incident | 同 SL-253 | 同 SL-253 | 同上 | 同上 |
| SL-409 | nav;…;graph-zoom;graph-reset;resource-drawer;ai-incident | 同 SL-312 | 同 SL-312 | 同 SL-312 | 同 SL-312 |
| SL-410 | nav;…;investigate-tab;ai-incident（调查记录） | 同 SL-403 | 同 SL-403 | 同 SL-403 | 同 SL-403 |
| SL-411 | close;nav;copy | 同 SL-303 | 同 SL-303（复制与关闭本轮无单点用例） | 同 SL-303 | 同 SL-303 |
| SL-428 | close;model;steps;timeout;question;scope（提交校验） | `AssistantPage` 提交前校验与收紧 | 本轮无单点用例 | 同 SL-310 | 同 SL-310 |
| SL-429 | close;nav;ai-resource | 共享资源抽屉 `pages/ResourceDetailPage.tsx` | 本轮无单点用例 | 同 SL-312 | 同 SL-312 |
| SL-430 | close;confirm-yes（停止调查确认窗口） | `InvestigationStopConfirm`（确认停止 / 继续运行） | `unified_investigation.test.tsx` 的 `opens the stop confirmation without sending any request`、`cancels nothing when the confirmation is dismissed`；`unified_investigation_lifecycle.test.tsx` 的 `sends exactly one stop request for the frozen confirmation`、`reports a conflict of the stop request without retrying it` | `docs/evidence/p405-focus-budget-20261109/README.md` | 真实浏览器确认窗口未现场复验 |
| SL-454 | close;nav;ai-resource | 同 SL-429 | 本轮无单点用例 | 同 SL-312 | 同 SL-312 |
| SL-455 | close;nav;ai-resource | 同 SL-429 | 本轮无单点用例 | 同 SL-312 | 同 SL-312 |

说明：
- 本表按动作粒度登记实现路径与**可定位的**实际跑过的测试，不把未测动作标为"已共享即通过"，也不以"全量测试"冒充某动作行为已验。共享导航/协作/图/资源动作沿用其他切片的既有实现并核对了实际调用点，本轮未另写一套。
- 可复用旧回执只表示该动作在旧证据中已有真实记录，不表示本轮重新执行；旧 9 executions / 7 attempts 与回退 unknown 证据直接复用，本轮未重跑。
- W1–W5 的原始 26 动作清单来自 `acceptance.csv` 的 P4-05 切片；本轮新增的用例未改变该清单的动作粒度，只把"已跑"一列换成真实用例名。

## 5. 真实未验证与剩余项

- 本轮未部署、未改动任何当前部署、账号、角色、来源或环境参数；公开工作台尚未被接受。
- 真实浏览器中的视觉与交互（停止确认窗口、抽屉、关系图缩放、复制）、真实 Worker 执行与真实会话回归仍待 Codex 现场验收。
- 上表标注"本轮无单点用例"的动作，其行为未被本轮单点断言，只能靠既有共享用例与人工现场验收，不能计入本轮已验。
- W5 的原生 PG 正反例已在远程 QA 目录取得 PASS 日志（`p405-r3-public-projection.log`、`p405-r3-regression.log`）；受审计测试库按规则保留，不删除，不访问 ops_current。
- 26 动作清单的"整片六维"与 visual 状态维持原判，不因本轮返修而提升；P4-05 / P5-01 / P5-02 / P6-01 仍整体未完成。

## Codex 独立现场审核：公开步骤仍需返修（2026-11-10）

相关 Web 29例、Go包、独立原生 PG（公开投影/分页/取消/撤权/禁用/版本失效以及取消预算保留和原生法律保留）通过，生成/TypeScript/Vite/Linux构建通过；193个模型阶段均为MiniMax-M3.1-Flash。代码已提交b3ebe1f并作为独立环境候选运行，正式current仍d7789c5，尚未record或宣称P4闭环通过。schema75，备份/mnt/ops-sdb/ops-current/backups/20261110T031835Z。

现场通过：旧故障历史主条目精确跳到原会话/轮次/Job且没有POST；作业号在浏览器授予剪贴板权限后复制内容正确；归档200，RCA1仍为原d131fc89…摘要。真实调查01a1c37c-af8a-719c-8873-afad97051c7b取消确认窗口0停止请求，确认1停止请求，终态cancelled并保留get_assistant_context步骤。另一真实Worker调查01a1c37c-e8bb-718d-ab08-f951b9785cf9步骤0→1→4→6→7，实际额度更新，最终partial/BUDGET_EXHAUSTED，非根因确认或模型完整性通过。终态轮询停止，手动GET单独排除。

阻断：该真实非空步骤中的result.data.binding.grantSnapshot仍从GET steps直接返回。当前PG用例步列表为空，不能证明实际非空内容无内部绑定；需要步骤公开投影及非空fixture证明，不能以已过的空列表测试关闭。RCA结果自己的inputDigest是固定版本合法溯源，不能按同名键全局删除。SSE步骤事件只含结果摘要，不含raw result，本轮不扩展重构。原始现场失败证据保留，公开证据中的内部grantSnapshot值已替换为标记，完整原件仅在任务私有目录。公开工作台整体仍未验收，四项任务与26片其余动作/HTML视觉仍未完成。

## 第三轮返修：非空原生步骤公开投影的 PG 正例（2026-11-10，MiniMax-M3.1-Flash）

### 6.1 本轮起点与上轮失败回执（原样保留，不删除）

- 上一轮 MiniMax 执行在 60 分钟上限处退出（`timeout60min exit6`），**没有产出最终产品报告**；但工作区实际落盘 4 个文件（`internal/httpapi/investigation_handlers.go`、`internal/httpapi/investigation_public_step_view.go`、`internal/httpapi/investigation_public_step_view_test.go`、`test/integration/native_public_step_projection_test.go`），并非"零改动"。
- Codex 独立复核结论（`~/.codex/minimax-dev/3567dabe-36f8-41bb-8ac7-acacc8d71e5f/codex-review.json`，状态 `changes_required`）：`go test ./internal/httpapi/... ./internal/investigation/... -count=1` 通过、Linux integration 编译通过、真实 PG 上 `TestNativePublicStepProjectionHidesAssistantContext` **失败**，`native_public_step_projection_test.go:106` 处 `CompleteToolStep` 返回 `BUDGET_EXHAUSTED`；同批 `TestNativePublicInvestigationProjectionBoundary`（撤权/禁用/授权版本失效）回归通过。独立日志 `independent-nonempty-step-pg.log` 保留在原处。
- 公开投影的实现本身（`internal/httpapi/investigation_public_step_view.go`）经独立复核判定为**保留**：只改公开读取的应答，不改写存储，不改模型上下文。

### 6.2 本轮修复（只动测试 fixture，不动产品预算与公开投影）

- 失败根因：结算会把预算回写进 tool-response 信封（`internal/investigation/repository.go` `settleTx` 的信封规范化循环），规范化后的输出比交给结算的原始信封更长；fixture 只按 `len(result)` 预留 `ResultBytes`，于是规范化输出超出预留，结算以 `BUDGET_EXHAUSTED` 拒绝。
- 修法：按**产品自身对该工具的合法上限**预留——MCP 网关对 `get_assistant_context`（v3）的预留是一次工具调用、1 MiB 结果字节、100 条证据（`internal/investigation/mcp/server.go` `toolReservation`，其边界由 `internal/investigation/mcp/server_test.go` 的 `TestAssistantContextReservationKeepsCommittedHistory` 锁定）；结算用量按网关同一口径保守计费（输出信封同样占字节）。**没有**上调产品预算，**没有**跳过结算校验，**没有**放宽断言。
- 诚实性更正（按 Codex 要求）：删除了 fixture 里伪造的 `sourceCapabilities` / `analysisCatalog`（真实助手输入里并不存在这两个键，此前把它们写成了"来源资格证明"，属于不实描述）；payload 现在**原样使用**真实 `GetAssistantInput` 返回的冻结输入。测试头注释明确写清：真实的是原生账号/会话、Worker 作业、签名上下文、真实冻结输入与真实落库的步骤行；**由 fixture 手工组装**的是 tool-response/v3 信封——本用例**不是**一次真实 Semantic API 工具调用（没有 MCP 网关调用、没有来源运行时、没有模型提供方参与）。真实 Worker 现场调用仍属部署验收项。
- 新增断言：公开读取后**重新读取存储行并逐字节比对**，证明公开投影没有改写原始 stored bytes；公开读取不新建作业（作业行数仍为 1）。

### 6.3 本轮实际执行的检查（真实命令与结果）

| 命令 | 结果 |
| --- | --- |
| `go test ./internal/httpapi/... ./internal/investigation/... -count=1` | 通过（httpapi 2.812s、investigation 1.808s、investigation/mcp 1.162s、investigation/tools 2.578s） |
| `go vet ./test/integration/` | 通过（无输出） |
| `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c -o /tmp/minimax-public-step-projection.test ./test/integration` | 编译通过（linux/amd64 测试二进制） |
| 远程 QA 实跑（`mssc-direct`，DSN 仅由 `private/dsn` 读入 `SP03_TEST_DATABASE_URL`，未打印；cwd `repo/test/integration`，migrations 1..75 已在） | **7 个用例全部 PASS**，见 6.4 |

### 6.4 远程原生 PG 实跑结果（2026-11-10，本轮）

运行器 `run-minimax-step2.py`，二进制 `minimax-public-step-projection.test`，日志 `minimax-public-step-regression.log`（均在 `mssc-direct:/mnt/ops-sdb/ops-p502-qa-20261109`）：

```
--- PASS: TestAssistantCancellationAndBudgetKeepCommittedNumbers (1.79s)
--- PASS: TestNativeActionCancelAndUnknownRecovery (0.66s)
--- PASS: TestNativeAssistantContextRefusesRevocationAndCancel (0.71s)
--- PASS: TestNativeAdminAssistantLegalHoldPersistsAndAudits (0.58s)
--- PASS: TestNativeAssistantLegalHoldKeepsTheRuntimeRoleFenced (0.59s)
--- PASS: TestNativePublicInvestigationProjectionBoundary (1.12s)
--- PASS: TestNativePublicStepProjectionHidesAssistantContext (0.63s)
PASS
```

- 本轮新增的 `TestNativePublicStepProjectionHidesAssistantContext` 由 6.1 的 `BUDGET_EXHAUSTED` **失败转为 PASS**（同一远程真实 PostgreSQL、同一运行角色、同一 native 会话上下文）。
- 该用例的真实内容：真实原生账号 + 会话 + `troubleshoot` 故障会话产出的 `investigation-job/v3`、真实签名 Worker 上下文、真实 `GetAssistantInput` 冻结输入、真实落库步骤行；公开 GET 断言步骤身份/状态/预算/公开子集保留，且 `grantSnapshot`、`executionScope`、`effectiveBudget`、`historyManifestDigest` 等内部执行上下文不出现。
- 受审计的测试库按既有规则保留（`reason=audit.records`，不做强制清理），未访问 ops_current / 业务库 / 部署 / 账号 / 环境。

### 6.5 本轮仍未验证与剩余项

- **未部署**：本轮没有部署新的 API/Web 候选，远程仍是 b3 候选、正式 current 仍 `d7789c5`、schema 75；本轮修复只在工作区，未提交。
- **真实 Worker 现场未做**：本轮 PG 用例是"真实作业 + 真实冻结输入 + 手工组装信封"，不是一次真实 Semantic API 工具调用；`01a1c37c-e8bb-718d-ab08-f951b9785cf9` 这类真实非空步骤需要 Codex 复用 b3 已有作业再读一次公开 steps，确认公开子集在真实工具输出上同样成立。
- 公开工作台整体仍未验收；P4-05 / P5-01 / P5-02 / P6-01 范围不变，26 动作清单其余动作、共享输出与 HTML 视觉仍未完成，本轮不宣称任何整项完成。
- 上一轮的 60 分钟超时（exit6、无最终报告）与本次 Codex 独立复核的 `BUDGET_EXHAUSTED` 失败回执均原样保留，未删除或改写。

## Codex 最终步骤现场复验（2026-11-10）

代码2dee0f2358b384c5681f7d443e8a170e7e943cd0，独立Go相关包与真实PG非空步骤正例/公开历史拒绝/取消预算保留通过，日志independent-nonempty-steps-pass.log。CLI本次51阶段Flash、运行成功，但最终JSON之前有一句说明导致桥接解析失败；没有追认为原始ready报告，而是独立核对实际代码与结果后接受本子链路。

部署后以原native操作员读取原真实Worker作业01a1c37c-e8bb-718d-ab08-f951b9785cf9：GET steps200仍为7条，get_assistant_context公开data只含focus/requestedAt/schemaVersion，grantSnapshot、message/history均缺失；get_formal_references的合法RCA inputDigest仍为fd604aa7…d93e9，步骤状态、实际消耗与部分结果均可见。仅复用旧作业读取，没有重跑原执行或产生新调查。备份20261110T043811Z，schema75；API更新，Web复用b3内容但挂载至当前候选源码目录，Worker c2/调查器4c复用。

本次只接受公开工作台垂直链路，receipt见acceptance.json；P4-05的26片共享动作、P5-01与P5-02共享输出、P6-01以及HTML视觉仍在原范围内接续，不声明四项整体完成。原失败回执均保留。
