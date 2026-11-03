# 智能运维平台 1.0

`platform/` is the platform's standalone Go monorepo. The repository keeps the API and worker as separate processes over one Go module.

## 当前开发范围

**KubeVirt 与 CDI 的开发、部署和运行验收延期。本轮先完成 SP-02 非虚拟化部分。** 编码前必须阅读 [AGENTS.md](AGENTS.md) 和 [ADR-0008](docs/adr/0008-defer-kubevirt-cdi-development.md)。

Task 2.6、Task 2.8/2.9 的虚拟化分项，以及后续 VM 功能和 virtualization/full Profile 不在本轮范围内。core 中两组件保持 disabled，运行兼容性为 unverified；已有证据保留。只有用户明确恢复虚拟化阶段后才重新开发。

## 全平台功能追踪

[功能完成清单](docs/release/feature-matrix.md)关联 PRD 条目、专项功能、实际入口、实现、测试和证据，分别记录开发状态与运行验证状态，并列出已确认缺口和待核对项。该清单不代表整个平台或正式发行验收通过；远程性能问题按用户要求仅作记录。

## 实际代码产品设计

[智能运维平台技术方案 Word（V8.0 正式版）](docs/product/智能运维平台技术方案_正式版_V8.0_20261101.docx) 与 [PDF](docs/product/智能运维平台技术方案_正式版_V8.0_20261101.pdf)面向技术管理、架构设计与研发评审，说明 22 项主要开源组件的版本、集成方式及具体职责，展开资源图、观测检查、模型请求、工具调用、Agent 与 MCP、查询编译、故障链取证、正式 RCA、任务运行及人工处置恢复验证。以 Pod 重启和 PVC 供应失败串联实现过程，附部署条件与源码导航。共 23 页、21 章、12 张图、10 张表和 5 段代码示例。

[智能运维 AI 技术方案 Word（V7.0 Kubernetes 工程师版）](docs/product/智能运维AI技术方案_Kubernetes工程师版_V7.0_20261101.docx) 与 [PDF](docs/product/智能运维AI技术方案_Kubernetes工程师版_V7.0_20261101.pdf)面向熟悉 Kubernetes、需要从基础理解 AI 的开发与架构人员，从 HTTP 请求、函数参数和循环程序解释模型、提示词、上下文、工具调用、Agent 与 MCP；以 Pod 重启及问数示例连接实际代码，附任务持久化、组件部署与源码导航。共 15 页、14 章、7 张图、6 张表和 7 段代码示例。

[智能运维平台产品技术方案 Word（V6.1 技术逻辑修订版）](docs/product/智能运维平台产品技术方案_领导汇报版_V6.1_20261101.docx) 与 [PDF](docs/product/智能运维平台产品技术方案_领导汇报版_V6.1_20261101.pdf)保留领导汇报口径，按实际运行分流补齐问数、HolmesGPT 调查和零模型调用的指定故障链取证，展开查询编译、逐步检查、PVC 正式配方的具体条件、助手与正式 RCA 的判定边界，以及关联资源恢复验证。共 21 页、20 章、15 张技术图和 8 张对照表，以同一存储问题说明从任务到事实、从事实到判断和从处置到验证的技术逻辑。

[智能运维平台产品技术方案 Word（V6.0 领导汇报版）](docs/product/智能运维平台产品技术方案_领导汇报版_V6.0_20261101.docx) 与 [PDF](docs/product/智能运维平台产品技术方案_领导汇报版_V6.0_20261101.pdf)按领导汇报逻辑组织产品定位、技术结论、组件架构与建设价值，共 19 页、18 章、13 张技术图和 6 张对照表。明确观测、资源图、原生检查、HolmesGPT 与 MCP 调查、PostgreSQL 与 SeaweedFS、OPA 与 OpenBao、Command Runner 等组件的职责、集成方式和协作链路，以存储供应排障贯穿自然语言提问、取证、规则核验、人工处置与恢复验证。

