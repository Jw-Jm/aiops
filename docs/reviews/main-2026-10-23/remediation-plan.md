# 修改方案与验收顺序

基线为 `a2fa14f419abdc4587f56a1cbae59eb6cbb48c4e`。本文件是待实施方案，本轮未修改业务实现。目标是修复 [20 项已确认问题](findings.md)，保留现有模块化单体、双角色、持久预算、人工执行及非虚拟化边界。

## 实施顺序与交付拆分

1. **账户完整性与失败预算：MR-014、MR-020。** 先修 MFA reset 的密码版本不变量，并使 Step-up 失败额度跨挑战/会话生效；两项都须新增前向迁移与真实数据库反例，分别审阅。
2. **原生跨进程授权：MR-017。** 补签名 RPC 的可信来源/用户/版本证明及 Worker 对应复核，是资源、来源分析和审计验证的基础。
3. **持久业务授权：MR-002、MR-012。** 分别修 v2 Job provenance 与 Incident 写事务内原生权限复核；在 MR-017 完成后验证真实工具读写闭环。
4. **高权限人工命令：MR-003。** Ack、Prepare、Dispatch、Claim 一次贯通；前后端与必要持久 proof 同步交付。
5. **问数、事件及状态正确性：MR-001、MR-004、MR-011、MR-013、MR-018、MR-019。** 修共享成本合同、原生 SSE、请求归属、来源 Step ID 与 Node Unknown 恢复。改动可以分别审阅；问数与调查的最终联调依赖前面的身份修复。
6. **历史、资源与恢复：MR-005、MR-009、MR-010、MR-015、MR-016。** 历史关系先按集群筛选，密码工作共享并发额度，指标接通真实状态；OpenBao 不确定写入恢复和 CRL 独立刷新分别验证。
7. **最终 Schema 与材料收口：MR-007 → MR-008 → MR-006。** 业务新增迁移定稿后统一前滚、生成模型、更新迁移准入；按最终代码重建两个架构运行闭包。不能先替换摘要掩盖尚未完成的资格证据。

每个变更应带真正的业务回归断言，不能只检查源文件出现某个字符串。最终再运行一轮完整本地检查；单项已经通过后，无新改动或新失败不重复执行相同全套检查。

## A1 · MR-001：使 Graph 查询使用其真实成本合同

**修改点：** `internal/analytics/cost.go` 和相邻成本测试；`internal/investigation/mcp/server_test.go`。

- 将 catalog 查找、operation 合法性检查提前，按来源分支验证输入。
- 对当前 Graph 聚合返回固定成本 1。当前数据集继续拒绝历史窗口；Graph 编译器仍负责完整语义检查。
- Victoria / DeepFlow 的聚合、比较、趋势维持已有成本公式。不能为了容纳 Graph 而放宽所有来源的时间要求。
- 确保 MCP 的预留函数与实际查询使用同一个冻结 intent；没有成功预留就不请求来源。

**关键测试：** 用生产 Compile 接受的无时间范围 `resources.current` 输入调用 MCP；预算剩 1 时成功执行且消费 1，余额 0 时来源调用数为零。保留比较成本 2、日志 50 点成本 50、网络半开桶与非法 step 拒绝。现有成本测试中带时间窗口的 Graph 样例应改成实际合法样例，并校验它能编译。

**兼容与恢复：** QueryIntent 对外结构不变，旧 Job 预算不补发、不重置；修复不自动重跑已经失败的用户任务，用户重新发起独立查询。

## A2 · MR-004：让调查 SSE 沿用共享认证

**修改点：** `web/src/api/sse-reader.ts`、Node SSE 测试与 `useInvestigationStream` 对应测试。

- 删除特定 reader 的 Bearer-only 前置判断，继续透传 requestOptions 的 Cookie/Bearer、signal 和游标。
- 不新增第二套认证状态或 token 存储；未登录情况下由现有共享客户端与服务端拒绝。
- 保留 sequence gap 拒绝、重复事件丢弃、410 游标失效和身份变化立即中止。

