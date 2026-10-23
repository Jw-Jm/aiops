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
