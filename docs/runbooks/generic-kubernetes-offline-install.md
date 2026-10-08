# 标准 Kubernetes/containerd 离线安装

本入口面向已有 Kubernetes 集群，不依赖 OrbStack、kind 或 Docker。采用非虚拟化 core、节点本地 OCI 导入和正式三阶段初始化。当前实际受测版本、架构和 Bundle 身份以[本轮证据](../evidence/generic-k8s-offline-20261008/task-ledger.json)为准；未完成的门禁不能由本文档代替。

## 目标与安装机条件

- 已有可访问的 kubeconfig/context；所有节点为 Ready 的同架构 Linux/containerd，具有稳定名称和 UID。Bundle 必须为相同架构的独立制包，不能将 arm64 包用于 amd64 节点。CRI-O、混合架构、内部 Registry 分发当前明确不支持。
- 集群 DNS、可执行 NetworkPolicy 的 CNI、持久 StorageClass/存储后端以及 ServiceAccount TokenReview 可用。安装器不会创建 Kubernetes 控制面、CNI、CSI，也不接管已有业务依赖。节点/存储/CNI 是目标环境前置，不能因模板存在就记录通过。
- 管理机持有可信 `opsctl`、兼容目标 API 的 `kubectl`、Helm 3，以及经独立信任验签的本地 Bundle/源材料。节点上具有 kubeconfig 的最小读取权限、目标 containerd Unix socket 管理权限及匹配其 snapshotter 的 `ctr`。准备阶段将工具及物料带到目标，安装阶段不下载语言模块、Chart、镜像或证书。
- 私有输入、信任公钥和恢复材料放在仓库及 Bundle 外的受控位置。模型服务为显式内网前置，权重不在包中；缺模型/身份/tenant/policy/source scope 时不能暗中开启宽权限。Ollama 推理和性能用户豁免不等于正式模型能力通过。

生产 HA、auto-unseal 与发行版生产资格仍受既有门禁约束。其他 Kubernetes 上的功能安装可采用 development Profile；改变发行版不自动构成生产资格通过。

## 1. 验签并准备当前目标 Profile

安装机与每个节点均用独立渠道取得的公钥验签，不能信任包内自带的公钥：

```sh
opsctl bundle verify --manifest /media/core/bundle.lock.json \
  --signature /media/core/bundle.lock.sig \
  --payload /media/core/payload.tar.zst --key /trust/bundle-trust.pem
```

安装器可由独立签名的工具 Bundle 更新，core Bundle 的镜像/Chart 保持其原签名与源码绑定。此时分别验签两包，从工具包取得匹配目标架构的 `opsctl` 与当前安装源材料，并核对二进制在该包认证清单中的 SHA256，再用该版本操作 core 包。本轮精确的 core/工具包配对见验收证据；不得只替换未签名的 CLI 或把工具包验证当成 core 业务验证。工具包不包含业务镜像，不改变已有安装 checkpoint 的 core Bundle 身份。

若管理机采用本轮提供的 Darwin/arm64 工具，另核验 `management-tool.json` 的 Ed25519 分离签名 `management-tool.sig`，仍使用独立取得的信任公钥。随后核对该清单的 binaryDigest 与实际 `opsctl-host` SHA256、sourceMaterialDigest 与已验签工具包的源码材料 digest，以及 sourceCommit 的对应关系。该管理机工具不冒充 Linux 节点二进制；未完成这些核验时不得用它安装。

按已验签文件清单展开物料及 `opsctl-source` 对应的安装源材料。`INSTALLER_SOURCE` 表示该解包目录，包含 Catalog、模板和 forward-only migrations；它不需要 `.git`、Git、Go、Python 包管理器或在线模块仓库。运行 OpenBao 初始化时在该源材料根目录中执行，以维持明确的私有恢复材料排除边界。

从签名材料提供的 Catalog/模板发现真实目标；模板可以先由操作者明确配置实际 namespace、端点和组件模式，不可直接作为 resolved Profile 安装：

```sh
opsctl profile detect --context target-k8s \
  --catalog /materials/installer-source/bundle/component-catalog.yaml \
  -f /secure/operator-template.yaml -o /secure/detected.yaml
opsctl profile resolve --catalog /materials/installer-source/bundle/component-catalog.yaml \
  -f /secure/detected.yaml -o /secure/resolved.yaml
```

无 `-f` 时，非 OrbStack 目标选择 `kubernetes-containerd.yaml` 模板；这是安装输入起点，模型占位端点必须显式替换。resolve 检查实际 kube-system UID、Server 版本、节点运行时和 StorageClass；`imageImporter` 必须为 `containerd_ctr`。不通过修改 `installable`、candidate 状态或版本锁绕过准入。

所有 bundled Service endpoint 与业务 values 必须使用本次选择的 namespace，来源范围中的 cluster/Pod/Node UID 必须重新发现。操作者可在模板的 `kubernetes.storageClass` 明确选择非默认类；未指定时使用发现的默认类，无默认且未指定则明确失败。resolve 核对该类实际存在及 provisioner，安装器会把它写入 PostgreSQL/OpenBao/SeaweedFS 和 Victoria 持久卷模板，不依赖其他环境的默认类。

## 2. 在每个节点执行本地导入

先通过管理机读取节点名称、UID、runtime/架构，并由节点管理员确认本地 CRI socket 和 snapshotter。将同一包、resolved Profile、独立公钥及锁定 Linux 安装工具带至每个节点。示例中的名称、UID、socket、snapshotter 必须替换为该节点的实际值：