**关键测试：** Cookie 请求带 same-origin；OIDC 请求带原 Bearer；两者各接收至少两条真实格式事件。服务端返回 401/403 时停止重连并清缓存，410 刷新持久状态，切换用户后缓冲中的旧事件不渲染。浏览器验收确认状态从 connecting 进入 connected，网络中确有事件请求。

**兼容与恢复：** 不改变 SSE envelope、eventId 或签名游标；已有轮询兜底保留。此项不能替代 MR-002 的任务执行修复。

## A3 · MR-011：切换范围后解除旧请求的忙碌状态

**修改点：** AssistantPage 的 scope effect、mutate 和相邻范围集成测试。

- 范围切换取消旧请求时，同时清除该范围的 busy。旧请求完成不得重写新范围的表单、缓存、URL 或错误。
- 通过现有 scopeRef/abort 加最小请求归属检查；如果新请求已经开始，旧 finally 不得清除新请求的 busy。无需新状态库或请求管理层。
- 验收必须同时断言“旧响应不提交”和“新范围可再次操作”，覆盖旧请求成功、失败及取消；在 AppShell 真实集群选择器和直接 ClusterScopeProvider 两种夹具中复核。

**兼容：** API、会话和 Job 不变；用户是否丢弃草稿仍沿用既有交互。无需迁移。

## A4 · MR-013：统一故障链提交的会话归属检查

**修改点：** FaultChainCollector、FaultChainGapCheck 和 AssistantPage 的 onSubmitted 调用点，必要时连同 BindingEditor 的迟到回调一起核对。

- 所有写回父页的回调必须证明当前 conversationId、范围和请求归属仍与发出时相同；不能只依赖身份 epoch 或子组件 key。
- 用现有 signal/ref 取消卸载组件的请求，完成后仍执行归属检查；请求取消不代表服务端 Job 没有创建，持久结果仍从原会话读取。
- 两类提交分别测试 A pending 后进入 B，再成功/失败/取消；B 不出现 A 的 turn/status/answer，也不因为旧响应读取 A 的当前轮次。
- 保持服务端幂等键和 conversation revision 不变。此项无数据库迁移，和 MR-011 可以同一前端变更交付，但两种错误必须各有断言。

## B1 · MR-002：持久化 v2 Job 的身份来源

**建议实现：** 采用与现有 action authority/v3 Assistant authority 一致的最小持久来源字段，不发明另一种授权框架。优先把 provenance 保存在数据库和内部 Job 模型，保持既有 v2 对外 JSON 合同稳定。

1. 新增前向迁移，为 investigation Job 存储身份来源、原生 userId、authorizationVersion。已有记录默认只能解释为 legacy；不能凭 subject 前缀猜测来源。
2. CreateJob 从已认证 context 取得来源，拒绝客户端传入来源；入库与业务 Job/队列/审计同事务提交。
3. load/current/Claim/Heartbeat/Context 续签/工具授权/恢复读取/Complete 均按该来源复核。原生来源走已有持久 native operator scope，旧来源走旧表；未知或不完整来源直接拒绝。
4. 复用现有 v3 工具装配中对持久身份的可信上下文传递，使 Graph/数据来源也按同一身份复核。只修 Claim 会将失败推迟到工具阶段，必须追完 Graph、Evidence 和 Job API。
5. 冻结的 scope digest 继续绑定 source/tenant/cluster 的当前 revision；账户撤权、重新授权版本变化不能让旧 Job 复活。
6. 若最终实现确实需要改变外部 Job JSON，必须同步 OpenAPI、Go/TS 绑定、InvocationContext、Python `validate_dispatch` 和严格 Schema；不要让一个额外字段被 Python 拒绝。可用内部非序列化字段完成时，不扩协议。

