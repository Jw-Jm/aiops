# 修复状态（2026-10-16，处置修复后的工作区）

修复基线为 `main / 4dd81de8`。本轮修复、审查记录及验证回执随源码提交同步 GitHub；未部署、未发布。下文验证结论对应本次提交内容。
本文件替代此前的“16 已修复 / 6 部分 / 3 裁定 / 14 未修复”快照。
“代码已修复”与“真实环境已验收”分开记录；当前不能宣称整体门禁或运行验收通过。
未修改上级 00～10 正式文档，未恢复 KubeVirt/CDI、Trace 或生产发布。

## 一、本轮新增修复

- **PWR-003**：investigation 复用现有 `modelprofile` 包，由 SP06 装配层注入已准入 Profile。AllocateModel 与 BeginCall 都绑定冻结的 Job Profile 引用，校验输入/输出预留；v2 保留 16K 规则，预算总额不提高。覆盖 8K/16K/32K/64K 上界、伪造输入、超额输出、引用不符、缺失配置与 v2 兼容的单元测试通过。较大 Profile 若超过 Job 总预算仍应拒绝。真实 PostgreSQL 中 8K/32K Profile 的预留等于上界、Profile 替换拒绝已通过；真实模型调用联调待执行。
- **PWR-006**：新增 ReconcileNativeSnapshot，在 collector 首次健康完整原生快照上，用持久身份/来源别名与原生 UID 集合求差；先注销旧身份，再解析新身份，避免同 providerID 重建阻塞。限定 tenant/source/cluster/GVR，拒绝撤销来源与 ObservationOnly/失败快照，不注销更新于快照时间的实体。复用现有表，无新增迁移。空进程状态、过滤后不可见、其它 Kind、撤销来源的单元测试通过；真实 PostgreSQL 用例已通过，包含新 Worker 无进程历史时注销已消失 UID。
- **PWR-007**：不可靠 Finding 的 started_at 写 NULL，创建与拆分共用同一判定。统计通过关联 Finding 的 v2 合同、可靠性与相同 startsAt 证明样本；历史无证明记录计入缺口，不回填虚假时间。复用已有可空列，无新增迁移。单元测试通过；真实 PostgreSQL 统计集成已通过，包含可靠来源及有值但无证明的历史样本；修正夹具参数的 timestamptz 类型推断。
- **PWR-008**：Worker 复用既有 metrics 监听，增加 healthz/readyz；初始化完成前 readyz=503，全部必需组件启动后为 200，退出时撤销 Ready。Chart 为 Worker 增加 startup/readiness/liveness 探针。就绪状态单测和真实 Helm 模板渲染通过；真实 rollout/依赖故障待验证。
- **PWR-009**：清理改为独立函数，finally 归还并发槽位。新增直接执行生产函数的轻量测试，验证两个 close 均异常、以及 KeyboardInterrupt 时槽位仍回收且不回显异常正文。2 个标准库 unittest 通过，不依赖模型/TLS 包。
- **PWR-012**：Chart schema 覆盖模板结构、组件、runtime、SP04～08、网络策略和模型预算；拒绝顶层/结构字段拼写错误及错误类型。保留动态字典、pending 网络模式的合法 null 与未启用阶段默认值。新增 Helm 反例门禁，现有 pending/阶段渲染通过。
- **PWR-015**：历史 task27-cleanup 入口即使显式 go run 也只报错退出，不再执行删除；两个只读历史探针保留构建约束。
- **PWR-016**：拆分风险确认 Required/Expired/ScopeMismatch/CommandMismatch；HTTP 返回相应 412/409 和明确错误码，确认归属不符拒绝，数据库异常不误报为确认缺失。保留幂等重放不重新分发的语义。同步 OpenAPI、错误 schema、生成 Go/TS 绑定及前端提示。错误映射、action 单测和 PostgreSQL 执行流程均通过；新增缺失/过期/命令变化/作用域变化错误断言，复用已消费确认返回冲突，原幂等键仍重放原执行。
- **PWR-018**：身份冲突写入 Finding v2 聚合，包含 findingId、revision、sourceRevision、namespace、cluster、digest 等；outbox 使用 finding/v2。单元测试验证合同及投递字段。真实 PostgreSQL 用例已通过 RelayPass → Incident 消费链。旧 v1 历史记录/死信不修改、不冒充新合同重新投递。
- **PWR-025**：SP05 列表及 Incident 子列表使用既有 Ed25519 签名游标，绑定 tenant、subject、scope 和查询，并沿用现有有效期；无签名配置时拒绝签发。跨用户游标在访问数据库前被拒绝的测试通过。旧未签名游标会失效，需要重新读取第一页。
- **PWR-027**：Redfish 凭证从投影文件按每个受控 GET 读取，轮换不需重启；缺失/无效文件失败关闭，不退回旧凭证。保留原有 origin/method/path/TLS 限制。轮换与文件丢失回归通过。
- **PWR-028**：API/Worker 入口将默认 slog 设为现有脱敏 logger，包级日志进入同一脱敏路径；ExpirePass 错误不再丢弃。Go 构建、静态检查、相关包测试通过。
- **PWR-031（辅助入口）**：顶层 test-integration 同时精确排除 Assistant 与 SP05 两个子进程 helper；父测试仍显式运行它们。排除验证 exit=0，helper run/skip 事件均为 0。
- **PWR-035（小范围清理）**：删除无调用的 evidenceNamespace、保留 import 的占位及对应未用导入；其余重复实现不进行无收益重构。
- **PWR-037（架构部分）**：metrics-server 的架构由安装器已有 Architecture 字段明确传入 Chart，schema 同时绑定架构与准入镜像，取消摘要三元推断。两个架构的 Helm 渲染及既有攻击夹具测试通过。
- **PWR-038（预算/可观测性部分）**：内部受认证 Job API 增加预算元数据读取；Python 初始及恢复缓存步骤后读取实时剩余预算，不沿用 Job 上限或历史回执预算。移除“各来源及反证均可追溯”的固定过度声明，续签失败输出静态故障类别。17 个 fault-chain unittest 通过，含低剩余预算恢复回归。
- **PWR-039（核心覆盖）**：新增 MCP 网关测试，覆盖 50 次日志展开预留不能进入仅剩 1 次预算的场景、非法展开拒绝及无身份请求在持久化之前拒绝。不能据此宣称全部管理面已覆盖。
- **PWR-040**：恢复 00038 的原始文件，不再编辑历史迁移。门禁仅以文件名及完整 SHA-256 放行这一精确历史文件，任何其它/改动后的 Down 仍拒绝；运行手册明确这不授权执行 Down。静态迁移门禁通过。没有更改任何迁移 SQL，亦无需所有者批准历史文件修改例外。

