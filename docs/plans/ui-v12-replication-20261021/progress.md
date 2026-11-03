## 2026-11-03 r51 真实 Pod 指标链路（GPT直接执行）

30471 已实际接通 kubelet/cAdvisor + 精确官方 KSM v2.17.0 → VictoriaMetrics v1.116.0 持续采集 → 真实密码/TOTP登记来源 → 原生运维会话/当前授权/签名owner → 四类Pod固定指标模板和趋势页面。CPU、内存、重启增量、phase四种趋势12点与原生查询逐点一致；无导入/模型生成指标，无新SQL或AI Job。覆盖始终unknown/partial，无数据不推断零，缺点不连线。

真实Chrome正例及跨集群/PromQL注入/CSRF/不存在UID守卫通过；停止KSM但指标库HTTP仍200时资格撤销、查询503，恢复后原证明恢复；TSDB保留持久目录重建后故障前窗口可读。同名Pod新旧UID在重建前后窗口互斥，600s实际投射令牌自动轮换后四类采样仍新鲜，8项原生RBAC正反例通过。原r49日志固定结果回放及摘要保持。

修复真实owner初始化阻碍：严格匹配且已验证的归档事实不再重复封装；保留物理Recover/Read，将prepare/recover期限12s对齐有界8s保留保护维护，不解除加密/来源/保留/授权守卫。首次r51与r51b未就绪失败保留，r51c恢复。代码生成、交付来源门禁、Go三包/race、16项定向UI、构建及OpenAPI/UIV12/来源合同通过；计划静态校验179合同且六维不提升。

证据 evidence/gpt-live-metrics-r51/README.md。仅单节点任务namespace四类Pod指标；P4-01为in_progress，完整视觉/多节点/双集群/Node/长时缺口/持久指标历史与正式Bundle未验；新TLS证书30d需正式续期。子秒窗口首轮503保留，只验整秒趋势；截图探针拒绝公开UUID误命中，未留截图、不声称视觉通过。无Git提交/推送，前次同步基线0d6caa2不变；30472/模型/SP05未变，虚拟化继续延期。

## 2026-11-03 r50 迁移闭包续期与全新库引导修复（GPT直接执行）

补齐71/72到arm64/amd64迁移对应源码及镜像内置锁，两个架构均在远程断网/空缓存容器离线重建，与原准入二进制逐字节一致。精确离线amd64 CLI实际完成全新1→72、带保留tenant的70→71→72、重复执行/低目标不回滚和负目标拒绝。

首轮全新库实测44创建identity schema因schema_owner缺少数据库CREATE权限失败；正式opsctl账号引导在原version1/空租户/超级用户及同一事务守卫内补授予仅schema_owner，API/Worker无新增DDL权限，冻结SQL/迁移CLI不改。基线计数72、安装参数Namespaces schema/共享fixture与既有Helm严格范围守卫对齐。失败与最终实证见 ../../evidence/migration-renew-20261103/README.md；完整交付来源门禁复检，正式Bundle整体验收仍未完成。

无提交/推送、无新增SQL迁移、无现有任务库/运行组件升级；隔离测试PG及构建容器已清理。ARM64原生SQL、完整Bundle安装和产品视觉未验；r49持续日志保持，P3/V1.2/六维不提升，虚拟化继续延期。

## 2026-11-03 r49 任务环境持续日志归档（GPT直接执行）

30471 已接入固定源码构建的 vlagent 原生 Kubernetes collector 与独立 VictoriaLogs，持续读取真实 CRI 文件，持久检查点和 1GB 队列，保留30d。新来源通过真实管理员密码+TOTP 登记；原当前单容器按需来源仍显式可选。Pod/Namespace UID、容器身份与原始纳秒时间已核对，消息不能覆盖身份字段，样例脱敏。每10s用真正存储事件刷新水位和30s资格，错误/断连撤销，动态水位不改变固定来源证明或历史结果。

真实页面两次各20条样例，刷新后4条自然新增；历史回放和外集群403通过。存储暂停约32s、期间采集器优雅重启后找回3条自然日志，固定窗口仍14条、历史摘要不变；中断时归档来源退出、原生按需仍可用。双容器及4个重启容器身份、同名Pod新UID隔离、删除后旧结果403通过，探针已清理。API/Worker/collector健康，schema72不变、30472/模型/SP05未动。

证据与构建补丁见 evidence/gpt-log-archive-r49/README.md。采集限本节点任务namespace（上游API需要跨namespace Pod元数据只读权限）；覆盖仍unknown/partial，不声称严格一次。kubelet真实按大小轮转、磁盘满/长断连/缺口时间线和正式Bundle仍未验；TLS证书30d需后续正式续期。P3-02与V1.2保持in_progress、六维不提升、虚拟化继续延期。本轮无提交/推送；前一次0d6caa2代码同步仍是GitHub/远程仓库基线。

## 2026-10-29 r48 实际 Kubernetes 原生日志来源（GPT直接执行）

用户明确要求“接通实际日志/来源改成实际的”。30471现为r48b API/Worker+r48 Web（index b05931cb…）、schema72；已登记Kubernetes源revision3、原CA/自动投射身份、原用户授权不变。PodLogs显式启用，通过当前Pod/Namespace UID和单运行容器指纹、实际pods/log零行权限探针，再按固定窗口直接读真实日志。当前runtime不再路由r37导入快照，旧容器/存储/登记/结果保留；不续期或导入日志。页面显示“Kubernetes 原生日志（当前容器）”和刷新来源，轮转与总体未知保留partial/not_measured，不以空样例冒充零。

真实浏览器两条查询各20条样例；刷新新窗口后3条自然产生在第一窗口截止之后，未插入测试消息。原结果摘要不变，重载历史恢复、外集群canonical403通过。独立kubectl按纳秒时间/脱敏内容核对40条样例，READ ONLY PG与实际PodUID/NamespaceUID/来源revision/credential/schema72及旧快照摘要吻合；r48→r48b组件重建后按原摘要读取通过。30项UI/构建、Native/Chart/原观测定向检查、远程独立PG生命周期及原生来源授权通过。

真实问题已保留并修复：任务Bao CRL于15:18:07(+08)到期导致组件启动拒绝；正常轮换并启用原生auto_rebuild、expiry仍72h，签名/CRL/TLS检查均保留。历史列表对退役快照重复探测native来源导致503，r48b提前拒绝不受runtime支持的来源，真实历史复验通过；已有Lease/路由慢日志不称永久消除。脚本/fixture/定位失败见evidence/gpt-native-logs-r48/README.md与分层记录，后续恢复没有重复创建新查询。

本轮是实际按需读取当前单容器日志，不是持续转发/集中归档，不覆盖轮转前/前次或多容器。模型仍data_query、SP05未启用、30472不改；两个真实集群、完整视觉/全栈重启、持续归档和P3其余/P4～P8仍未完成，六维不提升。无模型调用/提交/推送，不恢复虚拟化/Trace/生产。

## 2026-10-29 r47 资源筛选候选与重置（GPT直接执行）

30471 API/Worker/Web前滚r47（index441b5e97…），schema71、来源/模型配置与30472不改；r44后端和r46d静态Web保留。C002新增可选includeFilterOptions，由同一已授权图快照生成完整类型/namespace/集群级存在标记，不受本页和业务筛选限制；候选不扩大授权，旧请求默认无新字段，flag绑定签名/游标。namespace=__cluster__仅匹配已授权的空namespace。前端下拉/搜索和URL筛选接通，读取失败清掉AntD缓存候选；重置清筛选/游标/排序，保留集群和每页条数。

25定向UI与构建通过；新增namespace选择清游标及精确资源范围候选断言最终单独复核通过。图回归（排除既有20k容量构图）、HTTP及合同通过。首次广跑卡在既有20k构图，终止后有界90秒再跑同处超时；原失败证据保留，不称整包或容量通过，不为本轮修改容量实现/豁免。

真实Chrome确认6Kind/1namespace来自38条完整授权资源，limit1/无匹配/未授权namespace筛选不截断或扩权候选；集群级无授权返回空且不出现候选，非法flag400；页面下拉/重置/刷新和选Pod实查通过。READ ONLY PG核对38个身份，Kubernetes核对Pod/Service/StatefulSet UID，源码制品哈希/PodUID/schema71、旧日志摘要及模型/SP05原门禁一致。0业务写入/0页面异常；首轮弹层离场动画造成定位混入旧下拉的失败保留，按对应list控件定位后通过。

证据evidence/gpt-resource-filters-r47/README.md。未截新图或重采参考、未提升六维/P3；第二授权namespace和集群级正例仅fixture，完整视觉/双集群/持续来源/模型资源排查/事件运行与P4～P8仍未完成。无提交/推送。

## 2026-10-29 r46 资源详情、来源追溯与日志深链（GPT直接执行）

30471最终Web为r46d（index3ffed369…），API/Worker仍r44、schema71，30472未改。详情以当前cluster C002精确根核对后读取C003，scope/id绑定与非首节点定位，无权限/缺失不回退；四原型页签、来源登记版本/覆盖、共享关系图与新读邻居抽屉、范围链接、日志canonical深链、精确事件过滤/独立分页已接入。日志无当前资格或读取失败时禁用查询，失败复查不保留“资格已核对”。旧高级图入口保留。

62定向Vitest与最终构建通过。真实Chrome概况/版本/复制/关系抽屉、日志cap200 sources=[]且无POST、跨集群403/缺失无C003回退通过，0业务写入/0页面异常。READ ONLY PG与Kubernetes核对真实UID/active来源revision3/schema71及旧日志摘要，r44后端Pod未变。安全取得概况viewport/fullpage截图；实际长标签不再侵入值列，头60/内边距20/双栏间隔20核对。

真实阻塞：C029/C031均409 CAPABILITY_DISABLED，API/Worker的SP04配置实际无SP05块，保留“未启用/未取得读取结果”，未用DB0行冒充空列表，未绕门禁。图卡片/截图不等于完整视觉；全部Kind、非空SP05/双集群/持续来源/重启、AI资源排查与P4～P8仍未完成，六维不提升。原日志快照已到期、没有续期或新查询。源码/制品/分层证据见evidence/gpt-resource-detail-r46/README.md、source.json、web-runtime-r46d.json、browser-detail.json、runtime-oracle.json与visual-review.json。未提交/推送。

## 2026-10-29 r45 资源选择、对比与本页导出（GPT直接执行）

**10:48(+08)只读资格复核：原r37日志快照10:45截止已自然经过，同Pod capabilities真实200/sources=[]，不再提供该source。未POST新日志查询、未延长资格；此为来源资格到期，不是保存结果30天TTL自然到期验收。当前新的日志查询缺合格来源，仍可独立推进资源及其他模块。证据evidence/gpt-resource-controls-r45/source-expiry.json。**

30471 Web前滚r45，API/Worker仍r44、schema71，30472未改。目录选择2～4个本页资源，对比前复用C002同过滤条件+规范ID精确重读；关闭/重开重新核对，失权或成员缺失不回退旧表。导出点击时固定本页ID与匹配总数，先C002重新授权读，再输出当前投影CSV；记录UID/集群/来源/观测/读取时间/图版本/请求ID/时效/部分覆盖，明确仅本页，资源变更/错误拒绝下载，切换集群丢弃迟到响应，文本公式前缀/引号按纯文本处理。

33定向Vitest与正式构建通过。真实浏览器2资源新读对比、下载5行/点击时匹配10行通过；独立CSV解析/当前Kubernetes/READ ONLY PG证实5个规范ID/UID/namespace一致，CSV来源为新读取且保留部分覆盖警告，原日志摘要和r44后端Pod不变，0业务写入/0页面异常。无模型调用、来源资格变更或新迁移。详见evidence/gpt-resource-controls-r45/README.md、browser-controls.json、export-oracle.json。

P3-01/SL008仅追加实证，六维不提升。完整目录/详情/总览视觉、筛选候选/责任归属、完整来源追溯/持续来源、两真实集群与完整重启、r42实际AI创建执行及P4～P8仍未完成；原日志快照截止10:45(+08)未延长。没有长期负载/提交/推送。

## 2026-10-29 r44 资源目录搜索、健康投影与速览（GPT直接执行）

30471 API/Worker/Web前滚r44，schema71不变，旧r39c二进制/r43Web保留；调查器与模型Profile、30472未改。补q搜索合同（名称/类型/稳定UID，不区分大小写，≤256Unicode字符），授权过滤、汇总、排序和游标复用同一条件；保留原name精确筛选。修复健康过滤按真实状态、页面却读不到health的共享投影缺陷，列表/详情/拓扑统一返回health和resourceDomain（不改canonical domain），不修改不可变底层属性或历史结果。目录补UID/领域/命名空间/来源观测/速览与详情、每页10/5/20/50及URL恢复，速览重新GET C003，拒绝隐藏旧内容/链接。

Go graph/httpapi/contract通过；最终分类增量定向Go、28UI+最终4目录回归及正式构建通过。真实浏览器大写UID精确1条/匹配汇总1/正常Pod+Kubernetes领域、速览和刷新恢复、未授权namespace零汇总、257字400、拓扑正常状态通过，0业务写入/0页面异常。独立KubernetesReady/UID、READ ONLY PG/schema71、原日志摘要及实际二进制哈希核对通过。两次拓扑状态文本定位误报（圆点参与文本）与静态部署旧容器名错误均保留；无数据伪造，无模型调用或资格延长。

证据见evidence/gpt-resource-catalog-r44/README.md及分层JSON。完整视觉/目录剩余交互、资源详情、预置/持续日志来源、两真实集群与完整重启、r42实际AI创建执行及P4～P8仍未完成；六维不提升，P3-01保持in_progress。未做长期负载、提交或推送。

## 2026-10-29 r43 模型门禁实核与正式关系拓扑（GPT直接执行）

30471 Web前滚r43（index559e621a…），API/Worker r39c/schema71及30472未改。开放正式关系拓扑，复用C002/C004/C003，当前集群精确根核对/分页、1或2层、200节点400边、图列表/缩放/重置/键盘节点/重新读取详情与URL刷新接通；指定根拒绝或缺失不回退，跨范围迟到隔离。24定向Vitest及构建通过；真实Chrome5节点4边/一层4节点、真实缩放与刷新、跨集群403、真实B命名空间空根无回退、未授权集群无请求通过。独立PG与Kubernetes5UID/4关系oracle一致，0业务写入/0页面异常。

真实Pod挂载与Profile/材料/已发布策略交叉核对：taskKinds仍仅data_query，现有签名策略已支持受限operator/agent只读工具，但r28评测摘要只指向协议探针，缺资源排查专项评测与准入实证；不直接放开或绕门禁。原r41/r42摘要未改、资格未延长，无模型调用。截图探针拒绝，整页视觉不提升。详见evidence/gpt-topology-r43/README.md、model-gate-audit.json、browser-topology.json及oracle-persistence.json。P3-01改in_progress，六维不提升；总览目录/完整视觉、预置/持续来源/两真实集群/完整重启、r42实际创建执行与P4～P8仍未完成。无提交/推送。

## 2026-10-29 r42 日志到AI范围联动（GPT直接执行）

30471 Web前滚r42b，API/Worker/schema与30472未改。日志成功结果通过clusterUid+logQuery跳转助手，重新核对持久结果后显示精确资源/来源版本/微秒窗口，锁定运维域/资源排查/集群/命名空间；创建前再GET结果，随后复用既有create与事务内资源授权。403/410不回填旧缓存，切换集群丢弃迟到引用，保留模型门禁；不自动发送消息或把日志样例充作AI证据。

