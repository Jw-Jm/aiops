# 进入 SP-07 前的非虚拟化能力收口与离线部署验收清单

日期：2026-10-04。核对基线：`main@cd577dfc4e9d1d53f93b5c031d9b0a303ef32dc8`；SP-06 实现提交：`422d9717404cf8b75b9df948a7797777bcd4d0ea`。编写前工作树干净。

状态：**待实施的工作清单，不是新增能力的验收报告或环境清理授权。** 本次仅核对源码、既有证据和环境的只读清单，没有清理 OrbStack、部署组件或进入 SP-07。执行本清单时重新记录实际 Git/环境基线和用户修改。

## 1. 目标和总体判断

进入 SP-07 前，优先解决现有交付的两个问题：

1. **已有能力能否脱离历史开发环境，从正式离线交付物安装并初始化。** 必须覆盖当前 SP-03～SP-06 业务，不能只得到 API/Worker Pod Ready 或依赖旧测试容器。
2. **非虚拟化事实源的证明范围是否完整。** 补齐 DeepFlow 的分发准入和实际网络调查，以及 Metrics-server 的真实正向输入；同时如实保留硬件现场和模型准确率限制。

推荐顺序：**资源/数据盘点 → 修复正式部署与初始化入口、准备全部物料 → DeepFlow 与 Metrics 正向开发验收 → 备份恢复预演 → 按批准范围清理 → 当前 core 空环境离线部署 → deepflow Profile 离线部署与真实调查 → 数据恢复/重启回归 → SP-07 输入交接与独立审核。**

DeepFlow 并非每一条人工命令的技术前置条件；这里把它作为本轮选择补齐的既有产品能力。不能把“Profile 可禁用”解释为其 bundled 交付永远无需完成。

正式规格映射（根目录 00～10 只读，实施决定和新验收记录留在 `platform/docs`）：

- R0/R4：10 Task 2.7、离线安装/数据保留要求，以及 08 的 Evidence/来源身份与保留闭包。
- R1：10 Task 2.7、3.1～3.8、4.7、6.6；07 的统一身份/版本/API/SSE Contract。
- R2：02 FR-023/INT-003；10 Task 2.5、4.5、6.3～6.6；08 动态关系、源端范围和 TTL。
- R3：10 Task 5.3、6.3；ADR-0023 的官方 Metrics/UID/时效门禁。
- R5：06 具体 Recipe、最新 Evidence 和 RCA 质量边界；10 Task 6.4～6.6，以及未来 Task 7.6 的输入准备。
- R6：10 Task 7.1～7.6/§14.7 的明确底座依赖；05/07 的实际命令、确认、身份、幂等和执行模型。

## 2. 已开发内容和现有证明边界

### 2.1 可以复用的已有底座

- SP-01～SP-03：Contract/生成物、租户/RLS、幂等、真实 Keycloak PKCE/OTP step-up、签名 Registry/OPA、Transit、审计归档、工作负载 PKI/mTLS、日志和指标接入。
- SP-04：资源身份、原生 List/Watch、单 Active Worker Graph、授权语义查询、VictoriaMetrics/VictoriaLogs 实际源隔离、Evidence 归档及 Legal Hold/365 天依赖保护。
- SP-05：Finding/Incident、确定性 Recipe/RCA/Impact、非虚拟化黄金切片和锁定巡检内核；硬件正例主要是版本化协议/内核 Fixture。
- SP-06：持久 Job/Lease/Step、Transit InvocationContext、标准 MCP、HolmesGPT investigator、真实 Ollama 调查、Go Validator、持久 SSE、SIGKILL/接管和十个并发调查的正确性。

依据：[SP-03 验收](../evidence/sp03-acceptance-20261001.md)、[SP-04 验收](../evidence/sp04-20261001/acceptance.md)、[SP-05 验收](../evidence/sp05-20261002/acceptance.md)、[SP-06 验收](../evidence/sp06-20261003/acceptance.md)及[最终独立审核](../evidence/sp06-20261003/final-independent-review.md)。这些报告证明各自受测范围，不能当作下一轮运行结果。

### 2.2 必须补齐的正式部署入口

核对当前源码发现：

