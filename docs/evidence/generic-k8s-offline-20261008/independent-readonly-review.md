# 独立只读审核

审核者：`/root/functional_readonly_review`。主代理落盘，审核者未参与实现、制包、部署或业务验收，未修改文件或环境、未运行测试/构建、未调用模型。

## 审核者结论

**PASS，限定为本轮 ARM64 通用 Kubernetes/containerd 安装功能、实际安装及必要业务冒烟，以及 amd64 首方交叉编译和准入守卫。**

该结论不代表 amd64 完整离线交付、双架构实装、生产资格或原 R0～R6 全面通过。确认缺陷和必要证据缺口：本次限定范围内无未关闭项。

## 完整审核范围与依据

1. 完整检查 `3969325`、`ba2191e`、`a06c6f4`、`5fa82ea`、`36eef43`，覆盖 containerd 驱动、CLI、Profile、StorageClass、无 Git bootstrap、模板、ADR-0031 和 Runbook。节点本地导入先校验 cluster/version、Node UID、架构、运行时及 socket 中实际运行容器见证，再导入已验签 OCI；管理机逐节点核对精确引用并保留 UID 检查。未增加 SSH、kubectl exec、任意远程执行、在线拉取或新常驻分发服务。报告区分 verified 与 imported，Pod 保持 Never。

2. Docker Hub 引用规范化、CRI 日期别名和显式非默认 StorageClass 问题均保留 RED、GREEN、实际失败或安装证据。最终 importer 使用规范仓库和明确 base-name；错误 UID 在 mutation 前拒绝。别名清理限定本任务签名 OCI 描述符及单独记录的自有 CNI 前置，没有全局 prune、删卷或删除其他镜像。单测替身没有冒充实装，正式 CLI 导入、Pod 启动及业务验收分别提供正向证明。

3. 审核者直接使用独立信任公钥，对 core、installer-tools、Darwin 管理清单进行只读 OpenSSL detached signature 核验，均退出 0。Core `generic-k8s-core-20261008-r2` 源码 ba2191e，payload `sha256:1c0aa3b0573ec6085b09954a1c088a09e6004085c7261b433c66097c2fd3d310`；Tools `generic-k8s-installer-20261008-r2` 源码 5fa82ea，payload `sha256:d28316e80e6e580cfc3e295ba385b15690f8da26ca1d667fd15bfba91927a26c`。两份源码归档各 4752 项绑定内容摘要一致；Darwin 二进制摘要、源码材料 digest、签名清单配对正确。独立检查 API/Worker OCI manifest/config/layers 摘要闭包及 Linux/arm64 配置，镜像进程精确匹配绑定二进制。修复后 API/Worker 重编结果与 core 字节相同；最终源码差异仅对应独立工具修复与后续架构测试，无业务源码漂移。

4. dependencies、bootstrap-api、business 三阶段退出码均 0，使用同一 core 身份和受校验业务输入。空数据库、35 个 forward-only 迁移、三个职责分离 LOGIN、无 Git 已认证源目录的 OpenBao 初始化、实际 HTTPS Keycloak/PKCE/OTP/LoA-2、first-tenant、API/Registry 激活及重复初始化拒绝有原始证据。未使用 seed、直写业务表或改 checkpoint。缺失幂等键失败保留，随后正式接口重试通过。

5. 自有 Pod 实际 Unschedulable 进入 Finding/Evidence/Incident，归档 archived_verified，摘要/对象版本/保留有效。角色撤权后 Evidence 403，恢复后 200，摘要/版本保持，幂等重放验证。正式 Job 创建/取消、零残留预算、持久事件/Audit 各 2 条、SSE 完整读取 2 条和 cursor 续传 1 条通过。investigator 在故障及 Job 创建前暂停至无 Pod，最后恢复原 UID 和完整 spec；模型和工具调用均零。不证明模型/MCP 调查或 RCA 准确率。

6. 审核者独立只读 GET：测试目标 Kubernetes v1.35.5/containerd 2.3.1/ARM64，Node UID 与账本一致；11 个业务 Pod Ready，API e39896f6…52908、Worker 43db93ed…6517c 的 runtime digest 与签名 OCI 对应，五个 PVC 均 ops-offline-storage/Bound。原 OrbStack 也独立 GET 确认 11 Pod Ready，原 API UID 和 R13 digest 保持。后续停止保留必须报告“验收时 Ready、现已停止保留”，不能继续称当前运行；受保护对象、卷、历史解密材料须保留。

7. 工具链、生成物、源码闭包、安全、最终受影响 CLI/Bundle/Profile/security/contract/E2E 及 replay 的退出 0 有记录。聚合 make check 退出 2，缺少旧 live fixture/凭据等输入，原始失败保留，不能改写整体为 0。性能误触中止、容量/外盘工具环境失败和重试保留，没有删除断言或降低门禁。

## 未完成与排除项

- amd64 仅首方 opsctl/API/Worker/db-migrate 交叉编译和架构拒绝测试通过；第三方 OCI、Python/native、SBOM/许可证/源码闭包的独立准入、签名包及实际部署尚未完成，不是双架构完整交付。
- 实际多节点、CRI-O、混合架构、Registry 分发、生产 HA/容量没有新增通过。
- 性能/Ollama 推理为 USER_WAIVED_NOT_PASS。DeepFlow、物理现场、原历史恢复及完整 R0～R6 没有新增通过。
- KubeVirt/CDI 延期 disabled/unverified，PyRCA disabled/excluded，未进入 SP-07。
- Git 提交、普通 push、远端一致性和工作树 clean 由主代理在落盘后完成，不由本报告提前证明。

## 非阻断整理

唯一建议：账本 G2 completionCondition 曾残留 actual initialization pending，应同步已由正式三阶段和初始化证据验证。主代理已更新，无源码或测试变更，不需重跑。

2026-10-09 收尾新增环境重启恢复与停止保留记录另交审核者只读复核；不覆盖或回写昨晚观察。
