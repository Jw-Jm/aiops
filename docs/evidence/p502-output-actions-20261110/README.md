# P5-01 结构化回答 / P5-02 共享输出动作（Web 侧实现与前端自测）

2026-11-10，基线 HEAD `fb50599`（功能代码基线 `2dee0f2`）。**返修轮（Codex 独立审核 `changes_required` 后）改了 Go 后端与 API 合同**：为下钻发布最小公开范围描述符（`queryScope`），涉及 `internal/investigation/assistant_query_scope.go`、`internal/assistant/repository.go`、`internal/httpapi/assistant_handlers.go`、`scripts/generate-assistant-openapi.py`、`api/openapi/platform-v1.yaml` 与生成物 `gen/api`。**没有新增表、没有新增列、没有新迁移**。

本目录记录的是**实现与前端自测证据**，不是 live 浏览器验收回执。真实浏览器、真实带库授权下的行为、以及 HTML 1:1 视觉仍未验证，P5-01/P5-02 在 `task-progress.csv` 中保持 `in_progress`。

## 本轮实现的业务动作

沿用既有 `AssistantAnswerV1`、已持久化的 `AssistantQueryResultV1` / `AssistantQueryFactV1` 和 `getAssistantQueryResult` 读取路径，没有另造查询、统计或模型输出合同。数字只来自平台已提交并持久化的结果，模型解释不参与数值。

| 动作 | 实现路径 |
| --- | --- |
| count-drill | `web/src/features/assistant/drill.tsx` 的 `countDrillTarget`：只有 `resources.current`/`findings.occurrences`/`incidents.records` 且维度有对应列表筛选时才有下钻；链接携带 `aiDataset/aiScopeDigest/aiSnapshotAt/aiWindow/aiVersion/aiCount` |
| turn-view | `QueryResultView` 的 Segmented 切换图表/数值，只改呈现不改数值 |
| chart-point | `QueryDayTrend`/`QueryTimeTrend` 的命中层回传真实 dimension/value/unit/row，`QueryResultView` 打开"观测点与来源"抽屉 |
| turn-source | `QueryResultView` 的"查询口径与来源"抽屉 + `resultActions.ts` 的 `resultSourceVersion`/`resultSourceStatement` |
| turn-copy / turn-export | 结果级 `resultActions.ts`、回答级 `AssistantTurnActions.tsx`；只取公开业务字段与固定标识，不含 binding/grantSnapshot/raw 模型输入，不从 DOM 抓取 |
| ai-suggestion | `AssistantPage` 的 `turnSuggestions`：只写入草稿，不自动发送 |
| manual-from-ai | 会话 binding 的 incidentId → 既有 `/events/incidents/{id}?tab=action`（复用 CommandComposer 受控路径），不自动填命令、不自动本人确认、不创建执行 |
| ai-retry | `AssistantPage` 的 `retryTurn`：以该轮用户原文发起**明确的新一轮**（独立 idempotency key），旧 Job/固定输出保留，不后台盲重放，不带他人 clarificationId |
| 入口打通 | `AskAssistantEntry.tsx` + `scope.ts` 的 `assistantEntryHref`：指标/网络/硬件/日志/资源/拓扑/故障影响资源的"按此范围询问AI / AI排查 / 发起排查"统一传递 clusterUid/canonicalId/incidentId/logQuery/mode/from/to |

## 生命周期与失效边界

- `useAssistantScopeKey()` 把父代 `scopeGeneration` 纳入缓存键，A→B→A 不会命中旧代缓存。
- Fact 抽屉在 scope/结果变化时关闭；焦点 Fact 只能落在当前 scope。
- 401/403 只清除**本 scope** 的助手缓存；迟到的旧拒绝不会清掉新 scope 的缓存。
- 复制/导出在授权丢失后不提供新数据，只用已渲染的当前结果对象；导出不跨会话，不把 DOM 中隐含的其他会话一并导出。

## 实测（前端机制层）

