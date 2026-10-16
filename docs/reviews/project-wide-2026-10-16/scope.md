# 范围与文件分类（2026-10-16）

仓库共 **18,640** 个跟踪文件、**12,314** 个进入本次台账（含 `bundle/`、`third_party/`、`poc/`、`artifacts/` 的登记项）。逐文件的处理方式见 [coverage.csv](coverage.csv)，逐函数清单见 [functions.csv](functions.csv)。

## 一、按目录分类与处理方式

### 1. 自有维护源码（纳入人工逐行范围，按优先级分批）

| 目录 | 文件数 | 行数量级 | 处理方式 | 本轮结果 |
|---|---|---|---|---|
| `internal/`（除生成物） | 544 | ~95k | 逐文件阅读，高风险共享路径优先 | 共享边界包（investigation/analytics/assistant/resourcestore/resource/graph/incident/finding/rca/action/evidence/archive/inspection）非测试文件基本读完；httpapi/app 读完约 60%；integrations/bundle/upstream/supplychain 读完约 40%；其余按需检索 |
| `cmd/` | 31 | ~2.4k | 逐文件 | `opsctl` 17 个非测试、`platform-api/worker`、`command-runner`、`db-migrate` 读完；`*_test.go` 未逐行 |
| `web/` | 541 | TS/TSX ~29.6k | 逐文件静态阅读 | `web/src` 全部生产源码读完（静态）；`web/tests` 单元测试读完；`web/tests/e2e` 抽样；`web/src/api/generated/**` 属生成物，抽样比对 |
| `services/investigator/` | 37 | ~2.9k | 逐文件 | `src/investigator/*.py`（16 个）、`tests/*.py`（13 个）读完；`uv.lock` 仅头部+定向引用 |
| `test/` | 333 | ~40k | 断言有效性与门禁语义审查 | `test/contract` 关键文件读完；`test/integration` 27 个文件读完、其余按门禁分类；`test/security` 读完；`test/e2e`、`test/tools` 抽样 |
| `migrations/` | 43 | 2.5k | 逐文件 | 43 个全部读完 |
| `api/` | 138 | — | 契约核对 | `openapi/platform-v1.yaml` 读路径索引与关键 schema；`schemas/**` 抽样；`assistant-tools/**` 与 Go/Python 双侧比对 |
| `deploy/` | 43 | — | 逐文件 | charts/addons/profiles/network-policies 读完 |
| `scripts/` | 43 | — | 逐文件 | 准入/生成/校验脚本读完（`prepare-migration-source.py` 实测执行）；Python 采集脚本部分 |
| `policies/`、`recipes/` | 8 | — | 逐文件 | 2 个 rego + 4 个 recipe 读完 |
| `gen/` | 2 | — | 生成物 | 只核对生成来源与消费端 |
| `build/`、`poc/`、`artifacts/` | 138 | — | 登记 + 定向核对 | `artifacts/*.go` 遗留 main 包已登记为问题（PWR-015） |

### 2. 测试与夹具

- `*_test.go`（`internal/` 约 190 个、`test/` 约 130 个、`cmd/` 9 个）与 `web/tests/**`、`services/investigator/tests/**`、`test/fixtures/**`。
- 处理方式：**不因位于测试目录自动排除**。本轮核对断言有效性、`skip` 语义、mock 自证、fixture 与 live 的边界（见 [findings.md](findings.md) 的 PWR-005、PWR-017、PWR-031 与 [verification.md](verification.md)）。
- 未逐行读完的测试文件在台账中标记为"抽样/门禁分类"，**不记为通过**。

### 3. 生成代码与复制出的发行源码

| 生成物 | 生成来源 | 本轮处理 |
|---|---|---|
| `gen/api/openapi.gen.go`、`gen/internalgraph/openapi.gen.go` | `oapi-codegen`（`api/generate.go`） | 核对头部版本、消费方与漂移门禁 |
| `web/src/api/generated/**`（413 个 model + client） | Orval（`web/orval.config.ts`） | 抽样约 60 个 model + 关键 client 路径与后端/schema 比对 |
| `internal/persistence/dbgen/**` | sqlc（`sqlc.yaml`） | 核对 3 条查询与手写 SQL 的并存关系 |
| `services/investigator/src/investigator/output.schema.json`、`assistant-output.schema.json` | `scripts/generate-investigation-provider-schema.py` | 后者**未纳入** `check-generated`（PWR-013） |
| `deploy/addons/*/**-resolved.yaml` | 上游清单物化 | 仅作延期/边界核对，未逐行审计 |

**未经逐行阅读的生成产物未计入人工逐行覆盖率。**

### 4. 第三方源码、vendor、Bundle、锁与二进制

- `third_party/`（5,489 个文件）、`bundle/`（4,935 个）、`deploy/addons/victoria/charts/*.tgz`。
- 处理方式：审查**实际使用、依赖边界、配置、锁定、来源与准入证据**（`third_party/manifest.yaml`、`bundle/evidence/third_party/admission/*`、`bundle/licenses-policy.yaml`、`internal/bundle/verify.go`、`internal/supplychain/catalog.go`）。**不宣称完成第三方实现的逐行审计。**
- `bundle/evidence/**` 被 `bundle/catalog.go` 的 `//go:embed evidence` 编译进校验器，属**准入证据**；`artifacts/**` 主体被 `.gitignore` 忽略，属生成/本地物料，**不是准入证据**。

### 5. 历史报告、日志、截图与归档

