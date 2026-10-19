# 场景处置后验证与人工回退目标

日期：2026-10-19。范围为工作负载、网络后端、存储及人工回退的功能开发；没有部署或远程运行验收。

## 场景判据

- Pod：固定目标 UID，Running、Ready、全部业务容器运行且就绪、初始化容器完成或常驻边车就绪。独立 Pod 或能核对 ReplicaSet→Deployment 归属的 Pod；无法核对的其他控制器保持 inconclusive。按名称重建的 Pod 不接替原 UID；需要跟随重建时使用稳定 UID 的 Deployment 作为处置目标。
- Deployment：同一 UID，observedGeneration 已追上 generation、期望副本大于零，全部副本计数符合期望、Available、没有 Progressing=False；实际 Pod 经 Deployment→ReplicaSet→Pod 控制器链归属、镜像与模板匹配且健康。缩容到零不是可用性证明。
- Service/EndpointSlice：Service UID 与 Slice 控制器归属、标签、端口/协议、后端 Pod UID、地址与当前 Pod IP 对齐；端点 Ready、非终止、若有 Serving 则为真，后端 Pod 健康。空端点不判恢复；缺身份、地址或就绪事实保持 inconclusive。这里验证的是服务后端就绪，**没有主动探测或流量证据，不能据此证明 TCP/HTTP 真实连通、DNS、NetworkPolicy、丢包或时延恢复**；报告和页面明确披露此范围。ExternalName 和无 Pod 身份的手工端点不支持。
- PVC：使用新签名 v2 Recipe。除 Bound 外，核对 PV 的 claim UID/name/namespace 和 CSI driver，存在实际声明并挂载/使用该 PVC 的健康 Pod；CSI 驱动要求附着时，还要求对应 PV、Node、驱动的 VolumeAttachment 已附着且无附着/分离错误。attachRequired=false 可省附着对象；未使用 PVC、非 CSI 或缺授权/事实不作为恢复。此判据不证明应用实际读写或数据完整性。
- 对这些场景，检查目标及关联对象的 Warning Event。仅将执行完成后观测到的 Warning 列为新增异常观测，并阻止报告恢复；不声称异常由该命令引入。历史 Warning 不会被改写为新故障；缺时间的相关 Warning 保持 inconclusive。

## 证据与授权

复用同一原生来源登记、签名 Recipe 激活、Evidence 归档、事件和审计。新增声明在 `recipes/pod-health/v1`、`deployment-health/v1`、`service-endpoints-health/v1`、`pvc-csi-failure/v2`。前三项仅用于后检查，不生成 RCA 根因候选；PVC v1 的历史声明和报告保留，Worker 新采集不再把 v1 的 Bound 判据当成挂载恢复。

采集采用固定 API 路径、原执行 namespace 和最多 40 个归档对象；每个列表最多 40 项，发现 continuation/超界即不完整，原生关联采集遵守 Recipe 3 秒预算。namespace 很大、来源延迟或归档失败会得到 inconclusive，不通过扩容或重跑放宽判据。结束时复读原目标 UID/resourceVersion，拒绝采集过程中目标变化。

每个关联对象单独归档并逐条检查发起人的当前 scope，不借目标权限读取被撤回的子资源。PVC 所需 PV、CSIDriver、VolumeAttachment 仍需主体本身具备相应集群资源读取范围；没有扩大授权。报告接口和 SSE 也逐条重查 Evidence、来源及 Recipe。D0 归档只保留评审过的身份/健康/配置字段，去除 Pod 环境、任意注解、状态消息和卷句柄等内容。

## 人工回退

新增 `rollbackSpecDigest`（sha256），新发起的 rollback 必填；其他执行不得携带。它进入 `execution-request/v3` 本人确认摘要，修改目标摘要必须重新评估和确认。普通请求仍 v1、无回退目标的普通关联仍 v2；历史没有目标的回退只保留旧报告，新检查明确缺目标。

操作者从已核对的历史报告/期望配置选择摘要，界面不自动选择原执行后的配置。摘要为评审投影配置的规范 JSON SHA-256，不包括元数据和状态。Node 覆盖 unschedulable；Pod 覆盖 nodeName、容器镜像/端口/挂载及 PVC 引用；Deployment 覆盖副本数和业务容器镜像/端口/挂载声明；Service 覆盖类型/端口/selector；EndpointSlice 覆盖地址类型、端口、端点地址和目标引用；PVC 覆盖卷/StorageClass/accessModes/申请容量。未列出的配置字段不在本轮回退证明范围。

只有目标摘要匹配、可信健康判据通过且没有执行后新增 Warning，才报告 `rollback_goal_met`。配置不匹配是 `rollback_goal_not_met`，缺配置观测或资格不完整仍 inconclusive；命令成功不会替代这些条件。报告同时展示期望/实际配置摘要、异常观测与引用证据，不自动回退。

API、Worker、Schema 和 Web 必须同批交付；未验证新旧混合运行，没有迁移、签名 Bundle 或安装路径变更。FR-144 仍是部分实现：真实网络连通、应用读写、全部硬件及其他目标并未覆盖。

## 本地验证

`make check-local` 通过；前端 81 个组件测试与 5 个 SSE 协议测试、TypeScript 通过。新增定向用例覆盖归属/UID变化、旧观测/归档、Deployment generation、Service 后端替换、执行后 Warning、PVC 无消费者/未附着/无须附着、归档投影脱敏，以及回退目标缺失/不匹配/新异常。

数据库集成补充回退摘要进入风险绑定、改摘要拒绝复用确认等用例，仅编译，尚未运行 PostgreSQL。`make check-delivery` 的生成物、既有源码材料与交付合同通过；最终 Action/App/RCA/Registry 定向回归、Go 全仓构建和受影响包/Integration 的 vet 通过，开发清单与 Canvas 数据/类型检查通过。本记录不作为生产或真实来源验收。

没有访问远程机器、部署、调优或追加容量/浏览器运行。历史失败、未运行项和人工质量 pending 保留；KubeVirt/CDI、Trace 补全和 PyRCA 边界不变。

后续功能增补：[受控 TCP/HTTP 与专用 PVC 文件写读](controlled-verification-closeout.md) 已加入显式关联探测执行。该增补不追认本记录当时未覆盖的运行验收，也不改变原生判据本身的证明范围。

后续验证（2026-10-19）：源码候选 `1a7c7da` 已在远程专用 PostgreSQL 上执行完整 `TestSP07CommandTransactionAndClaim`，包含关联/冻结报告与受控回执两个子用例，均通过且无跳过；原本轮“仅编译”的说明为前一阶段记录。真实 Kubernetes/TCP/HTTP/PVC/Transit 路径仍未验收，见[候选与实际范围](../evidence/verification-candidate-20261019/README.md)。
