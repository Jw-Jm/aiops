# P0 实施裁决与缺口（2026-10-21）

代码基准与工作区见 [baseline-code.json](evidence/baseline-code.json)，远程只读记录见 [remote-environment.txt](evidence/remote-environment.txt)。原始方案文件保存在 original-plan/；不改变其“待实施”结论。当前状态以 progress.md、task-progress.csv 和 acceptance.csv 为准。

## 当前核实深度

458 个 spec.json、SPEC.md、recipe.js、replay.html 已逐项读取并与固定 manifest 的标识、入口核对；P0-02 初审后进一步读取每片 index.html 的实际 DOM 表单与字段。157 个动作按阶段绑定、10 条逐片 confirm、54 种表单（含 8 种 DOM-only）收敛到实际请求或本地交互、处理器、服务、存储/Worker 与结果投影，证据见 [act-bindings.csv](evidence/act-bindings.csv)、[form-bindings.csv](evidence/form-bindings.csv)、[confirm-bindings.csv](evidence/confirm-bindings.csv)、[form-inventory.csv](evidence/form-inventory.csv)、[state-bindings.csv](evidence/state-bindings.csv)。[contract-audit.csv](evidence/contract-audit.csv) 逐合同定位处理器、可复用服务、存储/Worker 和现有调用方；[form-contracts.csv](evidence/form-contracts.csv) 保留表单到合同的摘要。

这仍是静态源码核实，不是运行验收。绑定的 `direct` 表示现有请求的字段和语义已覆盖，不代表已联调；`adapt` 表示需映射或扩展；`missing` 表示没有公开合同，已写主任务和阻断范围。acceptance.csv 的六项本轮仍全部为 `not_verified`，那属于后续按切片验收；G0 不能通过的原因是本文件映射复核未关闭且无独立签核。

## P0-02 逐动作核实结果

### 覆盖与裁决

| 口径 | 数量 | 说明 |
| --- | ---: | --- |
| 状态 | 458 | 与固定 manifest 完全一致 |
| 绑定面 | 3888 | 2909 个动作实例 + 435 次表单提交 + 544 个 form 外控件；54 种可见表单、371 条逐片记录 |
| 阶段 | open 533 / prefetch 639 / submit 571 / confirm 46 / local 2099 | 逐片 stage 列；open/confirm 不产生请求 |
| 动作面裁决 | local 2616 / direct 255 / adapt 693 / missing 324 | 逐片记录在 state-bindings.csv |
| 阶段动作 | 157 | open 59 / prefetch 32 / submit 11 / confirm 12 / local 43 |
| confirm-yes | 10 | 逐片 mutation：4 direct、6 missing；不设通用 local 行 |
| 不同表单 | 54 | local 1 / direct 3 / adapt 19 / missing 31；含 8 种 DOM-only 表单 |
| 主读取 | direct 355 / adapt 26 / missing 77 | 对话框表单用候选读取覆盖 route 推断 |
| 复合状态 | 62 | 31 个 active dialog/drawer 叠层 + 31 个跨模块入口；按 DOM 与动作面归属 |

裁决口径：

- `open`：只打开对话框/抽屉/确认框，无平台请求；提交绑定在 `form:` 或 confirm 行。
- `prefetch`：打开或切换时读取列表/详情/候选；明确 GET，不用 route 代替。
- `submit`：真正发出 POST/PATCH/PUT/DELETE 的提交动作。
- `confirm`：只打开确认框并复用已有 assessment/字段/revision；具体 mutation 逐片绑定。
- `local`：客户端状态、导航、剪贴板、对已授权已加载数据的 CSV 导出；不产生请求，也不放宽权限。
- `direct`：现有请求的方法、字段、状态和错误语义已覆盖。
- `adapt`：现有请求存在，但字段、分页、状态、投影或权限语义需映射/扩展。
- `missing`：无公开合同或处理器，须按主任务新增并持久化；不得用前端假数据或计时器替代。

初审修正：`ai-resource`/`ai-incident`/`observe-ai`/`session-create` 只打开 `session-form`，POST 在表单提交和 `composer`；`binding-create`/`cluster-create`/`command-create`/`incident-state` 只打开表单；`command-review` 只打开确认框并复用已有 assessment，命令创建在 SL-071 的 `confirm-yes`（C-057，request digest/profile 版本/目标绑定）。SL-106 的 `v11-alert-link-form` 由 `v11-alert-link` 从 `alertDrawer` 打开，字段 incident/reason/review，写 C-039（missing），候选读取 C-031；不再只按 `/events/alerts` 推断 primary_read 和 gap。

