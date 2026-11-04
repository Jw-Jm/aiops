# r54 告警、故障处理、命令配置接通

2026-11-04，用户明确要求打通三项当前功能，长期稳定性不在本轮范围。基线 `3017b05`，仅 `mssc-direct` / 192.168.0.63 的独立任务环境；30471 已实际部署 API r54、Worker r54c、Web r54b。schema72不变，没有新增SQL迁移，没有修改正式00～10文档或恢复虚拟化/Trace。

## 结果

- [故障告警](https://192.168.0.63:30471/events/alerts)：实际原生异常经采集、Evidence 与 finding/incident 持久链路生成非空列表，接口从409变为200。
- [故障处理](https://192.168.0.63:30471/events/incidents)：增加处理说明及“确认受理、开始处置、恢复后关闭、刷新故障”。真实案例完成 acknowledged → mitigating → 原生恢复 resolved → closed，刷新后状态保存；时间线、告警、资源和当前诊断图200。旧 revision409、外集群403。
- [命令配置](https://192.168.0.63:30471/settings/execution)：接口从404变为200；真实密码+TOTP、公共签名、精确准入白名单验证后发布201，刷新后可回读。配置管理不启动执行 Worker/runner，执行接口409 CAPABILITY_DISABLED，能力页仍声明执行 disabled。

当前共用账号仍按显式 platform_admin＋任务namespace operator授权；没有扩大账号范围。

## 必要修复与部署

1. SP05代码存在而运行文件未启用；Kubernetes来源只允许 finding-envelope/v1，采集器使用v2。通过原生管理员接口、密码/TOTP及expectedRevision3，把来源更新至revision4，保留原v1并增加v2；namespace、cluster、backend、凭据均不扩张。API/Worker运行声明同步revision4。
2. SP07新增受信部署选项 configurationOnly。复用原签名发布、准入、当前角色与MFA流程；全执行模式原校验保持。该模式不需要执行凭据/监听器，Worker在启动执行服务前返回；所有非profile管理请求在使用执行Service之前拒绝。没有新建执行Job或运行真实命令。
3. 真实首轮证据归档失败：任务SeaweedFS的12个128MB卷已满，宿主/data仍有约1.5TB空闲。仅把该StatefulSet卷数配额增至32，保留原image、目录、所有卷文件、身份及加密。重启并选主后，四种独立IAM凭据均可列出本租户前缀，桶全局/其他前缀list及其他前缀get均403；归档积压恢复，不通过放宽IAM或跳过TLS解决。
4. 状态操作成功后，等待最新记录回读再解除按钮pending，避免连续提交读取上一版本；新增回归测试。真实首轮旧版本409保留，不自动覆盖并发写。
5. 命名空间故障的RCA原来以整个来源范围收集邻居，namespace operator读快照被正确403。现在仅在来源本已允许该namespace时构建该namespace内快照，排除cluster资源；集群级故障路径不改。真实最新PVC快照只有本namespace节点，读取200；旧超范围不可变快照继续拒绝，不通过扩大账号或删掉授权检查修复。
6. 通过原生MFA管理接口发布并激活仓库冻结的 pvc-csi-failure/v2 和 node-failure/v2 Recipe，仅任务namespace。诊断可能因缺少因果证据返回unresolved；不把可读取误称为根因已确认。

## 真实案例和收尾

创建专属 `ops-r54-unbound` PVC，原生Pending引出PVCUnbound及ProvisioningFailed事件，未调用导入/伪造Finding接口。Kubernetes来源持续采集生成告警及关联Incident。

主案例 `01a1a3e8-9c05-74ad-a1e7-0acaf32b5225`：页面受理200 revision4，开始处置200 revision5；为同一PVC创建精确claimRef的专属1Mi静态PV，实际变为Bound；保留既有五分钟恢复判定，系统resolved revision6；页面关闭200 revision7。时间线含6条原生/人工记录。

伴随的历史控制器事件案例使用新归档的真实Bound资源状态作为证据，经原生人工处理接口resolved/closed，未修改原始Finding事实；仅清理该专属PVC的原生测试事件，事件副本和归档事实保留。健康PVC、PV、只读SA/Role/NetworkPolicy及已发布 `r54-read-pvc` 配置保留，便于查看精确配置；未挂载该PV，也未授予执行权限。其他已有故障不人工更改。

存储Pod重建后以Kubernetes实际新UID复验：四类指标各3点、范围与CSRF守卫通过；原生日志来源返回20条真实样例，个人历史回放通过。

## 验证与证据

- `source-update.json`、`deployment.json`：真实来源更新与实际二进制部署。
- `profile-browser.json`：发布201、刷新回读、执行禁用；`recipe-publication.json` 和 `node-recipe-publication.json`：真实签名Recipe发布与namespace激活。
- `incident-process-refresh-race.json`：真实受理200及首轮409；`incident-process-rca-scope-failed.json`：处置200及原快照403；`incident-close.json`：最终关闭及全部指定只读区域200，过期版本/越权守卫。
- `archive-quota-change.json`、`archive-probe.json`：原目录容量解除和16项真实IAM检查。Python3.14默认strict对旧CA缺少KU扩展的诊断失败另存；最终诊断采用CERT_REQUIRED＋主机名验证的TLS client上下文，与Go验证口径相容，没有关闭证书验证或改平台TLS。
- `probe-recovery.json`、`probe-cleanup.json`、`current-seaweed.json`：真实UID与恢复、清理边界；旧失败和旧Pod事实不删除。
- `browser-metrics.json`、`browser-archive-logs.json`：已有链路回归。
- `runs/`：Go app/httpapi/action/incident/rca通过；前端全量253项及最终6项定向检查通过；TypeScript/Vite构建、OpenAPI/UIV12/来源合同和交付来源门禁通过。
- `final-browser.json`、`final-runtime.json`：最终三页面与接口200，主案例closed，五个任务Pod Ready、两个Web运行，执行/attempt计数均0。能力页全局graphReady仍为false（资源结果有partial/来源退化标记），不宣称全局图完整健康。
- 多轮浏览器定位失败保留：首次未等待初始配置集群选择完成即填入草稿，内容被正常范围切换清空；等待初始读取完成后真实发布通过。首次新Worker未形成图时的空快照也保留，最终非空且不越界。

P4-03/P4-04/P7-06仅更新为in_progress，不提升整套V1.2六维或完整阶段。未验证所有故障类型/所有交互视觉、命令实际执行、完整Bundle、长期稳定性或证书续期；没有启用未实现的独立命令作业入口。没有提交/推送本轮代码。
