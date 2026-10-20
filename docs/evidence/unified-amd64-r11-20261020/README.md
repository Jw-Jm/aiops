# 统一 amd64 r11：安装器 init 恢复语义（2026-10-20）

签名物料 `unified-amd64-20261019-r11`（工具包 `unified-amd64-installer-20261019-r11`），源码 `f4458fd`。仅安装器与文档变化，**运行镜像与 Chart 与 r10 逐字节一致**，因此 r10 的安装仍是受验证的运行基线。

## 为什么只升安装器

r10 轮暴露的真实缺陷不在业务运行时：OpenBao 首次 `init` 的响应可能丢失，服务端已 `initialized` 而客户端没拿到 shares，旧逻辑还会留下一个 0 字节恢复文件，使实例永久不可解封且重试被拒。修复只触及 CLI：

- 请求失败后读 `seal-status` 再决策：已初始化 → `OPENBAO_RECOVERY_UNAVAILABLE`（要求重置专属卷）；仍未初始化 → 清理空文件并允许安全重试。
- 启动时发现**空恢复文件**：仅当服务端确实未初始化才丢弃它。
- 新增 `internal/integrations/openbao/init_recovery_test.go` 覆盖三种情形。

## 验签

工具包经旧受信安装器验证，运行包由新安装器全量复核（102 个文件，payload `sha256:7a35a3ae…`），两包 OpenSSL 分离签名返回 0 → `result: PASS`。回执见本目录两个 JSON（其中 `installationAcceptance: NOT_RUN` 是构建器自检字段，不代表安装未验证）。

## 只读连通核对

用 r11 安装器对已部署的 r10 环境执行 `openbao status`（只读）：

```json
{"state":"ready","initialized":true,"sealed":false,"threshold":2,"shares":3,"progress":0,"version":"2.7.0"}
```

## 边界

- 未重跑三阶段安装：r11 不含镜像/Chart 变化，重装只会重复验证 r10 已证实的运行行为；r10 环境（13 个 Pod Running）保持为验收基线。
- 历史尝试环境 `ops-amd64-unified-20261019`、`r3`、`r4`、`r6`、`r7`、`r9` 的命名空间、专属 PV 与宿主数据目录已在本轮归档证据后回收；既有 core/final/助手环境未受影响。