第二轮阶段修正：`session-rename` 只开 `session-rename-form`；`session-archive` 只开确认框并在 SL-297 的 `confirm-yes` 归档（C-079）；`native-change-password` 只开 `native-password-form`；`session-scope` 只开只读抽屉，不提交 rebind；`observe-log-bucket` 只过滤样例、`observe-device` 只改选择、`query-source` 只展示已有检查，均不再声称重新查询。`policy-validate` 只做本地结构预检，服务端 validation-only 合同为 C-177（拟新增）；发布仍由 C-156 独立执行，绝不以发布替代验证。

R6 修正：native-password-form 七片按实际 mode 绑定：SL-004/118/432/433 self 与 SL-005 first 使用 C-011；SL-094/370 reset 使用 C-017（userId 路径、initialPassword、expectedRevision/expectedCredentialVersion 分别取 account/credential 投影，服务端强制 change_required）；SL-005 固定原型缺 currentPassword，登记补充控件/状态 P2-02，不放宽 Schema。model-form 区分 create replace/update keep；v11-user-form 区分 C-015 create 与 C-016 update，update 不发送密码；binding-form 改为 C-020 的 userId/scope/expectedAuthorizationVersion/reason，旧 subject handler 仅作待适配复用。

R5 修正：SL-001/002 是未认证登录/失败页，主读取改为 C-009（缺失），只绑定可见 `login-form`（username/password），不读取 overview；SL-005 首次改密主读取 C-011。SL-009 主读取改为 C-004 neighbors，新增 `graph-root`/`graph-depth` change 绑定；全部 458 片补 544 个 form 外控件（cluster-picker/config-cluster/session-search/riskAcknowledged/stepup 等），按可见 DOM 和 change/input 语义绑定。SL-106 保留父 Finding C-030、候选 Incident C-031 与写 C-039，不以单条 primary 压掉关联读取。

### 主要 adapt 缺口

1. **P3-02 证据/观测**：`POST /api/v1/evidence:query` 为同步单次查询。已有 `resourceCanonicalId/type/queryTemplate/timeRange{from,to}/budget/sourceRegistrationId/namespace/limit`；缺查询 ID、异步任务、取消、个人历史、服务端分桶、关键词与级别过滤。结果缺生效来源 revision、资格、覆盖与窗口回显。SL-049 的查询中/历史/取消/分桶/级别筛选因此不能判 direct。
2. **P4-03 告警**：`GET /findings` 支持 state/severity/observedFrom/observedTo/resourceCanonicalId/incidentId/sourceSystem，但不支持自由文本 q；`GET /findings/{id}` 不返回 `finding.timeline`，告警“状态时间线”需投影。
3. **P3-01 资源**：`GET /resources` 与 `by-canonical-id`/`neighbors`/`impact-scope` 已具备 canonicalId、scope、graph revision；抽屉的“指标与证据”页依赖证据查询，没有按 tab 的分页/历史合同。列表也没有自由文本 q。
4. **P6-01 执行**：`/command-executions` 缺全局 GET；执行配置列表只对平台管理员开放，operator 没有可选配置投影；`command-form` 只做 risk-assess，acknowledge 是独立 C-056 请求，`command-review` 复用已有 assessment，命令创建只在 confirm-yes（C-057）；`relatedExecutionId/executionRelation` 有字段但语义未定义。
5. **P4-05 调查**：创建只接收 `triggerRevision/triggerKind=manual`；原型的 steps/timeout/focus/model 不接收，预算由服务端策略决定；RCA 当前/历史读取存在，但没有调查重点和模型固定版本投影。
6. **P5-02/P5-03 AI**：会话详情、消息分页、turn、来源步骤、停止、归档、固定引用均有合同；重命名 `missing`；rebind 只接收 namespace/资源/窗口，不含模型；模型选择由平台准入 Profile 固定。
7. **P7-06 配置**：策略草稿/发布/激活/退役存在；没有只校验不发布的合同；执行配置表单缺 `schemaVersion/version/clusterUid/tools/principal/hostOnboardingRef`，`label` 与 `name` 语义不同；无按版本读取详情端点。
8. **P7-01 接入**：集群/来源登记、更新、凭据轮换、来源回滚存在；没有连接测试，也没有组件只读检查；`environment`、`namespaces` 等原型字段需映射到服务端结构化字段。
9. **P3-02 就绪投影**：现可用 `GET /assistant/capabilities?clusterUid` 取得来源能力，但它不是观测专用合同；缺来源 revision/资格/覆盖/窗口字段，不能作为完整“查看来源状态”。