**必须通过的集成路径：** 只在 identity.role_grants 中授予 operator，旧表没有镜像授权；真实 API 创建 v2 Job，再用无 Session Worker context Claim，调用一个真实受控工具并完成。分别加入 source revision 改变、权限撤销、撤销后重新授予、用户停用、跨租户和 Worker 接管。

**历史数据处理：** 不回填历史 Job 的 requesting_subject 或旧冻结 scope。当前失败记录保留；为重新调查创建新 Job，不把失败任务直接改回 queued。新增 Schema 必须可让旧数据正常读取，回滚应用时不得执行 Down 或删除 authority。

## B2 · MR-012：Incident 写事务使用原生权限来源

**修改点：** incident.Authorize/AuthorizeAll、manualEvidence 及 HTTP 幂等准入的共用调用点；保留现有 legacy SQL。

1. 对可信原生 context，复用当前 native role/account/grant 的事务共享锁及 scope 重建；必须在业务写事务内完成，不能仅在 handler 前置检查。
2. 当前 scope 必须允许 Incident 主资源、全部关联 Finding 和每条人工 Evidence；比较租户、集群、namespace 与 resource 身份，不凭 subject 前缀判断来源。
3. manualEvidence 当前再次直接调用旧锁函数，须接入相同分支。保持 source revision 锁、可信时间、归档验证和 evidence retention 校验。
4. legacy 请求维持原函数的权限和锁序。不改写已应用 00028，不创建原生角色的旧表镜像。
5. 真实数据库覆盖原生唯一授权与旧表为空，完成确认、抑制、合并、拆分和幂等重放；加入并发撤权、停用、跨租户/集群和不同 namespace 的拒绝用例。

**兼容与数据：** 不改写 Incident owner、历史时间线或 Finding 归属，不重跑旧失败写入；客户端按当前 revision 重新提交人工操作。若为锁序或最小权限补 SQL 函数，采用新前向迁移并同步集成及发行材料。

## B3 · MR-014：修复 MFA reset 的密码版本完整性

- 新前向版本替换最终 mfa_admin_reset，在同一目标账户锁内保存原密码 hash 到新版本，退役旧版本，再递增账户版本、撤销 Session/proof 并要求重新登记。
- 不返回或重新散列原密码，不需要用户/管理员提交新密码；角色授权与原始 owner 不改。
- 对既有断链，使用 reset 审计、原始版本和凭据记录提供可核对的定向修复，异常或多次改密不能猜测回填。
- 补真实数据库 HTTP 测试：reset 成功后原密码仍能登录、旧 Session 全拒绝、新 Session 受 MFA setup gate 限制、重新登记后恢复；加入并发 password reset、重复 MFA reset、停用与跨租户反例。

## C · MR-003：使 MFA 证明贯穿一次命令授权

**接口与确认阶段：**

- 前端在高权限风险确认请求中传入当前已验证 challenge 引用。command、target、profile、policy 或 assessment 变化时清除挑战与风险确认状态。
- AcknowledgeRisk 在事务内从 assessment 派生 `NativeCommandStepUpTarget`，构造与 Prepare 相同的 `NativeStepUpIntent`；不能接受浏览器自报的 LoA2 或只检查 header 是否存在。
- Ack 与 Prepare 都必须检查实际 assessment/command binding；风险确认 5 分钟有效期和一次消费语义不延长。

**异步派发阶段：**

- Prepare 在创建 execution 的事务里冻结可重新证明的 proof 引用、用户/会话/凭据/授权版本及精确 binding；保存引用与摘要，不保存 Cookie、密码、TOTP 或 Bearer。
- BeginDispatch 和 Runner Claim 根据持久 authority.Source 选择验证分支。原生分支使用现有 proof/recheck SQL 语义，校验操作、binding、期限、撤销和当前权限；legacy 分支保留 Keycloak 检查。
- 不可用 `frozen.Source == native` 直接赋值 StepUpVerified。若 proof 不能在无会话 context 中查验，补最小固定查询函数与显式权限，禁止对 identity 表泛化提权。
- 保持执行记录先持久、attempt 不重复、dispatch token 单次 Claim；未知执行结果不自动重试。Post-check 仅作后检查，不能借此重新派发。

