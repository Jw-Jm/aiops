# 需求追踪与阶段差距（2026-10-16）

## 一、需求来源与本次读取程度（先声明局限）

| 来源 | 位置 | 本轮读取程度 |
|---|---|---|
| 00～10 正式文档 | 上级 `ops/` 目录 | **未逐行通读**。按审查问题需要读取了相关章节：03 总体架构、04 详细设计（§33 内部事件、§34 人工命令）、05 领域与数据模型（§19 证据、§24.1 来源数据范围、§25.5 保留）、06 故障分析与 RCA、07 接口与协议（错误码、命令执行、post-check、幂等重放）、08 资源图谱与证据模型（§49 图重建/租约）、09 智能体与工具调用（§41 预算装载、§48.5/§49 门禁、§51.1/§51.3 调用入口与原子预留）、10 编码实施方案、01 产品说明书、02 PRD |
| 需求条目级追溯 | — | **不完整**。多数结论为"实现层确认"，未逐条比对全部 FR/ACC 编号；本文件列出的追踪关系标注了证据强度 |
| ADR（41 篇） | `docs/adr/` | 标题全部核对；正文按需读取（0005、0006、0007、0008、0017、0020、0032、0038、0039、0040、0041 等） |
| 仓库边界 | `AGENTS.md`（根 + 仓库）、`docs/plans/v1.0-remaining-work-agent-handoff.md` | 全部读取 |

> 因此本文件是**方向性需求追踪**，不是 FR 级完整矩阵。未读取到的需求条目不得视为"已满足"，也不得视为"缺失"。

## 二、当前授权范围与延期边界（来自 AGENTS.md 与 ADR）

**当前范围**：core Profile、核心组件准入、Bundle、离线导入安装、干净重装、健康/能力检查；Task 2.8 非虚拟化图/诊断/证据基线；Task 2.9 非虚拟化复用裁决。专项授权：AI 问数与自然语言排障（ADR-0038）、真实来源/运行机制验收（ADR-0039）、实时 DeepFlow L4/L7（ADR-0040）、故障链调查增强（ADR-0041）。

**延期范围**：KubeVirt/CDI 全部开发与运行验收（ADR-0008）；Task 2.8 新增虚拟化关系适配；Task 2.9 KubeVirt 指标/告警、must-gather、KubeVirt MCP toolset；`virtualization/full` Profile 实施与验收；Trace 补全与 PyRCA（ADR-0040/0020）。

**保留约定**：保留已有源码、Schema、Fixture、版本矩阵与失败证据，不为使检查通过而删除或把失败改为成功。

## 三、模块 → 需求 → 入口 → 实现 → 验证 追踪

状态图例：`✅有当前证据`｜`🔶源码已实现、运行待验证`｜`🟡部分实现`｜`⛔当前范围内缺失`｜`⏸延期`｜`❓需求歧义`

