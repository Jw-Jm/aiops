> 2026-10-28 r35：30471已更新Web/API/Worker、source3双任务namespace；30472更新Web/API，原r34 Worker保留。采集凭据故障/恢复、范围收窄/撤权及实际归档读回见evidence/gpt-history-recovery-r35/README.md。旧容器停止保留，数据库70无迁移，仍非正式Bundle发布。

> 2026-10-28：最新独立源码冷启动及重启证据见evidence/gpt-coldstart-r34/README.md；30472为新namespace/新库，原30471未变。下方历史配置按各轮边界读取。正式Bundle/离线验收仍未通过。

> 2026-10-27现状：本文r10～r13段落为历史边界；最新独立native运行已经验证，见文末r24与对应证据。正式不可变Bundle/离线安装仍未验证。

# r10原生模式部署配置：本地实现与接续

本轮只编辑/构建/离线渲染；未连接远程、未启动数据库/Kubernetes/模型/业务浏览器，没有新在线地址或部署。55/56仍未执行，最后独立验证库为15453/schema54，不推定当前远程状态。原生账号全栈切换P2-05尚未通过。

## 已贯通的配置

platform chart的runtime.humanAuthMode为唯一模式来源，默认oidc保留旧安装兼容；原生目标必须明确native。API/Worker均接收HUMAN_AUTH_MODE与固定NATIVE_AUTH_TENANT_ID。原生API另接NATIVE_AUTH_PUBLIC_ORIGIN及OPENBAO_MFA_TRANSIT_KEY，读取既有OpenBao地址、独立CA和ops-api专用投射JWT；不使用文件密钥回退。

Web在native模式只发布auth.mode=native和release元数据，不生成OIDC客户端/代理、不挂OIDC CA，/oidc/明确404；API不再要求OIDC issuer或SSL_CERT_FILE，Worker本就不验证人类OIDC。Web使用现有HTTPS证书、同源API代理、CSP与流式缓存禁用配置；渲染后的server和runtime配置参与Pod checksum，配置变更触发滚动更新。OIDC兼容模式保留独立CA验证代理。

原生配置需要非零tenant UUID、无路径的合法HTTPS origin（端口1～65535）、现有MFA Transit key名称、独立OpenBao CA及workloadIdentity.enabled=true。API与Web的publicOrigin必须相同。JWT复用已有openbao-identity只读投射卷，audience=openbao、有效期3600秒、由Kubernetes轮换，不启用默认无限制SA token。API按ops-api机器角色认证；Worker没有MFA Transit key配置，实际密钥隔离仍由OpenBao角色/策略实施，不能由环境变量缺失推导权限已隔离。

供后续核对的配置片段（不是可安装制品，不含已准入镜像/Profile/真实CA）：

```yaml
runtime:
  humanAuthMode: native
  nativeAuth:
    tenantId: <核对后的独立任务tenant UUID>
    publicOrigin: <与sp08.publicOrigin一致的HTTPS origin>
    mfaTransitKey: <已独立授权的MFA Transit key名称>
  openbaoAddress: <获准且与Profile匹配的OpenBao HTTPS地址>
  openbaoCABundle: <独立核对的公开CA PEM>
workloadIdentity:
  enabled: true
sp08:
  enabled: true
  publicOrigin: <与runtime.nativeAuth.publicOrigin一致>
```

不得填入密码、MFA种子、恢复码、OpenBao token或静态JWT到values/ConfigMap/报告。现有ops-api Transit策略需独立核对最小密钥权限；本轮不创建新密钥或放宽策略，也不修改共享存储或依赖服务。

## 执行配置列表修正

C-134的action.Service.ListProfiles此前仍直接查询legacy角色表，原生管理员会被拒绝。现改用共享authz.RequireAdmin在同事务校验真实当前会话/版本。独立operator分支也校验当前原生角色，再沿原有graph.EffectiveTx按目标范围过滤；无角色、撤权、身份不匹配与DB失败不返回列表。不复制旧角色，不因具有管理员角色授予命令执行权。C-139专用退役接口保持不变。

原有C-134分页缺口单独保留：OpenAPI有cursor/limit，但当前处理器/服务尚未实现相应分页与meta。调用方AdminPage与CommandComposer均传分页参数；本轮只修原生授权，不宣称该接口全部合同通过。真实目标范围投影、并发撤权及浏览器仍待验。

## 本地验证与保留失败

