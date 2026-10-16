# 检查回执（2026-10-16，基线 HEAD `4dd81de8`）

本文件记录本轮**实际执行**的检查、命令、退出码与结果。未执行、跳过、失败的检查分别登记，**不写成通过**。机器可读版本见 [evidence/checks.json](evidence/checks.json)。

## 一、环境

| 项 | 值 |
|---|---|
| 本机 | macOS arm64（**不含** Docker/PostgreSQL/K8s/浏览器运行） |
| Go | `go1.27.1 darwin/arm64` |
| Node / pnpm | `v24.21.0` / `12.7.0` |
| Python | `3.12.14`（系统与 mise 环境，**未安装 pytest**） |
| 依赖 | 未安装、未升级任何依赖（`web/node_modules` 为既有） |
| 磁盘 | 审查期间根卷可用空间一度降至 **485MB**，造成部分 Go 测试二进制链接失败（`no space left on device`）；系统回收后重跑成功 |

## 二、基线核对（只读）

```sh
$ git rev-parse --abbrev-ref HEAD          # main
$ git rev-parse HEAD                       # 4dd81de8e7a11b58daf6451af958ade6d6f7fd9f
$ git rev-parse 'HEAD^{tree}'              # a57a272fed2ebbc491f9340b63596655b64d5d93
$ git status --porcelain
?? docs/plans/project-wide-code-review-prompt.md
?? docs/plans/v1.0-remaining-work-agent-handoff.md
?? docs/reviews/
```

审查开始时、检查期间与结束时 HEAD 均为 `4dd81de8`；工作区差异清单未变化（仅 3 个预先存在的未跟踪项），**本次未修改任何被审查文件**。快照见 [evidence/baseline.txt](evidence/baseline.txt)。

## 三、已执行的检查

### 3.1 静态检查

| 检查 | 命令 | 结果 |
|---|---|---|
| Go 静态诊断 | `go vet ./internal/... ./cmd/...` | **退出 0**，无诊断输出（空输出不等于完成安全审计） |
| 前端类型检查 | `cd web && ./node_modules/.bin/tsc --noEmit -p tsconfig.json` | **退出 0**，无类型错误 |
| 前端生产构建 | `cd web && ./node_modules/.bin/vite build` | **退出 0**（`✓ built in 699ms`） |
| 覆盖台账生成 | 只读工具（`go/ast` + 正则）扫描 `git ls-files`；工具现位于 [_tools/ledger/](_tools/ledger/) | `files=12314 functions=4643`（r1 为 4,503，漏掉全部 TSX；已修正并重跑，见 [corrections.md](corrections.md) 第 4 条）→ 写入 [coverage.csv](coverage.csv)、[functions.csv](functions.csv) |

### 3.2 Go 测试

| 检查 | 命令 | 结果 |
|---|---|---|
| 相关包（非缓存） | `go test -count=1 ./internal/investigation ./internal/analytics ./internal/assistant ./internal/resourcestore ./internal/incident ./internal/finding ./internal/httpapi ./internal/auth` | **全部 ok**（1.6s / 2.8s / 0.7s / 1.2s / 3.2s / 2.2s / 3.9s / 4.5s） |
| 认证包 | `go test -count=1 ./internal/auth` | ok |
| 契约测试 | `go test -count=1 ./test/contract` | **ok 39.029s** |
| 安全测试 | `go test -count=1 ./test/security` | **ok 0.457s** |
| 无测试包 | `go test -count=1 ./internal/tenant` | `[no test files]`（tenant 仅有 gated 集成覆盖） |

> 首次以 `-count=1` 运行时，`internal/investigation/analytics/assistant/incident/finding/httpapi` 因磁盘耗尽**链接失败**（`no space left on device`），非代码失败；系统回收空间后重跑全部通过。该中间失败如实记录，不合并为"通过"。

### 3.3 复现探针（复用上一轮目录，按当前 HEAD 重跑）

```sh
$ go test -count=1 -v ./docs/reviews/2026-10-15-project-review/_probes/
--- PASS: TestReviewAccepted32KProfileRejectedBeforeModelAdmission
        valid contextWindow=32768; AllocateModel rejects its inputTokens=32768 as BUDGET_EXHAUSTED before database access
--- PASS: TestReviewVictoriaExpandedRawQueries/logs.records/trend
        one execute_analysis_query expands to 50 source HTTP requests; result rows=50
--- PASS: TestReviewVictoriaExpandedRawQueries/metrics.pod-memory/compare
        one execute_analysis_query expands to 2 source HTTP requests; result rows=3
--- PASS: TestReviewFilteredAbsenceBecomesTerminalRetirement
        absence from filtered Objects causes UPDATE deleted_at even without a native deletion
--- PASS: TestReviewFirstEmptySnapshotDoesNotReconcilePersistentInventory
        first empty snapshot with fresh process-local previous map performs zero persistence reads/writes
ok  ops-platform/docs/reviews/2026-10-15-project-review/_probes  0.870s
```

