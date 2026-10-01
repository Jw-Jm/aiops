# 当前 Bundle 的 Task 2.7 复跑前置条件

本文件为可评审操作范围，不是执行授权，也不宣称安装通过。用户本轮第八节要求：现有共享集群、core 服务和数据库上的覆盖、删除、缓存清空或有状态重装必须另获明确授权；Victoria、OpenBao、受保护 PVC、用户数据和其他 release 不得覆盖或删除。

## 已准备的交付物

- 受测实现/配置：`6355c6863df74e357699ba5ec13cbeb613c3c258`；运行时 Go 源码与 `3928403` 相同，之后只补齐默认 Profile TLS 和针对性测试。
- Bundle：`artifacts/bundles/sp010203-core-arm64-20261001-delivery`，Linux/arm64，23 项物料、69 个完整校验文件。
- payload SHA-256：`9285bb1402adc3fc2a022b3e18570b791586c89b4dd902b8e5653f2d51a97cbb`。
- 独立开发验签根指纹：`50629825fcb0cc3a3f4a65af7af4b5a516ec08b90e94e6936d1d14d62a66be99`；私钥仅在外部 0600 文件。该开发根不能替代生产操作者的信任根。
- 当前工具链/check/generated/security、26 项真实集成、2 项真实断网上游重放通过；当前 Chart API/Worker/依赖资源的 server dry-run 通过。

## 当前实际阻碍

共享 `orbstack/ops-system` 已有 `ops-dependencies` revision 1、`ops-platform` revision 2，以及受保护的 `ops-core` revision 2、`vmalert` revision 1。安装器会拒绝采用这些已有 release，不能直接执行完整 E2E 而当作本轮创建的资源。

已有 Keycloak Service UID `bc1c7f03-9348-490a-a18b-bb45c13f3b22` 仍为 HTTP/8080；现有 SeaweedFS Service UID `08a87dc7-87bd-49a3-9e29-9f43a9ac4505` 的 8333 没有 HTTPS appProtocol。当前 Bundle 要求实际 TLS、独立 CA、租户 IAM 和真实数据库运行角色。仅给旧 HTTP 端点换 URL 不能通过。

已有 PVC 必须保留：

- `data-ops-openbao-0`：`c6d6fd30-b259-4edd-ab4d-457a86c9680f`。
- `data-ops-postgresql-0`：`fa694502-a948-4fa7-b072-46eb2f4ce8bb`。
- `data-ops-seaweedfs-0`：`cacf92e6-8352-47e0-913c-4f9ff641eb34`。

未清理共享镜像缓存，未覆盖上述服务、Secret、数据库或 release。旧 Task 2.7 验收记录保留，但其旧二进制/Chart/配置不能证明当前 Bundle 的安装与重装。

## 授权或独立环境需明确的范围

如果选用当前共享 OrbStack，应明确允许以下定向操作及服务中断；未授权前不执行：

1. 只对 `ops-platform` 与 `ops-dependencies` 做逐资源所有权校验后的 release 清理/重装。`ops-core`、Victoria、vmalert、其他 release、PVC 和用户数据保持保护；不改变 OpenBao 的持久状态或恢复材料。
2. 只处理当前 resolved 安装计划选中的镜像引用和 manifest digest 缓存。先列出实际引用、在用容器/Pod 与阻碍；遇到受保护使用者停止。导入前必须证明引用及 digest 均不存在，不能用预热缓存获得通过。
3. 在外部安全保管的操作者材料支持下配置独立的 `ops-keycloak-tls`、`ops-seaweedfs-tls`、`ops-seaweedfs-iam`、`ops-platform-runtime` 和公共 `ops-platform-bootstrap`。既有材料的替换另需明确授权；不把私钥/凭据复制到仓库、Bundle 或报告。
4. 数据库验证只使用可确认归属的新独立数据库和 API/Worker-only LOGIN；迁移使用 migration-only LOGIN。不在现有用户数据库上运行迁移。先确认冻结的 Keycloak realm/PKCE/ACR、S3 bucket/retention/tenant IAM 和已有 OpenBao runtime policy；需要修改受保护实例时停止并提出具体范围，不借用 root token 作为运行凭据。
5. 完成安装、实际公网拒绝/内网允许对照、release 范围清理/重装、健康与能力检查，以及保护对象前后 UID/数据检查。记录当前源码/Profile/Catalog/Bundle/环境绑定并保留失败；失败后继续在授权范围内修复。

或者提供一个可执行相同 OrbStack 驱动的专用验收环境，避免共享环境上述操作。不能用 kind、Fixture 或普通单测替代正式方案要求的 OrbStack 空缓存运行验收。

完成前继续保留 Task 2.7 和总体未通过状态。KubeVirt/CDI 及专属能力延期、未验证；不启用相关环境变量，不操作其 CRD、Operator、VM、VMI、DataVolume。

## 用户授权后的状态

用户随后明确回复“授权”，上述限定操作范围已获授权。2026-10-01 已完成范围内可独立执行的 TLS、独立数据库/运行 LOGIN、全量 migration、新归档 bucket/租户配置、公共 bootstrap 和 release 所有权准备。

只读核查发现受保护 OpenBao 已初始化但 sealed（HTTP 503），且所选 Keycloak 镜像有两名范围外停止容器使用者。遵循第 2/4 项的保护例外，尚未解封、改 OpenBao 配置、清缓存或卸载 release。仅解封/只读核查及保留停止容器的定向缓存操作已提交补充授权请求，等待用户回复；具体对象、证据和最新未通过状态见 [全面复审记录](sp010203-review-20261001.md#定向授权后的实际准备与保护例外)。
