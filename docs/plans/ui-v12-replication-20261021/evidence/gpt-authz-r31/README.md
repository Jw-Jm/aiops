# r31：原生授权／历史主体保留／全栈一致性闭环（2026-10-27）

本轮接着 r30 的返修断点，做三件事：先修上一轮复审的少量精度与捕获余项；再把「原生用户授权 → 实际运维只读读取」这条链路在真实集群上跑通；最后补历史主体与部署一致性证据。复用 r8～r30 的实现与已验链路，未重做方案、未重采参考图、未重跑首租户初始化／MFA 重置、未恢复虚拟化。

基线：`platform/main/082f87d66c5b` + r23～r31 未提交改动，未 commit／未切分支／未推送。所有数据库、Kubernetes、模型、业务浏览器操作只在 `ssh mssc-direct`（hostname mssc / 10.10.10.2）执行。

## 1. 上一轮复审余项（先做，不单独作为整轮产出）

| 余项 | 处理 | 证据 |
| --- | --- | --- |
| 绝对上限被写成「数据库完全相等」 | 用只读 SQL 以微秒精度重读同两条 challenge：绝对样本 `idle_expires_at=06:59:06.953439`、`absolute_expires_at=06:59:06.953440`，**差 1 微秒**（迁移 65 的 `absolute - interval '1 microsecond'`）。原因是采集用了 `to_char(...,'MS')` 毫秒截断。已改断言为数值比较（`idleToAbsoluteMicroseconds<=1000`）、改 `US` 精度、改 README/repair 措辞，并把 `idleEqualsAbsolute` 纠正为 `false`＋新增 `precisionCorrection`。未重等窗口。 | `../gpt-auth-r30/runs/absolute-expiry.json`、`../gpt-auth-r30/repair.md` §4 |
| 9 个 r30 验收身份「所有 session 已物理撤销」 | 只读复核：8 个身份未撤销行 0；第 9 个身份存在 1 条未撤销 session 行，但其 idle 已到期、当前有效会话 0。本轮统一改写为「物理行已撤销」与「会话现已失效」两种事实分开陈述，不再合并。 | `../gpt-auth-r30/containment.json` |
| `safe-capture` 在 settle 之前探测 | `harness/safe-capture.mjs` 改为 settle → 探测 → 截图 → 再探测（命中即删图并抛错）。新增 `harness/safe-capture.test.mjs` 用**假秘密**自检三条：等待期出现 URI → 拒绝且不写图；探测后到截图之间出现 OTP → 删除已写文件并抛错；无秘密状态正常保留一张。命中的状态仍只记录 `describeSecretState()` 断言。未重置任何身份、未接触既有秘密。 | `runs/safe-capture-test.log` |

## 2. A：合法真实集群登记（本轮的关键改观）

r30 的结论是「任务环境无任何已登记集群，operator 真实范围无法验收」。本轮先核实：`platform.cluster_registrations`、`platform.source_registrations`、`platform.resource_entities` 在任务库（PG17.11/15454/schema70/`ui_v12_r24_native`）中确为 0，确认这是真实缺口而不是记录遗漏。

- **任务专属只读工作负载身份**：新建 SA `ops-r31-sp04-read` + ClusterRole `ops-native-v12-r24-20261026-r31-sp04-read`（仅 `get/list/watch` 的 17 个核心 Kind，**不含 secrets、不含任何写动词**）+ 只绑该 SA 的 ClusterRoleBinding + 命名空间 Role（仅 `r31-graph-lease` 的 `get/update/patch`，图所有权续约用）。令牌用 `kubectl create token --duration=86400s` 写入 Secret `r31-cluster-read`，与集群 CA 一起只读挂到 `/etc/r31`；仓库与文档无令牌值。
  - 边界实证：`GET /api/v1/nodes`、任务 ns `pods`、`/apis/apps/v1/deployments`、`storageclasses` 均 200；`get secrets`＝no、`delete pods`＝no、`create pods`＝no、`list pods --all-namespaces`＝yes（核心只读 Kind 需要集群级读）。
