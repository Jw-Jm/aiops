# 主分支问题清单

基线：`a2fa14f419abdc4587f56a1cbae59eb6cbb48c4e`。P1 表示主要业务链路在明确条件下不可用，优先修复；P2 表示局部正确性、可用性或交付门禁缺陷。这里的优先级不是漏洞严重性评级。

## MR-001 · P1 · 当前资源统计的编译合同与预算合同互斥

**这是什么：** AI 问数通过平台编译 `QueryIntent`，再由 MCP 预留查询预算，最后执行来源查询。

**问题与触发：** 用户询问“apps 命名空间有多少 Pod”，模型生成 `resources.current / aggregate / resource_count`，不带 `timeRange`。该输入能通过 `analytics.Compile`，但 `SourceRequestCost` 在识别数据集之前就因缺少时间范围返回错误，MCP 不会执行查询。给输入增加时间范围又会被 Graph 编译器拒绝。

**代码位置：** `internal/analytics/cost.go:24–27`，`internal/analytics/compiler.go:42–46`，`internal/investigation/mcp/server.go:305–317`。

**证据与反证：** [graph-budget-probe.log](evidence/graph-budget-probe.log) 使用同一条输入调用真实编译器和真实 MCP 预留函数，复现“编译成功 / 预算拒绝”；再加入窗口验证编译失败。已有 `cost_test.go` 为 Graph 构造了时间范围，而且不执行 Graph 编译，因此没有发现两层合同冲突。日志趋势、指标比较展开预算的既有修复仍有效，不应回退。

**影响：** `resources.current` 经 MCP 的当前资源计数、按健康或资源类型分组均受影响。直接调用 Graph/analytics 单元测试通过不能证明用户的问数链路可用。

**最小修复：** 在现有 `SourceRequestCost` 中先定位数据集和合法操作，再按来源检查窗口。`resources.current` 合法当前聚合消耗 1 次查询，保留“不接受时间范围”的合同；时序来源继续严格校验窗口与展开成本。不要向当前资源统计塞入虚构的时间窗口。

**涉及与验收：** `cost.go`、成本测试及 `mcp/server_test.go`；增加同一输入跨 Compile → reservation 的回归，并在 MCP 集成中验证预算先扣留 1 次再查询。保留日志 50 点、比较 2 次、网络趋势与余额不足拒绝测试。置信度高；未使用真实模型。

## MR-002 · P1 · 原生身份的 v2 Incident 调查在 Worker 端失去授权来源

**这是什么：** Incident 工作区的调查入口仍创建 `investigation-job/v2`；Worker 没有浏览器会话，需要从持久身份重新证明操作者权限。

**问题与触发：** 只有 `identity.role_grants` 授权的原生 operator 在页面发起调查。API 中 `graph.EffectiveTx` 能根据原生 Session 构造有效范围，`CreateJob` 保存 v2 Job；异步 `Claim` 调 `current` 时没有原生 Session，`current` 直接回到 `platform.role_bindings`，得到空范围并返回 `ErrDenied`。Claim 将该任务写成 `failed / AUTHORIZATION_REVOKED`，即便用户并未撤权。

**代码位置：** `internal/httpapi/investigation_handlers.go:146–149`；`internal/investigation/repository.go:61–68`、`:160` 的 v2 INSERT、`Claim:295–307`；`internal/graph/protocol.go:51–73`、旧授权表分支及 `finishScopeTx:232–235`。

**证据与反证：** [native-v2-probe.log](evidence/native-v2-probe.log) 和最终 [go-probes.log](evidence/go-probes.log) 调用真实 `current`，模拟有效租户/集群和空旧授权表，观察到拒绝。v3 Assistant Job 已有 `AuthorizationSource/UserID/Version` 和 `currentAssistant` 路径，本问题限于没有保存这些信息的 v2 路径。`sp06_lock_authority` 本身不要求找到 role binding；真正的无权限来自其后旧表读取与空范围检查。

**影响：** 原生登录后的传统 Incident 调查无法运行；SSE 或轮询修好也不能让该 Job 恢复。它不代表所有 v3 自然语言调查失败。

**最小修复：** 为新 v2 Job 保存服务端证明的身份来源、userId 和 authorizationVersion，并在所有无会话复核点按冻结来源重建权限。复用 v3 和 action 中已有的原生持久授权读取方式；历史无字段记录只能解释为 legacy，禁止失败后跨身份系统兜底。

**涉及与验收：** Job 持久模型、CreateJob、load/current、Claim、续签和结果读取；新增前向迁移；API/Worker/Job 协议与 Python 严格输入合同若增加字段必须共同更新。新增“无旧授权表记录”的原生 v2 Create → Claim → tool → Complete 集成用例，并验证撤权、重新授权版本变化、停用账户、跨租户、Worker 重启及历史 legacy Job。置信度高；本轮没有执行真实 PostgreSQL 全链路。

## MR-003 · P1 · 原生高权限命令的 MFA 证明没有贯穿确认与派发

**这是什么：** `k8s_cluster`、`ssh_root` Profile 必须经过 MFA、本人风险确认、持久执行记录，再由 Worker/Runner 复核后执行。

