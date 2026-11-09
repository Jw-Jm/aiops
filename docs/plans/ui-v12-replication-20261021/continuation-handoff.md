## 2026-11-09 最新接续：组合补验通过并纠正总账

此前“第 2 项完整收口”的声明过早：当时没有证明不扩大权限的命名空间 operator 调查正例，也没有把同一旧故障、固定旧 RCA、所引用旧来源归档和 AI 实际复盘串成一条链。两项现已补齐，见 [组合验收和产品修复](../../evidence/scope-history-combinations-20261109/README.md)、[可复核覆盖总账](../../evidence/scope-history-combinations-20261109/ledger-coverage.json)。原始失败及旧回执保留，不追溯改写为当时通过。

当前接受候选 `fbdfef5e6c80caf6fa5af6816ac9896e5ea5593d`，schema 74、ops-current namespace/端口不变。仅命名空间 operator 的统一页面调查、历史 RCA 1 两条来源版本 1 的真实归档事实读取/解释/引用、页面固定版本抽屉/重载，以及范围外403、匿名401、跨账号404通过；来源注册保持2，旧故障和冻结RCA未变。历史证据不完整时回答保持 partial，不冒充历史根因已经确认。

原真实 Deployment 故障、本人命令/MFA/Runner、实际修改、未恢复判定、未知结果无重发、取消/撤权和人工回退证据直接复用 [原运行记录](../../evidence/incident-remediation-20261109/README.md)。9 条执行/7 条 attempt 未新增；最终候选仅回读其原报告、记录和四条恢复归档，不声称重新执行过修改。未知结果仍未知。

额外修复旧 v2 调查接口在托管模型明确禁用 legacy model 时仍创建必败 Job：当前该 POST 提前返回 CAPABILITY_DISABLED，支持的统一页面使用已通过的 v3。两条原失败 Job 保留，不能当作 v2 成功。没有启用被禁用的模型回退。

`task-progress.csv` 的 P4-05/P5-01/P5-02/P6-01 已由 not_started 纠正为 in_progress，并登记已有证据和原任务剩余要求；`acceptance.csv` 的 17 个关联切片登记已覆盖子动作。整片多动作和视觉尚未逐项验收，六维不据此整片提升。`tasks.csv`/`original-plan` 为原始待实施方案清单，真实实施状态以 task-progress.csv 为准；不能再据原始状态忽略已经完成的运行证据。

下一项为 22 菜单及 HTML 1:1：逐项复用已有证明，再补各任务未覆盖动作和视觉。全局执行列表、人工验证尝试、会话全套操作等按原总账核实收口，不能将本次两组合通过改称全部菜单完成。完整发行、签名审计归档链附加检查、虚拟化/Trace/SP-08 仍按原方案及明确阶段边界处理，不由本补验自动宣称通过或恢复。

## 2026-11-08 当前接续：调查与受控只读命令闭环

当前实际环境以 `mssc-direct` 的 `/data/ops-current`、namespace `ops-current`、`ops_current` schema 74 为准，页面仍为 `https://192.168.0.63:30471`。应用接受版本为 `0d19b045f979af1fb47155e450dc8d2145ef27a7`，具体不可变镜像和复用来源见 [受控命令验收材料](../../evidence/controlled-command-20261108/README.md)。下方旧预览端口、数据库及运行版本仅是历史记录，不作为当前部署依据。

统一调查入口、真实故障调查/追问/固定引用/只复盘已通过，见 [调查闭环](../../evidence/investigation-closure-20261108/README.md)。沿用同一故障的本人实际命令、密码/TOTP、风险确认、Runner、输出归档、独立恢复检查和未知结果防重复也已通过。最终成功执行 `01a1bade-200d-7cbd-be53-28455ba39875`；故障注入执行 `01a1bad8-e6c2-7a0d-a2fb-453acf5db8dc` 有意保留结果未知，不能用目标健康补造其最终结果。重启后四条执行均仅一次 attempt/一个 Job，旧调查引用和来源升级前归档仍可读。

后续从 `tasks.csv`、`acceptance.csv` 继续 22 菜单逐项收口及 HTML 1:1，不重复上述已验收机制，也不把它们当作菜单六项/全视觉/完整发行通过。本次命令只读，未证明修改业务资源的处置；虚拟化、Trace、生产发布仍未恢复。原始未提交指导文件及 20261106 集群账号证据目录保留，不得覆盖或混入提交。

## 2026-10-28 r35 最新接续（GPT直接执行）

本轮按用户两项授权补齐legacy v2调查映射只读入口/列表/步骤/SSE逐事件复检及前端只读取消禁用；真实PG/HTTP同时验证旧执行输出、审计独立授权、证据引用、流中撤权和旧归属不变。来源revision变更复用旧UID/RV归档ID的问题已修为投影v3，旧证据保留。真实无效采集凭据→拒绝503→恢复原投射身份→漏采更新/新更新持久化，以及第二namespace实际只读RBAC、同名资源独立UID、用户范围A+B后收窄A/撤权拒绝通过。完整证据见evidence/gpt-history-recovery-r35/README.md、result.json、source.json、runtime-final.json。

30471现为r35 Web/API/Worker/source3双任务namespace；30472为r35 Web/API+原r34 Worker。原r24 admin/member密码/MFA/授权未改，r31测试operator已恢复原A范围（授权版本正常递增、测试会话失效）。新迁移无，仍70；无新模型调用。稳定窗口12次200不代表启动/故障窗口永无503。实际旧库实迁、历史query-result/formal-reference全组合、P1/P2完整视觉与正式Bundle/离线发行仍未关；P2-04/05及六维状态不提升。下一步补剩余引用组合和P1/P2视觉，再按依赖SL049；不重复736参考或本轮已验证的凭据/范围演练。

## 2026-10-28 r34 最新接续（GPT直接执行）

完成受控历史inventory真实CLI导入、管理UI显式映射/再次验证/错误摘要拒绝/旧会话失效，以及新namespace/新库无OIDC的Web/API/Worker/调查器/CLI启动与进程重建；历史会话只读前端修复已部署独立30472。原30471与既有用户不变。详见 evidence/gpt-coldstart-r34/README.md、result.json、source.json、runtime-final.json。会话重启有效，但故障502后页面需刷新恢复，未保证原路径。新库0 Jobs/0模型调用；受控旧主体不是实际旧库。P2-04/05和六维状态不提升。

下一步：历史执行/审计/证据引用及双角色组合；采集真实凭据失效恢复、第二namespace实际授权；P1/P2视觉收尾，再按依赖P3资源日志样板及P4～P8。独立源码冷启动已验证；正式不可变OCI/Bundle/离线安装切换仍缺合格材料。中断前脚本泄露的单个r34测试会话已自然过期401，失败回执已脱敏，未改其他会话，见session-containment.json。用户新增代码约束：入口一次校验；不重复判空或滥用try/catch；单次逻辑不乱抽函数；低概率防御先征求同意。

## 2026-10-27 r33 最新接续（GPT直接执行，覆盖下方旧交接约定）

