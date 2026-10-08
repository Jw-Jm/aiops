# 独立完整功能复审 r283（新增 Ollama 豁免之前）

审核者 `/root/functional_readonly_review`，未参与实现，严格只读；主代理据回传报告落盘。结论：**FAIL，仅当前功能修复范围**；两项源码缺陷已关闭，尚有一项必要功能回归证据缺口。

## 已关闭

- **PRE-FUNC-ACTION-TARGET-001 / P2**：`internal/investigation/result.go:206` 过滤 `deleted_at IS NULL`。r277 RED1 → r279 实际独立 API LOGIN 完整正反套件 GREEN0；已删除目标、跨租户、伪造候选和执行句柄拒绝，当前目标通过，RCA 不变。没有新增库存写权限、迁移或执行能力。
- **PRE-R3-METRIC-001 / P2**：原生 Node 重读、NodeMetrics、PodMetrics 拒绝终止 Node；NodeMetrics 拒绝缺创建时间及错误 namespace。r271/r272/r273 原始证据与源码一致，阈值、时效和预算未放宽。

## PRE-FUNC-EVIDENCE-002 — 当时的必要功能证据缺口

- 位置 `functional-current-check-r280.log:2727`。
- 触发：最终源码 make check 的单串行真实模型链。
- 实际结果：HolmesGPT 第一轮 `MODEL_TIMEOUT`，Job `failed/INVESTIGATOR_FAILURE`，make check 退出2。
- 影响：当时必要的单串行真实模型成功回归未通过；不能套用十并发豁免。
- 分类：必要证据缺口，没有证据归因于本次源码修复。
- 当时关闭方法：保留失败，核对模型可用性，以正式预算复跑失败用例及必要受影响检查，再交完整复审。

其余完整功能范围重新核对：bootstrap/数据库职责、角色状态、retention、DeepFlow 有界授权和动态 Graph 时效、Metrics 身份/撤权、MCP/Context/fence/nonce/预算、HolmesGPT 官方扩展点、Prompt 脱敏、Evidence/Recipe/候选校验、接管/unknown 保守结算/Step 复用/SSE/主链路保护，没有新增确认缺陷。r274 全0/replay零skip，r280 十个功能文件内容前后一致。

此报告保持当时 FAIL，不按后续结果回写。之后 r282 单串行重测退出0，用户新增整体 Ollama 豁免见 [r285](ollama-user-waiver-r285.json)；关闭裁决由最终独立复审给出。完整 R0～R6 未通过，当前 R12 未含新增修复。