**问题与触发：** 第一处：`AcknowledgeRisk` 用原始 ctx 调 `authorize(..., true)`，未构造 `NativeStepUpIntent`；`touchNativeStepUp` 因 `!bound` 必然拒绝。前端 confirm 请求也没有携带挑战 header。第二处：即使修好风险确认，`BeginDispatch` 和 `ClaimWithKey` 在无会话 context 中复用 `authorizeAs(..., true)`；高权限分支仍调用 `TouchCurrentStepUpSession`，落入需要 Keycloak SID 和 LoA2 ACR 的旧路径。原生 actor 没有 SID，其基础 ACR 为 LoA1。

**代码位置：** `internal/action/service.go:123–129`、`:271–287`、`:360–366`；`internal/auth/native_stepup.go:28–34`；`internal/auth/stepup.go:129–140`；`internal/action/runner_protocol.go:59`、`:134`；`web/src/features/action/CommandComposer.tsx:306–320`。

**证据与反证：** [go-probes.log](evidence/go-probes.log) 直接调用生产 Touch 函数，确认两种 context 都会在访问数据库前拒绝。该探针证明拒绝条件，不冒充已搭建完整命令执行环境。Prepare 已构造 command_execution intent，action authority 已保存原生 user/version，故不是“原生身份完全没实现”，而是这些已有机制未连接到全部阶段。普通 namespace Profile 在没有挑战 header 时不进入第一处强验证分支，不能把缺陷扩大为所有命令必败。

**影响：** 合法的原生高权限操作无法完成风险确认；仅补确认仍会导致异步 dispatch_denied。没有证据表明该问题允许绕过 MFA 执行。

**最小修复：** 风险确认与 Prepare 使用同一服务端 assessment 派生的 target。确认请求携带挑战引用；服务端重新验证精确 command binding。准备执行时冻结足以异步复核的原生 proof 引用和版本；派发/Claim 必须重查其来源、操作、binding、有效期、撤销和当前权限，复用现有 identity SQL，不再要求原生 actor 证明 Keycloak 会话。

**涉及与验收：** CommandComposer、action service/native_stepup/authority/runner_protocol、identity proof 查询及 execution 持久字段、前向迁移与合同。真实数据库串联 Assess → MFA → Ack → Prepare → Dispatch → Claim；加入命令变化、目标 revision 变化、挑战过期/撤销、权限版本变化、账户停用、并发与幂等重放反例。不得把后台阶段简单设为 StepUpVerified=true。置信度高。

## MR-004 · P2 · 调查事件流拒绝原生 Cookie 会话

**这是什么：** 调查面板使用可恢复 SSE 接收事件，以游标和单调 eventId 去重。

**问题与触发：** `requestOptions` 的原生分支使用 `credentials: same-origin`，无 Authorization；`streamInvestigationEventsWithResume` 却在 fetch 前强制检查 Bearer。原生用户进入活动调查后，本地循环失败并最多重试 10 次。

**代码位置：** `web/src/api/client.ts:76–101`；`web/src/api/sse-reader.ts:204–207`；`web/src/features/investigation/useInvestigationStream.ts:62–98`。

**证据与反证：** [native-sse-probe.log](evidence/native-sse-probe.log) 使用 Cookie 模式选项调用真实 reader，确认 fetch 调用数为零。共享 `streamJSONEvents` 本身支持 Cookie；后端 Foundation 也统一经过原生认证。Assistant 会话 SSE 使用共享 reader，没有这个 Bearer 前置。调查面板仍每 5 秒轮询，因此不是所有状态都不可见。

**最小修复：** 将认证有效性留在共享请求选项及服务端认证层，移除该特定 reader 的 Bearer-only 假设；保留游标、序列、撤权中止和 401/403/410 处理。同步修改把“缺少 Bearer 必须本地拒绝”写死的测试。

**涉及与验收：** `sse-reader.ts`、Node SSE 测试、调查 Hook 测试。分别验证原生 Cookie 与 OIDC Bearer 请求确实发出并按序接收，过期会话由后端 401 拒绝、断线续传不重复、撤权后不再渲染缓冲事件。置信度高；未做真实浏览器验收。

## MR-005 · P2 · 多集群同名资源抹掉合法历史关系

**这是什么：** 资源观测入库时冻结原生 owner、Pod→Node、Pod→PVC、PVC→PV 关系，供后续历史调查使用。

**问题与触发：** 同租户的两个集群均有 `Node/worker-1`。`observedRelations` 先在整个租户按 kind/namespace/name 查最多两条；得到两条后直接丢弃。对 cluster scope 和 API group 的检查在 `len(ids)==1` 后才发生，已经来不及筛掉另一个集群。

**代码位置：** `internal/resourcestore/observations.go:119–146`。

**证据与反证：** [go-probes.log](evidence/go-probes.log) 对真实函数给定单集群行集时得到关系，增加另一个集群的同名 Node 行后关系变为空。查询文本确实没有集群条件。后置检查能阻止跨集群错误连边，故问题是关系缺失，不是跨集群数据泄漏。owner 有 UID 时同样会先被租户级同名歧义过滤。