### 主要 missing 缺口与阻断范围

| 主任务 | 缺口 | 阻断范围 |
| --- | --- | --- |
| P2-02 | 原生登录/退出/改密 | 账号主链与所有真实登录状态；SL-001/002 等 |
| P2-04 | 用户创建/详情/编辑/停用/移除/重置/会话撤销 | 权限页与用户选择器；角色绑定只能手工输入 subject |
| P3-02 | 观测查询历史、取消、结果生命周期 | 日志/指标/网络工作台的历史、取消和异步过程 |
| P4-02 | 业务服务与责任归属 | 服务列表、登记、归属、相关故障 |
| P4-04 | 认领、分派、评论、复盘待办、告警关联修正 | 故障协作与跨模块告警抽屉 |
| P5-02 | 会话改名 | AI 历史会话操作 |
| P5-03 | 模型连接/测试/启停 | 四字段模型接入与运行路由 |
| P6-01 | 全局执行列表、人工复验尝试 | 执行历史与处置后验证 |
| P6-02 | 受控作业模板、预览 | 模板列表/编辑/使用 |
| P6-03 | 用户只读巡检任务、报告 | 巡检创建、进度、取消、重跑、导出 |
| P6-04 | 巡检计划、槽位派发、历史 | 计划启停、手动触发、执行历史 |
| P7-01 | 组件只读检查、暂缓状态 | 接入检查、能力缺口 |
| P7-02 | 缺口方案、受控部署、部署记录 | 按需部署与数据资格复验 |
| P7-03 | 通知通道/路由/静默/规则关联/投递重试 | 通知与维护窗口 |
| P7-04 | 知识文档/版本/导入/发布/归档/索引 | 知识列表、正文、版本与固定原文 |
| P7-05 | 知识检索测试与固定引用 | 检索测试、AI 引用原文 |
| P7-06 | 平台健康聚合 | 部署信息页；公开探针不能代替业务资格 |

### 复合状态

62 个状态标记 `composite=yes`：31 个由实际 active overlay 叠层判定（`active_overlays` 同时含 `modal` 与 `drawer`，如 SL-106/111/321/330/334/335/339/341/343/344/345/357/358/359/367/369/370/399/406/414/434/440/441/444/447/450/453/458，以及 `drawer`+`confirm` 的 SL-371/426/430），31 个由跨模块入口判定（SL-006/097、SL-062/298、SL-120/337/338、SL-194/206/214/222、SL-294、SL-312/402/409、SL-376/380/383/386/442/443/445/446/448/449/451/452、SL-424/429/454/455）。不能信 `spec.depth=0`；逐片 active_overlays、surfaces 与 primary_read 见 state-bindings.csv。

### 身份、版本与窗口关联

- 资源规范标识已嵌入来源稳定 UID；`namespace UID` 未建模，不能作为查询键，也不能在 UI 中伪造。
- `sourceRevision` 是 evidence 条目字段，不是查询入参或统一响应回显；历史读取需重新授权并核对来源。
- 观测时间窗为绝对半开 `timeRange{from,to}`，没有时区回显；原型“10-20 09:10–09:40”必须由 UI 换算为绝对时间并显示口径。
- 资源/证据读取使用 tenant 校验、operator 角色绑定、namespace/资源授权、scope digest 与 graph owner epoch/revision 复核；平台管理员不自动获得运维数据权限。
- 写操作使用 `expectedRevision`/幂等键；命令使用 request digest、确认摘要、profile 版本与目标 canonicalId 绑定。

### 合同清单订正

- 原方案 138 项（62 已有/76 拟新增）保留在 original-plan/，不重写历史。
- P0-02 发现 28 项 OpenAPI 已存在但原清单遗漏的操作，补为 C-140～C-167（会话详情/消息分页/turn/来源步骤/故障链、资源解释/解析、行动计划、法律保留、策略发布、来源回滚、RCA 当前/历史、事件合并/拆分等）。P0-03 另补 9 项拟新增操作 C-168～C-176（主体映射、MFA 设置/确认/恢复/管理员重置、观测结果读取、模型运行选项）；本次另补 validation-only C-177。
- 当前清单 177 项：91 已有、86 拟新增。C-139 仍是“执行配置版本退役”，不得替换成通用 registry 退役；[validate-progress.py](validate-progress.py) 对此有固定检查。

