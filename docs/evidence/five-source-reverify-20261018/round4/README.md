# 页面追问超时根因修复、Node/PVC 适用矩阵与长上下文补轮（2026-10-18 第二轮）

本轮只做三件事：修复并回归页面追问 `get_assistant_context` 超时的确定性代码缺陷；补齐 Node/PVC
适用矩阵的判定与不适用标记；在真实 DeepSeek 上做有界长上下文验证。未扩容、未放宽任何门禁、
未改来源资格、未重跑已通过的三窗口/联合/进程恢复。

## 一、页面追问 SOURCE_TIMEOUT：确定性代码缺陷，已修复

### 证据（保留库 `sp03_mig_efc27b7504304e1bb6e924ce2a574ea3`）
- 三次 `get_assistant_context` 步骤耗时：419ms、158ms、**3007ms**（失败那次正好撞上 MCP 网关
  的固定 3s 上限，`FailStep(SOURCE_TIMEOUT)`）。
- 同一秒 `browser-r1.log` 记录 `Graph Lease renewal unavailable stage=lease_read`，随后
  `internal analysis query rejected 409 STALE_CONTEXT`；即追问上下文读取期间租约续约正在
  API server 往返中。
- 代码：`internal/graph/lease.go` 的 `Tick` 全程持有互斥量并跨越原生 GET/PUT，`Confirm` 在同一
  互斥量上无限等待；`internal/httpapi/assistant_source_capabilities.go` 的能力读取又用 3s 上限，
  与所属工具的 3s 完全相同。

### 修复（共享路径最小改动）
- `internal/investigation/tools/deadline.go`：工具期限集中为 `DeadlineFor(name)`（
  `query_deepflow` 9s，其余 3s），MCP 网关改为引用它。
- `internal/httpapi/assistant_source_capabilities.go`：能力读取上限改为 2s，严格小于所属工具
  期限；超时只降级 `sourceCapabilityStatus=unavailable`，已提交历史照常返回，并记录
  `assistant context capability unavailable` 诊断日志。
- `internal/graph/lease.go`：互斥量改为单槽 gate，续约仍独占，但请求路径 `Confirm` 最多等待
  300ms 即失败关闭（`ErrNotReady`），不再继承一次续约的原生往返延迟；所有权仍以 Lease 文档
  本身为准，续约循环继续为其它请求提供栅栏。

### 回归（先失败后通过）
- `TestConfirmDoesNotInheritRenewalLatency`：续约 GET 阻塞 1.2s 时，`Confirm` 必须在 700ms 内
  失败关闭。修复前实测阻塞 1.25s 后成功（`--- FAIL: ... 1.25s`）；修复后 2.0s 内通过。
- `TestCapabilityDetailDeadlineFitsContextTool`：断言能力上限 < `get_assistant_context` 工具期限，
  且 `query_deepflow` 仍为 9s、其他工具仍为 3s。

### 真实页面复跑（未通过，原因是来源身份，不是本次缺陷）
| 轮次 | 结果 | 具体阻断 |
|---|---|---|
| ui-r5 | FAIL | 登录成功但 `/api/v1/auth/session` 403：采集租户 `3fb34947` 与 OIDC 令牌租户 `2984df90` 不一致 |
| ui-r6 | FAIL | 用 OIDC 租户重新采集（r4）后登录/会话通过；资源解析轮询超时，Playwright 的 API 助手因自签叶证书无 CA 文件而 `UNABLE_TO_VERIFY_LEAF_SIGNATURE` |
| ui-r7 | FAIL | 关闭 Node 侧证书校验（与浏览器同等信任级别）后，Go 侧在启动前即失败：`native client identity not qualified`，DeepFlow 控制器容器 ID 与原生不一致 |
| ui-r8 | FAIL | 控制器身份短暂一致后再次重启，仍无法映射 |
| ui-r9 | FAIL | 专属 client canary 于 12:07 第 4 次重启（其命令是 24h 睡眠，`Completed` 后自动重启），控制器身份再次漂移，且 controller `/v1/info` 在 2.5s 内无响应 |

结论：本轮页面闭环的实际阻断是**专属 canary 的容器身份漂移 + DeepFlow 控制器元数据滞后**，
属于环境/来源身份问题，不是产品统计或权限缺陷。已按要求不扩容、不放宽身份/时间/来源门禁，
未反复重跑。修复后的追问路径由上述两条确定性回归覆盖，真实页面的再次闭环需要等控制器重新
登记当前容器（或重建 canary 并重新采集窗口），本轮不谎称通过。

## 二、Node/PVC 实时适用矩阵

