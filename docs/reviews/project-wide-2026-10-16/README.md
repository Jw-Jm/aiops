# 项目全面代码审查（2026-10-16）— 修订版 r2

本目录是 2026-10-16 启动的项目全量代码审查的落盘交付物，已按两轮内部复核意见修订（见 [corrections.md](corrections.md)）。

**后续修复轮次**：已按本目录结论实施修复，逐项状态（已修复 16 / 部分 6 / 已裁定 3 / 未修复 14）、变更文件与回归回执见 [fix-status.md](fix-status.md)。修复只涉及工作区源码，未提交、未推送、未发布；审查阶段的结论与证据保持原样不改写。

审查对象：`/Users/mssc/Documents/Code/ops/platform`（上游 `ops/` 不是 Git 仓库，其 00～10 正式文档按需求依据读取）。

## 一、基线与工作区

| 项 | 值 |
|---|---|
| 分支 | `main` |
| HEAD（起始=结束） | `4dd81de8e7a11b58daf6451af958ade6d6f7fd9f` |
| tree | `a57a272fed2ebbc491f9340b63596655b64d5d93` |
| 未跟踪文件 | `docs/plans/project-wide-code-review-prompt.md`、`docs/plans/v1.0-remaining-work-agent-handoff.md`、`docs/reviews/`（属其他任务，全部保留未动） |
| 本机工具链 | `go1.27.1`、Node `v24.21.0`、pnpm `12.7.0`、Python `3.12.14`（与仓库声明一致，未安装或升级依赖） |
| 本机限制 | 审查期间根卷可用空间一度降至 485MB，导致部分 Go 测试二进制链接失败（`no space left on device`）；重跑通过。另有 Python `pytest` 缺失阻塞 |
| 动态环境 | 未访问 `192.168.0.63`，未启动本机 OrbStack、数据库、集群、模型或浏览器；所有运行期结论标注为待验证 |

与上一轮审查的关系：`docs/reviews/2026-10-15-project-review/` 基于另一分支（`fault-chain-multicaller-20261015`，HEAD `b685d51f`→`56946d32`），基线不同，故**另建目录**，未覆盖历史报告。上一轮 REV-001～007 在本基线的复核结论见 [findings.md](findings.md) 第 8 节。

## 二、结论摘要

**本轮登记 39 条 + 1 条经复核后否定，其中已确认缺陷 18 条**，按分类与定级：

| 分类 | 优先级 | 数量 | 编号 |
|---|---|---|---|
| A 运行时行为缺陷 | P1 | 3 | PWR-001、PWR-002、PWR-006 |
| A 运行时行为缺陷 | P2 | 8 | PWR-003、007、009、011、016、018、019、020 |
| B 门禁／物料／部署缺陷 | P2 | 7 | PWR-004、005、008、012、013、015、017 |
| C 建议与低危项 | P3 | 18 | PWR-010、021～029、031、032、034～039 |
| D 待裁定／需求歧义／设计取舍 | — | 3 | PWR-014、030、033 |

**必须优先处理的三项 P1（均已在本基线复现）**：

1. **PWR-001** 日志趋势/指标比较可把 1 次工具调用展开为 50/2 次来源请求，而预算只预留 1 —— 账本失去约束力、来源负载超出声明上限（上一轮 REV-001 未修复，探针复现）。
2. **PWR-002** 来源标签过滤导致的"缺失"被当作真实删除 —— 一次正常标签维护即产生**不可自愈的持久身份故障**并让整类图谱不可用（REV-002 未修复，探针复现）。
3. **PWR-006** Worker 停机期间真实消失的资源永不注销（`previous` 只是进程内 map）—— 实时图与数据库"仍存在"判定长期漂移（REV-004 未修复，探针复现）。

**其余 P2 中的关键项**：PWR-003（非 16K Profile 被 Go 常量拒绝，跨语言不一致）、PWR-005（集成验收零 skip 门禁被 helper 破坏，实测 exit 1）、PWR-004（迁移准入锁过期，实测失败）、PWR-008（api/worker 无健康探针）、PWR-009（Python 并发槽位可泄漏至全队列 429）、PWR-007（恢复时长统计使用未经证明的时钟）、PWR-016/018/019/020/011（错误码、死信、静默失败）等。

**本轮不能给出"项目通过验收"的结论**：静态审查未覆盖全部行；动态验收（数据库、集群、模型、浏览器）在本机不可执行；`make check` 因 PWR-004 当前不通过；`make test-integration` 因 PWR-005 无法判定通过。

## 三、覆盖情况（三种口径分别统计，不合并）

