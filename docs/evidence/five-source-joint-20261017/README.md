# 真实五来源联合调查闭环 — 2026-10-17 开发与验证记录

续验及 r16 根因更正见 [本轮闭环记录](closure/README.md)；以下保留上一轮原始报告。

范围：ADR-0041 故障链调查增强（非虚拟化）。运行环境 192.168.0.63 独立开发实例，
本地仅编辑与编译。不涉及 KubeVirt/CDI、Trace、PyRCA、生产发布。

## 一、实际根因与修复

### 1. DeepFlow 空总体被当作“已观测的 0”（本次主根因）
- 根因：`internal/analytics/network.go` 在 DeepFlow 汇总查询返回 0 行时仍产出
  `Row{current,0}` + `Exact=true` + `Status=complete`，下游 R6 会写出
  “未观察到 5xx”这类反向结论；采集空洞与“窗口内确无流量”不可区分。
- 修复：空总体不再产出数值行，`Exact=false`，显式
  `<label>_empty_observation_population` 警告，`Status=no_data`；总体统计
  （observations/responses/5xx）在空总体下仍完整落盘（`observations` 恒在）。
- 归因修复：`internal/investigation/fault_chain_context.go` 中空总体缺口原先
  被归属为 `complete_collector_coverage`（引导重跑查询的错误方向），现按实际
  原因记录 `nonempty_observation_population`。
- 回归：`TestNetworkEmptyPopulationIsNotAnExactObservation`（修复前必失败）、
  `TestNetworkEmptyWindowKeepsComparisonAndTrendHonest`（compare 单侧空、trend
  部分桶空、合同校验）、`fault_chain_context_test.go` 归因断言（修复前失败）。

### 2. Victoria compare 编译在采集窗口前回看不足
- 根因：`internal/analytics/victoria.go` 要求 compare/trend 的故障窗前 5 分钟
  在资格覆盖内（`From-5m >= CoverageFrom`）。此前采集轮次的冻结窗口距采集起点
  仅约 3 分钟，四个 `execute_analysis_query` 全部 `SOURCE_DEGRADED`。
- 修复（运行编排）：r6 轮采集延长到 660s，冻结窗口推迟到采集开始后 340s，
  覆盖 5 分钟回看（证据：window-proof-r6 PASS、queries=4 全部成功）。
- 未改产品判据。

### 3. 身份栅栏与源读取时延上限在负载单机不足
- 证据：身份解析（2 次原生快照 + 每容器 3 次标签查询 + controller）实测空闲
  ~1.3s，测试负载下多次 2.5s/3s/4s 超时（joint-r2/r3/r4/r5/r10/r11 日志），
  L4 范围验证在 `endpoint_identity`/`final_native` 阶段反复超时。
- 修复：`evidence.Query.TimeBudget` 上限 3000→8000ms（默认 8s）、
  `SummaryBatch` 3s→8s、内部 analysis/evidence 调用 4s→9s、`query_deepflow`
  工具读取 2500→9000ms、SP04 来源证明尝试 5s→12s。字节/行/账本预算不变，
  栅栏语义（前后原生快照、controller 信封、DB 授权）不变。各处附注释与实测依据。

### 4. 租约 Confirm 的瞬时失败摧毁已确认所有权
- 根因：`internal/graph/lease.go` `Confirm` 对传输失败/响应解析失败也调用
  `InvalidateOwner()`，一次 API 抖动即失效所有权，级联放大后续读取失败。
- 修复：传输失败与不可解析响应仅“本次读取失败关闭”，不撤销所有权；非 200、
  holder/epoch/renewTime 不符仍撤销。回归
  `TestLeaseConfirmTransportFailureKeepsConfirmedOwnership`（修复前失败）。

### 5. 独立对账探针的自证与归因缺口（独立复审发现，全部修复）
- 复读不一致时清空原始行导致误判采集黑洞 → 保留原始行 + `count_unstable_late_rows`。
- `namespaceObservations` 把 pair 自身计入 → 拆分 `pairObservations` /
  `namespaceOtherObservations`（排除 pair）。
- 三窗口探针缺复读确认、异常丢报告 → 复读一致才 PASS、`stable`、
  `previousMeasurements`、`sourceErrors`，异常不再丢报告。
