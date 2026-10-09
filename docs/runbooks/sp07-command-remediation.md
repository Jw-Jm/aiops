# SP-07 非虚拟化人工命令处置

阶段授权见 ADR-0032，人工处置模型见 ADR-0003，物料分发见 ADR-0033。KubeVirt/CDI/VM disabled/unverified、PyRCA excluded；没有 SP-08/SP-09 授权。

## 启用与安装

先用正式签名 Bundle 校验、离线 import 及现有开发安装的滚动更新流程。迁移按数据库 Runbook 的受控 migration login 前进到 00037；00036/00037 均为增量结构和最小权限，不重新初始化数据库。保留当前安装、Secret、租户数据、归档、原 PKI 与恢复材料。

`installation-business-values/v1` 的 `sp07` 字段进入正式 installer，Chart 默认 `sp07.enabled: false`。启用时声明 `sp07-runtime/v1`、已准入的精确 Runner image、租户与集群、四类 Profile、凭据和网络目标、host onboarding 报告及信任文件。业务值 Schema、Bundle verifier 与 installer 会拒绝不一致的 Runner image。只有受控一次性 Job，没有常驻 Runner Deployment。

`kubernetesCredentials` 的 Role/ClusterRole 必须是已核验的目标权限。namespace Profile 使用 namespace ServiceAccount；cluster Profile 使用单独 cluster ServiceAccount，并仍需 step-up。不能因为 AST 分类低风险就省略 RBAC、SSH principal 或 NetworkPolicy。示例验收仅授权自有 PVC，以及修复时对一个自有 PV 的 get/patch；不代表通用集群管理员权限。

OpenBao 初始化入口 `opsctl openbao bootstrap` 支持显式 `--command-ssh-user` 配置专用 `ops-sp07-user`/`ops-sp07-root` 短期签名角色及 API/Worker Transit 权限。必须使用现有受控初始化身份，凭据和恢复材料留在仓库外；调查 Agent 的 PKI/只读策略不增加执行、Transit 或 SSH 签名权限。

## 接入 Linux 主机和 Profile

目标必须是任务专属隔离 Linux 环境。先独立核验 host key、SSH CA 公钥和 `TrustedUserCAKeys`、principal 到本地账户映射、普通用户/root/sudo 边界。不能关闭 host key 校验，不能把共享宿主机当隔离目标。锁定 transport 的远端解释器为 `/usr/bin/python3`，接入时须核验其存在并指向真实受审 Python。

原始接入证据在仓库外制作签名 `host-onboarding/v1` 报告，经准入清单与当前 LoA2 admin 的 `POST /api/v1/admin/host-onboardings` 入库。随后签名 `execution-profile/v2`，由 `POST /api/v1/admin/execution-profiles` 发布；修改生成新版本。`POST /api/v1/admin/execution-profiles/{id}/versions/{version}:retire` 停用旧版本并保留历史记录。报告过期或 host/principal 不匹配时 fail closed。

Profile 固定 allowedTargets、clusterUid/namespace、工具 image digest、networkPolicyRef、credentialRef、principal、最长 900 秒和最多 10 MiB 输出。真实 TokenRequest 使用 Kubernetes 要求的最小 600 秒且不超过 Profile 上限；SSH 证书 TTL 不超过实际执行超时。没有静态私钥 fallback。admin 不继承 operator。

## 正式 API 闭环

