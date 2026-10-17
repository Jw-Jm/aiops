# 项目深入代码评审（2026-10-15）

What this repo does: 本项目是面向 Kubernetes 和物理基础设施的智能运维平台。Go API 与 Worker 共用模块和 PostgreSQL，提供资源图谱、Finding/Incident、证据归档、确定性 RCA、受控调查和人工命令执行；React 提供操作界面，Python 包装锁定的 HolmesGPT 调查循环。

本次按一个租户可能有多个操作者、后台 Worker 可竞争执行、进程会重启、来源会短暂不可用的场景评审。没有假定生产大规模吞吐，也没有把已豁免的模型容量测试恢复为本次门禁。

**结论：确认 7 项问题，P1 两项、P2 五项。优先修正原始查询预算和来源过滤触发的永久注销。现有单元测试通过不能覆盖这两类缺陷；迁移源码准入检查实际失败，当前不能据此宣称构建门禁全部通过。**

本次仅新增评审材料，未修复业务代码、修改正式方案、修改既有测试或部署环境。问题编号可直接用于后续修复任务。

## 评审基线与适用范围

- 仓库根：`/Users/mssc/Documents/Code/ops/platform`；上层 `ops/` 不是 Git 仓库。
- 分支：`fault-chain-multicaller-20261015`。
- 开始 HEAD：`b685d51f2c2f39c71bb33d6fd6cd9af4f82f5c93`。
- 复核 HEAD：`56946d32c867c80890ad339bfd192b4848d357f0`。评审期间外部工作流先后提交了多调用方测试与专用健康步骤断言；变动限于两个 probe 脚本和一份集成测试，见 [变动路径](concurrent-commit-paths.txt)。本文问题涉及的业务实现、Chart、模型合同和迁移锁未变。期间新增的运行证据目录和 runbook 编辑亦属外部工作，未作为本次新运行验收依据。
- 开始时已有两个已修改文件、一份未跟踪测试和一份未跟踪审查提示词；原始清单见 [worktree-before.txt](worktree-before.txt)，其中 `docs/reviews/` 是本次采集清单前刚创建的评审目录。现有变更均保留。
- 已读取根及平台 `AGENTS.md`、ADR-0008，以及双角色、人工处置、SP-07、SP-08、助手、实时 DeepFlow、故障链等适用 ADR。正式文档按相关章节核对，没有逐行阅读全部 00～10 文档。
- ADR-0032、0038～0041 的后续非虚拟化授权适用。KubeVirt/CDI、VM/VMI/DataVolume 继续延期；Trace 禁用。不把延期、模型回答质量未通过、SP-08 尚未恢复的整体验收作为新增代码缺陷。
- 本次是**全项目盘点后的高风险调用链深入评审**，不是所有文件、函数的穷尽逐行审计。人工阅读与搜索核对范围见 [coverage.md](coverage.md)，机器盘点见 [source-inventory.csv](source-inventory.csv)。没有把盘点或测试通过算成人工阅读覆盖率。

## 必须修复

### 1. REV-001 / P1：日志趋势与指标比较可以突破原始查询预算

**状态：已确认；置信度高。类别：预算、资源消耗、运行机制。**

**这是什么：** MCP 在执行来源查询之前预留 Job 的工具调用数、原始查询数和输出上限。原始查询数应计算实际展开的来源请求，而不是 MCP 调用次数；正式《09 智能体与工具调用设计》§51.3 要求先原子预留，再调用来源。

**问题及触发：** Job 只剩 1 次原始查询预算时，一个 `logs.records / trend` 请求仍可展开为 50 次 LogsQL 请求；`metrics.* / compare`、`compare_percent` 以及日志比较会展开为 2 次。Gateway 只有 `network.*` 进入展开计数，其余路径一律预留、结算 `RawQueries=1`。单次工具的三秒超时仍存在，但不能代替请求次数上限。

