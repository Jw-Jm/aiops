# SP-07 非虚拟化目标与物料输入（2026-10-08）

本文件只准备下一阶段输入。尚未创建执行 Profile、CommandExecution、Runner 或 Ansible 任务；Task 7.5 未验收，SP-07 未开始。

当前隔离 Kubernetes namespace 为 `ops-pre-sp07-targets-r8-20261007`，UID `a956c347-c7b5-43b1-b621-b0ac50923489`；cluster UID `607d16b7-8684-491b-bca8-be31264b371e`、Node UID `cc6ee959-3391-432f-b413-a5e7f2e64756`。同名调度测试 Pod 的旧 UID `b84fb634-54bf-45f3-9eb0-b1deed2130dc` 为 Pending/Unschedulable，按 UID/resourceVersion 删除后，新 UID `57f7f4c0-1d62-4452-955f-5f1bac26e4a0` 为 Running/Ready。独立预期在变更前记录，见 [预期](native-scheduling-recovery-independent-expected-r182.json)与[最新原生事实](native-scheduling-recovery-current-facts-r182.json)。新目标启动的是有限 sleep 负载，不是 SP-07 执行业务；deadline 到达后必须重新发现其状态，不能把本快照永久当作 Ready。

旧目标的真实调查经过锁定 HolmesGPT、真实模型、标准 MCP、Go Validator、归档 Evidence、Ledger/Audit 和持久 SSE，见 [严格产品链路](current-core-r8-investigation-chain-live-receipt-r160.json)。结果为 unresolved，不是 RCA 准确率通过。旧 Evidence 保留原 UID，不能引用到同名新 UID。[后续正式 Graph 验证](current-native-recovery-graph-history-r188.json)证明新 UID 的 Running/Ready，旧实时实体被明确拒绝，旧归档 Evidence 仍可授权读取。r186 保留了操作脚本对已删除实体误期望 HTTP 200 的失败。该处为 R8 历史观察；当前 R12 来源不可用/恢复见下文。完整 Post-check 留待下一阶段，Ready 是有限观察窗事实。

隔离 Linux 目标为容器 `fb6811638f175ec5cd553c3c1681e57acd61264d8815d9992b63f93602955a8c`，endpoint `192.168.117.2:2222`，属于本任务的 internal Docker network。server 只投影 host key、sshd_config、TrustedUserCAKeys 公钥和各账户 principal 文件；登录私钥与 CA signer 不投影入 server。独立 host key 核对、短期证书续期、过期/退休证书和跨账户拒绝见 [认证记录](ssh-target-server-only-projection-renewal-r127.json)。认证只用 ssh -N，无远程命令。

原生账户配置检查见 [账户边界](ssh-target-native-account-boundaries-r163.json)：root UID 0，opsordinary UID 1000 且不在 sudo 组，opssudo UID 1001 且在 sudo 组，默认 sudo 需要密码。未执行 sudo 或 root 命令。8 小时准备证书会到期，下一阶段须重新发行并核验；专属准备 CA 尚未接入平台 OpenBao SSH CA。

四类正式 ExecutionProfile 的输入情况：

- k8s_namespace：有本任务 namespace、UID 和有限恢复目标；原有 PVC/StorageClass 准备证据保留在 [历史输入](../pre-sp07-20261004/sp07-target-inputs.md)，使用前须重新核对 UID、消费关系及存储边界。未建立 pods/exec 执行授权。
- k8s_cluster：原生 cluster/Node 身份已核对，当前平台已用正式 API 注册该 cluster；未来 cluster 执行身份、权限与 step-up 留待下一阶段。
- ssh_user：普通/sudo 身份、host key、CA/principal 接入条件已有原生证明；未来唯一目标注册、凭据引用、网络范围和平台 CA 需正式绑定。
- ssh_root：独立 root principal 与跨身份拒绝已验证；风险确认、执行授权和 root 命令尚未实施。

下一阶段按根目录 10 的 Task 7.3–7.5 准入 Kubernetes Runner 最小镜像、ansible-runner/ansible-core/SSH 及依赖、mvdan.cc/sh/v3/syntax AST parser。先锁定精确版本、架构和 manifest digest，再补齐原始许可证/notice、SBOM、对应源码、依赖闭包、离线导入和签名。版本尚未裁决，不假设当前 investigator 或目标准备镜像可作执行镜像，不新增 parser 依赖。命令保持原始 UTF-8/摘要，未来 AST 规则不能改写用户命令。

性能为用户豁免，不能记 PASS。虚拟化 disabled/unverified；PyRCA disabled/excluded；无真实物理 BMC/DIMM/NIC 目标的现场能力继续未验证。ActionPlan 仅建议，无执行句柄。