**影响：** 多集群常见名称冲突使历史诊断缺少有效依赖；观测记录按 resourceVersion 去重，之后读当前图不能补造当时的历史关系。

**最小修复：** 在 SQL 查询中按 anchor 的精确集群限制候选，再做 API group 与可用 UID 匹配、唯一性判定和 LIMIT；保持 observed_at 时间约束。优先使用已有 cluster_id/canonical identity，不用名称推测跨集群目标。

**涉及与验收：** observations、相关单元/数据库用例。验证两个集群的同名 Node/PVC/PV/owner、同集群真正歧义、不同 API group、删除/重建 UID、晚于观测的对象；历史已提交缺口不得凭当前状态回填成确定事实。置信度高；探针使用模拟查询行集。

## MR-006 · P2 · 原生密码依赖扩展后，运行时源码准入仍锁定旧闭包

**问题与触发：** 当前主分支引入 Argon2id 密码处理，但运行时 corresponding-source 锁未覆盖新增实际编译文件。打包、导入与本地检查据此失败。

**证据：** [runtime-source.log](evidence/runtime-source.log)、[check-local.log](evidence/check-local.log)；[runtime-closure-diff.json](evidence/runtime-closure-diff.json) 实际生成并比较 arm64/amd64 两套闭包：arm64 新增 x/crypto 的 9 个 Argon2/Blake2 文件，amd64 新增 x/crypto 的 12 个文件及 x/sys/cpu 的 12 个文件。模块版本集合不变，标准库文件集合不变。

**代码位置：** `internal/identity/password.go:18,54,94`；`scripts/prepare-runtime-source.py:188–198`；`bundle/evidence/third_party/admission/platform-runtime-go*.lock.json` 及对应 Catalog。

**影响与反证：** `cmd/opsctl` 1 项、`internal/bundle` 4 项测试失败。校验器是在正确阻止未同步材料，不能把修复理解成放宽 digest 检查；也不能据此断言部署中的旧 Bundle 被篡改。

**最小修复与验收：** 保留必要的安全密码算法，按已有流程重新审查两个架构的选中文件/许可证、生成对应源码归档、重建关联二进制并更新独立准入、Catalog 和 Bundle 材料。两个架构分别校验闭包、离线重建和摘要；5 个失败测试及 runtime-source check 均通过。不得只复制本轮计算出的摘要到“已准入”字段，真实发行材料和证据必须匹配。置信度高。

## MR-007 · P2 · 迁移与发行基线存在三个不一致版本

**问题：** 实际迁移为 00001–00063，源码准入范围仍声明 00001–00043，`docs/release/delivery-baseline.json:159–165` 仍为 latest/count=57。另有 00047–00063 共 17 个文件包含 Down 区段标记，与既有静态前向迁移门禁冲突。

**证据：** [migration-source.log](evidence/migration-source.log) 确认源码锁检查失败；[remaining-local.log](evidence/remaining-local.log) 确认 Contract 的 `migration range/policy drift`；[forward-migration-static.log](evidence/forward-migration-static.log) 列出 17 个实际失败文件。迁移文件编号本身连续。

**代码位置：** `scripts/prepare-migration-source.py:55–65`；`bundle/evidence/third_party/admission/migration-tool-source.json:8`；`docs/release/delivery-baseline.json:159`；`test/contract/delivery_preparation_test.go:124–131`；`test/integration/migrations_test.go:35–45`。

**影响与反证：** 当前版本不能通过迁移源码与交付一致性门禁，旧准入归档也不能作为已覆盖新增身份 Schema 的证据。逐文件补审纠正了早期“均为空区段”的描述：00055–57 是主动抛错的回滚阻断，00058 的 Down 会删除历史读取函数，其余新增 Down 仅注释。完整分类见 [down-sections.json](evidence/down-sections.json)；00038 另有既有精确冻结的删列例外。不能把静态检查失败描述成已执行回滚；`db-migrate` CLI 只调用 Up/UpTo。

**最小修复：** 以定稿的前向迁移集合统一源码归档、准入、交付基线和验证记录。先核对这些文件是否已在共享环境应用；已应用的 SQL 必须保留原件，参照既有 00038 的准入方式逐项处理历史例外，明确区分注释、主动抛错和 00058 的实际删函数语义；迁移 CLI 仍只允许 Up/UpTo，并以回归验证不能触发 Down。静态规则不得笼统允许历史所有 Down。未应用文件才可在首次应用前整理为仅 Up。禁止批量重写已应用迁移或删除门禁来得到绿色结果。

**验收：** 交付 latest/count 与最终迁移集合一致；新空库与既有版本前滚通过；运行角色、RLS、函数授权和锁序回归通过；归档在空缓存离线重建并含全部 SQL；静态规则仍拒绝任何新的可执行 Down。置信度高；本轮仅执行无需数据库的静态迁移检查。

## MR-008 · P2 · sqlc 生成物未跟上身份和执行 Schema

**问题与触发：** `make check-generated` 在隔离目录重新生成后发现 `internal/persistence/dbgen/models.go` 不一致。

