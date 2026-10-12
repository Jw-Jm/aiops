# amd64 部署准备与验证（2026-10-09）

2026-10-11 最终统一重装复验完成：正式 R12 包依赖/API/业务阶段全部成功，11 个 Pod Ready；同一命名空间、5 PVC/5 PV 身份和配置保留，原租户/来源/Incident/执行记录及已成功模型结果保留。真实 Qwen 新调查 succeeded，标准 get_incident_context 与两个模型步骤成功；Go 校验后的 unresolved 结果持久化，没有候选或动作建议。两类 Kubernetes Job 退出码 0、后检查 not_resolved，临时集群权限实际 disabled。R9、首次 R12 和最终 R12 三批真实 stdout/stderr、准确断点续传、外国游标拒绝通过。全资源列表保留两个合法空值 Node 标签，fresh、非 partial、无来源降级。

汇总回执：[final-reinstallation-acceptance-r12-result.json](final-reinstallation-acceptance-r12-result.json)。本次最终复验使用已核验缓存，不代表重新冷缓存验收或 RCA 准确率评估。API 和一个 Worker 启动时各发生一次数据库暂不可用重启后恢复；无 BMC 硬件目标依用户决定待完成，KubeVirt/CDI 延期且 disabled，仓库没有可运行前端。以下按当时状态保留的进展与失败不被改写。

2026-10-11 R12 功能实测：四个业务 Pod Ready、干净重装存储/身份保留、新阶段回执及精确运行镜像通过；全资源列表含两个合法空值 Node 标签返回 200，fresh、非 partial、无 degradedSources。两类 Kubernetes 执行及权限撤销、真实 SSE 输出/准确断点续传/R9 历史输出重放通过。真实模型首轮工具与语义调用已成功，原 4096-token 服务配置使第二轮 4728-token 请求失败；保留原回执，远程配置为实际 16384 后，新真实 Pending PVC 调查 succeeded。Go 校验后的 unresolved 结果已持久化，两个模型步骤和一个语义工具步骤均成功；没有候选或动作建议，不代表根因准确率通过。外部模型配置及 runbook 已同步项目。正在按用户要求完成最后一次正式包统一重装复验。

开发流程按用户要求改为：目标机工作副本先修改和测试，同步本地项目；功能跑通后集中正式部署复验。历史失败与不可变包保留。

2026-10-11 R12 进展：签名 Bundle 独立验签、97 文件校验及 11 镜像离线导入通过；导入前三个变更引用为空，共享层缓存保留。R11 五个 release 的正式 cleanup 已完成，存储保留；R12 七个依赖运行、原 OpenBao 恢复材料解封通过。只读核对租户、来源、Incident、历史执行 ID 与清理前完全一致，真实 Pod 指标/日志重新写入和 TLS/错误凭据检查通过。API 安装及完整业务复测进行中。

2026-10-11 R11 最新实测：正式依赖、API、业务阶段与 release 干净重装通过。命名空间和 5 组存储身份/配置保留，新阶段回执绑定 R11，四个业务 Pod Ready。租户、来源、Incident、历史执行 ID 完全保留；两类 Kubernetes 命令真实执行、退出码 0 与后检查通过，临时集群权限撤销。SSE 原始 stdout/stderr、准确断点续传、外国游标拒绝和 R9 输出重放通过。R11 完整 Qwen 调查失败回执保留。

后续发现全资源列表包含合法空值 Node 标签时被 Schema 拒绝，命名空间过滤查询仍 200；Graph/Resource/Contract 回归已复现并修复，保留空标签语义。原模型提示混合首轮工具请求与最终 JSON；实际完整目录对照显示加入可信调查任务和分阶段输出说明后返回标准 get_incident_context。项目提示已修复，8 项原生 Python 回归通过。R12 发布和完整平台复测进行中；对照成功不代表完整调查通过。

2026-10-11 R11 重装进展：签名 Bundle 独立验签及 97 文件校验通过，11 个镜像离线导入通过；导入前仅两个变更引用为空，其余共享缓存保留，不宣称完整冷缓存重验。R9 当前实例的 5 个 release 已通过正式 cleanup 清理，5 个 PVC/PV 身份与配置保留；R11 七个依赖已 Ready，原 OpenBao 解封。只读数据库核对确认租户、来源、Incident、历史执行 ID 与清理前完全一致。API/业务重装和 R11 完整模型调查仍在进行。