| 能力 | 需求依据 | 入口 | 实现位置 | 证据 | 状态 |
|---|---|---|---|---|---|
| 模块化单体 + API/Worker 分工 | ADR-0001、03 | `cmd/platform-api`、`cmd/platform-worker` | `internal/app/*`、`internal/httpapi/*` | 源码；`go test ./internal/...` | ✅（架构一致，无需微服务） |
| 双角色无继承 + 租户隔离 | ADR-0002、05、07 | OIDC 中间件 | `internal/auth/oidc.go:108-163`、`internal/persistence/tenant_tx.go:26-58`、RLS（`00008_rls.sql`） | 源码 + `test/security/role_isolation_test.go`、`internal/auth/auth_test.go`（`TestRolesDoNotInherit`） | ✅（`platform.tenants` 无 RLS 见 PWR-030，需确认是否有意） |
| OpenAPI 为唯一权威并生成绑定 | ADR-0007 | `api/openapi/platform-v1.yaml` → `gen/api`、`web/src/api/generated` | 生成客户端已消费；运行时服务端以**手写前缀分发**承载路由（`internal/httpapi/foundation.go:80-114`），未使用生成的绑定。注：`api/oapi-codegen.yaml` 虽配置 `strict-server: true`，但 `HandlerFromMux` 接受的是非 strict 的 `ServerInterface`，需 `NewStrictHandler` 才启用 strict 校验 | `test/contract/openapi_test.go` 校验 spec 本身；**无"运行时路由 vs spec"的自动化校验** | 🟡（PWR-010，建议类） |
| 手工命令执行边界（不自动处置、step-up、不确定性不重放） | ADR-0003、07 | `POST /api/v1/commands:risk-assess` → `command-executions` | `internal/action/*`、`internal/httpapi/action_handlers.go` | 源码；`web/tests/command_authorization.test.tsx`；集成测试 gated | ✅机制已实现；错误码映射缺口见 PWR-016 |
| 来源注册与数据范围（RequiredLabels 等） | ADR-0014、05 §24.1 | `POST/GET /api/v1/admin/source-registrations` | `internal/source/*`、`internal/resourcestore/snapshot_scope.go` | 源码 | 🟡（过滤与删除未区分，PWR-002） |
| 资源身份、别名、注销 | 05、08 §49.1 | SP04 采集 | `internal/resource/*`、`internal/resourcestore/repository.go` | 源码；`resolver_test.go`（10,000 组 0 误合并） | 🟡（注销语义与重启恢复缺口，PWR-002/PWR-006） |
| 图重建原子性、租约单调、图查询 | 08 §49 | `POST /api/v1/diagnostic-graphs:build`、`/resources/neighbors` | `internal/graph/*` | 源码；`test/contract/graph_replay_test.go` | ✅机制正确 |
| Finding/Incident 关联 | 05、06 | `/findings`、`/incidents` | `internal/finding/*`、`internal/incident/*` | 源码；集成测试 gated | 🟡（`started_at` 可靠性未冻结，PWR-007；身份冲突 Finding 信封漂移，PWR-018） |
| 恢复时长统计口径 | PRD FR-044/ACC-005、`api/analytics/catalog-v1.json` `incidents.recovery-duration` | analytics control plane | `internal/analytics/controlplane.go:159`、`statistics.go:9` | 源码 | 🟡（PWR-007 的下游） |
| 确定性 RCA 与资格门禁 | ADR-0021/0022/0025、06 | `/incidents/{id}/rca` | `internal/rca/*` | 源码；`recipe_test.go`、`frozen_findings_test.go` | ✅机制正确（R3/R4/R5 严格门禁） |
| 受控调查（Job/Step/租约/预算） | ADR-0026、09 | `/incidents/{id}/investigations` | `internal/investigation/*` | 源码；集成测试 gated；4 个复现探针 | 🟡（预算低估 PWR-001；模型 token 预留 PWR-003） |
| AI 问数与追问（v3） | ADR-0038/0039 | `/api/v1/assistant/*` | `internal/assistant/*`、`internal/httpapi/assistant_*.go`、`services/investigator` | 源码；Python 28+ 项测试、评分器 9 项 | 🟡（PWR-003 跨语言不一致；来源覆盖依赖动态环境） |
| 故障链调查增强 | ADR-0041 | 助手 + `/fault-chain` 相关界面 | `services/investigator/fault_chain.py`、`internal/investigation/fault_chain_*.go` | 源码；`web/tests/fault_chain*.test.tsx` | 🔶（真实五来源联合闭环未在本轮执行） |
| 实时 DeepFlow L4/L7 | ADR-0040 | `deepflow.*` 数据集 | `internal/integrations/deepflow/*`、`internal/analytics/network.go` | 源码；历史证据目录 | 🔶（L7 需实测非空证明且 3 分钟过期；本轮未连远程） |
| 证据归档、digest、法律保留 | 05 §19/§25.5、ADR-0016 | `/evidence/*`、`/admin/legal-holds` | `internal/evidence/*`、`internal/archive/*` | 源码 | ✅机制正确（本轮未跑归档 live 测试） |
| 审计链（签名段、Merkle、前驱） | 07、ADR-0017 | `/audit-records` | `internal/audit/*` | 源码；集成测试 gated | ✅机制正确 |
| 离线 Bundle、镜像 digest、准入 | ADR-0005/0009/0010/0011/0012 | `opsctl` | `internal/bundle/*`、`internal/supplychain/*`、`bundle/evidence/**` | 源码 + `test/contract/*` | 🔴（迁移源码准入锁过期，PWR-004；未做离线冷装） |
| 核心组件准入（candidate 不入 Bundle） | ADR-0010、`AGENTS.md` | `supplychain/catalog.go` | 源码 | `Test...`（contract） | ✅（candidate 禁入已实现） |
| 部署/健康/能力检查 | `AGENTS.md` 当前范围 | `/api/v1/capabilities`、Chart | `internal/httpapi/*capabilities*`、`deploy/charts/ops-platform` | 源码 | 🟡（API/Worker 无健康探针与就绪端点，PWR-008） |
| 前端授权与阶段收口 | ADR-0034/0035/0036/0037 | `web/src` | 全部页面 | 源码 + 23 个单元测试文件通过 | ✅静态一致；**动态浏览器验收未执行** |
| Trace / PyRCA | ADR-0040/0020 | — | 无实现 | 契约保持 `CAPABILITY_DISABLED` | ⏸延期（边界完好） |
| KubeVirt/CDI | ADR-0008 | — | core 保持 `disabled`/`unverified` | 源码 | ⏸延期（边界完好，除 PWR-029 的 CLI 不对称） |

