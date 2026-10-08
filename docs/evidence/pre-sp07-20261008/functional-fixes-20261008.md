# 清理后的功能修复交付

状态：**本次功能修复源码及必要回归完成，独立最终完整功能审核 PASS。** Ollama 实模型验收用户豁免，不记 PASS；完整 R0～R6 未完成。

用户最新边界：清理完成后，仅完成功能类问题修复。此前清理结果见 [清理记录](environment-cleanup-20261008-r270.md)。完整 R0～R6 仍未完成，此文不替代全量前置验收。

## 确认并修复的缺陷

1. **PRE-R3-METRIC-001 / P2**：正在删除的 Node，以及缺少创建时间或错误携带 namespace 的 Node，会被 NodeMetrics 接受并生成恢复信号；Pod 指标也能关联到正在删除的 Node。修复原生 Node 重读及两条 Inspector 投影，拒绝不可证明当前身份的观测。正式 90% 阈值、5 分钟时效和预算均未改变。
2. **PRE-FUNC-ACTION-TARGET-001 / P2**：同租户、同 scope、与当前 RCA 输入无关的已删除历史资源会被 ActionPlan 最终 Validator 接受。目标查询加入 `deleted_at IS NULL`。使用现有 API 只读库存权限，不引入执行能力；当前有效目标仍可形成建议。

角色状态 API 和 Evidence retention 的既有修复也纳入本次功能审核，当前原生证明分别见 r217、r222；没有重新宣称 Bundle 或冷装通过。

## 复现与验证

- [Metrics RED](functional-node-lifecycle-red-r271.json)：退出 1，保留原始错误恢复信号与错误 Pod 关联。
- [受影响完整包 race 回归](functional-node-lifecycle-green-r272.json)：退出 0。
- [当前真实 Node/Pod Metrics](functional-current-metrics-command-r273.json)：退出 0，原生固定 GET 与当前新源码 Inspector；不冒充完整 Source/Evidence/MCP 验收。
- [ActionPlan RED](functional-action-target-red-r277.json)：退出 1，实际生产 Go Validator、PostgreSQL、Transit、S3、原生 Lease 和 mTLS Graph；事实输入明确为协议 golden Fixture。
- [正式 API 数据库身份 GREEN](functional-action-target-green-r279.json)：退出 0，已删除目标/跨租户/伪造候选/执行句柄拒绝，当前目标通过，确定性 RCA 未被模型修改。
- [工具链、生成物、源码闭包、安全、复用 replay 和相关包 race 回归](functional-regressions-r274.json)：六项退出均为 0；replay 零跳过。该批检查不声称整体源码在批次内不可变，ActionPlan 修复随后又进入最终完整检查。
- [最终源码 make check](functional-current-check-r280.json)：退出 **2**，唯一失败为单个真实 Ollama MODEL_TIMEOUT；十个功能源码/测试文件前后摘要一致。该失败原样保留，未改记为退出 0。
- [单个真实链串行重测](functional-single-real-model-retry-r282.json)：退出 0，tool 1 / model 2 / Audit 11 / partial，以及持久 SSE 和 API 重启续传断言通过；这是有限实际观察，不代表 RCA 准确率。
- [未到达的 Web/Contract 检查](functional-check-remainder-r284.json)：单独补跑，两项均退出 0；不把组合结果伪写为 make check 0。
- [新增 Ollama 用户豁免](ollama-user-waiver-r285.json)：用户随后明确“Ollama也豁免，我现在的开发机器性能不够”。本轮所有 Ollama 实模型验收 USER_WAIVED_NOT_PASS，不再重跑；预算、超时处理、MCP、Validator、恢复等功能正确性不豁免。
- [独立初审](functional-independent-review-initial-r276.md)：FAIL，发现并推动关闭 ActionPlan 目标缺陷。
- [第二次完整复审](functional-independent-review-r283.md)：当时 FAIL，源码缺陷已关闭，单实模型超时为必要证据缺口；之后实际重测和用户整体豁免均保留，最终裁决见 r288。
- [回归依赖恢复原停止状态](functional-regression-dependency-state-restored-r286.json)：仅恢复本次启动的四个精确归属依赖，数据/容器/存储身份保留；当前 R12 十个控制器仍 Ready。

- [最终完整独立功能审核 PASS](functional-independent-review-final-r288.md)：两项源码缺陷关闭，Ollama 证据缺口按明确用户豁免关闭。
- [最终受测功能源码身份](functional-final-source-state-r289.json)：十个文件与 r280 冻结摘要一致，git diff --check 退出0。

## 实际交付边界

分支 `pre-sp07/nonvirtual-readiness`，HEAD `cd577dfc4e9d1d53f93b5c031d9b0a303ef32dc8`；修复在保留用户修改的工作树中。当前安装 R12 不含本次新增源码修复；本次没有新 Bundle、部署、提交或推送。

DeepFlow bundled 准入/运行、全冷装、原历史恢复和下一阶段目标准备已移出当前功能修复范围，未被改记为通过。性能及全部 Ollama 实模型验收为用户豁免，不记 PASS。KubeVirt/CDI 延期 disabled/unverified，PyRCA disabled/excluded，SP-07 未开始。