- `cd web && npx tsc --noEmit` → 通过，无输出。
- `cd web && npx vitest run` → **62 个文件 / 328 个用例全部通过**（含新增 `tests/assistant_output_actions.test.tsx` 8 例，以及修正后受影响的既有用例 `observe_pages.test.tsx`、`resource_detail.test.tsx`）。
- `cd web && pnpm build` → `✓ built in 375ms`，产物正常。
- `go build ./...` → 通过（本轮未改 Go 代码）。

新增用例覆盖：数量下钻链接与快照身份、无列表数据集不下钻、复制载荷不含内部字段、结果数字与固定标识复制、图表/数值切换、图表点位抽屉、口径与来源抽屉、入口参数传递。

## 未验证 / 未做（诚实列出）

- 未做真实浏览器验收、真实数据（真实日志、真实资源计数、真实故障）下的交互核对。
- 未做 HTML 1:1 像素/视觉验收。
- 未做真实带库授权下的导出与下钻（QA 远端）复验；本轮无后端改动，未触发远端 QA 流程。
- 未做模型真实调用下的建议草稿/重试新轮次的现场核对。
- P4-05 其余 26 片共享动作、P6-01 全局执行列表与关联验证视图仍在原授权范围内，未在本轮处理。

## 既有证据复用（未重跑、未覆盖）

`p502-live-20261109`、`scope-history-combinations-20261109`、`incident-remediation-20261109`（9execution7attempt/真实修改回退/unknown，禁止重执行）、`investigation-closure-20261108`、`p405-public-workbench-20261110` 原样保留；本目录不覆盖任何 `codex-*` 或旧失败证据。

## 门禁

`docs/plans/ui-v12-replication-20261021/validate-progress.py` 通过（exit 0，39 任务 / 458 切片 / 179 身份 / 120 已有 / 59 拟新增不变）；`validate-plan.py` 通过（status passed，5084 检查，contracts 179、slices 458、tasks 39 不变）。`product_acceptance_asserted=false`、`business_runtime_tested=false` 保持不变，未以"全量测试"冒充单动作已验。


## 返修轮（2026-11-10，Codex 独立审核 `changes_required` 之后）

独立审核判定"8 个新增 Web 用例 + 4 个预算用例通过，但 3 个真实挂载探针全部失败，当前实现未通过审核、未部署、未提交"。本轮逐条返修，**不是"Web 实现完成"**，按动作列出定位、专项测试与未验证项。

