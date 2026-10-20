# 统一 amd64 r10：无手工修补的三阶段安装与 TCP/HTTP/PVC 闭环（2026-10-20）

签名物料 `unified-amd64-20261019-r10`（工具包 `unified-amd64-installer-20261019-r10`），源码 `53bae79`，独立开发环境 namespace `ops-amd64-unified-r10-20261019`。仅验证非虚拟化开发安装；生产发行、ARM64、KubeVirt/CDI、Trace/PyRCA 仍在范围外。

## 为什么需要 r10

r9 的实装暴露了一个必然结论：`sp07.workloadCABundle` 弃用、Runner 改从身份 Secret 投影工作负载 CA，这些改动同时落在 **Chart 与运行镜像**上。r9 只更新了 Chart，API/Worker 镜像仍是旧构建，遇到新增的 `identitySecret` 字段直接报 `SP07 configuration invalid` 并 CrashLoop。

r10 因此用 `53bae79` 重建了 `platform-api` 与 `platform-worker`：

| 镜像 | 摘要 |
|---|---|
| platform-api | `sha256:494817ba463482a5959c1c3e10968b3e28db1affc458dbdbe2cec0be5d4878a7` |
| platform-worker | `sha256:48d3700cd7595781e5384821b07e8fa38aaf172ada0fe2b55832fedd661a92ad` |

Worker 的 K8sGPT 与法务声明层从既有镜像原样复用，摘要断言为 `sha256:e6ce7073…ae4b66a4`（未重打包依赖）。构建回执见 `native-build-receipts.json`。

## 本轮验证目标：零手工修补

相对 r7（当时需手工执行 `openbao configure-commands` 并手工修改 Runner CA ConfigMap），本轮三个缺陷已在代码中固化：

1. `openbao configure` 内置 Transit 授权（`21e301b`），且重复执行不再误报 least-privilege drift（`53bae79`）。
2. Runner 的 `workload-ca.pem` 由 API 身份 Secret 投影（`4df038e`），不再需要操作者预先填写一个尚不存在的 PKI CA。
3. 策展侧不再需要任何 `kubectl patch`。

安装过程中的人工动作仅限两次**环境级恢复**（非产品绕过），均留痕：

- OpenBao `init` 响应丢失使恢复文件为 0 字节（本环境专属 PV），按真实 PV 路径清空该实例数据后重新 init/unseal/configure，恢复文件 209 字节，configure 一次通过。
- business 阶段首次因 investigator 调度等待（内存请求已近上限）触发 Helm 超时；缩容同节点上已被取代的 r4/r6/r7/r9 后回滚到 bootstrap 版本并重跑，`ops-platform` rev5 部署成功、13 个 Pod Running。

## 三阶段与闭环结果

`dependencies → bootstrap-api → business` 全部通过；OpenBao、租户级归档 IAM 与对象锁 Bucket、空库 43 迁移、真实 OTP + LoA-2、首租户、Graph 租约、真实 API 来源注册与签名发布均完成。物料验签见 `core-r10-independent-verification.json` 与 `installer-r10-independent-verification.json`（旧受信安装器验证工具包；新安装器全量复核运行包；两包 OpenSSL 分离签名返回 0）。

正式 binding 摄入 finding（`POST /api/v2/findings:ingest` → 200），correlator 生成事件，随后在同一 incident 上执行关联验证（裁决取自 `action.output_chunks`）：

| 验证 | 裁决 |
|---|---|
| 探测 Service 基础执行 | succeeded / exit 0 |
| TCP 8080 | `resolved` / `tcp_connected` |
| HTTP 8080 `/healthz` | `resolved` / `http_assertions_met` / 200 / bodyDigest `sha256:8712b72c…` |
| 探测 PVC 基础执行 | succeeded / exit 0 |
| PVC 写读 | `resolved` / `read_write_verified` / `cleanup=true` |

明细见 `r10-verification.json`。

## 边界与遗留

- 未修改既有服务、数据、PVC/PV、审计与历史失败；r4/r6/r7/r9 已缩容到 0 但数据保留，r10 为当前验证环境。
- 助手/故障链仍为 **NOT_RUN**：不存在经真实人工评审的 `admissionState=admitted` 模型 Profile，不代填、不伪造。
- 已观察但未修复的运维缺陷：OpenBao `init` 偶发响应丢失（三次中三次出现在首次 init），表现为 server 已 initialized 而客户端恢复文件为空；建议后续在 CLI 侧对 init 响应做确认或提供可重入的恢复路径。
- PVC 写读依赖运行器 subPath 目录可写（uid 65532）。本轮由准备步骤创建 `0777` 的 `.ops-verification`；该前提应写进后续验收准备脚本。
