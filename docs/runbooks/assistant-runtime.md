# AI 助手运行手册

适用范围：本专项非虚拟化 v3 功能，复用 SP-04/SP-05/SP-06。当前源码与
验收状态见 [开发进展](../plans/ai-assistant-development-progress.md)。

## 部署与版本

先部署支持 Job/Context/工具响应 v3 的新 API、Worker 和 investigator，再
启用自然语言提交。向前迁移 39、40 使用现有 migration_role/schema_owner；
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
- `EvidenceDigest`：实际准入证据摘要；`CollectorVersion`：已核对采集版本。
- `VerifiedUntil`、`CoverageFrom`、`CoverageTo`：有效期及实际覆盖时间。
- `MaxWindowSeconds`：已验证最大扫描时间窗。

指标/日志模板只接收受控 Intent；没有 qualification、来源版本变化、缺少
namespace/UID 标签、覆盖窗口不足或证明到期时拒绝查询。Fixture 证据不能
作为生产 qualification。模型和浏览器不能提供或覆盖以上配置。

`resources.current` 是当前状态；Pod 内存/phase/Node Ready 模板按窗口末端
求值，不能称为历史趋势。重启增加、CPU 使用核数、日志计数及控制面日分组
按目录口径处理。CPU 百分比、任意表达式及未经实现的趋势/比较保持不支持。
日志查询含显式 UTC 时间过滤。没有观测返回 no_data，不能解释为零或正常。

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
资源会话可选择最近 5/15/30/60 分钟，或填写跨度不超过 60 分钟的 UTC
绝对时间。页面展示服务端冻结的范围、真实资源身份和调查时间；更换目标或
资源调查窗口需新建会话。Incident 会话沿现有 Incident 上下文，不套用资源
调查时间控件。该入口不代表自然语言中的时间/指代调整已完整实现。

普通 Message/QueryResult 默认可读取 30 天；Legal Hold 字段和现有正式
Evidence 保护优先。依赖闭包及完整 TTL 清理尚未验收，不启用新的物理清理
任务。现有 Evidence 365 天保护不能被普通会话清理覆盖。

取消、预算和 lease fence 沿既有任务账本处理；晚到结果不应被接受。
预算耗尽时只能使用成功提交的查询事实生成 partial，未完成或未知工具的
结果不能成为答案引用。来源不完整或模型错误时保留失败证据。

## 验证与问题定位

本机只做编译、单元与合同检查；容器、数据库、模型和集群验证在用户指定的
`192.168.0.63`。专项隔离目录为 `/opt/ops-assistant-dev-20261012`。
数据库/聚合日志为 `integration-latest.log`，实际模型烟测为
`model-smoke-latest.log`。专项容器标记 `ops.task=assistant-20261012`，服务
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