| 读取项 | Pod | Node | PVC |
|---|---|---|---|
| 原生图/关系 | 已实测（联合 r2） | 合同允许（cluster-scoped） | 合同允许 |
| `metrics.*` | pod-restarts 已实测 | 仅 `metrics.node-ready`；无容器指标 | 无适用指标数据集 |
| `logs.records` | 已实测 | 不适用（显式标记） | 不适用（显式标记） |
| DeepFlow L4/L7 | 已实测（HTTP/TCP 三窗口） | 不适用（`network_not_applicable_for_Node`） | 不适用（`network_not_applicable_for_PersistentVolumeClaim`） |
| `resource_history/findings/changes` | 已实测 | 适用（本轮仅计划器验证） | 适用（本轮仅计划器验证） |

- 计划器新增显式标记：无适用指标数据集时 `"<kind>_metrics_not_applicable"`，非 Pod 目标追加
  `logs_and_pod_metrics_not_applicable_for_<kind>`，网络段继续 `network_not_applicable_for_<kind>`；
  不会借用任何 Pod 查询。
- 回归：`test_node_and_pvc_state_each_inapplicable_read_explicitly`（逐项断言没有
  `metrics.pod-*`/`logs.records`/`query_deepflow` 被借用到 Node/PVC）、
  `test_node_keeps_its_admitted_metric_dataset`（Node 只使用 `metrics.node-ready`）、
  `TestOperationalRequestKindsAndExplicitNodeScope`（合同：Node 需 cluster-scoped，PVC 允许）。
- 未执行：Node/PVC 的真实 assistant 任务。原因：专属测试的会话授权为单 namespace，Node 需要
  cluster-scoped 授权；专属 namespace 内没有 PVC 对象。未扩大授权、未用 Pod 结果代替。

## 三、上下文与模型分析质量（有界）

- 真实 DeepSeek `deepseek-flash`，密钥只引用远程受保护文件，不导出、不提交。
- **容量**（仅容量，不称质量）：单请求输入实测 **917,288 tokens** 被接受（prompt 2,020,760 字节），
  该次 `max_tokens=4096` 导致输出 `stop_reason=max_tokens`，因此不作为质量结论。
- **保留**（有界、真实载荷）：已提交上下文 + 已提交 QueryResults + 700 条本轮真实原始观测，
  输入 119,962 tokens，输出 2,895，`stop_reason=end_turn`，31.5s。逐项校验全部通过：
  三窗口 fromUnix/toUnix、HTTP 故障 5/1、TCP 100/0/+100、HTTP 恢复 0/5/−5、
  6 条 `missingEvidence` 原文、以及“已提交证据 vs 原始观测”的区分。
- 配置上限 1,000,000 / 输出 8192 与“实际接受 917K、质量已验 120K”分开记录；未声称满 1M 验收。
- 人工模型质量评审保持 `pending`，未代填。

## 四、本轮回归与制品

- 本地：`go build ./...`、`go vet ./...` 干净；`internal/graph`（含新增用例）、`internal/httpapi`、
  `internal/investigation/...`、`internal/analytics`、`internal/evidence`、`internal/app`、
  `internal/integrations/deepflow`、`test/contract`、`test/security` 全部通过。
- Python：`services/investigator` 93 项通过；`scripts/deepflow-dev` 7 项通过。
- 远程制品：`reports/windows-r4.json`（三窗口 PASS、stable）、`reports/fault-window-r4.json`、
  `reports/http-r4.json`、`live-source-r4/receipt.json`（132 周期、3168 指标、0 错误）、
  `reports/long-context-verification.json`，以及 ui-r5..r9 失败回执。
- 代码版本：本轮改动基于 `d870a23`，仅动 `internal/graph/lease.go`、`internal/investigation/tools/deadline.go`、
  `internal/investigation/mcp/server.go`、`internal/httpapi/assistant_source_capabilities*.go`、
  `internal/httpapi/assistant_tools.go`、`services/investigator/src/investigator/fault_chain.py` 与其测试。

## 五、未完成与下一步

1. 真实页面追问后的闭环：待 DeepFlow 控制器重新登记 canary 当前容器（或重建 canary 并新采窗口）
   后，用同一 Go 页面测试与 Playwright 脚本重跑；本轮不拼接历史通过结果。
2. Node/PVC 真实任务：需要 cluster-scoped 会话授权（Node）与专属 namespace 内 PVC 对象；未获批前
   只保留合同与计划器判定。
3. 模型质量：需要人工按 `humanQualityReview=pending` 填写；1M 容量仍需更长上下文与更大输出上限的
   有界验证，本轮只证明到 917K 输入接受与 120K 保留。
