# 统一 amd64 r7：TCP/HTTP 验证闭环（2026-10-20）

签名物料 `unified-amd64-20261019-r7`（工具包 `unified-amd64-installer-20261019-r7`），源码 `e5615d3`，独立开发环境 namespace `ops-amd64-unified-r7-20261019`。仅验证非虚拟化开发安装，不代表生产发行；KubeVirt/CDI、Trace、ARM64 仍在范围外。

## 本轮唯一产品改动

运行器命名空间级 profile 的 egress 原先只允许 `api:8084`、`openbao:8200`、kube-dns、kube-apiserver，任何被准入的探测 Service 都连不通，TCP/HTTP 验证在结构上不可能通过。r7 只增加一条**精确规则**（`deploy/charts/ops-platform/templates/sp07.yaml`）：

- 目标：本安装内带 `ops.platform.io/verification-probe: "true"` 标签的 Pod；
- 端口：固定 TCP 8080；
- 未借用 `ops-api`，未引入通用网络策略能力，业务目标与 API 权限不变。

探测 Service/PVC 仍在业务输入中显式准入（`sp07.admittedProfiles[].allowedTargets`），安装后不放宽目标集合（此前用新版本 profile 扩展目标已被 403 拒绝并留证于 `docs/evidence/sp07-20261020/`）。

## 隔离安装

`dependencies → bootstrap-api → business` 三阶段通过；OpenBao init/unseal/configure、租户级归档 IAM 与对象锁 Bucket、空库 43 迁移、真实 OTP + LoA-2、首租户、Graph 租约、真实 API 注册 3 个来源与 13 项签名发布、执行 profile 发布均完成。物料验签见 `core-r7-independent-verification.json`、`installer-r7-independent-verification.json`（旧受信安装器验证工具包，新安装器完成全量 payload 复核，两包 OpenSSL 分离签名返回 0）。

安装期间暴露两个已知前置问题，均按既定做法处理并留痕：

1. r7 使用的是修复前的安装器二进制（仅含 subjectId），OpenBao 缺少 API/Worker 的 `evidence-archive` Transit 权限，首次执行返回 503。用 HEAD（`fd5ee69`）构建的 CLI 执行 `openbao configure-commands`（不带 SSH 用户）补齐后恢复，CLI 修复尚未进入签名包。
2. `sp07.workloadCABundle` 若填 OpenBao bootstrap CA，运行器会 `execution_unknown`；改为 OpenBao PKI 工作负载 CA 后恢复。该字段的正确取值需在后续业务输入准备中固化。

## 真实验证结果（同一 incident，关联 follow-up）

| 验证 | 执行 | 状态 | 退出码 | 裁决 |
|---|---|---|---|---|
| 探测 Service 基础执行 | `01a15700-...` 前序 | succeeded | 0 | — |
| TCP 8080 | `01a15700-f1e3-7422-887a-c5a9e8d3c065` | succeeded | 0 | `resolved` / `tcp_connected` |
| HTTP 8080 `/healthz` | `01a15701-3529-71d6-8799-25c390587bfd` | succeeded | 0 | `resolved` / `http_assertions_met` / 200 / bodyDigest `sha256:8712b72c…` |
| 探测 PVC 基础执行 | — | succeeded | 0 | — |
| PVC 写读 | `01a15701-94eb-78d9-9976-a2fde99add1f` | succeeded | 0 | `resolved` / `read_write_verified` / `cleanup=true` |

裁决取自 `action.output_chunks` 的规范化验证结果，非接口推断。PVC 首次 `probe_unavailable` 是专属卷缺可写 subPath 目录（运行器 uid 65532），补 `0777` 后复测通过；全程未使用业务卷。

## 边界

- 未修改既有服务、数据、PVC/PV、审计与历史失败；r2/r3 尝试环境缩容到 0 但 PVC/PV/数据保留，r4/r6 保留运行。
- `SOURCE_DEGRADED`、`execution_unknown` 的失败尝试均保留原始回执，未重放或改写。
- 本目录仅存必要回执，私钥、令牌与渲染物料留在远程受限目录。
