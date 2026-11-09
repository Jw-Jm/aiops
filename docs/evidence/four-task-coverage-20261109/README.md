# 四项任务覆盖矩阵（P4-05 / P5-01 / P5-02 / P6-01）

- 记录时间：2026-11-09（原记录标注 2026-09-09 有误，按实际核对日期更正；原始失败报告保留于文末“更正记录”）
- 仓库：`/Users/mssc/Documents/Code/ops/platform`，HEAD `7f4c1cb8604b70584244b9e44888f61fcd3dd086`
- 切片基线：`docs/plans/ui-v12-replication-20261021/tasks.csv`、`scope-slices.csv`（四项 primary_task 共 89 条主切片）
- 原型参考：`/Users/mssc/Documents/Code/ops/ui-handoff/ops-platform-v1.2-20261020/slices/*/SPEC.md`
- 说明：本矩阵只记录**已核对到的事实**与**明确未实现项**。原型行为不等于产品通过；HTML 1:1 像素比对属后续独立阶段，本次不得标 visual passed。

## 1. 切片规模

| 任务 | 主切片数 | 路由 |
| --- | --- | --- |
| P4-05 | 26 | `/events/incidents/*`、`/assistant` |
| P5-01 | 34 | `/assistant`、`/observe/*`、`/resources/*`、`/events/incidents/*` |
| P5-02 | 14 | `/assistant` |
| P6-01 | 15 | `/jobs/commands` |

切片内动作是复合的（`open/prefetch/submit/confirm/local_stage/local/direct/adapt/missing`），89 条主切片展开后是数百个动作点，因此**不能用“已接线”代替“已验收”**。

## 2. 已有实现与证据（只读核对，代码路径存在）

### P4-05 调查证据与预算界面
- 服务端：`internal/investigation`（`assistant_budget.go`、`assistant_answer.go`、`fault_chain_*.go`、`recovery.go`、`read_limits.go`、`resource_changes.go`、`historical_read.go`）、`internal/httpapi/investigation_handlers.go`、`investigation_history.go`、`investigation_tools.go`。
- 预算合同**已实现收紧语义**：`internal/httpapi/investigation_handlers.go:161-176` 明确 “The workbench may only tighten the server budget, never widen it”，越界请求经 `investigation.LowerBudget` 收紧/拒绝。
- 接口面：`createInvestigation`、`getInvestigation`、`getInvestigationSteps`、`cancelInvestigation`、`streamInvestigationEvents`、`listIncidentInvestigations`、`getIncidentInvestigations`、`getIncidentRCA`。
- 前端：`web/src/features/investigation/{InvestigationPanel,BudgetSummary,StepLedger,useInvestigationStream}`，页面 `web/src/pages/IncidentWorkspace.tsx`。
- 可复用真实证据：`docs/evidence/incident-remediation-20261109/`、`docs/evidence/investigation-closure-20261108/`、`docs/evidence/controlled-command-20261108/`。

### P5-01 AI 结构化返回
- 服务端：`internal/investigation/assistant_v3.go`、`assistant_answer.go`、`assistant_clarification.go`、`assistant_entities.go`、`assistant_formal.go`、`assistant_formal_history.go`；`internal/assistant/{intent,time_intent,resource_context,formal_read,references}.go`；`internal/httpapi/assistant_{handlers,resolve,intent,tools,related,source_capabilities}.go`。
- 结构化答案合同：`api/schemas/assistant-answer-v1.schema.json`（kind/factRefs/queryResultRefs/evidenceRefs/partial/degradedSources/warnings/hypotheses/nextChecks）。
- 前端：`web/src/features/assistant/{QueryResultView,DiagnosisAnswer,AssistantEvidence,FormalReferenceView,QueryDayTrend,QueryTimeTrend,ConversationContext,BindingEditor}`，`web/src/pages/AssistantPage.tsx`。
- 跨页上下文已携带：`ResourceDetailPage` 的“AI 排查”经 `href()` 带上 `clusterUid`+`canonicalId`；`AssistantPage` 默认 `mode=troubleshoot`，建会话时 `resourceCanonicalIds = boundTarget ? [boundTarget] : []`（`AssistantPage.tsx:82-87,441`），`incidentId`/`logQuery` 分支同样绑定，故“按此范围询问 AI”是真绑定而非前端假状态。
- 可复用真实证据：`docs/evidence/scope-history-combinations-20261109/`、`docs/evidence/investigation-closure-20261108/`。