2026-10-08 PVC 实际恢复补充：见 `native-pvc-independent-expected-r195.json`、`current-native-pvc-finding-evidence-api-r196.json` 和 `current-core-r8-pvc-native-recovery-r208.json`。原 UID `47405f82-7803-4829-90e6-32f19d2726f8` 的缺失 StorageClass 产生真实 PVCUnbound/ProvisioningFailed Finding；归档 Evidence `4a2e8b63-1855-5ec1-bf6f-36899e1f622e` 保持原 digest、对象版本及 365 天保留。删除前证实原 PVC 未绑定、没有 PV claim 或 Pod 消费者，使用 UID/RV 条件正常删除，未移除保护 finalizer。相同显示名的新 PVC UID 为 `f1dec859-41f9-40a9-b042-93a222c1d439`，采用现场发现的 local-path StorageClass（UID `23b126cb-6924-476a-be45-3bf087e57c75`），已验证 Bound，PV UID `df7ad6f5-e842-4842-9bc4-f91bfb1b7cdc` 的 claimRef 精确对应新 UID。只读消费者 Pod UID `565df045-8fb0-471c-a15b-67b912f02fea` 在验收时 Ready，sleep 1800 秒、activeDeadlineSeconds 3600；此后状态必须重新发现，不能永久沿用 Ready 结论。新 PVC/PV/backend 保留；旧历史 Evidence 仍授权可读，不绑定新目标。本轮没有执行 Post-check、命令链或未来 Task 7.5。

模型并发裁决：用户于本轮确认，当前本地实例若为单并发则停止十并发模型闭环复测。`local-model-concurrency-user-disposition-r197.json` 记录实际启动配置 `OLLAMA_NUM_PARALLEL=1` 和 runner `n_seq_max=1`，裁决为 USER_WAIVED_NOT_PASS。此前十 Job 运行、主链路读取、Ledger/SSE 与失败记录保留；必要预算原子性、超时、恢复及并发正确性验证继续有效。该裁决不证明正式 RCA 准确率。


2026-10-08 当前 R12 交付补充：新业务 namespace 为 `ops-pre-sp07-core-r9-20261008`，正式 API 注册 tenant `12443ef7-3326-4e87-9b0a-a3444bfd16bc`，Kubernetes Source `01a1196d-82ff-7071-a499-02e0cfa5baaf` revision 1。自有目标 namespace `ops-pre-sp07-targets-r9-20261008` 的 UID 为 `f46dabc1-1ae5-4201-8730-d5e9be19f01e`；cluster/Node UID 仍分别为 `607d16b7-8684-491b-bca8-be31264b371e` / `cc6ee959-3391-432f-b413-a5e7f2e64756`。

独立预期先于场景建立。Pod `pre-sp07-r9-scheduling-r218` 的原 UID `9796e290-ef68-4559-9344-879cee69287e` 真实 Unschedulable，经 Source/Inspection 进入 Finding/Incident，再经过 [当前严格真实调查链路](current-core-r9-investigation-chain-live-receipt-r221.json)，模型结论仍为 unresolved。仅阻断该 Worker 的 Kubernetes 来源出口时，正式 Graph 返回 503 GRAPH_NOT_READY，独立原生读取仍确认原 UID 处于 Unschedulable，主链路 Incident 保持 200；这表示来源不可用，不能判为目标恢复。[精确策略恢复](source-outage-exact-policy-restoration-r224.json)后，[Graph 再次 fresh](current-core-r9-source-recovery-after-restoration-r225.json)。r223 操作脚本未识别 Kubernetes 对空 egress 的规范化而恢复 guard 失败的记录保留；r224 经 UID/原始 spec 核对完成实际恢复。

[当前目标恢复](current-core-r9-scheduling-native-recovery-r232.json)使用 UID/RV 条件正常删除无存储的原 Pod，再创建同显示名、移除不可满足 selector 的自有有限目标。新 UID `0f138d83-f793-499d-84de-d25e0caadf04` 原生 Running/Ready，正式 Graph 为 fresh 且指向新 UID。旧实时实体返回 403；归档 Evidence `406783e1-27a4-5a21-aa60-1adac508f309` 继续关联原 UID，内容 digest、对象版本和 365 天保留未变且仍可授权读取。新 Pod sleep 1800 秒、deadline 3600 秒；不能在时限后沿用本次 Ready 快照。该前后事实可供未来 Post-check 区分仍故障、来源不可用及已恢复，本轮没有建立或运行 Post-check 执行功能。

[当前 Metrics 正向记录](current-core-r9-pod-metric-archive-api-r231.json)将自有 Ready Pod UID `e3ff5534-7981-49ac-beab-81f63e38e63a` 的真实 timestamp/window/Quantity、Node UID/capacity/allocatable 与 Source scope 关联，并通过 Worker 归档及正式 Evidence API 解密读取。此证据不替代完整 Metrics 撤权/故障矩阵，也不宣布高利用率症状通过。