- [标准安装器](../../internal/bundle/installer.go)的 `ops-platform-chart` values 显式设置 `investigator.enabled=false`；没有装配 SP-04、SP-05、SP-06 的业务启用 values。
- [Chart 默认 values](../../deploy/charts/ops-platform/values.yaml)中 `sp04.enabled`、`sp05.enabled`、`sp06.enabled` 均为 false。默认关闭本身合理，但完整交付必须提供明确、受校验的启用流程。
- [SP-06 Runbook](../runbooks/sp06-investigation-runtime.md)要求操作者另行提供启用 values、tenant/policy、模型、精确网络范围和身份 Secrets。
- [原生验收](../../test/integration/sp04_native_chart_test.go)确实使用了签名镜像/Chart 和实际模型，但用专项 values、独立 OpenBao/Keycloak/S3、私有数据库以及测试内 Registry/Policy 初始化完成运行。

因此，既有 SP-06 原生 PASS **不能证明标准 `opsctl install` 已能在全新环境交付全部当前业务**。优先把这些部署与初始化步骤收口为正式、可重复的交付流程。不能为了验收成功临时调用测试私有构造器、直写业务表或复制测试生成的 Policy/Recipe 数据。

### 2.3 当前 Bundle 的定位

当前受测 Bundle 为 `sp06-nonvirtual-20261004-r10`，payload digest：

`sha256:863b2381784b189c2bfbd94db26b6414ed96efeb416bd7e465d0f69ef0c7ee96`

它包含 26 个物料、78 个认证 payload 文件；运行绑定为 4529 个文件。SP-06 离线门禁覆盖受影响 API/Worker/investigator 的导入、原生运行和 release 重装，保留共享依赖和已有数据。

先核对并保留该包作为现有基线。新增 DeepFlow/Metrics 或修复安装、配置、初始化后，必须构建新的源码绑定和签名 Bundle，重新验收；不能继续用 r10 的签名/历史退出码证明变更后的交付。既有签名公钥身份来自独立信任渠道，不能仅因包内存在公钥就信任它。

## 3. R0：清理前完成环境盘点与数据保护

### 3.1 只读观测快照

2026-10-04 编写时只读检查得到：

- Kubernetes context 和 Docker context 均为 `orbstack`；节点为 arm64、`v1.35.6+orb1`。
- 共 10 个 namespace；除 Kubernetes 固有 namespace 外，还有 `ops-system`、`monitoring`、`holmes`、`ops-dev-network`、`ops-sp02-deepflow-poc` 和 `sp04-chain-0587768a`。
- Helm 清单有 `holmes`、`ops-core`、`ops-dependencies`、`ops-platform`、`vm`、`vmalert` 六个 deployed release。存在历史 `ops-core` 和新 `ops-dependencies`，不得按名称推断资源互不重叠。
- Docker 有 109 个容器，其中 39 个不是 Kubernetes 命名的容器，5 个仍运行；有 611 个 volume。清单还包含历次 SP-03～SP-06 依赖和测试容器，以及不能仅凭名称判定归属的其他容器。
- `v1beta1.metrics.k8s.io` APIService 查询结果为空。

这只是时间点快照，不是删除清单。namespace/volume 数量、名称前缀、容器停止状态均不能证明归属或可删除；执行前重新盘点。存在 DeepFlow 命名 namespace 也不证明存在可用 live Server。

### 3.2 建立逐资源处置清单

- [ ] 记录 Kubernetes cluster UID、namespace UID、Helm release、工作负载/Service、CRD/APIService、ClusterRole/Binding、Lease、NetworkPolicy、PVC/PV、StorageClass 和实际后端存储身份。
- [ ] 记录 Docker container ID、image digest、labels、mount/volume 消费关系、运行状态及持久目录；包含停止容器和匿名 volume。
- [ ] 记录端口转发、独立 API/Worker、诊断代理等本机进程的 PID/命令身份；排查是否仍依赖临时目录。
- [ ] 每项标明“批准删除”“备份并恢复”“保留在清理范围之外”“归属待确认”。清单包含停止/删除顺序、所需授权和清理后核验方法。
- [ ] 对 Secret 只记录对象身份、键名和用途；实际值进入仓库外受控加密备份，不写入 Markdown、原始公开日志或 Bundle。