**代码证据：** [Gateway 预留](/Users/mssc/Documents/Code/ops/platform/internal/investigation/mcp/server.go:126) 第 126～138 行只有网络前缀例外；[日志趋势](/Users/mssc/Documents/Code/ops/platform/internal/analytics/log_trend.go:38) 第 38～41 行逐点执行；[比较](/Users/mssc/Documents/Code/ops/platform/internal/analytics/comparison.go:26) 第 26～32 行执行两个窗口。`used := reserve` 继续沿用低估的预留值，没有在来源侧另行扣减这些原始请求。

**验证：** [探针日志](review-probes.txt) 的 `TestReviewVictoriaExpandedRawQueries` 通过真实 `Victoria.Compile/Execute` 和内存 HTTP Transport 记录到 50/2 次请求，没有连接外部服务。Gateway 的低估通过源码核对确认；本次未搭建数据库/MCP 服务做完整 Job 耗尽复现。

**最小修复：** 在现有 analytics 成本计算处统一覆盖 Victoria 与 Network：比较为 2，日志趋势为窗口步数加 1，Metrics `query_range` 为 1；所有实际调用前按同一结果原子预留，失败按既有保守策略结算。不要提高总预算，也不要只在调用完成后补记。

**修复涉及与验收：** `mcp/server.go`、analytics 成本函数、相关成本单元测试及 MCP 预算集成测试。剩余预算 1 时，两次比较和 50 点日志趋势均应在来源调用前拒绝；足额时请求数与账本预留/消费一致，网络现有成本保持正确。

**不修复的后果：** 普通授权调查能产生超出正式硬上限的来源负载，账本却显示仍在限额内。该问题不授予命令执行权限。

### 2. REV-002 / P1：暂时离开来源标签范围的资源被永久注销，重新进入后会阻断采集

**状态：已确认；置信度高。类别：生命周期、来源隔离、可用性。**

**这是什么：** 来源可以通过 `RequiredLabels` 缩小采集范围；原生 Kubernetes UID 的真实删除则通过 `deleted_at` 记录不可复活的注销标记。

**问题及触发：** 已采集 Pod 带有来源所要求的标签；用户临时修改该标签，UID 没有变、Pod 也没有删除。`FilterSnapshot` 将它从 Objects 排除，随后 `SyncSnapshot` 把“过滤后缺失”当成真实删除调用 `Retire`。标签恢复后，同一 UID 重新被采集，但 `Resolve` 拒绝覆盖已注销行并返回身份错误；整个 GVR 快照因此在 Graph 发布前失败。只要来源配置了标签过滤就能触发，不依赖高并发。

**代码证据：** [过滤](/Users/mssc/Documents/Code/ops/platform/internal/resourcestore/snapshot_scope.go:45) 第 45～58 行丢弃不匹配对象；[调用顺序](/Users/mssc/Documents/Code/ops/platform/internal/app/sp04_collector.go:37) 第 37～47 行先过滤再同步；[缺失即注销](/Users/mssc/Documents/Code/ops/platform/internal/resourcestore/repository.go:222) 第 222～229 行；[拒绝复活](/Users/mssc/Documents/Code/ops/platform/internal/resourcestore/repository.go:140) 第 140～154 行。

**验证：** `TestReviewFilteredAbsenceBecomesTerminalRetirement` 将过滤产生的空 Objects 与上轮 UID 输入真实 `SyncSnapshot`，事务替身观察到对 `deleted_at` 的 UPDATE。过滤条件、后续 Resolve 和发布前返回路径已静态串联；未修改真实集群标签。

**最小修复：** 将来源可见性变化与原生 UID 删除分开。保留过滤前、已完成同步的原生 UID 集合用于判断真实消失，过滤后的集合只用于当前可见性/发布；如果需要持久化不可见状态，应支持同一 UID 重新进入来源范围。不能简单允许所有已删除 UID 复活。

**修复涉及与验收：** `collectorSink`、`FilterSnapshot/SyncSnapshot` 的输入契约、资源注销及来源当前有效性检查。增加同 UID 标签匹配→不匹配→匹配的用例，确认资源重新进入且无身份冲突；同时保留真实 DELETE 的终态、撤权隔离和不会向 Graph 发布范围外对象的验证。

**不修复的后果：** 正常标签维护会形成持久身份故障，后续新鲜资源图谱可能持续不可用；重启不能清除数据库中的错误注销标记。

