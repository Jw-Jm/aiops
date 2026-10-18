# 五来源联合闭环续验（2026-10-17）

2026-10-18 对后续独立复验结论的核对与修正版见 [独立复验结论核对](independent-reverification-review-20261018.md)：独立 r21 通过，但未执行进程故障恢复；页面历史通过与独立页面未复验分别记录。

范围为 ADR-0041 的专属 Pod canary；关系读取裁决见 [ADR-0042](../../../adr/0042-frozen-network-relation-read.md)。本地只编辑、编译和单元检查；容器、数据库、来源、模型与浏览器在同一独立验收机运行，SSH 按用户追加指示改用 `mssc-direct`。

**当前状态：最终源码完整三窗口联合复验 r22，以及同一制品的真实 OIDC 浏览器收集、模型解释、追问和授权隔离均通过。** 所有失败和先前的错误通过声明分别保留，不用历史 Step 冒充本轮新读取。

## 修复与授权边界

- 核对 r16 留存库 `sp03_mig_979934f4aabe49ba9848a7eddab372d3`：Job `resources=[]`，两个 query_deepflow 均 `warnings=[no_evidence]`。该次 edges=0 不能证明由 peerScopeLimited 导致；最近 120 秒查询与冻结故障窗错位已确认。原报告保留，归因在此更正。
- 选择方案②：收集轮显式执行一个 ledgered `query_deepflow/get dependencies` Step。API 核对不可变目标与故障窗，收窄到目标 namespace，保留现有 Resources 和 grant digest，不借用 worker 权限。单资源范围的 peerScopeLimited 仍不能投影对端。
- 同一 `NetworkRelations` 同时支持 worker 私有 Data 和 HTTP/archive factSlice；每对有向关系保留最新原始观测时间。历史关系只用于冻结窗口规划，不刷新当前 Graph TTL，明细行数不充当总体统计。
- 关系 Step 接入原 evaluator、来源句柄、答案引用、历史重读和重新授权。合同、OpenAPI、Go/TS 与模型输出引用清单同步；不新增图入口、来源客户端或运行时。
- 实测约 2992ms 的 DeepFlow 取消暴露 MCP/HTTP 两处残留 3s 截断：MCP 该工具为 9s，HTTP 使用原 Query.TimeBudget 的 8s 上限；其他工具、行/字节/采集范围不扩大。
- 上一轮空总体→no_data/Exact=false、nonempty_observation_population 归因、租约传输失败仅本次读取关闭，以及探针复读/失败归因修复继续保留，见[原始记录](../README.md)。

## 按模型配置上下文与输出

用户明确要求后续使用 DeepSeek，避免为 16K 做有损压缩。官方说明标明 deepseek-flash 为 1M 上下文；仅外部验收 Profile 配置为 **1000000 上下文、8192 输出、120s、0 retries**。平台允许上限与 Python/Go 校验同步。内置模型保留独立已验证窗口，128K/16K 不得声明为 1M，canonical fixture 未改。外部付费模型仍是 fixture_only，不作生产准入。

- 撤掉为 16K 新增的历史 Fact/关系关联裁剪；冻结值、事实关联、来源质量、反证与缺口保留。既有语义投影只移除不供模型验证的 opaque material，完整原始材料在 Step 中。
- 1M/2048 的 r12 实际输入 25761～45106 tokens，成功收到响应，但四个回答均恰好输出 2048、`finish_reason=length`，造成 MODEL_OUTPUT_INVALID。此为已实证的输出截断，不能都归为中文质量问题。
- 最终 DeepSeek 输出上限采用合同已有的 8192；总输入/输出预算为 8×1000000 / 8×8192。每次按当前 Profile 预留，已知用量按实际结算，未知用量保守消耗预留；策略仍可更低。工具/结果字节/调用数/时间/身份授权门禁保留，legacy v2 单次 16K 与其配置的输出上限不变。
- 新回归发现 Holmes 默认按模型目录猜测 64K/120K 输出余量，会错误挤占小窗口。BudgetedLLM 的原生输出余量扩展现使用冻结 `max_tokens`，16K/2048、128K/4096、1M/8192 逐项验证；先失败日志为 `output-reserve-before-fix.log`。
- 完整三窗口追问另实证 64KiB MCP 封套将较大上下文替换为 truncated（外层 Step succeeded、wire partial，当前问题也丢失）。仅 get_assistant_context/v3 的已提交上下文传输改为 1MiB；源工具仍为 64KiB、全局结果 40MiB，新增超限 guard 在模型分配前明确失败。`context-envelope-before-fix.log` 与 `context-runner-before-fix.log` 保留旧失败。
- 原官方 MCP Bridge 的同一 asyncio.Runner 不能被 Holmes 的并发同步工具重入，实测 loop already running。用标准 Lock 保护 catalog/call/close，未新增运行时或客户端，新增并发回归先失败后通过。
- 隔离网关使用标准 HTTP Transport clone，保留默认协议能力，显式禁用代理，原证书校验与时限保留。EOF 仅写私有诊断，公开响应仍为静态错误；没有添加模型自动重试、关闭 TLS 校验或更改生产客户端。不能仅凭一次成功断言网络 EOF 已根除。

