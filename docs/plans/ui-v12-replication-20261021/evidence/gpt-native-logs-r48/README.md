# r48：实际 Kubernetes 原生日志来源

用户明确要求接通实际日志、将来源改为实际来源。当前30471已使用已登记的Kubernetes来源，按需读取所选Pod的当前单个运行容器`pods/log`；不再将r37手工导入快照配置为当前查询来源。**这是实际原生日志读取，不是持续转发/集中归档采集。**旧快照容器、存储、来源登记及不可变结果保留，未续期、复制或填造新日志。

## 已接通路径

- 既有原生会话/运维授权、当前cluster/canonical/Namespace UID、签名mTLS Worker、独立持久查询/租约/取消/历史复用。没有AI会话或新Agent，也未扩大用户授权。
- `SP04Cluster.PodLogs`显式启用，默认关闭。复用已登记的Kubernetes来源`01a17c3c-ef66-70cf-9ff1-cad25a8bee34`，revision3/credentialRevision1、原CA/自动投射身份及原namespace边界。新Role只在任务namespace给Worker `get pods/log`；不提供exec、Secret或写动词。已有其他namespace的底层只读授权没有修改，应用仍按当前用户精确资源范围收窄。
- 真正读取Pod元数据及零行logs权限探针后才提供候选；只准入当前一个运行容器，冻结PodUID/NamespaceUID/容器名、ContainerID、restartCount、启动时间和来源版本形成稳定指纹。只改变墙钟时间不会改变来源指纹；更换Pod或容器会拒绝旧绑定。
- 点击查询直接调用Kubernetes日志API：明确container、timestamps、sinceTime，64KiB来源读取上限、8秒总查询预算及现有取消上下文。按固定半开窗口过滤，复用evidence.Redact，再取最新最多20条脱敏样例；读取前后核对同一容器，身份变化/权限失败/格式错误/越预算不发布结果。未请求follow或previous，不导入VictoriaLogs，不造测试日志。
- Kubernetes当前日志缓冲不能证明历史轮转覆盖或总体数量，结果始终`partial`、`population.basis=not_measured`且不携带count；空样例也不变成`no_data`。总体未测量与样例数分别显示。字节/样例等截断不声称完整来源。`collectorVersion=kubernetes-pod-logs/v1`为原生读取协议版本，不表示部署了持续采集DaemonSet。
- “刷新来源”清旧来源/窗口并重新读取实际候选，用户重新选择后取得较新的绝对窗口；不自动重发查询、不使旧结果随实时数据变化。历史/结果仍重新核对当前资源、容器、来源版本、凭据和权限。原生日志源指纹变化后旧结果会拒绝读取，不能把新同名容器当旧来源。

迁移72只前滚更新原`observation_lock_source`：在原tenant setting、source/cluster锁、active、revision/credential、cluster/namespace约束内允许当前Kubernetes来源。Kubernetes原生collection不支持的非空nativeTenant映射仍拒绝，不绕过原范围合同。CREATE OR REPLACE保留原owner/grant；未创建新表、未重写旧记录。原Victoria来源合同保留，运行中已退出配置的来源由Service在历史验证时提前拒绝，避免对另一实际来源重复探测。

## 验证

本地30项定向UI用例和最终构建通过（既有chunk大小提示保留）。Native专项、既有观测/结果边界及Chart合同通过；新增覆盖明确启用、同名Pod/容器更换、读取中替换、logs权限拒绝、精确namespace、半开窗口、脱敏、未知总体、空样例、字节末行、退役来源无跨源探测。Chart证明默认不授予logs权限，启用后只在指定namespace授予get。远程独立PG的原生来源授权和完整既有查询生命周期通过，含幂等/取消/晚到/过期/撤权及错误nativeTenant/凭据版本拒绝；属于机制fixture，不冒充实际采集材料。受保留规则保护的fixture数据库未强行删除。

真实Chrome与operator A在实际来源上只创建两条查询：

