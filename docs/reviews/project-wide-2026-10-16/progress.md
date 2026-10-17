# 审查进度（2026-10-16）

**当前状态：本轮已收口（阶段完整：静态审查 + 本机可执行验证 + 全部交付物落盘 + 报告自身经两轮复核更正）。动态验收因环境与授权限制**未完成**，不作为"本轮已验收通过"。**

## 零、本次审查自身的更正（r2）

报告交付后经两轮复核，累计 19 条意见（16 条成立、3 条部分成立、0 条不成立），已全部应用。要点：

- **措辞与回滚语义**：PWR-001 原写"成本低估导致误拒绝、可回滚到单一常量"、PWR-002 原写"不可见误判为删除可接受"——均为错误且有回归风险，已改为"低估是原缺陷""恢复旧判定不算回滚"。缺陷本身（PWR-001/002/006）**未撤销**，调用链与探针证据不变。
- **分类与计数**：由"P1 六 / P2 十四 / P3 十九"改为 **A 运行时缺陷 11（P1 三项）/ B 门禁·物料·部署 7（P2）/ C 建议 18（P3）/ D 待裁定 3**；PWR-030、033 从缺陷中移出（缺可达证据 / 需求歧义），PWR-022、023、034、035、037 归入建议。
- **定级**：P1 收敛为 PWR-001、002、006；PWR-003、004、005 改为 P2（分别受当前 16K 部署与"只影响门禁"约束）。
- **范围收窄**：PWR-008 仅 api/worker（`sp08.enabled=true` 时 web 由 `sp08.yaml:50-51` 渲染且已有探针）；PWR-014 仅前向迁移已有 `docs/runbooks/database-migrations.md:3` 说明，属取舍而非缺陷；PWR-036 删除 `-to 0` 子项。
- **修法更正**：PWR-005 改用顶层 `-skip '^TestAssistantCrashWorkerChild$'`（已实测 skip/run 事件均为 0），**不采用**裸 `return`；PWR-010 降为"合同偏差 + 验证缺口"，并区分 `ServerInterface` 与 `StrictServerInterface`。
- **台账工具缺陷**：r1 漏提取全部 TSX（81 个文件函数数为 0），修正后条目 4,503 → **4,643**；工具移入 `_tools/ledger/`，`scope.md` 已如实说明"只识别顶层声明"。

完整逐条对照见 [corrections.md](corrections.md)。

## 零之二、修复轮次

### 第一轮（2026-10-16）

已按本目录结论实施修复，逐项状态与变更清单见 [fix-status.md](fix-status.md)：**已修复并验证 16 项**（PWR-001、002、005、009、011、013、015、017、019、020、021、022、023、024、032、036）、**部分修复 6 项**（008、029、031、034、035、038）、**已裁定并记录 3 项**（014、030、033）、**未修复 14 项**（003、004、006、007、010、012、016、018、025、026、027、028、037、039，均已在 fix-status.md 说明原因，集中在需要数据库/集群验证、跨包设计改造或契约协同的项）。

回归：`go build ./...`、`go vet`、`go test ./internal/... ./test/contract ./test/security`、前端 `tsc`+`vitest`（23 文件）、Python `compileall` 通过；`make check` 当时受 PWR-004 阻塞，需要数据库的集成验收未运行。

### 第二轮（2026-10-17）：PWR-004 与运行时源闭包

PWR-004 已修复，详见 [fix-status.md](fix-status.md) 第六节。要点：

- **运行时源链未重签**：漂移来自 PWR-006 新增文件 `internal/resourcestore/reconcile.go` 对 `k8s.io/apimachinery/pkg/api/meta` 的依赖（编译闭包多选入 11 个 `pkg/api/meta` 文件）。改为让 `kubernetes.GVR` 携带精确 `Kind`（`Key()` 与持久化键不变）并在闭包内比较，闭包回到 3,043 个模块文件，运行时准入证据未作任何修改。
- **迁移源链按既有流程重签**：新增迁移 00038–00043 是真实变化；重签 4 份 lock、4 份 source 准入 json 与 `migration_admission_test.go` 摘要常量。`binarySHA256`/`firstPartySourceSHA256` 未变，两架构离线重建逐字节复现了已准入二进制。
- **验证**：runtime 与 migration 的 arm64/amd64 `--check` 全部 exit=0；此前失败的 5 个 `internal/bundle`/`cmd/opsctl` 用例通过；`go test ./internal/... ./cmd/... ./test/contract ./test/security` 无失败；`check-generated`、`check-runtime-source`、`lint` 通过。
- **远程验证**（`192.168.0.63`，专属目录 `/opt/ops-pwr004-verify-20261017`）：隔离无网络 PostgreSQL 17.11 中 amd64 `db-migrate` 前滚 00001–00043 并幂等重跑通过；当前源码编译的 `test/integration` 在真实 PostgreSQL/OpenBao/SeaweedFS 上 **PASS 35 / FAIL 0 / SKIP 0**。
- **未完成**：arm64 的 native 前滚迁移未执行（验证主机 x86_64 无 aarch64 QEMU，本机不使用容器）；`make check` 的 `test-unit` 在无环境主机仍因 `test/integration` live gate 失败（该集合设计为 fail-closed）；其余动态验收缺口不变。