## 真实浏览器与账本复核

最终页面用例 `browser-output8k-ui-r11` PASS（1.2 分钟），所属 `browser-context1m-output8k-r3` Go runtime PASS（146.68s）。与 r22 使用同一二进制/Python 挂载制品，冻结目标 UID 为 `2b9bb954-cb7b-42c7-8bde-665ffeba8605`。

- 真实 PKCE/RS256 OIDC、数据库角色、现有 API/Worker/MCP/Holmes 路径，无 route mock。
- 目标 lookup 第 1 次成功，断言请求 kind/name/namespace/cluster 与 includeClusterScoped=false；Node 未授权时按钮禁用，没有自动扩范围。
- 本次提交 6 个精确查询：pod-restarts、pod-memory、pod-cpu、logs.records、network.http-summary、network.tcp-summary；2 条网络 Evidence。HTTP 独立 oracle 为 1000 响应、50 次 503、5%。来源 Step/Evidence 可定位，重载恢复、键盘与窄屏检查通过。
- 解释轮 2 条成功模型响应，最终 13 条已提交 Fact 引用，额外来源读取为 0；追问轮 1 条成功响应、8 条 Fact 引用，新提交 history/findings/changes 各 1，目标及故障窗保持。
- 三次响应输入 18957/19026/22794，输出 2795/2714/2392 tokens，均 `finish_reason=stop`。最终回答通过正式结构/引用校验，answerKind=diagnosis，模型错误码为空，全部预留归零。
- 两轮仍为 partial：历史/事件/变更覆盖与因果机制证据不足，未伪装成无异常、已确认因果或完成了建议检查。人工中文质量/帮助性评审不由本用例代替。
- platform_admin 仍不能读取 operator 的私有调查（403/404）；原上下文保持不变。

回执见 `browser-receipt.json`、`browser-collection.json`、`browser-ledger-verification.json`，数据库为 `sp03_mig_b7c34a52f5124cda86307ec936577471`。截图为 `browser-desktop.png` 和本次窄屏发送前的 `browser-desktop-before-model.png`，已实际检查；无凭据、JWT 或 IdP 私有配置导出。先前 r10 页面 5 QueryResult/1 Evidence、模型 2+1 响应的通过记录另存 `prior-output8k-*`；先前窄屏截图明确为 `prior-output8k-browser-mobile.png`，不冒充本次截图。

## 联合运行及失败索引

原 r6 的 11 分钟采集、三窗口 oracle 和源数据仍在 `/opt/ops-five-source-20261017`；本轮在 `/opt/ops-five-source-closure-20261017` 对同一 UID/历史窗口重新读取。当前 300/15 探针只恢复来源资格，不回填历史。