## 固定的实现方向

1. 原生身份沿用服务器确认的默认组织。安装实例登录名唯一；新用户生成不透明 principal_subject。旧 subject 只通过受控显式映射迁入，绝不按登录名或显示名连接。保留历史 subject、审计和证据，先核对受限映射清单再迁移。管理员不继承运维权限。首次设密会话只允许改密/退出，不允许读取业务。
2. 使用已安装 x/crypto 的 Argon2id，参数版本化并在开发机校准后冻结。会话使用服务端随机材料及摘要、HttpOnly/Secure/SameSite cookie；写入检查 Origin、CSRF、幂等、凭据/授权版本。认证查询仅从安装配置解析组织，不接受未认证租户头。失败限制必须持久化，未知用户和错误密码使用一致错误。
3. 再次验证复用既有 action 风险与事务内复检。将提供方 SID 改成服务端本地会话绑定，保留绝对/空闲期限、撤销和授权/凭据版本；不能把密码登录直接标为原 loa:2。需要密码加 TOTP 的管理/高风险操作继续要求真实双因素。挑战提交后才产生短期验证记录，绑定当前会话和既有风险确认对象。补充状态在下文登记，接口须先定稿再生成客户端。
4. 独立观测不创建 AI 会话。复用 evidence/analytics、graph 授权和来源资格；新增个人查询/固定结果生命周期。资源 canonical ID、UID、namespace UID、来源 revision、绝对半开窗口必须一起固定，每次历史读取重新授权和核对来源。日志条数/分桶查询总体与脱敏样例分开，样例不能推总量。取消为请求/终止两个状态，迟到结果不得重新进入当前页面。
5. Finding 当前/历史来自来源生命周期，不从 Incident 处理状态推断。列表和详情优先复用 finding.records、finding.timeline、incident.finding_links；协作另记 revision/时间线/审计。现有 command-executions 无全局 GET；GET post-check 只读自动报告，人工复验另建尝试且不得重跑命令。
6. 模型仍只有服务地址、模型 ID、访问凭据、上下文长度四项用户配置。保存/连接测试不生成准入材料；已准入 Profile 自动关联现有证据，未知模型不执行。地址继续服从现有 Profile 约束。API/Go/Python Worker 必须在新 Job 固定同一 Profile 版本；旧 Job 不随配置切换。无法证明上下文上限时标未知，手工值只能收紧。外部验收网关不进入正式路由。
7. 知识首版为可解释的关键词检索：Unicode NFKC 规范化，空白分词后 AND 匹配，中文连续词按字面串匹配；仅授权已发布固定版本参与，界面称“关键词检索”，不称语义检索。排序和分页须稳定；归档排除新检索，旧引用保留且重新授权。P7-05 根据实际规模验证索引与容量，不为未要求的语义检索增加平台。
8. 计划在数据库保存时区/版本/槽位，复用 Worker 租约与围栏，唯一槽位派发；停用不取消运行任务。通知保存每次尝试与不确定回执，只测试隔离接收端。新集群检查只读且区分 denied/unreachable/missing/incompatible/unknown；安装要独立目标权限和合格物料，不能重装已有第三方组件。
9. 原生数据写入后只允许兼容原生版本回退或前滚修复，不恢复旧凭据或自动整库回滚。虚拟化、完整 Trace、生产发布和根目录 00～10 正式文档均不在此次变更范围。

## 必要补充状态（P0-03 / P2-03）

原型之外需实现：首次 TOTP 设置/绑定确认、挑战输入、错误/过期/已使用挑战、恢复码验证、恢复后会话失效、管理员受控重置 MFA、登录频率受限、CSRF/Origin 拒绝。它们属于保持原安全保证的补充，不用模拟勾选替代。对应操作已登记为 C-172～C-176（MFA enroll create/verify/get、recovery verify、admin MFA reset），Schema r4 已发布，但均仍为拟新增、未实现，责任 P2-03；不能记作现有接口。另有 SL-005 first-password 原型缺 `currentPassword` 控件，正式实现必须补重新输入当前凭据的补充状态，责任 P2-02；不得复用登录明文。角色授予需补 native member 选择、当前授权投影与 reason/expectedAuthorizationVersion，责任 P2-04；旧 subject handler 仅作待适配复用。

## 本轮具体未关闭项