### 3.3 数据恢复先于数据删除

至少保护 PostgreSQL 业务和审计元数据、SeaweedFS 对象/版本/retention/Legal Hold、OpenBao 存储及 Transit 历史 key version、Keycloak realm/用户/OTP 配置、Registry 签名信任、工作负载 CA，以及需要保留的 Metrics/Logs。

- [ ] 在私有位置形成一致恢复点，记录数据库/对象后端身份、迁移版本、备份 digest 和密钥版本；必要时先停止写入或做一致性核对。
- [ ] 在隔离位置实际恢复一个可读取历史 Evidence 和可验证 Audit 的样本，证明私钥/Transit key、数据库和对象备份相互匹配。
- [ ] 未到期 Compliance retention、Legal Hold 和关联依赖保护仍有效。复制备份不等于允许提前删除原对象或底层 volume；不能为环境“清干净”关闭保护、删除锁、强制移除 finalizer 或丢弃密钥。
- [ ] Bundle、独立验签信任、源码、许可证、锁定工具、离线安装工具、模型服务配置和可恢复密钥材料放在清理边界之外，避免先删除唯一可用物料再尝试重建。

**R0 完成条件：** 有资源级处置清单、可用物料和恢复证明。存在归属不明或无法保留的受保护数据时，不执行原地全量清理。

## 4. R1：正式安装、配置与初始化流程收口

- [ ] 定义当前非虚拟化交付清单：`platform-api`、两个 Worker、锁定 investigator，以及 PostgreSQL、Keycloak、OpenBao、SeaweedFS、VictoriaMetrics、VictoriaLogs、vmalert。无 Web/Command Runner 的完成声明。
- [ ] 提供正式的 Profile→校验后的 Chart values→安装/初始化流程，显式启用 SP-04～SP-06。可以复用现有 CLI/Chart/初始化命令，不另造 Agent Runtime，也不以专项测试作为安装器。
- [ ] 校验 operator values 的 Schema、来源和范围；缺 tenant、model、当前 Policy、身份、source 映射、确切私网地址时明确失败。不得注入 Profile/v1 未定义字段；确需公共 Contract 变化时版本化并更新生成物/消费者。
- [ ] 空数据库通过 [正式迁移入口](../runbooks/database-migrations.md)运行 forward-only migration；当前基线至少到 00035，后续以实际源码最高迁移为准。bootstrap、migration、API、Worker LOGIN 分离，不给运行组件数据库 owner/root 凭据。
- [ ] 用正式 bootstrap 建立安装 namespace 和公共信任配置；标准安装器当前要求 `ops-system` 预先存在，这一步必须写入空环境流程，不能借用旧 namespace、Secret 或 ConfigMap 隐性满足前置。
- [ ] PostgreSQL runtime role、Archive 四职责 IAM、TLS CA、bucket/retention、Keycloak realm/租户/operator/step-up、OpenBao init/unseal/Transit/PKI/TokenReview 有明确初始化顺序及受控私有输入。
- [ ] 用正式 API/Registry 流程完成 Tenant、Cluster、Source、RoleBinding、签名 Policy/Recipe/Tool 和激活；每项含 revision/digest 和审计。默认开发用户/源码样例不自动获得执行权限。
- [ ] 正式执行 `opsctl openbao configure --investigation-signing` 等已有入口，确认 Context 运行签名密钥由 Transit 持有；PKI 身份 SAN、CRL 和轮换链能持续工作，不能依赖测试内后台刷新线程。
- [ ] 重新发现模型 endpoint、DNS/Host header、CIDR、端口和可用模型；统一 Deployment Profile 与 Model Contract 的转换。不能硬编码之前测试的模型桥接 IP、Worker CIDR 或控制面地址。
- [ ] 核对 installer 对 `core`/`deepflow` 的实际支持，及 DeepFlow addon 是否被纳入有明确依赖顺序的离线流程；不能因为模板声明了 Profile 就认为 CLI 已支持。
- [ ] Runbook 更新为当前业务入口，标清历史 skeleton 记录。交付锁定的 opsctl、迁移工具及所需 helm/kubectl/导入工具；若离线从源码运行，则完整工具链和依赖缓存也是明确前置，不能暗中访问 Go/Python/Chart 下载源。