用户已撤销 CodeBuddy，交接1d91d497取消，后续由GPT实施；不得再派发。当前platform/main/082f87d66c5b及r23～r33全部未提交修改保留。r33完成采集/归档/Lease/chart具体返修、来源真实PG回归、历史消息HTTP分页映射修复和正反例；三版真实ConfigMap跨自动轮换/Worker重连持久化通过。API/Worker运行r33，Web-r14、调查器r28、PG17.11:15454/schema70不变；30471仍为任务预览。

先读 evidence/gpt-authz-r33/README.md/source.json/runtime-final.json。下一工作：C历史inventory/import/mapping完整API/UI（受控隔离数据，不能冒充真实旧库）、历史执行/审计/引用；D独立namespace/库整栈无OIDC冷启动及CLI/浏览器持久化；再补凭据故障恢复和第二namespace实际授权。正式Bundle缺材料仅阻塞正式发行验收，不阻止独立机制工作。P1/P2视觉及SL049后续不跳阶段。已有r28/r30成功链路和736参考不重做；用户admin/member身份和秘密不动。

## 2026-10-26 r23 当前接续（GPT直接执行）

用户最新要求完成四项，解除旧“发现问题后停止”的本轮边界；不得连接CodeBuddy。MFA窗口缺陷新增70修复，集中35项+P2独立10项通过，新登录single-flight修复及30470隔离预览浏览器通过。代码尚未提交，主分支基线082f87d。任务目录/data/ops-v12-completion-20261026-r1；新库ui_v12_r23_preview/schema70、API18091、metrics19091、Web30470；r15仍69、旧共用库57。70已执行必须冻结，下一迁移71+。新增来源能力交互证明修复、真实Transit浏览器MFA全链已通过；TokenReview旧凭据过期，续签同SA/audience任务凭据后恢复，未改角色/信任/key/策略。详细进展以progress.md r23节与evidence/gpt-completion-r23/README.md为准，不能照下方旧记录反复修已解决项。

剩余主目标：P1/P2完整视觉与原生全栈运行/切换收尾，再P3独立观测查询生命周期及SL-049同组真实资源日志样板；不把受控协议fixture称为真实安装来源。新预览冷启动只证明API/Web native起进程，未启动Worker，不能标P2-05完成。用户r14账号/原数据库/其他预览未改；复制的私有数据库dump、DSN、原生凭据仅远程private，不入Git。

# V1.2 接续记录：额度耗尽前的未完成工作

本记录按用户2026-10-22要求保存，专门保留上次额度耗尽的中断点；不是只记录其后发生的远程断连。用户已改为GPT直接实施，取消CodeBuddy交接c767b57e；后续不要再下发CodeBuddy。实际源码、运行状态和用户新指令优先，报告不能代替验证。

## 原中断点与已接回的结果

- 分支feat/ui-v12-replication，HEAD 9bd78d84e212217626cdf0cfe54af84a155a9a3d，继承大量未提交修改。不得重置、覆盖或切回main。根AGENTS/platform AGENTS/ADR-0008继续适用。
- G0已独立核实，P2-01完成；P1、P2-02/03仍有未完成项。不得把任务清单原始“待实施”基准或历史通过数当作当前状态；看task-progress.csv/acceptance.csv及实际证据。
- 上次已修复MFA审核R1–R6：事务内当前会话/版本/撤销复检、持久恢复失败预算、旧因子授权重绑、页面挑战revision/取消、退出失败刷新隔离、恢复后服务端强制重新设置。52/53/54在任务专属15453已执行，全部冻结，后续只新增55+迁移。
- 实际OpenBao已验证：TLS/Raft、Kubernetes TokenReview、任务SA JWT、专属Transit key，错误SA拒绝；真实MFA浏览器流程、停机503保留旧因子、重启/解封恢复均通过。证据evidence/gpt-mfa-r6/runtime-openbao/browser-result.json、setup-result.json。测试JWT限时，不是生产自动轮换；后续恢复联调前要核对期限并只更新任务凭据，不重新初始化Bao。
- 原生命令证明已绑定实际assessment、资源UID/revision、命令摘要及执行配置版本。最终integration-r6-command-fixed.log在真实15453通过，运行于19:19。该测试不代表正式策略准入、真实资源来源、命令派发/Worker/输出通过。API运行快照source-current.json早于最后command helper修正，不能把测试二进制通过当作已部署API版本通过。
- 上次中断时来源浏览器脚本结果未收回。本次已通过SSH读取远程logs/source-browser-result.json：passed，source 37728671-b5f0-40b3-8f75-0092787510f2，tenant abf33a27-b600-4eae-8bf8-4eb02bd14477，实际PATCH请求01a16359-b24e-7406-8400-2a7c46710b53成功并持久disabled/revision2。此为隔离合成注册，不是合格来源或实时日志数据。完整JSON/截图仍待回收，禁止盲目重跑造成额外Fixture。

## 本次已继续但未远程验证的r7

- administrative_write目标绑定HTTP方法/路径/规范化内容，不为新对象伪造ID或revision。已有revision/固定版本/摘要继续由实际服务检查；MFA和命令不接受此目标。
- AdminPage/RegistryEditor接通各自操作专属证明：来源、策略草稿创建/更新、策略发布、激活/回滚、registry退役、执行配置发布/专用C-139退役。输入/账号版本变化使旧证明失效。
- RetireProfile/OnboardHost改用共享原生授权，先验证证明再读授权，避免账户锁升级死锁。Native请求拒绝写legacy角色绑定/旧租户初始授权。
- 本地Go identity/httpapi/action及合同通过；27项前端定向检查、3项证明/冲突恢复检查和构建通过。Linux集成二进制artifacts/test-reports/ui-v12-session-r2/gpt-admin-r7.test已编译，新增真实registry草稿证明用例尚未运行。tenant legacy拒绝分支包测试已通过；运行验收仍未执行。新运行版本/来源清单见evidence/gpt-admin-r7/source.json，未部署。

## 继续顺序与具体未关闭项

1. 收尾r7：tenant边界单元已通过；复核所有管理调用方/合同；在15453运行TestNativeMFAEnrollmentStepUpAndReset（已含真实registry草稿正反向用例），保留失败；编译部署任务API/Web的新版本快照，保留旧二进制；真实浏览器操作、补充状态视觉、tested-source与运行说明绑定实际版本。
2. P2-02收尾：SL-001登录错误/加载/首次改密/退出逐状态和HTML切片比较；已通过静态布局与MFA/退出机制证据分别保留，不能直接推出全部六项通过。
3. P2-03未关闭：其余真实业务调用方操作证明接线、补充状态视觉、完整当前制品/迁移/机器身份说明；P2-03仍in_progress。密码类管理请求不能保存低成本密码摘要，需排除秘密字段并保持目标/版本绑定。
4. P2-04：原生用户创建/编辑/启停、独立密码重置、会话撤销、operator/platform_admin分别授权、实际cluster UID/namespace范围验证、最后管理员事务保护、历史主体显式且有证据的映射及历史归属读取。不得复制legacy角色或按名称继承权限。当前仅数据/授权基础与管理员列表查询已接通；用户生命周期API/页面未交付。下一步已选定此项，尚未写55迁移。
5. P2-05：Action/Worker/Job等仍需持久显式原生authority source/user/version，异步重新核对真实原生范围；不能靠subject前缀猜测或回退legacy。同步API/Worker/CLI/chart/安装配置，演练无OIDC冷启动和符合新写入边界的前滚恢复。
6. P3：按依赖推进真实资源与日志样板，覆盖SL-049同组所有状态、双集群同名资源/来源/UID/时间窗/授权/持久化/取消/恢复。再继续tasks.csv其余P4–P8。458状态的六维产品验收仍远未完成，绝不能宣称V1.2整体完成。
7. P1公共控件、导航与全分辨率状态定位尚未全部视觉验收。458主图/736总图参考已存在，不重采参考图、不用原型或新实现重放充当产品通过证据。

