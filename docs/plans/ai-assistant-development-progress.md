# AI 专项开发进展

更新：2026-10-13。实施基线：platform HEAD `883936d`；授权见
[ADR-0038](../adr/0038-ai-assistant-development.md)，需求见
[实施方案](ai-data-query-and-conversational-troubleshooting.md)。本文件记录实际
进度，不将源码、Fixture、实际运行、发行和生产放行合并为一个 PASS。

## 已落地源码

2026-10-13 补充：新增 assistant-model-profile/v1 闭合合同，绑定模型 ID/版本、
内网端点、Secret 引用、上下文/输出上限、保守 token accounting、零计费声明、
工具调用/结构化输出、任务模式、超时/零重试、许可证和权重/评测/准入证据摘要。
API、Worker 和 investigator 使用同一份材料；Job 的 modelProfileVersion 为
`version@sha256:...` 内容引用，任一字段改变都会改变本轮绑定。Pydantic 和 Go
共享摘要用例；v2 继续使用原五字段 ModelContract 和默认 16k 预算。

新 v3 模型请求预留 Profile contextWindow 和输出上限，未知使用量按预留结算。
锁定 Holmes 通过共享运行时传递超时；Profile 必须与该值及已批准出口端点一致。
Fixture Profile 只允许本机回环监听和模型端点的隔离烟测，Helm 禁止发布 Fixture
和 pending Profile。真实权重、许可证与中文效果仍须独立准入，材料中的声明
不能代替实际证据。
能力接口和新建会话模式按 Profile taskKinds 与当前角色共同过滤；仅问数
Profile 不开放资源/Incident 排障入口，管理员不继承运维排障模式。

- P0：非虚拟化分析目录、QueryIntent/QueryResult/Facts、资源选择与上下文、
  AssistantAnswer、Job/InvocationContext v3、REST/OpenAPI 和生成客户端。
  目录含资源、Finding、Incident、本人执行、管理员审计，以及 Pod 重启/CPU
  核数/内存/状态、Node Ready、日志计数的受控模板。
- P1：Conversation/Turn/Message/QueryResult/Event 持久化；租户 RLS、复合
  外键、创建人隔离、创建/提交幂等、版本冲突、单活动轮次、取消、持久 SSE。
  v3 接入既有签名、lease/fence、预算、MCP SDK、HolmesGPT 调度；旧 Worker
  默认不领取 v3。预算不足时保留已提交事实并返回部分回答。
- P2：五类控制面 dataset，权限过滤先于去重聚合；QueryResult 不允许模型
  提供数字，引用需属于已提交工具或当前授权的历史。历史、恢复及结果读取
  重新核对当前授权、来源版本和保留期。审计与运维授权域分开。
  过期回答在 Turn、Conversation、Job、工具记录及事件读取处隔离，法律保留
  继续允许当前授权读取；此项为读取保护，尚不代表物理 TTL 清理完成。
  EntityRef 必须来自成功工具的固定身份字段或已验证历史，再检查当前可见性；
  症状、日志、注释中的同名字段不能建立实体引用。
  每轮冻结服务端 requestedAt 和会话时区，供相对日期解析；此锚点不能由
  用户提交字段覆盖，历史 asOf 不因此变成当前观测。
- P3：VictoriaMetrics/VictoriaLogs 独立聚合编译与 Worker RPC、来源证明、
  来源覆盖时间、窗口限制、并发限制、结果校验和 no_data/partial/truncated。
  只有显式通过配置的 qualification 才能查询；既有单资源证据接通不能替代
  分析能力准入。日志模板含绝对时间范围，禁止调用者提交原始查询。
- P4：真实 Canonical ID/UID 解析，多义目标内部澄清，独立资源调查上下文；
  已接入 Finding、health、关系、Kubernetes 和既有只读证据工具；
  关联 Incident 仅在所有关联 Finding 都可见时返回。全局入口增加资源名称
  查找、最多十个候选和确认后重新解析真实 UID。
  历史取最近六轮，最近四轮完整消息，较早轮次由服务端压缩解释文字，保留
  已验证结构化引用和 asOf。无 Incident Job 不生成占位 Incident。
- P5：全局助手及 Resource/Incident 入口、会话列表、模式/范围选择、消息、
  持久轮次恢复、SSE 重连、取消/归档、查询表格/日分组趋势/事实/来源版本/查询时区和降级状态、
  诊断支持/冲突/缺失、证据详情侧栏、会话/消息分页展示；假设内支持和冲突
  证据可直接打开，并明确空冲突列表不代表已排除其他原因。推荐问题按当前角色
  和会话授权域可见目录筛选，发送后返回最新消息页。Helm 默认关闭，启用需冻结模型 profile，并将
  同一版本同时传给 API、Worker 和 investigator。
  资源创建入口支持最近 5/15/30/60 分钟及有界 UTC 绝对窗口；会话显示冻结的
  授权范围、时区、资源身份与调查时间。窗口/目标变更仍需新会话，未将此项
  计作完整多轮范围/时间调整或浏览器验收。