**R1 完成条件：** 无旧数据库/测试 seed/测试容器时，正式交付入口能够从空环境初始化当前业务；缺少输入、签名、身份、来源范围或前置条件时诚实失败。

## 5. R2：DeepFlow 完整准入、离线部署和实际调查

### 5.1 先完成物料准入

DeepFlow 是按 Profile 启用的正式网络能力，`external|bundled|disabled` 都需表达清楚。启用 Profile 的动态通信关系为 P0；其 `candidate` 状态不是类似虚拟化的延期授权。

当前 [Catalog](../../bundle/component-catalog.yaml)仍记录 DeepFlow `candidate`、依赖闭包未验证和若干来源/许可证 pending。历史 [2026-09-29 live PoC](../poc/deepflow-v7.2.0-orbstack.md)证明了一条真实 L4 流量及注册/隔离，不证明完整分发准入。

- [ ] 复用锁定 v7.2.0 源码、Server/Agent 镜像和分别锁定的 7.1.002 Chart；版本不一致要有实际兼容证明，不能从 Pod Ready 推断兼容，也不自行升级以绕过问题。
- [ ] 补全实际镜像、Chart、配置、MySQL/ClickHouse 等真实依赖闭包的精确来源、架构/digest、源码快照、原始许可证/notice、SBOM、分发裁决和适用的对应源码义务。
- [ ] 将实际 Chart 变换、补丁、关闭项、来源与可复现构建关系写入锁定材料；MySQL 不能只凭根目录许可证完成 GPL 分发准入。
- [ ] 所有正式启用的 DeepFlow 物料获准后才进入新签名 Bundle；不改 `candidate` 标志或 `installable` 来绕过准入。
- [ ] 保持一个 Server/storage 平面及每个受管 Kubernetes 集群一个 Agent DaemonSet；不引入 deepflow-app、GUI、Stella、ByConity、Jaeger、OTel 全家桶或重复中心平面。

### 5.2 验证真实流量进入平台业务

- [ ] 在自有隔离 namespace 中生成实际通信；记录请求方/服务方资源 UID、Canonical ID、观察时间和当前注册 Agent。不能把“Querier HTTP 200”作为观察到真实流量的证明。
- [ ] 通过正式 SourceRegistration、backendLogicalId 和 dataScopeMapping 接入。证明源端 organization/team/cluster/namespace 隔离；无法证明共享隔离时使用范围独立 endpoint/凭据或拒绝该范围。
- [ ] 六个既有有界网络语义操作分别验证适用的成功、空结果、partial/degraded、超时、权限拒绝和字段漂移。不能把零重传样本当作真实丢包/重传故障正例；某操作无法取得真实正例时保持该项未验证，不虚构通过。
- [ ] 实际 Flow→DeepFlowEvidenceAdapter→Evidence→Graph 动态边/Impact→标准 MCP `query_deepflow`/上下文工具→真实 Holmes 调查→Go Validator→Ledger/Audit→SSE。
- [ ] 动态边带来源、观察窗和 TTL；重复查询不使旧边永久有效。必要关系/最小事实切片归档后，Flow 原始短期数据过期仍能按授权重放过去分析。
- [ ] 包含跨租户/namespace、范围扩张、参数注入、源禁用/凭据轮换、缓存重放及调查过程撤权；不向 MCP 暴露通用 SQL、ClickHouse DSN 或任意 URL。
- [ ] 若引用确定性网络 Recipe，核对其 published 版本和确认谓词；缺少因果证据时允许 probable/unresolved，不能为了闭环生成 confirmed。
- [ ] L7 仅按实际 capability 开放；Trace completion 保持正式禁用边界，不以 DeepFlow 基础能力通过推断全部 L7/APM 能力通过。

**R2 完成条件：** 分发准入、签名物料、当前环境的真实网络业务链路和作用域隔离通过；未取得正例的子能力分别记录。使用 Fixture 的门禁明确标记，不能替代实际流量链路。

## 6. R3：Metrics-server 正向与 Node 观测补验收