## 应修复

### 3. REV-003 / P2：非 16K 的有效模型 Profile 无法执行模型调用

**状态：已确认；置信度高。类别：跨语言合同、功能正确性。**

**这是什么：** 新助手 Profile 支持 1024～65536 的 `contextWindow`，Python 按这个值预留输入 token；旧 v2 使用 16K 默认值。

**问题及触发：** 将一个有效助手 Profile 配置为 32768。Python 发出 `inputTokens=32768`，Go `AllocateModel` 却在读取 Job 前强制 `reserve.InputTokens == 16384`，直接返回 `ErrBudget`。8K 等其他有效窗口也同样被拒绝。问题不影响默认 16K，不应据此声称当前默认模型已全部失效。

**代码证据：** [固定 16K](/Users/mssc/Documents/Code/ops/platform/internal/investigation/allocation.go:93)；[Python 预留](/Users/mssc/Documents/Code/ops/platform/services/investigator/src/investigator/budget.py:57)；[Profile Provider](/Users/mssc/Documents/Code/ops/platform/services/investigator/src/investigator/model_profile.py:64)；[Go Profile 校验](/Users/mssc/Documents/Code/ops/platform/internal/modelprofile/profile.go:45)。Job 内只有 Profile 引用，当前准入没有按该引用解析 token 上界。

**验证：** `TestReviewAccepted32KProfileRejectedBeforeModelAdmission` 使用现有 fixture 改为 32768，`Profile.Valid` 通过，但对应 `AllocateModel` 在不访问数据库时即返回预算错误。现有 Python Profile 测试也明确构造了 32768 预留，但使用 Ledger 替身，没跨到 Go 准入。

**最小修复：** 保留旧 v2 的 16384 规则；v3 从服务端已冻结且可信的 Profile 材料取得上界，再验证预留值和 Job 剩余预算。不能直接接受客户端传来的任意上界，也不能只删除比较条件。

**修复涉及与验收：** Profile 引用解析/注入、`AllocateModel`、Job API 和 Go/Python 合同用例。验证 8K/16K/32K 合法预留、伪造预留、Job 总预算不足、旧 v2 兼容性；预算不足仍须拒绝，不自动扩大正式预算。

**不修复的后果：** 配置校验、能力展示与实际调用不一致，合法的模型变更会表现为预算耗尽或调查失败。

### 4. REV-004 / P2：Worker 停机期间消失的资源不会在首轮完整快照中注销

**状态：已确认；置信度高。类别：重启恢复、持久身份一致性。**

**这是什么：** 资源实体和别名在 PostgreSQL 保留；Graph 本身按当前原生快照重建，两者应能在采集进程重启后重新一致。

**问题及触发：** 所有相关采集 Worker 停止期间，已持久化 Node 被删除并以新 UID 重建，但沿用原来的 providerID。每个新 `collectorSink` 都从空 `previous` 开始，首次快照只 upsert 当前对象，删除判定也只遍历这份内存 map，旧实体仍然 `deleted_at IS NULL`。旧 providerID 别名被当成当前身份候选后，与新 Node UID 冲突，导致同步失败；若没有别名复用，也会留下未注销的持久身份。

**代码证据：** [空 previous](/Users/mssc/Documents/Code/ops/platform/internal/app/sp04_collector.go:20)；[仅遍历 previous](/Users/mssc/Documents/Code/ops/platform/internal/resourcestore/repository.go:222)；同文件 Resolve 第 67 行附近别名候选查询只排除 `deleted_at`；[不同原生 UID 冲突](/Users/mssc/Documents/Code/ops/platform/internal/resource/resolver.go:108)。已搜索资源注销写入口，未发现启动时按来源/GVR对持久集合进行缺失清理的补偿路径。

**验证：** `TestReviewFirstEmptySnapshotDoesNotReconcilePersistentInventory` 在新进程空 map 和已完成空快照下观察到零次数据库访问。持久旧别名→身份冲突为静态调用链证据；没有停止真实 Worker 或删除 Node。