- Go action/authz/app及完整test/contract通过；包括原生管理员、撤权、独立operator、会话主体不匹配、DB失败的事务分支单元测试。
- Helm离线测试覆盖native（有/无SP06）、OIDC兼容、缺tenant/CA/key/投射身份、非法origin/端口与API/Web origin不一致。解析实际YAML，检查无重复env、OpenBao audience与到期、Web无SA token/旧CA、Worker无MFA key配置。
- 构造器测试只证明无需OIDC配置和未知模式拒绝，不是进程冷启动。
- 守卫MFA集成已增加原生管理员执行配置读取/撤权拒绝断言，Linux集成二进制交叉编译，不运行数据库。
- 首次新Helm用例SP06 fixture缺SP05，按既有SP06依赖补齐fixture后通过；失败日志gpt-native-deploy-r10-chart-fixture-failed.log保留，未降低门禁。

## 未完成路径与恢复顺序

正式Bundle安装器internal/bundle/runtime_values.go仍要求Keycloak endpoint/CA，business_capabilities.go仍绑定Web OIDC，business-values合同及Profile依赖闭包也需一致演进。**本轮chart支持不能推出opsctl正式安装已支持native。** 不通过绕过安装器或删除准入门禁完成切换。此前opsctl来源材料锁不一致失败仍独立保留。

Job/Worker操作域的原生身份来源/user/version持久化及跨进程复检未完成；graph内部签名请求/Worker调用不能靠浏览器session猜测或回退legacy。历史记录读取与当前范围复检、原生授权并发/审计失败回滚、匹配版本集合、无OIDC真实冷启动、完整HTML视觉与P3 SL-049样板仍未完成。

恢复远程后先核对mssc-direct获准主机、目录/端口/库所有权和当前源码/制品，保留所有旧轮次。任务库55/56前滚与最新守卫集成需有效任务JWT及真实OpenBao，至少15分钟测试超时。新Web/API配置不能部署到受保护旧15452/schema49轮次；只在任务专属环境按匹配版本验证，迁移/映射及撤销事实只前滚保留，不回退旧OIDC凭据或整库快照。

## r11接回更新

C-134的HTTP分页/meta已接入，按当前授权可见结果的profile ID+版本定位，退役或失权锚点会400，界面需回首页刷新；r10分页未实现描述仅保留为历史。既有目录读取方式未下推为DB keyset，大目录性能未验证。OIDC+SP04动态PKI但无SP06时的API OpenBao地址/CA接线已补齐并离线回归。55还修正旧失效角色意外恢复问题，但SQL回归未执行，必须使用r11最新源码前滚/验证，不能复用旧55构建当作当前通过。详见evidence/gpt-review-r11/review.md。

## r12/r13接回更新（2026-10-23）

上文r10/r11“安装器尚未接通/55、56未执行/远程断连”只代表历史轮次。任务15453已前滚至56，55/56已冻结；真实集成与新预览看evidence/gpt-resume-r13/result.json，不能从旧记录推断当前通过。

r12正式安装器源码接入business-values可选humanAuth：mode=native、tenantId、publicOrigin、mfaTransitKey。原生部署固定一个租户，sp06.tenants必须只有同一UUID；API与启用Web的publicOrigin必须完全一致。Resolved Profile中keycloak必须明确mode: disabled；不隐式采用或删除现有身份服务。未指定humanAuth仍保留历史安装兼容，不表示正式原生页面提供OIDC登录选项。

native路径不读取Keycloak endpoint/CA、不要求Web OIDC CA配置；仍验证独立OpenBao/Archive CA与Registry公钥，仍要求合格且摘要匹配的OCI/Chart材料。禁用Keycloak只在完整验证其归档闭包后跳过缓存导入；其他核心依赖不得借此禁用。业务安装回执摘要绑定humanAuth，不允许在既有阶段回执后静默切换模式/租户/密钥。

环境引导输入可显式humanAuthMode=native，只接收OpenBao/Archive CA、Archive bucket、Registry公钥四项信任，以及PostgreSQL、Archive auth/IAM/TLS、OpenBao TLS五项独立Secret；拒绝混入Keycloak秘密和OIDC信任，不接管既有命名空间。现有默认OIDC兼容输入不变。

business阶段--registration-token-file在native模式读取仓库外私有文件中的原始opaque会话token，用__Host-ops-session Cookie执行固定Kubernetes端口隧道内的只读GET。它不接收密码、不自动登录、不生成角色，也不把此机制用作写请求CSRF豁免；过期/非管理员/首次改密会话应失败。无调用方提供的URL、代理或重定向，原有分页/大小/租户边界不变。该CLI路径只过本地边界测试，实际正式安装未验。