36项定向UI与构建通过。真实浏览器已通过日志→助手范围锁定、过期410/未知404拒绝；当前模型能力仅data_query，资源排查创建被门禁阻止。READ ONLY确认原结果摘要未改、0条r42会话；真实创建/持久化/执行尚未验证，不能把单位夹具通过当作运行通过。原快照资格未延长。详见evidence/gpt-log-ai-handoff-r42/README.md；P3/P5/六维不提升，完整视觉/预置/持续来源/两集群等继续待办。无提交/推送。

## 2026-10-29 r41 精确窗口选择与样例导出（GPT直接执行）

30471 Web更新r41b；API/Worker仍r39c、schema71，30472未改。双日期输入改为单窗口选择（合格来源/已保存历史窗口），原始RFC3339微秒字符串直接传递，删除手填日期专用重复校验。查询检查与导出移到结果头部，修正旧span选择器把按钮文字染灰的问题。

导出先复检C168结果，成功后只把点击时可见的脱敏样例生成CSV，不建新查询；保留UID/命名空间/来源版本/半开窗口/queryId/resultDigest及总体/返回/导出三种数量。CSV引用转义与公式前缀按纯文本；403/410/失败不下载旧缓存，资源切换丢弃迟到导出，空可见样例禁用。15项UI/正式构建通过；真实浏览器下载、数据库和CSV解析证实186总体/20返回/1导出，重新GET一次、无额外create。最新r41b只读截图0新查询通过。

证据见evidence/gpt-log-window-export-r41/README.md及implementation-bindings.json。P0 query-export local行仍为原型阶段基准，实际logs分支明确增加C168前置读取，未把其他观测模块宣称实现。整页仍缺AI范围/预置入口与间距/公共壳核对，P3/视觉/六维不提升；固定快照资格未延长，持续来源/两集群等仍未完。无提交/推送。

## 2026-10-29 r40 日志时间桶、字段侧栏与列表（GPT直接执行）

30471 Web已更新r40b，API/Worker仍r39c、schema71，30472未改。复用现有返回快照实现5分钟样例分桶、本地时间筛选/清除、级别分布/资源字段侧栏和可点击日志列表；按源CSS采用190px侧栏、64px日志行。侧栏级别查询复用保存请求，仅改filters；局部筛选不发新来源请求、不修改总体。

10项UI与正式构建通过；真实浏览器/SQL验证总体186与样例20分离、6柱合计20、空桶不是来源无日志、排序/换行/详情身份不变，以及INFO真实查询总体6/样例6。对应默认/换行/关键词空态的产品截图已回收；旧截图检测误判公开UUID，本轮仅排除明确授权元数据中的完整UUID，保留未知标识/恢复码/URI/OTP输入及前后探针，5项捕获协议用例通过。首次本地helper导入错误保留，捕获前修正。

完整证据见evidence/gpt-log-layout-r40/README.md、browser-layout.json、persistence.json、capture-states-r40b.json及visual-review.json。整页仍有AI/预置/导出入口、双日期控件和顶部间距差异，视觉不提升；来源仍是单Pod固定窗口快照（资格未延长），资源聚合拓扑、AI与预置视图、持续来源和两集群等仍待完成。P3保持in_progress，未提交/推送。

## 2026-10-28 r39 历史分页、完整请求与过期（GPT直接接续）

当前30471：API/Worker=r39c、Web=r39b、PG17.11/schema71；30472未改。保持data数组，增加owner/tenant/cluster限定的UUID游标和limit(1..50，默认20)，界面追加分页且逐页重新核对权限/来源。读取从不可变request列返回完整模板与budget，旧View也补齐；历史级别再查只改filters，不丢微秒窗口或改用当前预算。真实浏览器/SQL证实capability20、保存预算7、总体186/样例7，历史加载更多与复制通过。

过期：当前授权/来源登记复检在前；状态200返回expired且隐藏结果，results/cancel为410 RESULT_EXPIRED。排队过期不发来源，执行中到期停止平台接收，Finish保留授权复检且拒绝过期发布。独立真实PG覆盖旧View补齐/游标/撤权/过期读取、领取/停止/迟到；预览HTTP过期使用明确标记的元数据fixture，不代表实际业务等待30天到期。9项UI、定向Go/合同、生成和构建通过。

远程6443曾拒绝连接，未修改共享服务；后续只读复查已恢复。r39c重启首轮读取503保留，随后过期接口200/410通过；未声称永久消除间歇性503。详见evidence/gpt-log-history-r39/README.md、source.json及分层证据。完整视觉/时间桶/字段侧面/AI与预置视图/两集群等仍未完；P3与六维不提升，未提交/推送。

## 2026-10-28 r38 日志查询检查与详情（GPT直接执行）

30471 Web前滚r38，API/Worker/schema与30472未改。补查询检查三页签和持久化元数据复制、日志字段/脱敏原始内容、相邻样例、时间排序/自动换行、按级别真实再查；读取拒绝时隐藏旧缓存与抽屉。6项Vitest/正式构建通过。真实浏览器与剪贴板通过；SQL确认186总体/20样例的新结果摘要，以及同UID/来源/窗口/budget仅增加ERROR后的真实总体180/20，排序/换行/检查不重发。

完整证据见evidence/gpt-log-detail-r38/README.md、source.json、browser-details.json、persistence.json、web-runtime.json。测试定位/时间字符串误报失败保留。截图探针拒绝仍保留，完整视觉不提升；资源聚合/拓扑、时间桶/字段侧面、AI范围/预置视图、历史分页/过期、完整预算读取和两集群等仍未完，P3继续in_progress，无整体通过。

## 2026-10-28 日志工作台恢复与正式页面（GPT直接接续）

30471 Web已前滚r37j（API r37/Worker r37g/schema71）；30472未改。日志导航开放。真实来源恢复后的新查询186总体/20样例已持久化；SQL复查旧摘要不变、终止查询仍无迟到结果，资源/Namespace UID一致。正式浏览器成功、测得零空态、本地筛选不重发、脱敏原文、来源说明、个人历史恢复通过；来源故障与运行中取消界面均已通过，来源已恢复；SQL确认失败/终止记录均无结果，终止不伪造来源已停止。

实际浏览器发现并修复两处：秒级表单向资格窗外截断微秒导致503，改为开始向上/结束向下取整；accepted→running版本竞争导致取消409，改为取消前读最新回执，冲突只刷新并提示再次核对，不自动重复写。4项定向Vitest和正式构建通过。失败与部署轮次均保留。截图严格安全探针命中疑似恢复码，相关状态保留JSON断言，未放宽探针/未提升视觉通过。

P3仍进行中：时间桶/字段检查、排序换行与相邻样例、精确范围AI/预置监控、历史分页/明确过期、两物理集群与完整视觉未完成；来源仍单PodUID固定窗口真实快照，不是持续日志采集。详见 evidence/gpt-log-workbench-r37/README.md；无提交/推送/主分支切换，无长期负载测试。

## 2026-10-28 当前运行/账号增量

用户转问全页面账号，已新建v12.fullview并独立授予两种角色，真实首改密与总览/资源/助手/用户管理页面通过；密码只在远程private文件并直接交付用户。现有保护账号未变。30471 API=r37、Worker=r37g、schema71，Web仍r36b。P3实际API已通过186/20成功、测得零空态、跨范围拒绝、真实来源故障与运行中取消；恢复后的新查询、SQL复查、正式日志Web部署/浏览器/视觉和完整合同仍待继续。证据/安全接续点见 evidence/gpt-log-workbench-r37/README.md；没有提升完整六维或整体任务完成。

## 2026-10-28 r36/r37 当前接续（GPT直接执行，进行中）

r36补齐受控历史query-result/固定RCA与处置建议引用的映射/收窄/来源失效/版本/撤权组合，远程真实PG与HTTP通过；不代表真实旧库迁移。核对原生登录/首改密/MFA状态/用户授权安全画面，修复公共壳、状态条、表格、标签和按钮；34定向用例/构建通过，完整视觉仍未通过，未重采736参考。30471/30472 Web先更新r36b。

r37开始P3：独立持久化查询/原生事务复检/来源与UID资格/租约/取消/历史/结果及正式日志工作台已实现；远程一次性库schema71生命周期通过，3项工作台机制测试、生成/构建通过。30471 API/Worker已前滚schema71，真实任务日志快照已登记/测量，当前继续实际API及浏览器验收。范围仅单PodUID、固定窗口，不宣称全量持续日志采集；Kubernetes原持续采集未停。管理员和运维角色独立，已登录任务operator核实总览/资源/助手页面存在。详见 evidence/gpt-references-visual-r36/README.md 与 evidence/gpt-log-workbench-r37/README.md。P3、P1/P2完整视觉、P4～P8及正式发行仍未完成；不因准备工作提升六维。

## 2026-10-28 r35 最新接续（GPT直接执行）

本轮按用户两项授权补齐legacy v2调查映射只读入口/列表/步骤/SSE逐事件复检及前端只读取消禁用；真实PG/HTTP同时验证旧执行输出、审计独立授权、证据引用、流中撤权和旧归属不变。来源revision变更复用旧UID/RV归档ID的问题已修为投影v3，旧证据保留。真实无效采集凭据→拒绝503→恢复原投射身份→漏采更新/新更新持久化，以及第二namespace实际只读RBAC、同名资源独立UID、用户范围A+B后收窄A/撤权拒绝通过。完整证据见evidence/gpt-history-recovery-r35/README.md、result.json、source.json、runtime-final.json。

30471现为r35 Web/API/Worker/source3双任务namespace；30472为r35 Web/API+原r34 Worker。原r24 admin/member密码/MFA/授权未改，r31测试operator已恢复原A范围（授权版本正常递增、测试会话失效）。新迁移无，仍70；无新模型调用。稳定窗口12次200不代表启动/故障窗口永无503。实际旧库实迁、历史query-result/formal-reference全组合、P1/P2完整视觉与正式Bundle/离线发行仍未关；P2-04/05及六维状态不提升。下一步补剩余引用组合和P1/P2视觉，再按依赖SL049；不重复736参考或本轮已验证的凭据/范围演练。

## 2026-10-28 r34 最新接续（GPT直接执行）

完成受控历史inventory真实CLI导入、管理UI显式映射/再次验证/错误摘要拒绝/旧会话失效，以及新namespace/新库无OIDC的Web/API/Worker/调查器/CLI启动与进程重建；历史会话只读前端修复已部署独立30472。原30471与既有用户不变。详见 evidence/gpt-coldstart-r34/README.md、result.json、source.json、runtime-final.json。会话重启有效，但故障502后页面需刷新恢复，未保证原路径。新库0 Jobs/0模型调用；受控旧主体不是实际旧库。P2-04/05和六维状态不提升。

下一步：历史执行/审计/证据引用及双角色组合；采集真实凭据失效恢复、第二namespace实际授权；P1/P2视觉收尾，再按依赖P3资源日志样板及P4～P8。独立源码冷启动已验证；正式不可变OCI/Bundle/离线安装切换仍缺合格材料。中断前脚本泄露的单个r34测试会话已自然过期401，失败回执已脱敏，未改其他会话，见session-containment.json。用户新增代码约束：入口一次校验；不重复判空或滥用try/catch；单次逻辑不乱抽函数；低概率防御先征求同意。

## GPT r23 接续（2026-10-26，优先于下方历史版本说明）

本轮用户授权完成四项：MFA 缺陷、集中失败定位、P1/P2 收尾、P3 资源日志样板。由 GPT 直接执行，无 CodeBuddy。当前基线 main/082f87d，未提交工作树；前三轮 r13/r22 与 2026-10-25 主分支评审/部署记录按各自版本保留。

- MFA：新增向前迁移 70，窗口重置时清除过期锁，保留仍有效锁，单一 attempt_at 保证窗口判定一致；1～69 原文未改。远程独立 PostgreSQL 六种窗口/阈值边界通过。70 已在临时集成库及新 ui_v12_r23_preview 副本执行，原 r15 库仍69，原共享库仍57；不能再原地改70。
- 集中验收：原 11 项顶层失败已逐项到达正式边界并通过，最终35项顶层PASS/无SKIP。修正夹具：重复operator授权改为收窄现有grant；来源实例用完整UUID；已有policy activation使用真实revision；变更目标前创建挑战；发布真实profile v2再验证绑定；Finding恢复后读取新Incident revision；挑战证据由受控owner读取不扩大runtime权限；过期fixture保持created<idle；管理proof用实际管理员密码；无客户端证书断言TLS拒绝。旧RPC的无效请求/虚构task工厂由有效graph.Query和既有真实Submit任务200回归取代，保留窄权限反例。
- 追加P2机制：10项真实PG/HTTP回归通过，覆盖原生Action后检查、取消/unknown恢复、撤权、助手取消、source-step读回、first-tenant、映射历史、HTTP会话及迟到写拒绝。以上均为受控集成，不是安装态来源验收。
- 登录页：实测pending按钮未disabled且重复submit会abort旧登录并新发请求，已加inFlight单次保护及Form/Button disabled。18项前端定向/正式构建通过；新独立30470/API18091/metrics19091、任务专属runtime LOGIN、数据库副本57→69→70（原副本从69导出后前滚70）启动成功，真实浏览器pending禁用/401错误显示通过。未更新30469或旧预览，不把当前截图当成新黄金图。
- 原生来源能力读取修复：交互capabilities无持久JobID，旧实现只取nativeTaskProof会丢失当前session并签成legacy。现仅无Job的交互读使用服务端nativeProofFromContext；真实native任务仍须持久证明，未知来源拒绝。原实现overlay真实PG/HTTPS复现失败，修复后正向/撤权/非法来源通过；没有伪造任何来源资格。
- MFA现场：首次真实浏览器503，确认旧task TokenReview reviewer JWT已于10-23过期；在核对既有SA、audience、角色和CA后只续签任务凭据，角色/信任配置/Transit key/策略未改。30470真实Transit设置→刷新持久→恢复撤销→重新登录强制重设且业务403→新因子确认恢复原有管理员200全部通过；原账号与旧数据库未改。恢复码截图仅对10个秘密标签最小遮罩。新JWT仍为1小时测试凭据，不是自动投射轮换。
- 尚在推进：P1/P2全部视觉/安全补充状态、完整API+Worker无OIDC冷启动/重启/切换；P3六项合同及SL-049全组独立查询/历史/取消/来源链尚未实现，不能关闭P2或进入“样板已通过”。本轮证据 evidence/gpt-completion-r23/，原集中失败日志与本轮两次定向失败保留。

# V1.2 实施进度

## P2-05 阶段返修 r22：审计投影名、共享锁死锁、Action 来源严格性（2026-10-23；本段优先于下方历史记录）

- **P1 审计路径**：`nativeRoleScopeForJobTx` 原调不存在的 `investigation.job_native_authority_locked`，现改 63 的 `platform.native_authority_shared`。真实 PG：原生 platform_admin 审计会话 → Submit → binding 记 native/user/version → 无会话 Worker Claim + GetAssistantInput PASS；撤权后新审计 Job Claim 拒绝并置 `AUTHORIZATION_REVOKED`（`TestNativeAuditJobWorkerAuthority` 181.25s）。
- **P1 锁升级死锁**：61 的 FOR UPDATE 投影与 `identity.authorize_native_session_role` 的 FOR SHARE 冲突升级；63 改为 FOR SHARE（账号行→授权行同序），与 API 既有 SHARE 兼容、与管理工作写入（账号 FOR UPDATE/授权 UPDATE）冲突，并撤销 61 两变体的 runtime EXECUTE。`TestNativeAuthoritySharedLockNoDeadlock` PASS(148.12s)：两个同账号事务真实走 API 入口 SHARE → 受控屏障 → 并发 `CheckAssistantJobTx` → 均提交、无 40P01，Job 仍可 Claim。回归：撤权/收窄等待 55P03、撤权先提交拒绝、停用/重授不复活、operational 正向均 PASS。
- **P2 Action 来源**：`Authority.nativeForRecheck` 仅接受完整 native、显式 legacy 或历史空编码；未知 source/native 缺字段/legacy 带身份一律拒绝（含 9 组单测）。
- **证据版本**：r22 `source.json` 由最终工作树脚本生成，`sha256sum.txt` 经 `shasum -c` 复核 6 文件全部 OK。
- 仍未验/未做：独立 Worker 签名 Context 颁发校验、工具/结果持久化回显、取消/恢复/异常；Action 原生执行后检查真实用例；CLI 首次改密；安装器 native 闭包与无 OIDC 冷启动/重启/前滚演练；P2-04 剩余读取/映射并发、P2-02/03 与 P1 视觉、P3 SL-049、P4～P8。任务库仍 schema57，63 未部署。