- **真实集群身份**：`clusterUid = 79014400-9453-4cb4-9678-0fae47981d79`（本机 `kube-system` namespace UID）、`endpoint https://192.168.0.63:6443`、`distribution kubeadm`、`actualVersions {kubernetes v1.36.3, containerd 2.2.2, etcd 3.6.8-0}`、能力标志由 `kubectl api-resources`/`apiservices` 实测得出。CA 指纹与 endpoint 描述写入任务自己的 OpenBao（`secret/data/ops/r31-cluster/*`，仅元数据、无凭据），`apiEndpointRef` 指向该真实路径。
- **经受控 API 登记**：真实管理员 UI 登录 → `GET /api/v1/auth/session` 取 CSRF/会话版本 → 创建 `cluster_administration` 挑战并把**本次请求的 method/path/bodyDigest** 绑进 target → 密码＋动态码两步验证 → `POST /api/v1/admin/clusters` 201。往返 201 的 clusterId=`01a17c3c-80db-783b-9388-c6eee037f89b`，revision 1。
- **来源登记与范围更正**：`POST /api/v1/admin/source-registrations` 201（sourceId `01a17c3c-ef66-70cf-9ff1-cad25a8bee34`）。首次登记把 `nativeTenant` 一并声明，采集器按 `snapshot_scope.go` 要求 k8s 来源的 nativeTenant 必须为空，因此真实返回 `source_scope_unverified`；改用真实 `PATCH …​/{id}`（同为 step-up 绑定）收窄为「clusterUid + 任务 namespace」后 revision=2，采集恢复。**这是一处真实的产品语义不一致**（登记接受 nativeTenant，k8s 采集器拒绝），本轮只按契约修正声明，未改产品校验，登记为剩余项。
- **真实采集**：`r28-runtime/sp04.json` 增加该集群条目（Tenant/ClusterUID/SourceID/BackendLogicalID/Endpoint/TokenFile/CAFile/LeaseNamespace/LeaseName/SourceRevision/QPS 8/Burst 20），API 与 Worker 重建后由 Worker 的 SP04 采集器接管：17 个 GVR 全部 `list/watch` 成功，`platform.graph_ownership` epoch=1 持续续约，`platform.resource_entities`/`resource_observations` 从 0 增长到 72，operator 读回 39～43 条任务命名空间真实资源（Pod/Service/ConfigMap/Event/StatefulSet/EndpointSlice）。
- 未伪造任何 UID／组件／来源资格；未改共享集群配置或第三方组件；旧 30467/30469 服务、历史库与镜像未动。

## 3. B：原生授权正反例（真实界面 → API → 服务 → 事务 → 持久化 → 资源返回）

- **管理员负例**：`v12.r24.admin` 的会话是 `platform_admin`，`authorization.grants=[platform_admin]`、`authorizedClusters=0`；`GET /api/v1/resources` 与 `/api/v1/overview` 均为 **403 FORBIDDEN**。管理员角色没有被自动授予 operator，也没有为看菜单放宽认证。
- **创建与授权（真实 UI）**：管理员经界面创建 `r31.op.1793086009209`（201，密码+TOTP 操作证明，行内「尚未授权」），再经「配置授权」弹层授予 operator：集群范围选 `mssc 开发集群 r31`、命名空间 `ops-native-v12-r24-20261026`、填写原因；201 后整页刷新再从 `GET /api/v1/admin/users` 复读，仍是一条 active operator 授权且 clusterUid/namespace 与声明完全一致，`authorizationVersion` 前进到 2。
- **被授权用户真实读取**：该用户首次登录被限制为 `first_password_only`（零角色、零集群），完成强制改密后：`GET /api/v1/resources` 200、返回 39 条真实资源，**返回行全部属于任务命名空间**；`GET /api/v1/overview` 200；按未授权命名空间 `default` 过滤返回 0 条；浏览器 `/overview` 页头显示「运行集群 mssc 开发集群 r31」。
- **范围收窄**：界面把范围改成 `default` 后，旧会话立即 401，重新登录后读取 200 但任务命名空间命中 0 行（收窄生效且未残留缓存）。
- **撤权／再授**：界面「角色与授权」页撤销后，旧会话 401、重新登录后资源读 **403**；再按原范围授权后重新登录，读取恢复 200／39 条。
- **最后管理员保护**：以同一受控写路径（真实 step-up、绑定本次 PATCH 的 bodyDigest）尝试撤销唯一管理员的 platform_admin：**409 CONFLICT「不能停用或撤销最后一名有效管理员」**；之后复核 `platform_admin` 仍在、`activeAdministrators=1`、管理员对资源仍是 403，拒绝没有副作用。
- 期间观察到**瞬时 503 GRAPH_NOT_READY**（worker 日志 `Graph request unavailable stage=lease_before_query`）：读请求落在 2s 租约续约与 6s 归属镜像过期之间的窗口。脚本按原样记录每次尝试（`503 → 200`），不隐藏、不改阈值；作为运行观察写入风险。

## 4. C：旧主体归属保留

