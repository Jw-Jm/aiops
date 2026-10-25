# 实际验证回执

评审提交：`a2fa14f419abdc4587f56a1cbae59eb6cbb48c4e`。日期：2026-10-23～2026-10-25，Asia/Shanghai。

## 环境

Go 1.27.1、Node v24.21.0、pnpm 12.7.0、uv 0.12.17、Python 3.12.14，与仓库声明一致。使用现有依赖，未升级版本。PATH 中有 Helm，没有 psql/postgres/initdb。

没有读取或打印真实环境连接凭据，没有启动数据库/集群，没有调用实际模型、SSH/BMC 或远端业务 API。测试可使用仓库既有回环 HTTP/TLS Fixture。生成物检查在脚本自己的隔离目录运行。

## 已运行命令

1. `git fetch origin main`：退出 0。main、origin/main、HEAD 相同；固定提交见 [reviewed-commit.txt](evidence/reviewed-commit.txt)。
2. `make check-local`：退出 2。首先执行的 `go vet ./...` 成功，其后 Go 单元套件有 5 个 Bundle 相关失败，后续目标未被这条 make 继续执行。日志见 [check-local.log](evidence/check-local.log)。
3. `make test-contract test-web test-python test-security`：退出 2，在 test-contract 的 `TestDeliveryPreparationSourceBaseline` 失败。该次命令没有执行后续三套测试；它们随后分别独立运行。见 [remaining-local.log](evidence/remaining-local.log)。
4. `make test-web`：退出 0；tsc 成功，Node SSE 6/6，Vitest 37 文件 / 152 项通过。见 [web.log](evidence/web.log)。jsdom 打印缺少 Canvas getContext 的环境提示，测试未失败。
5. `make test-python`：退出 0；Investigator 93 项通过，DeepFlow/assistant-eval/assistant-runtime 脚本分别 10、9、6 项通过，共 25 项。见 [python.log](evidence/python.log)。两条第三方 deprecation warning 不作为失败。
6. `make test-security`：退出 0；报告校验器确认 13 项通过、零 skip。见 [security.log](evidence/security.log)。
7. `make check-generated check-runtime-source`：退出 2，在生成一致性阶段失败，未由该命令继续执行 source check。见 [delivery-checks.log](evidence/delivery-checks.log)。
8. `python3 scripts/prepare-runtime-source.py --check`：退出 1，选中 runtime source/module/license closure 漂移。见 [runtime-source.log](evidence/runtime-source.log)。
9. `python3 scripts/prepare-migration-source.py --check`：退出 1，迁移源码/notice 闭包漂移。见 [migration-source.log](evidence/migration-source.log)。
10. `go test ./test/integration -run '^TestMigrationScriptsAreForwardOnlyAndContiguous$' -count=1 -v`：退出 1；17 个 00047–00063 迁移含门禁不允许的 Down 标记。该用例仅静态读文件，不需要 PostgreSQL。见 [forward-migration-static.log](evidence/forward-migration-static.log)。
11. `pnpm --dir web build`：退出 0；TypeScript 与 Vite 构建成功。见 [web-build.log](evidence/web-build.log)。部分 chunk 大于 500 kB 的提示保留，没有据此推断浏览器体验或容量已经验证。
12. `python3 docs/reviews/main-2026-10-23/probes/run.py`：退出 0；Node 1 项、Go 10 项、React 2 项，共 13 项当前行为复现通过；2026-10-25 统一重跑退出 0。见下一节。

没有再次直接运行顶层 `make check`：其生成、source、unit、Contract 前置已分别出现有效失败，重复运行不会增加证据。测试日志含 `(cached)` 的 Go 包使用 Go 的现有缓存；没有把这些条目描述成全部强制重跑。

## 五个 Bundle 测试失败

- cmd/opsctl：`TestBundleBuildCLIAndIndependentVerify`。
- internal/bundle：`TestImportValidatesEveryLayerBeforeAnyRuntimeCall`、`TestPlanImportReturnsEveryImageWithoutRuntimeSideEffects`、`TestImportVerifiedOCIArchivesWithoutPull`、`TestInstallIncompleteBundleNeverRunsHelmOrRuntime`。