## 后发生的远程阻塞，独立于额度中断

- ssh mssc-direct解析root@10.10.10.2:22，要求绑定本机10.10.10.1；现该地址消失，报Can't assign requested address。
- 仅本次临时参数核对已获准LAN入口192.168.0.63:22（StrictHostKeyChecking=yes），也连接超时。没有修改SSH配置/网卡/路由，没有启动本机虚拟化。连接恢复前继续本地编辑、构建、单元/合同，DB/Kubernetes/模型/业务浏览器运行必须留待获准远程环境。

## 必须保留的环境与材料

- 旧30462/30463（15452/schema49）及用户v12.admin凭据、旧30451/15449–15451、原30464都受保护，不改密码/MFA、不升级或重置这些历史轮次。
- 当前任务根/data/ops-ui-v12-mfa-20261022-r1；15453/PG17.11/schema54；GPT r4入口30465/API18085；r6入口30466/API18086；r6 OpenBao18206。task-owned unit/container/storage/private所有权和实际状态恢复后须再核对。
- 不删除旧文件、镜像、容器或失败记录，不改共享存储，不把共享数据库只读授权扩大为写授权。凭据/密钥/恢复码只保留远程private，严禁复制到报告或提交。
- 不恢复KubeVirt/CDI/完整Trace，不使用本机OrbStack，不改根00～10正式文档，不生产发布。

## 后续接回r8：覆盖上方“尚未写55”的旧状态

- 55已新增为源码，**未执行、不冻结**；最后远程已验证schema仍54，不能假定55在运行。C-014..018、原生C-019..021及UsersPage已实现；当前P2-04依然in_progress。
- 本地37文件/144 Vitest+6 SSE、6个Go业务包+合同、正式Web构建通过，最终HTTP与摘要/边界复验通过；Linux API及集成二进制见evidence/gpt-users-r8/source.json。源码制品仅本地，未部署。
- 新增test/integration/native_user_administration_test.go由guarded MFA集成调用；真正等待TOTP窗口，不改last_used_step，运行时须给-test.timeout=15m。先从任务专属15453/schema54核对后前滚55；所有旧轮次保留。
- 下一步先跑r7/r8合并的真实DB/HTTP集成并排错，补最后管理员并发/审计失败回滚和真实浏览器；继续历史主体显式映射/历史归属读取，再P2-05 Worker/Job原生来源、CLI/chart冷启动和P3资源日志样板。新页面与HTML切片视觉/补充状态均未通过。
- 合同表更新时发生的CSV列名错误已恢复：完整177条，6条为本轮预期更新，原始基准未修改。evidence/gpt-admin-r7/contracts-recovery-r8.json保留恢复依据；不要从部分失败副本接续。

## r9本地续接（覆盖r8“映射尚未实现”的源码现状，不覆盖原额度中断项）

用户明确先推进无需远程验证部分；本轮GPT直接编码，未发CodeBuddy，未探测远程。C-170/C-171现已源码接通受控核对清单导入、原生管理员显式映射/分页、精确证明、同事务审计/幂等和目标会话撤销。新增56；55管理员视图计数错误已修正。两迁移都未运行、不冻结；运行库最后证据仍schema54，1～54保留。生成客户端、用户页历史映射tab和必要本地测试完成，145 Vitest+6 SSE及构建通过。

下一步独立源码任务仍是历史归属读取与当前原生范围复检、P2-05 Job/Worker显式authority及CLI/chart/无OIDC冷启动，不能通过别名或legacy角色回退绕过。恢复远程后先核对主机/所有权/数据库和制品，再前滚任务库55/56并运行守卫MFA集成（含新增用户及映射断言，至少15分钟超时，需有效任务JWT/真实OpenBao）。随后并发与审计回滚、浏览器及HTML像素验收。详见subject-mapping-runbook.md与evidence/gpt-mapping-r9/；先前额度中断、r7未运行内容、r6截图待回收等均未取消。

已保留opsctl全包来源材料锁不一致失败gpt-mapping-r9-go-first.log；不是新导入命令执行结果。新CLI定向验证通过，不宣称整个opsctl包无失败。不为本轮任务改来源材料或放宽锁。

## r10本地续接：原生部署配置与执行配置列表

用户继续要求推进本地可完成项。本轮GPT未发CodeBuddy、未探测远程、不启动本机运行环境。platform chart现支持runtime.humanAuthMode=native，固定tenant/origin、现有MFA Transit key与独立CA/投射JWT；Web不要求OIDC、不生成代理或CA挂载，API/Worker同模式；模式默认为oidc以保留受保护旧安装兼容，不是面向原生用户的OIDC选项。JWT复用现有只读投射，Worker未新增MFA key配置；OpenBao实际策略隔离未因此自动验证。

C-134 ListProfiles遗漏的legacy管理员查询已改共享原生事务复检，operator角色也真实复查后再走既有目标过滤。新增6组授权分支单元、原生构造器无issuer配置测试、native有/无SP06及OIDC兼容Helm渲染检查，完整action/authz/app/contract与最终定向复验通过，Linux API/集成二进制已构建。守卫集成新增原生管理员列表/撤权拒绝，未运行。C-134 cursor/limit/meta分页仍未实现，已记录合同缺口。

P2-05由not_started更新为in_progress仅表示独立部署配置子项有源码。正式安装器runtime_values.go、business_capabilities.go和business-values/Profile依赖闭包仍有OIDC绑定，不能将chart渲染通过当作opsctl正式安装或无OIDC冷启动通过。下一步仍可本地推进这些安装器路径及Job/Worker显式原生来源/user/version，但必须核实全部调用方、签名上下文/持久化和当前范围复检，不靠subject前缀/别名/legacy回退。历史读取权限与完整切片/SL-049仍未完成。

55/56未执行，不新增迁移，不改冻结1～54；最新运行证据仍schema54，无新在线地址。r10证据evidence/gpt-native-deploy-r10/，配置及接续native-deployment-runbook.md。先前额度耗尽中断、r7～r9待远程内容、r6来源截图回收等都继续保留。首次新Helm fixture缺SP05的失败日志保留，修正fixture后通过，未改生产门禁；r9来源材料锁失败也继续保留。

## r11本地定向复审：覆盖r10的C-134分页缺口

按用户“包括但不限于代码审查”继续，GPT直接实施，未发CodeBuddy/未连接远程。发现并修正55角色同步会让旧失效active角色恢复的P1问题；现只更新target旧authorization_version本来有效的独立角色，新增受控stale-admin集成回归，仍未运行SQL。55/56不冻结，1～54不改。另用失败Helm用例复现并修正OIDC+SP04动态PKI、无SP06时API缺OPENBAO_ADDR/CA_FILE，保留gpt-review-r11-pki-regression-before.log，修正后合同与最终定向通过。