| 口径 | 本轮实际 | 说明 |
|---|---|---|
| 文件级盘点 | 12,314 个跟踪文件进入台账 | 见 [coverage.csv](coverage.csv) |
| 函数级盘点 | **4,643** 个函数/方法/组件/Hook/存储过程 | [functions.csv](functions.csv)。Go 用 `go/ast`（精确跨度）；Python 用正则；**TS/TSX 用正则且只识别顶层声明**（跨度与内部嵌套函数为近似，例如 930 行的 `AssistantPage.tsx` 只登记 5 个顶层声明） |
| 人工逐行阅读（非测试生产码） | 约 210 个文件、约 3.4 万行完整阅读；约 40 个文件部分阅读 | 集中在高风险共享路径；其余为静态核对或仅登记 |
| 静态核对（非逐行） | 全仓库模块边界、依赖方向、生成物、准入锁、部署清单 | 见 [scope.md](scope.md) |
| 运行验证 | Go 11 个包、`test/contract`、`test/security`、前端 23 个测试文件、4 个复现探针、2 个 Python 测试集、2 条门禁命令（均失败，为有效证据） | 见 [verification.md](verification.md) |

**明确未完成**：约 75 个 `test/integration` 文件、约 190 个 Go `_test.go`、`web/tests/e2e/**`、`internal/bundle` 的 `canonical.go`/`helm_renderer.go` 等 6 个文件、`third_party/**` 与 `bundle/**` 第三方实现、`deploy/addons/**` 延期物料未逐行阅读；未进行真实 PostgreSQL/集群/OIDC/模型/浏览器/离线 Bundle 验收。这些**不记为通过**。

## 四、交付物索引

| 文件 | 内容 |
|---|---|
| [scope.md](scope.md) | 文件分类、纳入/排除依据、部署环境与源码对应关系 |
| [requirements.md](requirements.md) | 需求追踪（00～10 + ADR）与当前阶段差距、延期边界 |
| [coverage.csv](coverage.csv)、[functions.csv](functions.csv) | 逐文件与逐函数台账 |
| [findings.md](findings.md) | 39 条问题（A/B/C/D 分类）、历史问题复核、复核后否定项 |
| [remediation-plan.md](remediation-plan.md) | 按编号的修复方案、实施顺序、验收方式、风险与回滚语义 |
| [ui-review.md](ui-review.md) | 页面/路由/角色/状态清单、UI 缺陷（含"动态未验证"声明） |
| [verification.md](verification.md) | 实际命令、退出码、跳过与未执行项 |
| [progress.md](progress.md) | 已完成/剩余清单、重要判断、恢复审查的下一步 |
| [corrections.md](corrections.md) | **本次审查自身的更正记录**（两轮复核意见与已应用的修改） |
| [fix-status.md](fix-status.md) | **修复轮次记录**：逐项修复状态、裁定记录（PWR-014/030/033）、变更文件、回归回执 |
| [evidence/](evidence/) | 基线、检查回执 JSON、探针复跑、门禁失败日志、前端测试日志 |
| [_tools/ledger/](_tools/ledger/) | 只读台账生成工具（独立嵌套模块，含 TSX 修正）；复现方式见 [verification.md](verification.md) |

## 五、下一步建议（详见 [remediation-plan.md](remediation-plan.md)）

1. 先修 PWR-001 的共享成本计算与 PWR-002 的"过滤 ≠ 删除"判定；两项的**回滚语义**已明确：恢复旧的宽松判定不属于安全回滚。
2. PWR-006 与 PWR-002 同轮设计资源生命周期，分别验证"同 UID 重入来源范围"与"停机期间真实消失"。
3. PWR-005（顶层 `-skip` 精确排除辅助入口，改动最小）与 PWR-003（Profile↔token 绑定）可并行；PWR-004 的准入重签放在迁移定稿之后。
4. PWR-007、009、011、016、018、019、020 按批次推进；PWR-008、012、013、015、017 属部署与仓库卫生。
5. PWR-014、030、033 需先裁定再定方案；PWR-010 只保留"合同偏差 + 验证缺口"，低成本路线是新增契约测试，不主张大规模重构。

## 六、审查方法说明

- 主审负责共享边界、端到端调用链、结论复核与去重；模块级深读由多个只读子审查并行完成，子审查结论**均经主审用 `read_file`/`search_content` 复核关键行号或复跑探针/命令**后才写入报告；两轮复核进一步纠正了措辞、范围与定级（见 [corrections.md](corrections.md)）。
- 所有行号对齐 HEAD `4dd81de8`；审查期间未发生代码变更。
- 复现探针位于上一轮目录 `docs/reviews/2026-10-15-project-review/_probes/`，本轮**按当前 HEAD 重跑**，未新增产品测试。
- 本报告不断言"没有 bug"，也不把未执行/跳过/失败的检查写成通过。