## 一、已完成

| 批次 | 范围 | 结果 |
|---|---|---|
| 0 | 基线、工作区、工具链、文件盘点 | 完成；12,314 文件进入台账，[coverage.csv](coverage.csv)、[functions.csv](functions.csv) 生成 |
| 1 | 需求与边界（AGENTS.md、ADR×41、00～10 相关章节、交接文档） | 完成，见 [requirements.md](requirements.md)；**00～10 未逐行通读**，FR 级追溯不完整 |
| 2 | 预算/调查/分析链路（`internal/investigation`、`analytics`、`assistant`、`httpapi` 助手与调查面） | 非测试文件基本读完；发现 PWR-001、PWR-003、PWR-010、PWR-016、PWR-020 等 |
| 3 | 资源与图谱域（`resourcestore`、`resource`、`graph`、`incident`、`finding`、`rca`、`action`、`evidence`、`archive`、`inspection`） | 非测试文件基本读完；发现 PWR-002、PWR-006、PWR-007、PWR-018、PWR-026 |
| 4 | 认证/租户/来源/供应链/集成（`auth`、`tenant`、`source`、`audit`、`policy`、`upstream`、`supplychain`、`bundle`、`integrations`） | 约 40% 逐行；确认多项安全机制实现正确；发现 PWR-021、PWR-022、PWR-023、PWR-030 |
| 5 | 启动/生命周期/配置（`cmd/*`、`app`、`bootstrap`、`profile`、`configregistry`、`observability`、`persistence`） | 完成；发现 PWR-008、PWR-019、PWR-028、PWR-029、PWR-036 |
| 6 | 前端（`web/src`、`web/tests`） | 生产源码与单元测试读完（静态）；发现 PWR-011、PWR-020、PWR-032；23 个测试文件通过 |
| 7 | Python（`services/investigator`、`scripts/assistant-eval`、`scripts/deepflow-dev`） | 生产与测试文件读完；发现 PWR-009、PWR-038；评分器 9 项通过 |
| 8 | 数据/构建/部署/运维（`migrations`、`api`、`deploy`、`Makefile`、`scripts`、`gen`） | 完成；发现 PWR-004、PWR-012、PWR-013、PWR-014、PWR-015、PWR-037 |
| 9 | 测试与门禁有效性（`test/**`、`web/tests/e2e`） | 完成分类与断言审查；发现 PWR-005、PWR-017、PWR-031、PWR-039 |
| 10 | 运行验证 | 见 [verification.md](verification.md)：Go 11 包、契约、安全、4 个探针、前端 23 文件、Python 评分器 9 项通过；两条门禁命令**实测失败**（PWR-004、PWR-005） |
| 11 | 报告落盘 | [README.md](README.md)、[scope.md](scope.md)、[requirements.md](requirements.md)、[findings.md](findings.md)、[remediation-plan.md](remediation-plan.md)、[ui-review.md](ui-review.md)、[verification.md](verification.md)、[progress.md](progress.md)、`coverage.csv`、`functions.csv`、`evidence/**` |

## 二、剩余清单（未完成，恢复审查时从这里继续）

### 静态审查缺口（可继续，无需外部环境）

