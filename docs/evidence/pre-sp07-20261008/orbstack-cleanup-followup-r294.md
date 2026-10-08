# OrbStack 补充清理（2026-10-08）

用户反馈 OrbStack 仍不干净后重新盘点，确认此前属于部分清理。本次没有把原 OrbStack 全清理记为完成。

本次额外停用四套已被当前交付替代的自有环境：`ops-system`、`ops-pre-sp07-core-20261005`（保留 Metrics-server）、`ops-pre-sp07-oidc-20261005`、`ops-pre-sp07-pki-20261004`。核对实际 Namespace/控制器 UID、平台标签/Helm 归属、PVC 保留策略、外部配置中的 namespace/Service IP 调用关系；旧 core 与 OIDC 库无 queued/running 调查，ops-system 库尚无 investigation.jobs 表。三个被停用的 OpenBao 均使用持久 Raft 存储。未发现已有 kubectl port-forward。原始配置、日志和进程清单放在仓库外受控目录，未公开 Secret 内容。

18 个 Deployment/StatefulSet 通过 UID、resourceVersion、旧副本数条件逐项缩容到 0，18 个旧 Pod 正常退出。没有强制删除或移除 finalizer。没有删除 Namespace、PVC、PV、Secret、ConfigMap、Service、APIService、Helm release、Docker volume 或镜像；上述受保护对象身份及 PVC/PV/APIService spec 前后完全一致。原控制器、安装配置、密钥和数据保留；恢复时先核对计划中的 Namespace/控制器 UID，再按计划恢复副本数。

当前交付 `ops-pre-sp07-core-r9-20261008` 的 5 个 Deployment、5 个 StatefulSet 均 Ready，承载 11 个 Pod。旧 core 仍有 1 个 Metrics-server Pod，它提供集群的正式 `v1beta1.metrics.k8s.io` APIService，因此继续保留；网络组件也保留。真实 Metrics API Node timestamp/window 读取通过。通过真实 HTTPS Keycloak 身份及正式 API 复核现有成功调查、Evidence 摘要/版本、Ledger/Audit 和持久 SSE，退出 0；本次没有新模型请求，Ollama 用户豁免保持有效。

补充清理后仍有 38 个 Kubernetes Pod，其中当前平台 11、Metrics-server 1、平台网络 1、kube-system 2，其他 default/holmes/monitoring 共 23 个的归属/共享关系未确认，未凭名称或终止状态删除。该时点 Docker 共 556 个容器，其中 379 个明确标记 `protected-archive-fixture` 且已停止；它们承载历史归档保留义务，容器身份和数据继续保留。其余容器包含 Kubernetes 托管容器、保留的回归/恢复依赖和历史对象；本次未据“已停止”推断可删除。一个已授权准备的隔离 Linux SSH 目标继续运行，保留其接入准备用途。

原 27 条缺少匹配 Transit 历史密钥的 Evidence 及其底层数据继续保护；已有备份或本次停用均不构成删除授权。共享/归属不明对象和受保护存储仍是原环境全清理的未完成边界。未进入 SP-07。

证据：

- [逐项停用计划](legacy-workload-quiesce-plan-r291.json)、[实际操作](legacy-workload-quiesce-operations-r291.json)、[退出 0 的保护与运行核验](legacy-workload-quiesce-result-r291.json)。
- [正式 API 复核命令](current-core-r9-investigation-chain-live-command-r292.json)、[真实业务复核结果](current-core-r9-investigation-chain-live-receipt-r292.json)，命令退出 0；HTTPS OIDC 门禁同样退出 0。
- [补充清理后 Pod/容器分类清单](orbstack-remaining-resources-r293.json)。容器清单包含 kubelet 原生回收变化，快照数量不代表手动删除 Docker 容器的数量。
- [非 Kubernetes Docker 保护核验](legacy-cleanup-docker-protection-verification-r296.json)：457 个容器的 ID、挂载和状态均保留，包含全部 379 个受保护归档容器。首次依赖挂载数组顺序的比较退出 1；复核确认 81 个差异仅为 Docker inspect 的数组排序，按完整挂载字典无序比较退出 0，未忽略挂载字段。

此前 r264～r270 清理移除 59 个 Pod 和 10 个其他对象；本次额外移除 18 个旧 Pod。两次清理合计 77 个 Pod，仍不声明 OrbStack 已清空。

独立只读审核者 `/root/functional_readonly_review` 对本次完整增量清理复核明确 PASS，未发现必须修复问题；[审核报告](legacy-cleanup-independent-review-r295.md)。该结论不替代完整 R0～R6 交付验收。
