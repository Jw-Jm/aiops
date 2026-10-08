# 功能范围独立只读初审（r276）

审核者：`/root/functional_readonly_review`；本轮未参与实现。仅阅读源码、正式文档、测试及原始证据，没有运行测试、修改文件或改变环境。以下为主代理按审核者回传报告落盘。

结论：**FAIL，仅当前功能修复范围**。1 项确认必须修复的功能缺陷，无第二项确认、可达的必修缺陷。完整 R0～R6 不属于此结论且仍未完成。

## PRE-FUNC-ACTION-TARGET-001 — P2 — 真实功能缺陷

- 位置：`internal/investigation/result.go:203`（修复前）。
- 触发条件：Incident、当前 RCA、签名 Recipe 与 Evidence 有效；模型把 ActionPlan 目标指定为同租户、同 scope 的另一条 `deleted_at` 非空历史资源。目标不属于 RCA 输入，因此其删除不会触发当前 RCA 输入失效。
- 根因：目标查询未过滤 `deleted_at`；Scope.Allows 只验证 Canonical tenant/cluster/namespace/resource scope。
- 影响：Go Validator 接受并保存针对非当前 UID 的建议。这是建议有效性缺陷，不涉及 SP-07 执行授权。
- 违反要求：前置计划 §196 的目标 UID/scope 校验；正式《09 智能体与工具调用设计》§35 当前状态约束。
- 复现：既有 PostgreSQL/Transit/S3/原生 Lease/生产 Validator 集成入口增加同 scope、无关 RCA 的已删除资源，ActionPlan 指向其 Canonical ID；应 ErrDenied，原实现会通过。保留当前目标正向和跨租户反向控制。
- 关闭条件：保留 RED、修复当前目标过滤、实际 Validator 与受影响路径回归、完整独立复审。

## 已检查的完整功能范围

- Node 生命周期修复：r271 RED → r272 两个完整受影响包 race 0 → r273 真实聚合 Metrics GET、新源码 Node/Pod 投影 0。没有删除真实 Node，也没有冒充完整 Source/Evidence/API/MCP 验收。
- Bootstrap 显式身份/数据库职责、角色状态事务/step-up/幂等/scope、r217 原生接口，以及 Evidence NO KEY UPDATE 与 r222 原生 Legal Hold。
- DeepFlow 六项固定操作、组织头、整数绑定 SQL、Canonical 映射、查询前后授权、字段漂移/退化；动态 Graph 不以重复查询刷新旧边 TTL。bundled 准入/上线未通过，保持禁用。
- MCP 正式 SDK/工具目录/mTLS；Context audience/工作负载/摘要/nonce/当前授权/fence；事务预算、重放/乱序、unknown 保守结算、成功 Step 恢复。
- 锁定 HolmesGPT 官方 ToolCallingLLM/ToolExecutor/DefaultLLM 扩展点、Prompt 分层/脱敏、Ledger Evidence 引用、Go 候选/Recipe/版本/数据等级校验。
- 实际证据 r253 接管、r254 重启后历史链、r256 Metrics 403/恢复、r258 归档/API、r260/r261 UID 重建、r266 清理后持久链与有限声明一致；没有把恢复库旧 Context 拒绝冒充已证明。
- 持久 SSE 续传、单调序号、跨租户拒绝、当前角色/来源撤权；性能和十个同时真实模型调查 USER_WAIVED_NOT_PASS，必要并发及预算正确性保留。

最终复审必须使用最新源码与新回归收据。旧 r230 不绑定新源码。安装 R12 尚不含本次新修复；新 Bundle、全冷装、DeepFlow 上线、原历史恢复、SSH 准备已移出当前范围，不能记作通过。KubeVirt/CDI 延期，PyRCA 禁用，SP-07 未开始。
