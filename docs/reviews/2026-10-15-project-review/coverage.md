# 人工阅读与验证范围

本台账说明本次实际接触的源码，不声称完成全量逐行或逐函数覆盖。机器列出的文件、搜索命中的行、通过测试的包，均不自动视为人工完整阅读。没有为本次报告编造阅读覆盖百分比。

## 已阅读全文、核对相关调用上下文的文件

以下按模块列出；单个文件的完整阅读不代表该模块所有调用方、错误分支均经运行验证。

- 入口/基础设施：`cmd/platform-api/main.go`、`cmd/platform-worker/main.go`、`internal/app/bootstrap.go`、`internal/app/api_runtime.go`、`internal/persistence/tenant_tx.go`、`Makefile`、`go.mod`、`web/package.json`、`services/investigator/pyproject.toml`。
- 身份与 HTTP：`internal/auth/oidc.go`、`authorize.go`、`context.go`；`internal/httpapi/server.go`、`foundation.go`、`session.go`、`sp04_idempotency.go`、`legal_hold_handlers.go`、`overview.go`、`sp05_list.go`。
- 人工执行：`internal/action/service.go`、`runner.go`、`runner_protocol.go`、`reconciler.go`、`output.go`、`format.go`、`credentials.go`、`ansible_executor.go`、`process_unix.go`。
- 助手/调查：`internal/assistant/resource_context.go`、`time_intent.go`、`history.go`、`retention.go`、`fault_chain.go`、`rebind.go`；`internal/investigation/dispatcher.go`、`recovery.go`、`lifecycle.go`、`allocation.go`、`assistant_budget.go`、`fault_chain_recovery.go`、`fault_chain_changes.go`、`fault_chain_resources.go`。
- 受控分析：`internal/analytics/catalog.go`、`compiler.go`、`executor.go`、`controlplane.go`、`statistics.go`、`victoria.go`、`trend.go`、`comparison.go`、`log_trend.go`、`log_samples.go`、`network.go`、`capabilities.go`；`api/analytics/catalog-v1.json`。
- 采集/资源：`internal/app/sp04_collector.go`、`sp04_archive_queue.go`；`internal/resourcestore/repository.go`、`snapshot_scope.go`；`internal/resource/resolver.go`、`canonical_id.go`；`internal/integrations/kubernetes/projector.go`；`internal/integrations/deepflow/summary.go`。
- 故障、RCA 与证据：`internal/finding/domain.go`、`ingest.go`；`internal/incident/correlator.go`、`state_machine.go`；`internal/rca/input_digest.go`、`repository.go`；`internal/evidence/domain.go`、`recovery.go`；`internal/graph/lease.go`、`lease_recovery.go`。
- 配置/物料：`internal/configregistry/service.go`、`internal/modelprofile/profile.go`；`internal/bundle/verify.go`、`safe_extract.go`；`scripts/prepare-migration-source.py`、`prepare-runtime-source.py`；`deploy/charts/ops-platform/templates/deployments.yaml`、`sp08.yaml`；`migrations/00042_assistant_retention.sql`；`build/images/web/nginx-global.conf`。
- Python 服务：`services/investigator/src/investigator/worker.py`、`budget.py`、`model_profile.py`、`holmes_adapter.py`。
- 前端：`web/src/auth/AuthProvider.tsx`、`oidc.ts`；`web/src/api/client.ts`、`sse-reader.ts`；`web/src/features/assistant/useConversationStream.ts`、`FaultChainCollector.tsx`、`BindingEditor.tsx`；`web/src/features/investigation/useInvestigationStream.ts`；`web/src/features/action/output.ts`、`ExecutionOutput.tsx`。
- 为验证合同而阅读的既有测试：`internal/modelprofile/profile_test.go`、`internal/analytics/controlplane_test.go`、`comparison_test.go`、`extensions_test.go`、`internal/resource/resolver_test.go`、`internal/investigation/tools/assistant_response_test.go`、`services/investigator/tests/test_model_profile.py`、`scripts/deepflow-dev/test_multicaller_probe.py`。

同一行中省略目录的文件沿用该组最近明确给出的目录；机器清单中的 `full` 标签只应用到上述具体文件。

