# 审查自身的更正记录（corrections）

本文件记录本次审查**自身**被复核出的错误，以及据此对报告所做的修改。目的是避免后续读者把已被推翻的表述继续当作结论使用。

复核共两轮，累计 19 条意见：**16 条成立、3 条部分成立、0 条不成立**。所有更正均已应用到 [findings.md](findings.md)、[remediation-plan.md](remediation-plan.md)、[ui-review.md](ui-review.md)、[scope.md](scope.md)、[README.md](README.md)、[verification.md](verification.md)、[progress.md](progress.md)。

## 一、第一轮（13 条成立）

| # | 原表述 | 核实结果 | 已应用的更正 |
|---|---|---|---|
| 1 | `remediation-plan.md` PWR-001 风险段：成本函数"低估导致误拒绝（保守方向）"，并建议"回滚到单一常量" | **错误且有回归风险**。低估的后果是预留不足→**多发请求**（即原缺陷）；"回滚到单一常量"正是 PWR-001 的成因 | 改为"高估成本才是误拒绝（偏保守）；**低估是原缺陷**"；明确**不得**回滚到单一常量，正确回滚是保持更严格的"按实际展开量预留" |
| 2 | PWR-002 风险段：把"来源不可见被当成删除"称为"保守方向可接受" | **错误**。这正是 `repository.go:225-235 → :180 → :140-157` 的根因 | 改为"任何把不可见重新当作删除的回归都不可接受，须由验收用例显式拒绝" |
| 3 | `ui-review.md`：称"界面上不存在独立的过期提示，只依赖线型与透明度" | **错误**。`GraphView.tsx:246-255` 在 `stale` 时明确返回告警"收到过期图谱版本，请重新查询" | 改为"已存在 stale 告警；`:259-265` 属不可达冗余分支，仅建议删除"；并区分"旧 generation"与"关系 TTL 过期"是不同概念 |
| 4 | `scope.md` 称台账用"TS/TSX 正则（跨度近似）" | **工具缺陷**：`.tsx` 被分类为 `tsx`，而工具 `switch` 只匹配 `ts`，全部落入 SQL 提取器 → `functions.csv` 中 tsx 行数为 0，81 个 TSX 文件（web/src 58 个）函数数全为 0 | 修正工具（显式处理 `tsx`）并重跑：函数条目由 4,503 → **4,643**（tsx 贡献 140，web/src 126）；工具落入 `_tools/ledger/`；`scope.md` 改为如实描述"只识别顶层声明、内部嵌套函数未枚举" |
| 5 | PWR-039 称 `internal/httpapi` 管理面无单测 | **过度概括**。该包有 **16 个 `_test.go`**，含 `operator_role_status_test.go`（角色状态 PATCH 的越权/输入边界）与 `sp08_audit_verify_test.go`（审计核验） | 改为"管理面覆盖不完整"，并在 PWR-039 中列举既有覆盖与缺口 |
| 6 | PWR-014 把"仅前向迁移"列为缺陷；建议 PDB `minAvailable: 0`；称副本数为 1 | **三点均错**：`docs/runbooks/database-migrations.md:3` 明确 forward-only 并给出失败恢复约定；`minAvailable: 0` 不提供任何保护；`values.yaml:38,40` 在启用 SP04 时 worker=`workerReplicas`（默认 2） | PWR-014 移入 **D 类待裁定**，删除缺陷定性；删除 `minAvailable: 0` 建议并注明其无保护作用；补记 SP04 启用时 worker=2 |
| 7 | PWR-036 称 `-to 0` 存在语义歧义 | **不成立**：`cmd/db-migrate/main.go:17` 帮助文本已写明"zero migrates to the latest version" | 删除该子项；仅保留"Worker 强依赖其未读取的 `OIDC_ISSUER_URL`"（`bootstrap.go:64` 校验、读取点仅 `api_runtime.go:103` 与 `profile.go:36`） |
| 8 | PWR-031.1 称子进程 helper 裸 `return` 是"假 PASS"；PWR-031.5 称回执是"被测系统自证" | **不准确**：外层套件的通过数包含一个空执行的辅助入口（应如实表述），而父测试内部确有实质验证（等待 Step、SIGKILL、校验接管 generation）；回执由**测试**写出，非 SUT 自证 | 删除"假 PASS""SUT 自证"措辞，改为"通过数包含辅助入口"与"回执由测试写出，下游不得当作独立验证"；PWR-005 与 PWR-031 采用统一口径 |
| 9 | PWR-030、PWR-033 计入已确认缺陷 | **成立**：PWR-030 的可达接口受 `auth.RequireRole(auth.PlatformAdmin)` 保护（`tenant_handlers.go:34-38`），"任何已认证用户可跨租户枚举"缺可达证据；PWR-033 本为需求歧义 | 两条移入 **D 类待裁定**，不再计入确定缺陷数 |
| 10 | PWR-022、023、034、035、037 按"已确认缺陷"统计 | **成立**：这些更接近防御增强、维护建议或未来扩展风险 | 移入 **C 类建议与低危项**（P3），摘要表增加"分类"列 |
| 11 | PWR-008 称"API/Worker/Web 缺全部健康探针" | **范围过宽**：`deployments.yaml:9` 的 web 只在 `sp08.enabled=false` 时渲染；`sp08.enabled=true` 时 web 由 `sp08.yaml:50-51` 渲染且**已有探针** | 收窄为"api/worker 无探针；web 仅在其 `deployments.yaml` 渲染路径无探针" |
| 12 | PWR-003 称 Python 测试"固化了错误假设" | **不成立**：按 Profile 的 `contextWindow` 预留符合 09 §51.3 的设计，缺陷在 Go 侧常量 | 改为"Python 侧预留上界取 Profile 是正确实现；缺陷是 Go 以常量代替 Profile 解析"；修复方案不再要求改写 Python 断言 |
| 13 | PWR-003/004/005 均列 P1 | **成立**：按报告自定的 P1 判据（主要功能/安全/数据可靠性严重受损），PWR-003 在当前 16K 部署下不触发、PWR-004/005 只影响门禁与验收 | 三条改为 **P2**；P1 收敛为 PWR-001、002、006 |