仍不能宣称原生无OIDC冷启动：bootstrap first-tenant命令尚依赖旧OIDC证明，需补受控原生首租户引导；签名Bundle来源摘要问题也未解决。当前全bundle测试OPA来源材料不匹配失败已保留，不改来源锁或签名门禁来消除失败。Job/Worker显式原生authority/user/version、历史业务归属读取和全量像素/SL-049仍待实施。


## r24：独立native源码制品运行（2026-10-27）

当前预览 https://192.168.0.63:30471/login；namespace `ops-native-v12-r24-20261026`，远程目录 `/data/ops-v12-native-visual-r24-20261026`，独立PG15454/schema70。当前manifest为api-pod-final.json与worker-pod-final.json，二进制bin/ops-r24-api-final、bin/ops-r24-worker-final；Web容器ops-v12-native-r24-web-r4。精确来源、哈希、Pod UID与测试见evidence/gpt-native-visual-r24/source-final-2.json、runtime/final-environment.json与README.md。

Bao已有专用key通过`opsctl openbao configure/status --native-mfa-key`显式配置和只读校验；用当前ops-r24-final-opsctl，勿用早期旧CLI。API/Worker/reviewer使用600秒投射token，真实同Pod自动续取已验证；Worker无MFA key权限。首次租户回执已存在，禁止重跑一次性init/bootstrap/prepare。恢复Bao沿原实例解封，不重新初始化/生成key；恢复凭据仅任务private。迁移恢复采用前滚，数据库CREATE权限只作用本任务库，禁止修改共享角色/库。

这是现有精确镜像内只读挂载新二进制的源码canary，未形成新签名OCI/Bundle/PVC/HA验收；审计Worker实际运行不表示SP06业务dispatcher已配置。只在已核对任务namespace/目录/端口内恢复，保留原30467～30470及历史数据库、镜像、失败记录。当前账号是独立管理员，不自动拥有operator；业务权限需单独授权及真实来源资格。


2026-10-27 r25前滚：30471 Web已为`ops-v12-native-r25-web-r5`/`web-r5`，r24 Web-r4停止保留；API/Worker和数据库不改。来源/恢复见evidence/gpt-dispatcher-r25/README.md与web-runtime.json；r24段落仍是对应时点的历史证据。


2026-10-27 r28：原生真实审计业务Worker已启用，覆盖r24/r25“仅审计归档Worker、SP06未配置”的旧现状描述；当前API/Worker/调查器及Web-r6恢复manifest与配置见evidence/gpt-worker-r28/README.md。仅本任务受信DeepSeek，原生权限/固定Profile/签名与mTLS不变；正式OCI/Bundle仍另验。


2026-10-27 r29：30471前端当时为ops-v12-native-r29-web-r9/web-r9；r28业务API/Worker/调查器保持。登录失败与MFA失效恢复版本及故障注入范围见evidence/gpt-login-r29/README.md；旧Web容器/制品保留，禁止为恢复UI回滚原生身份数据。

2026-10-27 r30（当前，含返修）：30471当前前端为`ops-v12-native-r30-web-r14`（`web-r14`，index SHA256 `8465ddec2b4632b7cc5ac518f39506255c9a65b17b7a58e085fd217377075ba8`，归档 SHA256 `39bd606ef502affab37df9eb88fbdc1f2c5640381301f9bbfd3075ad1acc4c4b`），r9/r10～r13停止保留。API/Worker/调查器仍r28，PG17.11/15454、schema70不变；本轮无迁移、无模型调用。来源/恢复/逐状态证据见evidence/gpt-auth-r30/README.md、repair.md与runs/web-runtime-r14.json。

r30返修边界（安全前滚/恢复）：
- 前端回滚只需`docker start`旧容器并停占30471者（host网络），不涉及数据库/身份数据；禁止为恢复界面把已撤销的会话/因子数据回灌。
- 截图不得包含因子秘密：证据图片一律经`evidence/gpt-auth-r30/harness/safe-capture.mjs`探测后产出；otpauth URI/恢复码/动态码在屏时只记录机检断言，不产图。旧任务已隔离17张`mfa-*`图片（见quarantine清单），不得复制回仓库或再次展示。
- 因隔离前无法逐图判定，9个`r30.accept.*`验收身份已全部经管理员UI重置MFA（`mfa_reset_required=t`、活动登记0、未用恢复码0、活动会话0）；`v12.r24.admin`、`r24.member.*`密码/MFA/角色未改动。需要继续验收时用新独立身份，不复用旧因子。
- 身份与安全语义不回滚：不通过放宽因子/CSRF/版本/作用域校验来"修复"验收；任何身份语义变更必须先交回方案。
- 数据恢复：本轮无迁移，无需前滚或恢复脚本；如数据库从备份恢复，需重新执行本轮验收（会话/挑战/因子状态与备份时间点不同）。

