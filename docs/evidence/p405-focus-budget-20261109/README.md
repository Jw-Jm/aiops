# P4-05 统一 v3 调查入口：重点与当次准入收紧的垂直链路

范围：用户从统一入口（`web/src/pages/AssistantPage.tsx`）指定**调查重点**与**收紧额度**，
服务端按当次准入计算实际生效额度，冻结进本轮不可变输入与 Job，Worker 在该额度下真实执行，
页面回显本轮实际额度与重点。未部署、未做现场浏览器验收。

## 链路与代码坐标

| 环节 | 实现 |
| --- | --- |
| v3 合同（生成源） | `scripts/generate-assistant-openapi.py`：`AssistantSubmitMessageV1` 增加 `focus`（≤500）与 `budgetTightening`（`AssistantBudgetTighteningV1`，各维度 `minimum:0`）；`AssistantTurnV1` 增加 `focus` 与 `budget`（`AssistantBudgetV1` + `AssistantBudgetConsumedV1`）。生成物：`api/openapi/platform-v1.yaml`、`gen/api/openapi.gen.go`、`web/src/api/generated/**`（均走 `make generate`，未手改生成文件） |
| 合同共用 | `AssistantSubmitMessageV1` 同时用于 `POST .../messages` 与 `POST .../fault-chain-investigations`，两条入口同一套字段与同一套服务端规则 |
| 收紧语义 | `internal/investigation/domain.go`：`BudgetTightening` + `TightenBudget(base, request)`。0 = 该维度不请求；负值 = 非法输入（`ErrInvalid`），不静默按默认处理；沿用既有 `LowerBudget` 按维度取小，因此请求高于当次准入只会**收紧到准入值**，不会放大；超出平台最大值按既有 workbench 语义拒绝。`BudgetTightening` 自带 `UnmarshalJSON`：维度为非 nullable 整数合同，显式 null / 分数 / 字符串 / 布尔 / 未知维度一律拒绝，且在自定义解码内保留 `DisallowUnknownFields` 边界 |
| 预算类型边界 | `internal/assistant/domain.go` `SubmitMessage.UnmarshalJSON`：`budgetTightening` 整体显式 null 被拒（不悄悄变"未请求"）；未知请求字段被拒（该结构自定义解码后自行保留 `DisallowUnknownFields` 边界） |
| 服务端准入 | `internal/assistant/repository.go` `Submit`：`admission.Budget` → `TightenBudget` → 实际生效 `budget`；`focus` 经 `NormalizeFocus`（去空白、≤500 字、拒绝控制字符、`tools.RedactText` 脱敏） |
| 冻结输入 | `internal/assistant/domain.go` `TurnInput` 增加 `Focus` 与 `EffectiveBudget`（`omitempty`，旧 JSONB 行的摘要不变），并参与 `InputDigest` |
| 冻结 Job | `investigation.Job{... Budget: budget, Focus: focus, AdmissionDigest: sha256(budget,focus)}`；`internal/investigation/assistant_v3.go` `CreateAssistantJobTx` 写入既有 `jobs.focus` / `jobs.admission_digest` 列（迁移 00074 已有，无新表、无平行表） |
| Worker 实际约束 | `jobs.budget` 即收紧后的额度，`AllocateTool`（按 `j.Budget.ToolCalls`）、`AllocateModel`/`validateModelReservation`、`chargeRead` 全部按该行判定；额度耗尽由既有 `exhaustBudgetTx` 落 `partial` + `BUDGET_EXHAUSTED`，不伪造根因 |
| 调查器落点 | `services/investigator/src/investigator/assistant_binding.py` `messages()`：把 `job.focus` 作为**用户消息**追加（"user intent only; does not change the scope, the granted budget or the available tools"），不写入 system，不覆盖平台约束 |
| 幂等 | `assistant_request_keys.request_digest` 按 `SubmitMessage` 整体计算，新增字段自动入摘要：同 key 同载荷不重复 Job，异载荷 `ErrConflict` → 409 |
| 回显与恢复 | `internal/assistant/repository.go` `loadTurn` 以 `LEFT JOIN investigation.jobs` 读回 `focus` / `budget` / `budget_consumed`；`AssistantTurnV1.budget.consumed` 是已消耗量。页面从 `turn.budget` / `turn.focus` 回显，不显示请求值 |
| 页面控件 | `web/src/pages/AssistantPage.tsx`：调查重点输入 + 工具调用/模型请求/运行时长上限三个 `InputNumber`；非法值本地拒绝并提示；页面明写"留空或填 0 表示该维度不收紧，仍沿用当次准入额度（不会禁止调用）"；scope/账号/会话切换清稿（`setFocus("")` 与额度清空）；409 显示"会话已被其他操作修改，请刷新后重试"且**不**自行取新 revision 重试；401/403 显示明确原因并清缓存（既有 `client.removeQueries`） |
| 会话生命周期代次 | `web/src/pages/AssistantPage.tsx`：会话/作用域切换推进 `conversationLifecycle.epoch`（A→B→A 也会推进，旧代次不可复用）；`mutate` 的 `canCommit` 同时比较 scope、当前会话 id 与代次；提交绑定会话，离开会话即 abort 在途意图并丢弃迟到响应；提交时 revision 与幂等 key 冻结 |
| 故障链入口 | `web/src/features/assistant/FaultChainCollector.tsx` 复用同一 `focus` / `budgetTightening` 字段，走同一合同 |

