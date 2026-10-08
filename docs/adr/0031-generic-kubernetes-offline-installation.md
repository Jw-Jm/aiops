# ADR-0031：通用 Kubernetes 的节点本地离线导入与安装

状态：Accepted，2026-10-08 用户明确授权完成通用 Kubernetes 离线安装，目标不限 OrbStack。此授权扩展此前仅实现 OrbStack 的安装边界；不恢复虚拟化，不进入 SP-07，也不宣称生产 HA、容量或完整 R0～R6 验收通过。

## 决定

复用签名 Bundle、DeploymentProfile/v1 已声明的 `containerd_ctr`、现有 Chart 和正式 dependencies → bootstrap-api → business 初始化入口。支持已有标准 Kubernetes 的同架构 Linux/containerd 节点；精确镜像和平台架构必须与独立验签的物料一致。当前实际制包/验收架构由本轮证据明确，不把 arm64 digest 当作 amd64 物料。

操作者将同一签名包与独立信任带到每个节点，通过本地 `ctr` 的 `k8s.io` store 导入。导入前证明 kube-system UID、Server 版本、节点名称/UID、架构和运行时，并在所选 Unix socket 中查到该节点实际运行的 Kubernetes container ID。随后精确检查引用、target digest、完整内容和解包状态。没有 SSH、`kubectl exec`、任意远程命令、凭据分发或 Registry 服务。

安装可从拥有 kubeconfig 的管理机运行，不需要连接节点 socket。安装器读取所有节点 kubelet image inventory，逐一要求所选精确引用存在，节点集合和 UID 不变，并核对 StorageClass。已预装验证与实际本地导入在 CLI 报告中区分。inventory 缺失、截断或尚未刷新时明确失败，不能改用 tag、联网拉取或跳过节点。所有运行及探测 Pod 继续 `imagePullPolicy: Never`；inventory 是安装前检查，不能替代实际 Pod 启动和业务冒烟。

新节点加入、节点重建或镜像缓存被清理后，必须先重新导入并复核；本轮不实现常驻镜像分发服务。镜像存在性检查后发生的缓存删除不会触发公网回退，只会导致明确启动失败。内部 Registry 仍为未实现，不把该驱动名称当作能力证明。

正式 bootstrap 继续要求独立公共 CA、外部私有输入、实际 Tenant/Source/Policy、数据库职责分离、Forward-only 迁移和受校验业务配置。平台不创建 Kubernetes 控制面、CNI 或 CSI；可执行 NetworkPolicy 的 CNI、持久存储、集群 DNS 和 TokenReview 是目标前置。独立本机标准集群只是本轮测试载体，不成为产品运行依赖。

## 不变边界

公共 Profile/v1 的字段和枚举不变，未新增 API Contract。部署默认不附带租户或宽权限。Root 00～10 只读；ADR-0008 的 KubeVirt/CDI disabled/unverified、PyRCA disabled/excluded、ActionPlan 仅建议继续有效。Ollama 推理与性能验收为用户豁免，不记 PASS。已存在开发安装及受保护历史数据保持原位。

证据与命令：[本轮账本](../evidence/generic-k8s-offline-20261008/task-ledger.json)。
