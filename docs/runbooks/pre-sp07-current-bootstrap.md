# 当前非虚拟化安装与首次 Tenant 初始化

2026-10-08 开发环境最新更新：源码修复集 `95ae3dad9d80897f814eed5fb411e50f0c9044d9` 已合入 main 并推送，新签名包为 `pre-sp07-dev-20261008-r13`；现有 `ops-pre-sp07-core-r9-20261008` 安装完成 API/Worker 滚动更新，实际镜像、保护核验与必要冒烟见 [R13 发布记录](../evidence/pre-sp07-20261008/development-release-r13-r310.md)。以下旧现场记录继续作为历史证据保留，当前运行版本以 R13 为准。此次只更新既有开发安装，不重跑首次初始化或改写原安装 checkpoint，不宣称正式通用升级或全冷安装通过。开发 OpenBao 重启后仍须按本安装原有恢复材料通过正式 status/unseal/status 入口解封，再验证实际业务；本次没有采用自动解封或重新初始化。

状态：实现中。以下说明已实现入口，不是全依赖空环境安装的验收证明。正式验收结果和缺口以 [本轮账本](../evidence/pre-sp07-20261004/task-ledger.json)为准。

使用锁定且认证的本轮 Bundle、仓库外独立验签公钥和本轮发现的 resolved Profile。旧 r10 仅保留为验证过的历史基线。模型为独立内网前置，Bundle 不声明包含模型权重。

先在独立目标创建 namespace、公共信任和依赖 Secret。`environment.json` 必须显式提供 `schemaVersion: 1`、context、namespace 和五项独立公共信任；八组依赖凭据、匹配该 namespace 的 TLS 私钥及 Archive IAM 放在仓库外 0600 输入。入口拒绝已存在的 namespace，不读取或接管旧数据：

```sh
opsctl bootstrap environment --config /secure/environment.json \
  --secrets-file /secure/dependency-secrets.json
```

当前 bundled VictoriaMetrics/Logs 必须使用该安装 namespace 的正式 HTTPS Service，并先初始化原生来源信任。私有输入 schemaVersion=1，sources 恰好包含 victoriametrics、victorialogs，各自给出独立 CA、匹配实际 Service SAN 的 certificate/privateKey、明确 username 和不同的强 password。入口严格拒绝未知字段、私钥混入 CA、错误 SAN、重复凭据和已有 Secret；部分失败保留已创建资源。客户端投影仅有 CA 和显式版本的 Basic 凭据，不含服务 TLS 私钥。原生认证不等于来源范围已取得准入；必须另通过正式 Registry 和有界正向/拒绝验证。

```sh
opsctl bootstrap victoria-trust --profile /secure/current-resolved-profile.yaml \
  --business-values /secure/current-business-values.json \
  --secrets-file /secure/victoria-private.json
```

锁定官方 Victoria Chart 使用原生 TLS、文件口令和独立 Secret username 投影，vmalert 仅挂载读取所需的 CA/password。VictoriaLogs 通过官方 server.http 设置精确 TLS listener，HTTPS readiness 不使用默认明文设置。实际 R8 安装已验证 TLS/Basic 正向、401 拒绝，以及正式 Source→Adapter→归档→API 的原生有限事实；这些历史事实不代表当前目标仍健康，也不替代模型/MCP 和完整冷装验收。