**证据与位置：** [delivery-checks.log](evidence/delivery-checks.log)、[sqlc-models.diff](evidence/sqlc-models.diff)。差异包括 `ActionExecution.Authority` 和多项 Identity 模型。生成入口为 `internal/persistence/sqlc.go:3`，源为 migrations 与 `internal/persistence/queries/registry.sql`。

**影响与反证：** 主分支生成一致性门禁失败，后续 sqlc 使用者拿到过时模型。现有 identity 主要使用专用 SQL 接口，故不能仅凭模型缺失认定当前身份查询读取错列或泄露密码。

**最小修复：** 在 Schema 定稿后执行仓库既有生成命令并提交实际差异；不手写 generated 文件、不为消除模型而收窄真实 Schema 输入。

**验收：** `make check-generated` 通过，第二次生成无差异，Go build/vet 和相关查询测试通过。含敏感列的生成类型仍不得直接成为 HTTP 响应模型。置信度高。

## MR-009 · P2 · 密码散列并发未受默认容器内存约束

**这是什么：** 每次 Argon2id 散列使用 64 MiB；原生登录对未知用户名也进行同等成本验证，避免枚举时序差异。

**问题与触发：** 登录入口允许一个分钟窗口内 60 次尝试；这只是累计计数，锁在 AdmitLoginAttempt 返回后已经释放。多个合法格式、未知用户名请求可同时进入昂贵散列。HTTP Server 没有对应并发槽位，默认 API 内存限制仅 512 MiB。8 个同时进入的散列工作区已达到 512 MiB，尚未计入 API、OPA、数据库连接等内存。

**代码位置：** `internal/httpapi/native_auth.go:27–29,91–123`；`internal/identity/password.go:28,54,94`；`migrations/00045_native_http_sessions.sql:526–580`；`deploy/charts/ops-platform/templates/deployments.yaml:174–176`；`internal/app/api_runtime.go` 的 HTTP Server。

**影响与反证：** 能访问登录入口的客户端可在速率额度内制造内存竞争，导致 OOM 重启或主 API 延迟；已认证密码修改、MFA password step 也需纳入散列资源上限。Origin 检查对浏览器 CSRF 有效，但直接客户端可发送 Origin，不能替代资源限制。数据库速率锁不会覆盖事务外的散列阶段。

**证据等级：** 散列内存、允许速率、锁释放和无并发上限由代码确认；OOM 是由配置和并发路径推导的风险，未对运行服务做压力攻击，也未测量实际峰值 RSS。该项不声称已经观察到生产 OOM。

**最小修复：** 以标准库实现一个进程共享、可取消且有界的密码工作槽位，覆盖登录、dummy 验证、改密、新建/重置密码、MFA 密码步骤。按现有容器内存先采用保守小上限并实测；拥塞时返回明确可重试错误，不能无限排队。保持 Argon2 参数与未知用户的等成本行为。

**验收：** 阻塞式测试证明同时执行的 KDF 不超过上限；取消/异常后归还槽位；大量等待请求有界拒绝；在默认 API 配额内实测登录突发，确认 OOM=0 且健康检查仍可响应。数据库分钟速率限制仍保留。置信度中高；运行影响待受控负载验证。

## MR-010 · P2 · SP05 采集降级状态写入了不接受这些键的指标接口

**这是什么：** SP05 Worker、原生指标/控制面采集和 analyzer 在每轮结束时报告降级或恢复，供平台监控观察。

**问题与触发：** `SetComponentDegraded` 只接受 traces、metrics_listener、runtime、audit_signing 四个固定键。app 调用的 `sp05` 及 `sp05-control-plane/<sourceId>`、`sp05-metrics/<sourceId>`、`sp05-pod-metrics/<sourceId>`、`sp05-analyzer/<sourceId>` 全部落入未知键直接 return，故采集持续失败也不会记录这些状态。

**代码位置：** `internal/observability/metrics.go:35` 的 degradationBits 与 `:150–155`；`internal/app/sp05_worker.go:66`；`internal/app/sp05_official_observations.go:46–48`；`internal/app/sp05_analyzer.go:31`。

**证据与反证：** 最终 [go-probes.log](evidence/go-probes.log) 使用真实 NewMetrics/SetComponentDegraded，对所有生产调用形式置 true 后仍观察到 Degraded=false；audit_signing 正向对照生效。现有日志、Graph/source health 或其他业务指标仍可能暴露部分故障，所以不声称所有监控都失效；这里确认的是这些状态上报完全被丢弃。

**最小修复：** 明确业务采集降级与观测导出降级的指标职责，选用单一共享入口保存已配置来源的状态。若保留聚合指标，须按来源分别保存后取 any；某一来源恢复不得清掉另一来源的故障。只接受运行配置登记的有限来源/组件键，避免任意动态标签或无限增长状态。

**验收：** 控制面/NodeMetrics/PodMetrics/analyzer 逐类失败均能采集到相应状态；两来源同时失败、一个恢复时聚合仍降级，全部恢复后清除；已有 audit/export 指标语义不改变。置信度高；没有对真实采集端制造故障。