1. 调查通过既有 HolmesGPT、标准 MCP、Evidence 和 Go Validator 提出建议。模型标识符在校验后转换为平台建议 UUID，原标识保留在 extensions；只产生 ActionPlan 和摘要审计，不产生确认或执行。模型退化、证据不足或不返回建议时，操作者仍可独立处置。操作者建议可用 `POST /api/v1/action-plans` 记录，来源由服务端固定为 operator；accept/dismiss 不授权执行。
2. 操作者输入实际 Bash 原文和目标、Profile 版本、timeout/output 参数，调用 `POST /api/v1/commands:risk-assess`。UTF-8、无 NUL、最大 64 KiB，Shell 仅 bash。AST 只提示风险，不改写原文、不提升 Profile。
3. `POST /api/v1/commands:risk-acknowledge` 使用 assessmentId 和返回的 executionRequestDigest。高权限必须先有真实 Keycloak LoA2 重认证及 `POST /api/v1/auth/step-up-sessions` 平台会话。绝对和空闲期限均为一小时，执行事务内复查撤权、期限及原子 touch。
4. `POST /api/v1/command-executions` 提交实际命令和 riskAcknowledgementId。确认五分钟有效、单次消费；确认、幂等 Ledger、Transit 密文、Execution、审计同事务。原文任何字节、目标 UID、租户/主体/Incident/ActionPlan、Profile/Policy/risk 版本或参数变化都需重新确认。同一键完整重放返回同一记录，不重新执行；同键不同请求冲突。
5. `GET /api/v1/command-executions/{id}` 读取状态；`GET .../{id}/events` 通过 Last-Event-ID 持久续传。输出全流统一 seq、每 chunk ≤16 KiB，去重且拒绝缺口，最多 10 MiB，超限明确截断。stdout/stderr 共用有序混合流归档，首尾预览分流；归档保存 Transit 密文和明文/密文 digest，审计仅摘要/引用。归档核验后，完成及归档都满 24 小时才清理数据库 chunk；过期输出游标返回 410 CURSOR_EXPIRED，归档保留 365 天。
6. `GET .../{id}/post-check` 返回最新授权事实的 resolved/not_resolved/inconclusive。规则复用签名 Recipe 与 Evidence；退出码不是修复判据。当前原生规则支持 PVC Bound 和 Node Ready；未接入可信规则/事实源的 Linux 目标返回 inconclusive。源退化不能伪写 resolved。未修复时可以关联下一条建议，仍重新输入和确认命令。

旧 v1 请求省略新增字段时，只能从同一主体的不可变确认恢复精确绑定，不重新选 Profile 或使用默认参数。旧 approve/reject/execute 建议路由不产生权限。

## 取消、接管和不确定执行

`POST .../{id}:cancel` 在 prepared 时安全取消；发出后只能请求终止，副作用未证实时保持 execution_unknown。Job 使用 UID 前置条件删除。每个 execution 只有一次 dispatch attempt 和一次 claim；两个 Worker 可竞争和接管对账，不能自动重发任意 Bash。

Job 丢失、发送结果未知、claim 后崩溃、输出/完成回调丢失或超时均进入 execution_unknown。晚到回调不把 unknown 变成成功，重复完成回调不增加事件或刷新完成时间。操作者先检查最新目标事实和已归档输出，明确判断副作用，再通过新的确认和独立执行记录处置；不要重放旧命令试探是否执行。

Runner 内临时明文、kubeconfig、SSH 私钥/证书与 Ansible private data 在内存卷中，结束时清理，Pod TTL 清理后内存卷销毁。实际命令不在 PodSpec、环境变量、事件、describe 或 Runner 日志中；它仅通过 mTLS、单次 claim 进入 Runner。SSH host key 校验始终开启，固定 builtin shell transport、不允许用户 Playbook/inventory/插件/become 参数。

## 本轮验收边界

原始证据和 Task 账本位于 `docs/evidence/sp07-20261009`。ARM64 为真实滚动部署和有限业务验收；amd64 仅源码/构建兼容，部署 UNVERIFIED。真实 llama3.1:8b-16k 功能测试不代表 RCA 准确率，模型未生成建议时如实记录。十并发、模型吞吐/P95/持续压测/容量及机器扩容均为 USER_WAIVED_NOT_PASS。

历史完整 amd64 离线包、DeepFlow、缺失历史 Transit 恢复继续保留，不自动扩大为本轮前置。历史上游 replay 的缺失离线 SDK 镜像与未受影响的 20K Graph 测试超时单列失败；不删除断言、不调整正式阈值、不冒充生产容量或原 SP-02～SP-06 全部重新验收通过。