**改动范围：** CommandComposer、action service/native_stepup/authority/runner_protocol、必要 identity SQL 与最小前向迁移、内部模型、API header/生成绑定（合同若有变化）、单元与真实数据库集成测试。

**必须通过的反例：** 真实高权限正常流程；错误 challenge、其他用户 challenge、过期、撤销、命令字节变化、profile/policy/目标 revision 变化、Prepare 后禁用账户或改密码、并发 Ack/Prepare、重复幂等键与重复 Claim。确认失败时不得有 attempt、Runner、短期凭据或外部命令副作用。

**兼容性：** namespace 既有命令、legacy 高权限命令都要回归。历史 execution authority 缺省仍只能走旧来源；不能把读历史的兼容路径用于授予新派发权限。

## D1 · MR-005：先限定集群，再判断历史关系唯一性

**修改点：** `internal/resourcestore/observations.go` 的唯一查询边界。先解析 anchor，使用其不可变集群映射和已知 kind/group/UID 限制候选，再保留 LIMIT 2 和唯一性判断。不要先在整个租户查两个名字相同的对象。

**关键测试：** 同租户两集群的同名 Node、PVC、PV、Deployment owner；一个正确目标和一个错误 API group；同集群真正重复候选；目标被删除或 observed_at 晚于来源观测。必须同时证明正确关系被保留、错误关系不建立。

**数据处理：** 旧 observation 的缺失关系无法从当前状态证明，不进行自动历史回填。新采集采用修正查询；历史报告继续展示明确缺口。只有拥有同一时刻、同一 source revision 的原始可靠材料时，才另行设计可追溯的补充观测，不能覆盖旧记录。

## D2 · MR-009：给密码工作增加共享并发上限

**修改点：** 在现有 identity 密码工作边界使用标准库有界槽位，避免每个 handler 各开一个互不共享的限额。不新增依赖或通用任务执行器。

- 一个 API 进程内登录、dummy 验证、密码修改、创建/重置密码、MFA password step 共用额度。
- 先以小固定并发值实现并受默认 512 MiB 配额实测；参数依据常驻内存和散列峰值选定，不降低密码强度迁就内存。
- 槽位获取支持请求取消；拥塞不无限堆积等待者。记录脱敏限流指标，返回可重试错误并保留已有分钟频率限制。
- 已开始的 Argon2 工作不能因 HTTP context 取消就被假定立即停止；必须等实际返回后释放槽位。

**验证：** 用可控阻塞工作验证并发计数、异常与取消归还；实际多请求测试默认配额的 RSS、OOM 和健康检查。对 unknown/known 用户保持同一工作边界，避免重新引入用户名时序差异。

## D3 · MR-010：接通采集降级状态

**修改点：** observability metrics 与 SP05 worker/official observations/analyzer 调用点及测试。

- 先确定采集业务健康和遥测导出健康是否共用现有 gauge；避免一个指标同时表达不兼容语义。
- 将运行配置内的来源状态接入一个有限集合，保存每个来源/组件的故障与恢复，再聚合 any。不要把所有来源映射成同一个可覆盖的 bool。
- 未配置来源不得增加任意状态项；来源下线/配置替换时清除其旧状态，支持并发更新。
- 测试需真实 Gather 指标，并验证两个来源失败、其中一个恢复仍降级、全部恢复后正常；保留 audit_signing、metrics_listener 与 tracing 的原语义。

**验收：** 每类采集故障都能在 scrape 中观察到；恢复不互相覆盖。真实故障注入另记运行回执，本轮探针只证明接口键不匹配。

## E1 · MR-007：迁移集合与发行基线统一