**最小修复：** 在完整原生 List 边界，按租户、来源、集群、GVR 对当前 UID 集合与持久身份做差异同步，清理确实消失的来源关系/身份，再解析会复用别名的新对象。必要的来源归属可复用现有别名 provenance。不能在部分快照、来源失败、过滤后缺失或 Standby 旧快照上全局注销，也不能恢复或持久化第二套完整 Graph。

**修复涉及与验收：** Collector 初始化、快照同步和来源实体归属。覆盖停机删除/重建、空 GVR、同 providerID 新 UID、多来源和新旧快照交错，保持注销的单调性。

**不修复的后果：** 重启后的实时 Graph 与数据库“仍存在”判定不一致；Node 重建可能持续触发身份冲突。与 REV-002 不同，此项需要持久集合恢复，不能靠区分过滤原因单独解决。

### 5. REV-005 / P2：恢复时长统计将不可靠来源时间当作精确样本

**状态：已确认（静态）；置信度高。类别：统计语义、业务正确性。**

**这是什么：** `incidents.recovery-duration` 的目录合同要求只使用可信 `started_at/resolved_at`，缺少有效样本应返回 `no_data`，不能把未知时间混入精确平均值。

**问题及触发：** `timeReliable=false` 的 Finding 首次触发 Incident 时，仍然把其 `StartsAt` 写入 `started_at`。Incident 后续人工或自动 resolved 后，统计 SQL 只判断开始时间非空、结束不早于开始，没有校验开始时间的可靠性，也不把该情况计入 missing。只要时间顺序合法，就可能输出 `Exact=true` 的恢复平均值。例如不可靠的开始时钟比真实发生早一小时，统计会额外计算一小时，目录仍描述为可信恢复时长。

**代码证据：** [Incident 创建](/Users/mssc/Documents/Code/ops/platform/internal/incident/correlator.go:114) 第 114～115 行不以 `f.TimeReliable` 保护 `StartedAt`；[恢复统计 SQL](/Users/mssc/Documents/Code/ops/platform/internal/analytics/controlplane.go:159)；[missing 判定](/Users/mssc/Documents/Code/ops/platform/internal/analytics/statistics.go:9)；期望语义来自 `api/analytics/catalog-v1.json` 的 `incidents.recovery-duration`。

**验证：** 已核对 Finding 的校验允许显式不可靠时间、Incident 创建和状态恢复路径、统计构造及现有统计测试。未运行 PostgreSQL 复现；现有统计用例直接插入时间合法的 Incident，未覆盖来源时间可靠性。

**最小修复：** 在 Incident 持久数据中冻结开始时间的可信来源/可靠性，或使用能证明该原始时间可靠的不可变 Finding 依据。统计时将未验证样本排除并计入缺口；旧记录没有证据时保持未知。不能把非空或时间顺序有效当作时钟可靠，也不能仅根据后续更新的 Finding 值追认过去时间。

**修复涉及与验收：** Incident 创建与必要的迁移、合并/拆分/重开时间语义、analytics 查询及合同测试。混合可靠/不可靠样本时应降级；全无可信样本时应为 `no_data`，并保留现有租户、关联 Finding 和 namespace 授权过滤。

**不修复的后果：** 助手会把未经证明的恢复时长作为平台计算事实引用，影响运维复盘。

### 6. REV-006 / P2：API Deployment 缺少业务就绪探针，滚动替换会提前切流

**状态：已确认（Chart/启动顺序静态）；置信度高。类别：部署可用性。**

**这是什么：** 单副本 API 在初始化数据库、OIDC、SP04/06/07 后才进入 HTTP 服务循环；Chart 应在这一阶段完成后才将 Pod 纳入服务。

**问题及触发：** `deployments.yaml` 没有给 API 容器设置 readinessProbe/startupProbe。更新镜像或带 checksum 的运行配置时，新容器只要进入 Running，就可能被视为可用、触发旧 Pod 删除，但应用仍在 OIDC/数据库等初始化中。结果是 API 请求超时或 Web 代理错误。源码中 TCP `net.Listen` 甚至早于这些初始化，因此仅加 TCP 端口探测也不能证明业务已就绪。