C-134现已补HTTP cursor/limit/meta（权限过滤后按ID+版本）；退役/失权的锚点需从首页刷新。r10“分页未实现”现只作历史记录，当前源码见execution_profile_page.go；现有目录仍全读后过滤，未改成DB keyset，不声称大目录负载通过。OpenAPI/default/max/cookie认证与生成客户端已同步；清单解析拒绝NUL，identity和CLI定向通过。

完整相关Go包/contract、最终定向与13项前端测试、Web构建通过，Linux API/opsctl/集成二进制已构建未运行。最新源码/制品、复审具体情况和剩余问题见evidence/gpt-review-r11/。正式安装器/business-values/Profile、Job/Worker显式authority/user/version和签名跨进程复检、历史业务归属读取仍可继续本地实施；恢复远程后的55/56、并发/审计回滚、真实浏览器/HTML、SL-049验收顺序不变。原额度中断、r6待回收结果、旧轮次保护和来源材料锁失败全部保留。

## 2026-10-23恢复连接：r12/r13（覆盖旧“未执行55/56/远程断连”现状）

mssc-direct已恢复并复核mssc/任务存储/数据库标记。15453为PG17.11，55/56于2026-10-23实际前滚，现schema56；二者均冻结，后续SQL只能57+。现有Bao已核对精确role/policy/key，JWT限时刷新，无重新初始化。原远程仓库3339876e/分支fix/five-source-closure-20261017未改。

r13受控集成正在补r7～r11，失败/修复及最终结果看evidence/gpt-resume-r13/result.json和integration-*.log。独立轮次/data/ops-ui-v12-mfa-20261022-r1/gpt-resume-r13；新预览计划30467/API18087，仅最终集成PASS后启动，不覆盖旧制品。登录/改密审计主体、原生命名空间检查、管理员授权空数组、密码重置审计版本字段已修正，未放宽秘密门禁或SQL范围门禁。产品六项不因机制通过而提升。

r12安装器native业务配置/Keycloak显式禁用/CA/受控Cookie读取/环境引导已本地编码和定向测试；evidence/gpt-installer-r12/result.json。未部署/未冷启动：first-tenant CLI仍依赖OIDC，正式签名Bundle来源材料存在既有OPA摘要不一致，必须按来源证据处理。Job/Worker显式authority/user/version、历史业务归属读取、并发最后管理员与管理审计失败回滚、真实来源SL-049及HTML像素验收仍未关闭；范围数组上限合同namespace200与SQL100有差异，待统一，不改已执行55。

### r13最终交付状态（优先于本段先前“补验中/计划入口”）

新预览已运行https://192.168.0.63:30467/login，API18087，单元ops-ui-v12-gpt-r13-api.service；最终Web容器ops-ui-v12-gpt-r13-web-r3。旧入口不改，r13首次失败容器与停止的r2容器保留。最终API执行preview/platform-api-password，Web挂载preview/web-password，仅挂载公开Web/TLS/配置；private不挂载给Web。

完整MFA/用户/显式映射集成seventh通过467.85秒，含独立双角色、撤权、最后管理员保护、失效旧角色不复活。最后又从真实首次改密浏览器发现错误密码401误退出，新增CURRENT_PASSWORD_INVALID并同步OpenAPI/镜像Schema/生成客户端；保留UNAUTHENTICATED真正失效退出。最终HTTP集成与31项前端/构建通过，浏览器确认错误提示/201改密/旧Cookie401/零默认权限/退出200+401+刷新。改密脚本尾部因“退 出”可访问名称和未处理Promise中断，前面断言已完成；不重置该账号，另用browser-logout.mjs补完退出，所有失败保留。

实际源码/制品以evidence/gpt-resume-r13/source-current.json和runtime-password.txt为准；source.json仅保留早期快照，不能用于断言最终部署。r6来源结果/截图已回收，仍仅隔离合成注册而非来源合格。任务Bao JWT此次仍只续期1小时，后续恢复需再核对期限，不重新初始化或创建Transit key。

下一步：先补SL-005/账号安全/用户权限的HTML与补充状态视觉（当前独立首次改密页面未对齐SL-005对话框，不通过视觉）；继续历史业务归属读取与当前原生范围复检、管理并发/故障注入、原生first-tenant引导、Job/Worker authority/user/version与签名链，再无OIDC冷启动及P3 SL-049真实来源样板。namespace范围合同200/SQL100尚需裁决对齐，SQL只能新增57+。OPA来源摘要锁失败仍不得通过改材料/门禁掩盖。P2全部未关闭；原额度中断剩余项仍有效。


r14追加（2026-10-23）：SL-005已改居中660px对话框，真实取消/改密及持久化通过；149 Vitest/构建通过。evidence/gpt-dialog-r14/result.json记录安全扩展、动画/拦截脚本失败及视觉未完成范围。独立预览30468/API18088/metrics19088，目录/data/ops-ui-v12-mfa-20261022-r1/gpt-dialog-r14/preview；API仍r13二进制，schema56不变。30467保留，用户要求全部菜单账号：使用r14独立账号，已通过UI分别授予operator/platform_admin，仅明确登记的一个fixture集群；凭据仅remote private/browser-created-account.json，不复制到仓库。created-account-persistence.json记录授权前无角色的时间点，不能当作当前角色状态。继续原有P2与SL-049剩余，不重跑已通过机制；完整像素与pending/关闭/退出失败浏览器未通过。


## 最新接续断点：SL-005交互/登录背景r15与授权范围前滚r16（2026-10-23）

SL-005补齐真实关闭/Escape/加载禁用与退出失败网络故障、刷新、精确会话重试，均远程浏览器通过；错误当前密码、取消与持久化沿r14证据。重新核对原始PNG后纠正“overview背景”假设：固定参考实际为登录背景，提取NativeLoginLayout并作为inert、disabled、无凭据/无处理器背景复用，不触发业务读取。实测660px宽、6px字段间距、18.7px提示行高、10px按钮间距；安全额外字段/文案导致高度不同，完整像素不通过。152 Vitest、6 SSE及正式构建通过，中文关闭与administrator/operator首页路径均覆盖。浏览器英文Close/加载图标定位失败、组件库8px默认padding/margin及修正过程保留，不作产品失败掩盖或继承通过。

原生范围200namespace合同/SQL100差异已实测复现：真实UI+密码/TOTP授权400。新增57仅将admin_account_write的cluster/namespace上限100/100对齐合同50/200，保留所有事务、当前会话/角色/版本、最后管理员与独立授权推进；1～56与HEAD原文逐项核对不变。任务15453已执行57并冻结，后续58+。相同合同真实UI201并持久200；API角色SQL事务中200可接受，201namespace/51cluster拒绝22023，探测全部ROLLBACK，原binding/范围/版本不变。完整contract及identity/httpapi/db-migrate通过。另真实用户UI密码重置200、强制改密201、刷新进入operator总览并保留独立200namespace范围，无管理员菜单。首轮重置脚本误期望201而实际200，成功重置后中断未保存随机密码；仅隔离测试账号再经真实UI重置恢复，两个事件保留，用户r14账号未改。

