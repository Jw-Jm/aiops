
## 已确认为 MR-018：资源指标判断的来源 ID 被 QueryResult ID 覆盖

EvaluateCommittedFaultChainTx 的 execute_analysis_query 分支内层 for _, id := range data.QueryResultRefs 覆盖外层 step ID，然后把 id 传给 AssessResourceQuery 的 step 参数；该函数把它写入 Supports/Conflicts。前端 ResourceCorrelation 把 supports 当 Step ID 请求 getAssistantResourceSourceStep，仓库只匹配真实 steps.step_id 及 evaluator sourceSteps，必然拒绝。FactRefs 自身仍正确，来源列表仍可手工定位。已通过变量绑定及调用不变量核对列入正式问题，详见 findings.md；未运行真实数据库。

## 待定：DeepFlow 汇总外层预算小于内部已设计的完整身份围栏预算

analytics.Network.Execute 硬设 3s，MCP execute_analysis_query 的 DeadlineFor 默认也为 3s；SummaryBatch 设计 8s，前后 pair 围栏各 3.5s。内部注释记录负载下完整围栏超过 3s，但外层令内部 allowance 无法使用。静态审查已核对各层时限；内部 allowance 较大本身不足以证明实际业务失败，不把 source 注释误当本轮性能测试。本项列入后续实际来源时延验证，未升级为已确认缺陷，也没有为它修改超时或容量标准。