错误指向当前 `opa-sdk-source` 与锁定 corresponding-source 摘要不一致。其它单元包完成测试；不能因为这五项属于打包路径就跳过或改成 PASS。

## 新增复现检查及其证明边界

[run.py](probes/run.py) 在临时目录构建 Go overlay，把本目录的 `.go.txt` 映射成被评审包的虚拟测试文件，再直接运行生产函数。不写入 internal，不修改原有测试。React 诊断复用现有范围夹具，在 web/tests 或 web/tests/v12 创建唯一临时副本，结束后 finally 删除；只运行新增的诊断用例。所有替身仅用于本地分支与控制流验证。

- **原生 SSE**：生产 reader 收到 Cookie RequestInit 后立即抛错，fetch=0。证明客户端认证假设冲突；没有浏览器 Cookie 联调。
- **Graph 预算**：同一合法资源统计输入先通过 Compile，再被真实 toolReservation 拒绝；增加窗口后 Compile 拒绝。证明两层合同互斥；没有调用模型或真实 Graph 服务。
- **v2 Job 身份**：真实 current → EffectiveTx，模拟租户/集群有效、旧角色表为空，得到 ErrDenied。证明 Worker 选择了旧表，完整数据库状态转换由 Claim 源码核对；没有宣称实际跑了 Create → Claim 的 PostgreSQL 场景。
- **命令 MFA**：真实 TouchCurrentStepUpSession 在缺 intent 的原生 ctx 和无 Session 的异步原生 actor 下均在查库前拒绝；Ack/Dispatch/Claim 调用位置通过源码核对。没有产生 Runner 或执行命令。
- **多集群历史关系**：真实 observedRelations 对单候选保留关系，两个集群同名候选时返回空；SQL 字符串确认未限制 cluster。模拟行集是局部复现，不等同于 PostgreSQL 查询计划验证。
- **采集降级指标**：真实 Metrics 对所有 SP05 生产 key 忽略置位，支持的 audit_signing key 正向对照成功。未改变运行采集器。

- **助手忙碌状态**：真实 React 页面通过 ClusterScopeProvider 的集群选择器逻辑切换，旧 create 成功返回后新范围按钮仍为 loading，点击未新增 API 调用。使用 jsdom，无真实浏览器或服务端。
- **Incident 原生权限**：真实 Authorize 收到合法 scope 和原生 context 后仍调用旧 SQL 函数；模拟旧表无匹配返回 false 时拒绝。唯一 SQL 定义与全部迁移集合已静态核对；未运行真实数据库锁或撤权。

- **故障链迟到响应**：真实 GapCheck/请求客户端在子组件卸载后仍调用父 setter；父容器模拟已核对的页面回调，在会话 B 收到会话 A 的 turn。无真实服务端、整个页面或浏览器联调。

- **OpenBao 不确定提交**：真实 EnsureBootstrapTLS、临时外部 CA 路径与 fake kubectl 模拟“提交 Secret 后返回失败”，重试不能信任已有 Secret。没有集群请求或真实 Secret。
- **续签与 CRL**：真实 PKI、回环 TLS 夹具和可控时钟验证 sign 失败阻断可达 CRL；独立刷新后未过期身份恢复。没有等待真实证书轮换周期。
- **原生签名 RPC**：真实 SignContext/VerifyContext 后以无浏览器 Worker context 调 EffectiveTx，旧授权表为空时拒绝。使用假事务，不是完整 API/Worker/数据库集成。
- **Node Unknown 恢复**：真实 Signals/SignalKey 对同 UID 的 Unknown→True，确认旧 Unknown key 没有 resolved 候选；Finding/Incident 持久影响由源码链核对，未跑数据库。

13 项诊断只运行新增断言；两个 React 临时文件还包含既有夹具用例，因 -t 选择而显示的 8 个 skipped 不是已运行的回归，也没有计入这 13 项。完整前端回归的 152 项来自上面的独立 test-web。