- P0-02：G0 范围已关闭（R7 独立审核）。157 个动作阶段、10 条逐片 confirm、54 种可见表单、544 个 form 外控件、62 个复合状态、SL-001/002 登录 C-009、SL-009 图读取 C-004、SL-106 父 Finding C-030/候选 C-031/C-039 写、policy-validate C-177 已登记；运行/权限/持久化仍 not_verified。运行、权限与持久化仍为 `not_verified`，不得据此通过 G0。
- P0-03（G0 范围已关闭）：四个版本化 Schema 与 r4 静态测试实测 55 正例/87 反例通过；R6 已按 Schema 修正 C-051/052、C-170、C-173、C-175 与表单投影。C-017 密码重置已定义，9 项操作同步为 C-168～C-176。Schema 尚未进入 OpenAPI/实现；凭据写入 OpenBao、MFA 状态机与观测取消仍需实现验证。
- P0-04：任务专属数据库 r2 自测通过 46/46 检查，历史读取失败已修复；2 集群/3 来源/6 实体/3 绑定/2 会话/9 审计、第二次 seed 不重复。证据见 [p0-environment.md](p0-environment.md)、[r2 JSON](evidence/ui-v12-fixture-run-r2.json)、[r2 日志](evidence/fixture-run-r2.log)。原生用户/登录/主体映射仍属 P2；共享库未写入。
- 环境（2026-10-21 r2）：根分区 74%、约 59G 可用；/data 约 1.6T、11% 使用。当前任务环境为 `/data/ops-ui-v12-20261021-r2`（独立 PG 17.11、迁移 43、回环 15449）；`/data/ops-ui-v12-20261021-r1` 仅保留历史参考包/浏览器配置。未清理旧文件，也未变更 Docker/Kubernetes 存储配置。
- 历史主体与恢复管理员未核对；任何迁移前必须从受限环境确认实际 tenant/subject/授权映射，不生成假成员材料。

## G0 判定

G0 条件按 implementation-plan：458 映射无遗漏、39 项任务依赖有效、已有/拟新增无误标、模型合同与账号回退边界明确、安全补充状态已登记，并由独立审核签核；产品六项是后续按切片验收，不是 G0 的前置循环。当前 `evidence/g0-independent-approval.json` 已由 Codex 于 2026-10-21 独立批准（`g0_passed=true`），确认映射/合同/环境准备；它只允许进入 P1/P2，不表示产品六项通过。458 状态六项仍全部 `not_verified`。

## P2-02 运行裁决补充（2026-10-22，GPT直接修正）

- C-009的账号锁定继续由持久计数执行，对外与未知账号统一401及错误消息，共用有界Argon路径；429仅用于不依赖账号是否存在的入口整体限流。依据是登录不暴露成员信息的既有要求及真实DB/HTTP反例回归。未进行统计时序侧信道测量，不宣称网络耗时恒定。
- 迁移45在15452实例首次执行后冻结；窗口过期锁及clear_password_failure精确RLS缺陷通过新增46修复。44保持既有验收源码。后续SQL前滚，不修改已执行45/46；不回滚旧身份/撤销状态。
- 真实PG与HTTP处理链通过仅关闭对应机制核实，不能将全部458切片六项、浏览器或正式视觉标通过。证据见evidence/codex-p2-direct-r1.json；共享环境和旧30451未切换。

## 2026-10-22 GPT独立预览核实

正式Foundation此前未接native处理器，真实登录404；已补接并让集成测试使用正式装配。SL-001 username展示字段映射到loginName，不带演示账号/密码，原型说明替换为真实账号说明。登录/改密/会话/退出本轮实际链路及请求证据在evidence/preview-r1/result.json，contract-audit与form-contracts同步；G0绑定文件里的missing为核实时基准，不冒充本轮运行状态。来源服务仍复检旧role_bindings，原生管理员403；此缺口归P2-04/05，不补旧授权、不绕过事务内复检。MFA/再次验证待P2-03，仍fail-closed。SL-001视觉有差异，记录failed，其他切片不继承机制验收通过。


## r24 实际接线裁决（2026-10-27）