## 行为测试（真实代码路径）

| 测试 | 位置 | 覆盖 |
| --- | --- | --- |
| `TestTightenBudgetNeverWidens` / `TestTightenBudgetRejectsInvalidInput` | `internal/investigation/budget_tightening_test.go` | 只能收紧不能放大、0=不请求、负值与超上限被拒 |
| `TestNormalizeFocusAcceptsUserIntentAndRejectsControlInput` | `internal/assistant/focus_budget_test.go` | 长度上限、控制字符拒绝、空重点 |
| `TestNormalizeFocusRevalidatesAfterRedaction` | `internal/assistant/focus_budget_test.go` | 脱敏**之后**再次校验长度/UTF-8/控制字符：脱敏使重点超长时明确 `ErrInvalid`，不静默截断（独立探针的 492×"查"+"token=x" 场景） |
| `TestSubmitMessageRejectsInvalidBudgetTypes` | `internal/assistant/focus_budget_test.go` | 预算对象的显式 null、维度 null、分数、字符串、布尔、未知维度、未知请求字段全部拒绝；字段省略与已定义 0 语义保留 |
| `TestAssistantInvalidFocusAndBudgetWriteNothingInPostgreSQL` | `test/integration/assistant_focus_budget_test.go` | **真实 PG**：非法重点/非法预算类型被拒后 Job、幂等账本、审计、会话 revision 全部零写入，且随后合法请求仍被接受 |
| `TestAssistantHTTPRejectsInvalidBudgetTypesInPostgreSQL` | `test/integration/assistant_focus_budget_test.go` | **真实 HTTP decode → repository**：messages 与 fault-chain-investigations 两条路由对非法预算类型返回 400，且零 Job/账本/审计/revision 写入 |
| `tests/assistant_late_response.test.tsx`（4 例） | `web/tests/` | A→B 迟到成功不清 B 草稿/重点/额度、不引入 A 轮次；A→B→A 旧代次迟到被拒；另一会话的错误不显示；403 真正断言 `removeQueries` 被调用 |
| `TestFocusAndEffectiveBudgetAreFrozenIntoTheInputDigest` | `internal/assistant/focus_budget_test.go` | 重点/实际额度进入摘要；改任一项摘要不匹配；旧轮次 JSONB 仍匹配旧摘要（回归） |
| `TestAssistantFocusAndBudgetTighteningInPostgreSQL` | `test/integration/assistant_focus_budget_test.go` | **真实 Go repository + 真实 PostgreSQL**：回显收紧额度与重点、`jobs.focus`/`jobs.admission_digest`/冻结输入、请求高于准入被收紧回准入、超上限与负值被拒、同 key 同载荷不重复、异载荷 409、Worker 第三次工具调用被拒、终态 `partial`、重载后仍回显实际额度与重点 |
| `test_focus_is_user_intent_and_never_an_authorization_override` | `services/investigator/tests/test_assistant_binding.py` | focus 只进 user 消息，不进 system，不改 trusted_context |
| `tests/assistant_focus_budget.test.tsx`（4 例） | `web/tests/` | 提交载荷含 focus 与 budgetTightening、回显服务端实际额度、409 不静默重试（只调用一次）、403 清缓存 |

## 实际运行结果