30469/API18089/metrics19089为r15独立预览，最终容器ops-ui-v12-gpt-r15-web-r5及web-footer-final目录；API仍r13相同二进制。30467/30468和历史制品保留；三个预览共用任务15453，当前schema57，早期schema56证据仍只代表当时版本。未写共享开发库/生产。SL-005 interaction提升passed，visual/authorization/live未提升；P2仍in_progress。历史业务归属读取与当前范围复检、最后管理员并发/审计故障、原生first-tenant/Job/Worker/无OIDC冷启动、SL-049真实样板及其余切片仍待。证据见evidence/gpt-login-r15/result.json与evidence/gpt-scope-r16/result.json。

远程r15/preview为schema56时的旧Web交互测试材料，preview-r2才是30469运行目录；凭据均remote private，不复制到仓库。最后业务测试账号为r15 layout fixture，现operator+200namespace且已改密；用户可用r14双角色账号不受影响。r16 migrate.py仅允许从56升57，现已执行不得重跑；恢复用兼容原生数据版本/58+前滚，见evidence/gpt-scope-r16/runbook.md。


## GPT继续：P2-04审计故障与管理员并发r17（2026-10-23）

新增可重复边界测试并复用原生管理精确password/TOTP证明：仅本轮新建tenant/target/identity.admin.update的临时audit触发器令资料修改返回500，账号revision/业务审计均未提交；清理后同证明+同幂等键重试与重放200，只有一次revision和一次业务审计。双管理员并发停用前，控制事务持有既有tenant advisory lock；确认两个正式HTTP事务均在数据库锁等待后才释放，验证一个200、一个401/403/409，保留恰好一名有效管理员，幸存者实际登录/必要首次改密后读取用户列表200。

守卫MFA/用户管理整组623.37秒PASS；本地编译/vet通过。使用httptest正式Handler+真实任务PG15453/schema57及文件sealer测试fixture，不冒充生产OpenBao/浏览器/Worker或模型验收。只读回执绑定实际tenant/request/audit ID并确认故障trigger/function为0，r14用户账号仍active/credentialVersion2/双角色；30467/30469服务active。初次只读证据采集误用payload列，按实际record列修正后通过，失败说明保留，无需重跑已通过测试。

历史归属读取仍未实现：助手会话/列表/结果分别过滤旧主体，冻结grant摘要及Job输入/权限/策略校验横跨消息/引用/事件/Worker；不能只放开WHERE、改写旧主体或覆盖摘要。operational Job未持久化native authority source/user/version仍是P2-05缺口（本轮静态核实，不是运行验证）。命令历史资源域读取使用当前operator范围，不应误改为个人主体过滤。具体位置见evidence/gpt-admin-r17/history-read-review.md。

本轮只新增测试、复审与证据，没有修改Go业务实现、迁移或部署；1～57冻结，下一SQL58+。产品六维未提升；P2-04仍in_progress，保留历史读取/映射并发/其他故障/产品视觉、P2-05及SL-049完整样板等剩余项。证据见evidence/gpt-admin-r17/result.json；原生执行/取消/再次验证凭据不因映射放宽。

## r18（2026-10-23）：历史业务归属读取源码 + 真实 PG 机制验证

- 覆盖 r17「历史归属读取仍未实现」的源码现状：新增 58（仅源码，未在任务库执行，任务库仍 schema57；1～57 未改）与助手历史映射读取。映射只放行“谁可读”，读取仍以当前原生会话/角色/版本/集群/namespace/资源范围复检，冻结 owner/id/摘要/策略原样保留；写入路径仍是 owner 精确匹配。
- 真实 PG（任务 15453 上的临时库，1→58 全量迁移）通过：映射前不可见、映射后列表/详情可读且冻结字段未变、严格读取拒绝、撤权后拒绝、API 直接读映射表失败、审计记录存在。证据 evidence/gpt-historical-r18/。
- 仍未关闭：映射历史会话的前端视觉/交互、消息-结果-事件-来源步骤在真实 Job 上、审计域会话、映射冲突并发/其他故障注入、部署新 API/Web 与浏览器联调。P2-04 继续 in_progress；P2-02/03/05、P1、P3（SL-049）、P4～P8 未在本轮推进。
- 远程临时库 sp03_mig_*（schema58，含审计记录）按保留策略未删除；只读发现任务库 ui_v12_native_identity 仍为 57。旧 30467/30469、历史轮次与账号未改动。

## r19（2026-10-23）：映射历史读权限贯通故障链（返修）

- 修掉嵌套读丢权限：`AuthorizeHistoricalReadTx` 是唯一能力构造点，`CheckAssistantJobTx` 命中后仍用 `currentAssistantAs` 复检原生会话/版本/角色/集群/namespace/资源/策略；能力不写库、不改冻结字段、Worker 与写路径不可用。改动 `internal/investigation/assistant_v3.go`、`internal/assistant/repository.go`、`internal/assistant/fault_chain.go`，新增单元 `historical_grant_test.go`。
- 真实 PG（1→58 临时库）：`TestNativeMappedHistoricalOwnerRead` 与新增 `TestNativeMappedHistoricalTurnRead` 均 PASS。后者用真实 `Submit` 的 Turn/Job + committed `evaluate_fault_chain` Step + 触发器写入的 answer/message/event，验证故障链/Turn/Messages/Events 正向读取、冻结字段不变，以及未映射/异主体/异租户/收窄 scope/撤权与严格 Turn/Get/Submit 拒绝。证据 evidence/gpt-historical-r19/。
- 仍未验证：前端视觉/交互与 SSE、查询结果/正式引用/审计域会话、映射冲突并发、部署新制品与浏览器联调。任务库仍 schema57。
- 继续项（未在本轮推进）：P2-05（Job/Worker 显式 native authority、无 OIDC 冷启动）、P2-02/P2-03 视觉状态、P1 正式页面入口与全分辨率对比、P3（SL-049 同组日志样板）、P4～P7、P8 全量验收。OPA 摘要只阻塞 Bundle/冷装，不阻塞这些独立源码/UI 任务。

## r20（2026-10-23）：P2-05 业务Job/Worker原生权威 + 原生首租户

- 业务 Job 持久 identity source/native userID/authorization version（operational 也有，legacy 显式记 legacy），`AssistantTask.Valid` 与冻结摘要继续校验；Worker 按持久来源复检：native 用 `NativeOperatorScopeForJobTx`（`investigation.job_native_operator_scope` + `graph.EffectiveNativeTx`，与准入同一 scope/revision 实现），audit 用既有 `AuditScopeForJobTx`，仅历史空来源走 legacy 且 native 不回退。
- 迁移 59（门 + 投影，RLS 既存分支逐条保留）；迁移 60（migration_role 对 tenants 仅 INSERT）。改动 `internal/graph/protocol.go`、`internal/investigation/assistant_v3.go`、`internal/assistant/repository.go`。
- 真实 PG（1→59）`TestNativeOperationalJobWorkerAuthority` PASS：无会话 Claim/取输入、租约到期接管、撤权/重授/停用/收窄/篡改/跨租户拒绝、取消拒绝、`AUTHORIZATION_REVOKED`。
- 原生首租户：`identity.CreateFirstTenant` + `opsctl bootstrap first-tenant-native`；真实 PG（1→60）`TestNativeFirstTenantBootstrap` PASS：原子创建、change_required、审计、重复、非引导职责、并发唯一胜者。证据 evidence/gpt-native-job-r20/。
- 下一轮独立任务：Action 执行/巡检同类边界（`internal/action/read.go:11`、`postcheck.go:58`、`postcheck_report.go:165`、`service.go:78`）、安装器 native 闭包与无 OIDC 冷启动、P2-04 查询结果/正式引用/审计域/映射并发、P2-02/03 与 P1 视觉、P3 SL-049、P4～P7、P8。任务库仍 schema57；59/60 未部署。

