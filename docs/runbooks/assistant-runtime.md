# AI 助手运行手册

适用范围：本专项非虚拟化 v3 功能，复用 SP-04/SP-05/SP-06。当前源码与
验收状态见 [开发进展](../plans/ai-assistant-development-progress.md)。

## 部署与版本

先部署支持 Job/Context/工具响应 v3 的新 API、Worker 和 investigator，再
启用自然语言提交。向前迁移 39～42 使用现有 migration_role/schema_owner；
不为迁移新增数据库 CREATE 权限。旧 Worker 默认不领取 v3。

Helm 开关为 `sp06.assistant.enabled`，默认 false。启用时设置
`sp06.assistant.profile` 的完整材料和 `sp06.assistant.modelProfileVersion`；
后者必须是 Profile 的 `version@sha256:...` 内容引用。Chart 将同一材料和引用
同时传给三个服务。Profile 变更后重新生成引用并滚动更新三个服务，不能沿用旧
引用。Profile 合同见 `api/schemas/assistant-model-profile-v1.schema.json`。
API/Worker 读取 `assistant.enabled/modelProfileVersion`，investigator 读取
`assistantModelProfileVersion` 和 `assistantModelProfile`。材料摘要、适用任务
或运行版本不匹配时拒绝执行。v3 采用 Profile 上下文/输出上限预留预算；v2
保留原合同。当前锁定 Holmes 的超时为共享配置，Profile timeoutSeconds 必须
与 sp06.model.timeout 一致，baseURL 必须与已批准模型出口一致。

pending 不允许运行；fixture_only 仅限回环监听和回环模型的隔离测试，Helm
一律拒绝部署。test/fixtures/assistant/model-profile.fixture.json 的权重/评测/
准入摘要是测试值，许可证和能力为 Fixture 声明，不能作为发布材料或真实准入
证据；Ollama manifest 摘要也不能替代模型权重摘要。

必须使用与更新源码对应的镜像 digest、签名、依赖和发行物料。已有
SP-06 的已准入镜像没有这些更新，不能直接把旧镜像与新开关组成合格 Bundle。
模型 profile 字符串不是权重来源或质量准入证明；实际权重/许可证和中文质量
门禁尚未完成时，正式发行保持关闭。

## 数据源准入

控制面查询复用 PostgreSQL 租户事务；Graph 查询通过当前 Owner 的签名内部
路由。图、证据和分析请求携带本轮收窄范围，同时冻结完整授权摘要，执行前后
均核对当前授权。管理员审计域不访问 Graph、资源、指标或日志工具。

Victoria 分析能力由 Worker 的 `Sources[].AnalysisQualifications` 显式配置。
每项必须与真实 SourceRegistration 匹配，字段如下：

- `DatasetID`、`SourceRevision`、`BindingDigest`。
- `Operation`：aggregate/trend/compare/compare_percent/average/samples（依目录支持）。未填写只匹配旧 aggregate；每种
  新操作分别绑定经过验证的来源证明，不能自动借用旧聚合证明。
- `EvidenceDigest`：实际准入证据摘要；`CollectorVersion`：已核对采集版本。
- `VerifiedUntil`、`CoverageFrom`、`CoverageTo`：有效期及实际覆盖时间。
- `MaxWindowSeconds`：已验证最大扫描时间窗。

指标/日志模板只接收受控 Intent；没有 qualification、来源版本变化、缺少
namespace/UID 标签、覆盖窗口不足或证明到期时拒绝查询。Fixture 证据不能
作为生产 qualification。模型和浏览器不能提供或覆盖以上配置。

`resources.current` 是当前状态；Pod 内存/phase/Node Ready 的 aggregate
仍按窗口末端求值。它们的 trend 使用 dimension=time、stepSeconds，网格
从 from 到 to 包含两端，时间必须为整秒且窗口跨度能整除步长，最多 50 点。
显式使用 5 分钟 last_over_time 回看；这不是原始采样点或窗口平均值。来源
覆盖须包含 from 前额外 5 分钟，缺失点返回 partial/no_data，不补零。
compare 使用 dimension=comparison 和 comparisonTimeRange；两窗口同
时区、等长、不重叠且对照窗口在前，比较各自末端 gauge。两侧完整才计算
increase/decrease/unchanged 的绝对变化量，缺失或部分输入仅保留可用输入
事实，不生成变化量。compare_percent 输出方向与变化百分比，分母为对照值；
缺失、partial 或零基线返回 no_data。average 仅开放 gauge，以实际采样窗口
avg_over_time 后求和，不代表完整墙钟时间覆盖。
CPU trend 为每点固定 5 分钟 rate，aggregate/compare 为窗口平均 rate；
重启 trend 为每步窗口 increase，compare 为窗口增加量；日志 trend 为每步
独立半开桶计数，compare 为窗口计数。趋势包含 from/to 两端；首个计数器/日志
点需覆盖 from 前一个 step。最多 50 点、共享 3 秒执行预算，缺失不补零。
CPU 利用率百分比仍不在目录内（没有合法核心数分母）；任意表达式不支持。
日志 samples 为最多 20 条、单条 2048 字符、总响应 64 KiB 的脱敏样例，
再次校验 tenant/cluster/namespace/UID，状态为 partial，不用样例数说明总量。