| 命令 | 结果 |
| --- | --- |
| `go build ./...` | 通过 |
| `go test ./internal/assistant/ ./internal/investigation/ ./internal/httpapi/` | 全部 ok |
| `go test ./internal/... ./cmd/...` | 见 `checks` 记录 |
| `services/investigator`: `.venv/bin/python -m pytest tests/ -q` | 109 passed |
| `web`: `npx vitest run` | 58 files / 291 tests passed（含新增 4 例） |
| `web`: `npx tsc --noEmit && npx vite build` | 通过 |
| `python3 scripts/check-generated.py "$(command -v go)" gen web/src/api/generated internal/persistence/dbgen services/investigator/src/investigator/output.schema.json services/investigator/src/investigator/assistant-output.schema.json` | 退出码 0，生成物可复现 |
| 远程隔离库 `mssc-direct:/mnt/ops-sdb/ops-p502-qa-20261109`（PG 容器 `ops-p502-qa-20261109`，标签 `ops.platform.io/task=p502-qa-20261109`，127.0.0.1:15584；DSN 仅由 `private/dsn` 读入 `SP03_TEST_DATABASE_URL`，不打印） | 新增 `TestAssistantFocusAndBudgetTighteningInPostgreSQL` PASS；既有回归 `TestAssistantConversationLifecycle`、`TestAssistantModelProfileReservationInPostgreSQL`、`TestAssistantRenamePersistsAndAudits`、`TestAssistantSearchFiltersAndCursorStayConsistent`、`TestAssistantArchiveKeepsTheFixedReferences`、`TestAssistantHistoryRetentionAndPlatformClarification`、`TestAssistantCancellationAndBudgetKeepCommittedNumbers`、`TestAssistantExplicitScopeChangesAndPhysicalTTL`、`TestAssistantFaultChainAndClarifiedFollowup`、`TestAssistantPartialReasonsFollowCommittedReads` 全部 PASS；日志 `results-p405-focus-budget.log` |

## 未做与待现场

- 未部署到任何现场环境，未做浏览器像素/交互验收，HTML 1:1 仍属后续视觉阶段。
- 未访问 `ops-current-pg` / `ops_current`，未改远程当前部署、账号或来源。
- 既有 P5-02 会话机制（搜索/改名/归档/范围/续传停止）与 9 条 execution / 7 attempts / 回退 / 未知结果
  证据直接复用，未重复实现、未新执行受控资源修改命令。
- 26 条 P4-05 acceptance 的逐项清单仍在原任务范围内，本轮完成的是"重点+额度"垂直链路这一实现节点，
  不等于 P4-05 整项完成，更不等于四项完成。

## Codex独立审核与现场（2026-11-09）

返修task148fa7d4最终CLI超时exit6，未交有效ready；但实际保存的F1-F4修复由Codex独立审核与实跑通过，139个模型阶段均为MiniMax-M3.1-Flash。已提交产品4c7c413（包含之前R0/重点预算工作）。19项真实隔离PG用例无SKIP通过；3个Web文件16例通过；受影响Python19例、Go包、构建和生成检查通过。此前实现者表格是各自阶段自测，不能替代这些独立结果。

真实命名空间operator页面提交重点与6工具/3模型/120秒，实际生效额度与重点冻结于input和Job，预算消耗从Worker读回；null对象/null值/分数/脱敏增长后超长focus均400。工具1/模型1/60秒轮次实际partial/BUDGET_EXHAUSTED，重放同Job、异载荷409。浏览器原生CDP暂停202响应，等B会话读取完成后输入B草稿与重点再释放A响应，B内容未变（live/native-late-response-isolation-2.json）。第一次立即切页输入的探针未等待B加载，草稿为空的原记录保留，不作为该项成功证据；另一次route.fetch验收代理因不信任自签名证书未能发起，不计产品通过，随后改用固定SPKI的浏览器原生通道。

首次真实复盘因归档读取超时而partial/SOURCE_DEGRADED，失败保留。定位SeaweedFS1Gi容器匿名内存约1.056GB、页缓存仅12MB、refault累计57,836,833，持续345MB/s读IO。独立开发环境ops-seaweedfs设置GOMEMLIMIT=640MiB并滚动重启，归档恢复200，采样IO约16KB/s，RSS/cgroup当前约604MB且max/oom事件0；滚动重启与预算同时变更，因此这是恢复证据，尚非长期稳定性证明。后续真实模型轮次成功读取同一旧故障RCA1及两条sourceRevision1归档，固定引用/归档200（live/final-model-and-refs.json），页面固定RCA抽屉通过。未改旧RCA/来源版本/业务命令。

当前API/Web/调查器实际运行4c7c413，Worker仍c2，schema75；正式接受指针仍179ca6a。源chart尚未带GOMEMLIMIT，下一阶段必须落回deploy/charts/ops-dependencies/templates/seaweedfs.yaml并验证；没有把带未归并运行配置的候选登记成完整接受版本。

P4-05尚需统一入口公开步骤/调查详情与历史、停止确认等功能动作收口，26条主切片尚未整体通过；P5-01/P6-01及P5-02共享输出动作继续；HTML视觉未验。v1公开GET investigation/job及steps已现场200/4step，可复用既有API；不需要开启被禁用的旧v2模型创建路径。所有原执行/回退/unknown证据继续复用，不重新修改业务资源。