VictoriaMetrics 真实查询已验证；缺口是 Kubernetes `metrics.k8s.io` 的正向采集。现有 [官方观测边界](../adr/0023-sp05-official-observation-boundaries.md)和 SP-05 报告明确记录实际 API 缺失、正例为协议 Fixture。

- [ ] 选择与当前 Kubernetes/arm64 实际兼容的 Metrics-server，完成精确来源/许可证/依赖/SBOM/Chart 或 manifest/digest 准入；明确它作为受管前置或可选物料的交付方式。
- [ ] 离线部署并验证 APIService 实际可用、kubelet TLS/认证与最小权限。不得通过随意放宽 TLS 校验取得通过。
- [ ] 实际 Node/Pod 指标读取得到合法 timestamp/window/Quantity，与原生 Node UID、当前 capacity/allocatable 及 source scope 关联，经过既有 Inspection→Finding/Evidence→API/MCP 路径。
- [ ] 覆盖缺失、过期、错 UID、Pod/Node 重建、403、撤权和恢复；普通 CPU/内存使用率不单独确认因果。
- [ ] 正常正值读取不等于高利用率症状已取得 live 正例。若本轮要验收既有阈值症状，用有限、可恢复的自有负载验证触发与恢复，保留既有阈值；不变成性能基准或持续压测。

**R3 完成条件：** 真实 Metrics API 正向链路和缺失/身份/授权反例通过；“读到指标”与“阈值症状真实触发”分开记录。

## 7. R4：OrbStack 全量清理与两种安装验收

### 7.1 精确定义“全量”

建议清理目标为批准范围内的历史平台业务 release、测试 namespace、独立依赖容器、无保留义务的存储、旧临时进程及选定平台镜像引用。是否同时重置 OrbStack Kubernetes、清理其他 Docker 项目/机器，需要明确列入资源处置清单；不能从“全量”两个字推断它们均可销毁。

- 清理所有旧平台依赖后，真正的空环境验收应使用新的 bundled PostgreSQL/Keycloak/OpenBao/SeaweedFS/VM/VLogs/vmalert，不能仍把历史七个共享服务作为实际安装依赖。这里 VM 指 VictoriaMetrics，不是虚拟机。
- 若保留共享依赖，只能声明“复用 external 的干净业务 release 安装”，不能声明全部依赖冷安装。
- 若重置集群，原生 Kubernetes、DNS、StorageClass/CNI 等基线需要重建并记录其物料/版本。策略控制器不得依赖公网临时拉取；仅创建 NetworkPolicy 对象不能证明隔离有效。
- 如果 Compliance/Legal Hold、无法确认归属的存储或其他项目无法安全移出清理边界，原地全清理被阻断。可以另建独立干净目标完成空环境验收，但不能把它记成“原 OrbStack 已全量清理”。

本文不提供可直接执行的全局删除/prune 命令。实际删除逐项核对 UID/标签/消费关系，对 Kubernetes 删除使用有效的 UID 前置条件；不绕过 finalizer、不 blanket 删除 PVC/PV、image 或 volume。

### 7.2 安装 A：当前 core 从空环境安装

- [ ] 清理前包和安装工具已准备完毕；目标安装阶段禁止公网，打包端的联网准备与目标端严格区分。
- [ ] 留存清理前后清单；证明旧 release、独立测试 DB/Bao/S3/OIDC 服务、相关端口转发和旧业务数据没有给本次成功提供隐性依赖。
- [ ] 对每个本次选定平台/依赖 OCI 引用和 manifest digest 留下导入前不存在的证明；共享底层 layer/BuildKit 缓存和 Kubernetes 固有基线另行说明，不冒充整个宿主机完全无缓存。
- [ ] 新探测并 resolve 当前 Profile；全冷装声明要求相应依赖为 bundled，而不是 detect 后误复用旧 external。KubeVirt/CDI 保持 disabled/unverified。
- [ ] 验签→导入→正式安装→迁移→身份/来源/Registry 初始化→SP-04/05/06 业务健康与能力检查。全部使用已锁定本地安装物，不访问 Helm repo、OCI registry、语言包仓库或公网证书服务。
- [ ] 模型服务是明确的独立内网前置，锁定实际 endpoint/model 并证明可调用；模型权重不进入平台 Bundle。不把复用本机 Ollama 描述为 Bundle 已分发模型，也不删除它后以 Mock 替代真实调查。
- [ ] 从业务 Pod 分别证明内部 DNS/API/数据库/归档/模型正向可达、公网 IPv4/IPv6 拒绝；investigator 的数据库/事实源直连拒绝。具备独立正向控制，不能把本来不可达的公网当作 NetworkPolicy 生效证明。
- [ ] 证明 Resource→Graph→Evidence→Finding→Incident→确定性 RCA/Impact，以及 API→Job→Dispatcher→investigator→真实模型→标准 MCP→Validator→SSE；不是仅检查 readiness。

