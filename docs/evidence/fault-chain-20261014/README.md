# 故障链调查增强：开发同步检查点

日期：2026-10-14。状态：开发实现已同步，专项验收尚未全部完成。依据 [ADR-0041](../../adr/0041-fault-chain-investigation-enhancement.md) 和 [开发方案](../../plans/fault-chain-investigation-enhancement.md)。本记录不能作为生产发行准入或全部 P0～P7 完成证明。

已实现固定 Querier L4/L7 聚合、analytics 合同与分母事实、冻结窗口比较、签名 evidence_only 收集、已提交 Step 的有限评估、持久化上下文与当前授权重校验，以及助手中的调查入口、窗口时间线、链路选择、支持/冲突/缺口和统计事实展示。空流量不构造反证，错误率差值以百分点事实单位表示，相关性不写成确认根因。来源能力按数据集、操作、覆盖窗口与当前来源绑定开放。

## 已有验证

- Go 内部单元测试通过；针对最新修改的 DeepFlow、analytics、investigation、HTTP API、app 复测通过。
- `make check-generated`、`make test-contract`、相关 `go vet` 通过。
- 前端构建通过；19 个测试文件、63 项测试通过。Python 固定流程 4 项回归通过。
- [http-counts.json](http-counts.json)：192.168.0.63 独立 canary 发出 1000 个完成响应、50 个 503，单个 s-p 采集点的固定 HTTP 聚合与独立计数一致，超过旧明细 limit。记录只含 D1 元数据。
- [runtime.json](runtime.json)：远程 r7 首次完整固定流程通过，取得 3 个 QueryResult、3 份 Evidence 并读回持久化上下文。模型端点不可用、密钥缺失且模型调用为零。租户/角色/PKI 初始化及 TLS/IAM 归档依赖使用明确标注的辅助 Fixture。这是首次通过时的历史快照，后续代码的最终稳定验收仍待完成。

## 尚未完成的验收与能力边界

真实浏览器已完成 OIDC PKCE 登录、会话创建、固定调查提交和持久化读取，但严格统计断言曾因临时来源读取失败而失败；最终浏览器闭环及权限撤销验收仍需重跑。失败运行和受保护数据库保留在远程任务目录，不改写为成功。

最新代码将双端身份置于同一实时验证快照，查询后再做完整双端刷新；去掉重复的第三次 native 列表读取，保持来源、UID/容器生命周期及预算检查。临时失败最多重试一次，每次重新经过既有 MCP 预算准入。一次尝试将测试 QPS 配成 30，被已有双 Worker 的 QPS≤10/burst≤25 准入拒绝，已恢复 QPS=10、burst=20；没有放宽准入。

HTTP aggregate 有真实计数证明；compare/trend/恢复场景、并发与上限性能矩阵尚未完成全部真实验收。L4 新统计实现有单元验证，真实重传/连接失败统计须补齐对应来源证明后启用。采样、去重、完整覆盖与源时间未经验证时保持具体能力缺口；不提供未经证明的 P95、重传率、丢包率或传播先后。共享资源与变更线索在缺少合格来源时只保留待补证据，不输出原因候选。

源码闭包检查的 Go 部分已通过；既有迁移来源证明对 41/42 的漂移仍导致完整检查失败，本轮没有改写旧准入摘要。模型中文回答质量与生产来源、镜像/离线物料发行准入分开验收。

## 运行与回滚

仅使用 192.168.0.63 的任务目录 `/opt/ops-assistant-dev-20261012/fault-chain-20261014` 和独立 namespace `ops-assistant-deepflow-20261013`。新 canary release 为 `fault-chain-20261014`，既有生产、core/final 和旧 canary 配置不变。

入口由 `AssistantRuntimeConfig.faultChainEnabled` 控制，默认关闭。启用前配置与实际 sourceRevision、bindingDigest、proofDigest、collectorVersion、coverage、observationPoint 匹配的 `SP04Source.analysisQualifications`；仅有静态目录成员不代表来源操作合格。窗口最长 1 小时，最多 8 边，原始查询预算沿用现有账本；比较收费 2 次，trend 按实际子查询数收费，余额不足显式保留缺口。

冻结故障、基线、恢复窗口；基线/恢复须等长且不重叠。当前通信关系读取使用可信 requestedAt 前 120 秒，与历史窗口统计分别处理，不把当前关系标为历史已验证。暂停新增调查时关闭功能入口，保留兼容的历史读取组件和不可变证据；不删除历史数据、不回退迁移、不自动处置 Incident。

KubeVirt/CDI 继续延期，Trace 继续禁用，根目录 00～10 正式文档未修改。首次固定查询的 `Count(*)`/条件聚合不受该 Querier 支持，已改为固定 `Count(row)`、`Sum(response_duration)` 与有界分组；不直接访问 ClickHouse。字段依据锁定版本 [L4 源码](https://github.com/deepflowio/deepflow/blob/e567b167453ffa99f08f26def20379b4f831e073/server/ingester/flow_log/log_data/l4_flow_log.go) 与 [L7 源码](https://github.com/deepflowio/deepflow/blob/e567b167453ffa99f08f26def20379b4f831e073/server/ingester/flow_log/log_data/l7_flow_log.go)。