**注意语义**：这里的 PASS 表示"缺陷行为被复现"，**不是修复验收**。探针位于以 `_` 开头的目录，不进入 `go test ./...`，仅按显式路径运行。原始日志见 [evidence/review-probes-rerun.txt](evidence/review-probes-rerun.txt)。对应问题：PWR-001、PWR-002、PWR-003、PWR-006。

### 3.4 门禁与准入（关键失败项）

| 检查 | 命令 | 结果 |
|---|---|---|
| 迁移源码准入 | `python3 scripts/prepare-migration-source.py --check` | **失败（exit 1）**：`ValueError: migration source/notice closure has drifted; renew admission evidence` → PWR-004 |
| 集成"零 skip"门禁 | `go test ./test/integration -run '^TestAssistantCrashWorkerChild$' -count=1 -json` 后 `python3 scripts/check-test-report.py <report>` | **失败（exit 1）**：报告中存在 `{"Action":"skip","Test":"TestAssistantCrashWorkerChild"}` → PWR-005 |

日志：[evidence/migration-source-check.txt](evidence/migration-source-check.txt)、[evidence/skip-gate.txt](evidence/skip-gate.txt)。

### 3.5 Python

| 检查 | 命令 | 结果 |
|---|---|---|
| 评分器 | `python3 -m unittest discover -s scripts/assistant-eval -p 'test_*.py'` | **Ran 9 tests — OK** |
| 调查服务测试 | `cd services/investigator && PYTHONPATH=src python3 -m unittest discover -s tests -p 'test_*.py'` | **阻塞**：`Ran 28 tests`，其中 **9 项 error**，全部为 `ModuleNotFoundError: No module named 'pytest'`（`test_workload_tls.py:5` 等直接 `import pytest`）。本机无 pytest，未安装依赖；**该套件在本环境未通过验证**，需在项目锁定环境执行 |

### 3.6 前端

| 检查 | 命令 | 结果 |
|---|---|---|
| 单元测试 | `cd web && ./node_modules/.bin/vitest run` | **23 个测试文件全部通过，退出 0** |
| 类型检查 | `tsc --noEmit` | 退出 0 |
| 生产构建 | `vite build` | 退出 0 |

日志：[evidence/web-vitest.txt](evidence/web-vitest.txt)。首次尝试使用 `--reporter=basic` 失败（vitest 5 无该 reporter，`ERR_LOAD_URL`），属命令参数错误，已用默认 reporter 重跑。

## 四、未执行 / 跳过 / 阻塞（明确不记为通过）

| 项 | 原因 | 需要什么才能完成 |
|---|---|---|
| `make check`（全套） | 包含生成物检查、前端测试、运行时/迁移源码准入；其中迁移准入**已知失败** | 先修 PWR-004；再在锁定工具链执行 |
| `make test-integration`（完整） | 需 PostgreSQL 等真实依赖（`SP03_TEST_DATABASE_URL` 等），本机禁止启动 | 隔离环境 + 修 PWR-005 |
| `make test-e2e`、`test-replay`、`test-unit`（完整 `go test ./...`） | 需集群/夹具/较长时间；`test-unit` 会在无 env 时静默跳过全部 gated 测试，不能作为覆盖率证明 | 隔离环境 |
| `web/tests/e2e/**` | 需 `OPS_WEB_QA_INPUT`/`OPS_WEB_QA_CDP` 与真实浏览器/后端 | 合规环境；另见 PWR-017（缺环境会报错） |
| 服务（`services/investigator`）测试 | 本机缺 pytest | 项目锁定 Python 环境 |
| `helm template` / `helm lint` | 本机未确认安装 helm（未执行） | 安装 helm 或由 CI 执行 |
| 离线 Bundle 重建、干净重装、升级/回滚 | 需发行/离线授权与隔离环境 | 授权 + 环境 |
| 真实 PostgreSQL 迁移前滚/幂等重跑 | 无数据库 | 隔离环境 |
| 192.168.0.63 五来源联合验证、内网/外部模型、浏览器闭环 | **本轮未获授权、未访问** | 授权 + 凭据（密钥仅从远程受限文件读入） |
| 部署环境 18443/18643 的观察与差异报告 | 无凭据/授权 | 授权 + 环境 |
| 依赖漏洞公告核对 | 未做（避免仅凭依赖名称定性） | 针对已准入组件的官方公告核对 |
| `third_party/**`、`bundle/**`、`deploy/addons/**` 第三方实现逐行 | 明确排除 | 不在本轮范围 |

## 五、覆盖统计口径（三者分开，不合并）