## 仅部分正文或搜索上下文已核对

- `internal/httpapi/idempotency.go`：核心 Wrap、请求摘要和响应捕获区间，主要为第 65～270 行；回放 header 等末尾实现未完整阅读。
- `internal/persistence/idempotency.go`：Begin 与 Complete 等关键预留/回放区间；末尾辅助函数未完整阅读。
- `internal/auth/stepup.go`、`workload.go`、`internal/evidence/archive.go`、`retention.go`：已读主要授权/归档/清理逻辑，早期批量输出有截断，因此不登记为完整逐行阅读。
- `internal/assistant/repository.go`：第 1～245 行附近的创建、授权和读取；其余部分仅交叉搜索。
- `internal/httpapi/assistant_handlers.go`：第 1～260 行附近的入口、能力、创建及分页，后续 handler 未完整阅读。
- `internal/httpapi/assistant_tools.go`、`investigation_tools.go`、`investigation_handlers.go`：已读工具范围/分析查询实现和第 450～510 行模型准入入口等；未将整个 HTTP 模块登记为完整。
- `internal/investigation/mcp/server.go`：第 85～205 行调用准入、预算与结算；`assistant_v3.go` 的任务合同、授权及 Job 创建区间；`fault_chain_context.go` 的读取引用和相关范围检查通过搜索核对。
- `internal/app/sp06_runtime.go`：模型调用授权、预算配置与工具装配；`sp07_postcheck.go`：第 1～150 行附近。未完整阅读各专项 runtime 的所有初始化与维护循环。
- `internal/integrations/kubernetes/informer.go`：第 1～220 行及 LastListCompletedAt/Run 搜索上下文；后续流式 watch 状态机未完整审阅。
- `internal/integrations/deepflow/live.go`：第 1～165 行附近及相关身份引用；后续原生/backend 刷新实现未完整阅读。
- `web/src/pages/AssistantPage.tsx`：会话/能力/query key、create/send、调查窗口、范围编辑与展示等正文；大文件输出被截断的部分未记入完整阅读。
- `web/src/styles.css`：第 1～200 行；没有实际浏览器布局观察。
- `services/investigator/tests/test_fault_chain_unittest.py`：前 75 行夹具及预算替身，整个文件通过现有 unittest 执行，但不记完整人工阅读。
- `test/integration/assistant_fault_chain_test.go`、`assistant_statistics_test.go`、`assistant_scope_retention_test.go`：按具体场景搜索断言和夹具，不把外部提交或历史测试结果当成当前运行验收。
- 其他迁移、生成合同、模型 Schema、Graph/Source 查询消费端与 Chart Service：以具体问题的搜索命中为限，保留“部分核对”状态。

## 盘点但没有深入阅读全文的区域

- `cmd/opsctl` 的全部子命令、初始化/清理/冷装全过程；多数 bundle driver、bootstrap 和供应链实现。
- 多数资源图谱规则、Inspection、硬件、告警来源、配方实现，以及前端资源/故障/RCA/管理页面。
- 所有生成客户端/SQL 绑定的逐行实现，锁定第三方源码的逐行实现，完整物料二进制和历史证据归档。
- 所有测试夹具、全部集成/E2E/安全测试和真实环境前提。单元包通过不意味着这些类别都通过。

## 需求、延期与证据处理

- 完整读取仓库开发边界、ADR-0008、README，以及 ADR-0002/0003/0032/0037/0038/0039/0040/0041。
- 根目录正式文档按预算、租户、资源身份、图恢复、证据保留、阶段验收等相关章节检索并阅读；没有修改它们。
- 未跟踪的审查提示词仅作为项目上下文阅读，没有把它当作用户明确要求逐函数全量审计的独立授权。
- 不执行虚拟化、不连接实际环境、不重跑 SP-08 全业务；未验证的运行属性如实留空。

## 验证材料如何解读

`_probes/review_test.go` 是本次缺陷行为的最小取证，使用真实公开函数、内存 Transport 和记录调用的事务替身；它不模拟完整 PostgreSQL、Kubernetes 或 OIDC 服务。探针通过只确认相应局部机制。跨进程授权、SQL 并发与真实来源是否满足合同仍需适用隔离环境验证。