新增控制面恢复时长以 resolved_at 落入窗口、started_at 可信且时间有序的
已恢复记录求平均；缺失样本会降级。本人执行成功率只含 succeeded/failed，
以执行创建时间落入窗口，当前目标授权与 owner 先于统计过滤；取消/未知不
进入分母。空样本/分母是 no_data，不返回零。

来源能力接口可携带 clusterUid。经当前 Owner 的签名 RPC 展示有效配置的
operation、revision、proofDigest、collectorVersion 和 coverage；
qualification_configured 不代表刚完成生产采集验收。无证明或失效操作不能
借用目录存在或其他操作的证明。没有观测不能解释为零或正常。

## 会话、引用与恢复

创建和提交均要求 Idempotency-Key；提交带 expectedConversationRevision。
每个会话只有一个 queued/running 轮次。另一个主体不能读取正文、查询结果
或 SSE。归档只隐藏会话，不删除数据；活动轮次先显式取消。

SSE 使用持久 Event 和签名 Last-Event-ID；断线可重连，过期游标返回 410。
权限变化停止读取，浏览器清理对应授权范围缓存。消息与列表使用有界分页；
存在无权访问的历史会话时，分页仍继续扫描而不返回其标题。

历史最多取六轮，最多四轮完整消息，较早解释由服务端压缩。事实 ID、来源
版本及 asOf 保留；模型不得把历史观测说成当前状态。“现在”需重新补证。
历史结果按实际原 Job 的当前授权、策略和数据分类重新检查。
每轮 input 带服务端生成的 requestedAt，结合会话 timezone 作为相对日期的
基准；用户输入不能覆盖该字段。历史实体引用只能来自已验证身份字段，读取
时仍核对当前资源与权限。假设中的支持与冲突引用可直接打开证据详情。
资源会话创建可选择最近 5/15/30/60 分钟或不超过 60 分钟 UTC 窗口。
同会话可通过 POST `/assistant/conversations/{id}/binding` 显式更换真实目标、
namespace 或时间；带 Idempotency-Key 和 expectedConversationRevision，
空闲时才允许，保持原冻结 grant 并重新验证身份。问数窗口最大 31 天，资源
窗口最大 1 小时；查询仍受具体来源更小的限额。旧轮次不改写，范围外历史
被隔离。Incident 窗口沿正式上下文，不接受资源窗口重绑。

服务端解析最近/过去的分钟、小时、天及今天/昨天/前天，冻结绝对时间与 IANA
时区。已设置的绝对窗口冲突时拒绝，需明确重绑；无新窗口的追问继承最近
已授权轮次窗口，“现在”不继承旧观测。指代只取已验证身份；多义时通过持久
平台澄清返回，用户选项不得建立从未验证的 Canonical ID。

故障链每个链接具有 from/to/kind/relationDigest、hypothesis、支持、冲突、
缺失和下一步。支持/冲突 Evidence 不允许重复；关系和证据只来自成功提交
工具或重新授权历史。关系不证明因果，不创建正式 Incident 或 confirmed RCA。
Incident follow-up 的 get_formal_references 读取指定 RCA revision 和实际
ActionPlan revision/contentDigest。侧栏按 turn/index 再授权，不接受用户随意
指定其他 Incident。历史 RCA 标明版本；ActionPlan 变化后旧引用不可用，不用
最新版本替换，也没有增加历史 ActionPlan 仓库。缺失证据追问可使用原只读
工具补证，聊天不产生执行句柄。

普通 Message/QueryResult/v3 Step 默认 30 天。关闭新助手提交不停止已有数据清理。
Worker 每分钟调用有界清理
函数；每轮最多 100 个叶轮次及 100 个过期空会话，语句预算 2 秒、调用预算
3 秒。保留锁与会话锁防止并发提交/保留变更；到期祖先要等待所有后代删除。
法律保留、活跃任务、有效结果、正式 Evidence 的归档/保留引用及递归依赖
优先，正式 Evidence 仍由其原保留服务管理。成功工具正文不可更新，只有
schema_owner 权限的受限清理函数可删除已判断为候选的 v3 Step。

管理员在独立法律保留表单或 POST `/assistant/legal-holds` 指定 turnId、
held 和原因；当前管理员身份在事务中再次验证并写审计。此接口不授予运维
数据读取权，operator 不继承管理员能力。解除保留不覆盖其他正式保护。
归档会话仍遵守保留保护，到期物理删除会同时移除工具正文和旧空会话绑定。

取消、预算和 lease fence 沿既有任务账本处理；晚到结果不应被接受。
预算耗尽时只能使用成功提交的查询事实生成 partial，未完成或未知工具的
结果不能成为答案引用。来源不完整或模型错误时保留失败证据。

## 验证与问题定位

