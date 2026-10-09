# 通用 Kubernetes 离线安装：功能验收记录

当前状态：ARM64 正式安装及必要业务冒烟通过，[最终完整独立只读复审](independent-readonly-review-final.md)在该限定范围内 PASS。amd64 仅完成首方交叉编译与安装准入测试，第三方完整离线包和实际部署未通过、未交付。不能将本记录表述为 ARM/x86 双架构完整验收，亦不替代原始 R0～R6、DeepFlow 或历史恢复门禁。

## 范围与版本

基线 main `03bddf12c7b83a06cd766603cbbaeeef3f5d1644`；代码提交 `3969325`、`ba2191e`、`a06c6f4`、`5fa82ea`，双架构测试和文档 `36eef43`。最终 Git 交付提交、远端一致性和 clean 核对以主代理最终报告为准，本文不循环绑定自身提交。根目录正式文档未修改；虚拟化、PyRCA、SP-07 保持既有边界。用户豁免模型推理和性能，状态为 USER_WAIVED_NOT_PASS。

独立测试目标为 Kubernetes v1.35.5、containerd 2.3.1、Linux ARM64。kind/Docker/OrbStack 仅为本机测试载体，产品入口不依赖它们。节点、cluster UID、环境身份见 [target-identity.json](target-identity.json)。既有 OrbStack 开发安装与受保护历史数据未删除或接管。

## 签名交付配对

- core：`generic-k8s-core-20261008-r2`，payload `sha256:1c0aa3b0573ec6085b09954a1c088a09e6004085c7261b433c66097c2fd3d310`，源码绑定 `ba2191e`，32 项物料；[验签记录](bundle-receipt.json)。
- 安装工具：`generic-k8s-installer-20261008-r2`，payload `sha256:d28316e80e6e580cfc3e295ba385b15690f8da26ca1d667fd15bfba91927a26c`，源码绑定 `5fa82ea`，3 项物料；[验签记录](installer-tools-bundle-receipt.json)。Linux CLI 在签名物料中，Darwin 管理 CLI 使用同源、独立 Ed25519 分离签名的 [清单](management-tool.json) 与 [验证记录](management-tool-signature-receipt.json)。
- core 的 API/Worker 可执行文件与安装工具修复后重编译结果字节一致，见 [源码绑定](installer-tools-source-binding.json)。未替换旧包内容、签名或 checkpoint。
- 独立信任公钥 SHA256 为 `47f3b2279069c4b72c9b1ccb3db05de1e5805677d6922e7b2cc4d2a6f18569a7`；私钥、凭据、恢复材料不在仓库或 Bundle。
- 用户提供的 `/Volumes/vol/aiops/ops-generic-k8s-offline-20261008` 用于公开分发材料和验签暂存，物料迁移逐文件核对一致；[迁移记录](core-signed-bundle-external-relocation.json)。此前 NTFS 路径只读及容量中止均保留失败记录。

## 实际验证

正式 Linux `bundle import` 通过，保留最初空缓存记录、错误 Node UID 拒绝及镜像不变证明、两次真实导入缺陷与修复记录。首次失败为 Docker Hub 引用规范化差异；后续实际 Pod 暴露 import-date 别名导致 CRI 启动失败。修复明确 repository base，保留失败测试与实际事件；仅删除本任务生成且匹配签名 OCI 描述符的错误别名，并刷新专属测试节点 CRI 缓存。[最终导入](node-import-receipt.json)、[实际失败](native-target-cri-alias-failure-events.json)、[范围化清理](owned-failed-import-alias-cleanup.json)、[空诊断命名空间证明](cold-canonical-import-proof.json)。这属于必要修复回归，没有冒充重新完成原 R0～R6 全冷装。

正式三阶段安装退出码均为 0：[dependencies](install-dependencies-receipt.json)、[bootstrap-api](install-bootstrap-api-receipt.json)、[business](install-business-receipt.json)。管理机仅核对所有节点精确缓存，报告 verified/verifiedNodes，不冒充实际导入；Pod 不回退在线拉取。显式非默认 StorageClass 的 PostgreSQL/OpenBao/SeaweedFS/Victoria PVC 均 Bound。