1. `test/integration` 约 75 个文件、`internal/**` 约 190 个 `_test.go` 未逐行阅读（当前为门禁分类级）。
2. `internal/bundle` 未读的 6 个文件中的 `canonical.go`（RFC 8785 规范化，直接支撑签名/摘要）与 `helm_renderer.go`（post-render 注入点）在关键路径上，优先级最高。
3. `internal/httpapi/sp05_reads.go` 剩余分支；`internal/app` 的 `sp05_*` 分析/候选/官方观察路径。
4. `api/schemas/**`（100+ JSON Schema）与 `api/openapi/platform-v1.yaml`（~90 个 operation）的**逐 operation 字段/错误码比对**未完成（当前为抽样 + 关键路径）。
5. `internal/integrations` 未读测试与 `s3`/`keycloak` 细节；`internal/upstream` 的 `ontology/service/diagnostic/service.go`（788 行）已读但 `model/relation_spec.go`（387 行）等仅部分。
6. `third_party/**` 与 `bundle/**` 第三方实现的**来源/许可/锁**关系已核对，实现内部明确排除。
7. 依赖漏洞公告核对未做。

### 动态验收缺口（需环境与授权）

1. `192.168.0.63`：五来源（Kubernetes、VictoriaMetrics、VictoriaLogs、DeepFlow、内网模型）非空联合调查、已提交证据解释、冻结身份/窗口追问、现有页面定位、浏览器记录。
2. 来源退化场景：撤权、同名重建/容器替换、Watch 中断、半开窗口、历史缺失、仅时间接近的变更、来源超时、预算耗尽。
3. `18443`/`18643` core/final 环境观察与差异报告（本轮未访问）。
4. 离线 Bundle 重建、干净重装、升级/回滚演练。
5. 真实 PostgreSQL 迁移前滚/幂等、RLS 行为、并发与租约场景。
6. 浏览器 UI：登录/step-up、SSE 重连、图谱 canvas、终端组件、窄屏、对比度、键盘可达性。
7. 项目锁定 Python 环境下的 `services/investigator` 测试（本机缺 pytest）。

## 三、重要判断（恢复审查时先读）

1. **上一轮 7 项问题全部仍在**（详见 [findings.md](findings.md) 第 0 节）；REV-003 与 REV-007 本轮上调为 P1（跨语言硬不一致、门禁实测失败）。
2. **`make check` 当前不通过**，原因是迁移源码准入锁止于 `00037`（PWR-004），与助手/观察表的新迁移不匹配。任何"门禁全绿"的说法在当前 HEAD 都不成立。
3. **`make test-integration` 在当前 HEAD 无法判定通过**，与是否具备环境无关（PWR-005 已实测）。
4. **不存在足以宣称"项目验收通过"的证据链**：动态验收整体缺失，源码级覆盖率也不完整。**不得**把本报告读成通过结论。
5. **安全机制总体实现扎实**：OIDC 验签 + 双角色不继承 + step-up 绑定 + 租户事务（SET LOCAL）+ RLS + SPIFFE mTLS + 证据 digest/法律保留 + 命令执行授权链 + 反重放账本均有实质实现；确认的问题集中在**生命周期衔接、预算口径、契约落地与门禁一致性**，不是"整体缺乏防护"。
6. **延期边界完好**：未发现 KubeVirt/CDI/Trace 被误开启；唯一不对称是 CLI `profile resolve` 接受 `virtualization/full`（PWR-029）。
7. **审查方法学提醒**：子审查的候选结论必须复核。本轮已否定一条候选（`CreateJob` 重复 "queued" 事件，见 [findings.md](findings.md) 第 4 节）——原因是 `RowsAffected` 实际取自 `worker_queue` 的 `ON CONFLICT DO NOTHING`，而非 jobs 的 upsert。

## 四、恢复审查的下一步

1. 先复核基线是否变化：`git rev-parse HEAD`、`git status --porcelain`、`git log --oneline -5`。若 HEAD 已变，本报告的**行号全部需要重新核对**（问题根因通常仍在，但位置会漂移）。
2. 从 [progress.md](progress.md) 第二节的静态缺口继续，优先 `internal/bundle/canonical.go`、`helm_renderer.go` 与 OpenAPI 逐 operation 比对。
3. 需要动态验证时，明确列出所需环境/凭据与具体动作，先取得授权，再按 [verification.md](verification.md) 第六节的复现方式执行；每轮使用新的专属输出目录，不覆盖本轮证据。
4. 若开始修复，按 [remediation-plan.md](remediation-plan.md) 第一节的批次推进，并为每个 P1 先写"修复前失败、修复后通过"的最小回归（PWR-001 的探针语义需由"复现缺陷"翻转为"断言正确成本"）。