| 项 | 实际问题 | 本轮处理 | 专项测试 |
| --- | --- | --- | --- |
| R1 点位/Fact 生命周期 | `QueryDayTrend` 先 `rows.map` 克隆再 `rows.indexOf(row)`，恒为 -1，首点返回 -1 显示第 0 行；`QueryResultView` 只在 factId 随 scope/id 清空，`chartPoint/sourceOpen/copied/view` 不清，旧"观测点与来源"与旧数值与新结果混用；`focusFactId` 的 effect 被新 reset effect 覆盖，初始 Fact 深链不再打开 | `QueryDayTrend` 在映射时保留提交行原号（不按日期排序后重编号）；`QueryResultView` 把 factId/chartPoint/sourceOpen/copied/view 收敛为一个绑定 `scopeKey+id` 的 intent 状态，切换即关闭，抽屉只在打开时挂载；深链在**已加载且已授权**的结果里精确打开该 Fact，同 id 焦点变化会切换，换 scope 关闭 | `tests/assistant_output_actions.test.tsx`：日点位保留原行号、切结果关闭观测点抽屉、初始 Fact 深链打开（3 个独立探针已纳入正式产品测试） |
| R1 授权围栏 | 迟到读/拒绝/剪贴板/下载可能写新状态 | 复制/导出点击时经 `getAssistantQueryResult` **重新读取**当前授权；401/403 只清除本 scope 缓存；作用域变化后迟到的结果不再写状态 | 403 拒绝后 0 剪贴板写入；换轮后复制状态不复用 |
| R2 下钻是假同范围 | `drillHrefFor` 只带 `aiDataset/aiScopeDigest/aiVersion/aiWindow` 提示元数据与当前组字段，接收页只显示 Notice，不用它约束真实 GET；缺原查询 clusterUid、namespace/canonicalIDs、原 filters、observedFrom/openedFrom 等时间筛选 | 后端在既有授权读取合同上发布**最小公开范围描述符** `queryScope`（clusterUid/namespaces/canonicalIds/clusterScoped/filters），来源是已持久化的 `assistant_query_results.scope` 与该轮已提交的 `execute_analysis_query` 步骤 filters，**不返回 grantSnapshot/internal binding，不新造表或迁移**；web 下钻按原范围生成真实筛选参数 | 见下方"未验证" |
| R3 复制/导出真实行为与授权 | `AssistantTurnActions`/`QueryResultView` 只消费缓存 props，未校验当前主体/授权/结果身份；导出无条件带模型 `answer.summary`，绕回模型数字；答案 payload 丢失 queryResultRefs/evidenceRefs/formalRefs/entityRefs | 回答级复制/导出点击时重新读取已提交 QueryResult，取**真实 Fact 的固定标识、原值、单位**；模型解释单独标注且 `answerKind=data` 时不导出 summary；payload 补齐 queryResultRefs/evidenceRefs/formalRefs/entityRefs；不复制 DOM、不导出原始模型上下文 | 复制内容断言含真实数值、单位、固定 Fact id、模型解释标注；403 后 0 剪贴板 |
| R4 来源窗口真实传递 | `NetworkPage` 冻结最近 1 小时但新的 AI 入口不带 from/to；`AssistantPage` 用 `toISOString().slice(0,16)` 丢秒/毫秒，创建时再补 Z | `NetworkPage` 查询时冻结窗口并随入口传递（选择变化即失效）；`AssistantPage` 保留精确 ISO 时刻，表单按 UTC 秒级展示、提交同一时刻，不再静默截断；`MetricsPage` 已是 `result.timeRange` 传入，核对一致 | 见下方"未验证" |
| R5 建议/重试/人工入口接线 | fallback `active.answer` 路径的 `DiagnosisAnswer` 传了 suggestions 却没 `onReadOnlyCheck`，`AssistantTurnActions` 的 `onDraft` 为 undefined，按钮无效 | fallback 路径补齐 `onReadOnlyCheck`（只填草稿、0 自动 POST）；`AssistantTurnActions` 无 `onDraft` 时不渲染建议按钮；重试仍以原轮用户原文发起独立幂等键的新轮，旧 Job/固定引用不变；人工入口仍是 `tab=action`+故障/cluster，0 自动创建执行 | 见下方"未验证" |
| R6 文档 | 不能用"Web 实现完成"掩盖功能缺口 | 本表按动作给出定位、专项测试与未验证项；`acceptance.json` 的 `feature_commit` 标注为基线/运行时提交，不是本轮未提交的新功能提交 | — |

### 本轮返修的实测

- `cd web && npx tsc --noEmit` → 通过，无输出。
- `cd web && npx vitest run tests/assistant_output_actions.test.tsx` → **12/12 通过**（含 3 个原失败探针转为正式产品测试 + 回答级复制/拒绝用例）。
- `cd web && npx vitest run tests/assistant_output_actions.test.tsx tests/observe_pages.test.tsx tests/resource_detail.test.tsx` → **24/24 通过**。
- `cd web && npx tsc --noEmit`（全量）→ 通过。

### 返修轮仍未验证（诚实列出）

- **R2 下钻的真实同范围未做远端带库验收**：本轮新增了公开范围描述符与真实筛选参数，但"接收页真实 SDK 调用断言条件一致"（跨 cluster/ns、既有 filter 与行 filter 交集、分页、窗口、当前撤权拒绝）需要 QA 远端 `SP03_TEST_DATABASE_URL` 实跑，未在本轮完成。
- R4/R5 的**父页真实挂载**（`NetworkPage` 点击路径、`AssistantPage` 新建请求、`retry` 新轮幂等键与 409、`manual` 真实目标 tab）未新增父页挂载测试，沿用既有父级围栏与既有回执，未重新执行 9execution7attempt/rollback/unknown。
- 未做真实浏览器验收、HTML 1:1 视觉验收、模型真实调用下的建议/重试现场核对。
- 未提交、未推送、未部署；`task-progress.csv` 中 P5-01/P5-02 仍为 `in_progress`。