**代码证据：** [API Chart](/Users/mssc/Documents/Code/ops/platform/deploy/charts/ops-platform/templates/deployments.yaml:43) 的整个容器定义没有就绪检查；[先绑定端口](/Users/mssc/Documents/Code/ops/platform/cmd/platform-api/main.go:50)；[完成初始化后 Serve](/Users/mssc/Documents/Code/ops/platform/internal/app/api_runtime.go:125) 第 125～131 行。Web 在独立 `sp08.yaml` 中有探针，不能据此推断 API 也有。

**验证：** 已核对 Chart、Service 选择器和启动顺序；未进行真实 rollout，也没有测量故障持续时间。结论限当前 Chart，不能外推到未检查的现场补丁。

**最小修复：** 复用现有 HTTP/观测监听器增加不携带业务数据的就绪状态，仅在必要初始化完成、可提供业务请求后返回成功；Chart 指向该 HTTP 检查，初始化慢时用 startupProbe 保护。不要用已提前监听的 TCP 端口或单纯存活的 metrics 端点代替业务就绪。

**修复涉及与验收：** API 启动生命周期、就绪 handler 和 Chart；静态渲染与隔离环境延迟 OIDC/数据库的 rollout 验证。保持现有单体结构，无需为此引入服务拆分或新依赖。

**不修复的后果：** 正常配置更新可能造成短暂中断，部署 Ready 也不能代表 API 已可服务。

### 7. REV-007 / P2：迁移工具准入源码仍只覆盖至迁移 37，当前源码检查失败

**状态：已确认（实际命令失败）；置信度高。类别：构建门禁、离线物料一致性。**

**这是什么：** `prepare-migration-source.py --check` 将当前迁移 CLI、全部 SQL 迁移和源码包与独立准入记录比较；`make check` 与离线打包入口依赖该检查。

**问题及触发：** 当前源码包含 `00038`～`00042`，而已准入 `migration-tool-go.lock.json` 的 `firstPartyFiles` 缺少这些文件。本次在锁定 Go 工具链下执行检查退出 1，错误为 `migration source/notice closure has drifted; renew admission evidence`。模块、标准库、已有 firstParty 文件均未发生对比差异，差异已定位为新增的五个迁移，而不是本机工具链漂移。

**代码及验证：** [检查入口](/Users/mssc/Documents/Code/ops/platform/scripts/prepare-migration-source.py:56)，`Makefile` 的 `check-runtime-source` 和第 89 行 `check`；原始失败日志见 [migration-source-check.txt](migration-source-check.txt)，差异见 [migration-drift-details.json](migration-drift-details.json)。运行时源码的另一项检查已通过，两个结果不能合并为通过。

**最小修复：** 在获得对应发行/运行验证授权后，按现有迁移工具准入流程重新准备包含五个迁移的完整源码/notice 闭包，重建相应物料并补充前向迁移证据，再更新匹配的锁及准入记录。不能只复制新摘要、关闭检查或借用旧二进制与旧环境通过证据。

**修复涉及与验收：** 迁移 source lock/admission、实际源码包/工具物料和适用离线前向迁移证据。当前开发阶段可明确记录“发行准入待补”，但不能宣称当前 `make check` 或新助手离线包已通过。本次未执行重建、签名、数据库迁移或发行。

**不修复的后果：** 当前综合检查/打包门禁继续失败；历史合格迁移包无法证明新助手表结构可由该包安装。是否现场已手工迁移，本次没有证据。

## 架构与功能链路判断