## 二、此前修复的保留状态

PWR-001、002、005、011、013、017、019～024、029、032、036 的既有改动保留。
PWR-029 的剩余栏为“无”，不再列为部分修复。
PWR-013 的生成物一致性检查已实际执行并通过；PWR-009 不再仅依赖 compileall。
PWR-002 的真实标签切换与重新进入来源范围仍待集群验收；PWR-024 的数据库 step-up 撤销、当前授权及重放相关用例已通过。

## 三、处置结论与未完成项

- **PWR-004：已修复（2026-10-17，见第六节）。** 修复前默认 arm64 的 prepare-runtime-source.py --check 与 prepare-migration-source.py --check 均实测 exit=1，internal/bundle 4 个与 cmd/opsctl 1 个测试失败。经诊断，两条链的漂移原因不同，因此处置方式不同：运行时链的漂移来自 PWR-006 新增文件 internal/resourcestore/reconcile.go 对 k8s.io/apimachinery/pkg/api/meta 的依赖（编译闭包多选入 11 个 pkg/api/meta 文件），已通过在该文件中改用快照自带的精确 Kind 比较而消除，运行时源闭包回到已准入状态，**未修改任何运行时准入证据**；迁移链的漂移来自真实的迁移新增（00038–00043），按既有准入流程重签。原始失败日志见 evidence/repair-source-admission.txt 与 evidence/migration-source-check.txt，作为修复前状态保留。
- **PWR-010：公共路径/方法注册已绑定合同。** api.Schemas 嵌入原始 OpenAPI，foundation 复用既有 YAML 库读取路径/方法并注册 Chi 路由，再复用现有领域处理器；不增加大量手写转发适配。测试验证公开/动态路径可达、未声明路径 404、未声明方法 405，且不进入领域处理器。httpapi/app/contract/security 全部通过。它约束路由注册，不等于自动校验全部请求/响应 schema；逐 operation 的完整领域行为覆盖仍未完成。
- **PWR-026：硬件命令的完整后检查仍未实现。** 现有后检查只对 Node/PVC 提供受控原生健康判断；不能仅靠命令退出码、BMC 可连接或不完整 DIMM 证据给出 resolved。硬件路径继续诚实返回 inconclusive，需补受支持的 Recipe/来源证据与实际验证。
- **PWR-031：不添加 selfAttested 标记来暗示测试断言是被测系统自证。** 性能豁免已有“capacity is not passed”等明确日志；不把豁免当容量验收。不为清理磁盘删除合规保留数据库。其它断言覆盖缺口未宣称全部完成。
- **PWR-035：dbgen 仍受生成门禁管理；LiveMapper.ByBackend 仍实现接口并有测试调用。** 不机械删除这些代码，不为合并 token/CRL 实现开展额外跨包重构。
- **PWR-037：不统一禁止合法开放映射的 additionalProperties。** 除明确架构传参外，逐 operation schema 兼容性评估仍属覆盖缺口。
- **PWR-038：任务分类的文本规则仍保留。** 未凭空新增服务端分类协议；后续应以具体误分类案例和冻结合同决定是否改变。模型回答质量及完整 Python 锁定环境测试未完成。