```sh
sudo opsctl bundle import --profile /secure/resolved.yaml \
  --bundle /media/core --key /trust/bundle-trust.pem \
  --node-name actual-node --node-uid ACTUAL_NODE_UID \
  --containerd-address /run/containerd/containerd.sock --snapshotter overlayfs
```

命令先完整验签及验证 OCI 闭包，然后证明所选 socket 见到该节点当前运行的 Kubernetes container ID，才执行 `ctr --namespace k8s.io images import`。精确仓库引用、target digest、完整内容及解包状态均需通过。不会调用 SSH、`kubectl exec` 或 pull，不自动从其他运行时复制镜像，也不接受只有 tag 的替代。

多节点逐个执行，包含控制面节点。等待 kubelet image inventory 刷新后开始安装；若默认 inventory 上限截断了所需引用，应由集群管理员调整 kubelet `nodeStatusMaxImages` 并重新检查，不能关闭安装检查。所有节点都必须包含选定镜像，不以某个节点成功代替其他节点。

## 3. 正式 bootstrap 与 dependencies 阶段

准备外部 `environment.json`、依赖 Secret、独立公共 CA、Archive IAM 和完整 `installation-business-values/v1`。它们的字段、输入检查和详细顺序沿用[当前初始化 Runbook](pre-sp07-current-bootstrap.md)，不使用测试 seed 或业务表直写。

```sh
opsctl bootstrap environment --config /secure/environment.json \
  --secrets-file /secure/dependency-secrets.json
opsctl bootstrap victoria-trust --profile /secure/resolved.yaml \
  --business-values /secure/business-values.json --secrets-file /secure/victoria-private.json
opsctl install --profile core --resolved /secure/resolved.yaml \
  --bundle /media/core --key /trust/bundle-trust.pem \
  --business-values /secure/business-values.json --stage dependencies --offline
```

安装机仅需 Kubernetes/Helm 访问，不需节点 socket。报告 `imageOperation=verify-preloaded-all-nodes-no-pull`，`verified` 列出检查的物料，`verifiedNodes` 记录节点名称/UID，`imported` 不冒充管理机实际执行了导入。任一节点缺少精确引用、UID/集合变化、未 Ready、架构或 runtime 不符时在部署前失败。后续 Pod 仍采用 `imagePullPolicy: Never`，缓存丢失也不回退访问 registry。

安装器先应用精确策略并验证内部 DNS 正向/公网拒绝，再启动签名依赖。CNI 不执行策略会明确失败。fresh dependencies 拒绝接管已有 release；失败保留已创建资源和数据，按错误 checkpoint 修复，不自动删除 namespace/PVC。

## 4. 初始化身份、数据库、归档与业务

按当前正式流程完成下列顺序，所有密钥及实际口令均在外部私有文件：

1. OpenBao `init → unseal → configure --investigation-signing`；保存原始恢复材料，在安装 Profile 的独立观测副本上 configure，安装用 Profile 不变。
2. bootstrap 身份完成数据库角色初始迁移，正式 `bootstrap database-logins` 创建独立 migration/API/Worker LOGIN，受限 migration 身份完成其余 forward-only 迁移。
3. 正式 `bootstrap archive-iam/archive-bucket` 建立四职责凭据、TLS、版本化 COMPLIANCE 365 天归档；正式 `bootstrap oidc-realm` 配置实际 HTTPS 身份和真实 OTP/LoA-2。
4. 注册实际 workload identity、公共 CA、Context 签名和 source credentials 的最小 Secret 投影，通过 `bootstrap graph-leases` 创建本次 Graph Lease。
5. `install --stage bootstrap-api` 启动初始化 API；通过正式 `bootstrap first-tenant` 及 API/Registry 发布激活 Tenant、Cluster、Source、RoleBinding、签名 Policy/Recipe/Tool，并取得实际来源 scope 证明。
6. 新鲜 LoA-2 bearer 与正式 source IDs 输入 `install --stage business --registration-token-file ... --offline`，启动两个 Worker 和锁定 investigator。调查网络只允许确切内网模型及标准 MCP；禁止 investigator 直连数据库/事实源。

两次业务阶段使用同一签名 Bundle、Profile、namespace/installation UID 和业务配置，checkpoint 不能手工改写。模型可用性检查只访问固定 `/models`，不会替代实模型调查或用户豁免的验收。

## 5. 必要验收和维护

必要冒烟包含真实身份、Tenant/Source、原生资源/Graph、Finding/Evidence、归档 digest/version、角色撤权拒绝与恢复，以及正式 Job 创建/取消、Ledger/Audit、持久 SSE/cursor。记录当前 runtime digest、节点/namespace UID、命令、退出码和失败修复；Pod Ready 不替代业务通过。模型与性能豁免如实单列。

新节点、节点重建、runtime/snapshotter 更换或缓存清理后，先按新 UID 重新执行本地导入并复核，禁止把旧 UID 的证明自动绑定新节点。OpenBao 重启 sealed 时使用该安装原恢复材料解封，不能重新 init。保留、Legal Hold、历史 Transit key 与原对象版本约束继续有效。

安装器不创建 SP-07 CommandExecution/Runner，也不部署 DeepFlow、KubeVirt/CDI 或 PyRCA。后续 addon 及生产门禁分别按其授权和准入完成。