MR-014（MFA reset 版本连接）、MR-018（Step/QueryResult 变量绑定）、MR-020（跨 Challenge 失败额度）采用静态不变量/状态转移证明，不计入这 13 项动态诊断；各自的真实数据库验收仍待执行。

最终统一探针日志：[fault-chain-late-probe.log](evidence/fault-chain-late-probe.log)、[probe-run.log](evidence/probe-run.log)、[assistant-busy-probe.log](evidence/assistant-busy-probe.log)、[go-probes.log](evidence/go-probes.log)、[native-sse-probe.log](evidence/native-sse-probe.log)。较早独立执行的 [graph-budget-probe.log](evidence/graph-budget-probe.log)、[native-v2-probe.log](evidence/native-v2-probe.log) 保留作定位证据；以统一日志为最终复现结果。

## 隔离生成与闭包定位

- sqlc 在临时复制的 migrations/queries/config 上运行，生成差异见 [sqlc-models.diff](evidence/sqlc-models.diff)，退出 0；生产 generated 文件未改。
- runtime source prepare 在临时目录分别生成 arm64/amd64 对应闭包，比较已有 reviewed lock，定位差异见 [runtime-closure-diff.json](evidence/runtime-closure-diff.json)，定位脚本退出 0。两个 `matchesReviewedLock=false` 表示材料不一致，不能把“定位脚本成功”误读成准入成功。
- 全仓文件盘点为 19,787，见 [inventory-summary.json](evidence/inventory-summary.json)。文件分类为机械分类，不是阅读覆盖率。

## 补充全量合同与材料检查（2026-10-25）

- 使用 [contracts.go.txt](probes/contracts.go.txt) 的临时 Go overlay，在 internal/contract 包运行 TestReviewCompileAllContracts，退出 0：136 份嵌入根 Schema 及各自 definitions 全量编译。日志 [all-contracts.log](evidence/all-contracts.log)。这是结构/引用编译，不是所有实例和攻击向量的验证。
- 使用现有 Investigator venv 运行 [materials.py](probes/materials.py)，退出 0；结果 [material-checks.json](evidence/material-checks.json)。8 份 registry 共 4,095 个 scope 的唯一性、来源摘要字段、保留 notice/ADR 摘要匹配；两架构 Catalog 的现有材料结构；3 个 Chart 的安全目录、版本和归档摘要；Investigator 182 个锁定包、Runner 14 个精确 requirements、pnpm 两文档锁结构解析。依赖结构核验不宣称上游实现或公共 CVE 已审计。
- 公共 OpenAPI 109 个操作、521 个引用可解析；内部 Graph 1 个操作、5 个引用可解析。消费者语义结合人工审查，存在合同/运行差异时仍以具体问题报告。
- Catalog 中部分 notice 路径属于上游源码归档，未当作本地丢失文件，也未声称已读原始归档。material-checks.json 逐组件记录该数量。完整源码材料重建仍被 MR-006/007 阻断。
- 历史 build/offline-amd64 replacements 的模块声明与当前 go.mod 相同，99 个 replacement 都是历史本机绝对路径；它不是可移植的重新准入证明。当前源码闭包漂移按 MR-006 处理。
- 2026-10-25 再次 git fetch origin main，退出 0；main、origin/main、HEAD 仍为同一评审提交。结束时 git status 仅有本报告目录未跟踪，业务实现和原测试无改动，临时 React 文件已删除。

## 未执行

真实 PostgreSQL 迁移/RLS/并发撤权/任务恢复、真实模型及回答质量、Playwright 与真实 OIDC/native 浏览器、Kubernetes/BMC/SSH、断网 Bundle 安装/干净重装、回放与容量验收均未执行。KubeVirt/CDI 不在当前实施/验收范围。

MR-009 的 OOM 风险来自代码与默认配额推导，没有压力攻击或 RSS 测量。没有借历史环境回执为本轮提交签发运行通过结论。