## 二、第二轮（3 条成立／部分成立）

| # | 原表述 | 核实结果 | 已应用的更正 |
|---|---|---|---|
| 14 | 以"expired 通常就是 dynamic"作为撤回 PWR-032.1"互斥语义"判断的理由 | **理由错误**。`web/src/features/graph/layout.ts:5-10` 的 `edgeExpired()` 同时依据 `validTo` 与 `observedAt + ttl`，因此 **`ttl=0` 且 `validTo` 已过的边"过期但非动态"**；关系类别与过期状态是**可叠加的两个维度** | 撤回理由改为"类别与过期状态可叠加、并非互斥"；并补充关系表已分别呈现 `观测时效`（`GraphView.tsx:458-463`，用 `edgeExpired` 渲染 `Status`）与 `动态有效期`（`:464-467`），故不能断言"无法区分" |
| 15 | `GraphView.tsx:293` 图例文案 | **确认不严谨**："虚线：TTL / 有效期已过期"暗示"虚线 ⇔ 已过期"，但 `:136-138` 的**未过期动态边同样是虚线** | 作为 PWR-032 保留项（措辞缺陷），与第 14 条分开记录 |
| 16 | PWR-001／002 是否应随修复建议错误一并撤销 | **不应撤销**。两条调用链与探针证据未变 | findings 中保留为 P1；仅更正风险说明与回滚语义 |
| 17 | PWR-005 修法是否必须拆独立子命令 | **不必须**。父测试 `assistant_process_recovery_test.go:133-138` 自行用 `os.Executable()` 启动子进程并显式传 `-test.run ^TestAssistantCrashWorkerChild$`，顶层 `-skip` 不传播给它；实测顶层 `-skip` 后报告中 skip 事件 0、run 事件 0；`Makefile:67` 已有 `-skip` 先例 | 定为最小修复：在 `Makefile:71` 加 `-skip '^TestAssistantCrashWorkerChild$'`；**明确不采用**裸 `return`（会虚增通过数），独立子命令列为可选但改动更大 |
| 18 | PWR-031.2"`test.skip` 永不生效" | **需限定**：`session.ts:22-27,69-73` 的 fixture 在缺 `OPS_WEB_QA_INPUT`/`OPS_WEB_QA_CDP` 时抛错，此时函数体内的 skip 不会被求值；环境齐备时不成立 | 改为"fixture 初始化失败时 skip 不生效" |
| 19 | PWR-010 的定性与方案 | **部分成立**：事实（生产入口用手写分发）成立，但把 `ServerInterface` 与 `StrictServerInterface` 混为一谈——`api/oapi-codegen.yaml` 配置 `strict-server: true`，`HandlerFromMux`（`gen/api/openapi.gen.go:12669`）只接受非 strict 接口，需 `NewStrictHandler`（:16984）才启用 strict 校验；ADR-0007 未要求实现 strict 接口 | 降为 **C 类建议**；保留"合同偏差 + 缺运行时↔spec 校验"结论，给出低成本（契约测试）与可选的彻底路线，并明确不主张大规模重构 |

## 三、仍然坚持、未被推翻的结论

1. **PWR-001、PWR-002、PWR-006 的问题本身成立**（调用链 + 探针复现），本轮只更正了风险说明与回滚语义。
2. **PWR-032.1 的前半仍成立**：`legend.tsx:18` 写"动态关系：点划线"，实现为 `dashed`（`GraphView.tsx:136-138`），图例与实现不一致；仅撤回"互斥语义/无法区分"的判断。
3. **PWR-005 是事实**（`check-test-report.py` 实测 exit 1），只是修法与定级按第 17、13 条调整。
4. **PWR-036 的 Worker OIDC 子项成立**（`bootstrap.go:64` 对两角色都校验，而 Worker 从不读取该配置）。

## 四、对审查流程的改进（后续审查应遵循）

1. 台账工具必须先做**语言分支自检**（例如断言每种语言的 `function_count` 不全为 0），再用于统计；本次靠复核才发现 TSX 全漏。
2. 报告中的"缺陷/建议/待裁定"必须在**摘要表层面**分开计数，不能只在正文里写"属防御纵深"而在总数里按缺陷累计。
3. 修复方案的"风险/回滚"不得使用"保守方向"这类未定义措辞；必须写明**哪一侧是不安全方向**，并明确"恢复旧逻辑不算回滚"。
4. 涉及门禁/测试的结论必须实测（`exit code` + 原始事件），不能只凭静态推断；本轮 PWR-005 与 PWR-004 均为实测。
5. 引用他人（子审查）结论时，必须复核到具体行；跨接口名称（如 `ServerInterface` / `StrictServerInterface`）需打开生成代码确认。