2026-10-11 当前进展：新实例 `ops-amd64-final-20261010` 已完成 R9 正式依赖/API/业务安装，API、Investigator、两个 Worker 均 Ready。真实 OIDC OTP/LoA2、首租户、正式来源与签名配置注册、实际 Pod 指标/日志写入通过；两项 SP07 Kubernetes 执行配置正式发布。命名空间级和集群级执行完成真实 Job、退出码 0 与后检查 `not_resolved`；临时集群权限创建及撤销通过，空 scope 数组修复已获得实际 API 验证。对应新回执使用 `final-` 前缀，旧证据保留。

R9 Bundle 独立验签、97 个文件校验和离线导入通过。新环境七个依赖 Ready，37 迁移及重复执行通过，OpenBao、OIDC、运行投影、Graph Lease 和归档桶正式初始化完成。首次业务启动 API/Worker 各发生一次数据库不可用重启后恢复；首次命名空间命令执行成功但因遗漏归档桶初始化，后检查 inconclusive，原始 API 记录保留，补齐初始化后的新执行通过。

R9 真实 Qwen 调查仍失败：上游 Holmes 0.42 仅在工具选择 auto 时转发 tools，命名工具选择被丢弃。本项目已修复为通过官方模型 args 扩展转发首轮标准命名选择，并在调用后恢复参数。五项原生回归通过，包含真实 DefaultLLM 到底层 provider 的传参验证；R11 原生镜像通过实际 Holmes/LiteLLM/Ollama 调用返回 get_incident_context 标准工具调用。该对照不代表完整平台调查通过，R11 签名发布与平台复测仍待完成。

Metrics 发现修复的 Profile 包测试和目标机实际发现/解析通过，签名 R10 安装器物料保留；当前 R9 部署使用已认证 R9 安装器及冻结 Profile。完整 release 干净重装尚待完成；无 BMC 的硬件目标验收保留待完成，KubeVirt/CDI 继续延期。

以下记录按当时状态保留，涉及连接故障和未部署修复的说明已被上述最新进展更新。

2026-10-10 最新状态：R6 部署 Bundle 使用签名验证通过的 R8 安装器完成完整业务阶段，API、Investigator 和两个 Worker 已 Ready；已发布两项 SSH 主机报告及四项执行配置。命名空间级 Kubernetes 命令完成真实 Job 执行、输出获取和后检查，Pending PVC 未解决时后检查正确返回 `not_resolved`。这些运行回执仍位于目标机 `/data/ops-amd64-validation-20261010`，后续需提取到本目录。用户明确没有 BMC，硬件目标及 SSH 硬件事实源端到端验收保留待完成；虚拟化继续延期。

集群级执行的临时 RoleBinding 创建遇到 500：空 scope 数组在解析后变成 JSON null，违反数据库 array 合同；本仓库 `internal/httpapi/tenant_handlers.go` 已修复，完整 `internal/httpapi` 测试通过。真实模型首轮调查失败：Qwen 将工具 JSON 作为普通文本返回，未产生语义工具步骤，Go 最终结果校验拒绝。真实端点强制标准 `tool_choice` 的对照调用通过，因此 `services/investigator/src/investigator/budget.py` 现在要求首轮调用已授权的 `get_incident_context` 工具，后续及恢复调查保留上游选择；缺失该工具在分配预算和调用模型前拒绝。三个回归在实际原生 Investigator Python 环境通过。修复未部署，不将首次失败改为通过。

修复版 R9 的 API、opsctl 已使用 Go 1.27.1、空模块缓存及已核验本地替换源码离线编译，并生成对应 SBOM；来源包及二进制摘要见 `runtime-r9-local-build-result.json`。目标机随后在 SSH identification 阶段主动断开连接，11434 返回空响应。R9 原生镜像构建、签名包部署、模型重试、集群级执行和 release 干净重装仍待完成。以下记录按历史保留，不代表这些门禁已经通过。

2026-10-10 现场进度：以下早期准备记录保留为历史，不代表当前状态。R2/R3 完整 amd64 Bundle 已签名并逐文件校验通过（97 文件）；R2 的 12 个发布镜像已通过独立信任公钥正式离线导入，实际节点 UID 已验证，导入前发布引用/根摘要为空。见 `bundle-native-build-r2-result.json`、`bundle-native-build-r3-result.json`、`preimport-release-cache-r2.json` 和 `bundle-import-r2-result.json`。用户授权使用 `/data`，Bundle 输出与解包临时目录已改用 `/data/ops-amd64-validation-20261010`；不再使用容量不足的 `/tmp` tmpfs。