[智能运维平台产品技术方案 Word（V5.0 技术详解版）](docs/product/智能运维平台产品技术方案_技术详解版_V5.0_20261101.docx) 与 [PDF](docs/product/智能运维平台产品技术方案_技术详解版_V5.0_20261101.pdf)面向领导汇报与技术方案交流，共 16 页、15 章、13 张技术图和 5 张表，系统说明数据接入、资源语义、自然语言任务、查询编译、多步调查、规则诊断、证据校验、持久运行、身份授权、人工处置与恢复验证，并以存储供应调查串联完整执行过程。

[智能运维平台核心技术详解两页彩页 PDF（V4.0）](docs/product/智能运维平台_核心技术详解_两页彩页_V4.0_20261101.pdf)保留领导汇报口径，展开资源身份与关系图、语义查询编译、多步工具调用、版本化诊断规则、证据独立性与引用校验、持久任务及恢复验证，说明各技术环节的输入、处理与输出。

[智能运维平台产品技术概览两页彩页 PDF（V3.0）](docs/product/智能运维平台_产品技术概览_两页彩页_V3.0_20261101.pdf)面向领导汇报，以宏观技术口径呈现产品定位、运维全流程、组织建设价值与核心技术架构，集中解释资源语义、规则与大模型协同、工具调用、证据校验及运行控制。

[智能运维核心技术逻辑两页彩页 PDF（V2.0）](docs/product/智能运维_核心技术逻辑_两页彩页_V2.0_20261101.pdf)从产品逻辑与工程机制解释自然语言任务绑定、Tool Calling 多步取证、规则与模型分工、语义查询及引用校验、持久任务和恢复验证，适合需要了解底层技术逻辑的客户交流。

[智能运维两页宣传彩页 PDF](docs/product/智能运维_两页宣传彩页_V1.0_20261101.pdf)以 A4 两页呈现六项技术优势、自然语言排障示例、技术分工与恢复验证，适合客户交流及双面打印。

[智能运维技术优势方案 Word（客户易读版）](docs/product/智能运维产品技术优势方案_客户易读版_V1.0_20261101.docx) 与 [PDF](docs/product/智能运维产品技术优势方案_客户易读版_V1.0_20261101.pdf)聚焦智能运维，以六项技术优势、自然语言交互和完整排障场景说明客户价值；共 10 页、9 个主题、6 张示意图，保留任务及来源开放条件，适合客户方案交流。

[对外产品方案 Word（V1.2）](docs/product/智能运维平台产品方案_对外版_V1.2_20261031.docx) 与 [PDF](docs/product/智能运维平台产品方案_对外版_V1.2_20261031.pdf)面向客户交流与项目方案确认，覆盖产品价值、功能体系、典型场景、数据接入、部署运行、安全审计、实施服务及验收评价；第 5、10、15 章分别展开智能运维闭环、自然语言交互和技术实现，明确当前能力、配置条件、设计目标与延期范围。

[内部评审参考 Word（V1.0）](docs/product/智能运维平台产品方案_V1.0_20261030.docx) 与 [PDF](docs/product/智能运维平台产品方案_V1.0_20261030.pdf)保留内部产品评审及研发交付沟通材料，对外交流使用 V1.2。

[详细产品设计方案（2026-10-30 代码基线）](docs/product/product-design-code-baseline-20261030.md)说明当前产品定位、页面与交互、角色权限、数据口径、故障调查与人工处置闭环，并列出源码索引、验收场景和待补齐项。文档包含当时的工作区修改，不替代根目录正式方案，也不代表运行验收通过。

## Development toolchain

Exact development versions are pinned in `.tool-versions`:

- Go 1.27.1
- Node.js 24.21.0
- pnpm 12.7.0
- Python 3.12.14
- uv 0.12.17

Install these versions with an `.tool-versions` compatible manager, then run `make bootstrap` to install when a compatible manager is available and verify every version.

## Process configuration

Both process skeletons require these environment variables before startup:

- `DATABASE_URL`
- `OIDC_ISSUER_URL`
- `PLATFORM_PROFILE` (path to the selected deployment profile)

Missing values return a structured configuration error before either process starts.

## Architecture decisions

The accepted architecture decisions are recorded in [docs/adr](docs/adr/):

