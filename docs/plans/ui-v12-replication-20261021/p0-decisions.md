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