- 我的账号/本人改密使用 C-007 恢复或 C-009 登录回执中的当前账号投影；C-001 是参考底图 overview 读取，不能当作账号数据来源。补充 loginName/displayName 为可选只读字段，不从 subject 猜登录名；关闭为本地操作，退出必须 C-010 服务端撤销，改密为 C-011，confirmPassword 不发送。原始方案/参考清单保留，实际字段链见 evidence/gpt-native-visual-r24/form-implementation-bindings.json。
- 管理员 MFA 重置从 C-014 真实用户列表选择目标。C-176 仍要求 userId、confirmLoginName、reason、expectedUserRevision、expectedCredentialVersion 和目标绑定 stepUp；两个版本取所选服务端投影，不再让用户填写内部版本。C-012/013 当前管理员密码+TOTP、CSRF、会话撤销、双角色独立与事务内管理员/目标复检保持。
- 零角色原生账号可以查看自己的账号和安全设置，不因此获得业务角色；业务路由仍逐个检查 operator/platform_admin，服务端拒绝越权。真实浏览器已验证无角色 MFA、管理 API403 与管理员跨用户 enrollment GET404。
- contract-audit 中 C-012/013、C-172～176 旧 not implemented/false 是历史判断，按实际源码及本轮请求证据更新为已实现/限定范围核验；新增合同的原始分类和 original-plan/ 不重写，不删接口维持旧数量。
- 本轮独立 Worker 运行证明是审计签名/Archive 链，不推导 SP06 业务 Job Context/工具结果/租约接管/取消通过。前景尺寸匹配不推导整页视觉通过。


## 2026-10-27受信DeepSeek运行裁决（用户最新指令）

用户明确“deepseek就当作是内部模型”，覆盖早期只允许内部自托管路由的本任务假设；直接作为受信运行模型复用原Worker链路，不新增外部模型治理。固定精确官方OpenAI地址与实际可用deepseek-flash，现有四字段产品配置原则不变。为兼容当前闭合合同，Profile v2区分实际托管计费与供应商别名，保存真实协议证据与本次授权；不填伪造weightsDigest/自托管许可证/zero-charge，不将供应商别名当不可变权重。API、Worker和Python按相同Profile引用绑定；现有token/请求预算、无自动重试、原生来源/版本/范围复检及签名/mTLS保持。其他未知模型/地址仍拒绝。

实际审计data_query业务链路、取消、撤权和真实进程接管已验证，详见evidence/gpt-worker-r28/result.json。管理员仅访问审计域，并未获得operator；操作审计页新增审计问数入口属于必要权限接入扩展，不改变冻结参考或产品视觉通过状态。

## r34：显式映射历史会话的只读状态（2026-10-27）

现有历史读取合同只允许在当前独立授权下读取旧主体证据，不赋予新任务/归档/取消权限。正式前端据服务端返回的ownerSubject与当前session.subject区分会话归属：已映射历史显示“历史会话只读”，用户消息标为“历史用户”，冻结范围提示不会转为新任务授权；禁用发送/归档/停止/故障链提交，且不连接原所有者事件流。元数据尚未读取时也不先建立写入事件流。此为P2-04历史映射及P5-02相关的必要身份状态扩展，不变更服务端写权限，不改原始HTML基准、不提升visual验收。证据：evidence/gpt-coldstart-r34/（只读回归、正式构建、受控历史API/UI和冷启动验证；各项按实际结果记录）。

## r35：调查映射只读与来源版本归档（2026-10-28）

C-065/067/068/069沿用接口增加显式legacy映射的只读读取，事务内按当前operator完整范围复检；C-070仍拒绝映射主体取消，Worker不继承映射。新UI身份状态“历史调查只读”只禁用原任务取消，不改HTML基准或提升visual。证据与SSE撤权反例见evidence/gpt-history-recovery-r35/。

Kubernetes投影v3将SourceRevision加入不可变Evidence ID/QueryHash，避免配置版本变化时复用旧UID/RV归档；v1/v2记录保留，来源失去资格仍拒绝。新版本固定模板纳入已有并发首观测幂等规则，不新增通用放宽。此为现场复现缺陷修复，不变更证据保留规则或用户模型四字段合同。


## 2026-10-28 r37 独立日志查询实施裁决（未整体验收）

- C024～C028/C168已建立正式原生API→mTLS/签名Worker→持久化链路；手动查询不创建AI会话。当前实际只支持 logs.records/v1，资格来自真实单Pod日志快照，不能据此扩展到所有Pod/持续日志采集；补Qualification.resourceUids约束编译/能力与冻结构造。
- 查询view补revision，供已有cancel.expectedRevision真实绑定；补filters回显。通用schema支持上限不是当前模板承诺：首次复用现有适配器提供20样例、2次来源调用、累计128KiB响应体、8秒时限；总体来自聚合，样例不推总量；coverage未知仍partial。
- 所有观测调用必须携带clusterUid；capabilities可附canonicalId取得该UID资格。当前历史限最新50条可读记录，cursor分页和显式result_expired仍未实施，不能把C025/C168合同整体关闭。迁移71只前滚，不删除历史/恢复旧身份或自动降级；旧Worker不认识resourceUids，不得带此配置盲目降级。
- 视觉上保留原型状态条尺寸，用真实生产范围文案替代演示提示；实际账号/独立授权导航/必须的当前密码与再次验证/MFA补充状态登记差异。没有将原型材料或截图存在变为正式visual通过。

