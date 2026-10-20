# SP-07 执行链恢复（2026-10-20）

环境：`192.168.0.63`，namespace `ops-amd64-unified-r6-20261019`。仅验证当前非虚拟化开发安装；不代表新签名 Bundle 或完整阶段验收。

## 故障和修复

1. `ops-api-workload`、`ops-worker-workload` 缺少 `evidence-archive` Transit 加解密权限，导致提交执行在 `Protector.Seal` 失败，事务回滚，HTTP 兜底返回 `503 SOURCE_DEGRADED`。相同 ServiceAccount 认证成功但加密实际返回 403，见 [诊断](transit-permission-diagnosis.json)。
2. 拆出 `ConfigureCommandExecutionTransit`；`opsctl openbao configure-commands` 不带 SSH 用户参数时仅补齐 Transit，显式 SSH 参数保留原行为。用新 CLI 对当前安装初始化两次，均退出 0；读回验证 SSH 角色及调查身份权限未变，见 [权限修复](sp07-transit-repair-20261020.json)。API/Worker 镜像未更换。
3. 解除 503 后 Job 创建成功，但 Runner 的 `workload-ca.pem` 误用了 OpenBao HTTPS bootstrap CA。首次执行保持 `execution_unknown`，见 [原始失败执行](sp07-base-execution-transit-20261020.json)。将 ConfigMap 该单一字段按 resourceVersion 和原值前置条件替换为与 API identity 一致的工作负载 CA，见 [CA 修复](sp07-runner-ca-repair-20261020.json)。

## 实际验证

重新评估、确认并创建独立执行 `01a156bd-8c47-7ca0-89d0-63661c19d532`：POST 返回 202，Job/Runner 完成，state=`succeeded`，exitCode=0，stdout=`Bound`，outputComplete=true。见 [完整 API 回执](sp07-base-execution-ca-20261020.json)。

只读 SQL 复核：成功执行恰有一次 attempt，ack 已消费，claimed/completed/archived 时间齐全；两次提交的 prepare Ledger 均 completed。审计包含一次 `command.claimed`、一次 `command.completed`、两次 `command.output_archived`，原失败执行未重放或改写。Post-check 为 `inconclusive`，不宣称故障恢复。

代码检查：`go test ./internal/integrations/openbao ./cmd/opsctl` 通过；测试覆盖基础权限扩展、重复调用、保留已显式配置的 SSH 权限及未知策略漂移拒绝。Linux/amd64 CLI 构建成功，`git diff --check` 通过。

## 后续安装注意

本轮未重签或修改原 Bundle、checkpoint、PVC/PV 和受保护数据。CLI 修复尚未纳入新的签名分发包。

修正的后续业务输入保存在远端仓库外 `/opt/ops-verification-20261019-1a7c7da/install-r6/business-values-sp07-transit-20261020.json`，用于后续正式安装物料准备；原 `business-values-base.json` 保留。重新应用旧输入会重新引入 Runner CA 错配。仅验证了原 PVC 读取执行链，未扩展 TCP/HTTP/PVC 文件写读验收。

## 验证型 follow-up（2026-10-20 追加）

在成功的基础执行 `01a156bd-8c47-7ca0-89d0-63661c19d532` 之上补做三条关联验证执行，均真实派发 Job、真实裁决：

| 验证 | 执行 | 状态 | 退出码 | 裁决 |
|---|---|---|---|---|
| PVC 写读 | `01a156c3-0180-707e-a201-1cbddda7c5ce` | succeeded | 0 | `resolved` / `read_write_verified` / `cleanup=true` |
| TCP 8080 | `01a156bf-b4e1-7b2b-8fca-09f2ca375278` | failed | 1 | `not_resolved` / `connection_refused` |
| HTTP 8080 | `01a156c0-3ad4-73df-9cf5-7e48989a06ca` | failed | 1 | `not_resolved` / `connection_refused` |

**PVC 写读通过**：运行器在专属卷 `ops-verification-probe`（2 GiB local PV）内创建随机会话文件、fsync、回读校验一致并清理，`cleanup=true`。首次尝试为 `probe_unavailable`，原因是运行器 subPath `.ops-verification` 不存在且不可写；在该专属卷内创建 0777 目录后复测通过。未使用业务卷。

**TCP/HTTP 未通过，原因是既定策略边界，不是连通性缺陷**：

1. `ops-sp07-k8s-namespace` 的 egress 只允许 `api:8084`、`openbao:8200`、`kube-dns:53`、`kube-apiserver:6443`；专用探测 Service 的 8080 不在白名单内，实测为 `connection_refused`。
2. 尝试以新版本 profile 把 `ops-api` Service 加入 `allowedTargets` 后重签发布，被准入上限拒绝（403），说明安装后不能扩大目标集合。
3. 连通性对照：从同命名空间 Pod 访问该 Service `10.96.220.33:8080` 与后端 Pod `10.244.0.48:8080` 均成功，HTTP 返回 200，排除探测目标自身故障。

**结论**：若要在验收中真实覆盖 TCP/HTTP，必须在**准入输入**层面解决——要么在业务输入中把受控的 Service 目标纳入 admitted profile，要么提供与运行器 egress 白名单一致的探测端点（例如把专用探测端点放在允许的端口/标签组合上），或在产品侧提供 profile 级网络策略。当前签名物料下这两项标记为未通过，不宣称通过。

明细见 [验证裁决](sp07-verification-followups-20261020.json)、[PVC 执行回执](sp07-pvc-verify.json)；TCP 目标扩展被拒的过程见 [进度](sp07-tcp-verify-progress.json)。