## P2-05 阶段返修：Worker 授权锁 + Action 原生权威 + CLI 真跑 r21（2026-10-23；本段优先于下方历史记录）

- **P1 已修**：59 的解锁投影被 61 取代。新增 `platform.native_operator_scope_locked` / `platform.native_authority_locked`（VOLATILE，先锁账号行再按 binding_id 锁授权行，锁保持到调用方提交；版本不一致返回空并拒绝），并对 runtime duty 撤销 59 解锁投影的 EXECUTE。`graph.EffectivePersistedNativeTx` 统一在调用方事务内复检，investigation 的 operator/audit 分支都改用它。
- **并发证据（真实 PG，1→61/62 临时库）**：`TestNativeJobAuthorityLocksSerializeRevocation` PASS(6.33s)——Worker 事务内执行真实 `CheckAssistantJobTx` 后保持打开，管理侧 `lock_timeout=750ms` 的账号更新与授权收窄均返回 55P03（实际等待锁），Worker 提交后方成功，随后 Claim 拒绝并置 `AUTHORIZATION_REVOKED`；`TestNativeJobAuthorityRevocationBeforeWorkerRecheck` PASS(8.78s)——停用先提交则复检拒绝、解锁投影不可执行、Claim 拒绝。不依赖 sleep。
- **Action 同类边界**：62 为 `action.executions` 增加 `authority`（默认 {}＝legacy），创建命令时持久 native/legacy 来源与 user/version；`Execution.authorityScopeTx` 对 native 执行经持锁投影复检，`RecordPostCheck` 已接入；legacy 路径回归 `TestSP07CommandTransactionAndClaim` PASS(174.39s)。**native 执行的后检查真实用例尚未运行**。
- **首租户 CLI 真跑**：一次性库 native_cli_r21_99f4865d（1→62），以真实独立进程运行 `opsctl bootstrap first-tenant-native`（NOINHERIT migration-duty LOGIN、0600 私有文件）exit 0，重复执行 exit 1；库中 tenant active/account change_required/platform_admin active/两条审计。过程中发现并记录前滚前置：迁移 44 前必须 `GRANT CREATE ON DATABASE ... TO schema_owner`。
- 未完成：安装器 native 闭包与无 OIDC 冷启动/重启/前滚恢复演练；Job 签名 Context 颁发/校验、工具/结果持久化返回、取消的独立进程 Worker 运行；Action native 后检查运行用例；P2-04 剩余读取与映射并发、P2-02/03 与 P1 视觉、P3 SL-049、P4～P7、P8。任务库仍 schema57，61/62 未部署。

## P2-05 业务Job/Worker原生身份贯通 + 原生首租户 r20（2026-10-23；本段优先于下方历史记录）

**范围：P2-05 业务 Job/Worker 原生权威增量与原生 first-tenant CLI；未推进视觉/P3/P4～P8。**

- 业务 Job 现在对 operational 也持久 `authorizationSource`/`authorizationUserId`/`authorizationVersion`（native 会话取自已验证会话，legacy 显式记 legacy），并纳入 `AssistantTask.Valid` 与冻结摘要校验。
- Worker 无浏览器会话时按持久来源分派：native operational 走 `NativeOperatorScopeForJobTx`（`investigation.job_native_operator_scope` 返回活跃账号的 operator 授权行与账户版本，`graph.EffectiveNativeTx` 用与准入同一实现重建 scope 与 authorization revision），审计域走既有 `AuditScopeForJobTx`，只有历史空来源才走 legacy `graph.EffectiveTx`；native 永不回退 legacy。
- 新迁移 59：`identity.job_authority_active` 门（仅 schema_owner + api/worker 运行时职责 + 指定操作）与 `investigation.job_native_operator_scope` 只读投影；accounts/role_grants 的 RLS 既存分支逐条保留后追加该门，身份模式不对 Worker 开放。
- 真实 PG（1→59 临时库）：Worker（worker_runtime_role、无会话）Claim+GetAssistantInput 成功、租约到期后新实例接管成功；撤权（grant revoked+账户版本+1）与重授（新 grant 版本2）与停用均拒绝并置 `AUTHORIZATION_REVOKED`，取消亦拒绝；篡改 `authorizationUserId`/`effective_scope`、跨租户、收窄命名空间全部拒绝。
- 原生首租户：`identity.CreateFirstTenant`（迁移 60 仅授予 `migration_role` 对 `platform.tenants` 的 INSERT）在 NOINHERIT migration-duty 事务内原子创建租户+首管理员+凭据+platform_admin 授权+初始化标记+两条审计；新增 `opsctl bootstrap first-tenant-native --input --password-file`（无 OIDC）。真实 PG（1→60）：创建/登录查找/重复 ErrTenantExists/API 角色 ErrBootstrapDuty/并发仅一次成功均通过。
- 仍未验证/未做：安装器 native 闭包与无 OIDC 冷启动脚本、Action 执行边界（`internal/action/read.go:11`、`postcheck.go:58`、`postcheck_report.go:165`、`service.go:78` 仍用 `graph.EffectiveTx`）、真实模型/工具/集群来源的 Worker 执行、部署后复验；P2-04 查询结果/正式引用/审计域/映射并发、P2-02/03 与 P1 视觉、P3 SL-049、P4～P7、P8 未推进。任务库仍 schema57，59/60 仅源码与临时库。

## 映射历史读故障链贯通返修 r19（2026-10-23；本段优先于下方历史记录）

**范围：只返修 P2-04 的映射历史读权限贯通与验证缺口，未推进 P1/P2-02/P2-03/P2-05/P3～P8。**

- 缺陷：`FaultChainContext`/`authorizeTurnAnswer` 先用 `AssistantReadJobTxAs` 拿到保留旧 Subject 的 Job，随后嵌套 `ReadFaultChainContextTx` 走 `CheckAssistantJobTx → currentAssistant → graph.EffectiveTx(旧 Subject)`，在原生会话上下文直接拒绝，导致合法映射用户读含 `faultChainContextRef` 的答案、故障链、来源步骤失败。
- 修复（最小完整）：`internal/investigation/assistant_v3.go` 新增非导出的 `historicalReadGrant` 能力与唯一构造入口 `AuthorizeHistoricalReadTx`；`CheckAssistantJobTx` 命中能力时要求 tenant/job/owner 与该 Job 完全一致且 `Mapped`，再调用 `currentAssistantAs` 重新证明当前原生会话、版本、角色、集群/namespace/资源范围与策略版本。能力不写库、不改 `requesting_subject`、`effective_scope(_digest)`、`assistant_binding`、input/context digest，也不被 Worker/`Submit`/`Cancel`/`Archive`/`Rebind`/严格 `Turn`/`Get` 使用。`authorizeTurnAnswer` 与 `FaultChainContext`/`ResourceSourceStep` 使用该入口并把派生上下文传给嵌套读取。
- 真实 PG（任务 15453 临时库，1→58）验证：真实 `repository.Submit` 建出的持久 Turn/Job + committed `evaluate_fault_chain` Step + 经触发器写入的 answer/message/event，映射账号读故障链/Turn/Messages/Events 全部通过且冻结字段读取前后一致；未映射账号、异 legacy 主体、异租户、收窄 scope（新会话快照）、撤销授权均拒绝；严格 `Turn`/`Get`/`Submit` 拒绝。日志 [integration-mapped-history.log](evidence/gpt-historical-r19/integration-mapped-history.log)，结果 [result.json](evidence/gpt-historical-r19/result.json)。
- 仍未验证：映射历史会话在正式前端的视觉/交互与 SSE、查询结果/正式引用/审计域（tenant 级）会话、映射冲突并发；未部署新 API/Web 二进制（30467/30469 未改）。任务库仍 schema57，`00058` 仅源码。

## 历史业务归属读取 r18（2026-10-23；本段优先于下方历史记录）

**范围：只做 P2-04 的历史业务归属读取，不推进 P2-02/P2-03/P2-05、P1、P3～P8。**

- 助手历史读取现在支持“旧主体 → 原生账号”的显式已复核映射：新增迁移 `00058_native_historical_read.sql`（只读投影函数 `identity.read_mapped_legacy_subjects`，要求租户事务、活跃租户/账号、`verified_inventory` 依据并引用已导入复核清单；不授予映射表直接读取），`internal/identity/historical_read.go` 封装读取。
- 授权语义（`internal/assistant/historical.go`、`internal/investigation/assistant_v3.go`）：映射只决定“谁可以读”，不是角色；读取必须持当前原生会话，运维域要求当前 operator 授权仍覆盖冻结的集群/命名空间/资源范围，审计域要求当前 platform_admin。冻结 `owner_subject`、会话/Job ID、`GrantSnapshot`/摘要、输入与上下文摘要、策略版本一律保留并继续校验，`ScopeCoversMappedOwner` 只忽略跨身份系统不可比的 authorization revision。
- 读取面：会话列表、会话详情、消息分页、事件流、轮次、查询结果、来源步骤、正式引用、故障链。写入面（Submit/Cancel/Archive/Rebind、严格 `Get`/`Turn`）仍按 owner 精确匹配，映射账号不能向旧主体会话追加、归档或改写绑定。映射读取写一条 `assistant.history.mapped_owner.read` 审计（不含内容与凭据）。
- 验证（真实 PG17.11，任务 15453 的临时库，schema 1→58）：映射前列表为空且详情拒绝；映射后列表/详情返回旧主体会话且 owner/revision/cluster 未变；严格读取仍拒绝；撤销当前 operator 授权后再次拒绝；API LOGIN 直接读 `identity.subject_mappings` 失败；审计记录存在。日志 [integration-native-historical-read.log](evidence/gpt-historical-r18/integration-native-historical-read.log)，结果 [result.json](evidence/gpt-historical-r18/result.json)。本地 `go build ./...`、定向 `go vet`、assistant/investigation/identity/authz 单元测试与 `TestUIV12` 合同测试通过。
- 未验证：映射历史会话在正式前端的视觉/交互、消息-结果-事件全链真实 Job、审计域旧主体会话、映射冲突并发与其他故障注入；未部署新 API/Web 二进制（30467/30469 未改），未真实浏览器联调。**P2-04 仍 in_progress**，`00058` 仅源码，任务库仍 schema57，1～57 未改动。

## GPT远程恢复续作（2026-10-23，r13；本段优先于下方历史记录）

连接已恢复：mssc-direct仍为获准主机mssc；根盘74%/58G可用、/data 12%/1.6T可用。远程原仓库3339876e保持未改，本地feat/ui-v12-replication/9bd78d84及继承工作区保留。任务PostgreSQL17.11/15453已从54前滚至56，55/56按实际执行SQL摘要冻结；1～54未改，旧入口/镜像/凭据不覆盖。现有OpenBao TLS/Raft、Transit key与精确策略已核对，仅刷新任务JWT，真实TokenReview成功/错误SA403；无重新初始化或创建key。

r7～r11合并真实DB/HTTP集成已通过（seventh轮MFA用例467.85秒）；最终首次改密错误码修正另经真实HTTP和浏览器复验。新预览https://192.168.0.63:30467/login已启动，用户创建/首次改密/权限拒绝/退出有当前机制证据，尚无HTML像素通过。历史失败全部保留在evidence/gpt-resume-r13/。已修正：测试重放必须复用实际已消费TOTP；测试从真实会话读取userID而不解析主体前缀；首次改密按真实201合同；登录/改密审计用事务内持久主体；原生命名空间授权不要求整集群授权；管理员授权请求使用合同规定的空范围数组；密码重置审计只记录非秘密密码版本、不触碰通用秘密拦截。详细版本边界及最后浏览器脚本退出定位中断、接续完成记录见evidence/gpt-resume-r13/result.json；不把机制通过升级为整个切片通过。

r12独立安装器源码已补原生business-values/Profile/CA、Keycloak禁用与完整OCI校验后跳过缓存、原生环境引导所需信任与Secret、CLI来源核验用私有会话Cookie；定向单元/Helm/CLI通过。全bundle包保留OPA来源摘要不一致失败；首次租户CLI仍有OIDC依赖，完整冷启动未验，详见evidence/gpt-installer-r12/result.json。安装器未部署，不视为P2-05完成。

P2-02/03/04/05继续in_progress；历史业务归属读取、Job/Worker显式原生authority/user/version、并发最后管理员与管理审计故障注入、逐状态HTML像素比对、真实来源SL-049及后续模块仍未完成。原额度耗尽中断和后续断连的待办都保留于continuation-handoff.md。

## 本轮实施：SL-001收尾与共享授权层（2026-10-22，preview-r2）

**状态：P2-02 的 SL-001 六项已按实测记录（视觉通过，含登记文案差异）；P2-04 授权边界完成共享层落地并在真实浏览器验证；P2-03/P2-04 用户生命周期/P2-05/P3 未实现。**

- SL-001右侧改按`shared/platform.css`实测值重排（表单gap 18px、标签19.8px+6px、输入38px、标题27px/650、按钮43px/圆角6px），7个antd复位选择器加深到`v12.css`可赢的层级。同一远程Chrome内把冻结原型以`file://`打开逐元素测量，除刻意缩短的tag外全部元素y/x/w/h差值为0.00；截图差异21/1363300（0.00154%），只遮罩两处登记文案。证据：`evidence/preview-r2/{geometry.json,visual-diff-filled.json,product-login-filled.png}`。
- 登录状态实测：空字段校验不发请求、未知账号401常量文案、真实请求期间`aria-busy`加载态、登录后reload恢复、退出200+旧Cookie 401。新增“卸载即中止登录、迟到成功不采纳”Vitest用例。证据：`evidence/preview-r2/{login-state-*.png,auth-browser-result.json}`。
- 403根因修复落在共享层：新增`internal/authz`，原生会话只用`identity.authorize_native_role`（迁移47，48修正FOUND语义）按会话授权版本`FOR SHARE`复检，历史会话只用`platform.role_bindings`，禁止跨来源回退；迁移source/tenant/configregistry/action/audit/assistant七个调用点。迁移47误删role_grants策略的`find_session`分支，导致会话`grants: []`仍403，由迁移49前滚修复。真实浏览器`GET /api/v1/admin/clusters` 200且`platform.role_bindings`仍0行；纯管理员operator接口仍403。证据：`evidence/preview-r2/result.json`。
- 新增preview-r2（30463，独立API单元与容器，复用同一任务库/schema49），r1保留不改。运行说明见[preview-runtime.md](preview-runtime.md)。

未完成：P2-03 MFA/再次验证/恢复码；P2-04用户创建/改状态/重置/会话撤销/角色授予/历史主体映射API（因此无独立测试身份，首次改密现场复验、operator范围、MFA现场流程、入口429均待其解锁）；P2-05全栈切换；P3-01～04与SL-049。下面各段为历史轮次记录，环境与版本只对各自轮次有效。

## GPT直接续作：独立原生预览（2026-10-22）