1. 在 B、C 的新增迁移定稿后，确认实际最终最大编号 N；当前基线 N=63，修复后不能仍硬编码为 63。
2. 查询共享开发环境的已应用版本并记录文件身份；已应用迁移不得重写，纠正 SQL 一律新增前向版本。
3. 逐项处理 00047–00063 的 Down 与旧门禁冲突：00055–57 为主动抛错，00058 有实际 DROP FUNCTION，其余新增区段为注释。已应用文件保留原件，参照 00038 既有精确冻结例外逐项记录语义，静态规则继续拒绝任意新增/修改的 Down；迁移运行入口明确且测试只允许 Up/UpTo。未应用文件才可整理为仅 Up，不可把全部区段当作“空标记”放行。
4. 同步 delivery-baseline 的 count/latest、源码归档 SQL 集合和准入记录；旧 verification 保留为历史时注明其基线，新版本生成新的当前回执。
5. 分别从空库前滚、从已有数据库版本前滚；验证 identity/Job/action 数据保留、函数 EXECUTE 权限、RLS、双角色和锁序。
6. 生成迁移源码材料并离线重建迁移工具，再按架构补齐准入所需运行证据。

**验收入口：** `TestMigrationScriptsAreForwardOnlyAndContiguous`、`TestDeliveryPreparation*`、migration-source check、真实 PostgreSQL native/legacy 集成。仅更改 JSON 计数不构成完成。

## E2 · MR-008：重新生成 sqlc 模型

Schema 定稿后执行 `go generate ./internal/persistence`。审查与 [sqlc-models.diff](evidence/sqlc-models.diff) 相比的最终差异，提交生成模型；保持 handler 使用安全投影，不能直接输出含 password_hash、token_digest 或 secret_ciphertext 的生成类型。

执行 `make check-generated`，确认重新生成可复现。不能通过手改 models.go、忽略 identity Schema 或关闭生成检查来完成该项。

## E3 · MR-006：重做最终运行时源码准入

- 用最终 API/Worker/opsctl 入口分别生成 arm64/amd64 编译闭包，核对 Argon2/Blake2 与 amd64 CPU 文件和许可证来源。
- 按既有独立准入流程准备源码归档、SBOM/notice 与二进制；更新相匹配的 Catalog、admission、Bundle manifest 和对应构建输入。
- 运行时源码归档与迁移源码归档分别验收；它们包含的第一方/第三方闭包不同，不能复用彼此摘要。
- 默认及 amd64 的 source check、Bundle 构建/导入/验证、空缓存离线重建均须有当前证据。
- 真实断网安装、干净重装属于交付验收；未做时仍记待验收，不借本地测试宣称通过。

## F · MR-015、MR-016：OpenBao 生命周期恢复

**修改范围：** internal/integrations/openbao/bootstrap.go 的 EnsureBootstrapTLS；pki.go 的 refresh/rotationLoop/refreshCRL，以及同包生命周期测试。两项分别审阅，无业务 Schema 迁移，不更改信任根或证书有效期策略。

MR-015 先修写请求结果不确定时的材料保留和读回确认：本次 CA/叶证书必须与已保存 Secret 一致；确认未提交才清理，未知结果保留并返回可恢复错误。已有异源 Secret 不覆盖。不要把私钥或 Secret 内容写入审计日志。把诊断中的“已提交但客户端失败”作为真实边界回归。

MR-016 在既有轮换循环中独立执行 CRL freshness 维护，续签失败不阻断 CRL 请求；不新建服务或后台框架。错误分别记录，健康状态能区分签发故障与撤销列表故障。采用可控时钟验证两个故障组合，不依赖长时间 sleep；完成 loopback 验证后在非生产环境核对轮换、吊销及恢复回执。

**历史恢复：** 如果 MR-015 已删除 CA，必须从原受保护的材料副本恢复并验证叶证书链；无法证明原信任根时应走明确的重新置备流程，不从同名 Secret 自动建立信任。MR-016 不更改已吊销证书或过期 CRL 的拒绝语义。