- **模块化单体与 API/Worker 分工可沿用。** 选主、任务租约、预算、审计和来源适配器已有共享实现；已确认的问题集中在合同和生命周期衔接。没有证据要求改造成微服务或新增通用框架。
- **权限与执行保护已有实质实现。** OIDC 验签后从平台加载角色；两角色不继承；tenant 事务使用 SET LOCAL；人工执行绑定命令、目标 UID、Profile/Policy，并在 dispatch/claim 前重新授权；不确定结果不会由 reconciler 自动重发。这是所读路径的实现事实，不等于全仓库“无越权”。
- **调查恢复与证据可信性总体有明确边界。** 看到 generation fencing、成功 Step 恢复、当前来源重查、不可变 QueryResult、归档 digest 核对和法律保留串行化。对超时、撤权、来源不可用的完整现场闭环，本次没有新运行证据。
- **故障链不应扩大为确认根因。** 所读 R3/R4/R5 规则要求已提交查询、来源、时间与资源健康依据；DeepFlow 当前 summary 的事件时间仍不可靠，代码保留相应缺口。该限制按 ADR-0040/0041 记录，未把 Fixture 的 timeReliable=true 当成真实来源已满足。
- **前端为源码评审。** 已检查 AuthProvider、OIDC 内存凭据、公共 fetch/SSE、助手页面及范围编辑/证据调查组件，并抽查命令输出安全显示与样式。未观察真实浏览器、窄屏、键盘操作或页面视觉效果；不能给出“UI 美观、可访问性、全页面已验收”结论。
- **精简建议：** 没有确认可直接删除的依赖或死代码，故不虚报可削减行数。优先修复上述共享边界，避免为每个数据集复制一套预算规则。

## 检查回执

完整命令、退出码与说明见 [checks.json](checks.json)。本机环境为 macOS arm64；`go1.27.1`、Node `v24.21.0`、pnpm `12.7.0`、Python `3.12.14` 与仓库声明相符；没有安装或升级依赖。

- 关键后端测试：10 个有测试的包通过，MCP 包无测试文件，见 [go-targeted-tests.txt](go-targeted-tests.txt)。Graph 包耗时约 247.5 秒；没有把单次本机耗时作为生产性能评测。
- 补充领域/适配器测试：14 个有测试的包通过，tenant/resourcestore 无测试文件，见 [go-domain-tests.txt](go-domain-tests.txt)。合计 24 个有测试包通过，另有 3 个无测试包；默认输出未逐项枚举 skip，仓库 Graph 测试保留明确的性能测试豁免。
- `go vet ./internal/... ./cmd/...` 退出 0，见 [go-vet.txt](go-vet.txt)；空日志表示无诊断，不表示完成安全审计。
- Python 故障链确定性测试 12 项通过；当前多调用方 probe 的纯本地测试 2 项通过，见 [Python 故障链](python-fault-chain-tests.txt)、[多调用方](python-multicaller-tests.txt)。
- 本次 4 个审查探针通过，含 Victoria 两个展开子用例；**通过表示当前缺陷行为已被复现**，不是修复验收。源文件位于 [_probes/review_test.go](_probes/review_test.go)，目录以 `_` 开头，不进入常规 `go test ./...` 自动遍历；仅按回执中的显式路径运行。
- 运行时 arm64 源码闭包检查通过；迁移源码检查失败，分别见 [runtime-source-check.txt](runtime-source-check.txt)、[migration-source-check.txt](migration-source-check.txt)。
- 没有执行 `make check` 全套：其包含生成物检查、前端测试等；本文只对列出的实际命令作结论。未运行的检查不记通过，旧的运行证据不替代当前版本验证。

## 修复顺序与验收边界

1. 先修 REV-001 的共享预算成本，以及 REV-002 的过滤/删除区分；两项都有普通授权操作可触发的明确后果。
2. REV-004 与 REV-002 应在同一轮生命周期设计中核对，但分别验证“同 UID 重入范围”和“停机期间真实 UID 消失/重建”。
3. 修 REV-003 的服务端 Profile/token 绑定，再修 REV-005 的开始时间可信依据，避免改变正式预算或伪造统计精确度。
4. 在适用隔离环境验证 REV-006；在发行准入恢复时处理 REV-007。二者不授权生产发布、修改 core/final 或重启本机 OrbStack。

Verdict: 先修查询预算与资源生命周期，再完成模型合同、统计可信性和适用的部署/物料验证；本轮不能标记项目完整验收通过。

Not checked: 全仓库逐函数穷尽阅读、第三方实现逐行审计、依赖漏洞公告、真实 PostgreSQL/集群/OIDC/模型/浏览器、SP-08 全业务复测、生产容量/HA/灾备与离线冷装。没有连接 192.168.0.63；KubeVirt/CDI 继续延期、未验证。