已在mssc-direct启动正式API/Web独立入口 https://192.168.0.63:30462/login 。修复Foundation未接native路由导致的真实登录404；集成测试改走正式装配并通过真实PG。Chrome验证首次改密201、刷新恢复、退出200/旧Cookie401、新密码再登录及API进程重启后会话持久化。SL-001修为原型双栏并与既有1440×960参考比较，visual仍failed；没有重采参考图或继承全量通过。来源服务仍依赖旧授权表事务复检，原生管理员集群列表403，待P2-04/05处理，未插入旧授权绕过。P2-02继续in_progress。入口、密码私有读取、服务恢复及限制见[运行说明](preview-runtime.md)，证据见[evidence/preview-r1/result.json](evidence/preview-r1/result.json)。下面“未部署页面/浏览器未验”等旧段落只代表各自历史轮次。

更新：2026-10-22（Asia/Shanghai）。**项目未完成；G0/P2-01已验，P2-02真实DB及HTTP处理链验证已通过，正式浏览器/视觉/live未验；P1/P2-02仍in_progress，产品验收未通过。** 本文件记录真实实施，不继承原型或方案验证的通过结论。

## 历史交付记录（环境与版本仅适用于各自证据轮次）

- 分支 `feat/ui-v12-replication`，开始 HEAD `9bd78d84e212217626cdf0cfe54af84a155a9a3d`，初始工作区干净，origin/main 与本地一致。本次代码尚未提交/推送，没有创建 PR。基准见 [baseline-code.json](evidence/baseline-code.json)。保留原始五个清单/基准文件在 original-plan/，方案正文可从此 HEAD 恢复。
- P0-01 完成代码、设计与实际运行版本的分离记录。远程仓库在 `3339876e`，运行实例使用独立镜像 digest；现存统一开发数据库为 PostgreSQL 17.11、当前迁移 43。只读依据见 [运行记录](evidence/remote-environment.txt)、[当前数据库元数据](evidence/shared-db-readonly-current.txt)。没有把现存实例更新为本分支。
- P0-03 的四个版本化 Schema 与 r4 合同测试已完成：55 正例/87 反例通过，C-017 密码重置已定义；9 项新增操作同步为 C-168～C-176。
- P1-01 已落地紫色设计令牌、238px 侧栏/70px 顶栏、页面头/卡片/状态/表格/表单字段/抽屉/确认与嵌套浮层层；portal 使用独立 `v12-layer-root` 皮肤，context API 稳定，焦点恢复/顶层关闭/长内容底部可达有定向测试；已接入 AppShell、PageHeading、Status、Findings/Resources。
- P1-02 已按固定源 NAV 恢复 7 个可折叠模块（知识管理含 cases/runbooks），manifest 22 路由通过 `/knowledge`→cases 映射，不硬凑链接数；导航保留运行/配置 clusterUid，URL/history 与授权撤销走统一 generation/cancel/草稿确认，切换清空 canonicalId/conversation/incident/cursor 等旧对象；配置集群从管理员 `getClusters` 独立读取，来源按注册集群全量分页过滤，策略/执行/部署明确租户级；纯 admin 不显示 operator 菜单，operator 保留 `/capabilities` 路由。
- P0-04 的任务专属 Fixture 已在远程 r2 自测 46/46 通过，历史读取失败修复；证据为 [r2 JSON](evidence/ui-v12-fixture-run-r2.json) 与 [r2 日志](evidence/fixture-run-r2.log)，r1 失败证据保留。
- P0-02 初审后已按“开窗/预取/提交/确认”重做 458 状态的绑定：157 个动作按阶段绑定，`confirm-yes` 改为 10 条逐片 mutation，54 个可见表单（含 8 个只在 snapshot DOM 出现的表单）都有提交绑定，并补 544 个 form 外控件（cluster-picker、graph-root/depth、rca-version、session-search、riskAcknowledged、stepup 等），见 [act-bindings.csv](evidence/act-bindings.csv)、[form-bindings.csv](evidence/form-bindings.csv)、[confirm-bindings.csv](evidence/confirm-bindings.csv)、[form-inventory.csv](evidence/form-inventory.csv)、[state-bindings.csv](evidence/state-bindings.csv)。P0-02 已在 G0 范围关闭（R7 独立审核签核）。
- 原方案 138 项方法/路径基准保留在 original-plan/。逐动作核实补录 28 项 OpenAPI 已存在但原清单遗漏的操作（C-140～C-167），并保留 C-139 为执行配置版本退役，不替换成通用 registry。当前清单 177 项：91 项已有、86 项拟新增。[合同审查](evidence/contract-audit.csv)、[表单摘要](evidence/form-contracts.csv)。
- 正式请求层已修复身份/授权切换后的迟到成功响应：退出、换账号、撤权时中断旧请求及 SSE，丢弃缓冲的旧事件和旧成功结果。同一凭据刷新保留有效请求及 tenant，调用方取消继续有效。复用既有 client/SSE，没有新增依赖或更改后端合同。改动触及所有 requestOptions/checked 调用方和三个流式入口，已做相应前端回归。
- 远程 Linux Chrome 156 在 1440×960、DPR=1、zh-CN、Asia/Shanghai 下，从固定原型生成 **458 张 viewport 参考图、736 张总截图**。额外保存长页和独立内部滚动底部，没有遮罩。Noto Sans/Noto Sans CJK SC 字体回退已记录。长页补图可能不含滚动条沟槽，主参考强制校验 1440×960；没有缩放缩略图。[采集结果](evidence/reference-capture.json)、[三轮差异及修复](evidence/reference-rounds.json)。抽查 SL-049 加载态及 SL-106 嵌套弹窗；这不是正式产品像素比较。

## P0-02 核实结论（初审后重新打开）

核实方式：逐条读取 458 份 `spec.json`/`SPEC.md`/`recipe.js`/`replay.html`，并对每份 `index.html` 的实际 DOM 提取当前可见表单、字段和 `form=` 提交按钮（排除隐藏 login 底板）；动作阶段从原型分派实际调用的函数读取，再沿平台实际路由、处理器、服务、SQL/Worker 与 Web 调用方核对。所有结论来自源码位置，不以“路径在 OpenAPI 中存在”或“文件存在”签核，也不把 recipe 候选复用当作完整逐动作签核。

- 458 个状态共 3888 个绑定面：2909 个动作实例 + 435 次表单提交实例 + 544 个 form 外控件实例（54 种可见表单、371 条逐片表单记录、8 类非表单控件；recipe 只覆盖 46 种表单）。
- 阶段裁决：`open=533`、`prefetch=639`、`submit=571`、`confirm=46`、`local=2099`。
- 动作面裁决：`local=2616`、`direct=255`、`adapt=693`、`missing=324`。
- 157 个按阶段绑定的动作：`open=59`、`prefetch=32`、`submit=11`、`confirm=12`、`local=43`；`confirm-yes` 不设通用行，改由 10 条逐片确认绑定（4 条 direct、6 条 missing）。
- 54 个表单：`local=1`、`direct=3`、`adapt=19`、`missing=31`；其中 8 种只在 snapshot DOM 出现，不在 recipe 提交步骤中。
- 458 个状态的主读取：`direct=355`、`adapt=26`、`missing=77`；带对话框表单的切片按上下文读取覆盖 route 推断：SL-106 主读取父 Finding C-030、关联候选 Incident C-031，关联写入 C-039；SL-001/002 为未认证 C-009，SL-009 为图读取 C-004。
- 跨模块/叠层复合状态 62 个：31 个由实际 active dialog/drawer 叠层判定（SL-106 modal+drawer 等），31 个由跨模块入口判定（资源抽屉、告警抽屉、AI 入口、服务列表等）。逐片记录在 `state-bindings.csv` 的 `composite`、`active_overlays` 与 `surfaces`，不按 `spec.depth` 臆断。

阶段口径（初审修正）：

- `open`：只打开对话框/抽屉/确认框，不产生平台请求；提交绑定在对应 `form:` 或 `confirm-bindings.csv`。
- `prefetch`：打开或切换时读取列表/详情/候选数据；列出具体 GET，不以 route 代替。
- `submit`：真正发出 POST/PATCH/PUT/DELETE 的提交动作；命令创建只在 `confirm-yes`，不在 `command-review`。
- `confirm`：只打开确认框，复用已有 assessment/字段/revision；具体 mutation 逐片绑定。
- `local`：客户端状态、导航、剪贴板、对已授权已加载数据的导出；不因此放宽权限。

裁决口径：

- `direct`：现有请求的方法、字段、状态和错误语义已覆盖，只差正式前端接线。
- `adapt`：现有请求存在，但字段、状态、分页、投影或权限语义需要映射或扩展。
- `missing`：平台没有对应公开合同或处理器，需按主任务新增并持久化，不允许用前端演示数据或定时器替代。

初审修正的具体点：

1. `ai-resource`、`ai-incident`、`observe-ai`、`session-create` 只调用 `newSessionDialog` 打开 `session-form`，不在这里 POST 创建会话；创建在 `session-form` 提交，首条消息在 `composer` 提交。
2. `binding-create` 只打开 `binding-form`；`cluster-create` 只打开 `cluster-wizard-form`；`command-create` 只打开 `command-form`；`incident-state` 只打开 `transition-form`。POST 均移到对应表单提交行。
3. `command-review` 只打开确认框，复用已有 assessment/ack/step-up，不再执行 risk-assess→ack→create 三连；命令创建在 SL-071 的 `confirm-yes`，绑定 C-057、request digest、profile 版本与目标。
4. `confirm-yes` 不再笼统记 local：SL-071/297/306/430 分别绑定执行创建、会话归档、执行取消、调查停止；SL-165/166/176/348/371/426 绑定知识发布/归档、模型停用、规则停用、会话撤销、巡检取消，均为 missing 并写清主任务。
5. DOM 表单补充：除 recipe 的 46 种外，补 `v11-user-filter`、`model-filter`、`evidence-filter`、`session-rename-form`、`binding-filter`、`v11-monitor-form`、`knowledge-form`、`source-filter` 8 种。SL-106 的 `v11-alert-link-form` 由 `v11-alert-link` 从 `alertDrawer` 打开，字段 incident/reason/review，写 C-039（missing），候选读取 C-031；其 surfaces 现含 `form:v11-alert-link-form`，gap 明示 C-039 missing。
6. 第二轮阶段复核：`session-rename` 只开 `session-rename-form`；`session-archive` 只开确认框，归档在 SL-297 的 `confirm-yes`；`native-change-password` 只开 `native-password-form`；`session-scope` 只开只读范围/模型抽屉，不提交 rebind；`observe-log-bucket` 只过滤已加载样例，`observe-device` 只改选择并回 idle；`query-source` 只展示已有查询检查，不重新 POST evidence 查询。
7. `policy-validate` 不再映射发布：本地结构预检保留，服务端 validation-only 合同为 C-177（拟新增，`POST /api/v1/admin/policy-bundles:validate`），不签名、不写版本、不激活；真实发布仍由 `policy-publish-form` 的 C-156 执行。
8. R5 登录修正：SL-001/002 主读取改为 `POST /api/v1/auth/login`（C-009 missing），只含可见 `login-form`（username/password→loginName/password），不读 overview；SL-005 首次改密主读取 C-011。
9. R5 拓扑/非表单修正：SL-009 主读取改为 C-004 neighbors，新增 `graph-root`/`graph-depth` change 绑定；全部 458 片补 544 个 form 外控件（cluster-picker/config-cluster/session-search/riskAcknowledged/stepup 等），按可见 DOM 与 change/input 语义绑定，cluster-picker 仅本地切范围后重读。
10. R5 复合读取：SL-106 保留父 Finding C-030、候选 Incident C-031 与写 C-039，不压成单条 primary。
11. R6 复用改密表单：native-password-form 七片按 self/first/reset 绑定；self/first→C-011 passwordChangeRequest，SL-005 first 缺 currentPassword 登记补充状态 P2-02；SL-094/370 reset→C-017 adminPasswordResetRequest（userId 路径、initialPassword、双 revision），confirmPassword/mustChange 不进 body。
12. R6 表单请求：model-form 区分 create replace/update keep；v11-user-form create C-015 与 update C-016 分列，update 不发送密码；binding-form 改为 C-020 的 userId/scope/expectedAuthorizationVersion/reason，旧 subject handler 仅作待适配复用。


## 重要发现

1. **证据查询不是日志工作台合同。** `POST /api/v1/evidence:query` 是同步、单次、有界查询：接收 `resourceCanonicalId`、`type`、`queryTemplate`、绝对 `timeRange{from,to}`、`budget`、`sourceRegistrationId`、`namespace`、`limit`；没有查询 ID、异步任务、取消、个人历史、服务端时间分桶、关键词或级别过滤。结果只给 evidence 列表与 `partial/freshness/degradedSources/warnings/queryHash`，不回显生效来源 revision、资格、覆盖和窗口。因此 SL-049 的提交、历史、取消、分桶、级别筛选分别为 `adapt/missing`，不能声称日志工作台合同已具备。
2. **告警状态时间线没有投影。** `GET /findings/{id}` 返回 finding 记录（含 `sourceRevision` 与发生/恢复时间），但 `finding.timeline` 未暴露；告警详情“状态时间线”仍是 `adapt`（P4-03）。
3. **执行链路缺全局列表和 operator 配置投影。** `/command-executions` 只有 POST 与单条 GET；全局列表仍为 `missing`（P6-01）。执行配置列表只允许 `platform_admin`，普通 operator 无法选择 `executionProfileId/version`，命令创建对话框必须补受控能力投影（P6-01）。
4. **调查创建字段远少于原型。** 现有 POST 只接收 `triggerRevision` 与 `triggerKind=manual`，预算/工具目录由服务端策略决定；原型的步骤、超时、调查重点和模型选择不能提交（P4-05）。
5. **AI 会话没有改名，范围重绑不含模型。** 归档是独立 POST，重命名仍为 `missing`（P5-02）；`binding` 只接收 namespace/资源/窗口，模型由平台准入 Profile 固定（P5-03）。停止、断流与固定引用已有合同。
6. **策略只能“发布即校验”。** 没有只校验不发布的合同；`policy-validate` 为 `adapt`，发布路径会真实编译和签名（P7-06）。执行配置表单缺少服务端必填的 `schemaVersion/version/clusterUid/tools/principal`，`label` 与 `name` 语义不同（P7-06）。
7. **接入与组件能力仍为空。** 来源登记/更新/轮换存在，但没有连接测试；组件只读检查、缺口方案、受控部署、部署历史均无合同（P7-01/P7-02）。核对已有组件不自动授予凭据或数据资格。
8. **知识、模型、通知、巡检与用户管理没有公开合同。** 分别归 P7-04/P7-05、P5-03、P7-03、P6-03/P6-04、P2-04；这些状态的读取和写入均不能按现有 API 接线。
9. **身份关联的实际边界。** 资源规范标识已嵌入来源稳定 UID；`namespace UID` 未建模，不能作为查询键。`sourceRevision` 是每条 evidence 的字段，不是查询入参或响应回显。查询窗口是绝对半开 `from/to`，没有时区回显。资源/证据读取使用 tenant 校验、operator 角色绑定、namespace/资源授权、scope digest 与 graph owner epoch/revision 复核；平台管理员不自动获得运维数据权限。
10. **合同清单原先少记 28 项现有操作。** 例如会话详情、消息分页、turn、来源步骤、故障链上下文/调查、资源解释/解析、行动计划、法律保留、策略发布、来源回滚、RCA 当前/历史、事件合并/拆分等。它们已补为 C-140～C-167，避免把已有能力误标为拟新增。

## 验证结果与适用范围