### 7.3 安装 B：deepflow Profile 和补充物料离线安装

- [ ] 在合格 core 上通过正式独立 addon 流程安装 DeepFlow；使用 `deepflow` Profile，不使用会包含延期虚拟化的 `full` Profile。
- [ ] 证明 DeepFlow 及其实际依赖的选定镜像导入前缺失、来自签名物料、无公网、Agent 注册和实际流量观察；随后执行 R2 全链路。
- [ ] 执行 R3 Metrics 正向验证；确认 addon 故障只影响声明能力，核心事实/调查状态诚实退化。
- [ ] 图、调查、模型、单一来源故障不导致全部 API 副本退出 readiness；重启后不依赖内存 SSE 或旧 Fixture 会话。

### 7.4 安装 C：数据保留、恢复及重装

此项与空环境安装独立记录。先完成 A 的空库证明，再在批准的隔离恢复路径验证历史数据；不能先恢复旧库再声称 fresh install。

- [ ] 用同一受测交付验证业务 release 重装后 retained 数据可用；另验证 R0 的实际备份恢复，而不只检查备份文件存在。
- [ ] Evidence digest、对象版本、Transit 历史解密、Audit 链、Legal Hold 和 365 天依赖闭包保持有效。旧 v1 未验证审计 proof 的限制继续保留，恢复不自动使其通过。
- [ ] 集群重置产生新 cluster UID/Node UID 时，按正式身份/注册规则建新当前身份并保留旧历史；不得把旧 scope、Graph、SourceMapping 或 Registry 自动绑定到新目标。
- [ ] 数据库备份、OpenBao key 和对象存储版本配套恢复；backendLogicalId 不能被默默指向一个新的空桶来伪装同一来源。
- [ ] 验证恢复后的 Job 接管、generation/context fencing、已提交成功 Step 不重跑、未知调用保守结算，以及持久 SSE 续传和当前撤权。
- [ ] 本次新产生的保留对象/数据有长期存放位置；测试完成后的清理同样不能破坏刚验证的保护。

**R4 完成条件：** 空环境 core、DeepFlow addon、历史恢复/保留分别有实际命令、退出码、源码/物料绑定和完整业务证据。未完成任一路径时报告其具体状态，不以另一条 PASS 替代。

## 8. R5：调查质量、故障场景和 Post-check 输入基线

SP-06 原生十调查最终 9 failed、1 succeeded 证明并发预算/审计和降级正确性，不证明模型建议稳定可用，也不否定已完成的正确性门禁。

- [ ] 从当前新部署环境选择有限的串行真实案例，分别记录模型服务失败、超时/预算、输出格式/引用拒绝和建议可用性；修复确认的功能问题，不调大正式预算或删除失败记录取得成功。
- [ ] 检查有建议时 ActionPlan/v2 类型、目标 UID/scope、Evidence 引用和风险提示可被 Go Validator 校验；没有证据时允许 unresolved，Agent 仍无执行凭据或执行句柄。
- [ ] 为真实 Kubernetes 调度/PVC、Node 观测、网络流量/来源退化建立版本化场景与独立预期。网络失败与恢复采用自有测试资源，不破坏共享网络或控制面。
- [ ] 在进入命令开发前验证当前事实/Recipe 可区分“已恢复”“仍故障”“来源不可用”；观察窗使用最新 Evidence，不能拿旧 confirmed 当作现时恢复证明。
- [ ] 命令退出 0 但故障仍在、命令不确定、Post-check 来源退化的完整执行案例留在 Task 7.6；此处只准备目标、事实和预期，不提前创建 CommandExecution 或 Runner。