## MR-011 · P2 · 助手请求中切换集群后，处理中状态无法恢复

**问题与触发：** 在助手页创建会话的请求未结束时，通过集群切换器切换到另一授权集群。`scopeKey` 变化会取消旧 signal 并清除旧表单，但没有清除 `busy`。旧请求的 finally 又因 `canCommit()` 为 false 不再执行 `setBusy(false)`，新范围保留 `busy=true`。创建按钮持续 loading，不能再次创建；离开页面重新进入才能恢复。

**代码位置：** `web/src/pages/AssistantPage.tsx:145–168` 的范围清理、`:250–258` 的 abort、`:272–295` 的 mutate/finally；创建按钮绑定 `loading={busy}`。

**证据与反证：** [assistant-busy-probe.log](evidence/assistant-busy-probe.log) 复用现有真实 React 页面及 ClusterScopeProvider 测试夹具，发出有效异步 create，调用与集群切换器相同的 requestCluster，完成旧请求后断言新创建按钮仍有 `ant-btn-loading`，再次点击不会调用 API。该测试不是完整浏览器验收。旧响应确实不会跳到旧会话，隔离机制本身有效；现有测试只覆盖旧响应被丢弃，没有覆盖新范围能否继续操作。通过 URL 导航可能触发 AppShell 临时卸载，不把它与选择器切换混为同一路径。

**最小修复：** 范围切换时同步重置 busy，并用当前请求的范围/序号判断 finally 是否仍拥有该状态，避免旧请求清除新请求的忙碌状态。复用现有 abort/ref，不新建请求框架。身份切换、卸载与同范围新会话也核对同一状态归属。

**验收：** 旧请求 pending → 选择器切集群 → 旧成功/旧失败/取消三种完成方式，新范围都能再次 create；旧响应不得导航或写缓存；新请求开始后旧 finally 不得使新按钮提前解除 loading。置信度高。

## MR-012 · P1 · 原生用户的 Incident 人工变更仍被旧角色表拒绝

**这是什么：** Incident 的确认、缓解、抑制、关闭、合并、拆分，以及关联人工证据，在写事务中再次锁定操作者权限，防止检查后撤权。

**问题与触发：** 原生 operator 的授权位于 `identity.role_grants`，没有旧 `platform.role_bindings` 镜像。`graph.EffectiveTx` 可从 Session 得到合法 scope，但 `incident.Authorize` 无条件调用 `platform.sp05_lock_operator_scope`。该函数唯一的定义仍在 00028，只枚举旧表，没有原生分支，因而返回 false。HTTP 幂等准入和最终业务写事务均调用它，合法操作在提交前被拒绝。

**代码位置：** `internal/incident/state_machine.go:28–40`；`migrations/00028_sp05_operator_lock.sql:5–17`；`internal/httpapi/sp05_mutation.go:64–75`；`internal/incident/manual_evidence.go:43–48`；Merge、Split 和 AuthorizeAll 的调用链。

**证据与反证：** 搜索完整迁移集合确认没有后续替换此函数。最终 [go-probes.log](evidence/go-probes.log) 调真实 Authorize，提供允许目标的 scope 和原生 context，模拟旧表锁函数无匹配的 false 结果，确认仍走旧函数并返回 ErrScope；true 正向对照成功。SQL 逻辑由源码核对，没有实际 PostgreSQL 集成执行。Incident 读取和 Worker 自动恢复有独立路径，不因此声称全部 Incident 读写均失败。此项与 MR-002 的异步 Job 身份丢失不同，单修其中一个不会修好另一个。

**最小修复：** 在共享 Incident 授权边界按可信 context 选择原生/legacy 事务复核。原生路径复用已有 native account/grant 的共享锁与 graph 范围重建，并校验当前权限仍覆盖冻结目标及每一关联资源；legacy 保留原有函数。人工证据处也使用同一边界，不能仅替换 HTTP 准入或直接相信浏览器 scope。不要同步两套角色表以绕过来源问题，不重写已应用的 00028。

**验收：** 只有原生 grant、旧角色表为空的真实 API 确认/抑制/合并/拆分成功且产生审计；namespace、跨集群、关联资源越界、停用/撤权、并发撤权与幂等重放全部按当前权限处理。legacy 回归保持通过；自动恢复不改变。置信度高，运行联调待完成。

## MR-013 · P2 · 故障链迟到响应可以覆盖另一个会话的当前轮次

**问题与触发：** 在会话 A 提交故障链收集或只读补查，请求尚未返回时从会话列表进入 B。两个子组件直接使用 `requestOptions(undefined, true)`，没有取消或会话归属检查，成功后始终调用旧 onSubmitted。AssistantPage 的这些 callback 直接 setTurn，不核对当前 URL 的会话。因此 A 的迟到轮次会写入仍挂载的父页面；随后 `getAssistantTurn` 还会读取 A 的轮次，并在 B 的工作区显示其状态/回答。