- P1 R3 修复后：`pnpm test`：6 项 Node SSE 合同 + 28 个 Vitest 文件、113 项通过（含真实 AppShell+Assistant 路由阻断、成功包络正对照、create body 和 SSE abort），[日志](evidence/web-tests-p1-r4.log)。jsdom 输出已有 canvas getContext 未实现提示，不能据此声称真实图形渲染已验收。
- P1 R3 修复后：`pnpm build`：TypeScript 与 Vite 构建通过，[日志](evidence/web-build-p1-r4.log)。已有大 chunk 提示未在本任务做无关拆包。
- 修复前独立审核保留：88 项 Vitest 通过记录见 [web-tests.log](evidence/web-tests.log)、[web-build.log](evidence/web-build.log)，复审意见见 [codex-p1-review-r1.json](evidence/codex-p1-review-r1.json)。
- 参考图采集：r1 的 DOM/浮层顺序断言错误已修复；r2 全部采集成功，但全页图尺寸不应替代固定 viewport；r3 全部通过 viewport 像素尺寸自检。三轮原始输出都保留，不将错误抹成首次成功。
- `python3 docs/plans/ui-v12-replication-20261021/validate-progress.py`：核对 458 状态、157 个阶段动作、10 条逐片确认、54 种表单（含 8 种 DOM-only）、62 个复合状态、177 合同引用与六项未验证状态，[结果](evidence/progress-validation.json)。`g0_passed=true`（G0 准备阶段独立签核）；产品六项仍 not_verified，属于后续切片验收。
- `go test ./test/contract -run 'TestUIV12' -count=1`：4 个 Schema、55 正例/87 反例通过；`go test ./test/integration -run '^TestUIV12FixtureDSNGuard$'`：通过。
- `GOOS=linux GOARCH=amd64 go test -c ./test/integration`：生成 Linux 二进制；远程 r2 自测 46/46 通过（见上）。
- `validate-plan.py` 与 `validation.json` 已恢复为开始时 HEAD 内容，本轮不运行、不改造；它们只用于实施前基准，旧证据保持 `checks_passed=4904`。当前扩展校验全部由 `validate-progress.py` 及其日志承担。
- `git -c core.whitespace=cr-at-eol diff --check` 通过；CSV 保留原 UTF-8 BOM/CRLF；validate-progress.py 还会重新解析 458 份 index.html，断言 form-inventory.csv 未漂移。源码版本及工作树文件摘要见 [tested-source.json](evidence/tested-source.json)。

没有执行业务数据库迁移、原生账号切换、真实模型/查询/执行/巡检/通知联调或产品视觉比较。上述单元、构建、原型参考采集与静态绑定均不能证明这些业务通过。

## 当前未完成与阻断范围

完整 39 项状态见 [task-progress.csv](task-progress.csv)，具体裁决见 [p0-decisions.md](p0-decisions.md)。P0-01～P0-04 均已按 [G0 独立批准](evidence/g0-independent-approval.json) 完成准备阶段；P1-01/P1-02 已开始实施，P1-03 仍受 P2 原生身份依赖，P1-04 保留参考图与远程视觉待办；P2 未启动。

1. **P0-02 已关闭（G0 范围）**：458 状态、阶段/confirm/54 种可见表单/544 个 form 外控件/62 复合状态已完成，R7 独立审核通过；仍不等于接口运行、权限或持久化验收。
2. **P0-03 已关闭（G0 范围）**：四个版本化 Schema 与 r4 测试实测 **55 正例/87 反例**通过；R6 已按实际 Schema 修正 C-051/052、C-170、C-173、C-175 和登录/模型/用户/改密/角色表单投影，R7 独立审核通过。C-017 密码重置已定义，9 项新增操作同步为 C-168～C-176。Schema 未进入 OpenAPI/客户端/实现。
3. **P0-04**：任务专属数据库 Fixture 已在远程 r2 自测通过 46/46 检查，历史读取失败已修复；2 集群/3 来源/6 实体/3 绑定/2 会话/9 审计、第二次 seed 不重复。证据见 [p0-environment.md](p0-environment.md)、[r2 JSON](evidence/ui-v12-fixture-run-r2.json)、[r2 日志](evidence/fixture-run-r2.log)。原生用户/登录/主体映射仍属 P2；G0 不因此关闭。
4. **G0 准备阶段已通过，产品验收未通过**：[G0 独立批准](evidence/g0-independent-approval.json) 绑定 2026-10-21 工作树，确认映射/合同/环境准备；它只允许进入 P1/P2，不表示产品六项通过。458 状态六项仍全部 `not_verified`。
5. **P1-01/P1-02 实施中**：已落地紫色设计令牌/公共控件、固定源折叠导航、规范路由与旧链接映射、运行/配置集群分离与切换清理；正式页面视觉对比、P1-04 状态定位及 P2 原生身份仍未完成。
6. 远程当前事实（2026-10-21 r2）：根分区 74%、约 59G 可用；`/data` 约 1.6T、11% 使用。任务环境为 `/data/ops-ui-v12-20261021-r2`（独立 PG 17.11、迁移 43、回环 15449）；`/data/ops-ui-v12-20261021-r1` 仅保留历史参考图/浏览器材料。不删除旧目录、镜像或变更 Docker/Kubernetes 共享配置。

下一步继续 P1-01/P1-02 的页面接入与远程视觉核对，按依赖推进 P1-03/P2；SL-049 日志链路依赖 P3 新查询合同。未恢复虚拟化、完整 Trace 或生产发布，也未改根目录 00～10 正式文档。

## 运行与制品位置

本地只编辑/编译/单元/合同验证；业务浏览器、数据库、集群和模型运行仍只在获准开发机。`ssh mssc-direct` 已验证指向同时具有 10.10.10.2 和 192.168.0.63 的同一主机。

参考 PNG 保存在本地忽略目录 `platform/artifacts/test-reports/ui-v12-reference-r3/`，可直接查看；其完整清单在 evidence/reference-capture.json。远程固定参考包在 `/data/ops-ui-v12-20261021-r1/ops-platform-v1.2-20261020/`。本任务的 HTTP/Chrome 进程和 SSH 转发已停止，[清理记录](evidence/reference-cleanup.txt)，共享服务未改变。

重采参考图需在远程重启专属回环 HTTP/Chrome、转发 CDP，并使用新的输出目录（不覆盖旧轮次）。浏览器 profile 和日志仍放在 /data；HTTP 仅绑定 127.0.0.1:18775，CDP 仅绑定 127.0.0.1:18776。驱动命令在本地 web/ 执行：

```sh
node tests/v12/capture-reference.mjs \
  --cdp http://127.0.0.1:18776 \
  --base http://127.0.0.1:18775/ \
  --design ../../ui-handoff/ops-platform-v1.2-20261020 \
  --output ../artifacts/test-reports/ui-v12-reference-r4
```

驱动强制核对固定源 SHA、远程 Linux、DPR、viewport 和每张主图的 PNG 像素尺寸。它不运行产品、不宣告六项验收通过。仅查看已有截图无需重启服务。该批尚无数据库变更或原生账号写入，不涉及身份回滚；以后产生原生写入后须遵循方案的兼容版本/前滚边界。

## Codex 回调独立复核 r5

合同包与 Fixture guard 独立测试通过；同源码独立交叉编译的 Linux 二进制在 mssc-direct 任务专属库复验 46/46 通过，保留 r1 失败及 r2 自测。P0-04 数据准备结论可保留。P0-02 的登录、拓扑/非 form 控件、SL-106 复合读取已按 R5 修正并由 R7 签核；P0-03 的合同 CSV/表单字段已按已测 Schema 同步。详见 evidence/codex-callback-review-r5.json 与 r7；G0 准备阶段已批准，产品六项未验证属于后续产品验收。上文旧环境与完成描述已按 2026-10-21 r2 事实同步；历史 r1 目录仅作参考材料保留。

## Codex 回调复核 r6

登录/拓扑/form外控件和指定Schema字段增量修复已核实，合同测试及结构检查通过。复用native-password-form的self/first/reset仍共用C-011，管理员reset应为C-017；模型/用户submit_request与原生授权表单仍混用旧字段，P0-02/03保持打开。精确清单见evidence/codex-callback-review-r6.json。Fixture未变，不重复远程运行；G0未签核，产品六项未验证。

## 当前结论：Codex r7 G0 独立签核

2026-10-21：P0-02/03 本轮增量返修已独立复核，P0-01/04 既有准备证据保留；G0 按实施方案的映射、依赖、已有/拟新增分类、版本合同、账号回退与补充安全状态条件通过。证据为 evidence/codex-callback-review-r7.json、evidence/g0-independent-approval.json，签核关联未提交工作树的具体清单摘要。上文逐轮“未通过/重开”是保留的历史结论，以本节和 task-progress.csv 为当前状态。

继续按 tasks.csv 进入 P1-01 公共设计令牌/控件，再依赖推进 P1-02 导航与集群上下文；P2 原生认证及 SL-049 全栈样板仍未实现。本签核不代表产品通过：458 状态六项仍为 not_verified；原生身份/CSRF/MFA/历史映射、实际来源资格、模型运行与产品视觉比较均留后续真实验证。参考图与独立 Fixture r3 46/46 不重做。

## Codex P1 独立审核 r1

实际公共控件与shell已接入，独立pnpm test通过6合同+92 Vitest，TypeScript/Vite构建通过。仍有运行集群导航/URL/助手流失效、配置集群真实范围、导航层级/角色、portal样式和产品不可用文案问题；详见evidence/codex-p1-review-r1.json。P1-01/P1-02保持in_progress，产品六项未验证；G0准备签核保留。

P1 R1 返修结果（2026-10-21）：运行/配置集群切换已走统一 generation/cancel/草稿确认并清空对象参数，导航保留 clusterUid，助手 scopeKey/SSE 与异步 mutation 按作用域中止；配置集群改为管理员 getClusters 全分页候选，来源按注册集群全分页过滤，策略/执行/部署明确租户级；导航恢复固定源折叠层级与角色隔离，portal 皮肤/焦点/长内容与不可用产品文案已修。完整 `pnpm test` 25 文件/100 项通过，构建通过；P1-01/P1-02 仍 in_progress，视觉/live 未验证。

P1 R2 返修结果（2026-10-21）：助手首次/同 scope incident/canonicalId 深链保持排障模式，不再被 mount 重置；URL/history 与手动 selector 分离，带新对象的合法跳转保留完整位置，dirty 取消恢复原提交位置与草稿，确认才失效旧范围；Assistant create/send/resolve 在异步结果后先校验 scope/signal/unmount 再本地提交，卸载也会中止 controller。初始 unknown UID、撤销 A 且仍授权 B 时最终无静默回退。真实 AssistantPage/URL 边界新增 8 项回归，完整 `pnpm test` 26 文件/108 项通过，构建通过。

P1 R3 返修结果（2026-10-21）：Assistant 集成测试改用生成合同成功包络并增加正对照；跨 scope/unmount 迟到 create 不提交；AppShell 用 React Router `useBlocker` 在提交前阻断 dirty 跨集群导航，真实 AssistantPage 保持 pathname/search/hash/对象/草稿，确认才切换并保留新 hash；未授权 URL 在子请求前门控；同 scope 新 incident/canonicalId 深链按实际 create body 验证 mode/domain/cluster/object；真实 hook 受控 stream 验证旧 SSE abort。完整 `pnpm test` 28 文件/113 项通过，构建通过。

## Codex P1 独立审核 r2

独立全web复验6合同+100 Vitest与构建通过；导航层级、角色路由、配置候选/来源分页及portal修正认可。助手初始化mode清理、URL绑定对象/取消回滚与异步局部提交仍需修正，详见evidence/codex-p1-review-r2.json。P1仍in_progress，远程视觉/live未验证。不核验CodeBuddy模型设置。

## Codex P1 独立审核 r3

20项定向用例与构建通过；真实成功包络/SSE未覆盖，AppShell切换会卸载草稿页、完整位置回滚未包括pathname/hash，同scope新排障深链仍需核对。详见evidence/codex-p1-review-r3.json。保留失败与既有通过记录，P1仍in_progress，不将测试通过扩大为功能验收。

## Codex P1 独立审核 r4：增量功能修正接收

21项真实Shell/Assistant/Layer定向检查与正式构建通过；useBlocker阻断、有效成功包络与正对照、旧SSE信号中止和同scope排障深链修正已核实。P1-01/02保持in_progress，正式视觉/live验收未完成，P1-04仍待状态定位/比较。

## Codex P2-01 独立审核 r1

身份与CLI定向单元通过；静态审核发现外键顺序、rolsuper拼写、NOINHERIT职责角色/USAGE、登录前FORCE RLS上下文、跨tenant关联FK及DB测试防误连问题，见`evidence/codex-p2-review-r1.json`。OPA source-lock失败归因保持unresolved，未更改无关锁。

## Codex P2-01 独立审核 r2

初始化顺序/职责校验修正认可；指出复合FK仍为单列引用、preauth查找别名错误，见`evidence/codex-p2-review-r2.json`。随后只允许新建任务专属PG实例核验。

## P2-01 代码交付（2026-10-21）

新增前滚迁移44、`internal/identity`、`opsctl bootstrap native-administrator`与默认跳过的数据库边界测试；早期交付未运行DB，未切OIDC，未碰共享库。

## P2-01 静态审核返修（2026-10-21）

按r1/r2修正：tenant advisory lock后按accounts → credentials → platform_admin grant → initialization marker → audit顺序写入；CLI/事务使用真实NOINHERIT migration LOGIN并`SET LOCAL ROLE migration_role`核验；pre-auth函数用重置后的精确GUC保留FORCE RLS；六个account子表使用复合`(tenant_id,user_id)` FK；DB测试增加任务专属guard、NOINHERIT LOGIN、跨租户FK/RLS、GUC spoof、API/Worker拒绝和持久审计检查。OPA source-lock失败归因保持unresolved。

## P2-01 实际隔离 DB 验证（2026-10-21）

## Codex P2-01 独立验收 r3（2026-10-22）

Codex在全新15451/PG17.11实例从空库迁移1..44，真实NativeIdentity测试通过，独立CLI首运行成功/重复拒绝；证据见`evidence/codex-p2-review-r3.json`与`evidence/codex-native-r2/`。验收范围仅P2-01数据/初始化，不代表P2-02或产品验收。


## P2-02 代码交付（2026-10-22）

新增前滚迁移45（未执行）、native session repository/auth middleware/HTTP handlers、C-007/C-009/C-010/C-011 OpenAPI与生成客户端，以及正式Web登录/首改密/退出接入。native模式要求固定部署租户、server session cookie、CSRF/Origin、DB版本/状态复核和known/unknown登录围栏；first_password_only没有业务grants且只能读session/改密/退出。OIDC兼容模式保留，workload mTLS链不变。本轮只完成Go单元/合同、Web TestingLibrary、TypeScript/build与Linux交叉编译；未运行迁移45、HTTP server或真实登录。详见[p2-native-auth.md](p2-native-auth.md)。P2-02保持in_progress，产品六项仍not_verified。

## P2-02 集中返修 r1（2026-10-22）

## P2-02 集中返修 r2（2026-10-22）

按 `codex-p2-http-review-r2.json` 处理 B1-B9：安装登录入口默认值并连接真实改密输入；cookie 派生 CSRF 写入 middleware；主动登录清理不再触发 expired/generation 自失效；cluster resolver 走 tenant RLS 事务且 operator/admin 范围分离，Web 使用服务端 registered internal ID；SQL known-failure 限定列并用 account FOR UPDATE 串行化，改密增加入口准入；session 创建/改密的事务内完成投影与 administrator_state，`find_active_session` 校验 issued session version；兼容既有 SP08 OIDC runtime-config，`/login` 门控并支持安全深链/角色 home；administrator_state 返回真实计数并补 REVOKE PUBLIC。新增真实 AuthProvider/Login/generated fetch 测试、B4 迟到测试、operator/admin scope 与投影回滚集成测试代码。本地 Go/合同、31 个 Web 文件/129 项、构建、Linux 编译通过；迁移45/HTTP/真实登录仍未执行。P2-02 继续 in_progress。