**验收：** apply 已提交/未提交/无法确认、同名异源 Secret、重复启动和中断；登录失败/sign 失败但 CRL 可达、CRL 同时失败、吊销命中、叶证书到期、成功续签及停止循环。分别证明故障被保留、正确重试能恢复、异源身份仍拒绝。

两项不要求恢复已延期的 KubeVirt/CDI 阶段。

## G · MR-017：贯通原生 API→Worker 身份证明

**修改范围：** graph/protocol.go 的 Claims/验证与共享授权入口；httpapi/resource_handlers.go 的 callWithGrantDigest，app/sp04_runtime.go 的 Evidence RPC、assistant_analysis.go 的分析 RPC，以及审计范围验证双方。同步 api/internal/graph-v1.yaml、gen/internalgraph 和签名协议夹具；新增 SQL 仅在现有窄函数确实不能满足权限时进行。

先在 graph.Claims 及内部合同内定义受签名保护的来源/身份/版本字段，再补共享 Worker 授权入口，逐一接入 Graph、Evidence read/query、源分析/capabilities 和 Audit verify。交互请求来自可信 NativeSessionContext；v3 Job 转发来自已持久化、已重证的 AssistantTask authority。两条来源都不能由工具参数代填。参考 EffectivePersistedNativeTx/native_authority_shared 的既有锁序与版本约束；确有数据库权限缺口时新增窄范围前向函数，不提升 Worker 为 API/owner 角色。

复用既有 30 秒以内请求签名、body digest、mTLS 与最终撤权复核；缺失字段编码只明确兼容 legacy，原生永不回退旧表。同步独立生成的 internal graph 类型和协议 fixtures，实际联调后才能宣布原生业务链可用。先完成此项，再验收 MR-001/002/012 依赖的真实交叉链路。

**兼容与验收：** API/Worker 按同一协议版本更新；未知来源或不完整证明拒绝，不能允许新 API 借旧 Worker 的 fallback 授权。覆盖两种人类来源和持久 Job 来源，操作员/管理员分别测试；无旧表镜像时正常读取，停用、改密、撤权再授权、中途撤权、过期、body/scope/source 篡改与跨租户均拒绝。审计 admin 复核不授予 operational operator 权限。保留已有 scope digest 和旧历史记录。

## H · MR-018、MR-019：修正持久来源与故障生命周期

**修改范围：** MR-018 为 investigation/fault_chain_committed.go 的 execute_analysis_query 分支及 evaluator/API 来源定位测试；MR-019 为 inspection/kubernetes/official.go 的 Node Ready 投影，以及 Finding/Incident 真实生命周期回归。两项均优先复用既有合同和持久键，不新建版本或改写历史身份。

MR-018 仅修 evaluator 调用参数，不重建已有结果或重写历史 Step。现有错误历史判断需明确来源不可定位，新的轮次应得到正确 Step 引用；需要修历史时另做可追溯的映射验证，不能用 QueryResult ID 冒充 Step。

MR-019 在既有 Node 条件投影中补旧规则的明确恢复候选，不引入缺失样本推断。当前可信 Ready=True 观察应通过已有 PollIdentity/Archive/Ingest/Relay 结清旧 occurrence，仍保留源撤销、顺序与 UID 约束。端到端验证 Finding resolved 与 Incident settle/recovery，不能只验证新数组多一项。

**验收：** evaluator 的每个 supports/conflicts 都必须是本轮 sourceSteps 的实际 Step ID，并从正式 API 读回；FactRef 仍定位正确 QueryResult/Fact，其他 Job 或未知 ID 拒绝。Node 覆盖 Unknown→True、False→Unknown→True、重复健康、乱序/过期观察、来源中断和同名新 UID；恢复只作用于相同资源身份的旧 occurrence，按正常 settle window 结束 Incident。

## I · MR-020：账户级 Step-up 失败预算