**代码位置：** `web/src/features/assistant/FaultChainCollector.tsx:103–116`；`web/src/features/assistant/FaultChainGapCheck.tsx:72–113`；`web/src/pages/AssistantPage.tsx:822–830,881–890,921–931`。同账号切换会话不会改变共享客户端的 credentialEpoch，因此 checked 的身份校验不足以阻止此情况。

**证据与反证：** [fault-chain-late-probe.log](evidence/fault-chain-late-probe.log) 使用真实 GapCheck、generated fetch 和 checked，复用现有故障链夹具，在父容器切换会话并卸载旧子组件后完成请求，确认父容器在 B 下仍收到 A 的 turn。父容器模拟 AssistantPage 已核对的 setTurn 回调；它不是整个 AssistantPage 或浏览器联调。普通 send/create 使用父页 mutate，有独立保护；身份变化也会被 checked 拒绝，不能扩大为跨用户未授权泄漏。

**影响：** 已授权资料被展示到错误会话上下文，造成故障证据和回答误归属，也可能把 B 的操作禁用状态绑定到 A 的 running 任务。服务端原始 conversation/turn 归属没有被改写。

**最小修复：** 让所有子组件提交回调由父页统一复核当前会话、范围和请求归属；子组件卸载/会话变化时取消请求，成功回调前再次核对。仅给子组件增加 key 不够，旧 Promise 仍可调用父级 setter。复用父页现有 abort/ref/mutate 语义，不创建第二套凭据状态。

**验收：** 收集与补查分别覆盖 A pending → B → A 成功/失败/取消，B 的 active turn、回答、缓存和 URL 均不受影响；A 的持久任务仍可从 A 正常读回。同会话正常提交、撤权和卸载回归通过。置信度高，完整页面联调待执行。

## MR-014 · P1 · 管理员重置 MFA 后，目标用户失去可登录的密码版本

**问题与触发：** 用户丢失第二因素，管理员成功调用 MFA reset。最终生效的 `identity.mfa_admin_reset` 把 account.credential_version 从 N 改为 N+1，撤销会话并要求重新登记 MFA，却没有复制或创建 N+1 的 password credential。`find_login_account` 使用账户当前版本 INNER JOIN credentials，因而找不到该账户。原密码没有改变，但重新登录一直返回统一认证失败，无法进入重新登记流程。

**位置：** `migrations/00052_native_mfa_lifecycle.sql` 的 mfa_admin_reset 更新账户段；`migrations/00044_native_identity.sql` 的 find_login_account 精确版本连接；`internal/identity/mfa.go:713–775`、`internal/httpapi/native_mfa.go:518–590`。后续 53–63 没有补写新密码行；Go 事务仅调用函数和追加审计。

**证据与边界：** [mfa-reset-invariant.json](evidence/mfa-reset-invariant.json) 记录最终函数与连接不变量：密码行仍为 N、账户已为 N+1，连接结果为零。这是静态 SQL 证明，不是 PostgreSQL 运行复现。既有 `native_mfa_test.go:751–805` 检查 reset=200、旧会话=401、版本+1、审计和 reset_required，没有重新登录断言。普通 password reset 会创建新密码行，是独立路径；不能据此认为 MFA reset 正常。

**最小修复：** 新增前向迁移，在目标账户锁内读取当前有效密码散列，原子地保存到新 credential_version，并按既有凭据规则退役旧版本；保留会话/step-up 撤销和 mfa_reset_required。不能降低版本、恢复旧会话或要求管理员再改一次用户密码作为正常恢复步骤。

**历史处理与验收：** 对已发生版本断链的账户，先核对 MFA reset 审计及历史凭据，提供受控修复步骤，禁止不加条件地把“最后一个 hash”用于所有异常账户。真实集成必须覆盖 reset → 原密码重新登录 → 只能登记 MFA → 登记完成后恢复原授权；已改密前的密码、旧会话、跨租户、并发改密和重复 reset 反例保持拒绝。置信度高，数据库运行验证待完成。

## MR-015 · P2 · OpenBao TLS Secret 提交结果不确定时删除了唯一的 CA 信任文件

**触发与影响：** 首次 bootstrap 已生成 CA 文件，执行 kubectl apply 保存 TLS Secret。若 API 已提交 Secret，但客户端收到超时或丢失响应而返回非零，EnsureBootstrapTLS 删除本地 CA 文件。下次发现 Secret 已存在，却无法从外部 CA 文件校验其证书，返回 ErrOpenBaoCAUntrusted，自动重试无法恢复。Secret 只包含服务端叶证书与密钥，不能从它重新取得被删除的根 CA。

**位置：** `internal/integrations/openbao/bootstrap.go` EnsureBootstrapTLS 的 apply 错误清理分支与已有 Secret 校验分支。

**证据：** [openbao-lifecycle-probe.log](evidence/openbao-lifecycle-probe.log) 使用真实函数和私有临时目录内的 fake kubectl，模拟 apply 已保存对象、随后返回失败；确认 CA 文件被删除，第二次调用被信任检查拒绝。没有调用真实集群。明确未提交的失败可清理生成物，未知提交结果不能据此认定未写入。保持拒绝不可信 Secret 本身是正确行为，问题是 bootstrap 错误地销毁了恢复所需的本地信任依据。