上述是实现状态；P0～P6 仍有下列未完成项，不宣称完整阶段验收通过。

## 已有验证证据

本机仅做源码编译、单元和合同检查，不使用 OrbStack。运行验证在
`192.168.0.63` 的专项隔离目录 `/opt/ops-assistant-dev-20261012`；没有修改
现有生产部署。测试容器使用专项标签及回环地址。

- 受影响 analytics、assistant、investigation、tools、httpapi、app Go 包测试
  已通过；新增 Graph 收窄范围测试通过；JSON Schema/OpenAPI 合同检查通过。
- Web TypeScript/Vite 构建已通过，继续随源码修改复验。
- 远程真实 PostgreSQL 迁移 1～40，并使用实际 API/Worker 数据库角色验证
  会话所有权、轮次幂等与并发、旧 Worker 忽略 v3、领取/取消、预算部分回答、
  撤权和控制面统计。`TestAssistant*` 三项集成测试通过；最新复测补充创建
  幂等、消息分页、不可变正文、过期回答的会话/SSE/工具记录隔离及法律保留。
  v3 取消后迟到回答、Worker 租约接管、旧 Worker 迟到工具提交，以及未提交
  工具按预留上限计费均通过专项数据库用例；Job 事件读取也核对回答保留期。
- 远程真实 VictoriaMetrics/VictoriaLogs 后端的人工 Fixture 聚合通过：
  Pod 内存与日志计数排除授权外 namespace 和其他租户。此证据仅为
  `fixture_only`，不代表生产采集、其他指标模板或质量门禁已通过。
- 远程既有 investigator 容器运行更新 Python 源码测试：47 项通过，包含
  完整 Profile 摘要/准入边界和实际 Holmes 参数格式化；32k Profile 请求
  按上游 max_context_size 属性预留 32k，未知使用量保守结算。
- 真实 `qwen2.5:1.5b` + HolmesGPT + 官方 MCP + OpenBao + mTLS +
  PostgreSQL 基本链路烟测通过，已提交一次助手上下文读取并返回经平台校验
  的 unsupported 回答。租户和身份为隔离 Fixture，模型实际运行；此项
  不证明中文问数正确率、有效排障、多轮质量或模型正式准入。
  最新完整 Profile smoke 保留数据库 `sp03_mig_98eb6c41f5ef4bddab91d61ff9e6fcbc`，
  结果为 unsupported，成功提交一次上下文读取，用时 46.77 秒。该时间仅为
  一次测试耗时，不是性能指标。Profile 联调中的重复 timeout 参数失败及
  非法假设引用被平台拒绝均保留，数据库分别为
  `sp03_mig_92b99e1466044992a8f73598a5da950f` 和
  `sp03_mig_9621049c0ea347b1a4f61aefba4d4507`；修复后复验通过，未抹除失败。

远程数据库/聚合日志位于专项目录 `integration-latest.log`。登录凭据和
数据库密码不进入源码、此文档或发行物。

## 尚需完成与复验

1. P0/P6：真实来源能力清单、Profile 权重/许可证与实际效果准入、冻结中文
   评测集和独立重复测量；不可变 Profile 合同与运行接入已实现，不能把材料
   声明或现有烟测 Profile 当成中文质量通过。
2. P1/P6：官方 MCP + 实际模型完整闭环、实际进程崩溃接管、跨主体引用、
   保护归档与保留依赖的完整集成证据；目前数据库租约接管与迟到提交已通过，
   不等同于完整进程故障演练。
3. P2：恢复时间/比率等具体统计、Incident RCA/ActionPlan 引用和追问补证
   全链路、严格的历史指代及显式范围/时间调整。
4. P3：趋势、比较、脱敏日志样例、所有指标的实际采集与完整性验收；
   production SourceRegistration qualification 未建立的 dataset 保持不可用。
5. P4：全局自由症状的意图解析、来源能力呈现、
   故障链支持/冲突依据及相关工具输出范围的完整验证。
6. P5：分页、证据侧栏与推荐问题的实际浏览器复验、其他引用导航、窄屏/键盘及
   真实浏览器业务复验。
7. P6：对应源码镜像/离线物料/Bundle 更新、升级和回滚演练、容量测量、
   范围内最终审核；[运行手册](../runbooks/assistant-runtime.md) 已建立，
   部署与回滚步骤仍待对应运行证据。现有已准入镜像不能冒充更新源码的发行包。

KubeVirt/CDI 持续 deferred/disabled/unverified；根目录 00～10 正式文档
保留。人工处置继续本人输入真实 Bash 并确认，不建立聊天自动执行通道。