- `ingestLagSeconds` 把确认开销计入 → 拆出 `confirmSeconds`。
- `--wait-seconds` 非硬上限 → 每次请求超时受剩余 deadline 约束。
- LIMIT 截断无诊断 → `group_budget_exceeded`。
- 新增 `test_fault_chain_window_probe.py`（5 条判据逐项弱化验证）与 README 边界文档。

## 二、真实运行证据（192.168.0.63，专属 fault-chain-20261014 canary）

- 采集 r6（132 周期 / 3168 指标 / 2635 日志 / 0 错误 / 11 分钟，digest
  `sha256:2061834f...` 为 r5、r6 见 receipt）：`artifacts/live-source-faultchain-r6/receipt.json`
- HTTP 对账：PASS，1000 响应 / 50×503，观测点 `s`，ingest 10s + 确认 10s 一致，
  `artifacts/reports/http-proof-r6.json`
- 三窗口对账：PASS（stable=true，polls=1，无源错误），基线 1% / 故障 5%+100 TCP
  refused / 恢复 0%，`artifacts/reports/window-proof-r6.json`
- DeepFlow 来源资格：`sp04/deepflow/v1` 验证行写入（joint-r13 库 12:48:49）
- 联合运行 r16：16 个收集步骤全部成功，4 个 Victoria compare（pod-restarts、
  logs.records、pod-memory、pod-cpu）完整提交并通过 `evaluate_fault_chain`，
  `artifacts/joint-r16.log`
- 采集空洞证据（保留）：keepalive-r4-1/2 `collector_blackout_downstream_of_agent_or_ingest`
  （namespace 全无 L7）、keepalive-r4-3 `count_mismatch`（计数不稳定）。

## 三、已完成 / 未完成验收项

已完成：
- DeepFlow 非空采集与独立对账机制（oracle 独立、fail-closed、可归因）。
- Kubernetes / VictoriaMetrics / VictoriaLogs 三来源真实读取与已提交数字、
  证据、失败记录可读；撤权、身份替换、窗口边界校验随既有测试通过。
- 解释/追问机制所需的确定性收集与已提交结果一致性（`evaluate_fault_chain` 成功）。

未完成（本次范围未闭环）：
1. **网络聚合未进入本轮收集计划**：收集的 `query_deepflow` 读取按最小权限以
   `Resources=[target]` 执行，返回行按设计标记 `peerScopeLimited`，
   `deepflow.NetworkRelations` 拒绝据此投影 communicates_with 边（投影日志
   `edges=0`），规划器无可选 Pod 对端 → 网络聚合查询未发起 →
   `context.EvidenceRefs` 为空 → 测试 `evidence=0` 断言失败。
   这是设计层缺口：图关系投影的唯一入口（evidence 读取路径）与收集的最小
   授权范围冲突。候选方案（需裁决，未实施）：
   a) Worker 在来源资格验证成功后，以其自身命名空间范围（绑定已授权范围，
      不扩大权限）周期性执行一次 GetNetworkDependencies 并投影关系；
   b) 收集轮次显式申请一次“关系读取”窗口，冻结身份与窗口并在账本中单列。
2. 因此本轮未运行：真实模型解释轮、补证追问轮、真实浏览器页面闭环。
   外部 DeepSeek 未使用；内网模型路径未验证。

## 四、独立复审

- 第 1 轮（code-explorer 子智能体，只读，52 次工具调用）：确认无阻断问题，
  提出 4 项应修 + 8 项建议；除第 12 项（既有 degraded+Rows 合同隐患，非本次
  引入，留档）与第 13 项（测试共享变量，无竞态，留档）外全部修复。
- 第 2 轮：对修复后的探针与 Go 改动按上述 A–G 清单复核；运行期发现的
  Victoria 回看、租约级联、投影冲突三项根因以运行证据（步骤表、所有权表、
  投影日志）逐项验证并修复或留档。

## 五、回滚与剩余风险

- 代码回滚：`git checkout 470ccd1 -- internal/ scripts/deepflow-dev/ test/integration/`。
- 运行回滚：kubectl patch deployment fault-chain-client-b 恢复原 sleep 命令；
  缩容 network canary 不影响本目录证据。
- 剩余风险：时延上限放宽后单次源读取最长 8–9s，受每步预算与 HTTP 上限约束；
  采集空洞（agent 重附加）仍会以显式缺口呈现，不会伪装为通过。