**修改与验收：** 对已发出的写请求保留 CA 文件，读取远端确认 Secret 是否为本次生成身份后完成恢复；不能仅因同名就接管其他 Secret，也不能覆盖已有密钥。复用现有证书匹配校验，必要时用 create 避免覆盖。覆盖提交成功但响应丢失、真正未提交、已有同名异源 Secret、重试及 CA 文件权限；仍禁止不加校验地信任未知证书。置信度高；集群级超时回执待验收。

## MR-016 · P2 · PKI 续签失败阻断独立 CRL 刷新，提前中断仍有效的工作负载证书

**触发与影响：** rotationLoop 在证书寿命约三分之二后每轮只调用 refresh。Kubernetes 登录或签发接口失败时，refresh 在调用 refreshCRL 前返回。即使公共 CRL 接口正常，也不再读取它；五分钟后缓存超过 CRL freshness 上限，getCertificate 拒绝尚未到期的证书，产生本可避免的业务中断。

**位置：** `internal/integrations/openbao/pki.go` rotationLoop、refresh、refreshCRL、getCertificate。现有失败保留证书和 CRL 过期测试分别覆盖，没有组合覆盖。

**证据与边界：** [openbao-lifecycle-probe.log](evidence/openbao-lifecycle-probe.log) 使用真实 PKI 逻辑、可控时钟和 loopback TLS OpenBao 夹具：续签失败后 CRL 读取次数不增加，仍有效的叶证书因旧 CRL 被拒绝；单独刷新同一可达 CRL 后恢复。测试直接调用与循环一致的分支，没有真实计时等待，也没有真实 OpenBao/集群。不能通过放宽 CRL 或证书过期规则修复。

**修改与验收：** 让 CRL 刷新独立于签发成功与否，在续签失败时仍尝试刷新可达 CRL，并保留原有错误与健康状态。已吊销、CRL 真过期、叶证书过期仍必须拒绝。覆盖登录失败、sign 失败但 CRL 可达、CRL 同时失败、恢复签发、吊销命中和停止循环。置信度高，真实证书轮换联调待完成。

## MR-017 · P1 · API 到 Worker 的上下文丢失原生授权来源，阻断资源、证据与分析读取

**触发与影响：** 原生 operator 只有 identity.role_grants，旧 role_bindings 为空。API 在自己的请求 context 内成功重建原生 scope，但 callWithGrantDigest 签名的 graph.Claims 只有 subject/scope/digest/deadline，没有原生 user/version/source。跨 HTTP 后 Worker 没有 NativeSessionContext，graph.Authorization.Effective 无条件进入旧角色表分支并拒绝。资源列表/图、证据查询与回读、Victoria/DeepFlow 分析及 capabilities 的实际远端路径均受影响。原生管理员的审计列表能读取，但后台范围签名验证也用缺少原生身份的 RequireAdmin，显示 unavailable。

**位置：** `internal/httpapi/resource_handlers.go` callWithGrantDigest；`internal/graph/protocol.go` Claims、EffectiveTx、InternalHandler.ServeHTTP；`internal/app/sp04_runtime.go` serveWorkerEvidence；`internal/app/assistant_analysis.go` serveWorkerAnalysis；`internal/httpapi/audit_handlers.go` verifyAuditRange；`internal/app/sp08_audit_verify.go` authorize。MR-002 是持久 v2 Job 的同类独立缺口，单修 Job 不会修好这些 RPC。

**证据与边界：** [native-worker-node-probe.log](evidence/native-worker-node-probe.log) 通过真实 SignContext/VerifyContext，再用 Worker 的无浏览器 context 调真实 EffectiveTx，确认读取空的 legacy binding 并拒绝；假事务只模拟表结果，没有数据库或集群。所有列出的生产调用点均静态核对。没有证明权限绕过或资料泄漏；当前行为是合法原生用户被拒绝。legacy 角色能够通过此边界，不受同样故障。

**最小修改：** 扩展现有可信签名上下文，明确原生/legacy 来源，绑定 tenant、subject、user、当前授权版本及所需会话版本证明；Worker 用现有共享锁的原生授权重建函数核对当前权限，不能从 subject 前缀猜来源、复制旧角色或直接相信 scope。审计 RPC 使用原生 admin 对应复核。同步交互式请求及可信 v3 Job 转发两条生产链、内部 Schema 与签名/兼容测试；不转发 Cookie、密码或可重用凭据。

**验收：** 原生角色表空旧绑定下真实 API→Worker 资源列表、详情/图、Evidence、源统计、助手 source capabilities、审计验证成功；原生停用/撤权/版本变化、中途撤权、篡改或缺来源、租户/集群越界仍拒绝。旧编码只能走明确 legacy；禁止回退借权。置信度高，完整 API/数据库/Worker 联调待完成。

## MR-018 · P2 · 资源指标判断使用 QueryResult ID 作为 Step ID，来源定位始终失败