- **机制实证（真实 PostgreSQL，schema 70）**：用当前工作树构建的集成测试二进制，在任务实例上创建一次性库并迁移 1→70，`TestNativeMappedHistoricalOwnerRead`、`TestNativeMappedHistoricalTurnRead` 均 **PASS**：显式映射后的历史 owner 可读冻结会话/轮次且 owner、摘要、策略字节不变；未映射主体与另一原生主体被拒绝；撤权后拒绝；历史读取授权不会变成 Job/Worker 授权。一次性 `SUPERUSER` 探针角色仅用于迁移职责，用后已把保留库所有权转回 `postgres` 并删除。
- **反例（真实服务端守卫）**：想为「不存在的旧主体」构造映射时，真实 `opsctl identity import-subject-inventory` 被服务端拒绝：`identity.import_subject_inventory` 要求该主体必须已存在于本租户的 role_bindings/assistant_conversations/investigation.jobs/action.executions。任务库本来就没有任何旧主体历史，因此任何映射都会是编造——守卫正好挡住了编造。拒绝后 `identity.subject_mappings`／`subject_mapping_inventories` 仍为 0 行。
- **边界**：这是「机制验证 + 拒绝编造」，**不是旧用户全业务迁移**；live 库无旧数据，因此没有在 live 上重演历史保留。前端映射面板本轮未改动，也未在 live 制造历史以“演示”它。

## 5. D：全栈部署一致性

- 组件与来源：API `ops-api-query`（sha256 `8fe8d7bd…`）、Worker `ops-worker`（`735a9044…`）、investigator（`ops.local/verification-20261019/holmesgpt@sha256:7ecb3550…`，只读挂 `r28-python` 源码）、Web `ops-v12-native-r30-web-r14`（index `8465ddec…`）、CLI `ops-r24-final-opsctl`（`a2e8de1f…`）。API/Worker/调查器 Pod 的 env 只有 `SP04_RUNTIME_FILE`／`SP06_RUNTIME_FILE`／OpenBao 相关，**没有任何 OIDC/Keycloak/issuer 变量**。
- 冷启动与会话持久：重建 API Pod 前用真实 operator 会话取 `session:01a17c4b-62de-725d-8c24-1727efe6b9c5`（session 200、资源读 200），重建同一 Pod 后**用同一 Cookie 回放**仍然 session 200 并读到 43 条真实资源，说明会话在 PostgreSQL 而非进程内存，且进程启动不依赖身份提供方。
- Web：`/runtime-config.json` = `{"auth":{"mode":"native"}}`，无 oidc/issuer/client 键；`/oidc/.well-known/openid-configuration` 落到 SPA 外壳（text/html），不提供发现文档。
- 合同定向测试：`go test ./test/contract -run Native` 通过（chart 无 OIDC 依赖 + MFA duty 投射两种组合、scope 边界 5 例）。
- 未通过／阻塞（不伪造）：正式不可变 OCI/Bundle、离线安装与切换仍缺本任务可用的合格签名物料，OPA 来源材料不一致保持原失败；当前栈是既有镜像上的源码 canary，不是 chart 发布。

## 6. 本轮之后的边界

- 六维状态未提升：458 状态/177 合同结构不变，`visual` 仍为 0。operator 真实范围这一条从「无合法集群」变为「已合法登记并有正反例」，但仍未做整页视觉，因此不据此关闭 P2-04/P2-05 或 V1.2。
- 新增未验／剩余：Web 静态发布与 chart 渲染的一致性、source 登记接受 `nativeTenant` 而 k8s 采集器拒绝这一语义不一致、`GRAPH_NOT_READY` 瞬时窗口、正式 Bundle/离线切换、P1 其余逐状态视觉、P3（SL-049）及 P4～P8。
- P2-04、P2-05 维持 `in_progress`；不宣称 G2 或开始 P3 样板验收。

## 7. 运行与恢复

- 当前 API：`kubectl -n ops-native-v12-r24-20261026` 的 `ops-api`（manifest `r28-worker/api-pod-r31.json` 副本在任务根 `r31/api-pod-r31.json`，比 r28 多挂 `/etc/r31`）。Worker 同理（`r31/worker-pod-r31.json`）。回退只需 `kubectl apply -f r31/api-pod-before.json`（或 `worker-pod-before.json`）并保留 configmap 原值 `r31/sp04.before.json`。
- 采集依赖 Secret `r31-cluster-read` 与 Lease `r31-graph-lease`；令牌 24 小时到期后需用同一 SA 重新 `kubectl create token` 并更新 Secret，否则采集会失败（不影响 API 登录与授权数据）。
- 账号：新增 `r31.op.1793086009209`（operator，本任务命名空间）、`r31.hist.1793086582839`（零角色）。`v12.r24.admin` 与 `r24.member.*` 的角色/密码/MFA 未改动，未给管理员加 operator。
- 数据：本轮无迁移；两张一次性测试库 `sp03_mig_*` 因含审计记录按保留策略未删除（未删任何证据）。