裁定记录：
- **PWR-014**：只支持前向执行，保留历史迁移原件；不增加 minAvailable=0 的无效 PDB。没有执行数据库 down。
- **PWR-030**：当前 GET /admin/tenants 只读取当前租户，并非枚举本部署所有租户。普通用户可跨租户枚举未获可达证据；数据库角色仍可读租户表，不将应用层过滤等同于 RLS。暂保留权限，不作“全局管理员枚举”这一错误论证。
- **PWR-033**：公开 steps 读取不消耗 Job 预算；内部模型恢复读取继续计费。既有注释保留。
- **PWR-034**：当前层保留浏览器亚秒时间表达；故障链入口继续执行自身秒精度限制，不凭空统一两种时间契约。

## 四、本轮验证回执

通过：
- go build ./...、go vet ./...。
- go test -count=1 ./internal/... ./cmd/... ./test/contract ./test/security 中，除下一段列出的 5 个发行准入用例外，其余包通过；包含约 261 秒的 graph 测试，不设置性能豁免。
- 核心修复包在后续修改后重新定向运行通过：action、httpapi、app、investigation/mcp、resourcestore、incident、analytics、redfish、observability。
- metrics-server 两架构、pending 模型三个安装阶段的真实 Helm 渲染通过；顶层、runtime、端口、预算字段的 schema 反例通过。
- TestMigrationScriptsAreForwardOnlyAndContiguous 通过；两个 helper 排除检查 run=0、skip=0。
- go generate ./api 及 check-generated.py（包括 assistant-output.schema.json）均退出 0。
- 前端 tsc --noEmit 通过；pnpm --dir web test：5 个 Node SSE 测试、23 个 Vitest 文件/75 个测试通过。
- Python fault-chain 标准库 unittest：17 个通过；生产槽位清理函数测试：2 个通过；调查服务源码 AST 解析通过。后续使用 Python 3.12.14、uv sync --locked --offline 与 uv run --locked --offline pytest -q，完整调查服务测试 84 项通过。

修复前未通过（该五项与 make check 的 exit=2 已于 2026-10-17 修复，见第六节；本段保留修复前状态）：
- internal/bundle 的 TestImportValidatesEveryLayerBeforeAnyRuntimeCall、TestPlanImportReturnsEveryImageWithoutRuntimeSideEffects、TestImportVerifiedOCIArchivesWithoutPull、TestInstallIncompleteBundleNeverRunsHelmOrRuntime。
- cmd/opsctl 的 TestBundleBuildCLIAndIndependentVerify。
- 上述 5 项均因当前生成的 opa-sdk-source runtime 源材料与既有准入摘要不一致而失败；必须完成 PWR-004 的对应源码重新准入，不能修改测试绕过校验。
- prepare-runtime-source.py --check 与 prepare-migration-source.py --check 均失败；实际执行 make check，生成物检查通过后在 runtime 来源锁检查失败（exit=2）；不将其记录成未运行。