- `9708b6c4-033e-42cb-afca-bc0c8b51db23`，15:26:19(+08)截止的30分钟窗口，返回20条脱敏样例。
- 刷新实际来源后`372c6a92-aab7-459d-ba1e-470edd693b4e`，15:26:44(+08)截止的新窗口，返回20条，其中3条自然产生在第一窗口截止之后。没有向应用或日志服务插入测试消息。
- 两条均unknown population/partial，不显示伪造总体。来源下拉只有实际Kubernetes来源，没有snapshot。刷新后第一条结果摘要不变；重新登录/页面重载后从服务端历史恢复第二条，跨集群canonical真实403。
- READ ONLY PG核对source类型/revision/credential、schema72、两条completed记录/窗口/结果摘要；原r41/r42快照查询摘要仍为14f0d1e…未改。独立kubectl读取实际Pod日志，按纳秒时间与脱敏内容核对全部40条返回样例，并核对实际PodUID/NamespaceUID/容器。
- API/Worker从r48到r48b重建后原生结果仍能按原摘要读取；不是全栈/双集群重启验收。来源配置中0个旧snapshot，模型仍仅data_query，SP05未启用，独立30472仍运行。无其他业务写入、无模型调用、无页面异常。

首次两次查询发生在r48；历史503修复后在最终r48b恢复历史并执行独立oracle。`backend-runtime-recovered.json`记录前一阶段，`backend-runtime-r48b.json`为当前后端，`web-runtime.json`为最终静态Web。不要把不同阶段合并成单一运行制品已覆盖所有分支。

## 真实故障与修复

部署中任务Bao的原CRL于2026-10-29 15:18:07(+08)到期，API/Worker在启动时拒绝`OPENBAO_PKI_CRL_STALE_OR_INVALID`。通过同一任务Bao的正常CRL轮换恢复，签名验证通过；启用OpenBao原生auto_rebuild，原72小时expiry保持不变，未关闭CRL/TLS检查、未更换信任根或删除撤销记录。`crl-repair.json`保留前后有效期与配置；依据[OpenBao官方维护说明](https://openbao.org/docs/commands/pki/health-check/)。未修改30472的Bao。

首次历史探测暴露真实503：已退出配置的旧快照记录仍反复探测native来源，消耗Kubernetes共享预算。r48b在来源ID不受当前runtime支持时提前拒绝，保留原授权/来源复检；加入无跨源I/O回归，真实历史重验通过。Kubernetes Lease/路由更新仍有慢请求日志，未声称永久消除全部间歇性503。

其他失败原样保留：macOS archive的AppleDouble迁移文件、fixture错误allowedSchemas、Chart新增flag未登记schema、Go Cmd重复使用/TS测试冗余exact参数、Select加载等待和历史按钮/文本定位。产品错误修复和脚本错误分别记录。浏览器首次已成功创建两条查询；后续只恢复历史验证，未重复创建更多查询来掩盖失败。

## 运行/使用/恢复

入口：[30471日志查询](https://192.168.0.63:30471/observe/logs)。刷新页面，选已授权的运行Pod，数据来源选“Kubernetes 原生日志（当前容器）”，选择窗口再查询；需要较新日志时点“刷新来源”并重新选择。来源读取/结果样例有界，用户不应期待无限流式滚动或全部日志下载。

实际环境：mssc-direct/192.168.0.63，task namespace `ops-native-v12-r24-20261026`、schema72、r48b API/Worker+r48 Web；源码/制品/PodUID和分层证据见source.json、运行JSON、browser-native-logs.json与native-log-oracle.json。原r47/r48二进制和r47静态Web保留；恢复采用兼容schema72/原生身份的前滚，不恢复旧凭据、整库或修改不可变结果。部署脚本是一次性执行记录，不应盲重跑。

未验证持续归档采集、轮转前/前次容器/多容器或已结束容器、两个真实集群、Native live取消/故障注入全组合、完整视觉及长期负载；没有把旧快照续期当持续来源通过。P3六维/整个V1.2不提升，虚拟化/Trace/生产发行不恢复；没有提交或推送。