按 `codex-p2-http-review-r1.json` 修正：cookie 改为先写 Set-Cookie 再写 body；GET session 稳定派生并恢复 CSRF，改密/退出统一请求头；迁移45改用单行入口围栏、精确 RLS 操作、revoke/change `RETURN NEXT`、`issued_session_version`、绝对到期边界封顶、旧 session 事务内复检、SQL 内 known 失败阈值和原子审计；operator 集群 UID 经 DB resolver 转真实 ID，先聚合独立角色 scope，再处理 namespace（单 cluster 多 namespace 全保留，跨 cluster 歧义 fail-closed）；AuthProvider 配置门控、native expired/forbidden、logout 前置 generation、迟到 refresh 拒绝、同 session refresh 不误清缓存、first_password_only 空 grants；request context 保留取消与值。新增真实 AuthProvider/生成请求测试和真实 HTTP/DB 链集成测试代码。本地 Go/合同、30 个 Web 文件/123 项、构建、Linux 交叉编译通过；未执行迁移45/HTTP/真实登录。P2-02 继续 in_progress。

## Codex P2-02 独立审核 r1（2026-10-22）

Go四包、Web三文件18项与正式构建独立通过；实际代码审核发现Cookie写入顺序、恢复CSRF/改密请求头、迁移45的RLS/结果行/版本/TTL、事务审计/旧session复检、并发限流、授权投影和身份生命周期问题，见evidence/codex-p2-http-review-r1.json。迁移45尚未执行，不得切换运行或判定P2-02通过；按新任务264ba468-737c-4111-b2ff-93b686aff578集中返修。P2-01已验数据/初始化保持原范围；产品六项仍未验证。原Stop通知未触发，已使用bridge人工恢复一次并成功notified；不核验CodeBuddy模型。

## Codex P2-02 独立审核 r2（2026-10-22）

Go五包（含合同）、6项SSE合同+30文件123项Web及构建独立通过，迁移44未变。Cookie顺序/SQL结果行/部分RLS、事务审计、context/Origin和first-only修正认可；仍有入口默认值、改密参数、恢复CSRF、登录generation自失效、注册集群RLS/角色范围、SQL限流并发、提交后投影、旧OIDC配置及管理员事实问题。见evidence/codex-p2-http-review-r2.json，集中返修任务766b08fa-17fd-45ca-af7a-564aa3f83fd9。生成客户端仍被mock且未覆盖真实nativeLogin，默认skip的DB测试不代表运行通过。P2-02保持in_progress；未运行45/HTTP/浏览器，产品六项仍未验。本轮自动回调正常。

## GPT直接修正与真实数据库验证（2026-10-22）

按用户最新要求，本轮由本对话GPT直接修改，未发送新CodeBuddy任务。修正766b08fa回报中仍漏接的返回列/sessionVersion/限流边界，并通过真实复跑发现及修复窗口过期锁、清理失败计数RLS与吞错；45首次执行后冻结，新增前滚46。新实例`/data/ops-ui-v12-http-20261022-r1`、回环15452、PG17.11从空库迁移1..45后前滚46。最终NativeHTTPSessionFlow（含角色/租户范围、审计失败回滚、绝对到期子测试）及NativeIdentityRepositoryAndRoles通过。Web退出立即取消旧请求，晚到退出不清除新身份；登录未知/锁定同401，同源/CSRF/请求ID边界已复验。131项Vitest+6SSE、Go单元/合同和构建通过。失败和版本绑定见[evidence/codex-p2-direct-r1.json](evidence/codex-p2-direct-r1.json)。

验收范围仅真实DB+HTTP处理链与本地Web机制；未启动常驻native API/Web、未进行正式浏览器/HTML像素/live验收，未部署新的可登录V1.2页面。P2-02保持in_progress，全部切片六项状态保持未验证。旧30451/15449/15450/15451及共享环境未改；44与既有验收源码一致。

## Codex 集中返修 r3：R1～R4 关闭与 P2-03 MFA/再次验证（2026-10-22，preview-r3 独立实例）

**状态：R1（会话绑定授权）、R2（审计授权来源分派与摘要）、R3（退出撤销未确认可见且可受控重试）、R4（浏览器关键结果断言与分母纠正）已实现并在真实 PostgreSQL/真实浏览器验证；P2-03 的 TOTP 设置/确认/恢复码/管理员重置与真实两因素 step-up 已交付；P2-04 用户生命周期 API、P2-05 全栈切换、P3 仍未交付。**

- R1：新增前滚迁移 50 `identity.authorize_native_session_role(...)`（绑定确切 session、租户、用户、会话签发的凭据/授权/会话版本、账户 active、非首次改密态、会话未撤销且在空闲与绝对期限内、租户有效、授权版本一致的角色授予；锁序 account→session→grant `FOR SHARE`；旧的未绑定 `identity.authorize_native_role` 被删除）。真实实例验证：撤销会话、凭据版本轮换、账户停用、会话过期、首次改密态全部被拒且写事务无残留；并发场景下先取共享锁的授权线性化在前、并发撤销随后生效，之后授权必败。证据：`evidence/mfa-r3/session-binding-integration.log`。
- R2：审计域授权改为可信来源分派（native 只走 `identity.authorize_native_session_role` 并记录来源/用户/版本，历史绑定只走旧围栏且不得跨来源回退）；`AuthorizationRevision` 改为绑定租户 revision+subject+真实 native 授权版本+生效授予集合，撤权再授予后摘要必然变化；Worker/Job 复检沿用记录来源，不降级 legacy。单元测试 `internal/investigation/assistant_audit_authority_test.go`。
- R3：退出时服务器撤销失败不再静默吞错——本机身份立即清除并取消旧请求，同时在登录页保留“上次退出的服务器会话尚未确认撤销”提示与“重试撤销”按钮（重试绑定原 sessionRef/会话版本/CSRF，浏览器换账号后拒绝跨账号撤销，不清新身份）。真实浏览器注入 500 后复验提示、重试、旧 Cookie 401。
- R4：浏览器验收脚本改为逐条断言（admin/clusters 200、operator 无授权范围 403 且 code=FORBIDDEN、legacy role-bindings 0 行、会话 LoA1→LoA2、注册 URI 只出现一次、恢复码 10 条、入口限流 429 且 code=RATE_LIMITED、首次登录 first-password-only 且无业务授权）。分母纠正：像素复算为 21/1363300（两处登记文案遮罩），旧报告的 1365851 已在 p2-native-auth.md/progress.md/acceptance.csv/task-progress.csv/preview-r2/result.json 更正；本轮构建按同一采集流程复算同为 21/1363300（`evidence/mfa-r3/visual-diff-current.json`）。
- P2-03：新增迁移 51（`identity.mfa_enrollments`、`identity.mfa_recovery_codes`、`identity.step_up_challenges`、`identity.step_up_sessions` 与 16 个 SECURITY DEFINER 函数，FORCE RLS、API/Worker 无表授权、固定 search_path）。TOTP 仅用标准库实现（RFC 6238，6 位/SHA-1/30 秒），密钥只以密文入库（AES-256-GCM 任务域密钥后端，另有 OpenBao/机器身份链适配器；无密钥后端时接口 503 而不是明文降级），恢复码高熵且只存 SHA-256 摘要。挑战绑定 sessionRef/operation/目标/风险确认/版本/绝对与空闲期限，一次性消费、并发重放拒绝；只有真实密码+TOTP 才 loa2，恢复码只恢复 loa1 并撤销旧会话、强制重设；管理员重置在目标绑定的 step-up 下同事务递增目标/版本、撤销会话、删除因子与恢复码并写审计。设置 secret 只在创建响应出现一次，后续 GET 不回显。
- 新增正式路由与客户端：`/api/v1/auth/mfa/status`、`/api/v1/auth/mfa/enrollments`(+`{id}`/`{id}:verify`)、`/api/v1/auth/step-up-challenges`(+`{id}:verify`)、`/api/v1/auth/mfa/recovery-codes:verify`、`/api/v1/admin/users/{userId}/mfa:reset`，OpenAPI/生成客户端/配置同步；Web 新增正式路由 `/account/security`（状态、绑定、验证、恢复码、管理员重置）。
- 验证环境：全新独立实例 `ops-ui-v12-mfa-pg-20261022`（127.0.0.1:15453，PG17.11，从空库前滚 1..51）、API `ops-ui-v12-mfa-api`（127.0.0.1:18084）、Web 容器 `ops-ui-v12-mfa-web`（192.168.0.63:30464）；15452 与 30462/30463 共享入口、15449～15451 及旧存储未改动，v12.admin 密码与 MFA 状态未改。证据：`evidence/mfa-r3/`（集成日志、浏览器结果与截图、像素复算、Go/Web 测试日志）。

## Codex独立审核preview-r2（2026-10-22）

仅接受SL-001布局改进、原生管理员列表接通等已验证子集；整体changes_required。定向Go/19Web/合同通过，独立像素复算21/1363300（两处登记文案遮罩）。只读真实DB探针发现撤销前上下文仍能通过新授权函数，原生审计分支仍被旧表围栏提前拒绝；退出失败静默与浏览器关键结果缺断言亦需修复。见[evidence/codex-preview-r2-review-r1.json](evidence/codex-preview-r2-review-r1.json)。P2-03、P2-04剩余生命周期、P2-05、P3尚未交付，ready不代表整体通过。47～49已执行冻结，修复继续新迁移。

## Codex 独立审核 MFA r3（2026-10-22）

结论 `changes_required`，取代上一节“R1～R4关闭”的整体声明。独立7包Go、25项Web、合同、构建及15453/schema51两项集成测试通过；认可迁移50会话绑定修正，原生审计来源分派/摘要结构已修正，完整审计与Worker运行仍需验证。MFA事务复检、恢复码失败预算、重绑流程、错误后revision、退出失败刷新、恢复后强制设置及真实业务再次验证仍未闭环；详见[evidence/codex-mfa-r3-review-r1/result.json](evidence/codex-mfa-r3-review-r1/result.json)。两个负向SQL探针均回滚；撤销探针仅在事务内延长证明期限以隔离撤销判断，失败浏览器探针保留且不计通过。50/51已执行冻结，后续修复只用52+。

P2-02/P2-03保持in_progress；SL-001既有静态visual结论保留，生命周期缺陷使interaction/authorization/live不能按全部状态通过。OpenBao实际启动接线、制品版本绑定和运行说明尚未完整。Hook记录执行模型minimax-m3.1-flash，与指定Deepseek-V4.1-Flash不符；保留成果并记录流程偏差，不将此视为模型质量测试。

## GPT直接继续：MFA生命周期 r4/r5（2026-10-22）

用户取消CodeBuddy执行，交接c767b57e已cancelled，后续由本对话GPT直接实现。新增前滚52并在隔离15453执行；修复MFA当前会话复检与锁、持久恢复失败预算、保留旧因子的原子重绑、服务端强制重新设置、页面revision及嵌套guard、退出待撤销刷新。独立真实表单与500/模拟超时/模拟断网后GET200→刷新→重试401、恢复后重新登录限制及重新设置恢复业务均通过，详见evidence/gpt-mfa-r4/result.json。隔离新入口30465/API18085，旧30462/30463/30464及用户凭据未变。

继续新增53精确challenge证明解析/重置锁序及原生源配置请求绑定。真实集成已验证无proof拒绝、篡改body拒绝、正确PATCH持久revision2；命令及其余业务接线仍进行中，P2-03不标完成。52/53已执行冻结。OpenBao机器身份启动接线已写，实际Transit验收尚未完成；未把文件密钥后端当成OpenBao通过。

## GPT直接继续：r6真实OpenBao与r7管理操作接线（2026-10-22）

隔离15453已前滚54并冻结。真实OpenBao TLS/Raft + Kubernetes TokenReview + ops-api projected JWT + 专属Transit key完成MFA设置、重绑、恢复、退出失败恢复及后端停机503/重启恢复验证，见evidence/gpt-mfa-r6/runtime-openbao/。原生命令证明与实际assessment/UID/资源revision/命令摘要/执行配置版本绑定的真实集成通过，证明测试不代表真实命令派发、Worker或策略准入。独立来源浏览器已实测PATCH200并持久status=disabled/revision2，请求ID 01a16359-b24e-7406-8400-2a7c46710b53；完整结果仍在远程gpt-r6-preview/logs/source-browser-result.json，尚待回收截图。

r7补齐策略草稿、发布、激活/回滚、版本退役及专用执行配置发布/退役的请求级证明，移除RetireProfile/OnboardHost旧角色表围栏；明确拒绝native修改legacy授权/创建legacy初始授权租户。新增administrative_write仅绑定实际HTTP方法/路径/规范化内容，允许新对象尚无ID/revision；修订号/固定版本仍由业务服务检查。Go三包及合同、27项前端定向、3项证明/冲突恢复检查与构建通过。r7尚未部署及真实数据库运行，不能标P2-03完成。

当前远程局部阻塞：ssh mssc-direct的本机10.10.10.1绑定地址消失，192.168.0.63:22严格已知主机入口也超时；未改SSH/网络配置，未启动OrbStack。继续本地原生用户/授权开发，所有新迁移和业务浏览器验收保留未验证。OpenBao测试JWT是限时TokenRequest样本，重新联调前需重新签发任务专属JWT，生产部署仍需Kubernetes自动轮换projection。

用户特别要求保留“额度耗尽前”的未完成项；完整接续断点、已接回结果、r7未运行变更、后续依赖及环境保护见[continuation-handoff.md](continuation-handoff.md)。远程断连单独记录，不取代原中断任务。

## GPT直接继续：P2-04原生用户生命周期与授权r8（2026-10-22）

已源码接通C-014～018及C-019～021原生适配：真实账号列表/创建、资料与启停、独立密码重置、会话撤销、双角色分别授予与撤销；角色范围使用登记cluster UID，namespace不得隐式扩大为集群。创建强制改密且无默认角色，密码/状态/授权变化撤销旧会话；新SQL在目标有序锁和tenant advisory lock下复查当前原生管理员/版本，保留最后有效管理员。所有业务变更与幂等结果、审计在同一事务；API没有identity表直授权。原生请求不回退legacy角色表，旧OIDC路径继续保留原合同。

新增55尚未执行、不冻结；delivery-baseline的55是源码迁移范围，不是运行库版本。当前最后验证运行库仍15453/schema54。新用户页面复用v12公共页头、Surface、Modal/Drawer；安全验证和原生密码12～256字符要求作为补充交互，不能直接继承HTML像素通过。原生C-021只能撤销；恢复授权须重新使用C-020明确角色/范围，保留新binding和审计。

密码请求先严格规范化，再用当前会话CSRF秘密对密码做HMAC参与证明/幂等摘要；不将低成本密码验证摘要、明文密码或Argon2 hash写入证明、幂等响应或审计。Go/Web独立向量一致，修改密码/目标/版本使旧证明失效。

本地144 Vitest+6 SSE、Go auth/identity/httpapi/tenant/action/app与合同、TypeScript/Vite正式构建通过；最终HTTP/密码摘要/用户边界定向复验通过；Linux API和集成二进制已构建未运行。失败记录保留：首次合同检查因55源码范围未同步delivery-baseline失败，修正后通过；合同CSV写入列名错误已从本任务最新保存快照完整恢复177条并核对，后续原子写入，见evidence/gpt-admin-r7/contracts-recovery-r8.json。

真实数据库集成用例已准备，使用真实TOTP时间窗等待，不重置防重放计数；需在恢复连接后用15分钟测试超时运行。实际DB/HTTP结果、最后管理员并发、审计回滚、真实浏览器/视觉尚未验证，C-170/C-171历史主体显式映射及历史归属读取未实现，P2-04继续in_progress；P2-05/P3及其余依赖仍未完成。版本、制品及限制见[evidence/gpt-users-r8/result.json](evidence/gpt-users-r8/result.json)。