正式模型 RCA 准确率需要合格模型和独立标注 holdout，不能用 `llama3.1:8b-16k` 的协议测试代替。它不是开始人工命令实现的普遍前置：正式方案允许 probable/unresolved 下由 operator 按实际命令策略、权限、本人确认和 step-up 决定执行。生产准确率声明仍需另行完成正式评估。

## 9. R6：SP-07 必要前置与阶段内工作分界

### 9.1 进入前准备

- [ ] 当前实际交付上选择性复核 SP-03 幂等、真实 Keycloak step-up（ACR、1 小时 absolute/idle、撤权）、OPA 当前版本、Transit protector、Audit 追加与归档；operator/admin 不继承，Agent write 拒绝。无需重写已通过的底座。
- [ ] Kubernetes 的 namespace/cluster 测试目标、UID、RBAC 边界和网络范围已明确；准备独立 Linux SSH 测试目标、普通用户/root/sudo 边界、host key 和接入责任。
- [ ] Host Onboarding 前置必须可落实：目标 `sshd` 可配置 TrustedUserCAKeys、principal 映射明确、known_hosts 可独立核验。不把真实 SSH transport 测试冒充物理 BMC/DIMM 故障现场验收。
- [ ] 明确 SP-07 非虚拟化授权范围、四类 ExecutionProfile 的实际目标，以及缺少 SSH 目标时可开发的部分和无法完成的验收；无 SSH 正向目标不能最终宣称 Task 7.5 通过。
- [ ] 将新 Runner/Ansible、Bash AST parser、工具镜像来源/许可证/依赖闭包准备列入阶段计划；不能把当前 investigator 镜像当作已准入执行镜像。

### 9.2 属于 SP-07 的实现，不提前做

Action/RiskAcknowledgement/CommandExecution 模型与迁移、Bash 风险分类、ExecutionProfile/step-up 原子门禁、一次性 Runner claim、Kubernetes Runner、Ansible Runner/OpenBao SSH CA、输出/SSE、`execution_unknown` 对账和完整 Post-check 属于 Task 7.1～7.6。工具镜像分发准入必须在首次真实执行和最终验收前完成，不要求它们在本清单阶段已实现。

[正式 Task](../../../10-智能运维平台1.0具体编码实施方案-最终版.md)及 [ADR-0003](../adr/0003-manual-command-remediation.md)继续约束：操作者提交实际 Bash 并本人确认；Agent 只能建议；没有第二审批人、命令 DSL 或不确定执行自动重试。

## 10. 不作为本次进入前阻断的项

- KubeVirt/CDI、VM 调查/工具/生命周期保持 [ADR-0008](../adr/0008-defer-kubevirt-cdi-development.md)延期，disabled/unverified；环境清理不构成恢复实施授权。
- PyRCA 按 [ADR-0020](../adr/0020-sp05-pyrca-disabled-gain-decision.md)继续 disabled/excluded，不为收口调查而启用。
- 物理 DIMM/NIC/BMC 现场若无实际目标、凭据和可安全实施的场景，单独记录未验证；既有协议 Fixture 不是现场 PASS。若下一阶段选择交付该硬件处置能力，则对应真实来源和目标成为该切片的必要门禁。
- 不提前完成 SP-08 UI、SP-09 发布体系、kubeadm/Kylin 生产兼容、HA/灾备或生产保留容量。开发机成功不等于这些项目通过。
- 本清单不安排专门基准、持续压测、容量验收或追加 P95。此前性能豁免不写成通过；后续执行范围中的豁免需按用户实际授权记录，不能从 SP-06 自动推导所有未来阶段均被豁免。

## 11. 实施证据和必要门禁

执行时新建 `platform/docs/evidence/pre-sp07-<实际日期>/`，不能覆盖 SP-01～SP-06 的历史报告。至少保存：