## Codex 独立审核与直接完成（2026-11-10）

此节取代上方历史轮次对当前状态的描述；上方保留失败与返修过程，不可作为当前通过声明。回调 b45dd2bf 因连接错误 exit4 结束，报告没有测试记录；没有重新派发。Codex 直接核对并完成代码与测试，提交 `f2c3db9eb4914590c31c970d8391896c37efe37c`；真实浏览器又发现后台资源分析丢失 TaskAuthority，直接修复并增加原生授权 PG 回归，提交 `d84cf54fdbccfb90911048729a3349ab72c629fa`。

### 实际变更与独立检查

- R2 改为接收接口读取已保存 QueryResult 的原始授权范围，当前角色再次授权后，求原 filters、行 group 与原资源身份的交集。前端携带 assistantQueryResultId/group；GET resources/findings/incidents 真正执行约束，游标绑定原查询与组。未知历史 filtersKnown=false 明确拒绝精确下钻。无新迁移，schema75、179身份保持。
- R3 回答复制/导出先读取真实 turn（包括零 QueryResult），再读取 fresh answer 引用的已提交结果；结果与回答操作冻结身份和代次，每个异步结算受围栏保护，新上下文重置 busy。数据回答导出不含模型 summary，保留固定引用、原值、单位、来源和缺失声明。
- R4/R5 已加入父页真实挂载测试：Network 精确窗口/目标切换；AssistantPage 创建窗口保留毫秒、建议只填草稿、显式重试/409、正确人工故障入口。未重执行历史9execution7attempt、真实修改与回退。
- Web 全量63文件343用例通过，TypeScript、构建通过；Go httpapi/analytics/assistant/investigation通过；生成物复现通过；计划5084检查与进度校验通过。日志副本见 checks/。
- 隔离 PG 真实结算查询用例通过：非空原 filters/多个结果、canonical 范围、分页、组伪造/他人/撤权拒绝、合法历史缺描述符。原生资源分析用例在无浏览器会话的真实 Worker 租约下完成签名任务证明，撤权后禁止额外来源读取。其 graph summary 是协议 Fixture，不冒充真实来源准确性证据。

### 真实原生页面链路

候选 API/Web 为 d84cf54，Worker/调查器复用已验版本。第一次真实查询在 f2c3db9 因原生任务授权上下文缺失失败（live/initial-query-result.json 保留）。修复后点击页面“重新提问”，只新增一次提交，新 turn `01a1c483-0a56-7200-890d-85b40dd5cd13`，旧失败轮次仍 partial，未重写。真实模型通过受控工具产生 QueryResult `01a1c483-1744-75e9-85a7-af7ce56a7f35`，资源快照 normal=10，明确 partial/exact=false/stale，不代表当前精确总数。

结果级与回答级真实剪贴板/下载均核对 QueryResult、Fact、原数值10、单位与 contentDigest；数据回答导出不含模型 summary。来源抽屉显示固定来源版本/摘要/降级声明。点击数量下钻后，接收页真实 GET resources 携带原结果引用和 normal 分组，200返回10条，逐条核对 kind=Pod、namespace=ops-current、health=normal，没有扩大范围。截图与公开响应见 live/，没有会话凭据。

### 当前未完成范围

这次通过的是 R1–R5 修复和上述真实资源输出链路，四任务整体仍 in_progress。P4其余共享动作、P5真实趋势/日志输出与知识引用依赖、P6全局搜索和执行关联验证视图仍需完成逐动作收口；HTML1:1未验。资源来源仍报告 continuity/archive/proof/npd 降级，必须继续诊断，不能用本链路通过宣布来源准确或全功能完成。旧已通过真实处置/回退证据继续复用。