| 口径 | 本轮实际 |
|---|---|
| 文件级盘点 | 12,314 个跟踪文件进入台账（含第三方/生成物登记） |
| 人工逐行阅读（非测试生产码） | 约 210 个文件、约 3.4 万行完整阅读；约 40 个文件部分阅读；其余为静态核对或仅登记 |
| 函数级 | **4,643** 个函数/组件/Hook/存储过程登记（Go 3,560 / Python 604 / SQL 37 / TS 302 / TSX 140）；约 900 个位于已逐行阅读区间。**TS/TSX 只统计文件级顶层声明**，组件内部嵌套函数与子组件未枚举，故该数字不是完整函数清单（见 [corrections.md](corrections.md) 第 4 条） |
| 静态核对 | 全仓库模块边界、依赖方向、生成物、准入锁、部署清单（未逐行） |
| 运行验证 | Go 11 个包 + `test/contract` + `test/security`；前端 23 个测试文件；4 个复现探针；2 个门禁命令（均失败，为有效证据）；Python 评分器 9 项 |

**覆盖率不代表代码无缺陷**；未完成部分见 [scope.md](scope.md) 与 [progress.md](progress.md)。

## 五之二、复核轮次（r2）追加执行的检查

本节记录"审查结论自身被复核"所用的证据；相应更正见 [corrections.md](corrections.md)。

| 检查 | 命令或读取对象 | 结果 |
|---|---|---|
| 台账工具 TSX 缺陷定位与修正 | 统计 `functions.csv` / `coverage.csv` 的语言分布，修正工具后重跑 | 修正前：tsx 文件 81 个、函数 0（缺陷确认）；修正后：tsx 函数 140（`web/src` 126），总条目 **4,503 → 4,643** |
| PWR-005 修法验证 | `go test ./test/integration -run '^TestAssistantCrashWorkerChild$' -skip '^TestAssistantCrashWorkerChild$' -count=1 -json` | 报告中 **skip 事件 0、run 事件 0**（辅助入口被整体排除，不再触发零 skip 门禁）；父测试 `assistant_process_recovery_test.go:133-138` 自行启动子进程，不受顶层 `-skip` 影响 |
| 生成服务器接口核对 | `api/oapi-codegen.yaml`；`gen/api/openapi.gen.go:7356,12669,16698,16984` | `ServerInterface` 与 `StrictServerInterface` 并存；`HandlerFromMux` 只接受非 strict 接口，`NewStrictHandler` 才启用 strict |
| GraphView 结论核对 | `GraphView.tsx:246-302,409-468`、`legend.tsx`、`layout.ts:5-10` | 确认 `:246-255` **已有** stale 告警（推翻原"不存在独立过期提示"）；确认 `edgeExpired` 依据 `validTo` 与 ttl，关系类别与过期状态可叠加 |
| httpapi 测试清点 | 列出 `internal/httpapi/*_test.go` | **16 个**测试文件，含 `operator_role_status_test.go`、`sp08_audit_verify_test.go`（推翻"管理面无单测"） |
| 迁移与运维约定核对 | `docs/runbooks/database-migrations.md:1-25`、`cmd/db-migrate/main.go:15-19`、`deploy/charts/ops-platform/values.yaml:38-40` | forward-only 已文档化；`-to 0` 帮助文本已说明；SP04 启用时 worker=`workerReplicas`（默认 2） |
| 部署探针范围核对 | `deploy/charts/ops-platform/templates/deployments.yaml:9,20`、`sp08.yaml:50-51` | web 在 `sp08.enabled=true` 时由 `sp08.yaml` 渲染且已有探针，需收窄 PWR-008 范围 |
| tenant 端点门禁核对 | `internal/httpapi/tenant_handlers.go:34-38` | 租户/角色端点均挂 `auth.RequireRole(auth.PlatformAdmin)`，PWR-030 缺可达证据 |

## 六、复现方式（供后续审查复用）

```sh
# 1) 基线
cd /Users/mssc/Documents/Code/ops/platform && git rev-parse HEAD && git status --porcelain

# 2) Go 相关包与门禁
go vet ./internal/... ./cmd/...
go test -count=1 ./internal/investigation ./internal/analytics ./internal/assistant \
  ./internal/resourcestore ./internal/incident ./internal/finding ./internal/httpapi ./internal/auth
go test -count=1 ./test/contract ./test/security
go test -count=1 -v ./docs/reviews/2026-10-15-project-review/_probes/

# 3) 两个已知失败的门禁
python3 scripts/prepare-migration-source.py --check                                   # 期望：失败(PWR-004)
go test ./test/integration -run '^TestAssistantCrashWorkerChild$' -count=1 -json > /tmp/r.jsonl \
  && python3 scripts/check-test-report.py /tmp/r.jsonl                                # 期望：失败(PWR-005)

# 4) Python / 前端
python3 -m unittest discover -s scripts/assistant-eval -p 'test_*.py'
cd web && ./node_modules/.bin/vitest run && ./node_modules/.bin/tsc --noEmit -p tsconfig.json

# 5) 台账（只读，本目录内独立嵌套模块）
cd docs/reviews/project-wide-2026-10-16/_tools/ledger
GOPROXY=off go run . /Users/mssc/Documents/Code/ops/platform \
  /Users/mssc/Documents/Code/ops/platform/docs/reviews/project-wide-2026-10-16
#    产物：本目录 coverage.csv / functions.csv（期望输出 files=12314 functions=4643）
```