真实隔离环境（192.168.0.63）已恢复 root SSH，使用新的 `/opt/ops-pwr-review-20261016` 目录；未覆盖正在运行的开发工作副本、未部署服务。当前源码交叉编译的测试程序在真实 PostgreSQL 17.11、已有 OpenBao 和精确缓存 SeaweedFS 镜像上运行：**35 个顶层集成用例全部 PASS，exit=0，SKIP=0**，包含前滚至 00043、角色/RLS、来源管理、恢复预算、MCP 15 工具状态矩阵、D1 归档权限、8K/32K Profile 预留和四类风险确认错误。测试创建独立数据库；含 Audit/Evidence 的材料按既有保留逻辑保留。原始失败与最终回执分别保留，未通过调整历史迁移或权限来使测试通过。

本轮额外修正 4 类集成夹具/断言：统计时间参数显式 timestamptz；角色断言允许 00036 已授予 Worker 的执行读取，同时断言 INSERT/DELETE 禁止；风险确认复用断言匹配已消费回执的冲突语义，并增加明确错误类型回归；真实比较断言按合同校验方向与绝对变化量，覆盖下降场景，不再把负差值与正幅度直接比较。初始并发启动两个迁移测试进程时出现集群级角色 ALTER 冲突，后续改为串行运行；该失败不归咎于数据库不可用。

来源专项最终通过 `TestAssistantActualCollectedSourcesAndCapacity`（exit=0，SKIP=0）：在原有两 Pod canary 上新采集 7 分钟、84 周期、1,340 条指标及 1,222 条日志，采集 errors=0；11 组聚合/趋势/比较/样本查询及 4×25 次有界并发查询通过。6 分钟尝试不足以覆盖 CPU 的 5 分钟回看与窗口边界，保留失败并重采；未降低门禁。比较下降场景暴露的旧断言已修正为校验方向及绝对幅度。当前工作区对应的 903 个源码/合同文件与远程副本最终一致。

**五来源联合验收仍未通过**：独立 HTTP canary 实际完成 1,000 响应、其中 50 个 503、transportFailures=0；DeepFlow 未返回可对账的完成 HTTP 采集点（本轮 metadataCounts 为空），独立 source proof 为 FAIL。未把空来源或历史回执当成功，也未修改 DeepFlow 部署/捕获策略。以上 PostgreSQL 与 Kubernetes/Victoria 通过不能替代此门禁。真实 BMC 轮换、浏览器交互、离线 Bundle 重建/干净重装、真实模型调用及完整 test-integration 所需其它发行环境仍未验收。
历史 coverage.csv/functions.csv 及旧报告的阅读覆盖声明不因本次修复自动升级为已完成。

最终回执索引：`evidence/remote-validation/validation-summary.json`、`database-complete.log`、`source-capacity-r4.log`；失败尝试保留在同目录。候选离线构建只记作 `evidence/repair-offline-source-build.json`，未晋升发行准入。

## 五之二、PWR-004 修复轮次（2026-10-17）

基线 `main / 55d6e1c`。本轮只处置 PWR-004 及其暴露的运行时源闭包漂移，未恢复虚拟化、Trace 或生产发布；上一节记录的历史结论与失败证据未改写。

### 一、两条准入链的漂移原因与处置