## r21（2026-10-23）：P2-05 阶段返修（Worker 授权锁）＋Action 权威接线＋首租户 CLI 真跑

- P1：61 新增 `platform.native_operator_scope_locked`/`platform.native_authority_locked`（VOLATILE，账号→授权行锁，持至提交），撤销 runtime 对 59 解锁投影的 EXECUTE；`graph.EffectivePersistedNativeTx` 成为 Job 与 Action 共用的持锁复检。真实并发双向验证（Worker 先→管理等待 55P03；撤权先→Worker 拒绝），停用/收窄/重授不复活。
- Action：62 增 `action.executions.authority`，创建时持久来源/user/version，`RecordPostCheck` 走 `Execution.authorityScopeTx`（native 持锁、legacy 原路径、不回退）；SP07 legacy 回归 PASS。native 执行后检查用例待补。
- 首租户 CLI：一次性库 1→62 后真实进程运行成功、重复拒绝；前滚前置 `GRANT CREATE ON DATABASE ... TO schema_owner` 已记录，安装器/runbook 需包含。
- 下一轮：安装器 native 闭包与无 OIDC 冷启动/重启/前滚演练；Job 签名 Context/工具结果/取消的独立 Worker 运行；Action native 后检查用例；P2-04 剩余读取与映射并发、P2-02/03 与 P1 视觉、P3 SL-049、P4～P8。任务库仍 schema57。

## r22（2026-10-23）：审计投影名+共享锁死锁+Action来源+证据一致

- 修 P1：audit 分支原调不存在的 `investigation.job_native_authority_locked`，现改 `platform.native_authority_shared`；原生 platform_admin 审计 Job 准入/Claim/取输入正向 PASS(181s)，撤权后新审计 Job 拒绝并置 AUTHORIZATION_REVOKED。
- 修 P1：61 的 FOR UPDATE 投影与 API 入口 `authorize_native_session_role` 的 FOR SHARE 形成升级死锁；63 改 FOR SHARE（账号→授权同序，与 API SHARE 兼容、与管理工作写入冲突），并撤销 61 两变体的 runtime EXECUTE。同账号双 API 事务并发复检 PASS(148s)；撤权/收窄等待、撤权先提交拒绝、停用/重授不复活 PASS。
- 修 P2：`Authority.nativeForRecheck` 只接受完整 native / 显式 legacy / 历史空编码；未知 source 与混合字段拒绝，含单测。
- 证据：r22 source.json 由最终树脚本生成并 `shasum -c` 复核通过。
- 下一轮（未完成）：独立 Worker 签名 Context/工具结果/取消/异常、Action 原生后检查真实用例、CLI 首次改密、安装器 native 闭包与无 OIDC 冷启动/重启/前滚、P2-04 剩余读取与映射并发、P2-02/03 与 P1 视觉、P3 SL-049、P4～P8。任务库仍 schema57。


## 最新断点 r24（2026-10-27，优先于旧冷启动/视觉描述）

远程已恢复并重新核对mssc身份。现用独立namespace `ops-native-v12-r24-20261026`、目录 `/data/ops-v12-native-visual-r24-20261026`、PostgreSQL17.11/15454/schema70、预览 https://192.168.0.63:30471/login。真实first-tenant CLI与API/独立审计Worker/Web/OpenBao/Archive无OIDC启动通过；MFA只授API，Worker访问403；同Pod API/Worker/reviewer投射凭据自动续取后仍有效。10月27日再次重建API/Worker后，同会话保持有效，退出后旧Cookie401；旧签名段未变，生产S3读/解密/摘要/签名/前驱链验证范围1～49共7段通过。

真实浏览器已验证登录/首次改密/自助改密、MFA注册/恢复/重新绑定、管理员密码与MFA重置、零角色自助安全和业务拒绝、独立管理员授权撤销、禁用持久化与迟到结果恢复。MFA enrollment owner pending/confirmed GET200不泄露秘密、其他管理员GET404。测试为最终178 Vitest+6 SSE、Go race/contract/生成及构建通过，范围以r24结果为准。SL-003稳定前景660×424/header76与固定切片尺寸一致；内容/背景/完整逐状态像素仍不通过，不提升458项整体验收。

实际版本为API/Worker final二进制、Web-r4、`source-final-2/`，源码归档哈希71a4480aa1d67094585c2507689a5f179d693e047a71e93abd375c6b9a431a74。当前是挂载源码制品canary，不是新OCI准入或不可变Bundle验收。SP06业务dispatcher未配置，不能用审计Worker归档冒充业务Job签名Context/工具结果/接管/取消。1/2与P1/P2保持未完成，P3～P8剩余不变。所有早期失败、动画帧误采和脚本导出失败保留；凭据仅远程private，不导入仓库。

详见 [r24记录](evidence/gpt-native-visual-r24/README.md) 与 [结果](evidence/gpt-native-visual-r24/result.json)。

续接先核对当前HEAD/工作区及r24实际Pod/容器，不重跑prepare/init/bootstrap或重复已完成的MFA重置。凭据与恢复材料仅任务private。当前API/Worker final、CLI ops-r24-final-opsctl、Web-r4；旧root/source与30467～30470不是本轮最终版本。

优先未关闭项：SP06业务独立Worker签名Context/工具结果/接管/撤权取消与异常恢复；正式安装器/Bundle native闭包和切换；27主状态及MFA补充状态完整视觉/错误交互。按依赖推进SL-049与P4～P8，不恢复虚拟化/Trace，不将本轮局部通过写成V1.2完成。

本轮收尾校验：validate-progress.py通过458状态/177合同结构核对；新增evidence/contract-adaptations.json登记真实原生账号合同增量，原slice-map不改。移除原基准合同或加入未登记合同的负向自检均拒绝。结构检查不代表产品六维通过；失败记录已保留。


## r25（2026-10-27）：运行中Dispatcher边界与MFA读取状态

新增正式Dispatcher原生运行中取消/撤权/停机接管回归，远程任务PG15454独立随机库/schema70三项通过；旧上下文拒绝、平台取消部分结果不被迟到成功覆盖、新generation可发布。签名器与调查器仍fixture，不算真实SP06业务Worker运行。失败测试假设及数据库审计保留。

修复MFA未知读取状态被显示成未启用/恢复码0，改React Query+QueryState并覆盖加载、失败、重试；180 Vitest+6 SSE/TypeScript/Vite通过，远程浏览器网络中断→真实GET200重试通过。30471当前Web为ops-v12-native-r25-web-r5/web-r5；r24旧容器停止保留，API/Worker/PG/Bao不改。来源为r24 source-final-2加r25 source-delta-2，见evidence/gpt-dispatcher-r25/source.json。

