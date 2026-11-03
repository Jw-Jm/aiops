# r37 日志工作台：实施中，未整体验收

GPT直接实现，延续脏main工作区；不提交/不推送。不要使用CodeBuddy。工作在 mssc-direct 任务库/目录/端口，未做长期负载测试。

已完成代码：独立观测查询 C024～C028/C168 的API→签名mTLS Worker→持久化→正式日志页面；迁移71新增独立记录/租约/不可变请求结果/原生与来源事务复检。查询不产生AI会话或假Job。重放用Idempotency-Key固定记录；活跃并发冲突；取消请求与终止回执分离，HTTP取消不宣称来源停止；迟到结果拒绝。Worker中断显式失败后允许新查询；不默默重复来源调用。历史读取重新核对原生会话/操作员授权、资源/namespace UID、来源revision/credential及当前资格。

复用现有 analytics.Victoria 两个固定模板：总体聚合+最多20条脱敏样例，最多2次来源请求、128KiB响应体预算、8秒总时限；样例不代表总量，本地关键词只筛样例。只有真实测得零才no_data；采集完整性未知保持partial/coverage未知。补 qualification.resourceUids：只验证过一个UID的采集材料不能准入其他/重建Pod，构造时冻结此数组；有正反例单测。

前端已实现输入、状态轮询、结果、空态、错误、取消、重查、历史、来源状态和脱敏原文弹层；当前导航仍待真实API验收后开启。尚不能关闭P3：预置视图/按范围AI衔接、完整视觉及两集群样板等仍未验/未完。旧参考736张不重采。

验证：34项原生界面定向用例；新增3项工作台机制用例通过（迟到响应/取消版本/样例与总体分离），UI fixture不是live证据。远程独立PG迁移1→71，TestNativeObservationQueryLifecycle通过（幂等/取消/晚到/不可变结果/来源停用/中断/撤权/0 AI Jobs）。首轮参数$6 timestamptz推断错误已修，失败保留。OpenAPI生成、正式构建、定向合同/迁移基线检查通过；完整合同检查首轮发现两处声明/基线差异，修正后定向复验通过。

运行：r37已将30471 API/Worker前滚至schema71；此前Web为r36b。backend-runtime.json以实际回收为准。r34/30472的API/Worker/schema不随本轮升级。现有r24 admin/member角色/密码/MFA未修改，Task operator保持原A范围。

新增任务专属VictoriaLogs（复用本机已有镜像）绑定127.0.0.1:19428/TLS/Basic/256MiB/0.5CPU，存储仅 /data/ops-v12-native-visual-r24-20261026/r37/victoria-data。凭据只在远程0700/0600、Task Bao及只读Secret挂载。导入186条真实ops-seaweedfs-0 Pod日志；固定UID/30分钟窗口；来源注册201+密码/TOTP绑定写请求。实际查询核对总体186/样例21以及tenant/ns/UID负例。**这是受限原生日志快照，不是持续采集或整个集群覆盖。** Kubernetes原采集仍持续自动轮换。

当前接续：完成实际API success/empty/权限/来源故障/取消/恢复，回收证据后部署/核对日志正式浏览器。首次准备在TLS尚未就绪时失败，未导入；resume先验证空库再导入，未重复数据。旧文件/镜像/容器保留。

恢复：只前滚。迁移71无Down，不删除历史记录/审计，不恢复旧身份或整库。R37 Worker识别resourceUids资格，旧Worker会忽略新增约束，不得带此配置盲目降级。保留新查询记录和撤权状态，故障先修正兼容运行版本。


## 当前安全接续点（用户转问全页面账号时）

30471 API仍r37（8683c025…），Worker已前滚r37g（e82e5cb9…），schema71；Web仍r36b（尚未部署日志工作台新构建，导航仍标记未接入）。日志实际API success（186总体/20样例/partial）、empty（DEBUG测得0/no_data）、越范围403、真实暂停专属来源后的failed与running取消→terminated均通过。所有暂停均finally恢复。真实恢复后的新查询、重启/SQL持久化复查、正式日志浏览器及逐状态视觉尚待完成，不宣称P3整体关闭。

修复与失败：授权子集scope_limited不等于采集故障；原17GVR不含Namespace，已改用现有Kubernetes客户端只读当前注册且已授权namespace的实际Pod元数据/Namespace UID，准入复读具体Pod以拒绝同名重建。只增加任务namespace名单的Namespace get，没有list/watch或全局枚举；chart和合同测试同步。该原生元数据路径独立于图重建，不放宽图采集连续性门禁。空filters持久化省略后为nil已在Intent边界归一为[]，增加合同测试。撤权后Worker在来源I/O之前拒绝，远程真实PG新增回归通过（observation-revocation-before-dispatch.log保留远程r36）。