- `docs/evidence/**`、`docs/reviews/**`、`artifacts/test-reports/**`：用于理解需求、旧问题与验证背景；**不把历史通过当成当前版本通过**，不重复计入源码阅读工作量。
- `docs/plans/v1.0-remaining-work-agent-handoff.md` 等未跟踪文件属其他任务，保留未动。

### 6. 已延期能力（单独列出，不计为当前缺陷）

| 能力 | 延期依据 | 当前应保持的边界 | 本轮核对 |
|---|---|---|---|
| KubeVirt / CDI 开发与运行验收 | [ADR-0008](../../adr/0008-defer-kubevirt-cdi-development.md)、`AGENTS.md` | core Profile 中 `kubevirt`/`cdi` 保持 `mode: disabled`、`compatibility: unverified`，`candidate` 不入 Bundle；不创建集群虚拟化 CRD/VM/VMI/DataVolume | 未发现误开启：`deploy/profiles/*.yaml` 为 `disabled`，`internal/bundle/importer.go` 拒绝核心启用、`internal/source/cluster.go` 拒绝虚拟化能力键、`internal/action/profile.go` 禁止 `virtctl`；**CLI 侧 `profile resolve` 门禁偏宽**（PWR-029） |
| Trace / PyRCA | [ADR-0040](../../adr/0040-deepflow-live-l4-l7-development.md)、[ADR-0020](../../adr/0020-sp05-pyrca-disabled-gain-decision.md) | 能力合同保持 `CAPABILITY_DISABLED`；不实现 | DeepFlow 适配器 Trace 恒 `CAPABILITY_DISABLED`（`internal/integrations/deepflow/adapter.go:38-40,79`），契约 `platform-v1.yaml` 中 `const: CAPABILITY_DISABLED` |
| 虚拟化相关测试 | `AGENTS.md` | `OPS_KUBEVIRT_ORBSTACK_POC` 未设置时跳过属正常 | `Makefile:67` 用 `-skip` 排除，不产生 skip 事件，不破坏零 skip 门禁 |

**边界失效、误开放或影响当前路径的问题仍会上报**（如 PWR-029）。

## 二、部署环境与源码的对应关系

| 环境 | 本轮可核对的程度 | 结论范围 |
|---|---|---|
| 本机（macOS arm64） | 源码、静态检查、单元/契约/安全测试、复现探针 | 仅源码级结论；**不代表运行实例** |
| `192.168.0.63`（远程独立开发环境，`/opt/ops-assistant-dev-20261012`） | **本轮未访问**（无授权/未提供凭据） | 长驻 API/Worker/模型/预览实例的版本、digest、来源资格、集群 UID、浏览器行为**全部待验证** |
| `18443` / `18643` core/final 环境 | **本轮未访问**，未修改 | 部署观察与差异报告**未完成** |

源码版本与运行实例版本的关系：`docs/plans/v1.0-remaining-work-agent-handoff.md` 明确指出"源码同步不代表运行实例已使用最新代码"，本轮沿用该判断，不作相反假设。

## 三、纳入与排除依据

- **纳入**：仓库内全部跟踪的自有源码、测试、夹具、迁移、契约、策略、脚本、构建与部署配置，以及第三方/生成物的**来源、锁定与消费关系**。
- **排除（并说明理由）**：第三方实现内部逐行；`bundle/component-catalog*.yaml`（各约 2.5MB 组件账本，仅抽样核对结构）；`artifacts/sp06-investigator/python-runtime-source.tar` 等大体积物料；`web/pnpm-lock.yaml` 正文（依赖锁定由 pnpm 政策测试覆盖）；`deploy/addons/kubevirt/*-resolved.yaml`（约 835KB 延期物料，仅边界核对）。
- 排除项**不计入覆盖率分母之外**，也不被声称为已审查。

## 四、台账字段说明（coverage.csv / functions.csv）

- `coverage.csv`：`path, lang, kind, module, lines, function_count, manual_review_status, note`。
  - `kind` ∈ {source, test, generated, config, third_party, doc}；`manual_review_status` 为**模块级**口径（如"非测试已逐行(测试部分)""生成物(不逐行)"），用于区分人工逐行、静态核对与仅登记。
- `functions.csv`：`path, name, kind, start_line, end_line, lang`。Go 函数/方法用 `go/ast` 提取（精确）；SQL `CREATE FUNCTION` 用正则；Python 用正则；**TS/TSX 用正则且只识别文件级顶层声明**（`function`、`const/let = (`、带类型标注的 `const X: T = (`），因此组件内部嵌套函数、内联 Hooks、子组件不会进入台账，`end_line` 取下一处声明的上一行，属**近似跨度**。例：930 行的 `web/src/pages/AssistantPage.tsx` 只登记 5 个顶层声明。**该台账是"文件级 + 顶层声明级"清单，不是完整函数清单。
- **修订记录（r2）**：首版工具把 `.tsx` 分类为 `tsx` 但调度只匹配 `ts`，导致 81 个 TSX 文件（`web/src` 58 个）`function_count` 全为 0、`functions.csv` 中 tsx 行数为 0；修正后条目由 4,503 增至 **4,643**（tsx 贡献 140）。详见 [corrections.md](corrections.md) 第 4 条。
- 生成工具为只读脚本，现存放于本目录 [_tools/ledger/](_tools/ledger/)（独立嵌套 Go 模块，不进入仓库根模块的 `go vet/test ./...`）；复现方式见 [verification.md](verification.md)。