下一步仍为SP06真实独立Worker/来源与工具链、完整P1/P2视觉及正式Bundle；随后按依赖P3～P8。本轮未提升产品六维，不重跑一次性初始化/首租户/MFA重置。详见evidence/gpt-dispatcher-r25/README.md、result.json。


## r26（2026-10-27）：MiniMax真实连接402，第一项未完成

用户提供MiniMax-M3.1-Flash及https://api.minimax.cn/anthropic/v1/messages并授权真实验证。凭据仅远程任务private/minimax-m31-api-key，0600，续接直接读取，不输出或写Git。一次最小请求在mssc-direct返回HTTP402 insufficient_balance_error，请求ID072f36eff225abdaaf349c18d2fab315，未获得模型回答，不重复重试。

另核实Go Profile限制内部/v1、Python只准内部OpenAI llama3.1:8b-16k、Schema费用固定zero-charge并要求自托管权重证据，需显式扩展外部Anthropic/计费/来源合同，不能伪造材料或绕过准入。第一项真实业务Worker仍未完成；额度恢复后再验证协议/工具/usage并接真实固定Profile业务链路。局部阻塞不阻止其他视觉和独立任务。30471/r25 Web-r5及API/Worker/PG未改。证据见evidence/gpt-minimax-r26/result.json、connectivity.json。


## r28（2026-10-27）：真实原生审计业务Worker链路完成

按用户明确指示将DeepSeek作为本任务受信内部模型，使用实际deepseek-flash官方OpenAI接口。原生会话/CSRF/幂等→真实签名策略与登记Context→独立Worker→真实投射身份/OpenBao Transit/PKI双向TLS→锁定Holmes→真实DeepSeek→真实audit.events工具统计→持久化/事实引用→浏览器和刷新回显通过。首个完成Job01a17b2e-7857-7d6b-973b-205c44bb3912。

真实模型运行中取消、临时独立管理员撤权（用户失权，AUTHORIZATION_REVOKED无结果）以及Worker/调查器进程崩溃、实际租约过期epoch1→2接管成功，均有独立实证，不使用fixture签名器/模型/工具、不修改租约SQL。临时授权已撤销。修复控制面查询工具在Worker侧仍用浏览器/legacy授权的遗漏；修复JSON模式未提供精确输出Schema。新增管理审计页审计问数入口，管理员无需operator，运维资源API仍403。

30 Python、Go相关包/contract与真实PG查询回归、181 Vitest+6 SSE/正式构建通过。旧失败/原始任务保留。30471现Web-r6；API r28-worker/ops-api-query，Worker r28-worker/ops-worker，调查器source-4，PG15454/schema70不变；准确源码/manifest/恢复见evidence/gpt-worker-r28/README.md、source-final.json、runtime/final-runtime.json。凭据仅远程private/任务Secret，不进入报告。

这是审计data_query业务链路通过，不是所有业务工具、回答质量、全部视觉或签名Bundle验收。P2-05仍in_progress，下一步继续原P1/P2视觉与正式安装切换、P3资源日志SL-049及P4～P8；原额度中断剩余与虚拟化延期不变。不重跑一次性初始化/首租户/已发布策略，不回滚身份数据。


## r29（2026-10-27）：认证失败态复刻与MFA409恢复

SL-002错误条按原HTML复刻，400/401统一不枚举账号的凭据错误；后台12～256密码校验、同源/限流未改。真实401等待/禁用/重试、网络abort通过，稳定真实400使用原型相同短错误输入。原r3参考无重采/无掩码，错误条14200像素完全一致；整图仍6259像素不同，不提升整体visual。

补MFA失效恢复：409/expired/revoked清除待绑定URI、旧表单/证明与过时提示，并更新请求代次。真实单挑战state=expired故障注入→API409→重新创建/取消通过，既有MFA确认时间和恢复码数量不变；自然计时到期仍不算本次验证。184 Vitest+6 SSE、构建及最终9项MFA增强restart点击检查通过，失败测试/捕获假设保留。

30471现Web-r9（ops-v12-native-r29-web-r9/web-r9），API/Worker/调查器r28和PG15454/schema70不改。来源、版本范围、恢复及证据见evidence/gpt-login-r29/README.md、source.json、runtime/web-runtime-r9.json。本轮无模型调用、无真实账号改密/角色变更，不重跑已通过Worker链路。P1/P2全量视觉与安全补充/正式安装仍未完成；下一步继续这些缺口，再按依赖P3资源日志与P4～P8。六维状态未提升。


## r30（2026-10-27，最新断点）：P2-02/P2-03 认证与账号安全整组真实验收

Web 现为 `ops-v12-native-r30-web-r14`（`/data/ops-v12-native-visual-r24-20261026/web-r14`，index SHA256 `8465ddec2b4632b7cc5ac518f39506255c9a65b17b7a58e085fd217377075ba8`）；r8～r13 停止保留。API/Worker/investigator 仍 r28，PG15454/schema70 未改；本轮无迁移、无模型调用、未重采参考。

已修：登录/改密传输失败文案不再回显「请先核对持久记录」；强制重绑 MFA 的登录路由竞态（登录页会话重定向与提交目标不一致，可能落到普通首页）；账号安全面包屑「未匹配页面」与顶层页面模块名重复；焦点环 `#208f89/3px` → 原型 `#aaa7ef/2px`，对话初始焦点按原生 dialog 落到关闭控件。

已实证（真实浏览器/真实 PG，证据在 `evidence/gpt-auth-r30/`）：登录空闲/加载/400/401/网络中断、同源与 CSRF 403、入口 429（第 60 次，`Retry-After`，窗口后恢复）、5 次失败锁定返回与错误密码完全相同的 401 并在 15 分钟后解锁、首次改密全套（刷新/错误当前密码/取消撤销/201 轮换/旧 Cookie 401）、首改密会话对业务/MFA/管理员接口 403、自助改密与账号菜单退出撤销、MFA 登记确认（10 码）/取消（零请求）/错误动态码保持挑战/自然闲置 660s 后真实 409 且清除旧密钥/再绑定推进 confirmedAt 并拒绝旧恢复码/管理员重置→`reset_required` 强制重绑/恢复码一次性消费撤销全部会话。

未验（本节随后被 r30 返修断点修订）：逐状态整页视觉（SL-001/002 的演示凭据与演示页脚、SL-005 登记的安全扩展字段「当前密码」）、operator 真实运行（任务环境无合法集群范围，未伪造注册）、正式不可变 OCI/Bundle/离线安装与切换、资源/业务工具路径的 SP06、完整 Trace 与虚拟化（继续延期）。15 分钟绝对到期与并发/跨账号反例已在返修中补验。六维：visual 0（返修后纠正）、interaction 5、api 5、persistence 5、authorization 3、live 5；P1/P2 与 V1.2 仍未完成。

续接顺序：先按 tasks.csv 依赖推进 P2-04/P2-05 正式部署闭包与 P1 剩余视觉，再 P3 资源日志 SL-049 同组（不以通用详情代替日志工作台），然后 P4～P8。不要重跑本轮已通过的一次性脚本（创建账号/重置/恢复）；验收身份 `r30.accept.*` 保留用于复核，`v12.r24.admin`、`r24.member.*` 未改动。