## 四、需求冲突、歧义与延期判定（需人工裁定，不自行改写需求）

1. **CLI 与运行时的 Profile 门禁不对称**（PWR-029）：`AGENTS.md` 要求 `virtualization/full` 延期，但 `internal/profile/resolve.go:36-44,70-77` 允许其为合法 resolved Profile，仅运行时 `internal/app/profile.go:18` 拒绝。属"宽进严出"的实现不对称，不是需求冲突，但需要一处显式判定。
2. **公开 steps 读取是否计入 Job 输出限额**（PWR-033）：`09 §51.3` 只规定"恢复读取计入读取限额"；公开接口 `ReadSteps`（`internal/investigation/recovery.go:122-139`）未 `chargeRead`，内部 `Steps`（`repository.go:252-269`）有。属需求歧义。
3. **`platform.tenants` 无 RLS**（PWR-030）：需求要求 tenant 隔离；`00008_rls.sql:15` 显式排除 `tenants` 表，`api_runtime_role` 仍可 `SELECT`（`00001_roles_and_schemas.sql:65`，`00010` 只回收写权限）。可能是为平台管理员枚举租户有意为之，需裁定。
4. **迁移仅前向**（PWR-014，待裁定）：`docs/runbooks/database-migrations.md:3` 已明确声明"forward-only; no migration contains a `Down` section"并给出失败恢复约定，故**不是缺陷**；需另行评估的是"应用回滚后 schema 领先代码"的兼容性（运维取舍）。
5. **KubeVirt 物料存在于仓库**：`deploy/addons/kubevirt/*-resolved.yaml`、`test/e2e/kubevirt_poc_test.go` 属"保留证据"，`AGENTS.md` 明确要求保留但不得开启。本轮未发现开启。
6. **助手中文质量未通过**（`qwen2.5:1.5b` 五例 r6：2 PASS / 3 partial FAIL）属**需求未满足**而非代码缺陷；`docs/plans/v1.0-remaining-work-agent-handoff.md` 已声明模型回答质量不作为本轮机制验收标准，本轮不重复计入缺陷。

## 五、功能完整性差距（当前范围）

| 差距 | 类型 | 位置/问题编号 |
|---|---|---|
| 来源标签维护会永久注销身份并阻塞采集 | 缺陷 | PWR-002 |
| Worker 停机期间真实消失的资源不注销 | 缺陷 | PWR-006 |
| 来源原始查询预算可被 50 倍展开突破 | 缺陷 | PWR-001 |
| 非 16K Profile 合法配置无法执行模型调用 | 缺陷 | PWR-003 |
| 迁移源码准入门禁当前失败 | 缺陷/物料 | PWR-004 |
| 集成验收零 skip 门禁自相矛盾 | 缺陷/门禁 | PWR-005 |
| 恢复时长统计使用未经证明的时钟 | 缺陷 | PWR-007 |
| api/worker 无健康探针与就绪端点（web 在 `sp08.enabled=true` 路径已有探针） | 缺陷（B 类） | PWR-008 |
| 缺"运行时路由 ↔ OpenAPI spec"的自动化校验（非 strict 接口混淆已更正） | 建议（C 类） | PWR-010 |
| 真实五来源联合闭环、Node/PVC 覆盖、来源退化场景 | 待运行验证 | 交接文档第四节；本轮未执行 |
| 离线 Bundle 重建与干净重装证据 | 待授权/环境 | ADR-0005；本轮未执行 |
| 浏览器 UI 美观/可访问性/窄屏验收 | 待环境 | [ui-review.md](ui-review.md) |
| 助手中文解释质量与人工评审 | 需求未满足（非代码缺陷） | 交接文档第三节 |

**说明**：本表只列当前授权范围内的差距；延期项（KubeVirt/CDI、Trace、PyRCA、虚拟化 Profile）不计入。