Metrics addon 两次预安装失败均保留：第一次 Chart schema 仅允许 ARM 摘要，第二次 CLI 固定使用 OrbStack ARM 驱动；两次均未创建 Metrics 工作负载。修复落在本仓库 `deploy/charts/ops-metrics-server/values.schema.json` 与 `cmd/opsctl/metrics_addon.go`，只允许两项已经独立准入的精确镜像，并按 resolved Profile 选择验证预加载镜像的 containerd 驱动。实际 Helm 正反测试及覆盖双架构的 Chart 安全/归属测试、CLI 驱动选择测试已通过。R5 已修复 Chart 的固定 ARM 节点选择器，Metrics 安装成功且原生聚合 NodeMetrics 已返回；见 `metrics-addon-r5-result.json` 和 `metrics-native-node-result.json`。失败的 R4 Metrics release 经 namespace/installation-id/9 项资源 UID/Helm 归属核对后完整清理，TLS/CA 与存储保留；脚本位于 `scripts/cleanup-failed-metrics-addon.py`。核心依赖随后发现 containerd OCI 导出的 digest-only ref.name 被错误优先选用；`internal/bundle/oci.go` 已修复为选择同摘要的完整 containerd image.name，并拒绝冲突注解，回归测试通过。R6 已完成签名与完整校验，并成功安装核心依赖和 bootstrap API。正式数据库的 37 项迁移及重复执行、OpenBao 初始化/解封/配置、真实 HTTPS OIDC PKCE/OTP/LoA2 登录与首租户初始化已通过；正式 API 已注册 Cluster/Source 并发布基础策略、配方和只读工具。完整业务部署与 SP01–SP07 运行验收仍待完成，不能宣称整体验收通过。


2026-10-10 SP07 初始化重试：Graph Lease preflight 未沿用显式初始化的基础配置摘要，导致新增 SP07 后拒绝原 Lease checkpoint。修复位于 `internal/bundle/graph_leases.go`；回归覆盖隐式添加拒绝、显式初始化允许以及基础策略变化拒绝。R7 修复安装器从完整来源闭包离线编译并生成真实 SBOM，完整签名包已构建，使用已验证 R6 安装器及独立公钥验签通过（97 文件）；见 `bundle-native-independent-r7-result.json`。安装器更新后仍操作原 R6 部署 Bundle，不改写依赖/API/Lease checkpoint，不重复导入或替换已初始化存储。R7 重试通过 Lease 校验，但在 plan-charts 拒绝缺少 release 标签的 SP07 NetworkPolicy，未启动新业务工作负载；见 `business-r6-installer-r7-failure.log`。本仓库已补齐 SP07 Chart 所有资源归属标签；安装器采用一致的 platform ownership post-renderer，只补 release 标签，不改镜像/组件/策略/RBAC，并按显式凭据配置允许对应 ServiceAccount/Role/RoleBinding 的精确目标命名空间，已有凭据仍拒绝接管。完整 Bundle/CLI 单元回归及新增策略保留、外国归属/命名空间/资源拒绝测试通过，R8 安装器签名构建及独立验签通过（`bundle-native-build-r8-result.json`、`bundle-native-independent-r8-result.json`），已核对实际 CLI 与认证清单的二进制摘要；继续用原 R6 部署包重试业务阶段。四类实际命令执行、平台真实模型调查与 release 干净重装尚未通过。

目标：192.168.0.63，单节点 Ubuntu amd64/Kubernetes v1.36.3/containerd 2.2.2。用户本次授权所有已开发的 SP-01～SP-07 非虚拟化内容；KubeVirt/CDI 仍延期。模型接口由用户另行提供，没有使用占位服务冒充真实模型。

已完成独立本地持久存储（5 个 10Gi PV，Retain）；正式 environment bootstrap 创建 ops-amd64-core-20261009 及公共信任和 8 个依赖 Secret；私有输入仅存于目标主机。当前 API/Worker/opsctl/Command Runner/db-migrate 及锁定源码 K8sGPT 的 Linux/amd64 构建通过。

已校验物料见 *-receipt.json；原始 OCI 和已完成转存的镜像保存在主机 /opt/ops-amd64-validation-20261009/materials。仅转存并核验本任务产生的文件，没有删除历史物料。镜像未因下载或 SBOM 比对而自动准入。已审核 arm64 包与新 amd64 包的依赖差异见 *-dependency-parity.json，源码/许可证复核尚未完成。PostgreSQL 当前同版本标签不包含原始已准入 arm64 manifest，单独固定新发行物供复核，没有改写旧锁或把两个物料等同。