用户明确要求可查看全部页面的账号：新建v12.fullview，分别经过密码+TOTP绑定请求授予operator（仅原任务cluster+namespaceA）和platform_admin，真实首改密后验证四个已实现页面与双角色菜单；fullview-access.json。密码只保存在远程private/fullview-account.json并直接交给用户，不入仓库/证据。受保护旧admin/member和r31 operator授权/密码/MFA不改；新账号尚未绑定MFA，管理写入仍受再次验证门禁。首次admin grant用{} scope被400拒绝，改为正式要求的空数组scope后201，失败保留远程。

下一步：先做来源恢复成功/SQL结果不变与取消无迟到提交，再部署web工作台和真实浏览器状态；完善P3剩余预置视图/AI精确范围衔接/历史分页/明确过期合同及两集群、视觉。不要重复已通过的Kubernetes凭据失效/第二namespace演练，不调用CodeBuddy，不做长期高负载。


## 2026-10-28 后续增量：正式工作台已部署，P3仍未整体验收

最新运行以本段为准：30471 Web=`ops-v12-native-r37j-web-30471`，index SHA256 `b1c2787f7bc7e4343d77a1d90ba60d1ba6dc35499cc3f132280e7744658d712d`，API r37/Worker r37g/schema71不变；30472未改。web-runtime-r37j.json、source-current.json记录运行与本地脏源码身份。旧r36b/r37h/i容器已停止保留，未提交/推送代码。

- live-recovery.json + recovery-persistence.json：来源恢复后新查询92a32e2e…完成，真实总体186/样例20/partial。SQL核对原结果摘要不变、30fc42ec…仍terminated且没有结果，三个记录绑定同一PodUID/NamespaceUID。
- browser-workbench.json：1440×960远程真实浏览器完成选Pod/资格来源/提交、186与20分别回显、本地筛选保持总体且Enter不重发、脱敏原文、来源说明、个人持久历史恢复和DEBUG测得零空态。查询窗口使用可见秒级边界，较完整来源资格窗略向内收缩。
- browser-workbench-failure.json、browser-workbench-cancel.json：仅docker pause本任务私有VictoriaLogs，真实界面显示加载→失败或加载→服务端终止回执；运行中取消明确sourceStopped=false，不将HTTP中止冒充来源停止。所有故障包装器finally unpause；browser-source-*-resumed.json记录恢复。browser-terminal-persistence.json复查两条记录failed/terminated均无结果，恢复后未晚到发布。
- 实际修复1：datetime-local直接截断微秒使开始早于coverageFrom，提交503；表单开始向上/结束向下取整到秒，包括历史回填；微秒边界回归通过。未放宽资格合同。
- 实际修复2：accepted→running使取消expectedRevision迟到409；取消前读取最新回执，终态不再写取消，仍冲突则刷新并提示核对后重试，不自动重复。回归明确使用新版8而不是界面旧版7，并验证409不重发；最终4项Vitest与正式构建通过。只在本地筛选关键词Enter阻止form提交，活跃查询也不重复提交。
- 失败保留：browser-workbench-precision-failed.json为产品时间窗问题；browser-workbench-r37i-failed.json及*-locator-*/history-list-*为后端已返回成功但测试定位不含子节点文本或AntD空格的脚本错误；browser-workbench-cancel-conflict-failed.json是真实旧revision取消409。修正后重新验证，不覆盖成成功。

截图：严格safe-capture全页探针均报告recovery-code；未放宽/绕过探针，未保存相关新图片。JSON交互断言不代表视觉通过，736张原参考未重采。本轮未完成对这些页面的图像差异验收。

运行查看：[30471日志查询](https://192.168.0.63:30471/observe/logs)，使用先前交付的v12.fullview（或任务operator）登录，监控与观测→日志查询；选择ops-seaweedfs-0及r37-native-pod-log-snapshot来源，使用来源默认绝对窗口。该来源只证明单Pod固定窗口快照，资格24小时/证书2天，失效后必须重新用真实材料核实，不自动放宽或虚构持续覆盖。现有Kubernetes自动轮换采集不受此快照来源的期限替代。

仍须推进：完整SL049组1:1视觉、单窗口选择控件与原型差异、时间桶/字段与查询检查、样例排序/自动换行/相邻条目、AI精确资源/来源/UID/时间窗衔接和预置监控视图、历史分页与明确过期合同、两个真实集群及Worker实际重启时中断路径。仅已有受控PG重启租约机制证据。P3-02/03/04保持in_progress，六维未提升；无长期高负载验证、虚拟化或生产发布。