## r30 返修断点（2026-10-27，复审后）

- 敏感截图：仓库与该目录不再含任何 `mfa-*` 图；远程 23 张可疑图已移入 `private-evidence-quarantine`（0700/0600）。禁止把它们复制回仓库、再次展示或交给模型；后续截图必须走 `evidence/gpt-auth-r30/harness/safe-capture.mjs`（命中 otpauth/恢复码/动态码/base32 即拒绝出图），含秘密状态只留机检断言。
- 9 个 `r30.accept.*` 身份已全部管理员重置 MFA（`mfa_reset_required=t`、无活动登记/恢复码/会话），继续验收请用新独立身份；`v12.r24.admin`、`r24.member.*` 未改动。
- SL-001 `visual` 已纠正为 `not_verified`，全表 visual passed=0；不要再引用旧的 visual 1。
- 窗口到期实证：`runs/idle-expiry-confirmed.json`（闲置 600s，无活动）、`runs/absolute-expiry.json`（绝对 900s，t+9min 的活动只刷新闲置）；并发/跨账号见 `runs/cross-account-concurrency.json`。
- 运行手册 r30 段写明 Web-r14（index SHA `8465ddec…`）、安全前滚/恢复边界与"数据库恢复后必须重跑验收"。
- SP06 剩余范围限定为资源/业务工具路径；审计 data_query 真实链路 r28 已通过，不要笼统写成"SP06 业务 Worker 未完成"。
- 未通过/未验（继续登记，不继承旧 PASS）：SL-001/002/005 整页视觉（演示数据与登记的安全扩展字段）、operator 真实范围（无合法集群，不伪造注册）、正式不可变 OCI/Bundle 与离线切换、完整 Trace 与虚拟化（延期）。
- 下一步：按 tasks.csv 依赖推进 P2-04/05 正式部署闭包与 P1 余项（逐状态整页视觉、operator 合法范围），随后 P3 日志 SL-049 同组样板与 P4～P8。

## r31（2026-10-27，最新断点）：原生授权／历史主体／部署一致性

Web 仍 `ops-v12-native-r30-web-r14`（index `8465ddec…`）；API `r28-worker/ops-api-query`（sha256 `8fe8d7bd…`）、Worker `r28-worker/ops-worker`（`735a9044…`）、调查器 `r28-python` 源码只读挂载、PG15454/schema70 均未升级，本轮无迁移、无模型调用。API/Worker Pod 已按 `r31/api-pod-r31.json`、`r31/worker-pod-r31.json` 重建（比 r28 多一个 `/etc/r31` 只读挂载），configmap `r28-runtime` 的 `sp04.json` 增加真实集群条目（备份 `r31/sp04.before.json`）。

已就绪的真实链路：任务专属只读身份 `ops-r31-sp04-read` + ClusterRole `ops-native-v12-r24-20261026-r31-sp04-read`（17 个核心 Kind 只读、无 secrets、无写动词）+ 命名空间 `r31-sp04-lease` Role + Lease `r31-graph-lease`；集群登记 `clusterId 01a17c3c-80db-783b-9388-c6eee037f89b`（UID `79014400-9453-4cb4-9678-0fae47981d79`，endpoint `https://192.168.0.63:6443`，`apiEndpointRef openbao://secret/data/ops/r31-cluster/endpoint`）；来源 `sourceId 01a17c3c-ef66-70cf-9ff1-cad25a8bee34`（revision 2，scope=该 UID + `ops-native-v12-r24-20261026`）。SP04 采集 17 GVR 全部就绪，图所有权 epoch=1。

授权：`r31.op.1793086009209` 持 operator（该 UID + 任务 namespace，authorizationVersion 8）；`v12.r24.admin` 仍只有 platform_admin、**未加 operator**；`r24.member.*` 未动。管理员读资源 403、该 operator 读 39～43 条真实资源。`r31.hist.1793086582839` 为零角色账号，仅用于历史映射负例（映射被服务端守卫拒绝，未产生映射行）。

复现/继续时注意：
- 24h 集群只读令牌到期后需 `kubectl create token ops-r31-sp04-read -n ops-native-v12-r24-20261026 --duration=86400s` 更新 Secret `r31-cluster-read`，否则采集失败（API 登录与授权数据不受影响）。
- 读到瞬时 503 `GRAPH_NOT_READY`（worker `stage=lease_before_query`）是租约续约窗口内的 fail-closed，重试即恢复；不要为此放宽作用域或改租约 SQL。
- 回退：`kubectl apply -f r31/api-pod-before.json` / `r31/worker-pod-before.json`，并把 `sp04.json` 恢复为 `r31/sp04.before.json`。回退不影响数据库与身份数据。
- source 登记接口接受 `nativeTenant`，但 k8s 采集器要求为空——先按 PATCH 收窄再采集；这是待决策的语义不一致，不要用改产品校验来"修"验收。

未通过/未验：整页视觉（SL-001/002/005 及全表 visual 0）、正式不可变 OCI/Bundle 与离线切换、Web 静态发布与 chart 渲染一致性、P3（SL-049）与 P4～P8。历史归属只做了真实 PG 机制验证，未在 live 重演旧用户迁移（live 无旧主体，且映射守卫拒绝编造）。六维未提升，P2-04/P2-05 仍 in_progress。

## r32（2026-10-27，最新断点）：采集收敛/自动轮换/503 根因/原生管理

复审 5 项集中返修完成 4.5 项，全部远程实证，详见 `evidence/gpt-authz-r32/README.md`。要点：采集身份收敛为 Worker 自身 SA+命名空间 Role+6 类集群级 Kind（旧全局 ClusterRole/SA/静态令牌已删除且旧令牌 401）；采集凭据为 600s 投射令牌自动轮换（三次轮换摘要+跨轮换连续 200 实证）；503 根因为续约全程 ~500ms 超过 Confirm gate 300ms（实测 gate_timeout），修复后 60 连读 0 失败且所有权/过期语义不变；k8s 来源 nativeTenant 在全部登记入口统一拒绝并可显式修复呈现；B 账号生命周期（停用/恢复/双并发反例）通过。

chart（sp04.yaml、deployments.yaml、values.schema.json）已同步为收敛模型并新增渲染合同测试；`go test ./internal/{graph,source,httpapi,app}` 与 `./test/contract` 全部通过。

剩余（按优先级）：C 历史主体正反例需一次性独立测试库+隔离 API/UI 环境（live 无旧主体、守卫正确拒绝编造）；D 无 OIDC 隔离冷启动（整栈新 namespace+新库+chart 安装+first-tenant）；正式不可变 OCI/Bundle 物料缺口继续阻塞；P1 整页视觉与 P3 SL-049 不变。运行环境现为 r32 API/Worker（r32-worker/ops-api、r32-worker/ops-worker），回退路径见 README"版本与回退"。本地 r32 目录 `/Users/mssc/Documents/Code/ops/r32/40-account-lifecycle.mjs`（远程 `r32/` 为运行副本）。凭据仍仅远程 private。
