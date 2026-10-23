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
