# 本轮 SP-07 输入准备快照

2026-10-07。本文件记录可复核的接入输入，不表示 R6 全部完成，也不启动 SP-07。四类名称来自根目录正式 Task 7.3；执行 Profile、Runner、Ansible、风险确认和命令链留在下一阶段。

`k8s_namespace`：自有命名空间 `ops-pre-sp07-targets-r6-20261006`，UID `17d739b2-a1f5-4d44-86a3-9a7310c457ce`，保留 ingress/egress 默认拒绝。实际 SourceRegistration 的 namespace 范围来自正式 API 注册。新 PVC UID `6e084407-055f-41e2-8171-f81f2f9e3534` 已 Bound，PV UID `c495fa45-189a-4e57-af6d-8ca981c30825`，本轮发现的 StorageClass `local-path` UID `23b126cb-6924-476a-be45-3bf087e57c75`；只读有限消费者 UID `030a2f8c-a732-424c-abef-e52206c57399` 在记录观察时 Ready。Pod 的 600 秒期限不能当作永久健康。实际后端为 PV `spec.local.path` 和 Node affinity，见 [原始恢复证明](current-core-r6-pvc-native-recovery-r3.json)。验证脚本曾错误预期 `hostPath`，失败保留于 r2；复核读取实际 `local` 后端，未更改产品断言或存储配置。

PVC 独立预期先于创建记录在 [预期](current-core-r6-native-pvc-independent-expectations-r1.json)。原 UID `f882cae4-1681-4697-92e6-5326690d6d20` 的真实 `PVCUnbound` Finding、Incident 和已归档 Evidence 经正式 API 读取。删除前验证 Pending、无 PV 和无消费者，正常 UID/resourceVersion 前置条件删除；未移除 finalizer。重建后的新 UID 不能被描述为原 UID 恢复。原 Evidence `b0058029-e3e9-527f-b0db-16a363b12cc3` 的 digest、版本和保留期在新对象绑定后仍可读取。最新 Graph 生命周期、恢复症状、来源不可用与模型 Post-check 的完整门禁尚未通过。

`k8s_cluster`：真实 Cluster UID `607d16b7-8684-491b-bca8-be31264b371e`、Node UID `cc6ee959-3391-432f-b413-a5e7f2e64756`。本轮未生成 cluster 执行凭据或授予 `pods/exec`。Metrics 真实 Node/Pod 时间窗、Quantity、容器身份和 capacity/allocatable 进入正式 Worker/归档/API，见 [Pod UID 重建证据](current-core-r6-pod-metric-archive-api-r3.json)。[原生 403 控制](current-core-r6-metrics-native-403-control-r1.json)只证明独立无 RBAC 身份拒绝和正向读取，不替代正式 Source 撤权/恢复。有限负载没有达到正式 90% 阈值，相关高利用率症状门禁仍失败；性能为用户豁免，不能写 PASS。

`ssh_user` 与 `ssh_root`：用户授权的专属 Linux 准备目标 container ID `fb6811638f175ec5cd553c3c1681e57acd61264d8815d9992b63f93602955a8c`，独立发现内网端点 `192.168.117.2:2222`。仅 server projection 挂载；CA signer、客户端私钥和密码不进入目标。普通 `opsordinary` 无 sudo 组，`opssudo` 保留需密码 sudo 边界，root 单独 principal；`ssh -N` 验证普通/root/sudo 认证和跨 principal、过期及旧 CA 拒绝，不执行远程命令。host key 独立比对、TrustedUserCAKeys 和 principal 配置见 [当前认证与轮换](ssh-target-server-only-projection-renewal-r4.json)。证书有效期 8 小时；后续接入必须重新读取有效性和独立身份，不能按地址自动绑定。修复前私钥挂载缺陷与失败证据保留。该准备 CA 尚未接入正式平台 SSH CA；未来 Task 7.5 未验收。

下一阶段物料计划继续沿用 [初始输入计划](../pre-sp07-20261004/sp07-target-inputs.md)：分别锁定 Kubernetes Runner 的最小工具镜像、Ansible Runner/ansible-core/SSH/Python 闭包，以及正式指定 `mvdan.cc/sh/v3/syntax` AST parser 的精确模块版本。首次执行前取得各自架构 manifest、SBOM、原始许可证/notice、对应源码和签名离线材料，并按四类 Profile 限定权限、目标、网络、超时和输出。当前 investigator/SSH 准备镜像不能替代执行镜像准入；本轮未增加这些依赖或服务。

真实挂载故障与恢复、原生指标和正式 Finding 分别见 [挂载观察](current-core-r6-real-mount-metric-native-fault-and-restoration-r1.json)和 [Job 原始 Ledger](current-core-r6-real-mount-job-ledger-observation-r2.json)。对应模型调查失败并保守结算未知用量，不能写建议可用性 PASS。ActionPlan 仅建议，没有执行句柄。KubeVirt/CDI、VM 保持延期 disabled/unverified，PyRCA disabled/excluded，物理 BMC/DIMM/NIC 无现场证据仍 unverified。