- relation-r1、internal-r2：HTTP 序列化丢失私有 Data，87 条可投影观测未跨边界；失败先保留再修复 factSlice。
- internal-r3：保留残留 3s 截断实证。
- internal-r4：实际内网模型路径 PASS（295.19s），5 QueryResult、6 网络 Evidence；HTTP fault/recovery、TCP fault 对账通过。留存库有 3 条成功模型响应，解释/追问 partial；追问 BUDGET_EXHAUSTED 保留，不作为中文质量通过。后续依用户 DeepSeek 指示执行外部验收，不以外部结果改写此内网记录。
- external-r5/r6：来源重启后断开的 port-forward / 未同步 inventory，分别拒绝；保留 no_rows_for_pair_with_unverified_namespace，不写成统计零。
- external-r7：旧 harness 显示 PASS，但实际模型成功响应为 0。该旧 PASS **作废为模型验收证据**。现要求每轮至少一条成功模型响应、允许的终态、解释不额外取证与追问恰好三项新读取。
- external-r8/r9：输入 17757/15668 加输出 2048 超过 16384，FAIL。上游 compaction 不足归类 MODEL_CONTEXT_LIMIT，避免泛化失败后重派。
- external-r10：旧 16K/裁剪视图下 PASS（110.52s），5 QueryResult、6 Evidence、解释/追问各 2 成功响应。该新增裁剪已撤掉，不作为最终源码验收。
- external-context1m-r11：网关 BadGatewayError，FAIL（85.30s）；未标上下文错误。
- external-context1m-r12：1M/2048 机制 PASS（165.58s），5 QueryResult、6 Evidence、解释 3/补证 2 成功响应，完整保留事实；输出 length 截断导致两轮 MODEL_OUTPUT_INVALID，未放宽校验。
- output8k-r13：来源资格读取超时；DeepFlow 在 07:33:02Z 再次退出 137。Kubernetes 事件确认 liveness 超时触发重启，随后转发断开；不将共现写成模型/OOM 因果。仅恢复本任务转发，没有调来源阈值。
- output8k-r14：pair fence 不满足，协议/窗口仅收集 1/3，断言拒绝；未强制资格或降低对账值。
- output8k-r15：三窗口采集/对账已过，首次模型调用 BadGatewayError，联合用例 FAIL；没有拼接成一次通过。
- output8k-r16：三窗口采集后模型传输 FAIL，私有诊断证实 unexpected EOF。无凭据 TLS 对照探针 4/4 为 401，未据此修改 TLS 曲线或证书规则。
- output8k-r17：标准 Transport 下解释一次有效响应；追问因 64KiB 封套丢失当前问题，误选额外工具并触发共享 Runner 并发错误/预算拒绝，FAIL。没有把该轮解释成功冒充整体通过。
- output8k-r18：上下文与并发回归修复后，来源栅栏/预算只收齐 2/3 协议窗口，FAIL，尚未调用模型。
- output8k-r19：三窗口及解释通过，完整追问封套为 70435 bytes，当前问题已保留；最终模型 upstream unexpected EOF，整体 FAIL。`context-envelope-ledger-comparison.json` 对比原 611 bytes/truncated。
- output8k-r20：私有 trace 记录新连接 TLS 完成且请求已发送，上游 HTTP/2 stream INTERNAL_ERROR，整体 FAIL，未改变 TLS/协议以追求通过。
- output8k-r21：解释有效，追问复用连接 unexpected EOF，整体 FAIL。历史解释/已预取的显式三项补证不再向模型开放多余工具；其他调查和实际查询仍保留原工具。
- **output8k-r22：最终源码完整联合 PASS（90.57s）**。5 QueryResult、6 网络 Evidence；HTTP fault/recovery、TCP fault 对账通过，Victoria compare 通过。解释与追问各一次成功模型响应，diagnosis/partial、错误码为空；16/14 Fact 引用，输入 25557/29523，输出 2853/2727 tokens，均 stop。解释无新增来源读取，追问 history/findings/changes 各一次保持 UID/故障窗；上下文完整、全部预留归零。原授权撤销后的历史查询/上下文访问拒绝仍通过。此轮未注入进程恢复故障（processRecovery=false），不声称验证了该项。回执与账本见 `external-context1m-output8k-r22-runtime-report.json`、`final-joint-ledger-verification.json`。

页面初期失败单独保留：旧 IdP fixture 硬编码其他租户而 403；新 fixture 首次资料初始化；Node API 请求缺根 CA；异步候选列表竞态；“发 送”中文间距造成旧精确定位失败。已使用独立同镜像 IdP 对齐测试租户并初始化，Node 加载正确根 CA，测试等待真实解析响应并核对完整请求，保留正确隔离断言。原 IdP/用户/realm 未改。旧 2K 输出页面 harness 明确以失败回执停止，不宣称通过。

## 回归、范围与制品

- `relation-before-fix.log` / `transport-before-fix.log`：新回归在旧实现失败。
- Go：analytics/evidence/app/graph，以及 HTTP API、investigation、MCP、DeepFlow、modelprofile 相关包通过；graph 实际耗时约 218s，未误判为挂起。
- Python：锁定镜像内最终 unittest 40 项；collector 17 项；独立探针 6 项。没有安装缺失依赖掩盖环境，原镜像无 pytest，既有纯投影断言另执行过。
- PostgreSQL Profile 预留：8192/32768/1000000、替换拒绝、取消、策略降低通过。Go 网关两项 race 检查通过。
- 前端 tsc/vite build、生成物一致性、Helm lint、diff whitespace 检查通过。
- 新增联合目标为专属 Pod。Node 当前会话未选入集群范围，页面明确拒绝自动扩大；PVC 不套用 Pod 网络/指标模板，本轮未新增 Node/PVC 实时目标矩阵结论，既有实现/记录保留。

制品绑定见本目录 JSON；密钥仅留远程私有文件。全部失败数据库/归档按既有保留策略留存，未删除以美化结果。临时 OIDC 容器已停止并保留，原网关命令/原 UI 已恢复；TLS 页面与原 OIDC discovery 均为 200，原 IdP 仍运行。已完成的临时 API 停止，原库/归档不删；恢复的本任务 DeepFlow 转发继续可用。见 `environment-restoration.json`。

1M 是配置上限与本次实际输入路径验证，**未做满 1M 压力或长上下文质量评测**。不宣称人工模型质量、来源持续可用性、V1.0 全面验收、发行/离线 Bundle 或生产发布通过；KubeVirt/CDI、Trace、PyRCA 的延期/禁用不变。