- [ADR-0001: Modular Monolith](docs/adr/0001-modular-monolith.md) — one Go module, separate API and worker processes, internal Incident module.
- [ADR-0002: Two-Role Authorization Model](docs/adr/0002-two-role-model.md) — `operator` and `platform_admin`, with no role inheritance.
- [ADR-0003: Operator-Entered Command Remediation](docs/adr/0003-manual-command-remediation.md) — operators enter and confirm the actual command; isolated execution is policy-gated.
- [ADR-0004: SeaweedFS Evidence Archive](docs/adr/0004-seaweedfs-object-storage.md) — SeaweedFS is the default bundled S3-compatible archive.
- [ADR-0005: Fully Offline Bundle](docs/adr/0005-airgap-bundle.md) — installation artifacts are digest-locked and verified offline.
- [ADR-0006: DeepFlow as a Profile-Selected Add-on](docs/adr/0006-deepflow-boundary.md) — DeepFlow stays behind an adapter and its live capability remains PoC-gated.
- [ADR-0007: OpenAPI as the Public API Source of Truth](docs/adr/0007-openapi-source-of-truth.md) — OpenAPI 3.1 is the source for generated public API bindings.
- [ADR-0008: Defer KubeVirt/CDI development and acceptance](docs/adr/0008-defer-kubevirt-cdi-development.md) — KubeVirt/CDI development and acceptance are deferred; the current stage covers non-virtualization work.
- [ADR-0009: Exact upstream Bundle versions](docs/adr/0009-exact-upstream-bundle-versions.md) — preserve exact two-segment upstream releases in Bundle material locks.

- [ADR-0010: Core image license admission](docs/adr/0010-core-image-license-admission.md) — exact native/Go/JVM notice and corresponding-source obligations for the reviewed arm64 distributions.
- [ADR-0011: Native dependency source provenance](docs/adr/0011-native-dependency-source-provenance.md) — publisher archive checksums and paired source materials.
- [ADR-0012: Preserve upstream multi-platform digests](docs/adr/0012-preserve-upstream-multiplatform-digests.md) — authenticate original indices and the complete selected platform closure.
- [ADR-0013: Licensed K8sGPT CLI baseline](docs/adr/0013-lock-licensed-k8sgpt-cli.md) — freeze the reviewed upstream no-LLM CLI and preserve rejected license evidence.

## Commands

- `make bootstrap` installs and verifies the pinned toolchain.
- `make generate` runs available Go generators.
- `make test-local` runs local Go unit/Contract/security, web component/SSE/TypeScript, and Python checks without live services, browser automation, replay or capacity measurements.
- `make check-local` adds `go vet`. `make check` also checks generated output and locked runtime source materials; it is not live acceptance.
- `make fmt` formats Go sources.
- `make test-unit`, `make test-contract`, `make test-web`, and `make test-python` run independently. Python uses the locked offline dev environment; install the pinned dependencies first.
- `make test-integration`, `make test-live-sources`, `make test-native`, `make test-browser-runtime`, `make test-web-e2e`, `make test-capacity`, `make test-capacity-live`, `make test-replay`, and `make test-e2e` are explicit environment/load entry points. Acceptance reports reject skips and empty suites; missing inputs do not become a pass.
- See [development test entry points](docs/runbooks/development-tests.md) for suite selection, prerequisites and report paths. KubeVirt/CDI remain deferred.
- `make check-delivery` checks the [unified delivery preparation](docs/release/unified-delivery.md) baseline, generated output and existing runtime/migration source materials. This prepares a reviewable version set; it does not build/sign a new Bundle, deploy or grant release admission.
- [Shared post-check and execution links](docs/release/disposition-verification-closeout.md) records the frozen verification report and manually confirmed follow-up/rollback association; concrete resource recipes and runtime acceptance remain separately scoped.
- `make verify-bundle BUNDLE_DIR=/absolute/bundle TRUST_KEY=/outside/bundle/trust.pem` verifies a signed local Bundle against an independently trusted key.
- `make bundle-dev-arm64 BUNDLE_SPEC=/absolute/build.json BUNDLE_DIR=/absolute/new-bundle SIGNING_KEY=/outside/repository/signing.pem` packages already prepared, qualified local inputs.
- Core import/install commands, prerequisites and actual acceptance evidence are in [the offline install runbook](docs/runbooks/dev-offline-install.md). Component admission and code checks alone do not establish a live install/reinstall pass.
