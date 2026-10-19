# 统一安装配置功能记录

日期：2026-10-19。状态：功能开发完成，运行未验收；复用 `opsctl`、签名 Bundle、现有 Chart 及 `dependencies → bootstrap-api → business` 三阶段。没有新增安装器、迁移或发行许可。

## 业务输入

`installation-business-values/v1` 新增可选 `sp08` 和 `sp06.assistant`。旧输入继续有效，缺省显式关闭 Web/助手；模型 pending 分支只允许关闭助手，故障链不能单独开启。

- `sp06.assistant.enabled=true` 时必须提供 `modelProfileVersion` 和完整 `profile`，可显式设置 `faultChainEnabled`。复用 `modelprofile.Profile.Valid/Reference`，要求 admitted、全部字段摘要匹配、与共享 Holmes 运行端点/API key 引用/timeout 一致。助手有独立 modelID，现有安装模型可用性探针同时检查该 ID 与原调查模型 ID；原调查模型的两项 allowlist 与原预算上限保留，不把本轮接线解释为长上下文质量或模型准入通过。
- `sp08.enabled=true` 时必须提供 HTTPS `publicOrigin`、`tlsSecretName`、`oidcCAConfigMapName`、完整 `oidc`、`service` 与 1～20 条规范化非 /0 的 `ingressCIDRs`。`service` 为 ClusterIP 或显式 NodePort（30000～32767），ClusterIP 不接受 NodePort 字段。
- 统一安装使用冻结 realm 的 `oidc.clientId=ops-web`。`oidc.authority` 必须等于 resolved Profile 的 Keycloak endpoint 加 `/realms/ops`，`proxyHost` 必须匹配其主机；当前 nginx 仅支持 HTTPS/8443 Keycloak，其他端口/路径提前拒绝。API upstream 由安装 namespace 推导，不接受输入覆盖。
- 镜像、任意 Chart overrides 和 `sp08.release` 不在业务输入中。Web 镜像只取已验签 Bundle 的 `platform-web`，调查服务取 `holmesgpt`。UI release 的 version/payload digest 由验签后的 Manifest 填充，signature 留空；不是浏览器独立验签或新的发行证明。

Web 示例（并入既有完整业务值，不能独立用于安装）：

```yaml
sp08:
  enabled: true
  publicOrigin: https://ops.example.invalid:30443
  tlsSecretName: ops-web-tls
  oidcCAConfigMapName: ops-web-oidc-ca
  oidc:
    authority: https://ops-keycloak:8443/realms/ops
    proxyHost: ops-keycloak
    clientId: ops-web
  service:
    type: NodePort
    port: 8443
    nodePort: 30443
  ingressCIDRs: [192.0.2.0/24]
```

端点、客户端、CIDR 和端口须替换成实际已登记环境值；示例域名不证明可连通。OIDC realm 初始化应显式设置 `clientMode: browser-pkce`，callback 等于 `publicOrigin + /auth/callback`，不传 clientSecret；另登记同源 `/auth/silent-callback`，回调/origin/PKCE/LoA-2 仍按现有初始化流程核对，不自动重写 realm。

## 阶段和回执

在 dependencies 前确定完整能力输入并保持三阶段一致。现有依赖与 API bootstrap 回执的完整业务摘要（包括注册后稳定摘要）自动覆盖 Web、Profile 和故障链开关；更改这些字段会在资源操作前拒绝旧回执。不是在既有安装上任意切开关的通用升级入口，后续激活须按现有受控重装/升级准备流程处理。

依赖阶段仅渲染平台网络策略；bootstrap-api 不渲染 Web ConfigMap/Deployment/Service。business 阶段才安装 Web，并额外等待 `deployment/ops-web` rollout。Pod Ready 仅用于安装检查，不表示登录、助手、故障链或真实来源验收成功。

business 前只用 Kubernetes presence 模板核查 Web Secret 的 `web.crt`、`web.key` 和独立 OIDC CA ConfigMap 的 `oidc-ca.pem`，不读取私钥内容、不生成自签证书、不关闭证书验证。运维方仍须预置可信证书、匹配私钥/SAN/完整链和正确 CA；实际 TLS 与浏览器验证留待部署阶段。

## 检查与限制

本地测试覆盖旧输入、助手 Profile/端点/引用拒绝、pending 模型隔离、镜像配对、OIDC/架构边界、证书存在检查、两类旧回执拒绝配置变更，以及三阶段真实 Helm 渲染。模型 Profile 摘要改为包内标准库计算，移除对 Graph 的间接依赖以避免 Installer 导入循环；既有 Go/Python 共享摘要向量保持不变。

Web Dockerfile 仍复制 amd64 库；启用 Web 的非 amd64 目标在接触集群前拒绝。DEL-002 保持 open，后续需逐架构制品/闭包与运行准入，不能以 API/Worker 交叉编译替代。没有构建或签名新 Bundle、构建镜像、远程部署、访问真实 Kubernetes/数据库/模型、浏览器验收、升级回滚演练或人工质量代填。远程性能问题仅作记录；虚拟化与 Trace 延期边界不变。

本轮本地检查通过：`go test ./internal/bundle ./internal/modelprofile`、`make test-contract`、`make check-delivery`、`go build ./...`、受影响包 `go vet`、嵌入模型探针 Python 语法检查、`git diff --check`。助手模型可用性回执另测缺失/错配/成功及探针 UID 清理，均使用 mock，不是真实模型调用。