中文五用例评测工具见 [评测说明](../../scripts/assistant-eval/README.md)。冻结
验收集与开发集分开，原始轮次不可覆盖，评分包含未支持和超时失败；模型
调用继续经既有 HolmesGPT/官方 MCP，不新增模型 client。测试身份入口不是
生产 OIDC/UI 验收。当前五例为 1 PASS/4 FAIL，平台 UID/时间继承和澄清通过
不能替代模型问数/排障效果；整体 FAIL、人工复核待填，生产未准入。
证据保留在远程 `evaluation-five-20261013-final/`；评分器退出码 2 表示
质量未通过，与采集运行工具的退出码 0 分别解释，不合并成一个 PASS。

本机只做编译、单元与合同检查；容器、数据库、模型和集群验证在用户指定的
`192.168.0.63`。专项隔离目录为 `/opt/ops-assistant-dev-20261012`。
最新数据库/聚合日志为 `six-integration-latest.log`，实际模型烟测为
`six-model-smoke-latest.log`，隔离浏览器验证为 `ui/browser-latest.log`。专项容器标记 `ops.task=assistant-20261012`，服务
只监听回环地址。测试凭据只位于该目录的受限文件，不复制到发行物料。

运行测试必须使用真实 API/Worker 数据库角色和更新源代码。模型烟测通过只
代表工具调用、签名、预算和提交闭环，不等于问数正确率、诊断质量或容量通过。
不能以“unsupported 回答成功”替代数据查询、跨权限或多轮效果验收。

出现拒绝时优先核对：主体/租户、当前 grant、原 Job policy、工具目录摘要、
profile、来源 revision/coverage、租约/nonce、保留期、已提交引用。诊断日志
使用静态错误类别，不记录模型密钥、上下文令牌、凭据或未脱敏原文。

## 暂停与回滚

暂停新提交时将助手开关设为 false，保留 v3 账本及已受保护的结果。先停止
新提交并让活动任务结束或显式取消，再回退服务；不做破坏性向下迁移。
旧 Worker 不应领取 v3，旧 v2 入口继续按原合同工作。当前尚无本专项升级/
回滚演练证据，正式放行仍需完成该门禁。

KubeVirt/CDI 始终按 ADR-0008 延期；人工处置使用原人工确认流程，不接受
聊天生成的执行句柄或自动修复。


## 真实来源与 DeepFlow 开发部署

真实采集/运行机制的阶段边界见 ADR-0039、ADR-0040，模型回答质量排除于
本轮机制标准，既有质量 FAIL 保留。来源、范围、协议、提交及引用校验仍有效。
运行步骤见 [真实运行脚本](../../scripts/assistant-runtime/README.md)，部署
步骤见 [DeepFlow 开发配置](../../scripts/deepflow-dev/README.md)。

Agent 的容器 limits 与 Agent Group 的 global.limits 必须一起变更；仅
kubectl 修改容器资源会被 Server 重写。当前默认 1024 MiB / 1000 millicpus，
requests 256Mi / 100m，来自同一份 agent-defaults.json。通信地址由当前
独立 Service 推导，不下发 127.0.0.1。Server 启动探针保护数据库初始化窗口，
但 readiness 仍须实际健康；启动等待不能当作通过。

L4/L7 查询须有实时 Kubernetes UID、namespace UID、容器 ID、生命周期
和组织/双端范围证明；每轮结束重新验证来源及身份。当前协议验证限开发
HTTP 200/503，不能推广为所有协议/内核兼容性。重传不等于直接丢包测量，
路径上下文只标记 L4 不完整观测。L7 仅 D1 元数据，Trace 仍禁用。
网络能力由当前授权范围的 Worker RPC 返回，来源失效/撤销/过期时如实不可用。

浏览器回执带本轮随机 runtimeId，不能复用旧成功回执。受保护 Archive
依赖只停止测试进程并保留存储与身份。发现远程 I/O/启动/租约失败时保存失败
轮次，不修改旧 PASS/FAIL，不修改生产服务或磁盘来掩盖问题。


2026-10-14 机械盘排查：`/data` 在 sda1（ROTA=1），采样读等待 231～417ms、
利用率约 94%～100%；本专项三个 DeepFlow PVC 的 hostPath 实际位于 `/opt`
的 NVMe 上。containerd 与原浏览器工具仍在 `/data`，不能从 PVC 路径推断
运行镜像和工具不受机械盘影响。追加验收复制既有 Chromium/Node 到独立
`/opt/ops-assistant-browser-20261014`，不升级版本；独立非 root 浏览器使用
19226 环回 CDP、专属 tmpfs profile，保留实际 TLS 信任校验。

用户于 2026-10-14 授权此远程开发实例绕过 DeepFlow 系统负载阈值：Agent
Group 的 `global.circuit_breakers.relative_sys_load.trigger_threshold` 为 0，
checked-in 开发默认值与回读一致。1024 MiB / 1000 millicpus 上限和平台
实时身份/范围/来源证据门禁保留；core/final 发行配置不采用此开发覆盖值。