最终整理出现Go摘要函数重复err声明，编译失败日志已保留gpt-users-r8-boundaries-compile-failed.log；修正后相关Go包/合同复验及HTTP最终复验、Linux API/集成二进制重建通过，见gpt-users-r8-go-revalidated.log和gpt-users-r8-http-revalidated.log。密码HashPassword延后到真实操作证明及幂等授权通过之后，重放直接复用结果，避免未验证请求进行昂贵哈希。源码制品清单已绑定最终版本。

## GPT本地继续：历史主体受控清单与显式映射r9（2026-10-22）

按用户要求推进不依赖远程的部分，未探测/修改远程、未启动本机数据库或业务浏览器。补齐C-170/C-171源码：受控migration-duty CLI导入已人工核对清单，严格JSON/重复键/大小/目标ID边界；原生管理员用逐请求再次验证提交映射，服务端按同tenant清单摘要、完整旧主体及真实user ID匹配。旧主体必须在该tenant已核实历史来源中存在，拒绝现有原生主体与冲突归属。映射与账号版本更新、旧会话撤销、幂等结果及审计在同一事务；原生subject和双角色范围不变，不复制旧角色。列表、分页、创建弹窗已接真实生成客户端；修改字段后旧证明失效，关闭进行中请求提示刷新确认。

新增56与此前55均未执行、不冻结；源码delivery-baseline已为56，最后独立验证运行库仍15453/schema54。复核修正55的管理员计数错误：各管理员应匹配其自身授权版本，不能同时等于当前查看账号版本。已准备不同版本账号的断言，尚未运行SQL。原有1～54未改。历史映射存在但不代表历史读取已接通；旧NULL inventory_digest记录保留，不自动升级为本轮核对材料。

本地Go identity/httpapi/contract通过，CLI新命令及边界定向通过；145 Vitest+6 SSE、TypeScript/Vite构建通过；Linux API/CLI/集成二进制交叉编译通过未运行。opsctl首次全包测试保留失败：TestBundleBuildCLIAndIndependentVerify因opa-sdk-source与既有reviewed对应源码锁不一致，未修改来源材料/锁来绕过；新CLI定向测试通过。进度校验177合同/458状态通过，不提升任何产品六维验收。迁移55/56执行、真实HTTP/持久化/并发/审计回滚、历史归属读取复检和视觉均待验证。

操作、恢复边界与续接步骤见[subject-mapping-runbook.md](subject-mapping-runbook.md)，证据见[evidence/gpt-mapping-r9/result.json](evidence/gpt-mapping-r9/result.json)。P2-04继续in_progress，P2-05与P3未宣称完成。

## GPT本地继续：原生部署配置及执行配置读取r10（2026-10-22）

独立推进P2-05可离线实施部分：platform Helm runtime.humanAuthMode统一Web/API/Worker，native发布auth.mode=native、去掉OIDC issuer/代理/CA依赖，/oidc/404；API接固定tenant/origin和现有MFA Transit key，复用独立CA与既有OpenBao audience限时JWT投射；不向Worker添加MFA key配置，不改OpenBao策略。缺配置、未知模式、非法origin/端口、API/Web origin不一致均拒绝渲染。Web实际渲染配置参与checksum，保留TLS/CSRF同源边界、禁缓存/SSE和旧OIDC兼容配置。

C-134 action.ListProfiles仍直查legacy角色表的遗漏已修，改共享authz.RequireAdmin及独立native operator当前会话/版本复检。单元覆盖原生管理员、撤权、operator独立、主体不匹配、数据库失败；守卫集成补读取与撤权断言，仅交叉编译未运行。发现原OpenAPI有cursor/limit但处理器/服务未实现分页与meta，合同审计显式保留缺口；C-139专用退役未改。

Go action/authz/app和完整contract通过，最终Helm定向复验通过；Linux API及集成二进制构建通过。首次SP06离线fixture遗漏SP05导致失败，补齐原依赖后通过，失败日志保留；不降低原门禁。没有本机数据库/业务浏览器、没有远程调用/部署、没有CodeBuddy。未改Web源码，不重复无关Vitest/视觉参考采集，未提升产品验收。

P2-05仅配置子项in_progress，P2-03/04前置验收仍未完成，不宣称进入后续业务验收。正式Bundle安装器/business-values/Profile仍依赖OIDC，Job/Worker授权来源与版本持久化、历史读取复检、无OIDC真实冷启动和切换回退尚未完成。55/56仍未执行，最后独立验证schema54。操作及限制见[native-deployment-runbook.md](native-deployment-runbook.md)，证据见[evidence/gpt-native-deploy-r10/result.json](evidence/gpt-native-deploy-r10/result.json)。

## GPT本地继续：定向代码复审与修正r11（2026-10-22）

复核r8～r10原生身份/授权、主体映射清单、C-134及部署调用链，修正两项P1源码问题：55修改一个角色时曾将其他active但版本已失效的角色一并更新为当前版本，会意外恢复旧角色；现只推进变更前版本本来有效的独立授权，准备stale-admin→更新operator仍不成为admin的真实集成断言。55未执行，SQL回归只编译未运行，不写通过。动态PKI在OIDC+SP04、无SP06时API缺OpenBao地址/CA的遗漏，已用Helm用例复现后补齐，失败证据保留，完整合同及最终定向复验通过。

C-134补HTTP分页/meta，先按当前权限过滤，再按profile ID+版本定位游标；同ID多版本不丢失，未知/失权/退役游标拒绝，limit默认100最大200、cursor最大128。OpenAPI与生成代码同步，原生Cookie认证声明接入；目录读取仍沿既有实现，不把数据库大目录分页/性能写通过。清单解析新增reviewedBy/legacySubject的NUL拒绝，避免非法文本触库后才失败。

identity/httpapi/action/app、完整contract、本轮最终OpenAPI/UIV12及Helm定向、CLI相关定向通过；13项前端定向与正式构建通过，Linux API/opsctl/守卫集成二进制构建未运行。未连接远程、未运行本机DB/业务浏览器、未派发CodeBuddy。1～54未改，55/56仍未执行，未升级六维验收；P2-04/P2-05及V1.2未完成。完整发现、具体输入、修正及验证界限见[evidence/gpt-review-r11/review.md](evidence/gpt-review-r11/review.md)；最新证据[evidence/gpt-review-r11/result.json](evidence/gpt-review-r11/result.json)。正式安装器、Job/Worker显式原生授权、历史归属读取、并发与审计回滚仍待继续。


## GPT继续：SL-005对话框与独立双角色预览r14（2026-10-23）

首次改密从独立整页修正为固定HTML的660px居中对话框，复用V12Modal；当前密码、12～256字符安全策略及不读取业务背景保留并登记。取消/关闭走真实服务端退出，提交期间禁用关闭/取消/表单。真实远程浏览器验证无业务预取、取消200及旧Cookie401、刷新仍强制改密、业务403、确认不一致无请求、错误当前密码401保留会话、成功201轮换及旧Cookie401、无默认角色、刷新持久化及退出；独立SQL核对版本2/强制改密解除/原生主体审计。API/持久化仅提升SL-005对应维度，其余未提升。

149 Vitest及正式构建通过；最初全量测试因旧标签失败，修正调用者后通过。jsdom动画可见性失败仅测试中关闭motion，产品动画未改；首轮浏览器采到scale0.2动画帧、后续拦截pending请求脚本中断及虚拟Select点击失败均保留，不计通过。完整像素和pending/关闭/退出失败浏览器验收未完成。

远程独立30468/API18088/metrics19088复用r13相同API制品和任务数据库15453/schema56，未覆盖30467和其他环境。用户明确要求可看全部菜单账号，已为独立任务账号经真实用户UI/密码+TOTP分别授予operator与platform_admin并验证30467全部菜单；操作员仅一个明确登记的任务fixture集群，无真实来源准入结论。凭据仅远程private，不写证据。P2仍in_progress、V1.2未完成；下一步延续P2历史归属/事务复检/并发故障、原生first-tenant及Worker、SL-049完整样板。证据见evidence/gpt-dialog-r14/result.json。


## GPT继续：SL-005交互/登录背景r15与授权范围前滚r16（2026-10-23）

SL-005补齐真实关闭/Escape/加载禁用与退出失败网络故障、刷新、精确会话重试，均远程浏览器通过；错误当前密码、取消与持久化沿r14证据。重新核对原始PNG后纠正“overview背景”假设：固定参考实际为登录背景，提取NativeLoginLayout并作为inert、disabled、无凭据/无处理器背景复用，不触发业务读取。实测660px宽、6px字段间距、18.7px提示行高、10px按钮间距；安全额外字段/文案导致高度不同，完整像素不通过。152 Vitest、6 SSE及正式构建通过，中文关闭与administrator/operator首页路径均覆盖。浏览器英文Close/加载图标定位失败、组件库8px默认padding/margin及修正过程保留，不作产品失败掩盖或继承通过。

原生范围200namespace合同/SQL100差异已实测复现：真实UI+密码/TOTP授权400。新增57仅将admin_account_write的cluster/namespace上限100/100对齐合同50/200，保留所有事务、当前会话/角色/版本、最后管理员与独立授权推进；1～56与HEAD原文逐项核对不变。任务15453已执行57并冻结，后续58+。相同合同真实UI201并持久200；API角色SQL事务中200可接受，201namespace/51cluster拒绝22023，探测全部ROLLBACK，原binding/范围/版本不变。完整contract及identity/httpapi/db-migrate通过。另真实用户UI密码重置200、强制改密201、刷新进入operator总览并保留独立200namespace范围，无管理员菜单。首轮重置脚本误期望201而实际200，成功重置后中断未保存随机密码；仅隔离测试账号再经真实UI重置恢复，两个事件保留，用户r14账号未改。

30469/API18089/metrics19089为r15独立预览，最终容器ops-ui-v12-gpt-r15-web-r5及web-footer-final目录；API仍r13相同二进制。30467/30468和历史制品保留；三个预览共用任务15453，当前schema57，早期schema56证据仍只代表当时版本。未写共享开发库/生产。SL-005 interaction提升passed，visual/authorization/live未提升；P2仍in_progress。历史业务归属读取与当前范围复检、最后管理员并发/审计故障、原生first-tenant/Job/Worker/无OIDC冷启动、SL-049真实样板及其余切片仍待。证据见evidence/gpt-login-r15/result.json与evidence/gpt-scope-r16/result.json。


## GPT继续：P2-04审计故障与管理员并发r17（2026-10-23）

新增可重复边界测试并复用原生管理精确password/TOTP证明：仅本轮新建tenant/target/identity.admin.update的临时audit触发器令资料修改返回500，账号revision/业务审计均未提交；清理后同证明+同幂等键重试与重放200，只有一次revision和一次业务审计。双管理员并发停用前，控制事务持有既有tenant advisory lock；确认两个正式HTTP事务均在数据库锁等待后才释放，验证一个200、一个401/403/409，保留恰好一名有效管理员，幸存者实际登录/必要首次改密后读取用户列表200。

守卫MFA/用户管理整组623.37秒PASS；本地编译/vet通过。使用httptest正式Handler+真实任务PG15453/schema57及文件sealer测试fixture，不冒充生产OpenBao/浏览器/Worker或模型验收。只读回执绑定实际tenant/request/audit ID并确认故障trigger/function为0，r14用户账号仍active/credentialVersion2/双角色；30467/30469服务active。初次只读证据采集误用payload列，按实际record列修正后通过，失败说明保留，无需重跑已通过测试。

历史归属读取仍未实现：助手会话/列表/结果分别过滤旧主体，冻结grant摘要及Job输入/权限/策略校验横跨消息/引用/事件/Worker；不能只放开WHERE、改写旧主体或覆盖摘要。operational Job未持久化native authority source/user/version仍是P2-05缺口（本轮静态核实，不是运行验证）。命令历史资源域读取使用当前operator范围，不应误改为个人主体过滤。具体位置见evidence/gpt-admin-r17/history-read-review.md。

本轮只新增测试、复审与证据，没有修改Go业务实现、迁移或部署；1～57冻结，下一SQL58+。产品六维未提升；P2-04仍in_progress，保留历史读取/映射并发/其他故障/产品视觉、P2-05及SL-049完整样板等剩余项。证据见evidence/gpt-admin-r17/result.json；原生执行/取消/再次验证凭据不因映射放宽。


## 2026-10-26 r24：原生全栈/账号用户视觉收尾（远程阻塞，未完成1/2整体验收）

补SL-003我的账号、SL-004自助改密入口，服务端姓名/登录名投影同步OpenAPI/镜像Schema/生成客户端；复用真实会话轮换，等待禁止重复提交及关闭。用户页表格/筛选/详情抽屉/弹窗和公共层按固定HTML尺寸修正，保留安全扩展。正式Bao新增显式已有MFA key的API-only encrypt/decrypt授权与只读status漂移核对，不隐式创建/轮换key，不修改foreign role。

本地172 Vitest+6 SSE、最终7项账号/用户增量、Go OpenBao/httpapi/opsctl race、app/contract、生成一致性/来源检查及正式构建通过。27主状态+补充状态见evidence/gpt-native-visual-r24/state-review.json，未提升六维验收。新任务Bao实际初始化/配置通过；旧任务PG新空库迁移1被有限owner权限拒绝，未升权。准备全隔离PG15454并接续时mssc-direct再次超时，执行结果未回收。30471未确认可用；轮换、独立Worker/CLI/全栈冷启、重启接管、撤权取消前滚和新版截图均未通过。恢复须先查实际对象，不盲重跑一次性脚本、不覆盖原账号/回执。详见evidence/gpt-native-visual-r24/README.md与result.json。


## 2026-10-27 r24续接：独立原生运行与账号安全验证（覆盖上节远程阻塞描述）

远程已恢复并重新核对mssc身份。现用独立namespace `ops-native-v12-r24-20261026`、目录 `/data/ops-v12-native-visual-r24-20261026`、PostgreSQL17.11/15454/schema70、预览 https://192.168.0.63:30471/login。真实first-tenant CLI与API/独立审计Worker/Web/OpenBao/Archive无OIDC启动通过；MFA只授API，Worker访问403；同Pod API/Worker/reviewer投射凭据自动续取后仍有效。10月27日再次重建API/Worker后，同会话保持有效，退出后旧Cookie401；旧签名段未变，生产S3读/解密/摘要/签名/前驱链验证范围1～49共7段通过。

真实浏览器已验证登录/首次改密/自助改密、MFA注册/恢复/重新绑定、管理员密码与MFA重置、零角色自助安全和业务拒绝、独立管理员授权撤销、禁用持久化与迟到结果恢复。MFA enrollment owner pending/confirmed GET200不泄露秘密、其他管理员GET404。测试为最终178 Vitest+6 SSE、Go race/contract/生成及构建通过，范围以r24结果为准。SL-003稳定前景660×424/header76与固定切片尺寸一致；内容/背景/完整逐状态像素仍不通过，不提升458项整体验收。

实际版本为API/Worker final二进制、Web-r4、`source-final-2/`，源码归档哈希71a4480aa1d67094585c2507689a5f179d693e047a71e93abd375c6b9a431a74。当前是挂载源码制品canary，不是新OCI准入或不可变Bundle验收。SP06业务dispatcher未配置，不能用审计Worker归档冒充业务Job签名Context/工具结果/接管/取消。1/2与P1/P2保持未完成，P3～P8剩余不变。所有早期失败、动画帧误采和脚本导出失败保留；凭据仅远程private，不导入仓库。

详见 [r24记录](evidence/gpt-native-visual-r24/README.md) 与 [结果](evidence/gpt-native-visual-r24/result.json)。

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


## r30（2026-10-27）：P2-02/P2-03 原生认证与账号安全整组复刻及真实验收