**触发与影响：** 故障链 evaluator 处理 execute_analysis_query 时，外层 id 是已提交 Step，内层 `for _, id := range data.QueryResultRefs` 将它遮蔽。调用 AssessResourceQuery 时传的是 QueryResult ID，该函数把参数保存到 supports/conflicts。前端把这两个数组作为来源 Step ID 请求 resource-source-step，仓库只接受同 Job 的 steps.step_id/evaluator sourceSteps，故返回拒绝。判断与 FactRefs 仍可显示，来源列表也仍可手工找，但判断卡片的直接证据链接不能使用。

**位置与证据：** `internal/investigation/fault_chain_committed.go` execute_analysis_query 分支的 AssessResourceQuery 调用；`resource_correlation.go` Supports/Conflicts 赋值及 ReadResourceSourceStepTx；`web/src/features/assistant/ResourceCorrelation.tsx` locate 按钮。这是直接变量绑定与接口契约证明，没有把单函数测试当作数据库复现。现有 resource_correlation_test 只测历史状态，没有覆盖指标判断来源。

**修改与验收：** 保留并传入外层 Step ID，给内层 QueryResult ID 使用明确变量名；FactRef 继续绑定 QueryResult ID。对真实 EvaluateCommittedFaultChainTx 输出验证每个 supports/conflicts 都属于 sourceSteps.stepId，并用 API 读回该 Step；同时验证 factRefs 能定位对应查询，其他 Job、未知 ID 仍拒绝。置信度高。

## MR-019 · P2 · Node Ready 从 Unknown 恢复 True 后，Unknown Finding 永不自动恢复

**触发与影响：** Signals 在 Ready=Unknown 时创建独立规则 NodeReadyUnknown 的 firing candidate；Ready=True 只产生 NotReady 的 resolved candidate，两者 SignalKey 不同。SubmitSP05Candidate/PollIdentity 按规则与资源键持久化 occurrence，仅明确 resolved 才关闭旧故障。因此 Unknown occurrence 持续 firing，相应 Incident 的 allResolved 永不成立，即使节点已经 Ready=True。

**位置与证据：** `internal/inspection/kubernetes/official.go` Node Ready 分支；`internal/app/sp05_candidate.go` key；`internal/finding/poll.go`；`internal/incident/state_machine.go` RecoveryPass。 [native-worker-node-probe.log](evidence/native-worker-node-probe.log) 对同 UID 的 Unknown→True 调用真实 Signals/SignalKey，确认健康结果没有旧 key 的 resolved。测试未执行数据库；持久化与恢复影响由现有代码链证明。

**修改与验收：** 保持历史 occurrence 身份，健康 Ready=True 时显式结清 NodeReadyUnknown 与 NotReady；核对 False/Unknown 间转移的语义，不把未知当健康。已卡住记录应基于当前可信原生观察走正常 resolved 路径，不能批量改库为健康。覆盖 Unknown→True、False→Unknown→True、重复健康、旧乱序样本、source 中断、UID 替换；真实 reducer 最终应在正常 settle window 后恢复 Incident。置信度高。

## MR-020 · P2 · 重建 MFA Challenge 可以重置失败次数上限

**问题与触发：** 已持有有效原生 full/LoA1 会话的调用者可以反复 POST 创建同一种操作的 Challenge，再提交错误 TOTP 或密码。最终生效的 `step_up_challenge_create` 撤销旧 pending Challenge，却给新行重新设置 `attempts=0 / attempt_limit=5`；失败函数只更新当前 Challenge。已有账户登录失败锁定和登录入口限流没有覆盖这两个 Step-up 入口。六位 TOTP 的在线猜测预算因此能通过更换 Challenge 重新获得。

**位置：** `migrations/00052_native_mfa_lifecycle.sql:575–593`、`:185–219`；`internal/identity/mfa.go:380–419`、`:453–463`、`:548–553`；`internal/httpapi/native_mfa.go:242–290`、`:331–370`。

**证据与边界：** 已追踪最终 SQL 函数替换、Go BeginChallenge/FailChallenge/VerifyTOTPStep、HTTP Create/Verify 路由和现有登录限流。创建挑战只要求当前会话，校验接口允许先提交 TOTP；没有要求先成功验证密码，也没有跨 Challenge 的账户失败预算。该结论是静态状态转移证明，未运行真实数据库或在线暴力尝试；不宣称已绕过 MFA。TOTP 的成功时间步消费仍存在，不能因本问题移除重放保护。

**最小修复：** 沿用 PostgreSQL 持久计数方式，在账户与因子层维护跨 Challenge、会话和 API 实例的失败窗口/冷却；挑战替换不能重置预算。创建与验证都检查当前预算，失败计数原子递增，成功仅按明确策略解除，保留现有挑战 revision CAS、版本绑定和一次性时间步消费。密码 Step-up 也应纳入统一的账户失败预算，避免只修 TOTP UI。

**验收：** 真实 PostgreSQL/API 用例连续五次失败后换 Challenge，第六次仍被拒绝；覆盖两个并发会话、两个 API 实例、过期/替换、正常密码+TOTP 成功和成功码重放。既有登录及恢复码锁定保持独立、可观察。置信度高；风险前提是已经取得有效会话。
