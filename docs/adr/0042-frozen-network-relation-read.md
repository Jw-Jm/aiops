# ADR-0042：收集轮显式冻结网络关系读取

日期：2026-10-17。状态：Accepted（用户要求继续完成；本轮选择方案②并保留既有授权范围）。

## 决定

复用现有 MCP `query_deepflow` 的 `get dependencies` 操作，在同一 Job 的独立 Step 中读取冻结目标 Pod、故障窗口内的通信观测。服务器从不可变轮次输入核对目标和窗口；范围只在会话已选择且当前授权仍有效的资源/namespace 内进一步缩小到目标 namespace，不借用 worker 权限，不清空已有资源限制。

关系仍由 `deepflow.NetworkRelations` 统一投影。HTTP 传输后的 `factSlice` 与 worker 内私有 `Data` 使用同一投影函数；`peerScopeLimited` 不投影对端。用于规划的每对有向关系保留最新原始观测时间，不将明细行数解释为总体统计。

收集器合并该 Step 的已提交关系与既有原生依赖，选择一跳 Pod 对，再调用原 analytics 聚合。Evaluator、答案引用、历史重读与页面来源定位共享原 Step/Evidence 授权链。历史窗口关系不依赖当前图 300 秒覆盖，重读不刷新其 TTL，不改变因果、时间资格或 R3/R4/R5 判据。

不新增 namespace 周期采集、后台投影器、工具、队列、来源客户端或模型运行时。权限不足、空观测、来源不可用、预算不足仍保留具体缺口，不自动扩大范围。

## 运行根因更正

r16 留存库的 Job `resources=[]`，两次 `query_deepflow` 的 warning 都为 `no_evidence`。因此该次 `edges=0` 不足以证明由 `peerScopeLimited` 导致；最近 120 秒查询与冻结历史故障窗口错位是已核对的问题。单资源授权下隐藏对端的保护仍正确，不能通过移除它解决计划缺失。

## 兼容与回滚

工具名称、输入合同和任务版本保持原有形式；来源步骤合同增加 `query_deepflow`，旧结果继续可读。API、Go Worker、Python 与 UI 按同一制品升级；回滚停止新收集，保留本轮 Step/Fact/QueryResult/Evidence 和只读历史，不重算或删除已提交结果。虚拟化延期、Trace 禁用和非生产范围继续有效。

## 模型上下文（2026-10-17 用户追加裁决）

用户明确要求后续使用 DeepSeek，关键证据不为 16K 窗口做有损压缩。DeepSeek 官方模型说明（https://api-docs.deepseek.com/quick_start/pricing/，2026-10-17 核对）标明 deepseek-flash 为 1M 上下文。外部验收 Profile 固定 `contextWindow=1000000`，初版输出为 2048；实际四次 `finish_reason=length` 与 2048 token 证明输出截断后，最终改为合同已允许的 8192，deadline 仍为 120s，关闭 hidden thinking；本轮不改生产外网/付费准入。

平台合同、Python 与模型分配入口允许的最大值同步为 1000000；这是可配置上限，不把所有模型设成 1M。每次调用从不可变 Profile 取得实际窗口，包含输入、工具/schema 和输出余量。内置模型独立使用其已验证配置，128K 模型不得声明 1M。既有 16K fixture 和 legacy v2 每次调用的界限保持不变。

总输入预算改为最多 8 次调用 × 1000000；每次预留本模型窗口，已知用量按实际 token 结算，未知用量保守消耗预留量。显式策略仍可降低预算；工具/字节/时间/权限限制不扩大；整轮输出支持 8 × 8192，单次仍受各 Profile 限制。更大总预算不改变单次窗口，也不要求每次填满。

撤掉为 16K 新增的历史 Fact 引用与关系裁剪，保留原有语义投影中的来源质量、冻结值、事实关联、冲突和缺口。模型窗口不足应报告具体缺口，不通过扩大来源授权解决。1M 是配置和当前闭环验收范围，不代表已完成百万 token 容量或长上下文质量评测。


## 完整三窗口追问实测补修

最终三窗口/8K 解释实际完成后，追问暴露 get_assistant_context 原有 64KiB
封套把完整历史替换为 truncated，外层 Step 仍为 succeeded、内部 wire 为 partial，
导致当前问题和冻结请求不再可读。仅此已提交会话上下文的封套提高到 1MiB，
所有原始来源工具仍为 64KiB，全局结果 40MiB 不变；超限上下文在模型分配前
明确 MODEL_CONTEXT_LIMIT。此为传输已授权历史，不扩大来源捕获范围。

Holmes 并行调用同步工具时，共用的 asyncio.Runner 也实际发生 loop already running。
用标准 Lock 串行保护原 Runner 的 catalog/call/close，不创建替代客户端、线程运行时
或工具循环。新增回归均先失败后通过，40 项 Python 和相关 Go 检查通过。