空业务库、35 个 forward-only 迁移与三个受限 LOGIN 角色见 [数据库证明](database-live-receipt.json)。正式身份、归档和 Graph 初始化见 [完整命令序列](formal-initialization-sequence.json)。无 Git 的已认证源目录完成 OpenBao 初始化。真实 HTTPS Keycloak、PKCE、OTP、LoA-2、正式 first-tenant、原子 Audit、重复初始化拒绝见 [实际测试](current-core-r9-oidc-first-tenant-generic-r2-first-r2.log)。正式 API/Registry 创建并激活当前 Tenant、Cluster、Source、RoleBinding、Policy/Recipe/Tool，未直写业务表或使用 seed。

[必要业务冒烟](business-smoke.json)退出码 0：自有 Pod 的真实 Unschedulable 事实进入 Finding/Evidence/Incident；Evidence archived_verified，digest/对象版本有效；角色撤权拒绝和恢复、幂等重放；正式 Job 创建/取消、预算预留归零、持久事件/Audit 数量一致、SSE cursor 续传。investigator 在创建故障与 Job 前暂停，最后恢复同 UID 和完整 spec；模型请求和工具调用为零。此结论不包含真实模型/MCP 调查或 RCA 准确率通过。

## 检查与限制

check-toolchain/check-generated/check-runtime-source/test-security 均退出 0，见 [前期检查](affected-checks.json)。最终 check-generated/check-runtime-source 退出 0，见 [最终源码检查](final-source-checks.json)。最终受影响 CLI/Bundle/Profile/security/contract/E2E 回归退出 0，见 [命令与版本](final-affected-regression.json)。`make test-replay` 退出 0，见 [离线回归命令](offline-regression-commands.json)。

`make check` 退出 2，缺少旧 live fixture 服务/凭据等输入的失败完整保留于 [原始日志](check-with-user-waiver.log)；不能标为整体 PASS。第一次误触性能测试已中止并记录，不以中止冒充性能通过。外盘 exFAT 不用于本机 Go 临时链接产物；该运行工具环境问题的失败和本机临时目录重试保留。

双架构编译和 ELF 格式见 [构建记录](dual-architecture-build.json)，amd64 原始依赖准入缺口见 [Catalog 实际状态](architecture-material-status.json)。多节点行为有测试覆盖，本轮实际目标为单节点。CRI-O、混合架构、Registry 分发、生产 HA/容量、DeepFlow addon、物理硬件现场及历史恢复没有新增通过声明。

## 验收后处置与 2026-10-09 收尾

专属标准 Kubernetes 节点和临时模型转发已正常停止保留，没有删除容器、卷、Namespace、PVC、Evidence、对象或 Transit key，见 [首次处置](test-infrastructure-final-disposition.json)。今早观测到测试节点再次运行，启动主体未知；随后仅对该任务节点关闭自动重启并停止保留，见 [最终处置](test-infrastructure-final-disposition-r2.json)。验收时 Ready 不代表当前仍运行。

原开发环境今早的 API/Worker 出现 OPENBAO_KUBERNETES_LOGIN_FAILED。正式 status 确认 persistent OpenBao initialized/sealed，使用该安装原恢复文件的两个 share 正常解封，没有重新 init、替换密钥或数据。随后原开发环境 11 Pod 恢复 Ready，见 [恢复与原始命令](development-restart-recovery.json)。这不是缺失历史 Transit 材料的旧 27 条 Evidence 恢复证明，不覆盖原历史阻断。

恢复后以真实 HTTPS OIDC/PKCE/OTP 登录退出 0，Source、RoleBinding 与当前开发安装的 archived_verified Evidence 读取均 HTTP 200，见 [登录原始日志](current-core-r9-oidc-operator-login-restart-recovery-20261009.log)及 [实际读取](development-recovery-read-smoke.json)。没有模型请求或业务写入；该读取不代表旧 27 条 Evidence 已恢复。

最终完整交付（含今天恢复及停止保留）由未参与实现的独立审核者复审 PASS，见 [最终报告](independent-readonly-review-final.md)。收尾只读状态见 [最终环境观察](final-environment-observation.json)。