2026-10-27 r31（当前）：30471 前端仍 `ops-v12-native-r30-web-r14`（index `8465ddec…`）；API/Worker/调查器仍 r28 二进制（`8fe8d7bd…`/`735a9044…`），PG17.11/15454、schema70 未改，本轮无迁移。新增变化只在本任务 namespace：SP04 集群采集与任务专属只读身份。

- 集群与来源：`platform.cluster_registrations` 一条（`79014400-9453-4cb4-9678-0fae47981d79`，kubeadm v1.36.3，endpoint `https://192.168.0.63:6443`，描述在任务 OpenBao `secret/data/ops/r31-cluster/endpoint`）；`platform.source_registrations` 一条（revision 2，scope 仅该 UID + `ops-native-v12-r24-20261026`）。均为真实管理员 step-up 写接口登记，非 SQL 插入。
- 只读身份：SA `ops-r31-sp04-read`；ClusterRole `ops-native-v12-r24-20261026-r31-sp04-read` 仅 `get/list/watch` 17 个核心 Kind（无 secrets、无写动词）；Role `r31-sp04-lease` 仅 `r31-graph-lease` 的 `get/update/patch`。令牌以 `kubectl create token --duration=86400s` 写入 Secret `r31-cluster-read`（与集群 CA 一起挂 `/etc/r31`，0440/0644）。到期后必须轮换，否则采集停止；API 登录与授权数据不受影响。
- 运行时：configmap `r28-runtime` 的 `sp04.json` 增加 `Clusters[0]`（Tenant/ClusterUID/SourceID/BackendLogicalID/Endpoint/CAFile/TokenFile/LeaseNamespace/LeaseName/SourceRevision/QPS 8/Burst 20），`Sources` 仍为空。Pod manifest 为 `r31/api-pod-r31.json`、`r31/worker-pod-r31.json`（r28 版本 + `/etc/r31` 挂载）。
- 采集：Worker 的 SP04 采集 17 个 GVR 全部 list/watch 就绪，`platform.graph_ownership` epoch=1 续约，资源实体写入 `platform.resource_entities`/`resource_observations`；被授权 operator 经 `/api/v1/resources` 读真实资源。
- 回退：`kubectl apply -f r31/api-pod-before.json`、`r31/worker-pod-before.json`，并把 `sp04.json` 恢复为 `r31/sp04.before.json`；不涉及数据库/身份数据回滚。
- 已知观察：读请求可能落在 2s 租约续约与 6s 归属镜像过期之间，返回 503 `GRAPH_NOT_READY`（worker `stage=lease_before_query`），重试即恢复；不得为消除该现象放宽作用域或改租约 SQL。
- 仍未验证：正式不可变 OCI/Bundle、离线安装与切换（本任务无合格签名物料，OPA 来源材料不一致保持原失败）；当前栈是既有镜像上的源码 canary，不是 chart 发布。

## r33 当前运行与恢复（2026-10-27，优先于旧r31/r32回退建议）

入口仍 https://192.168.0.63:30471/login；API/Worker 已换为 `/data/ops-v12-native-visual-r24-20261026/r33/ops-v12-r33-api` 与 `ops-v12-r33-worker`，Pod清单在该目录 `api-pod-r33.json`/`worker-pod-r33.json`，SHA见 `evidence/gpt-authz-r33/runtime-final.json`。Web-r14/调查器r28、PG schema70不变，无新迁移。API和Worker分别只重建任务Pod，不涉及生产。配置仍保留kubelet600秒自动投射令牌、逐请求读取、任务namespace及既有六类cluster只读元数据，用户admin/member密码/MFA/角色不变。

证据 harness `run-integration.py` 只能在mssc执行，固定15454任务实例，创建独立随机库；含审计者保留，秘密从remote private读取且不输出。不要将其中fixture历史数据理解为真实旧库迁移。ConfigMap r33-rotation-proof及失败/审计数据库保留。

恢复优先对r33前滚修正；必要时只恢复经核对的本任务二进制和Pod命令，保持当前原生数据库、撤销/授权状态、投射凭据和受限RBAC。不按旧r32回退段恢复r31全局RBAC或静态24h令牌，不恢复旧用户凭据/历史身份状态，不重放数据库快照。r32二进制已知仍有本轮修复的问题，不能称已验证的等价恢复版本。正式不可变OCI/Bundle/离线安装、独立无OIDC整栈冷启动仍未通过。