用户要求整组交付而非局部小修。本轮用 Web-r14（`ops-v12-native-r30-web-r14`，index SHA256 `8465ddec2b4632b7cc5ac518f39506255c9a65b17b7a58e085fd217377075ba8`）在 30471 完成登录/首次改密/自助改密/退出撤销/MFA 全生命周期/限流锁定/同源与 CSRF/零权限的真实浏览器与真实 PG 验收；API/Worker/investigator 仍 r28，PG15454/schema70 未改，无迁移、无模型调用。

修复的真实缺陷：登录与改密在传输/5xx 失败时回显 API 客户端通用文案「请先核对持久记录」（登录不写记录，且可能被读成凭据结论），改为各自的可重试说明；登录成功后必须重新绑定 MFA 时，Login 页的会话重定向与提交处理的目标不一致，存在落到普通首页的竞态，现两处统一为 `/account/security`；账号安全页面包屑曾为「未匹配页面」且顶层页面模块名重复，现扩展页登记为「账号 / 账号安全」、`navModule` 只对子项返回模块名；焦点环由旧 V1 的 `#208f89/3px` 改为原型 `#aaa7ef/2px`，`V12Modal/V12Drawer` 初始焦点按原型原生 dialog 语义落到关闭控件（`[data-autofocus]` 仍优先）。

真实证据要点：真实 401 统一常量文案且不禁用重试；请求挂起时唯一提交控件 `aria-busy` 且 disabled；真实 abort 显示服务不可用；5 次失败后正确密码返回与错误密码完全相同的 401（不暴露锁定），15 分钟后同一密码 201；入口限流第 60 次 429+`Retry-After` 且窗口内正确密码同样 429，窗口后恢复；首改密会话对业务/MFA/管理员接口一律 403，取消/改密均撤销旧 Cookie；MFA 挑战闲置 660s 后用正确动态码仍返回真实 409 并清除旧密钥；恢复码一次性消费撤销全部会话且整套恢复码删除；再绑定推进 `confirmedAt` 并拒绝旧恢复码；账号菜单退出 200 且旧 Cookie 401、刷新不回会话。DB 侧 session 撤销原因、challenge `expired`（created→idle 恰好 600s）、enrollment 状态与 audit 事件数一致。

视觉：复用 r3 参考、无掩码。SL-003 对话 660×423.8125/头 76/关闭 31×31 与参考一致，前景按参考滚动条 -7px 对齐后 12651/279840（>20 者 7278）；SL-004 对话 660×492.625，前景对齐后 9257/325380（>20 者 5235）；SL-001/002 差异全部落在演示凭据预填、演示标签/页脚与真实账号名文本，SL-002 错误条逐像素相同；SL-005 因登记的安全扩展字段「当前密码」比参考高 84.6px，整页不通过。故只提升已有充分实证的 interaction/api/persistence/authorization/live，SL-001/002/005 的 visual 仍未通过；同时记录 operator 真实运行仍缺合法集群范围（未伪造集群注册），不以本组通过宣称 P1/P2 或 V1.2 完成。

证据：`evidence/gpt-auth-r30/`（README、result.json、runs/、shots/、visual-comparisons.json、db-evidence.json）。validate-progress.py 通过 458 状态/177 合同；六维 passed：visual 1、interaction 5、api 5、persistence 5、authorization 3、live 5（复审后 SL-001 visual 纠正为 not_verified，现为 visual 0，见下一节）。

## r30 返修（2026-10-27，依据独立复审 changes_required）

集中处理复审五项，不重跑已通过的改密/MFA 重置/r28 审计 Worker，不改迁移、不放松身份语义。

1. 敏感截图：17 张 `mfa-*`（另有 3 张 `mfa-status-*` 与 2 张不确定的安全页图）已从远程可浏览目录移入 `private-evidence-quarantine`（0700/0600），仓库内不留该批次；本轮未打开、未展示、未复制回 Git。因无法逐图判定，9 个 `r30.accept.*` 全部经真实管理员 UI 重置 MFA → `mfa_reset_required=t`、活动登记 0、未用恢复码 0、活动会话 0，隔离图片中的任何 URI/恢复码/动态码均已失效；`v12.r24.admin`、`r24.member.*` 未动。新增 `harness/safe-capture.mjs` + `harness/screenshot-policy.md`：截图前探测 otpauth/恢复码/动态码/base32，命中即拒绝出图；含秘密状态只记录机检断言。安全重采 3 张（已启用、取消绑定后、管理员安全页）并替换了原不确定图。
2. SL-001 `visual` 由旧 PASS 纠正为 `not_verified`（旧 PASS 属更早基线双遮罩历史，r30 整图不一致），全表 visual passed 归零，汇总同步。
3. `04` 脚本"提交已确认因子正确码"的结论撤回：该 409 发生在密码步、服务端未评估任何因子；DB 时间表述改为"最后活动起 600s"（created→idle 601s）。改由 `12-idle-expiry-confirmed.mjs`（独立身份+自生成确认因子：无活动 11 分钟后 409 且 challenge `expired`、idle 恰 600s；随后同身份新挑战用同一因子的正确码达到 LoA2）实证。
4. 补 `10-absolute-expiry.mjs`（真实墙钟：t+9min 完成 password 把闲置窗口刷到 t+19min，t+15.8min 提交确认因子的正确动态码仍 409，绝对窗口 `900s` 恒定，全程无 SQL 改时间/状态）与 `11-cross-account-concurrency.mjs`（跨账号挑战不可验证、同 revision 并发恰一个 200 一个 409、一次性码不可重放、他人恢复码无效）。
5. 运行手册补 r30 段（Web-r14、安全前滚/恢复边界、截图策略、身份语义不回滚、数据库恢复后须重跑）；剩余范围纠偏为"SP06 未完成限于资源/业务工具路径，审计 data_query 真实链路 r28 已通过"。

证据：`evidence/gpt-auth-r30/repair.md`、`containment.json`、`runs/{absolute-expiry,idle-expiry-confirmed,cross-account-concurrency,safe-captures,quarantine-reset*}.json`、`harness/`、`shots/security-*.png`。validate-progress.py 通过，六维 visual 0/interaction 5/api 5/persistence 5/authorization 3/live 5；P2 整组与 V1.2 继续 in_progress。

## r31（2026-10-27）：原生授权／历史主体保留／全栈一致性闭环

先修上一轮复审余项，再做实体业务闭环。余项：绝对上限改为微秒断言（实测 `idle=absolute-1µs`，此前 `to_char` 毫秒截断显示相等，脚本改 `US` 精度并改数值比较，未重等窗口）；r30 九身份会话按「物理行已撤销」与「会话现已失效」分开陈述（第 9 个身份有 1 条未撤销行但 idle 已到期）；`safe-capture` 改为 settle→探测→截图→再探测（命中即删图），新增假秘密自检 3 例（等待期出现 URI 拒绝且不写图、探测后出现 OTP 删除已写文件、无秘密正常保留）。

A（关键改观）：核实任务库 `cluster_registrations/source_registrations/resource_entities` 均为 0 后，新建任务专属只读身份 `ops-r31-sp04-read`（17 个核心 Kind 只读、无 secrets、无写动词；仅 `r31-graph-lease` 可 get/update 用于图所有权续约；24h TokenRequest 令牌只存 Secret 并只读挂载），以真实 `kube-system` UID `79014400-9453-4cb4-9678-0fae47981d79`、真实 endpoint/CA、实测发行版与版本、实测能力标志，经真实管理员会话+绑定本次请求 method/path/bodyDigest 的密码+TOTP step-up，调 `POST /api/v1/admin/clusters`（201）与 `POST /api/v1/admin/source-registrations`（201）登记；首次 `nativeTenant` 声明被采集器按契约拒绝（`source_scope_unverified`），用真实 `PATCH` 收窄为 clusterUid+任务 namespace 后 revision=2，SP04 采集 17 个 GVR 全部 list/watch 成功，图所有权 epoch=1 续约，资源实体 0→72。未伪造 UID/来源，未改共享集群配置与第三方组件。

B：管理员 `v12.r24.admin` 无 operator 授权、`authorizedClusters=0`，`/api/v1/resources` 与 `/overview` 均 403（管理员角色不自动获 operator）；经真实 UI 创建 `r31.op.1793086009209`（201、行内「尚未授权」）并经授权弹层授予 operator（单集群+任务 namespace，201），整页刷新后复读仍一条 active 且 scope 一致。该用户首次登录被限制 `first_password_only`，强制改密后 `/api/v1/resources` 200 返回 39 条真实资源且全部在任务 namespace，`/overview` 200，未授权 namespace 过滤 0 条，浏览器页头显示集群名。界面收窄到 `default` 后旧会话 401、重登读取 0 条任务 ns 记录；界面撤权后重登 403；再授权后重登恢复 39 条；以同一受控写路径尝试撤销唯一管理员 → 409「不能停用或撤销最后一名有效管理员」，之后 `platform_admin` 与 `activeAdministrators=1` 不变、资源仍 403。观察到瞬时 503 `GRAPH_NOT_READY`（租约续约与归属镜像过期窗口），按原样记录每次尝试。

C：当前工作树集成测试在任务实例一次性库迁移 1→70，`TestNativeMappedHistoricalOwnerRead`/`TestNativeMappedHistoricalTurnRead` 均 PASS（映射后可读且冻结字段不变、未映射/异主体拒绝、撤权拒绝、历史读取不产生 Job/Worker 授权）；一次性 SUPERUSER 探针角色用后删除（保留库所有权转回 postgres）。想为不存在的旧主体造映射时被真实守卫拒绝（`import_subject_inventory` 要求该主体已在本租户历史中），映射表仍 0 行——这是「机制验证+拒绝编造」，不是旧用户全业务迁移。

D：API/Worker/调查器/Web/CLI 的来源哈希、env（无任何 OIDC/Keycloak/issuer 变量）、Web `runtime-config.json` `auth.mode=native` 与 `/oidc/...` 仅落 SPA 外壳均已核对；重建 API Pod 后用重启前的同一 operator 会话回放仍 200 并读到 43 条真实资源（会话在 PG 而非进程内存）；`go test ./test/contract -run Native` 通过。正式不可变 OCI/Bundle/离线切换仍缺合格物料，保持阻塞。

证据：`evidence/gpt-authz-r31/`（README 与各 JSON/日志）。六维未提升（visual 仍 0）；P2-04/P2-05 继续 in_progress；operator 真实范围已由「无合法集群」变为「已登记并有正反例」，但仍未做整页视觉，不据此关闭 P1/P2 或 V1.2。
## r32（2026-10-27）：采集收敛／短期凭据自动轮换／503 根因修复与原生管理接续

针对 r31 独立复审 5 项的集中返修，全部在 `ssh mssc-direct` 真实环境完成；API/Worker 升级为本轮源码构建的 r32 二进制（Worker `2752842d…`、API `5057b8d3…`），清单与配置见 `evidence/gpt-authz-r32/README.md`。无迁移（1～70 不变）、无模型调用、不涉及虚拟化；`v12.r24.admin`/`r24.member.*`/共享服务未动。

1. 采集 RBAC 收敛：删除 r31 全局 ClusterRole/Binding 与专用 SA，改为 Worker 自身 SA + 命名空间 Role（任务 ns 11 类 namespaced Kind）+ 仅 6 类无命名空间 API 的受限 ClusterRole（保留原因与差异已在 README 声明）；`kubectl auth can-i` 正反例实测；采集端 `Namespaces` 必填并与登记 scope 启动期严格校验，namespaced GVR 走真实 namespaced list/watch 路径；chart（sp04.yaml/deployments.yaml/values.schema.json）同步为同一模型并新增合同测试。
2. 短期凭据自动轮换：Worker 采集凭据改为 600s 投射 ServiceAccount 令牌（kubelet 自动轮换、逐请求读、异常文件拒绝），实测三次轮换摘要（2545bbc4→ee3622d6→df0b6b85）与跨轮换连续 22 次资源读全 200（08:28→08:33，覆盖 08:31 轮换边界），不再需要手工续 24h 令牌。
3. 503 根因实测修复：探针构建实测 60 连读 3×503 + worker `gate_timeout`×4 + 续约分段耗时（lease_read≈230ms/lease_cas≈252ms，10s 探测叠加时全程 ~500ms > Confirm gate 300ms）；最小修复后（查询路径脱离续约栅栏、所有权快照一次捕获、Confirm 复检 UID/epoch/holder/renewTime 不变）60 连读 0 失败、0 confirm 失败；所有权变更/断开/过期仍 fail-closed，租约 SQL 未改。
4. kubernetes 来源 nativeTenant 登记不一致：`ErrScopeIncompatible` 统一 Register/Update/Rotate/Rollback 校验，存量不兼容行以 `queryCapability.reason=incompatible_data_scope_mapping` 呈现并经 update 修复，rotation 拒绝重新激活不可采集来源；OpenAPI/单测/HTTP 用例同步。
5. 原生管理接续：B 完成——新零角色身份 `r32.acc.1793091626814` 停用→登录 401 UNAUTHENTICATED、恢复→登录+首改密 201+会话 200、并发 disable/restore 与并发同向写（同 expectedRevision、独立 step-up）恰好一个 200 一个 409 CONFLICT、TOTP 每 30s 步只消费一次（`evidence/gpt-authz-r32/runtime/account-lifecycle.json`）。C 历史主体正反例的 API/UI 路径与 D 无 OIDC 隔离冷启动仍未执行（需一次性独立测试库/整栈环境）；chart 渲染/配置一致性本轮完成。

证据：`evidence/gpt-authz-r32/`（README、manifests/、runtime/ 的 live RBAC、探针/修复后读数、跨轮换读数、账号生命周期）。本地 `go test ./internal/{graph,source,httpapi,app}`、`./test/contract`（含新增 SP04 渲染用例）全部通过。六维未提升（visual 仍 0）；P2-04/P2-05 继续 in_progress。

## r33（2026-10-27）：GPT 直接接续、采集与历史 HTTP 返修

用户撤销 CodeBuddy 执行，交接1d91d497已取消；本轮 GPT 直接实现/自测/远程验收，保留全部未提交改动。修正 Lease nil Graph 崩溃、chart 多 Namespaces 只授权发布 namespace、LIST(float64)/WATCH(int64)导致同 UID/RV 数字投影不一致、归档v2首观察并发未归一及冲突告警被其他队列完成清除；来源兼容 reason 同步 Go/TS 生成物。远程真实 PG 的来源登记/修复/轮换/回滚与归档全组回归通过，失败保留。

C 增量：真实 PG + 正式 Cookie 登录/中间件/历史 HTTP 回归发现详情200但消息分页404；只读预检改 GetReadable 后会话/消息/轮次200，同名未映射/撤权拒绝，追加任务拒绝且 Job 不增加，冻结历史归属/input/scope/binding 不变。受控历史 fixture 不冒充真实旧库迁移；完整 inventory/mapping API/UI 和执行审计引用未验。

真实采集：专属无秘密 ConfigMap UID85711746-8bbe-4411-9620-f683d5acf665，轮换前 RV1863416→自动短令牌更替后 RV1863556→Worker重启重连后 RV1864069，三版实际PG保留，API回显标签two/three（200）。API/Worker已部署r33，Web仍r14，schema70不变；来源及制品见 evidence/gpt-authz-r33/source.json/runtime-final.json。没有手工续签、模型调用、共享配置/用户账号更改或迁移。

尚未关闭：C完整管理UI/API与D独立无OIDC整栈冷启动、真实凭据故障恢复、第二namespace实际授权/冲突UI及P1/P2完整视觉；正式Bundle材料门禁另记，不把未执行称外部阻塞。P2-04/05仍in_progress，六维通过数不变（visual0）。详细边界、失败和接续见 evidence/gpt-authz-r33/README.md。