**修改范围：** 最终生效的 identity.step_up_challenge_create/失败更新/因子验证 SQL、新前向迁移、identity/mfa.go 与 native_mfa handlers 的统一错误映射及真实数据库测试。HTTP 入口继续要求已有会话、CSRF、Origin 和业务 intent；不让浏览器提交额度策略。

新增前向迁移保存账户/因子失败窗口，复用现有身份函数的窄权限与账户锁序。Challenge 创建、密码/TOTP 验证统一检查预算，失败在同一事务累计；新 Challenge 不恢复尝试额度。前端继续展示服务端剩余额度及冷却时间，不能靠按钮禁用实现限制。补跨挑战、跨会话和并发真实数据库回归，同时保留 TOTP 一次性消费、挑战过期、撤权和历史会话拒绝。无需新增限流服务。

**原子性与兼容：** 账户/因子计数跨 API 实例共享；替换/过期/注销/新会话不能清零。统一核对 Challenge revision、凭据/授权版本、失败累计与成功时间步消费的锁序。成功解除/冷却策略在服务端固定并测试，错误提示不暴露密码/TOTP 内容。已有五次单 Challenge 上限继续有效，新计数从前向迁移生效时累积，不伪造迁移前攻击记录。

**验收：** 五次失败后更换 Challenge，第六次仍拒绝；两个会话和两个实例并发，失败累计不能丢失；过期、替换、换操作、冷却边界与正常双因子成功；成功码重放、错误用户/租户/版本仍拒绝。既有登录失败锁定和恢复码锁定不能因本项被清除或互相覆盖。

## J · 每项实施的共同边界

**分清修复与历史恢复。** MR-014 的已有断链必须按审计及明确原版本定向恢复；无法证明来源时先由现有受审计的密码重置流程处理，不猜测“最近一条 hash”。MR-005、018 的旧历史观测/引用不能用当前状态回填成当时事实；MR-019 使用新的可信正常观察结清旧 occurrence。旧失败 Job、未知命令执行结果不自动重跑。

**最小权限与兼容。** MR-002/003/014/017/020 若需 SQL，新增前向迁移，保持 API/Worker/迁移角色分离及原有函数最小 EXECUTE；不得改已应用 SQL、镜像原生角色到旧表或提升 Worker 为 owner。未改变对外合同的修复不新建 Schema 版本；确有字段变化时，按调用链同步 Schema、Go/TS 生成物、Python 严格校验与 fixtures。兼容缺字段只允许确定的 legacy 记录，原生复核失败不能回退借权。

**可观测而不扩张。** MR-009/010/016 复用已有指标和错误框架，不新增服务、任务调度器或状态库。限流、来源降级和证书故障分别保留可解释的失败；健康状态恢复不得依赖隐藏错误、放宽 CRL 或延长业务预算。

**交付验收分层。** 每项先运行足以覆盖触发与反例的单元/合同检查，再按依赖完成原生/legacy 数据库/API/Worker 联调。全部业务变更定稿后只做一次完整本地检查与生产构建，然后在独立环境记录当前迁移、源闭包、二进制和 Bundle 的运行回执。已有静态失败、旧回执或跳过项不能转为当前 PASS。

## 完成条件

- 20 项问题各有对应修复、必要回归和审阅记录；如果任一项经真实反证否定，保留否定依据，不改成无证据的“已完成”。
- `make check` 当前版本全通过；前端生产构建通过。
- 原生与 legacy 身份下的调查、MFA 命令和权限撤销完成数据库/API/Worker 联调。
- 当前材料可离线重建；最终源闭包、迁移、二进制和 Bundle 准入一致。
- 未引入角色继承、自动命令执行、未知执行自动重试、虚拟化恢复或对根目录正式方案的修改。

没有必要为本轮新增服务、工作流框架、依赖或大范围文件拆分。主要成本在把已有边界贯穿到新身份链路，以及补齐当前版本的真实验证证据。

