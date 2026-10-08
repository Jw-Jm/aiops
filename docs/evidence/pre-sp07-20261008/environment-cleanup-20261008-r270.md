# 本平台已核实无用资源清理（2026-10-08）

用户本次明确要求“清理环境中无用pod以及其他资源”。本记录只说明本次实际清理，不声明原 OrbStack 全清理或 R0～R6 完成交付。

本次移除 59 个 Pod：37 个已完成、失败或被替代的自有测试/辅助 Pod 按 UID/resourceVersion 条件正常删除；旧 R7/R8 的 20 个自有 Deployment/StatefulSet 在确认无 queued/running 调查、无声明的外部运行调用方、PVC whenScaled 为 Retain 后缩容到 0，22 个旧业务 Pod 正常退出。旧控制器和恢复所需副本计划保留。另删除 6 个零副本且无消费者的旧 ReplicaSet（每个 Deployment 保留最新两版）、2 个已删除测试 namespace 的 ClusterRoleBinding、1 个对应孤立 ClusterRole 和 1 个无消费者的非秘密测试标记 ConfigMap。

删除前逐项保存资源状态、事件及有界日志到仓库外私有目录。未强制删除、移除 finalizer 或执行全局 prune。Namespace、PVC、PV、Secret 的 UID 与 PVC/PV spec/claim 绑定在本次全流程前后保持一致。原 27 条缺少匹配 Transit 恢复材料的历史 Evidence 及其数据库、对象和密钥相关存储继续保留。349 个明确标记 protected-archive-fixture、承担历史归档保留义务的 Docker 容器全部保留；独立恢复卷、私有备份、签名 Bundle、信任和模型配置保留。共享或归属不明的 default/holmes/monitoring Pod，以及保留 release 的 Pending Pod 没有凭名称或状态删除。

前后原生 Docker 清单另观察到 5 个匿名卷随所删 Pod 的原生容器生命周期被回收。逐一关联到本次自有只读 PVC 消费者/有限 sleep 目标及精确 container ID：它们是 postgres 镜像声明的 /var/lib/postgresql/data 匿名卷，实际覆盖命令只执行有限 sleep，未运行 postgres/initdb；真实 PVC 挂在 /readiness 等独立位置且完整保留。没有手动删除 Docker volume 或镜像，也不声称所有匿名卷名称均保持不变。此归属链保存在最终核验记录。

当前 R12/R9 交付的 5 个 Deployment 与 5 个 StatefulSet 均保持 Ready。清理主要操作后，用真实 HTTPS OIDC 及正式 API 验证历史成功调查、归档 Evidence 摘要/对象版本、Ledger/Audit 与持久 SSE，命令退出 0；没有新增模型调用或十并发复测。随后两个旧失败辅助 Pod 的删除及最终原生保护/Ready 核验均退出 0。

证据：

- [清理前原生清单](current-orbstack-resource-inventory-r263.json)；[清理后全清单](current-orbstack-resource-inventory-r267.json)，再叠加后续 2 个失败辅助 Pod 的删除结果与最终核验。
- [逐项删除计划](resource-cleanup-plan-r264.json)、[操作及结果](resource-cleanup-result-r264.json)，退出 0。
- [旧交付停用计划](superseded-core-quiesce-plan-r265.json)、[停用结果](superseded-core-quiesce-result-r265.json)，退出 0。
- [最后两个辅助 Pod 计划](failed-helper-cleanup-plan-r269.json)、[删除结果](failed-helper-cleanup-result-r269.json)，退出 0。
- [当前业务实测命令](current-core-r9-investigation-chain-live-command-r266.json)、[业务实测结果](current-core-r9-investigation-chain-live-receipt-r266.json)，退出 0。
- [最终 UID、存储、保留容器及 Ready 核验](resource-cleanup-final-verification-r270.json)，退出 0。

此后新的功能测试目标必须重新创建或发现并记录当前 UID。本次没有进入 SP-07，没有提交、合并或推送尚未完成验收的整体交付。