| 链 | 漂移原因（实测） | 处置 |
|---|---|---|
| 运行时源 | `internal/resourcestore/reconcile.go`（PWR-006 新增文件）导入 `k8s.io/apimachinery/pkg/api/meta`，其编译闭包相对既有锁多选入 11 个 `pkg/api/meta/*` 文件（`k8s.io/apimachinery` 130→141） | 不再从 Kind 猜测资源复数形式：`kubernetes.GVR` 增加精确 `Kind` 字段（`Key()` 仍只用 group/version/resource，持久化键不变），`CoreRequiredGVRs()` 填充全部 17 项 Kind，`ReconcileNativeSnapshot` 改为比较 canonical 身份的精确 Kind 与 API 组，并在缺少 Kind 时 fail-closed。闭包回到 3043 个模块文件，**运行时准入证据一字未改** |
| 迁移源 | 真实的迁移新增：既有锁的 `firstPartyFiles` 不含 `00038`–`00043`（其余 modules/标准库/上游文件逐项相同） | 按既有准入流程重签：`prepare-migration-source.py --out`（arm64/amd64）→ 更新 4 份 lock（`bundle/evidence/**` 与 `third_party/admission/**` 双副本）→ 更新 4 份 source 准入 json 的 `sourceBundleSHA256`、`scope` 与证据路径 → 同步 `internal/bundle/migration_admission_test.go` 的摘要常量 |

`binarySHA256` 与 `firstPartySourceSHA256` **未变更**：`cmd/db-migrate` 不内嵌迁移，`main.go` 摘要仍为 `sha256:87571abb…`，且用重签后的归档离线重建逐字节复现了两个架构已准入的二进制。

### 二、本轮验证回执

通过：
- `prepare-runtime-source.py --check`（arm64）与 `--check --architecture amd64` 均 exit=0，摘要分别为 `sha256:14f4060c…` 与 `sha256:f9530a88…`（与既有 catalog/POC 记录一致）。
- `prepare-migration-source.py --check`（arm64）与 `--check --architecture amd64` 均 exit=0，摘要分别为 `sha256:6d1a43a6…` 与 `sha256:3ff52bd1…`。
- 此前失败的 5 个用例（`internal/bundle` 4 个 + `cmd/opsctl` 的 `TestBundleBuildCLIAndIndependentVerify`）全部通过；`go test -count=1 ./internal/... ./cmd/... ./test/contract ./test/security` 无失败。
- `make check-generated`、`make check-runtime-source`、`make lint` 通过。
- 隔离离线重建（空 GOMODCACHE、`GOPROXY=off`、`GOSUMDB=off`、`GOTOOLCHAIN=local`、`go build -mod=mod -trimpath -buildvcs=false`）：两架构均 exit=0 且二进制摘要与准入值逐字节一致，见 `docs/evidence/pwr004-migration-renew-20261016/offline-rebuild-{arm64,amd64}.json`。
- 远程隔离环境（`192.168.0.63`，专属目录 `/opt/ops-pwr004-verify-20261017`）：`--network none` 的 PostgreSQL 17.11 容器内由重签归档重建的 amd64 db-migrate 完成 00001–00043 前滚与幂等重跑，`forwardVersion=43`、`idempotent=true`，见 `docs/evidence/pwr004-migration-renew-20261016/forward-migration-amd64.json`。
- 同一远程环境中，用当前源码交叉编译的 `test/integration` 二进制（sha256 `6e9b2d52…`）在真实 PostgreSQL/OpenBao/SeaweedFS 上运行 35 个顶层集成用例：PASS 35、FAIL 0、SKIP 0、exit=0，包含直接调用改写后 `ReconcileNativeSnapshot` 的 `TestSP04IdentityConflictFindingAndTerminalTombstone`。

未完成（如实记录）：
- **arm64 的 native 前滚迁移未在本环境执行**：隔离验证主机为 x86_64 且未注册 aarch64 binfmt/QEMU，本机为 darwin/arm64 且本轮不使用本机容器。已完成的等效证据（归档离线重建复现同一二进制、两个架构同一份迁移 SQL、amd64 native 前滚通过）见 `docs/evidence/pwr004-migration-renew-20261016/forward-migration-arm64.json`。
- `make check` 的 `test-unit` 目标在无环境主机上仍失败：`go test ./...` 会执行 `test/integration` 的 live gate（要求真实 PostgreSQL/SeaweedFS/K8sGPT 前置），这些用例设计为 fail-closed 而非 skip。本轮在远程真实环境用 35 个用例覆盖了该集合的核心部分，其余 live gate 仍需各自环境。
- 上一节“四、本轮验证回执”中的动态验收缺口（五来源联合验收、离线 Bundle 重建/干净重装、真实模型调用、BMC 轮换、浏览器交互）状态不变。