隔离 SSH 目标在本机环境重启后已按原 container ID、internal network、server-only 投影和独立 host key 恢复。[最新认证](ssh-target-server-only-projection-restart-r235.json)记录 endpoint `192.168.117.2:2222`，普通/sudo/root 证书认证成功，普通身份跨账户、过期及退休证书均被拒绝；全部为 ssh -N，不含远程命令。当前 8 小时证书有效窗为 2026-10-08 08:29:01–16:30:01 Asia/Shanghai，下一阶段使用前必须重新发现有效性并按正式渠道签发。准备 CA 尚未接入平台 SSH CA，Task 7.5 仍未验收。

2026-10-08 05:56 UTC 最新有限目标与恢复记录：此前 Ready 快照中的 Pod UID `e3ff5534-7981-49ac-beab-81f63e38e63a` 和 `0f138d83-f793-499d-84de-d25e0caadf04` 已到期失败，经归属、终态、无存储和 UID/RV 核对后正常删除；见 [清理结果](current-owned-terminal-cleanup-result-r262.json)。Namespace/PVC/PV/Secret UID 集合均保持不变，历史 Evidence 继续保留。这两项不能继续作为现时 Ready 目标。

当前同名 Metrics 目标 `pre-sp07-r9-metrics-r255` 从旧 UID `8997e07b-8fab-498b-8baf-8a846f537887` 正常重建为新 UID `42982816-5786-4d0f-aeaf-f51acb318e71`；见 [独立预期与实际重建](current-core-r9-metrics-uid-native-rebuild-r260.json)。新目标 sleep 900 秒、deadline 1200 秒，使用前必须重新发现。正式 Worker 的新指标窗口晚于新容器启动时间，原生 UID、容器 ID、Node UID/capacity/allocatable、Quantity 与 source scope 一致，归档 Evidence `353e7546-cf3b-5d40-87a9-1642698136ac` 经正式 API 读取并校验摘要，见 [新 UID 指标](current-core-r9-pod-metric-archive-api-r261.json)。旧 UID 的 Evidence `ca363a74-9949-55a2-9b00-858b8c176bcd` 保持旧对象版本、摘要和授权读取，不绑定新目标。

[来源授权撤销和恢复](current-core-r9-metrics-source-rbac-revoke-recovery-r256.json)使用同一个实际 Worker projected credential：精确移除本 release 独占 ClusterRole 的 metrics get 规则，Pod/Node Metrics 均返回 403，独立原生 Pod 读取及 Incident 主链路仍返回 200；恢复原规则后指标返回 200 且 timestamp 前进。此项不代表全部 Metrics 反例或 MCP 门禁完成。

当前仍故障的有限目标 `pre-sp07-r9-takeover-r253`，Pod UID `9d07c1d9-b96a-45cf-978d-907aaf42ba23`，使用不存在的 node selector；sleep 1100 秒、deadline 1200 秒，使用前须重新发现。[运行中 Job 接管](current-core-r9-active-takeover-r253.json)在工具 Step 已提交、模型 Step 已预留且运行时精确 SIGKILL 当前 Worker/investigator 容器，随后 fencing epoch 到 3，成功工具 Step 未重跑，1 个未知调用按原预留保守结算，终态 partial、预留归零。13 个持久 SSE 事件及杀进程前 cursor 的精确后缀续传通过，22 次 Incident 主链路读取保持 200。本项没有证明从隔离备份恢复后的旧 Context 拒绝，不代替完整安装 C。

[最新 SSH 认证](ssh-target-server-only-projection-restart-r259.json)再次核对原 container/network/host key/server-only 投影，普通/sudo/root 认证通过，跨账户、过期和退休证书拒绝；全部为 ssh -N。当前准备证书仍为 2026-10-08 08:29:01–16:30:01 Asia/Shanghai，有效期后须按正式渠道重新签发和核验。没有执行命令链、Runner、Ansible 或平台 SSH CA 接入，Task 7.5 未验收。

2026-10-08 本次环境清理后的有效状态：用户要求清理无用 Pod 及其他资源。上述 R6/R8/R9 的有限调度、Metrics 和只读 PVC 消费者 Pod 已按实际 UID/RV 正常删除，旧 Pod UID 不再是现时可用目标。R7/R8 业务交付已被当前 R12/R9 替代，确认无活动调查或声明的外部运行调用方后，20 个控制器缩容到 0，22 个旧业务 Pod 正常退出；数据、Transit/PKI key、Secret、PVC/PV 和 release 保留。R8 新 PVC UID `f1dec859-41f9-40a9-b042-93a222c1d439` 与 PV UID `df7ad6f5-e842-4842-9bc4-f91bfb1b7cdc` 继续保留，现已没有此前的只读测试消费者。后续功能验收需要新目标时，必须重新发现或创建独立目标并记录新 UID、来源 scope 和观察窗，不能沿用本文件的旧 Ready 快照。隔离 SSH 目标未在此次删除范围内。见 [最终清理核验](resource-cleanup-final-verification-r270.json)、[旧交付正常停用与恢复副本计划](superseded-core-quiesce-plan-r265.json)及[清理后的当前业务检查](current-core-r9-investigation-chain-live-receipt-r266.json)。SP-07、执行功能及 Task 7.5 仍未实施或验收。