### P5-02 会话历史及流恢复
- 服务端：`internal/assistant/{history,rebind,retention,references,historical,repository}.go`；`investigation.assistant_conversations` 表已有 `title/owner_subject/archived_at/revision` 列，`assistant_messages` 有 `retain_until/legal_hold` 列（`migrations/00039_assistant_conversations.sql`）。
- 接口面：`listAssistantConversations`、`getAssistantConversation`、`rebindAssistantConversation`、`archiveAssistantConversation`、`listAssistantMessages`、`getAssistantConversationEvents`（SSE，`Last-Event-ID` 续传）、`cancelAssistantTurn`、`setAssistantLegalHold`。
- 前端：`web/src/features/assistant/{useConversationStream,LegalHoldControl,BindingEditor,FormalReferenceView}`。
- 已知真实证据：命名空间正例账号 `native:01a1bdd3-8d96-7c39-9489-cf7b94bd4d9f`、最终会话 `01a1be1b-2a19-73ea-b2c4-60b099f655b1`/轮次 `01a1be1b-4ad6-70a3-be11-ee53cbbb8bb4` 已实际解释 FailedMount。

### P6-01 人工执行全链
- 服务端：`internal/action`（`runner.go`、`service.go`、`repository.go`、`plan.go`、`postcheck.go`、`postcheck_report.go`、`verification.go`、`authority.go`、`archive.go`、`native_stepup.go`、`execution_relation.go`、`reconciler.go`、`runner_protocol.go`）。
- 接口面：`createCommandExecution`、`listCommandExecutions`、`getCommandExecution`、`cancelCommandExecution`、`getCommandPostCheck`（自动验证报告）、`streamCommandExecutionEvents`、`assessCommandRisk`、`acceptActionPlan`、`dismissActionPlan`。
- 全局执行列表已存在：`web/src/pages/CommandExecutionsPage.tsx`（状态筛选 + cursor 分页 + 展开详情 + 取消 + `execution_unknown` 显式警示“暂勿重复提交”）。
- 可复用真实证据：真实故障 `01a1bdac-eaef-7730-a73a-90738956cc96`，9 条执行/7 次 attempt 已通过，人工回退 `01a1bdbe-f44e-7796-9d5b-e7ab8a625eab`，恢复摘要 `sha256:779dcecd…5e28aa`；未知结果 `01a1bdb7-d0b2-76b3-ad19-12e88272c0dc` 必须仍未知、不得重发。

## 3. 缺实现（本轮核对确认无对应代码，必须新增）

| 编号 | 任务 | 缺口 | 证据 | 涉及切片 |
| --- | --- | --- | --- | --- |
| G1 | P5-02 | **本轮已实现**：`PATCH /api/v1/assistant/conversations/{conversationId}`（`renameAssistantConversation`）+ `internal/assistant/rename.go`（所有者/授权重验、revision 围栏、幂等键）+ web 重命名弹窗 | `api/openapi/platform-v1.yaml`、`internal/assistant/rename.go`、`internal/httpapi/assistant_handlers.go`、`web/src/pages/AssistantPage.tsx` | SL-296、SL-065 |
| G2 | P5-02 | **本轮已实现**：列表新增 `q`（服务端 title 过滤）与 `archived` 参数，过滤条件绑定游标；web 搜索框走服务端检索 | `internal/assistant/repository.go`（`ConversationFilter`/`ListPage`）、`internal/httpapi/assistant_handlers.go`、`web/src/pages/AssistantPage.tsx` | SL-065（`session-search`） |
| G3 | P6-01 | **原判定有误，更正为已核实事实**：验证 Runner、结果校验、关联执行与展示已存在（`action.CommandRequest.Verification`、`internal/action/service.go:218` 验证绑定检查、`internal/action/verification.go`、`PostCheckPanel.tsx`）。不得据此新增 `verification-attempts` 表/API 或确认流程；P6-01 阶段须先复用这些既有实现 | SL-073、SL-074、SL-308、SL-427 |
| G4 | P6-01 | 执行详情缺**故障关联**与恢复检查/回退关联视图 | `CommandExecutionsPage` 详情只有 state/output/取消，无 incident 关联与恢复检查链展示 | SL-074、SL-309 |
| G5 | P4-05 | **原判定已更正**：预算收紧控件并非不存在——`InvestigationPanel.tsx` 的 `!assistantHref` 分支已有 focus/收紧控件；当前统一 v3 入口带 `assistantHref` 时该分支隐藏，v3 创建/提交未暴露收紧入口。P4-05 阶段须沿用 `LowerBudget` 约束接好 v3，不得启用被托管模型禁用的旧 v2 路径 | `web/src/features/investigation/InvestigationPanel.tsx`、`web/src/features/investigation/BudgetSummary.tsx` | SL-076、SL-077、SL-310、SL-428 |