## 2026-10-28 r38 日志检查读回裁决

检查页只呈现服务端View已公开的持久化resource/source/window/filters及结果快照，复制不伪装完整createRequest；预算仍在数据库原请求中，但当前View未返回，明确列为待补合同。详情“原始内容”是同一脱敏样例快照加固定身份，不是未经脱敏来源日志。排序/换行/相邻导航仅作用返回样例，不产生总体统计或额外来源读取；级别过滤新发真实持久化查询。本轮真实数据库验证完整请求仅level不同、来源总体186→180。完整视觉、时间桶和字段侧面等未通过，六维不提升。

## 2026-10-28 r39 历史与期限裁决

C025保留data数组，新增nextCursor/limit；游标取不可变行UUID并在当前tenant/owner/cluster查询边界复查，不是授权凭据；每次最多扫描limit+1条，来源/权限失效条目省略但游标继续推进。C026的request从持久化request列补齐，旧View不伪造当前预算。C168/取消过期410只在当前会话/独立operator范围/来源登记事务复检后返回；expired读投影不改写原不可变结果。Worker领取/停止/发布均尊重expiresAt，不以来源HTTP取消宣称来源已停止。完整过期业务长时演练未做，元数据fixture/PG与真实来源各自单列；保留启动503，不降级原生权限或来源门禁。

## 2026-10-29 r40 样例分布与视觉证据裁决

时间桶、级别分布只计算本次已返回的不可变脱敏样例；柱高/侧栏计数不推断总体、错误率或采集完整性。本地时间/关键词筛选不重发请求，级别侧栏则以固定原请求执行真实新查询。现有request/result/授权合同不新增，C139及original-plan不变。新日志捕获协议只在/observe/logs排除已从授权元数据核实的完整UUID，未知UUID/独立恢复码/因子URI/OTP/Base32输入仍拒绝，前后探针保留；不修改r30/r36历史协议或历史验收。默认/换行/空态截图只作真实产品比较证据；整页入口和布局差异保留，视觉不通过。

## 2026-10-29 r41 窗口与导出裁决

日志窗口来自当前合格来源或已获准历史记录的固定窗口，单选择器回填原始时间字符串，不经Date.toISOString截断微秒。导出无专用服务，实际logs动作是先GET C168做当前权限/来源/过期复检，再本地CSV，仅导出点击时可见样例；不隐含全总体导出。原P0 query-export local分类描述原型，全局计数不冒充新运行链，当前仅日志路径由implementation-bindings.json显式补充prefetch+local；其他模块不因此通过。CSV内容在表格软件按纯文本处理公式前缀；不加入无关容错、依赖或后台循环。

## r42 observe-ai范围接续

原型observe-ai打开资源排查表单，本轮日志实现以queryId重新GET C168后展示确认，创建前再次核对结果，再由既有会话创建合同绑定canonical/namespace/精确时间并服务端重新解析和事务内复检。原查询来源仅展示核对，不等同AI来源固定或样例证据引用。当前运行模型仅data_query，保留资源排查门禁，真实创建与持久化未验收；单位夹具正向不替代远程运行结果。P0原始阶段库存保留，实际链见evidence/gpt-log-ai-handoff-r42/implementation-bindings.json。


## r43 关系拓扑与模型门禁核对（2026-10-29）

SL009实际根选项/分页及精确根核对使用C002，邻居C004返回真实节点与边，抽屉重新读取C003；原型候选C022不是本轮必需读取，不冒充已接来源管理。API按当前clusterUid验证指定canonicalIds，403或空根均不退回其他资源；图深度1/2、节点200/边400与URL选择固定。关系由既有Worker Ariadne实时投影提供，当前不存在platform.resource_relations表；C002/003/004实际存储/Worker链已在contract-audit纠正，原基准不改。5节点/4边经独立Kubernetes UID、ownerReference、selector、volume与READ ONLY PG核对，不将关系称因果或声明来源连续性。