- `task-ledger.md/json`：R0～R6 → 正式 Task/Contract → 实现入口 → 测试 → 原始证据；明确硬前置、条件项、豁免和延期。
- `baseline.json`：分支/HEAD、工作树/用户修改、远端基线、cluster/runtime 身份和受测源码清单。
- `inventory-before/after`、`cleanup-plan`、`cleanup-results`：资源 UID/归属/授权/消费关系、逐项操作/退出码和保护对象核对；不包含 Secret 值。
- 私有备份的公开身份/digest与 `restore-verification`；原始密钥/备份在仓库外受控位置。
- 新 Bundle manifest/signature、独立公钥身份、Profile/配置 digest、源码/镜像/Chart/SBOM/许可证锁和物料验证输出。
- core fresh install、DeepFlow actual traffic、Metrics positive、完整调查、重启/恢复、跨租户/撤权和公网隔离原始日志。
- 失败、修复、针对性与受影响路径回归、最终完整独立只读审核及关闭发现。

复用现有入口，并根据新安装/DeepFlow全链路补充真实测试：

- `make check-toolchain`、`make check-generated`、`make check-runtime-source`、`make check`、`make test-security`、实际 `make test-replay`。
- [core 离线 E2E](../../test/e2e/offline_install_test.go)：`TestCoreOfflineInstallation`；其历史 skeleton/release 验证范围不能代替本清单的当前业务全冷装门禁。
- [DeepFlow PoC](../../test/e2e/deepflow_poc_test.go)：`TestDeepFlowPOC`；另补真实源→Graph/Evidence→MCP→调查的端到端门禁。
- [原生 Chart 门禁](../../test/integration/sp04_native_chart_test.go)：`TestSP04SignedChartNativeWorkerOfflineReinstall`；继续用于受影响回归，不能用它冒充全依赖 fresh install。
- [SP-06 实际模型/MCP](../../test/integration/sp06_real_chain_test.go)、[Context](../../test/integration/sp06_context_live_test.go)、[审核边界](../../test/integration/sp06_review_boundaries_test.go)、[SSE](../../test/integration/sp06_sse_live_test.go)、[归档保留](../../test/integration/sp06_archive_test.go)；来源/配置改变后重跑相应实际路径。
- [真实 step-up](../../test/integration/keycloak_test.go)、[PKI/mTLS](../../test/integration/projected_workload_test.go)、[Audit Worker](../../test/integration/worker_runtime_test.go)、[源隔离](../../test/integration/sp04_victoria_test.go)和迁移/恢复；按受影响范围选择，不为清单而无限重复未变化测试。

以上是待执行入口，不是已执行命令。正式验收必须提供依赖并显式启用 live 入口；skip、空报告、仅单测、Mock、历史日志或仅 Ready 均不能替代本次实际门禁。不得放宽阈值或删断言取得通过。必要并发正确性、预算、超时、重启/接管及主链路保护仍保留；不转为专门性能测量。

## 12. 进入 SP-07 的完成判断

选定以下清单为本次收口交付后，全部满足才报告该范围完成：

- [ ] R0：处置边界清楚、物料齐备、受保护数据可恢复；真实清理范围与“全量”声明一致。
- [ ] R1：正式部署/初始化入口可从空环境交付 SP-03～SP-06，不依赖测试 seed 或历史临时服务。
- [ ] R2：DeepFlow 物料完整准入、离线部署、真实网络事实和平台调查链路通过；单项未验证能力如实列出。
- [ ] R3：Metrics-server 实际正向采集、身份/来源授权和退化通过。
- [ ] R4：当前 core 全冷装、deepflow addon、数据保留/恢复与重装各自通过；公网拒绝和内部正向控制具有实际证明。
- [ ] R5：真实场景与建议输出的功能问题关闭，最新事实可支撑 Post-check 判据；准确率与并发正确性的结论边界明确。
- [ ] R6：SP-07 底座复核、隔离目标、凭据接入和执行阶段物料计划明确；条件不足的切片不能被默认为完成。
- [ ] 未参与实现的独立只读审核者重新审查完整收口交付及新运行证据，确认范围内缺陷/必要证据缺口关闭并明确 PASS。

新收口交付不覆盖或否定 SP-06 既有受限范围 PASS，也不把它扩展成所有源、所有安装模式或生产能力均通过。下一阶段只有在用户明确授权后才实施 SP-07；命令执行边界在此前继续关闭。