## 4. 缺接入（后端有、页面未接或接法不完整）

| 编号 | 任务 | 内容 |
| --- | --- | --- |
| A1 | P5-01 | `getAssistantTurn`/`getAssistantQueryResult`/`getAssistantFormalReference`/`getAssistantResourceSourceStep` 已有接口与组件，但**来源版本、样本数/总量、不完整覆盖 vs 精确值、假设 vs 根因**的区分文案需在页面按证据口径逐项落实（当前 `QueryResultView`/`DiagnosisAnswer` 已有骨架，需逐字段确认）。 |
| A2 | P5-02 | 归档不级联删除引用、旧来源固定引用不得替换为 latest、越权历史不可读三条语义在后端有（`internal/assistant/retention.go`、`references.go`），页面**未提供引用状态展示**，用户无法判断“该引用按权限读取”。 |
| A3 | P6-01 | `getCommandPostCheck`（自动验证报告）接口已有；与 G3 的人工验证尝试在页面**未并列展示**，无法体现“命令成功 ≠ 故障恢复”。 |

## 5. 缺验收（已有实现但六维未现场核验）

- `docs/evidence/scope-history-combinations-20261109/ledger-coverage.json` 中四项相关主切片六维基本为 `not_verified`，只有两个组合登记子动作且**未提升整片六维**。
- `docs/evidence/incident-remediation-20261109/` 已覆盖真实修改/回退/独立恢复/未知结果，但**未覆盖 P6-01 人工验证尝试**（该能力此前不存在）。
- 浏览器闭环、真实模型、真实 Job/Worker、真实来源下的 P5-01 结构化返回与 P5-02 流恢复均需 Codex 现场验收，本地自测不能替代。

## 6. 原任务外（不属本轮，禁止顺手扩展）

- HTML 1:1 像素比对与全量视觉验收（P8-01）。
- 受控作业模板（P6-02）、知识检索与固定引用（P7-05）、KubeVirt/CDI、Trace、生产/高可用、全量发行。
- 根目录 00～10 正式文档；既有 schema74 迁移。

## 7. 建议的实现顺序（下一轮直接开工用）

1. G3 + A3（P6-01 人工验证尝试 + 与自动验证报告并列）——本轮任务书点名的重点。
2. G1 + G2（P5-02 重命名与搜索）——`title` 列已存在，改动面最小。
3. G4（G6-01 故障关联与恢复链视图）。
4. G5（P4-05 预算收紧控件与服务端上限回显）。
5. A1/A2 的页面口径补齐。

## 8. 更正记录（2026-11-09，保留原失败报告）

- 原记录时间标注为 2026-09-09，实际核对日期为 2026-11-09。
- 原 G3 断言“人工验证无后端”不成立：`action.CommandRequest.Verification`、`internal/action/service.go:218` 的验证绑定检查、`internal/action/verification.go` 与 `PostCheckPanel.tsx` 已实现验证 Runner、结果校验、关联执行与展示；后续阶段不得新增 `verification-attempts` 表/API 或确认流程。
- 原 G5 断言“所有预算控件都不存在”不成立：`InvestigationPanel.tsx` 的 `!assistantHref` 分支已有 focus/收紧控件；缺口是统一 v3 路径未暴露收紧入口，不是控件缺失。
- 原 A1/A2 “页面未接”结论不足以成立：已有 `QueryResultView`、`DiagnosisAnswer`、`FormalReferenceView` 与旧现场证据须先复用。
- G1/G2 已由本轮 P5-02 实现补齐（服务端搜索过滤、改名接口与仓储、web 搜索框与重命名弹窗、归档确认）。