VictoriaLogs 原生 `/insert/jsonline` 初始化探测必须显式使用 `_stream_fields=tenant,cluster,namespace,uid`，并保留实际资源 UID、原始日志时间与内容。仅在 JSON 中提供四个标签不会自动形成 stream：默认 `{}` stream 无法满足正式 Adapter 的精确范围 selector，来源必须保持未验证。该要求来自[官方原生摄取说明](https://docs.victoriametrics.com/victorialogs/data-ingestion/#http-query-string-parameters)。修复时只重投已核验的原始事实，不能改写时间、扩大 selector 或直写准入状态；由正式来源验证循环取得范围证明。真实失败、修复和正式 API 读回见 [r117](../evidence/pre-sp07-20261007/current-core-r8-native-victoria-evidence-api-r117.json)、[r118](../evidence/pre-sp07-20261007/current-core-r8-native-log-stream-mapping-r118.json)及 [r120](../evidence/pre-sp07-20261007/current-core-r8-native-victoria-evidence-api-r120.json)。

当前安装分为两段。第一段只部署签名依赖和策略；namespace UID、安装 ID、Bundle/Profile/business 输入 digest、依赖资源和 PVC UID 进入安装 checkpoint。第二段必须匹配这一 checkpoint，拒绝旧资源或 scope 变化。Source ScopeProbe 的 from/to 可在初始化后刷新为当前有界窗；只有这两个时间字段不参与跨阶段 digest，其余身份、scope、query、预算和 trust 保持绑定。当前只允许复用自己的 NetworkPolicy，不接管旧业务工作负载。当前安装入口：

```sh
opsctl install --profile core --resolved /secure/current-resolved-profile.yaml \
  --bundle /secure/current-signed-bundle --key /secure/independent-trust.pem \
  --business-values /secure/current-business-values.yaml --stage dependencies --offline
```

business values 遵守 [安装 Schema](../../api/schemas/installation-business-values-v1.schema.json)。必须显式提供全部 SP-04～SP-06 字段，以及 `workloadIdentity: {enabled: true, mode: openbao-kubernetes}`。当前入口拒绝静态身份模式。模型和控制面 CIDR 使用本轮发现的确切 /32 或 /128；模型 URL 与端口必须匹配，禁止公网上游。OrbStack 原生桥使用显式 `modelNetworkMode: orbstack-host` 和 `host.docker.internal`；CIDR 来自容器内当前发现，业务激活前在 investigator 策略范围内验证全部 DNS 地址和 `/v1/models`。此例外只用于模型，不放宽事实源地址校验。身份、模型和源凭据分别投影不同 Secret。私密值放在外部输入与 Secret 中，不能放入 values、公开日志或 Bundle。

当前 values 输入覆盖 Kubernetes 与 VictoriaMetrics/Logs；每个调查 tenant 必须显式提供两个 Victoria 来源及匹配 cluster/namespace 的有界只读 probe。API、Worker 与 investigator 使用 `openbao-kubernetes`，复用进程内 CertReloader 和正式 CRL 检查。Chart 为不同 ServiceAccount 投影 audience=openbao token；investigator 仅允许访问同 namespace、指定依赖 release 的 OpenBao 8200，CA 来自独立公共信任。身份 Secret 保存公共 CA 和 Context 信任，不预置工作负载私钥。Context 签名在租期内重新认证，失败拒绝。

2026-10-07 的签名 R10 已在独立 R8 namespace 通过正式依赖、空库迁移、真实 Keycloak OTP/LoA-2、Tenant、Registry、业务安装及原生 Metrics/Victoria 事实归档 API 的分项验证，实际 investigator 使用签名运行镜像。OCI 缓存并非全冷，完整模型/MCP、十并发、DeepFlow 与最终恢复尚未通过。R10 原生 RoleBinding 状态变更暴露事务授权顺序缺陷；当前源码已补真实数据库 HTTP 复现并修复，新的源码绑定签名包和原生回归仍待完成，不能沿用 R10 PASS。DeepFlow bundled 是本轮必做项，精确分发准入未完成时不能启用。最新结果以任务账本和逐项原始证据为准。

Keycloak 使用单独的 `keycloak` 数据库与 `ops-keycloak-database` 身份，不再使用平台 PostgreSQL bootstrap 凭据。首次 PostgreSQL 启动通过固定 Chart SQL 创建其受限角色；该路径的 Chart 回归和独立实际 first-init 都已通过；只使用新建隔离 volume，不代表签名包冷导入。

空数据库先使用 [正式 forward-only 迁移流程](database-migrations.md)，由 bootstrap 身份完成第 1 版角色初始化，migration LOGIN 完成其余迁移。版本 1 后，用 `opsctl bootstrap database-logins --secrets-file /secure/database-logins.json` 创建独立 migration/API/Worker LOGIN，再由 migration LOGIN 完成后续迁移。私有输入使用 schemaVersion=1 及 migration/api/worker 各自的 name/password；支持 24～256 字节的可打印非空格 ASCII 口令，避免 SCRAM 归一化歧义。角色不存在时才创建，现有角色拒绝接管。正确和错误 SCRAM 认证、三类权限分工及 1→35 实际迁移已通过。investigator 不持有数据库账户。

依赖阶段后的安装 Profile 必须保持不可变。`opsctl openbao configure` 会向它的 `--profile` 文件追加观测证据；因此先把安装 Profile 复制为独立的初始化观测文件，OpenBao 的 init/unseal/configure 使用该副本，业务安装继续使用原始安装 Profile。两者具有相同 endpoint、scope、namespace、版本及镜像锁，观测文件独立留证。不能编辑 checkpoint 或忽略 Profile 变更来通过校验。本轮实际已证明：追加观测的文件被拒绝，保留的原始安装 Profile 能通过 checkpoint 并进入缺少业务凭据的明确拒绝门禁。

依赖准备后，以独立 CA 和私有凭据运行 `opsctl bootstrap oidc-realm --profile /secure/current-resolved-profile.yaml --config /secure/oidc-input.json --secrets-file /secure/oidc-private.json --ca /secure/oidc-ca.pem`。公共输入显式给出 schemaVersion、HTTPS callbackURL、tenantId、username、email、firstName、lastName；私有输入给出 bootstrapUsername/bootstrapPassword/clientSecret/subjectPassword。它复用冻结 realm 的 PKCE/LoA 配置，仅设定确切 callback/origin/client credential，通过固定 Keycloak Admin API 创建 realm 和初始主体。真实 OTP enrollment 和 LoA-2 登录仍是首次 Tenant 前置；初始化旧 realm 时拒绝覆盖。冻结 realm 的实际 HTTPS 初始化、重复拒绝、OTP enrollment、PKCE/nonce、LoA-2 和首次 Tenant 的正式 CLI 链路已通过。命名 Keycloak 管理身份及 bootstrap 身份退役已在隔离初始化目标实际完成；当前完整冷装中的同一路径仍待验收。

命名管理员退役入口分两步，均要求独立 CA、正式 namespace/安装身份、明确 tenant/subject/username 及真实新鲜 RS256、ops-api audience、LoA-2 bearer。管理员公共输入为 schemaVersion=1、tenantId、subject、username；私有输入为 bootstrapUsername、bootstrapPassword、replacementBootstrapPassword。它必须与该安装的临时 Secret 匹配，不能接管其他安装。

```sh
opsctl bootstrap oidc-administrator --stage prepare \
  --profile /secure/current-resolved-profile.yaml --config /secure/administrator.json \
  --secrets-file /secure/administrator-private.json --ca /secure/oidc-ca.pem \
  --token-file /secure/fresh-loa2.token
```

prepare 仅给指定已启用、已 OTP enrollment 的主体授予既有 `ops` realm 的 `realm-management/realm-admin`，不授予 master 管理权限或平台操作 scope。重新完成 OTP 登录并更新私有 bearer 后，以同样输入运行 `--stage retire`。它先验证命名管理员真实管理访问及 master 拒绝，再按 Secret resourceVersion 轮换凭据、移除精确的临时 master 主体，验证旧 token 和旧密码拒绝，重启 Keycloak，并复核命名管理访问。服务故障不等于凭据拒绝；中间失败返回明确 partial 状态，保留新命名身份和数据，不声称完成。隔离目标的实际退役、错误租户/未授角色拒绝、重启及另开连接的新 OTP 登录分别已退出 0，见 `oidc-administrator-live-r1/r2`；这仍不是全冷装证明。退役后不能继续把临时凭据作为运行凭据。

准备外部 `first-tenant.json`，仅包含 `tenantId`、`slug`、`displayName`、`adminSubject`。tenant 与主体由操作者明确选定；不存在默认用户或自动宽范围。Keycloak 已完成真实 HTTPS、tenant membership 和 LoA-2 配置后，把新鲜 bearer 存入私有 token 文件：

```sh
opsctl bootstrap first-tenant --config /secure/first-tenant.json \
  --issuer https://current-issuer.example/realms/ops \
  --ca /secure/independent-issuer-ca.pem --token-file /secure/fresh-loa2.token \
  --profile /secure/current-resolved-profile.yaml
```

`OPS_BOOTSTRAP_DATABASE_URL` 由外部受控环境提供，不传入命令参数或保存到公开命令日志。OIDC 与数据库连接失败明确拒绝。重复初始化返回 `BOOTSTRAP_ALREADY_INITIALIZED`。完成后关闭 bootstrap 会话，后续 Cluster、Source、RoleBinding 与签名 Policy/Recipe/Tool 通过正式 API/Registry 发布激活，保留 revision、digest 和同事务 Audit。

Archive 输入必须显式列出 tenant 与彼此独立的 write/read/protect/cleanup 四类身份。`opsctl bootstrap archive-iam --secrets-file /secure/archive-iam-input.json --output /secure/new-archive-iam.json` 生成固定 bucket、tenant hash prefix 和各自唯一职责的 IAM policy；拒绝跨 tenant、混合职责、宽 prefix 或覆盖现有输出。私有输入和输出均在 Git 外，输出为新建 0600 文件。随后使用正式 environment 输入及新 IAM 启动 SeaweedFS。

每次新鲜 Keycloak LoA-2 登录后，管理写入前还须通过正式 `POST /api/v1/auth/step-up-sessions`（空对象与独立 Idempotency-Key）登记与当前已验证身份绑定的会话。单独取得 LoA-2 bearer 不能替代这个持久会话；缺失时 Legal Hold 等管理入口应返回 `STEP_UP_REQUIRED`，不得以直写会话表代替。Legal Hold 创建返回 `protectionSync: pending` 只表示已登记意图，必须再核对正常 Worker 同步后精确对象版本的原生 COMPLIANCE 保留与 hold=ON，不能仅凭 HTTP 201 宣称底层保护通过。

```sh
opsctl bootstrap archive-bucket --profile /secure/current-resolved-profile.yaml \
  --secrets-file /secure/archive-bootstrap-private.json
```

该入口只访问正式 namespace 的固定 SeaweedFS HTTPS Service，以独立公共信任校验 TLS；要求实际 bucket 不存在，创建并读回 ObjectLock、Versioning=Enabled、默认 COMPLIANCE 365 天。现有或不可读 bucket 拒绝接管。隔离目标的正式 IAM/bucket 初始化、四角色源端允许/拒绝、跨 prefix 拒绝、版本/digest、365 天保护和 Legal Hold 实测已通过，受保护对象和 PVC 继续保留。Archive bootstrap admin 的实际退役与完整 API/MCP 闭环仍待完成。

迁移工具的独立补充包 `pre-sp07-migration-tools-20261005-r1` 已用仓库外独立信任验签，payload=`sha256:f89c2baf713df19a98a4d09e80ee4373732deb2bdbbe97bc1ab9de33f213eb2f`。包内包括实测 Linux/arm64 `db-migrate`、完整 CLI/35 个 forward SQL/Go 闭包源码、SBOM 和原始 notices。源码在无网络、空构建缓存下重建得到字节一致的实测 binary；分别使用 bootstrap 与受限 migration LOGIN 的实际 1→35、重复迁移、降目标不回滚、负目标拒绝已通过。此包仅补充迁移材料，不代表当前 core/deepflow Bundle 已完成，也不分发模型权重。

`--foundation-only` 是显式历史基础安装选项，与 `--business-values` 互斥。它不能证明 SP-04～SP-06 或 investigator 已交付。

完成依赖初始化、公共 workload CA、业务 Secret 与空库迁移后，先创建明确的本地 Graph Lease，并启动 API 初始化阶段：

```sh
opsctl install --profile core --resolved /secure/current-resolved-profile.yaml \
  --bundle /secure/current-signed-bundle --key /secure/independent-trust.pem \
  --business-values /secure/current-business-values.yaml --stage bootstrap-api --offline
```

在 API 阶段之前运行 `opsctl bootstrap graph-leases --profile /secure/current-resolved-profile.yaml --business-values /secure/current-business-values.yaml`。该入口核对真实 cluster UID、正式 namespace/installation-id，创建每个配置 cluster 对应的独立 Lease 和 UID checkpoint。它拒绝已有/不可读对象和混用 Lease；部分失败保留资源。Worker 仍仅有指定 Lease 的 get/update 权限。轮换时保留 bootstrap 身份标签，不授予 Worker create 权限。

`bootstrap-api` 仅运行正式 API，保留显式 SP-04～SP-06 配置和精确网络边界；Worker、Web、investigator 尚不启动。它核对原依赖 checkpoint，并记录 API 阶段的资源 UID、配置摘要、Helm 归属和原依赖/存储身份。通过正式 API 初始化 Cluster、Source、RoleBinding 和签名 Registry 后，将 API 返回的 UUIDv7 Source ID 绑定到显式业务输入（包括引用它的 ingestion bindings），然后运行：

```sh
opsctl install --profile core --resolved /secure/current-resolved-profile.yaml \
  --bundle /secure/current-signed-bundle --key /secure/independent-trust.pem \
  --business-values /secure/api-registered-business-values.yaml --stage business \
  --registration-token-file /secure/fresh-operator.token --offline
```

该 token 文件必须为仓库外私有普通文件。安装器仅经受选定 Kubernetes context 认证的 loopback 隧道读取固定的安装 API SourceRegistration 路由；没有用户 URL、重定向或通用 Fetch 工具。真实 API 的 tenant/type/cluster/backend/revision/status 和精确 Victoria scope mapping 必须匹配。阶段间只允许计划 Source ID 到正式注册 ID 的一致转换及有限探测时间窗刷新；端点、租户、目标、范围、信任、预算和配方不随之放宽。API 资源的 UID、配置或 Helm 归属变化、缺少 Lease checkpoint 或未注册 Source ID 均拒绝启用。它不修改旧 checkpoint，也不接管既有 Worker。SourceRegistration 核对不是事实源 query qualification。

此三阶段源码已通过关键失败与修复回归；当前实际 API 上精确 Source ID 绑定及错误 ID 拒绝已验证。2026-10-06 的 r7 签名包已在独立新 namespace 和空业务库上通过三个正式安装阶段，具体证明边界见下方最新现场记录；全依赖冷导入及完整 R0～R6 验收仍未完成。

此流程尚未完成当前 core/deepflow 的新签名 Bundle、全依赖冷装和完整业务验收。单项初始化或回归退出码 0 不能替代这些门禁。原历史 Transit 恢复缺口只约束历史恢复和受保护清理，不阻断独立新环境安装。

OpenBao 的 Pod Ready 仅用于初始化入口连接进程：当前探针对 sealed/uninitialized 返回 204，不能据此开放业务或记恢复通过。开发环境重启后必须运行 `opsctl openbao status`；若为 sealed，使用该安装独立保管的匹配恢复材料，通过正式 unseal 提交两份不同 share，再携恢复文件执行 status 核对持久配置。禁止在现有 Raft 存储上重新初始化。API、Worker 和 investigator 仍须完成实际 PKI、归档解密和业务验证；CrashLoop 的历史错误与当前恢复结果分别保留。生产 auto-unseal/HA 仍按正式交付条件处理，不能把开发手工解封扩张为通过。

本轮实际回归的归档测试材料必须存入仓库外私有目录，可用 `OPS_TEST_PROTECTED_MATERIAL_ROOT` 明确指定；未指定时使用操作系统用户 cache 下的独立 `ops-protected-integration` 目录，权限必须为 0700。各归档 fixture 使用独立 bind storage，保留容器身份、IAM/TLS 和对象版本，结束时只停自己的进程。含 Audit、Evidence、归档 intent 或依赖/保留引用的测试数据库，以及保留状态不可读的数据库，均不自动删除。无此数据的空测试库也不使用 FORCE。对应实际生命周期 gate 已通过；旧辅助函数的先前删除行为与影响保留在 `fixture-cleanup-protection-finding-r1.json`，不得将此前功能退出码 0 扩张成数据保留通过。

## Metrics-server 正式 addon 入口

Metrics-server 保持独立可选 addon；本轮 R3 要求实际交付。`metrics-addon/v1`
是显式安装输入，不改变冻结的 Deployment Profile/v1。使用当前 Catalog 解析
`metrics-server.mode=bundled`，并在签名包内包含精确官方 OCI、完整对应源码和
当前 `ops-metrics-server-chart`。候选物料不能进入安装入口。

在正式 bootstrap 的 namespace 中，以独立核验的 serving CA 签发
`ops-metrics-server-metrics.<namespace>.svc` 和 `.svc.cluster.local` 的服务证书。
证书、私钥与 kubelet CA 放在仓库外 0600 JSON（`certificate`、`privateKey`、
`kubeletCA`）。公网/宽 CIDR、缺端口、未知字段、错误 SAN/CA/私钥均拒绝。

`opsctl bootstrap metrics-trust --context <actual-context> --config <public-json> --secrets-file <private-json>`
只创建新的 TLS Secret 和 kubelet CA ConfigMap，绑定 namespace 的 installation-id，
拒绝覆盖已有信任。部分创建失败时保留资源供核对，不自动回滚或删除。

`opsctl addon install --name metrics-server --resolved <actual-profile> --bundle <new-signed-bundle> --key <independent-public-key> --config <public-json> --offline`
先核验完整签名包，再核对当前 Node UID/kubelet 端口、Node InternalIP、Kubernetes
Service/endpoint 和实际 API 端口。它仅导入签名包中的官方 image，拒绝采用既有全局
APIService/RBAC/Helm 资源，先安装精确网络策略，再启动单个官方进程。kubelet TLS
和 aggregation TLS 不降级，镜像拉取策略为 Never。

Pod Ready 与 APIService Available 只证明 addon 安装。公网 IPv4/IPv6 独立控制、
真实 Node/Pod UID/时效、source scope/撤权/恢复以及 Inspection→Evidence→API/MCP
链仍须另行完成 R3/R4 实际验收；CLI 回执明确记录此边界。

## 本轮数据库隔离恢复的角色要求

2026-10-05 对 61 个受保护测试数据库实际恢复时，不同初始化超级用户名造成原
`GRANTED BY ops_bootstrap` 授权失败（r1/r2）。r3 使用源集群相同的初始化角色名，
保持初始化角色身份，原 globals 中仅将一条 `CREATE ROLE ops_bootstrap` 替换为已存在
注释；所有 ALTER/GRANT 原文应用，原始备份保留不变。恢复后实际比较角色属性、
SCRAM verifier、成员关系及 grantor，再比较七类数据表的行数与内容 SHA256。

[PostgreSQL 官方 pg_dumpall 文档](https://www.postgresql.org/docs/18/app-pg-dumpall.html)
说明目标初始化角色已存在会引起 CREATE ROLE 冲突；
[官方讨论](https://www.postgresql.org/message-id/671134.1778008247%40sss.pgh.pa.us)
记录不同初始化角色下的 GRANTED BY 恢复限制。本轮验证只涵盖上述具体修复，不
推广成任意角色/集群恢复方案。数据库内容恢复通过不代替对象/密钥/签名 Audit
及 365 天依赖闭包；源数据和隔离恢复副本继续保留。

## 本轮历史现场记录（2026-10-05，未构成最终验收）

当前自有安装为 `ops-pre-sp07-core-r4-20261005`，namespace UID 为 `cdeeb73a-7e37-4090-901b-eadde26df845`。r4 签名依赖安装、空业务库 forward-only 迁移到 35、职责分离数据库身份、真实 HTTPS Keycloak OTP/PKCE/nonce/LoA-2 first Tenant、Victoria 公共信任及 Archive COMPLIANCE 365 初始化均有独立原始记录。随后通过实际安装的 API 注册了 Cluster/Source/RoleBinding，并发布及激活签名 Policy/Recipe/只读 Tool；没有直写业务表或 fixture seed。见 [正式 API 初始化记录](../evidence/pre-sp07-20261005/current-core-r4-formal-api-initialization-r5.json)。源注册返回的 queryCapability 仍为 disabled/unverified，原生范围验证与完整业务链通过前不得提升。

r5 修复了受管 OpenBao 到精确控制面地址的 TokenReview egress，以及 investigator 模型 Secret 与投影 JWT 经 `/var/run` symlink 产生的只读挂载嵌套。实际三类 Kubernetes auth 登录均返回 200，并校验独立 TLS。失败 Helm revision 遗留的旧 model-key 挂载仅在 UID/resourceVersion 校验后移除；未修改存储或阶段 checkpoint。该过程是签名 Chart 现场修复，不能记为新空环境冷装。实际 API、两个 Worker、investigator 和七个依赖曾全部 Ready；Ready 也不等于主链路验收。

正式安装顺序仍有必要缺口：依赖阶段绑定了预先填入的 Source ID，但正式注册 API 分配 UUIDv7。当前现场用 API 返回身份修复了运行配置，保留原 checkpoint，见 [身份绑定记录](../evidence/pre-sp07-20261005/current-core-r4-source-id-binding-resolution-r2.json)。产品必须补齐 API bootstrap、正式 Registry 注册、校验运行绑定、业务激活的阶段流程，不能通过编辑 checkpoint、接受任意身份变更或 seed 绕过。该缺口未关闭，R1 尚未完成。

配置摘要触发 rollout 的新增 Chart 回归已有 red/green 原始记录；对应 r6 新包尚须独立验签及现场回归。首次 r6 构建在实际解包验签时耗尽本地磁盘并拒绝输出有效包，随后发现 OrbStack engine 已停止；不能据时间接近断言引擎停止原因。见 [中断记录](../evidence/pre-sp07-20261005/current-environment-disk-and-engine-interruption-r1.json)。受保护数据库、对象、恢复输入和 key 不在空间处理范围内。历史 r10 与当前 r5 完整签名包继续保留；本轮中间包如采用无损归档，必须先在隔离位置实际恢复到原压缩 payload SHA256，再保留 manifest、signature、归档、独立工具身份和恢复命令。该物料恢复不是历史 Evidence/Audit 恢复。

本轮自有调度故障 Pod UID 为 `87e0dc6f-92b8-44b4-9f91-59e57fc8496d`，namespace 为 `ops-pre-sp07-targets-20261004`。引擎中断前刚创建，原生故障/恢复及业务链仍待观察；完成后只按确切 UID 删除该 Pod，不触及 PVC、Evidence 或保留对象。当前进度见 [r26 账本](../evidence/pre-sp07-20261005/current-readiness-progress-r26.json)。R0～R6、最终完整独立审核和 Git 交付均未完成；原历史 27 条 Evidence 因无匹配 Transit 材料继续阻断对应恢复与原地受保护存储清理，其他独立工作继续。性能为用户豁免，虚拟化延期 disabled/unverified，PyRCA disabled/excluded，未开始 SP-07。

## 本轮最新现场记录（2026-10-06，未构成最终验收）

当前签名包为 `pre-sp07-core-20261005-r7`，payload 为 `sha256:a3a80033c78ed22deea0754a0cee4440efa7b1d4f7d4d8d61558ed3fbb871ab4`，第一方源码归档为 `sha256:fdf202f15480d10a3673a4da7f8d438c3c52af892de831afa3dc6b347c6996a8`。独立信任验签和正式导入均为退出码 0，见 [签名包回执](../evidence/pre-sp07-20261005/current-core-signed-bundle-receipt-r7.json)。模型权重继续由独立的实际 Ollama 服务提供，不在 Bundle 内。

新安装 namespace 为 `ops-pre-sp07-core-r5-20261006`。新 PostgreSQL PVC 上实际验证业务库为空，签名迁移工具完成 0→1→35，迁移、API、Worker LOGIN 分离，迁移重试不回滚；见 [空库迁移证明](../evidence/pre-sp07-20261006/current-core-r5-empty-migration-receipt-r1.json)。新持久 OpenBao、Keycloak realm、真实 OTP/PKCE/nonce/LoA-2 first Tenant、公共 workload CA、Archive 四职责 IAM/TLS/版本化/COMPLIANCE 365、Victoria 独立 TLS/认证均重新初始化。原受保护数据库、对象和恢复材料没有提供这些初始化数据。

`dependencies`、`bootstrap-api`、`business` 三个正式 r7 安装阶段分别退出 0。bootstrap-api 现场只运行 API，见 [阶段回执](../evidence/pre-sp07-20261006/current-core-r5-api-bootstrap-live-receipt-r2.json)。Cluster、三个 Source、明确 operator RoleBinding、签名 Policy/Recipe/五个只读 Tool 均经实际 API 发布激活，Tool 输出使用正式 `tool-response/v2` Schema，见 [API 初始化](../evidence/pre-sp07-20261006/current-core-r5-formal-api-initialization-r5.json)。最终业务输入只替换正式 API 分配的 Source ID，未改 tenant、scope、endpoint、trust 或预算；安装器使用实际认证的 SourceRegistration 进行核对，见 [身份绑定](../evidence/pre-sp07-20261006/current-core-r5-api-source-identity-binding-r1.json)。没有编辑 checkpoint、seed 或采用历史 Worker。Source 注册成功仍不等于源端 query qualification。

Archive IAM 激活时使用滚动重启新增了 StatefulSet 的 `kubectl.kubernetes.io/restartedAt`，严格配置快照因此拒绝首次 API 阶段，原始 [退出码 1](../evidence/pre-sp07-20261006/current-core-r5-api-bootstrap-r1.log) 保留。现场仅在 UID、resourceVersion、确切 annotation 和原配置摘要全部匹配后恢复原签名 StatefulSet 声明，保留新 tenant IAM、PVC 和所有对象，见 [声明恢复](../evidence/pre-sp07-20261006/current-core-r5-archive-signed-config-restoration-r1.json)。重复安装随后通过。正式操作顺序应在 dependencies 前生成完整 tenant IAM；初始化阶段若有显式滚动重启，重新进入阶段前恢复原声明并验证所有配置身份，不能修改 checkpoint 或放宽配置校验。

该新环境仍共享 OrbStack OCI 缓存，不能称为全冷安装；原 OrbStack 全清理、DeepFlow、完整 Metrics 业务门禁、恢复/保留闭包和最终独立审核仍未完成。已有 r4 环境的一次真实调查及 Ledger/Audit/归档解密/持久 SSE 已通过 [窄范围实际门禁](../evidence/pre-sp07-20261006/current-formal-investigation-chain-live-receipt-r1.json)，结果为 `unresolved`、ActionPlan 为空，不代表建议可用性或正式 RCA 准确率通过。新环境的实际主链路、并发调查及恢复验收继续单独记录。性能为用户豁免；KubeVirt/CDI 延期 disabled/unverified；PyRCA disabled/excluded；没有进入 SP-07。

当前 Operator binding 的撤权/恢复入口：`PATCH /api/v1/admin/role-bindings/{bindingId}/status`，请求仅为 `expectedRevision` 和 `status: disabled|active`。用当前租户的真实平台管理员 LoA2 身份及幂等键请求。先查询当前 binding revision；状态更新推进 revision，恢复时必须重新查询并使用新 revision。不能在该请求带 subject/role/tenant/scopes，也不能使用旧 revision 恢复。该入口只操作 Operator；其他角色返回 INVALID_ARGUMENT。step-up 在幂等重放前复核。当前 R9 的真实请求仍返回 404；修复源码的 OpenAPI 版本为 1.4.0，必须生成新签名 Bundle 并完成真实部署回归后才报告此入口交付。见 [ADR-0030](../adr/0030-pre-sp07-operator-role-status-contract.md)。

范围探针须引用确实存在、与指定 UID/namespace 对应的已采集样本及原时间窗。空 Victoria 存储或缺失日志不能证明 scope，运行时继续 SOURCE_SCOPE_UNVERIFIED/partial。首次安装前准备有界真实采集条件，并记录原始事实来源；不能虚构样本、回填当前时间、自动换 UID 或修改初始化回执绕过身份绑定。新准备目标的 startup stdout 不等于原探针目标的日志。真实历史 canary 只证明来源范围，不用于最新恢复或 RCA 判断。