当前Profile确实只有data_query，r28协议探针及审计链不证明资源排查准入；签名策略已有受范围约束的运维只读规则，缺口是专项任务评测/准入实证及实际决策验证。没有扩大Profile或绕门禁。图页面截图被探针拒绝、六维不提升，完整视觉及全部P3仍未验收；证据见evidence/gpt-topology-r43/README.md。


## r44 资源搜索与健康投影（2026-10-29）

C002增加q（名称/Kind/稳定UID的大小写无关子串，≤256Unicode字符），与已有精确字段取交集；授权过滤、汇总、排序、分页及签名内部queryDigest一致。原name语义不变，原始候选清单保留，公开/内部/生成合同同步。共享projectEntity把原entityHealth/entityDomain分类结果放入attributes.health/resourceDomain，canonical domain保持原身份含义，不写回不可变属性/历史结果；这修复了筛选判正常而页面因缺字段显示未知的矛盾。API/Worker都须前滚识别q，新API不能盲配旧Worker。目录速览是显式C003新读，403不回显旧缓存。证据与当前r44版本见evidence/gpt-resource-catalog-r44/README.md；全产品及六维未通过。


## r45 资源比较与导出（2026-10-29）

原型export-resources为local库存，本轮实际是C002精确ID新读取后本地CSV，限定本页且明确区分导出数与点击时全过滤匹配数；不把已加载页当全量导出，不继承其他模块完成状态。对比同样先读取2～4个canonical，成员不足/拒绝不回退缓存；只比静态字段，不混合指标/证据。原始库存/参考不变。来源资格10:45已经过，10:48真实capabilities200/sources=[]，没有续期或新日志查询；保存结果自身TTL与来源当前资格分开判断。见evidence/gpt-resource-controls-r45/README.md。


## r46 资源详情入口与事件禁用状态（2026-10-29）

当前Web先C002按active cluster+canonicalIds精确核对，再C003按自带scope的canonical查详情；C003/C004不存在额外clusterUid参数，清单按生成合同纠正，不改变原始基准。C031资源过滤实际字段为resourceCanonicalId而非incidentId。查询键、节点链接和来源版本展示保留当前cluster/规范身份，旧tab=graph高级关系路径兼容。来源revision来自C003公开observationSources并与当前active登记核对，不等同当前业务资格/连续覆盖。

资源“关联事件”实际C029/C031为409 CAPABILITY_DISABLED，当前API和Worker的SP04配置均未启用SP05；不将未执行读取视为0条或改从DB渲染，不自动开启模块。日志深链仅设置canonical并GET资格，source=[]时禁止新查询，失败复查撤掉旧合格徽标；没有续期原10:45快照或新建查询。来源追溯卡片/手动刷新/明确无资格状态为真实接入扩展，整页视觉与六维未通过，见evidence/gpt-resource-detail-r46/README.md。

## 2026-10-29 r47：资源候选范围和重置

C002通过可选includeFilterOptions复用当前授权图快照，返回meta.summary.filterOptions{kinds,namespaces,clusterScoped}，按授权过滤后、业务筛选和分页前去重排序；不从本页猜候选、不新增管理接口或授权。旧调用默认省略，false省略签名字段，true进入QueryDigest；当前匹配total/byKind等语义不变。保留namespace精确值，新增保留值__cluster__选择空namespace（非合法Kubernetes名称），仍受ClusterScoped授权约束。按SL008补候选与重置；保留现有即时筛选URL方式，未宣称整页复刻通过。来源部分覆盖提示不因候选来自完整已授权快照而消失。API/Worker先后前滚保持同一合同，schema71未变。

## 2026-10-29 r48：实际日志源采用当前 Kubernetes 原生读取

当前环境有VictoriaLogs服务但没有持续日志采集代理。按用户“来源改成实际的”要求，最小完整路径复用真实Kubernetes来源/CA/自动投射身份与现有独立观测查询，不新建Agent/手工周期快照或把旧快照延长。PodLogs按集群显式启用、默认关闭；仅一个运行容器，UID/NamespaceUID/ContainerID/重启次数固定，当前pods/log权限实际验证。该路径是按需读取当前缓冲，不宣称持续归档或历史完整。沿用resultView既有not_measured/partial合同，不做虚假aggregate/no_data；刷新来源获取新绝对窗口。迁移72在原source/credential/cluster/namespace/租户锁内允许已有Kubernetes来源，保持collection不接受非空nativeTenant的语义，不改旧来源记录。schema72只前滚，不使用旧库/旧身份恢复。当前来源不恢复延期虚拟化、模型任务或SP05。