VictoriaMetrics 的独立 containerd namespace/Docker PoC 已核对版本，并在 network=none、只读根文件系统条件下运行原生 query API 返回 success，见 victoria-metrics-native-query.log；不代表整个平台或离线冷导入/重装验收。生产 k8s.io namespace 尚未执行 core 镜像导入。

当前完整 amd64 Bundle、core release 范围内 NetworkPolicy、正式依赖安装与数据库/身份/归档/租户来源初始化、SP-01～SP-07 业务验收仍未完成。SP-06 模型接口已可用，仍待实际发布网络策略与真实模型调查验证。不得据本目录宣称完整部署或任何阶段整体验收通过。

网络策略控制器已安装且 Ready；network-probe-r1-failure.json 保留首次公网正向对照失败，network-probe-r2-result.json 记录 DNS 正向以及可达公网 IPv4 146.56.39.233:443 在策略前成功、策略后拒绝。仅代表此有限前置探针，不代表 core 实装或 IPv6 验收。postgresql-native-poc-result.json 为新发行物断网/只读根条件下实际 TCP 口令认证 SQL SELECT 1 和版本检查；新发行物的 146 个包与原 Debian 12 物料存在广泛变化，完整来源/许可准入尚未完成。

VictoriaLogs 的原生 JSON stream 写入及精确 stream 查询已在 network=none、只读根条件下通过，见 victoria-logs-native-poc-result.json。该镜像没有 shell，探针改为目标主机进入专属容器网络 namespace 发起请求；未增加镜像依赖或开放宿主机端口。r1/r2 失败记录保留，PoC 容器已清理。安装器现已支持显式 `sp06.modelState: pending` 的初始安装，相关 Bundle/opsctl 检查及三个阶段的 Helm 渲染通过；模型默认值被清空，调查器暂不启动且没有模型出口。更新后的 amd64 工具已转存目标机，但完整 Bundle 尚未具备，不代表正式安装完成。模型随后激活的配置变更与回执流程尚未实现。

本轮补充的 OpenBao 原生单节点 Raft 初始化、解封及 KV 写/读/删除验证通过，见 openbao-native-poc-result.json；测试使用仅隔离容器内部的环回 HTTP，不能代替正式 TLS/PKI 或 HA 验收。r1–r3 失败记录保留，测试容器及私有初始化密钥已清理。K8sGPT 使用只有测试命名空间 Pod get/list 权限的临时账号，识别真实调度器的 Unschedulable 条件；没有模型请求或 Secret 权限。该结果只覆盖 Pod CLI，不代表固定完整 Analyzer 的 cgroup/网络准入；命名空间及临时令牌已清理。

VictoriaMetrics/VictoriaLogs 共享源码包核对了完整 source inventory（245/89 个文件）以及原始准入公告的摘要（124/57 个）；见 *-shared-source-integrity.json。此记录仅证明源码完整性及依赖子集，尚未据此改写架构准入。

## 模型端点与 amd64 迁移工具更新

用户提供的 Ollama 0.40.1 / `qwen2.5:1.5b` 已通过 LAN OpenAI 兼容文本与工具调用协议探测；待实际发布的网络策略与调查流程验证。初次安装将使用真实端点，不再等待模型服务。

amd64 迁移工具已从单独保留的源码离线构建，在隔离 PostgreSQL 中完成 37 个迁移及重复执行验证，新增独立架构准入记录。此证明不代替平台数据库角色、身份和业务初始化验收。

真实 amd64 HolmesGPT 0.42.0 / LiteLLM 调用 `qwen2.5:1.5b` 的文本与工具调用通过，见 investigator-native-model-result.json。独立 Kubernetes 探针验证模型目的地允许连接、禁止出口时连接失败，见 model-network-probe-result.json；不代替正式 release 调查器网络策略验收。

SP-07 amd64 执行器离线镜像的 Python/Ansible/kubectl/OpenSSL 原生运行检查通过；116 个 Debian 包的 84 个源包身份与保留的已认证源码一致，274 个源文件摘要校验通过，116 个 copyright 文件全部一致。见 runner-native-runtime-result.json 与 source-reuse-proof.json。完整发行物仍需独立架构准入与签名 Bundle。
