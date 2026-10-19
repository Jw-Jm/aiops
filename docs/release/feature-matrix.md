# 全平台功能完成清单

日期：2026-10-19。基线：`b10b2f0bc378f06291dbfc04ddccaae3a555155d` 加当前工作区改动。

本清单按真实需求编号关联产品入口、实现、测试、UI、运行 Profile 与证据。它是后续开发输入，不是整个平台功能、运行验收或发行通过声明。

覆盖 PRD 中 **144 个**正式条目（FR/UI/INT/DATA/SEC/NFR/OPS/ACC），另列 **12 个**专项及平台工程条目，分属 **39 个**技术责任模块。PRD 的编号不是连续整数，不虚构 FR-004 等不存在的条目。

## 阅读方式与结论边界

- 开发状态与运行状态分别记录。“主机制已实现”只说明已找到可达入口和相关机制，不代表逐条验收条件全部满足。
- 清单以需求级静态追踪为基础，核心流程另有本地回归：见补齐记录。未逐行审计全部实现，未运行全量业务场景。测试映射不等于执行或完整覆盖。
- 历史回执绑定历史源码/范围，不自动继承到本工作区。当前完整真实运行未知时保持未验证，不从源码推导 PASS。
- 混合条目中的非虚拟化功能与延期 VM 子项分别解释；生产 HA/灾备、KubeVirt/CDI、网络 Trace 补全不自动恢复。
- 用户已明确远程机器性能问题仅记录。本次无远程访问、部署、容器、集群或浏览器运行；没有性能调优。
- FR-141/ACC-012 仍按 [0003-manual-command-remediation.md](/Users/mssc/Documents/Code/ops/platform/docs/adr/0003-manual-command-remediation.md)：操作者本人输入与风险确认，必要时 step-up，不引入第二人审批。

权威数据为 [feature-matrix.json](/Users/mssc/Documents/Code/ops/platform/docs/release/feature-matrix.json)；结构核对回执为 [feature-matrix.verification.json](/Users/mssc/Documents/Code/ops/platform/docs/release/feature-matrix.verification.json)。JSON 中模块承担技术责任归属，不虚构人员负责人。

## 差距与处理记录

最初登记 6 项缺口。第二项已完成 G-001/002/003/005 的功能开发，并按 G-004 的原定选项明确不支持范围；FR-144 全目标自动检查仍为部分实现。G-006 的本地/真实检查入口拆分现已完成并通过 check-local；C-002 的共用关联已补齐，C-001/003 仍待核对。具体开发和实际测试边界见核心流程补齐记录。

### G-001 · P0 · 补齐非虚拟化总览计数口径

对应：FR-001。

增加 active Finding、Open/Critical Incident、运行中 Investigation 的授权统计；硬件总数仅用合格硬件来源，未知不能补零；复用现有 overview 合同扩展。

**完成条件：** API 与详情筛选口径一致；无跨租户/范围泄露；unknown/degraded 组件回归。

源码依据：[overview.go](/Users/mssc/Documents/Code/ops/platform/internal/httpapi/overview.go)、[OverviewPage.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/pages/OverviewPage.tsx)。

**当前处理：** 增加活动告警、未确认/严重故障与我的运行中调查；当前授权过滤覆盖所有关联 Finding。硬件未知不补零。 [补齐与验证记录](/Users/mssc/Documents/Code/ops/platform/docs/release/core-workflow-closeout.md)。

### G-002 · P0 · 健康分布按领域分组并联动资源列表

对应：FR-002。

复用已知资源分类提供 Hardware/Kubernetes/Network/Storage 的可用/未知分布与授权列表链接；KubeVirt 维持延期。

**完成条件：** 分组口径可解释，点击携带范围和健康筛选；未知来源不显示为健康。

源码依据：[OverviewPage.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/pages/OverviewPage.tsx)、[overview.go](/Users/mssc/Documents/Code/ops/platform/internal/httpapi/overview.go)。

**当前处理：** 按请求启用领域汇总，领域/健康链接与列表筛选同源；查询摘要绑定签名游标，旧请求维持返回形状。 [补齐与验证记录](/Users/mssc/Documents/Code/ops/platform/docs/release/core-workflow-closeout.md)。

### G-003 · P0 · 实现首页重点故障列表

对应：FR-003。

复用 Incident 查询展示高严重度待处理故障、资源、状态/开始/时长/RCA/影响摘要及详情入口；有界数量和稳定排序。

**完成条件：** 首页出现真实查询结果、空/错误/权限态正确；不会把记录总数误作 Open 数量。

源码依据：[OverviewPage.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/pages/OverviewPage.tsx)、[sp05_list.go](/Users/mssc/Documents/Code/ops/platform/internal/httpapi/sp05_list.go)。

**当前处理：** 有界重点故障、稳定排序、可信时间与可授权冻结 RCA 影响摘要已接入首页。 [补齐与验证记录](/Users/mssc/Documents/Code/ops/platform/docs/release/core-workflow-closeout.md)。

### G-004 · P0 · 明确并补齐现有执行范围的 Post-check 判据

对应：FR-144。

先列执行 Profile×目标×可信后检查矩阵；复用现有 recipe/source adapter 补适用判据，或显式披露不支持范围，不以退出码或模型判断代替。

**完成条件：** 支持目标验证原异常恢复/新异常及最新 Evidence；不支持者保留 inconclusive；不扩大 BMC 写权限。

源码依据：[sp07_postcheck.go](/Users/mssc/Documents/Code/ops/platform/internal/app/sp07_postcheck.go)、[postcheck.go](/Users/mssc/Documents/Code/ops/platform/internal/action/postcheck.go)。

**当前处理：** 已增加原生场景及相关 Warning 验证；真实网络连通、应用读写和未覆盖目标仍明确不支持，不宣称全目标 Post-check 完成。 [补齐与验证记录](/Users/mssc/Documents/Code/ops/platform/docs/release/core-workflow-closeout.md)。

### G-005 · P1 · 补足平台自身业务运行指标

对应：OPS-001。

在实际主路径接入积压、Incident 聚合时延、调查队列/耗时、Evidence 时延与数据库可达状态等缺项；复用私有 metrics registry，固定低基数标签。

**完成条件：** 指标来自真实事件/当前状态而非占位；本地记录/导出测试覆盖，不展开远程性能调优。

源码依据：[metrics.go](/Users/mssc/Documents/Code/ops/platform/internal/observability/metrics.go)、[runtime_observe.go](/Users/mssc/Documents/Code/ops/platform/internal/app/runtime_observe.go)。

**当前处理：** 实际主路径时长与有界 Worker 业务快照已接入；固定标签、失效 NaN、采样时间/可用性显式提供。 [补齐与验证记录](/Users/mssc/Documents/Code/ops/platform/docs/release/core-workflow-closeout.md)。

### G-006 · P1 · 拆分本地功能检查与真实环境门禁

对应：DEV-003。

本地入口只运行无真实环境依赖的单元/合同/前端/Python；真实集成保留独立 fail-closed 入口，不能改为静默 skip。

**完成条件：** 干净开发环境可执行本地检查；缺真实环境的集成明确失败；原 skip/report 门禁不放宽。

源码依据：[Makefile](/Users/mssc/Documents/Code/ops/platform/Makefile)、[verification_targets_test.go](/Users/mssc/Documents/Code/ops/platform/test/contract/verification_targets_test.go)。

**当前处理：** 开发检查入口已拆分并执行 make check-local 通过；真实来源、浏览器、原生工具、回放与容量独立运行，缺环境/跳过不作为 PASS。 [补齐与验证记录](/Users/mssc/Documents/Code/ops/platform/docs/runbooks/development-tests.md)。

## 语义核对项目与处理记录

### C-001 · BMC 信息是否满足独立资源要求

对应：FR-012。对照 FR-012 的 BMC 资源和 Server 管理信息表达，确认是否确缺独立投影；不因无实机先新增硬件框架。

依据：[inventory.go](/Users/mssc/Documents/Code/ops/platform/internal/integrations/redfish/inventory.go)。

### C-002 · 回退结果与原操作关联

对应：FR-145。已实现本人确认绑定的原执行关联和明确回退配置目标，冻结报告比较配置摘要与场景健康；没有自动回退。

依据：[action-plan-v2.schema.json](/Users/mssc/Documents/Code/ops/platform/api/schemas/action-plan-v2.schema.json)、[domain.go](/Users/mssc/Documents/Code/ops/platform/internal/action/domain.go)、[plan.go](/Users/mssc/Documents/Code/ops/platform/internal/action/plan.go)。

### C-003 · 关键告警与 Runbook 的覆盖关系

对应：OPS-004。逐项关联已有关键告警/指标与现有 Runbook，缺绑定才补；不重写整套运维文档。

依据：[sp03-foundation-runtime.md](/Users/mssc/Documents/Code/ops/platform/docs/runbooks/sp03-foundation-runtime.md)、[metrics.go](/Users/mssc/Documents/Code/ops/platform/internal/observability/metrics.go)。

## 运行受限与独立验证记录

- **五来源/页面/新同轮长上下文**：联合 FAIL，页面/新同轮长上下文 NOT_RUN；局部采集/oracle PASS 不替代整体验收；[acceptance-status.json](/Users/mssc/Documents/Code/ops/platform/docs/evidence/five-source-reverify-20261018/round5/acceptance-status.json)。
- **模型中文质量/长上下文**：旧内容重算 5/6，租户/窗口不一致，人工 pending；独立容量不作为质量结论；[prior-retention-reassessment.json](/Users/mssc/Documents/Code/ops/platform/docs/evidence/five-source-reverify-20261018/round5/prior-retention-reassessment.json)。
- **源码修复与历史限定运行**：已修复项按当前源码解释；不复制旧 requirements.md 为当前缺陷；[fix-status.md](/Users/mssc/Documents/Code/ops/platform/docs/reviews/project-wide-2026-10-16/fix-status.md)。
- **Node/PVC 实时助手任务**：实现存在；cluster-scoped 会话授权和专属 PVC 未具备，NOT_RUN，不扩权；[acceptance-status.json](/Users/mssc/Documents/Code/ops/platform/docs/evidence/five-source-reverify-20261018/round5/acceptance-status.json)。

这些项目不自动进入功能开发缺口清单。远程超时根因未证明的，继续注明未知，不能全部归因为机器性能。

## 已实现或延期项的逐条追踪

以下每一条都继承所属模块的入口、实现、测试、UI、Profile 和证据；备注给出该需求的具体差异。未逐项重跑历史回执；本轮实际本地回归范围见 [core-workflow-closeout.md](/Users/mssc/Documents/Code/ops/platform/docs/release/core-workflow-closeout.md)。

### 运行总览 · `overview`

**入口：** GET /api/v1/overview；页面 /overview。

**实现：** [overview.go](/Users/mssc/Documents/Code/ops/platform/internal/httpapi/overview.go)、[OverviewPage.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/pages/OverviewPage.tsx)、[overview_records.go](/Users/mssc/Documents/Code/ops/platform/internal/httpapi/overview_records.go)、[list_filters.go](/Users/mssc/Documents/Code/ops/platform/internal/graph/list_filters.go)、[ariadne_adapter.go](/Users/mssc/Documents/Code/ops/platform/internal/graph/ariadne_adapter.go)。

**测试入口：** [overview_resources.test.tsx](/Users/mssc/Documents/Code/ops/platform/web/tests/overview_resources.test.tsx)、[overview_test.go](/Users/mssc/Documents/Code/ops/platform/internal/httpapi/overview_test.go)、[overview_records_test.go](/Users/mssc/Documents/Code/ops/platform/test/integration/overview_records_test.go)、[list_filters_test.go](/Users/mssc/Documents/Code/ops/platform/internal/graph/list_filters_test.go)。

**UI：** [OverviewPage.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/pages/OverviewPage.tsx)。

**Profile：** 非虚拟化 core；来源/模型/执行能力按配置与资格启用。

**证据或边界：** [core-workflow-closeout.md](/Users/mssc/Documents/Code/ops/platform/docs/release/core-workflow-closeout.md)。

- **FR-001 运行总览**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:420)。当前授权单集群；活动 Finding、open/critical Incident、当前主体运行中 Investigation 分项已接入。硬件数来自已观测 PhysicalServer，缺观测为未知；VM 分项延期。 对应 G-001。
- **FR-002 健康分布**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:447)。Hardware/Kubernetes/Network/Storage 领域健康汇总及过滤链接已接入；按授权后同一资源集合分页前统计。网络资源健康不代表链路质量；未观测领域为未知，KubeVirt 延期。 对应 G-002。
- **FR-003 重点故障展示**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:465)。首页显示最多五条当前可见 open critical/warning Incident，严重度/开始时间/ID 排序；真实时间缺失不推算，RCA 影响仅披露当前仍可授权的冻结结果。 对应 G-003。

### 资源查询与详情 · `resources`

**入口：** GET /api/v1/resources；GET /api/v1/resources/by-canonical-id。

**实现：** [resource_handlers.go](/Users/mssc/Documents/Code/ops/platform/internal/httpapi/resource_handlers.go)、[list_filters.go](/Users/mssc/Documents/Code/ops/platform/internal/graph/list_filters.go)、[informer.go](/Users/mssc/Documents/Code/ops/platform/internal/integrations/kubernetes/informer.go)。

**测试入口：** [list_filters_test.go](/Users/mssc/Documents/Code/ops/platform/internal/graph/list_filters_test.go)、[sp04_runtime_test.go](/Users/mssc/Documents/Code/ops/platform/test/integration/sp04_runtime_test.go)、[overview_resources.test.tsx](/Users/mssc/Documents/Code/ops/platform/web/tests/overview_resources.test.tsx)。

**UI：** [ResourcesPage.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/pages/ResourcesPage.tsx)、[ResourceDetailPage.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/pages/ResourceDetailPage.tsx)。

**Profile：** 非虚拟化 core；来源/模型/执行能力按配置与资格启用。

**证据或边界：** [current-native-business-status-r250.json](/Users/mssc/Documents/Code/ops/platform/docs/evidence/pre-sp07-20261008/current-native-business-status-r250.json)。

- **FR-010 统一资源查询**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:484)。原生 GVR 与授权分页主路径存在；容器、事件、资源详情采用安全投影。
- **FR-011 资源详情**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:506)。原生 GVR 与授权分页主路径存在；容器、事件、资源详情采用安全投影。
- **FR-013 Kubernetes 资源支持**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:553)。原生 GVR 与授权分页主路径存在；容器、事件、资源详情采用安全投影。

### 硬件采集与诊断 · `hardware`

**入口：** SP04 Redfish 采集；SP05 硬件 Inspection；资源详情。

**实现：** [inventory.go](/Users/mssc/Documents/Code/ops/platform/internal/integrations/redfish/inventory.go)、[sp04_hardware.go](/Users/mssc/Documents/Code/ops/platform/internal/app/sp04_hardware.go)、[npd_adapter.go](/Users/mssc/Documents/Code/ops/platform/internal/inspection/hardware/npd_adapter.go)、[node_hardware.go](/Users/mssc/Documents/Code/ops/platform/internal/rca/node_hardware.go)。

**测试入口：** [redfish_test.go](/Users/mssc/Documents/Code/ops/platform/internal/integrations/redfish/redfish_test.go)、[sp05_diagnostics_test.go](/Users/mssc/Documents/Code/ops/platform/internal/integrations/redfish/sp05_diagnostics_test.go)、[node_hardware_test.go](/Users/mssc/Documents/Code/ops/platform/internal/rca/node_hardware_test.go)。

**UI：** [ResourceDetailPage.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/pages/ResourceDetailPage.tsx)。

**Profile：** 非虚拟化 core；来源/模型/执行能力按配置与资格启用。

**证据或边界：** [sp05-dimm-failure.md](/Users/mssc/Documents/Code/ops/platform/docs/runbooks/sp05-dimm-failure.md)。

- **FR-012 物理硬件资源支持**（P0）：待核对；环境/来源/授权受限（仅记录）。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:524)。已见 PhysicalServer/CPU/DIMM/NIC/Disk/PSU/Fan 投影，未见独立 BMC 实体投影；需确认 BMC 信息展示是否满足正式条目。真实 BMC 未验另作环境项。 对应 C-001。
- **FR-033 硬件异常识别**（P0）：主机制已实现；环境/来源/授权受限（仅记录）。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:729)。原生 Adapter、硬件投影与 DIMM/CPER 判据存在；真实 BMC 未验证，不能将协议 Fixture 视为实机验收。
- **INT-006 Redfish/IPMI**（未单列）：主机制已实现；环境/来源/授权受限（仅记录）。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1737)。原生 Adapter、硬件投影与 DIMM/CPER 判据存在；真实 BMC 未验证，不能将协议 Fixture 视为实机验收。
- **ACC-003 DIMM 故障**（未单列）：主机制已实现；环境/来源/授权受限（仅记录）。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:2228)。原生 Adapter、硬件投影与 DIMM/CPER 判据存在；真实 BMC 未验证，不能将协议 Fixture 视为实机验收。

### KubeVirt/CDI/VM 延期 · `virtualization`

**入口：** 能力报告 disabled；不开放 VM 生命周期。

**实现：** [profile.go](/Users/mssc/Documents/Code/ops/platform/internal/app/profile.go)、[kubernetes-containerd.yaml](/Users/mssc/Documents/Code/ops/platform/deploy/profiles/kubernetes-containerd.yaml)。

**测试入口：** [profile_test.go](/Users/mssc/Documents/Code/ops/platform/internal/app/profile_test.go)、[upstream_graph_nonvirtual_test.go](/Users/mssc/Documents/Code/ops/platform/test/contract/upstream_graph_nonvirtual_test.go)。

**UI：** [CapabilitiesPage.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/pages/CapabilitiesPage.tsx)。

**Profile：** KubeVirt/CDI disabled、candidate、unverified。

**证据或边界：** [0008-defer-kubevirt-cdi-development.md](/Users/mssc/Documents/Code/ops/platform/docs/adr/0008-defer-kubevirt-cdi-development.md)。

- **FR-014 KubeVirt 资源支持**（P0）：延期/禁用/后续增强；不运行/禁用。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:577)。既有源码/Fixture/物料保留；不恢复开发、部署或验收。
- **FR-032 KubeVirt 异常识别**（P0）：延期/禁用/后续增强；不运行/禁用。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:712)。既有源码/Fixture/物料保留；不恢复开发、部署或验收。
- **INT-005 KubeVirt API**（未单列）：延期/禁用/后续增强；不运行/禁用。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1731)。既有源码/Fixture/物料保留；不恢复开发、部署或验收。
- **ACC-001 VM 启动失败**（未单列）：延期/禁用/后续增强；不运行/禁用。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:2193)。既有源码/Fixture/物料保留；不恢复开发、部署或验收。
- **ACC-002 VM 网络异常**（未单列）：延期/禁用/后续增强；不运行/禁用。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:2207)。既有源码/Fixture/物料保留；不恢复开发、部署或验收。

### 资源身份与生命周期 · `identity`

**入口：** 采集 → resolver → resource_entities；资源详情。

**实现：** [resolver.go](/Users/mssc/Documents/Code/ops/platform/internal/resource/resolver.go)、[repository.go](/Users/mssc/Documents/Code/ops/platform/internal/resourcestore/repository.go)、[reconcile.go](/Users/mssc/Documents/Code/ops/platform/internal/resourcestore/reconcile.go)。

**测试入口：** [resolver_test.go](/Users/mssc/Documents/Code/ops/platform/internal/resource/resolver_test.go)、[sync_snapshot_test.go](/Users/mssc/Documents/Code/ops/platform/internal/resourcestore/sync_snapshot_test.go)、[reconcile_test.go](/Users/mssc/Documents/Code/ops/platform/internal/resourcestore/reconcile_test.go)、[sp04_relist_replacement_test.go](/Users/mssc/Documents/Code/ops/platform/test/integration/sp04_relist_replacement_test.go)。

**UI：** [ResourceDetailPage.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/pages/ResourceDetailPage.tsx)。

**Profile：** 非虚拟化 core；来源/模型/执行能力按配置与资格启用。

**证据或边界：** [p2-followup-20261017.md](/Users/mssc/Documents/Code/ops/platform/docs/reviews/project-wide-2026-10-16/p2-followup-20261017.md)。

- **FR-020 统一资源身份**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:601)。重列替换、过滤不等于删除及冷启动注销已有当前回归；不沿用旧 PWR-002/006 未修复状态。
- **FR-021 身份匹配状态**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:609)。重列替换、过滤不等于删除及冷启动注销已有当前回归；不沿用旧 PWR-002/006 未修复状态。

### 原生关系与溯源 · `relations`

**入口：** GET /api/v1/resources/neighbors。

**实现：** [projector.go](/Users/mssc/Documents/Code/ops/platform/internal/integrations/kubernetes/projector.go)、[ontology_bridge.go](/Users/mssc/Documents/Code/ops/platform/internal/graph/ontology_bridge.go)、[ariadne_adapter.go](/Users/mssc/Documents/Code/ops/platform/internal/graph/ariadne_adapter.go)。

**测试入口：** [upstream_graph_nonvirtual_test.go](/Users/mssc/Documents/Code/ops/platform/test/contract/upstream_graph_nonvirtual_test.go)、[upstream_conformance_test.go](/Users/mssc/Documents/Code/ops/platform/internal/graph/upstream_conformance_test.go)。

**UI：** [GraphView.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/features/graph/GraphView.tsx)。

**Profile：** 非虚拟化 core；来源/模型/执行能力按配置与资格启用。

**证据或边界：** [sp04-resource-evidence-runtime.md](/Users/mssc/Documents/Code/ops/platform/docs/runbooks/sp04-resource-evidence-runtime.md)。

- **FR-022 基础资源关系**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:629)。非虚拟化原生关系主路径；VM 关系分项延期。
- **FR-024 关系溯源**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:658)。非虚拟化原生关系主路径；VM 关系分项延期。

### DeepFlow 动态通信与 L4/L7 · `network`

**入口：** query_deepflow；analytics network datasets；/assistant。

**实现：** [live.go](/Users/mssc/Documents/Code/ops/platform/internal/integrations/deepflow/live.go)、[adapter.go](/Users/mssc/Documents/Code/ops/platform/internal/integrations/deepflow/adapter.go)、[summary.go](/Users/mssc/Documents/Code/ops/platform/internal/integrations/deepflow/summary.go)、[network.go](/Users/mssc/Documents/Code/ops/platform/internal/analytics/network.go)。

**测试入口：** [live_test.go](/Users/mssc/Documents/Code/ops/platform/internal/integrations/deepflow/live_test.go)、[assistant_deepflow_live_test.go](/Users/mssc/Documents/Code/ops/platform/test/integration/assistant_deepflow_live_test.go)、[assistant_window_proof_test.go](/Users/mssc/Documents/Code/ops/platform/test/integration/assistant_window_proof_test.go)。

**UI：** [FaultChainPanel.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/features/assistant/FaultChainPanel.tsx)。

**Profile：** 非虚拟化 core；来源/模型/执行能力按配置与资格启用。

**证据或边界：** [acceptance-status.json](/Users/mssc/Documents/Code/ops/platform/docs/evidence/five-source-reverify-20261018/round5/acceptance-status.json)。

- **FR-023 动态通信关系**（未单列）：主机制已实现；环境/来源/授权受限（仅记录）。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:650)。UID/容器/来源/半开窗与独立 oracle 门禁存在；当前联合 Graph 就绪失败，时间仍 unreliable。
- **INT-003 DeepFlow**（未单列）：主机制已实现；环境/来源/授权受限（仅记录）。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1719)。UID/容器/来源/半开窗与独立 oracle 门禁存在；当前联合 Graph 就绪失败，时间仍 unreliable。
- **BOUND-001 Trace 补全保持禁用**（专项/交付）：延期/禁用/后续增强；不运行/禁用。[需求依据](/Users/mssc/Documents/Code/ops/platform/docs/adr/0040-deepflow-live-l4-l7-development.md:1)。Trace completion capability_disabled；平台自身 observability tracing 与网络 Trace 补全是不同能力。
  专项实现：[adapter.go](/Users/mssc/Documents/Code/ops/platform/internal/integrations/deepflow/adapter.go)、[capabilities.go](/Users/mssc/Documents/Code/ops/platform/internal/integrations/deepflow/capabilities.go)；专项测试：[live_test.go](/Users/mssc/Documents/Code/ops/platform/internal/integrations/deepflow/live_test.go)。

### 健康与 Kubernetes 异常 · `health`

**入口：** SP05 Inspection；资源/Finding/Incident 页面。

**实现：** [health.go](/Users/mssc/Documents/Code/ops/platform/internal/graph/health.go)、[source_health.go](/Users/mssc/Documents/Code/ops/platform/internal/graph/source_health.go)、[official.go](/Users/mssc/Documents/Code/ops/platform/internal/inspection/kubernetes/official.go)、[sp05_worker.go](/Users/mssc/Documents/Code/ops/platform/internal/app/sp05_worker.go)。

**测试入口：** [inspection_test.go](/Users/mssc/Documents/Code/ops/platform/internal/inspection/kubernetes/inspection_test.go)、[sp05_official_observations_test.go](/Users/mssc/Documents/Code/ops/platform/test/integration/sp05_official_observations_test.go)。

**UI：** [ResourceDetailPage.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/pages/ResourceDetailPage.tsx)、[CapabilityStatus.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/components/CapabilityStatus.tsx)。

**Profile：** 非虚拟化 core；来源/模型/执行能力按配置与资格启用。

**证据或边界：** [sp05-node-failure.md](/Users/mssc/Documents/Code/ops/platform/docs/runbooks/sp05-node-failure.md)。

- **FR-030 资源健康状态**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:674)。确定性异常判据、健康与缺失/退化区分存在；不自动启用虚拟化。
- **FR-031 Kubernetes 异常识别**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:693)。确定性异常判据、健康与缺失/退化区分存在；不自动启用虚拟化。
- **FR-034 数据不完整标识**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:746)。确定性异常判据、健康与缺失/退化区分存在；不自动启用虚拟化。

### Finding 接入与生命周期 · `findings`

**入口：** POST /api/v1/findings:ingest；GET /api/v1/findings；GET /api/v1/findings/{findingId}。

**实现：** [ingest.go](/Users/mssc/Documents/Code/ops/platform/internal/finding/ingest.go)、[domain.go](/Users/mssc/Documents/Code/ops/platform/internal/finding/domain.go)、[sp05_list.go](/Users/mssc/Documents/Code/ops/platform/internal/httpapi/sp05_list.go)。

**测试入口：** [ingest_test.go](/Users/mssc/Documents/Code/ops/platform/internal/finding/ingest_test.go)、[sp05_finding_test.go](/Users/mssc/Documents/Code/ops/platform/test/integration/sp05_finding_test.go)、[sp05_order_audit_test.go](/Users/mssc/Documents/Code/ops/platform/test/integration/sp05_order_audit_test.go)。

**UI：** [FindingsPage.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/pages/FindingsPage.tsx)。

**Profile：** 非虚拟化 core；来源/模型/执行能力按配置与资格启用。

**证据或边界：** [fix-status.md](/Users/mssc/Documents/Code/ops/platform/docs/reviews/project-wide-2026-10-16/fix-status.md)。

- **FR-040 Finding 标准化**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:760)。幂等、occurrence、迟到/乱序及身份冲突信封修复存在；历史集成证据有明确范围。
- **FR-041 Finding 生命周期**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:780)。幂等、occurrence、迟到/乱序及身份冲突信封修复存在；历史集成证据有明确范围。
- **FR-042 Finding 去重**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:794)。幂等、occurrence、迟到/乱序及身份冲突信封修复存在；历史集成证据有明确范围。
- **FR-043 Finding 再发生**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:802)。幂等、occurrence、迟到/乱序及身份冲突信封修复存在；历史集成证据有明确范围。
- **FR-044 乱序与迟到处理**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:810)。幂等、occurrence、迟到/乱序及身份冲突信封修复存在；历史集成证据有明确范围。
- **FR-045 Finding 详情**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:825)。幂等、occurrence、迟到/乱序及身份冲突信封修复存在；历史集成证据有明确范围。
- **ACC-005 Finding 重复投递**（未单列）：主机制已实现；验收目标未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:2259)。幂等、occurrence、迟到/乱序及身份冲突信封修复存在；历史集成证据有明确范围。
- **ACC-006 Finding 恢复后再发生**（未单列）：主机制已实现；验收目标未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:2265)。幂等、occurrence、迟到/乱序及身份冲突信封修复存在；历史集成证据有明确范围。

### Incident、恢复与时间线 · `incidents`

**入口：** GET /api/v1/incidents；POST /api/v1/incidents:merge；POST /api/v1/incidents/{incidentId}:split；GET /api/v1/incidents/{incidentId}/timeline。

**实现：** [correlator.go](/Users/mssc/Documents/Code/ops/platform/internal/incident/correlator.go)、[state_machine.go](/Users/mssc/Documents/Code/ops/platform/internal/incident/state_machine.go)、[split.go](/Users/mssc/Documents/Code/ops/platform/internal/incident/split.go)、[sp05_reads.go](/Users/mssc/Documents/Code/ops/platform/internal/httpapi/sp05_reads.go)、[sp05_mutation.go](/Users/mssc/Documents/Code/ops/platform/internal/httpapi/sp05_mutation.go)。

**测试入口：** [correlator_test.go](/Users/mssc/Documents/Code/ops/platform/internal/incident/correlator_test.go)、[reliable_start_test.go](/Users/mssc/Documents/Code/ops/platform/internal/incident/reliable_start_test.go)、[sp05_incident_test.go](/Users/mssc/Documents/Code/ops/platform/test/integration/sp05_incident_test.go)、[incident_workspace.test.tsx](/Users/mssc/Documents/Code/ops/platform/web/tests/incident_workspace.test.tsx)。

**UI：** [IncidentWorkspace.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/pages/IncidentWorkspace.tsx)。

**Profile：** 非虚拟化 core；来源/模型/执行能力按配置与资格启用。

**证据或边界：** [fix-status.md](/Users/mssc/Documents/Code/ops/platform/docs/reviews/project-wide-2026-10-16/fix-status.md)。

- **FR-050 Incident 自动生成**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:845)。生命周期、Merge/Split、稳定恢复与可靠起始时间门禁已有实现；逐类 timeline 事件展示另按专项 UI 验证。
- **FR-051 Incident 状态**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:853)。生命周期、Merge/Split、稳定恢复与可靠起始时间门禁已有实现；逐类 timeline 事件展示另按专项 UI 验证。
- **FR-052 Incident 自动恢复**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:870)。生命周期、Merge/Split、稳定恢复与可靠起始时间门禁已有实现；逐类 timeline 事件展示另按专项 UI 验证。
- **FR-053 Incident 重开**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:884)。生命周期、Merge/Split、稳定恢复与可靠起始时间门禁已有实现；逐类 timeline 事件展示另按专项 UI 验证。
- **FR-054 Incident Merge/Split**（P1）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:892)。生命周期、Merge/Split、稳定恢复与可靠起始时间门禁已有实现；逐类 timeline 事件展示另按专项 UI 验证。
- **FR-055 Incident 详情**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:912)。组件/协议和历史页面证据存在；ADR-0037 后续页面变更以及最终同轮联合页面未完整运行验收，不将旧通过覆盖新代码。
- **FR-060 Incident Timeline**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:939)。生命周期、Merge/Split、稳定恢复与可靠起始时间门禁已有实现；逐类 timeline 事件展示另按专项 UI 验证。
- **ACC-007 Incident 自动恢复**（未单列）：主机制已实现；验收目标未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:2271)。生命周期、Merge/Split、稳定恢复与可靠起始时间门禁已有实现；逐类 timeline 事件展示另按专项 UI 验证。

### Evidence、归档与重放 · `evidence`

**入口：** POST /api/v1/evidence:query；GET /api/v1/evidence/{evidenceId}。

**实现：** [domain.go](/Users/mssc/Documents/Code/ops/platform/internal/evidence/domain.go)、[repository.go](/Users/mssc/Documents/Code/ops/platform/internal/evidence/repository.go)、[archive.go](/Users/mssc/Documents/Code/ops/platform/internal/evidence/archive.go)、[recovery.go](/Users/mssc/Documents/Code/ops/platform/internal/evidence/recovery.go)、[store.go](/Users/mssc/Documents/Code/ops/platform/internal/archive/store.go)。

**测试入口：** [archive_test.go](/Users/mssc/Documents/Code/ops/platform/internal/evidence/archive_test.go)、[sp04_archive_test.go](/Users/mssc/Documents/Code/ops/platform/test/integration/sp04_archive_test.go)、[sp04_archive_roles_test.go](/Users/mssc/Documents/Code/ops/platform/test/integration/sp04_archive_roles_test.go)。

**UI：** [EvidencePanel.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/features/evidence/EvidencePanel.tsx)。

**Profile：** 非虚拟化 core；来源/模型/执行能力按配置与资格启用。

**证据或边界：** [README.md](/Users/mssc/Documents/Code/ops/platform/docs/evidence/sp010203-core-final-20261001/README.md)。

- **FR-070 Evidence 类型**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:975)。引用/摘要/来源授权、归档保护与明确降级；历史归档运行不代表当前全量恢复演练。
- **FR-071 Evidence 基本信息**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:994)。引用/摘要/来源授权、归档保护与明确降级；历史归档运行不代表当前全量恢复演练。
- **FR-072 Supporting Evidence**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1013)。引用/摘要/来源授权、归档保护与明确降级；历史归档运行不代表当前全量恢复演练。
- **FR-073 Conflicting Evidence**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1021)。引用/摘要/来源授权、归档保护与明确降级；历史归档运行不代表当前全量恢复演练。
- **FR-074 Missing Evidence**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1029)。引用/摘要/来源授权、归档保护与明确降级；历史归档运行不代表当前全量恢复演练。
- **FR-075 Degraded Source**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1037)。引用/摘要/来源授权、归档保护与明确降级；历史归档运行不代表当前全量恢复演练。
- **FR-076 Ranked Evidence**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1045)。引用/摘要/来源授权、归档保护与明确降级；历史归档运行不代表当前全量恢复演练。
- **FR-077 Evidence 可重放**（未单列）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1053)。引用/摘要/来源授权、归档保护与明确降级；历史归档运行不代表当前全量恢复演练。
- **DATA-004 Evidence 原始数据**（未单列）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1801)。引用/摘要/来源授权、归档保护与明确降级；历史归档运行不代表当前全量恢复演练。
- **ACC-008 Evidence Source 不可用**（未单列）：主机制已实现；验收目标未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:2277)。引用/摘要/来源授权、归档保护与明确降级；历史归档运行不代表当前全量恢复演练。

### 诊断图、范围与新鲜度 · `graph`

**入口：** POST /api/v1/diagnostic-graphs:build；GET /api/v1/resources/neighbors。

**实现：** [ariadne_adapter.go](/Users/mssc/Documents/Code/ops/platform/internal/graph/ariadne_adapter.go)、[protocol.go](/Users/mssc/Documents/Code/ops/platform/internal/graph/protocol.go)、[lease.go](/Users/mssc/Documents/Code/ops/platform/internal/graph/lease.go)、[response.go](/Users/mssc/Documents/Code/ops/platform/internal/graph/response.go)。

**测试入口：** [graph_test.go](/Users/mssc/Documents/Code/ops/platform/internal/graph/graph_test.go)、[lease_test.go](/Users/mssc/Documents/Code/ops/platform/internal/graph/lease_test.go)、[graph_replay_test.go](/Users/mssc/Documents/Code/ops/platform/test/contract/graph_replay_test.go)、[graph_view.test.tsx](/Users/mssc/Documents/Code/ops/platform/web/tests/graph_view.test.tsx)。

**UI：** [GraphView.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/features/graph/GraphView.tsx)。

**Profile：** 非虚拟化 core；来源/模型/执行能力按配置与资格启用。

**证据或边界：** [identity-timing-diagnostic.json](/Users/mssc/Documents/Code/ops/platform/docs/evidence/five-source-reverify-20261018/round5/identity-timing-diagnostic.json)。

- **FR-080 Diagnostic Graph 构建**（P0）：主机制已实现；环境/来源/授权受限（仅记录）。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1063)。图内核、重建、游标/epoch、来源与前后身份栅栏存在；当前远程 Graph 就绪失败仅记录。
- **FR-081 Diagnostic Graph 内容**（P0）：主机制已实现；环境/来源/授权受限（仅记录）。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1071)。图内核、重建、游标/epoch、来源与前后身份栅栏存在；当前远程 Graph 就绪失败仅记录。
- **FR-082 图查询边界**（P0）：主机制已实现；环境/来源/授权受限（仅记录）。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1088)。图内核、重建、游标/epoch、来源与前后身份栅栏存在；当前远程 Graph 就绪失败仅记录。
- **FR-083 图数据新鲜度**（P0）：主机制已实现；环境/来源/授权受限（仅记录）。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1105)。图内核、重建、游标/epoch、来源与前后身份栅栏存在；当前远程 Graph 就绪失败仅记录。
- **NFR-021 Graph 可重建**（未单列）：主机制已实现；环境/来源/授权受限（仅记录）。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1999)。图内核、重建、游标/epoch、来源与前后身份栅栏存在；当前远程 Graph 就绪失败仅记录。

### 确定性 RCA 与 Revision · `rca`

**入口：** GET /api/v1/incidents/{incidentId}/rca；GET /api/v1/incidents/{incidentId}/rca/revisions。

**实现：** [engine.go](/Users/mssc/Documents/Code/ops/platform/internal/rca/engine.go)、[recipe.go](/Users/mssc/Documents/Code/ops/platform/internal/rca/recipe.go)、[repository.go](/Users/mssc/Documents/Code/ops/platform/internal/rca/repository.go)、[runtime_fence_test.go](/Users/mssc/Documents/Code/ops/platform/internal/rca/runtime_fence_test.go)。

**测试入口：** [recipe_test.go](/Users/mssc/Documents/Code/ops/platform/internal/rca/recipe_test.go)、[causal_test.go](/Users/mssc/Documents/Code/ops/platform/internal/rca/causal_test.go)、[sp05_review_rca_test.go](/Users/mssc/Documents/Code/ops/platform/test/integration/sp05_review_rca_test.go)。

**UI：** [RCAPanel.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/features/rca/RCAPanel.tsx)。

**Profile：** 非虚拟化 core；来源/模型/执行能力按配置与资格启用。

**证据或边界：** [sp05-golden-slices.md](/Users/mssc/Documents/Code/ops/platform/docs/runbooks/sp05-golden-slices.md)。

- **FR-090 Root Cause Candidate**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1122)。confirmed/probable/unresolved、证据引用和追加 revision；少时间或关系资格时保留缺口，不构成代码未开发。
- **FR-091 RCA 状态**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1130)。confirmed/probable/unresolved、证据引用和追加 revision；少时间或关系资格时保留缺口，不构成代码未开发。
- **FR-092 RCA 证据引用**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1169)。confirmed/probable/unresolved、证据引用和追加 revision；少时间或关系资格时保留缺口，不构成代码未开发。
- **FR-093 RCA Revision**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1177)。confirmed/probable/unresolved、证据引用和追加 revision；少时间或关系资格时保留缺口，不构成代码未开发。
- **FR-094 RCA 解释**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1196)。confirmed/probable/unresolved、证据引用和追加 revision；少时间或关系资格时保留缺口，不构成代码未开发。
- **ACC-009 RCA Unresolved**（未单列）：主机制已实现；验收目标未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:2283)。confirmed/probable/unresolved、证据引用和追加 revision；少时间或关系资格时保留缺口，不构成代码未开发。
- **AI-004 R1～R6 关联规则、反证与缺口**（专项/交付）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/platform/docs/adr/0041-fault-chain-investigation-enhancement.md:1)。确定性谓词与来源/时间资格存在；不把共现、模型推断或数值改善写成 confirmed RCA。
  专项实现：[fault_chain_context.go](/Users/mssc/Documents/Code/ops/platform/internal/investigation/fault_chain_context.go)、[fault_chain_changes.go](/Users/mssc/Documents/Code/ops/platform/internal/investigation/fault_chain_changes.go)、[fault_chain_recovery.go](/Users/mssc/Documents/Code/ops/platform/internal/investigation/fault_chain_recovery.go)；专项测试：[fault_chain_context_test.go](/Users/mssc/Documents/Code/ops/platform/internal/investigation/fault_chain_context_test.go)、[fault_chain_recovery_test.go](/Users/mssc/Documents/Code/ops/platform/internal/investigation/fault_chain_recovery_test.go)。

### 持久调查与恢复 · `investigation`

**入口：** POST /api/v1/incidents/{incidentId}/investigations；GET /api/v1/investigations/{jobId}/events；POST /api/v1/investigations/{jobId}:cancel。

**实现：** [repository.go](/Users/mssc/Documents/Code/ops/platform/internal/investigation/repository.go)、[recovery.go](/Users/mssc/Documents/Code/ops/platform/internal/investigation/recovery.go)、[sp06_runtime.go](/Users/mssc/Documents/Code/ops/platform/internal/app/sp06_runtime.go)、[worker.py](/Users/mssc/Documents/Code/ops/platform/services/investigator/src/investigator/worker.py)。

**测试入口：** [sp06_job_test.go](/Users/mssc/Documents/Code/ops/platform/test/integration/sp06_job_test.go)、[sp06_recovery_test.go](/Users/mssc/Documents/Code/ops/platform/test/integration/sp06_recovery_test.go)、[sp06_sse_live_test.go](/Users/mssc/Documents/Code/ops/platform/test/integration/sp06_sse_live_test.go)、[investigation.test.tsx](/Users/mssc/Documents/Code/ops/platform/web/tests/investigation.test.tsx)。

**UI：** [InvestigationPanel.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/features/investigation/InvestigationPanel.tsx)。

**Profile：** 非虚拟化 core；来源/模型/执行能力按配置与资格启用。

**证据或边界：** [sp06-investigation-runtime.md](/Users/mssc/Documents/Code/ops/platform/docs/runbooks/sp06-investigation-runtime.md)。

- **FR-100 发起 Investigation**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1214)。持久 Job/Step、租约 generation、取消/接管与模型离线降级已接通。
- **FR-101 Investigation 状态**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1224)。持久 Job/Step、租约 generation、取消/接管与模型离线降级已接通。
- **FR-102 调查进度**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1241)。持久 Job/Step、租约 generation、取消/接管与模型离线降级已接通。
- **FR-103 调查继承**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1256)。持久 Job/Step、租约 generation、取消/接管与模型离线降级已接通。
- **FR-104 调查取消**（P1）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1264)。持久 Job/Step、租约 generation、取消/接管与模型离线降级已接通。
- **FR-105 Agent 不可用降级**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1272)。持久 Job/Step、租约 generation、取消/接管与模型离线降级已接通。
- **NFR-022 Investigation 恢复**（未单列）：主机制已实现；验收目标未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:2005)。持久 Job/Step、租约 generation、取消/接管与模型离线降级已接通。
- **NFR-030 Agent 降级**（未单列）：主机制已实现；验收目标未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:2032)。持久 Job/Step、租约 generation、取消/接管与模型离线降级已接通。
- **ACC-010 Agent 崩溃恢复**（未单列）：主机制已实现；验收目标未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:2289)。持久 Job/Step、租约 generation、取消/接管与模型离线降级已接通。
- **AI-003 Pod/Node/PVC 确定性证据收集**（专项/交付）：主机制已实现；环境/来源/授权受限（仅记录）。[需求依据](/Users/mssc/Documents/Code/ops/platform/docs/plans/v1.0-operational-analysis-completion.md:1)。三类型共用收集主路径；Node 显式 clusterScoped，PVC 不套用 Pod 网络/指标；实际 Node/PVC 授权/对象仍未具备。
  专项实现：[fault_chain.py](/Users/mssc/Documents/Code/ops/platform/services/investigator/src/investigator/fault_chain.py)、[resource_correlation.go](/Users/mssc/Documents/Code/ops/platform/internal/investigation/resource_correlation.go)、[FaultChainCollector.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/features/assistant/FaultChainCollector.tsx)；专项测试：[test_fault_chain_unittest.py](/Users/mssc/Documents/Code/ops/platform/services/investigator/tests/test_fault_chain_unittest.py)、[resource_correlation_test.go](/Users/mssc/Documents/Code/ops/platform/internal/investigation/resource_correlation_test.go)、[resource_correlation.test.tsx](/Users/mssc/Documents/Code/ops/platform/web/tests/resource_correlation.test.tsx)。

### 语义工具、授权与预算 · `tools`

**入口：** 标准 MCP /mcp；调查 Step 账本。

**实现：** [server.go](/Users/mssc/Documents/Code/ops/platform/internal/investigation/mcp/server.go)、[catalog.go](/Users/mssc/Documents/Code/ops/platform/internal/investigation/tools/catalog.go)、[context_signer.go](/Users/mssc/Documents/Code/ops/platform/internal/investigation/context_signer.go)、[allocation.go](/Users/mssc/Documents/Code/ops/platform/internal/investigation/allocation.go)。

**测试入口：** [server_test.go](/Users/mssc/Documents/Code/ops/platform/internal/investigation/mcp/server_test.go)、[sp06_mcp_test.go](/Users/mssc/Documents/Code/ops/platform/test/integration/sp06_mcp_test.go)、[sp06_review_boundaries_test.go](/Users/mssc/Documents/Code/ops/platform/test/integration/sp06_review_boundaries_test.go)。

**UI：** [StepLedger.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/features/investigation/StepLedger.tsx)。

**Profile：** 非虚拟化 core；来源/模型/执行能力按配置与资格启用。

**证据或边界：** [fix-status.md](/Users/mssc/Documents/Code/ops/platform/docs/reviews/project-wide-2026-10-16/fix-status.md)。

- **FR-110 语义工具优先**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1289)。短时 Context/JTI/序列、真实预算与成功提交复用；历史 token 预留/展开成本缺陷已修复。
- **FR-111 工具授权**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1312)。短时 Context/JTI/序列、真实预算与成功提交复用；历史 token 预留/展开成本缺陷已修复。
- **FR-112 工具预算**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1328)。短时 Context/JTI/序列、真实预算与成功提交复用；历史 token 预留/展开成本缺陷已修复。
- **FR-113 调用记录**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1347)。短时 Context/JTI/序列、真实预算与成功提交复用；历史 token 预留/展开成本缺陷已修复。
- **SEC-004 Agent 最小权限**（未单列）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1846)。短时 Context/JTI/序列、真实预算与成功提交复用；历史 token 预留/展开成本缺陷已修复。
- **SEC-005 工具重放防护**（未单列）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1852)。短时 Context/JTI/序列、真实预算与成功提交复用；历史 token 预留/展开成本缺陷已修复。
- **ACC-011 Tool 越权**（未单列）：主机制已实现；验收目标未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:2295)。短时 Context/JTI/序列、真实预算与成功提交复用；历史 token 预留/展开成本缺陷已修复。

### 影响范围与分类 · `impact`

**入口：** GET /api/v1/resources/impact-scope。

**实现：** [impact.go](/Users/mssc/Documents/Code/ops/platform/internal/graph/impact.go)、[impact.go](/Users/mssc/Documents/Code/ops/platform/internal/rca/impact.go)。

**测试入口：** [impact_test.go](/Users/mssc/Documents/Code/ops/platform/internal/rca/impact_test.go)、[sp05_hardware_impact_test.go](/Users/mssc/Documents/Code/ops/platform/internal/graph/sp05_hardware_impact_test.go)、[graph_view.test.tsx](/Users/mssc/Documents/Code/ops/platform/web/tests/graph_view.test.tsx)。

**UI：** [GraphView.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/features/graph/GraphView.tsx)。

**Profile：** 非虚拟化 core；来源/模型/执行能力按配置与资格启用。

**证据或边界：** [sp05-dimm-failure.md](/Users/mssc/Documents/Code/ops/platform/docs/runbooks/sp05-dimm-failure.md)。

- **FR-120 影响范围**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1379)。直接、间接、仅依赖分类已实现；正式 VM 链路分项延期。
- **FR-121 影响分类**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1395)。直接、间接、仅依赖分类已实现；正式 VM 链路分项延期。
- **UI-004 影响范围可视化**（P1）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1697)。组件/协议和历史页面证据存在；ADR-0037 后续页面变更以及最终同轮联合页面未完整运行验收，不将旧通过覆盖新代码。

### 处置建议与 ActionPlan · `plans`

**入口：** POST /api/v1/action-plans；POST /api/v1/action-plans/{actionPlanId}:accept；POST /api/v1/action-plans/{actionPlanId}:dismiss。

**实现：** [plan.go](/Users/mssc/Documents/Code/ops/platform/internal/action/plan.go)、[result.go](/Users/mssc/Documents/Code/ops/platform/internal/investigation/result.go)、[action-plan-v2.schema.json](/Users/mssc/Documents/Code/ops/platform/api/schemas/action-plan-v2.schema.json)。

**测试入口：** [sp06_validator_live_test.go](/Users/mssc/Documents/Code/ops/platform/test/integration/sp06_validator_live_test.go)、[manual_command_flow.test.tsx](/Users/mssc/Documents/Code/ops/platform/web/tests/manual_command_flow.test.tsx)。

**UI：** [SuggestionCard.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/features/action/SuggestionCard.tsx)。

**Profile：** 非虚拟化 core；来源/模型/执行能力按配置与资格启用。

**证据或边界：** [README.md](/Users/mssc/Documents/Code/ops/platform/docs/evidence/sp07-20261009/README.md)。

- **FR-130 处置建议**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1411)。建议与操作者实际命令分离，ActionPlan 状态不等同执行；真实模型建议质量独立。
- **FR-131 结构化 ActionPlan**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1430)。建议与操作者实际命令分离，ActionPlan 状态不等同执行；真实模型建议质量独立。
- **FR-132 ActionPlan 状态**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1438)。建议与操作者实际命令分离，ActionPlan 状态不等同执行；真实模型建议质量独立。

### 人工确认与受控执行 · `commands`

**入口：** POST /api/v1/commands:risk-assess；POST /api/v1/commands:risk-acknowledge；POST /api/v1/command-executions。

**实现：** [service.go](/Users/mssc/Documents/Code/ops/platform/internal/action/service.go)、[runner_protocol.go](/Users/mssc/Documents/Code/ops/platform/internal/action/runner_protocol.go)、[stepup.go](/Users/mssc/Documents/Code/ops/platform/internal/auth/stepup.go)、[action_handlers.go](/Users/mssc/Documents/Code/ops/platform/internal/httpapi/action_handlers.go)。

**测试入口：** [service_test.go](/Users/mssc/Documents/Code/ops/platform/internal/action/service_test.go)、[sp07_command_test.go](/Users/mssc/Documents/Code/ops/platform/test/integration/sp07_command_test.go)、[stepup_replay_test.go](/Users/mssc/Documents/Code/ops/platform/test/integration/stepup_replay_test.go)、[sp07_execution_profile_test.go](/Users/mssc/Documents/Code/ops/platform/test/security/sp07_execution_profile_test.go)、[command_authorization.test.tsx](/Users/mssc/Documents/Code/ops/platform/web/tests/command_authorization.test.tsx)。

**UI：** [CommandComposer.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/features/action/CommandComposer.tsx)、[CommandEditor.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/features/action/CommandEditor.tsx)。

**Profile：** 非虚拟化 core；来源/模型/执行能力按配置与资格启用。

**证据或边界：** [0003-manual-command-remediation.md](/Users/mssc/Documents/Code/ops/platform/docs/adr/0003-manual-command-remediation.md)、[README.md](/Users/mssc/Documents/Code/ops/platform/docs/evidence/sp07-20261009/README.md)。

- **FR-140 策略校验**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1457)。本人实际命令确认、必要 step-up、最多一次 dispatch、不确定结果先对账；无审批人流程、无模型自动执行。
- **FR-141 操作者风险确认**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1474)。本人实际命令确认、必要 step-up、最多一次 dispatch、不确定结果先对账；无审批人流程、无模型自动执行。
- **FR-142 执行**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1484)。本人实际命令确认、必要 step-up、最多一次 dispatch、不确定结果先对账；无审批人流程、无模型自动执行。
- **FR-143 幂等执行**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1494)。本人实际命令确认、必要 step-up、最多一次 dispatch、不确定结果先对账；无审批人流程、无模型自动执行。
- **ACC-012 命令风险确认与 step-up**（未单列）：主机制已实现；验收目标未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:2301)。本人实际命令确认、必要 step-up、最多一次 dispatch、不确定结果先对账；无审批人流程、无模型自动执行。
- **ACC-013 Action 幂等**（未单列）：主机制已实现；验收目标未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:2307)。本人实际命令确认、必要 step-up、最多一次 dispatch、不确定结果先对账；无审批人流程、无模型自动执行。
- **ACC-014 Action 执行结果不确定**（未单列）：主机制已实现；验收目标未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:2313)。本人实际命令确认、必要 step-up、最多一次 dispatch、不确定结果先对账；无审批人流程、无模型自动执行。

### 处置后检查与回退 · `postcheck`

**入口：** GET /api/v1/command-executions/{executionId}/post-check。

**实现：** [sp07_postcheck.go](/Users/mssc/Documents/Code/ops/platform/internal/app/sp07_postcheck.go)、[postcheck.go](/Users/mssc/Documents/Code/ops/platform/internal/action/postcheck.go)、[plan.go](/Users/mssc/Documents/Code/ops/platform/internal/action/plan.go)、[postcheck_support.go](/Users/mssc/Documents/Code/ops/platform/internal/action/postcheck_support.go)、[RiskPanel.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/features/action/RiskPanel.tsx)、[execution_relation.go](/Users/mssc/Documents/Code/ops/platform/internal/action/execution_relation.go)、[postcheck_report.go](/Users/mssc/Documents/Code/ops/platform/internal/action/postcheck_report.go)、[CommandComposer.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/features/action/CommandComposer.tsx)。

**测试入口：** [kubernetes_executor_test.go](/Users/mssc/Documents/Code/ops/platform/internal/action/kubernetes_executor_test.go)、[sp07_command_test.go](/Users/mssc/Documents/Code/ops/platform/test/integration/sp07_command_test.go)、[postcheck.test.tsx](/Users/mssc/Documents/Code/ops/platform/web/tests/postcheck.test.tsx)、[postcheck_support_test.go](/Users/mssc/Documents/Code/ops/platform/internal/action/postcheck_support_test.go)、[execution_relation_test.go](/Users/mssc/Documents/Code/ops/platform/internal/action/execution_relation_test.go)、[postcheck_report_test.go](/Users/mssc/Documents/Code/ops/platform/internal/action/postcheck_report_test.go)、[command_authorization.test.tsx](/Users/mssc/Documents/Code/ops/platform/web/tests/command_authorization.test.tsx)。

**UI：** [PostCheckPanel.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/features/action/PostCheckPanel.tsx)。

**Profile：** 非虚拟化 core；来源/模型/执行能力按配置与资格启用。

**证据或边界：** [README.md](/Users/mssc/Documents/Code/ops/platform/docs/evidence/sp07-20261009/README.md)、[core-workflow-closeout.md](/Users/mssc/Documents/Code/ops/platform/docs/release/core-workflow-closeout.md)、[disposition-verification-closeout.md](/Users/mssc/Documents/Code/ops/platform/docs/release/disposition-verification-closeout.md)。

- **FR-144 Post-check**（P0）：部分实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1504)。共用冻结报告与证据/来源/Recipe 门禁已实现；增加 Pod、Deployment、Service/EndpointSlice 后端和 PVC/CSI 原生关联健康判据及执行后 Warning。真实网络连通、应用读写、其他目标及全部新增故障仍未覆盖，保持部分实现。 已增加经本人确认的 TCP/HTTP 和专用 PVC 文件写读执行，回执须认证、逐参数绑定且输出归档验证后，才与原生场景事实共同用于结论；仅证明 Runner 位置和专用测试卷，不等同全部客户端或业务数据验收。 对应 G-004。
- **FR-145 回退**（P1）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1516)。回退与原执行关联并纳入本人确认；新回退要求明确的评审配置摘要，只有摘要匹配且可信场景健康通过、无新增 Warning 才判目标达成。仍是人工输入/确认，不自动回退，不证明未覆盖配置字段或业务数据恢复。 已增加经本人确认的 TCP/HTTP 和专用 PVC 文件写读执行，回执须认证、逐参数绑定且输出归档验证后，才与原生场景事实共同用于结论；仅证明 Runner 位置和专用测试卷，不等同全部客户端或业务数据验收。 对应 C-002。
  专项实现：[execution_relation.go](/Users/mssc/Documents/Code/ops/platform/internal/action/execution_relation.go)、[postcheck_report.go](/Users/mssc/Documents/Code/ops/platform/internal/action/postcheck_report.go)、[service.go](/Users/mssc/Documents/Code/ops/platform/internal/action/service.go)、[PostCheckPanel.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/features/action/PostCheckPanel.tsx)；专项测试：[execution_relation_test.go](/Users/mssc/Documents/Code/ops/platform/internal/action/execution_relation_test.go)、[postcheck_report_test.go](/Users/mssc/Documents/Code/ops/platform/internal/action/postcheck_report_test.go)、[postcheck.test.tsx](/Users/mssc/Documents/Code/ops/platform/web/tests/postcheck.test.tsx)、[sp07_command_test.go](/Users/mssc/Documents/Code/ops/platform/test/integration/sp07_command_test.go)。

### 审计与不可抵赖 · `audit`

**入口：** GET /api/v1/audit-records；GET /api/v1/audit-records/{auditId}。

**实现：** [append.go](/Users/mssc/Documents/Code/ops/platform/internal/audit/append.go)、[segments.go](/Users/mssc/Documents/Code/ops/platform/internal/audit/segments.go)、[merkle.go](/Users/mssc/Documents/Code/ops/platform/internal/audit/merkle.go)、[audit_handlers.go](/Users/mssc/Documents/Code/ops/platform/internal/httpapi/audit_handlers.go)。

**测试入口：** [append_test.go](/Users/mssc/Documents/Code/ops/platform/internal/audit/append_test.go)、[segment_test.go](/Users/mssc/Documents/Code/ops/platform/internal/audit/segment_test.go)、[audit_segments_test.go](/Users/mssc/Documents/Code/ops/platform/test/integration/audit_segments_test.go)、[admin_audit.test.tsx](/Users/mssc/Documents/Code/ops/platform/web/tests/admin_audit.test.tsx)。

**UI：** [AuditPage.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/pages/admin/AuditPage.tsx)。

**Profile：** 非虚拟化 core；来源/模型/执行能力按配置与资格启用。

**证据或边界：** [README.md](/Users/mssc/Documents/Code/ops/platform/docs/evidence/sp010203-core-final-20261001/README.md)。

- **FR-150 全链路审计**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1532)。租户审计、签名段/Merkle/前驱验证、授权查询存在；不自动宣称所有新增行为均已运行审计。
- **FR-151 审计不可抵赖**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1555)。租户审计、签名段/Merkle/前驱验证、授权查询存在；不自动宣称所有新增行为均已运行审计。
- **FR-152 审计查询**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1563)。租户审计、签名段/Merkle/前驱验证、授权查询存在；不自动宣称所有新增行为均已运行审计。

### 来源与集群管理 · `registration`

**入口：** GET /api/v1/admin/source-registrations；GET /api/v1/admin/clusters。

**实现：** [service.go](/Users/mssc/Documents/Code/ops/platform/internal/source/service.go)、[cluster.go](/Users/mssc/Documents/Code/ops/platform/internal/source/cluster.go)、[source_handlers.go](/Users/mssc/Documents/Code/ops/platform/internal/httpapi/source_handlers.go)、[cluster_handlers.go](/Users/mssc/Documents/Code/ops/platform/internal/httpapi/cluster_handlers.go)。

**测试入口：** [service_test.go](/Users/mssc/Documents/Code/ops/platform/internal/source/service_test.go)、[source_test.go](/Users/mssc/Documents/Code/ops/platform/test/integration/source_test.go)、[admin_audit.test.tsx](/Users/mssc/Documents/Code/ops/platform/web/tests/admin_audit.test.tsx)。

**UI：** [AdminPage.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/pages/admin/AdminPage.tsx)。

**Profile：** 非虚拟化 core；来源/模型/执行能力按配置与资格启用。

**证据或边界：** [sp03-foundation-runtime.md](/Users/mssc/Documents/Code/ops/platform/docs/runbooks/sp03-foundation-runtime.md)。

- **FR-160 数据源管理**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1583)。非虚拟化注册、来源版本/范围、凭据轮换和状态；来源配置不等于实际能力已合格。
- **FR-161 集群管理**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1600)。非虚拟化注册、来源版本/范围、凭据轮换和状态；来源配置不等于实际能力已合格。

### 策略、配方与工具管理 · `registry`

**入口：** GET /api/v1/admin/tools；POST /api/v1/admin/registry-drafts；POST /api/v1/admin/registry-drafts/{draftId}:publish；POST /api/v1/admin/registry-activations。

**实现：** [service.go](/Users/mssc/Documents/Code/ops/platform/internal/configregistry/service.go)、[config_handlers.go](/Users/mssc/Documents/Code/ops/platform/internal/httpapi/config_handlers.go)。

**测试入口：** [service_test.go](/Users/mssc/Documents/Code/ops/platform/internal/configregistry/service_test.go)、[sp05_recipe_test.go](/Users/mssc/Documents/Code/ops/platform/internal/configregistry/sp05_recipe_test.go)、[registry_recovery.test.tsx](/Users/mssc/Documents/Code/ops/platform/web/tests/registry_recovery.test.tsx)。

**UI：** [RegistryEditor.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/pages/admin/RegistryEditor.tsx)。

**Profile：** 非虚拟化 core；来源/模型/执行能力按配置与资格启用。

**证据或边界：** [sp03-foundation-runtime.md](/Users/mssc/Documents/Code/ops/platform/docs/runbooks/sp03-foundation-runtime.md)。

- **FR-162 策略管理**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1616)。配置草稿、验证/签名发布/激活/退役与双角色边界；清单只证明主路径，不替代每种策略内容验收。
- **FR-163 Tool 管理**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1632)。配置草稿、验证/签名发布/激活/退役与双角色边界；清单只证明主路径，不替代每种策略内容验收。

### 统一工作台与证据交互 · `workbench`

**入口：** 页面 /incidents/:incidentId、/assistant。

**实现：** [IncidentWorkspace.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/pages/IncidentWorkspace.tsx)、[FaultChainPanel.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/features/assistant/FaultChainPanel.tsx)、[DiagnosisAnswer.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/features/assistant/DiagnosisAnswer.tsx)。

**测试入口：** [incident_workspace.test.tsx](/Users/mssc/Documents/Code/ops/platform/web/tests/incident_workspace.test.tsx)、[fault_chain.test.tsx](/Users/mssc/Documents/Code/ops/platform/web/tests/fault_chain.test.tsx)、[fault_chain_checks.test.tsx](/Users/mssc/Documents/Code/ops/platform/web/tests/fault_chain_checks.test.tsx)、[assistant_status.test.tsx](/Users/mssc/Documents/Code/ops/platform/web/tests/assistant_status.test.tsx)。

**UI：** [IncidentWorkspace.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/pages/IncidentWorkspace.tsx)、[FaultChainPanel.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/features/assistant/FaultChainPanel.tsx)。

**Profile：** 非虚拟化 core；来源/模型/执行能力按配置与资格启用。

**证据或边界：** [README.md](/Users/mssc/Documents/Code/ops/platform/docs/evidence/fault-chain-ui-20261015/README.md)、[acceptance-status.json](/Users/mssc/Documents/Code/ops/platform/docs/evidence/five-source-reverify-20261018/round5/acceptance-status.json)。

- **UI-001 统一故障工作台**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1648)。组件/协议和历史页面证据存在；ADR-0037 后续页面变更以及最终同轮联合页面未完整运行验收，不将旧通过覆盖新代码。
- **UI-002 不确定性展示**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1671)。组件/协议和历史页面证据存在；ADR-0037 后续页面变更以及最终同轮联合页面未完整运行验收，不将旧通过覆盖新代码。
- **UI-003 Evidence 可追溯**（P0）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1689)。组件/协议和历史页面证据存在；ADR-0037 后续页面变更以及最终同轮联合页面未完整运行验收，不将旧通过覆盖新代码。
- **AI-006 证据定位、缺口只读补查与轮次状态**（专项/交付）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/platform/docs/evidence/fault-chain-ui-20261015/README.md:1)。独立 evidence_only 补查绑定原缺口/UID/窗口，不覆盖原证据，不自动扩权。
  专项实现：[FaultChainPanel.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/features/assistant/FaultChainPanel.tsx)、[FaultChainGapCheck.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/features/assistant/FaultChainGapCheck.tsx)、[assistant_fault_chain.go](/Users/mssc/Documents/Code/ops/platform/internal/httpapi/assistant_fault_chain.go)；专项测试：[fault_chain_checks.test.tsx](/Users/mssc/Documents/Code/ops/platform/web/tests/fault_chain_checks.test.tsx)、[fault_chain_check_test.go](/Users/mssc/Documents/Code/ops/platform/internal/assistant/fault_chain_check_test.go)。

### 指标与日志来源 · `victoria`

**入口：** analytics 指标/日志；query_metrics/query_logs。

**实现：** [victoria.go](/Users/mssc/Documents/Code/ops/platform/internal/evidence/victoria.go)、[victoria.go](/Users/mssc/Documents/Code/ops/platform/internal/analytics/victoria.go)、[log_trend.go](/Users/mssc/Documents/Code/ops/platform/internal/analytics/log_trend.go)。

**测试入口：** [victoria_adapters_test.go](/Users/mssc/Documents/Code/ops/platform/internal/evidence/victoria_adapters_test.go)、[assistant_live_source_test.go](/Users/mssc/Documents/Code/ops/platform/test/integration/assistant_live_source_test.go)。

**UI：** [QueryResultView.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/features/assistant/QueryResultView.tsx)。

**Profile：** 非虚拟化 core；来源/模型/执行能力按配置与资格启用。

**证据或边界：** [receipt.json](/Users/mssc/Documents/Code/ops/platform/docs/evidence/five-source-reverify-20261018/round5/live-source/receipt.json)。

- **INT-001 VictoriaMetrics**（未单列）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1707)。同 UID/租户/窗口与来源 coverage；样例不作总体，历史真实采集有界。
- **INT-002 VictoriaLogs**（未单列）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1713)。同 UID/租户/窗口与来源 coverage；样例不作总体，历史真实采集有界。

### 原生 Kubernetes 来源 · `kubeapi`

**入口：** SP04 list/watch → graph/history；SP05 原生查询。

**实现：** [informer.go](/Users/mssc/Documents/Code/ops/platform/internal/integrations/kubernetes/informer.go)、[projection_nested.go](/Users/mssc/Documents/Code/ops/platform/internal/integrations/kubernetes/projection_nested.go)、[sp04_collector.go](/Users/mssc/Documents/Code/ops/platform/internal/app/sp04_collector.go)。

**测试入口：** [rate_budget_test.go](/Users/mssc/Documents/Code/ops/platform/internal/integrations/kubernetes/rate_budget_test.go)、[sp04_runtime_test.go](/Users/mssc/Documents/Code/ops/platform/test/integration/sp04_runtime_test.go)。

**UI：** [CapabilitiesPage.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/pages/CapabilitiesPage.tsx)。

**Profile：** 非虚拟化 core；来源/模型/执行能力按配置与资格启用。

**证据或边界：** [sp04-resource-evidence-runtime.md](/Users/mssc/Documents/Code/ops/platform/docs/runbooks/sp04-resource-evidence-runtime.md)。

- **INT-004 Kubernetes API**（未单列）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1725)。共享 QPS、List/Watch、重列与 UID 绑定；远程性能只留档。

### 模型 Profile 与 HolmesGPT · `model`

**入口：** SP06 调查；Assistant 解释轮次。

**实现：** [profile.go](/Users/mssc/Documents/Code/ops/platform/internal/modelprofile/profile.go)、[holmes_adapter.py](/Users/mssc/Documents/Code/ops/platform/services/investigator/src/investigator/holmes_adapter.py)、[budget.py](/Users/mssc/Documents/Code/ops/platform/services/investigator/src/investigator/budget.py)。

**测试入口：** [profile_test.go](/Users/mssc/Documents/Code/ops/platform/internal/modelprofile/profile_test.go)、[test_assistant_binding.py](/Users/mssc/Documents/Code/ops/platform/services/investigator/tests/test_assistant_binding.py)、[test_context_projection_unittest.py](/Users/mssc/Documents/Code/ops/platform/services/investigator/tests/test_context_projection_unittest.py)。

**UI：** [AssistantPage.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/pages/AssistantPage.tsx)。

**Profile：** 非虚拟化 core；来源/模型/执行能力按配置与资格启用。

**证据或边界：** [README.md](/Users/mssc/Documents/Code/ops/platform/docs/evidence/deepseek-acceptance-20261016/README.md)、[prior-retention-reassessment.json](/Users/mssc/Documents/Code/ops/platform/docs/evidence/five-source-reverify-20261018/round5/prior-retention-reassessment.json)。

- **INT-007 模型服务**（未单列）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1745)。调用与 Profile 机制存在；内网模型质量、外部 DeepSeek 探针和平台运行单独记录；旧长上下文结果已更正为 5/6。
- **AI-001 会话、问数与 operational 分析**（专项/交付）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/platform/docs/adr/0038-ai-assistant-development.md:1)。复用会话、语义数据集、Job v3 与已提交 QueryResult；问数实际执行已有专项证据。
  专项实现：[repository.go](/Users/mssc/Documents/Code/ops/platform/internal/assistant/repository.go)、[assistant_handlers.go](/Users/mssc/Documents/Code/ops/platform/internal/httpapi/assistant_handlers.go)、[executor.go](/Users/mssc/Documents/Code/ops/platform/internal/analytics/executor.go)；专项测试：[analytics_test.go](/Users/mssc/Documents/Code/ops/platform/internal/analytics/analytics_test.go)、[test_assistant_binding.py](/Users/mssc/Documents/Code/ops/platform/services/investigator/tests/test_assistant_binding.py)。
- **AI-005 独立解释、冻结追问与超时降级**（专项/交付）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/platform/docs/plans/v1.0-operational-analysis-completion.md:1)。上下文和历史已提交引用保留；辅助能力整体 2s 上限在 owning tool 3s 内降级；真实页面最终闭环未运行成功。
  专项实现：[assistant_tools.go](/Users/mssc/Documents/Code/ops/platform/internal/httpapi/assistant_tools.go)、[assistant_source_capabilities.go](/Users/mssc/Documents/Code/ops/platform/internal/httpapi/assistant_source_capabilities.go)、[assistant_binding.py](/Users/mssc/Documents/Code/ops/platform/services/investigator/src/investigator/assistant_binding.py)；专项测试：[assistant_source_capabilities_test.go](/Users/mssc/Documents/Code/ops/platform/internal/httpapi/assistant_source_capabilities_test.go)、[assistant_context_timeout_test.go](/Users/mssc/Documents/Code/ops/platform/test/integration/assistant_context_timeout_test.go)、[test_assistant_context_preload.py](/Users/mssc/Documents/Code/ops/platform/services/investigator/tests/test_assistant_context_preload.py)。
- **AI-007 长上下文投影、Profile 与复核**（专项/交付）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/platform/docs/evidence/five-source-reverify-20261018/round5/README.md:1)。功能和数值方向校验存在；119962-token 旧响应重算 5/6 且拼接范围不一致；917288 仅容量，满 1M 质量未验证。
  专项实现：[assistant_binding.py](/Users/mssc/Documents/Code/ops/platform/services/investigator/src/investigator/assistant_binding.py)、[model_profile.py](/Users/mssc/Documents/Code/ops/platform/services/investigator/src/investigator/model_profile.py)、[verify_long_context.py](/Users/mssc/Documents/Code/ops/platform/scripts/assistant-runtime/verify_long_context.py)；专项测试：[test_context_projection_unittest.py](/Users/mssc/Documents/Code/ops/platform/services/investigator/tests/test_context_projection_unittest.py)、[test_verify_long_context.py](/Users/mssc/Documents/Code/ops/platform/scripts/assistant-runtime/test_verify_long_context.py)。

### 领域、时间与保留 · `persistence`

**入口：** 租户事务、迁移、归档/引用闭包与保留 worker。

**实现：** [tenant_tx.go](/Users/mssc/Documents/Code/ops/platform/internal/persistence/tenant_tx.go)、[retention.go](/Users/mssc/Documents/Code/ops/platform/internal/evidence/retention.go)、[retention.go](/Users/mssc/Documents/Code/ops/platform/internal/assistant/retention.go)、[reliable_start_test.go](/Users/mssc/Documents/Code/ops/platform/internal/incident/reliable_start_test.go)、[00043_resource_observations.sql](/Users/mssc/Documents/Code/ops/platform/migrations/00043_resource_observations.sql)。

**测试入口：** [migrations_test.go](/Users/mssc/Documents/Code/ops/platform/test/integration/migrations_test.go)、[assistant_scope_retention_test.go](/Users/mssc/Documents/Code/ops/platform/test/integration/assistant_scope_retention_test.go)、[reliable_start_test.go](/Users/mssc/Documents/Code/ops/platform/internal/incident/reliable_start_test.go)。

**UI：** [EnvelopeMeta.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/components/EnvelopeMeta.tsx)。

**Profile：** 非虚拟化 core；来源/模型/执行能力按配置与资格启用。

**证据或边界：** [database-migrations.md](/Users/mssc/Documents/Code/ops/platform/docs/runbooks/database-migrations.md)、[fix-status.md](/Users/mssc/Documents/Code/ops/platform/docs/reviews/project-wide-2026-10-16/fix-status.md)。

- **DATA-001 核心领域对象**（未单列）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1755)。前向迁移、时间可靠性、tenant/RLS 与引用依赖闭包存在；完整保留恢复仍需独立验收。
- **DATA-002 时间语义**（未单列）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1776)。前向迁移、时间可靠性、tenant/RLS 与引用依赖闭包存在；完整保留恢复仍需独立验收。
- **DATA-003 数据保留**（未单列）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1788)。前向迁移、时间可靠性、tenant/RLS 与引用依赖闭包存在；完整保留恢复仍需独立验收。
- **DATA-005 数据完整性**（未单列）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1809)。前向迁移、时间可靠性、tenant/RLS 与引用依赖闭包存在；完整保留恢复仍需独立验收。
- **DEV-001 进程装配、初始化与前向迁移**（专项/交付）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/platform/docs/adr/0001-modular-monolith.md:1)。API/Worker、配置与 tenant/source bootstrap 已存在；不另建服务框架。
  专项实现：[main.go](/Users/mssc/Documents/Code/ops/platform/cmd/platform-api/main.go)、[main.go](/Users/mssc/Documents/Code/ops/platform/cmd/platform-worker/main.go)、[api_runtime.go](/Users/mssc/Documents/Code/ops/platform/internal/app/api_runtime.go)、[bootstrap.go](/Users/mssc/Documents/Code/ops/platform/internal/app/bootstrap.go)；专项测试：[runtime_test.go](/Users/mssc/Documents/Code/ops/platform/internal/app/runtime_test.go)、[migrations_test.go](/Users/mssc/Documents/Code/ops/platform/test/integration/migrations_test.go)。
- **AI-002 资源历史、Finding、变更与来源步骤**（专项/交付）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/platform/docs/plans/v1.0-operational-analysis-completion.md:1)。窗口/UID/来源绑定、历史覆盖与同 Job 成功引用；Kubernetes 观察时间不自动可靠。
  专项实现：[resource_history.go](/Users/mssc/Documents/Code/ops/platform/internal/investigation/resource_history.go)、[resource_changes.go](/Users/mssc/Documents/Code/ops/platform/internal/investigation/resource_changes.go)、[fault_chain_committed.go](/Users/mssc/Documents/Code/ops/platform/internal/investigation/fault_chain_committed.go)；专项测试：[resource_changes_test.go](/Users/mssc/Documents/Code/ops/platform/internal/investigation/resource_changes_test.go)、[test_fault_chain_unittest.py](/Users/mssc/Documents/Code/ops/platform/services/investigator/tests/test_fault_chain_unittest.py)。

### 身份、租户与授权 · `authorization`

**入口：** OIDC → session/role bindings → 每次授权；/auth/callback。

**实现：** [oidc.go](/Users/mssc/Documents/Code/ops/platform/internal/auth/oidc.go)、[authorize.go](/Users/mssc/Documents/Code/ops/platform/internal/auth/authorize.go)、[tenant_tx.go](/Users/mssc/Documents/Code/ops/platform/internal/persistence/tenant_tx.go)、[contract_routes.go](/Users/mssc/Documents/Code/ops/platform/internal/httpapi/contract_routes.go)。

**测试入口：** [auth_test.go](/Users/mssc/Documents/Code/ops/platform/internal/auth/auth_test.go)、[role_isolation_test.go](/Users/mssc/Documents/Code/ops/platform/test/security/role_isolation_test.go)、[tenant_test.go](/Users/mssc/Documents/Code/ops/platform/test/integration/tenant_test.go)、[contract_routes_test.go](/Users/mssc/Documents/Code/ops/platform/internal/httpapi/contract_routes_test.go)。

**UI：** [guards.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/auth/guards.tsx)、[StepUpControl.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/auth/StepUpControl.tsx)。

**Profile：** 非虚拟化 core；来源/模型/执行能力按配置与资格启用。

**证据或边界：** [fix-status.md](/Users/mssc/Documents/Code/ops/platform/docs/reviews/project-wide-2026-10-16/fix-status.md)。

- **SEC-001 用户身份**（未单列）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1817)。operator/platform_admin 无继承，租户/集群/namespace/资源/动作/data_class 边界；应用过滤不等同 RLS。
- **SEC-002 多租户隔离**（未单列）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1823)。operator/platform_admin 无继承，租户/集群/namespace/资源/动作/data_class 边界；应用过滤不等同 RLS。
- **SEC-003 权限粒度**（未单列）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1831)。operator/platform_admin 无继承，租户/集群/namespace/资源/动作/data_class 边界；应用过滤不等同 RLS。

### 模型数据边界 · `model-safety`

**入口：** MCP 安全投影 → provider 消息 → Go Validator。

**实现：** [redaction.py](/Users/mssc/Documents/Code/ops/platform/services/investigator/src/investigator/redaction.py)、[assistant_binding.py](/Users/mssc/Documents/Code/ops/platform/services/investigator/src/investigator/assistant_binding.py)、[result.go](/Users/mssc/Documents/Code/ops/platform/internal/investigation/result.go)、[projection_nested.go](/Users/mssc/Documents/Code/ops/platform/internal/integrations/kubernetes/projection_nested.go)。

**测试入口：** [test_assistant_binding.py](/Users/mssc/Documents/Code/ops/platform/services/investigator/tests/test_assistant_binding.py)、[sp06_review_boundaries_test.go](/Users/mssc/Documents/Code/ops/platform/test/integration/sp06_review_boundaries_test.go)。

**UI：** [DiagnosisAnswer.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/features/assistant/DiagnosisAnswer.tsx)。

**Profile：** 非虚拟化 core；来源/模型/执行能力按配置与资格启用。

**证据或边界：** [sp06-investigation-runtime.md](/Users/mssc/Documents/Code/ops/platform/docs/runbooks/sp06-investigation-runtime.md)。

- **SEC-006 Prompt Injection 防护**（未单列）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1858)。不可信来源降权、敏感投影与输出引用校验存在；安全测试文件存在不等同整个威胁面已穷尽。
- **SEC-007 敏感数据控制**（未单列）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1864)。不可信来源降权、敏感投影与输出引用校验存在；安全测试文件存在不等同整个威胁面已穷尽。

### 工作负载与网络边界 · `runtime-security`

**入口：** Chart securityContext、NetworkPolicy、mTLS/工作负载证书。

**实现：** [deployments.yaml](/Users/mssc/Documents/Code/ops/platform/deploy/charts/ops-platform/templates/deployments.yaml)、[workload.go](/Users/mssc/Documents/Code/ops/platform/internal/auth/workload.go)、[workload_identity.go](/Users/mssc/Documents/Code/ops/platform/internal/app/workload_identity.go)。

**测试入口：** [workload_identity_test.go](/Users/mssc/Documents/Code/ops/platform/test/security/workload_identity_test.go)、[sp06_native_chart_test.go](/Users/mssc/Documents/Code/ops/platform/test/integration/sp06_native_chart_test.go)、[workload_identity_test.go](/Users/mssc/Documents/Code/ops/platform/test/integration/workload_identity_test.go)。

**UI：** 后台/CLI/合同路径，不另造页面。

**Profile：** 非虚拟化 core；来源/模型/执行能力按配置与资格启用。

**证据或边界：** [0026-sp06-nonvirtual-investigation.md](/Users/mssc/Documents/Code/ops/platform/docs/adr/0026-sp06-nonvirtual-investigation.md)。

- **SEC-010 Agent 网络隔离**（未单列）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1364)。受控身份、来源/模型允许目的地与隔离 Runner；生产环境需按实际发行验证。
- **SEC-008 运行时安全**（未单列）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1876)。受控身份、来源/模型允许目的地与隔离 Runner；生产环境需按实际发行验证。

### 供应链与离线交付 · `supplychain`

**入口：** opsctl bundle build/verify/import/install。

**实现：** [importer.go](/Users/mssc/Documents/Code/ops/platform/internal/bundle/importer.go)、[installer.go](/Users/mssc/Documents/Code/ops/platform/internal/bundle/installer.go)、[catalog.go](/Users/mssc/Documents/Code/ops/platform/internal/supplychain/catalog.go)、[manifest.yaml](/Users/mssc/Documents/Code/ops/platform/third_party/manifest.yaml)。

**测试入口：** [verify_test.go](/Users/mssc/Documents/Code/ops/platform/internal/bundle/verify_test.go)、[migration_admission_test.go](/Users/mssc/Documents/Code/ops/platform/internal/bundle/migration_admission_test.go)、[upstream_reuse_test.go](/Users/mssc/Documents/Code/ops/platform/test/contract/upstream_reuse_test.go)。

**UI：** 后台/CLI/合同路径，不另造页面。

**Profile：** 非虚拟化 core；来源/模型/执行能力按配置与资格启用。

**证据或边界：** [README.md](/Users/mssc/Documents/Code/ops/platform/docs/evidence/sp010203-core-final-20261001/README.md)、[fix-status.md](/Users/mssc/Documents/Code/ops/platform/docs/reviews/project-wide-2026-10-16/fix-status.md)。

- **SEC-009 供应链安全**（未单列）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1882)。历史 arm64/core 离线安装与新版迁移源码重签证据分别存在，最新全平台 Bundle/升级回滚未全验。
- **NFR-040 目标环境兼容性**（未单列）：主机制已实现；验收目标未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:2104)。历史 arm64/core 离线安装与新版迁移源码重签证据分别存在，最新全平台 Bundle/升级回滚未全验。
- **NFR-041 禁止浮动版本**（未单列）：主机制已实现；验收目标未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:2117)。历史 arm64/core 离线安装与新版迁移源码重签证据分别存在，最新全平台 Bundle/升级回滚未全验。
- **NFR-042 兼容性测试**（未单列）：主机制已实现；验收目标未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:2129)。历史 arm64/core 离线安装与新版迁移源码重签证据分别存在，最新全平台 Bundle/升级回滚未全验。
- **DEV-002 离线 Bundle、节点导入与能力检测**（专项/交付）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/platform/docs/adr/0031-generic-kubernetes-offline-installation.md:1)。原生 containerd 与库存前置、签名/准入已有；最新全平台 Bundle 与内部 Registry 不由旧安装证明。
  专项实现：[main.go](/Users/mssc/Documents/Code/ops/platform/cmd/opsctl/main.go)、[importer.go](/Users/mssc/Documents/Code/ops/platform/internal/bundle/importer.go)、[installer.go](/Users/mssc/Documents/Code/ops/platform/internal/bundle/installer.go)；专项测试：[installer_test.go](/Users/mssc/Documents/Code/ops/platform/internal/bundle/installer_test.go)、[verify_test.go](/Users/mssc/Documents/Code/ops/platform/internal/bundle/verify_test.go)。
- **FUTURE-001 内部 Registry 导入**（专项/交付）：延期/禁用/后续增强；不运行/禁用。[需求依据](/Users/mssc/Documents/Code/ops/platform/docs/adr/0031-generic-kubernetes-offline-installation.md:1)。ADR-0031 明确内部 Registry 导入未实现，原生 containerd/库存路径已实现；它是后续增强，不计本轮缺口。
  专项实现：[importer.go](/Users/mssc/Documents/Code/ops/platform/internal/bundle/importer.go)；专项测试：[importer_test.go](/Users/mssc/Documents/Code/ops/platform/internal/bundle/importer_test.go)。

### 时延、吞吐与规模目标 · `performance`

**入口：** API/采集/队列/Graph/Evidence 主路径的性能约束。

**实现：** [metrics.go](/Users/mssc/Documents/Code/ops/platform/internal/observability/metrics.go)、[ingest.go](/Users/mssc/Documents/Code/ops/platform/internal/finding/ingest.go)、[correlator.go](/Users/mssc/Documents/Code/ops/platform/internal/incident/correlator.go)、[ariadne_adapter.go](/Users/mssc/Documents/Code/ops/platform/internal/graph/ariadne_adapter.go)。

**测试入口：** [upstream_conformance_test.go](/Users/mssc/Documents/Code/ops/platform/internal/graph/upstream_conformance_test.go)、[graph_test.go](/Users/mssc/Documents/Code/ops/platform/internal/graph/graph_test.go)、[pre_sp07_concurrency_live_test.go](/Users/mssc/Documents/Code/ops/platform/test/integration/pre_sp07_concurrency_live_test.go)。

**UI：** 后台/CLI/合同路径，不另造页面。

**Profile：** 非虚拟化 core；来源/模型/执行能力按配置与资格启用。

**证据或边界：** [identity-timing-host.json](/Users/mssc/Documents/Code/ops/platform/docs/evidence/five-source-reverify-20261018/round5/identity-timing-host.json)、[README.md](/Users/mssc/Documents/Code/ops/platform/docs/evidence/sp07-20261009/README.md)。

- **NFR-001 API 可用性**（未单列）：主机制已实现；验收目标未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1896)。机制有代码不代表满足月度 99.9%、20 万对象、100 Finding/s 或 10 并发的端到端目标；按用户要求不追加远程性能测试。
- **NFR-002 Finding 接入时延**（未单列）：主机制已实现；验收目标未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1908)。机制有代码不代表满足月度 99.9%、20 万对象、100 Finding/s 或 10 并发的端到端目标；按用户要求不追加远程性能测试。
- **NFR-003 Incident 时延**（未单列）：主机制已实现；验收目标未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1918)。机制有代码不代表满足月度 99.9%、20 万对象、100 Finding/s 或 10 并发的端到端目标；按用户要求不追加远程性能测试。
- **NFR-004 Resource Graph 新鲜度**（未单列）：主机制已实现；验收目标未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1928)。机制有代码不代表满足月度 99.9%、20 万对象、100 Finding/s 或 10 并发的端到端目标；按用户要求不追加远程性能测试。
- **NFR-005 Resource Graph 查询**（未单列）：主机制已实现；验收目标未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1938)。机制有代码不代表满足月度 99.9%、20 万对象、100 Finding/s 或 10 并发的端到端目标；按用户要求不追加远程性能测试。
- **NFR-006 Evidence API**（未单列）：主机制已实现；验收目标未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1955)。机制有代码不代表满足月度 99.9%、20 万对象、100 Finding/s 或 10 并发的端到端目标；按用户要求不追加远程性能测试。
- **NFR-007 调查并发**（未单列）：主机制已实现；验收目标未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1965)。机制有代码不代表满足月度 99.9%、20 万对象、100 Finding/s 或 10 并发的端到端目标；按用户要求不追加远程性能测试。
- **NFR-010 首期验收规模**（未单列）：主机制已实现；验收目标未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1973)。机制有代码不代表满足月度 99.9%、20 万对象、100 Finding/s 或 10 并发的端到端目标；按用户要求不追加远程性能测试。
- **NFR-011 Finding 吞吐**（未单列）：主机制已实现；验收目标未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1985)。机制有代码不代表满足月度 99.9%、20 万对象、100 Finding/s 或 10 并发的端到端目标；按用户要求不追加远程性能测试。

### 持久化与无状态故障 · `reliability`

**入口：** 进程重启 → 持久数据库/Step/Outbox 恢复。

**实现：** [recovery.go](/Users/mssc/Documents/Code/ops/platform/internal/investigation/recovery.go)、[outbox.go](/Users/mssc/Documents/Code/ops/platform/internal/finding/outbox.go)、[correlator.go](/Users/mssc/Documents/Code/ops/platform/internal/incident/correlator.go)。

**测试入口：** [sp05_process_crash_test.go](/Users/mssc/Documents/Code/ops/platform/test/integration/sp05_process_crash_test.go)、[sp06_recovery_test.go](/Users/mssc/Documents/Code/ops/platform/test/integration/sp06_recovery_test.go)。

**UI：** 后台/CLI/合同路径，不另造页面。

**Profile：** 非虚拟化 core；来源/模型/执行能力按配置与资格启用。

**证据或边界：** [sp06-investigation-runtime.md](/Users/mssc/Documents/Code/ops/platform/docs/runbooks/sp06-investigation-runtime.md)。

- **NFR-020 无状态服务故障**（未单列）：主机制已实现；验收目标未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:1993)。进程故障恢复有机制；生产多实例故障不等同已有隔离场景验证。

### 数据库与归档灾备 · `disaster-recovery`

**入口：** 后续生产灾备阶段，不在当前功能开发范围。

**实现：** [database-migrations.md](/Users/mssc/Documents/Code/ops/platform/docs/runbooks/database-migrations.md)、[dev-offline-install.md](/Users/mssc/Documents/Code/ops/platform/docs/runbooks/dev-offline-install.md)。

**测试入口：** [sp04_archive_test.go](/Users/mssc/Documents/Code/ops/platform/test/integration/sp04_archive_test.go)。

**UI：** 后台/CLI/合同路径，不另造页面。

**Profile：** 非虚拟化 core；来源/模型/执行能力按配置与资格启用。

**证据或边界：** [v1.0-operational-analysis-completion.md](/Users/mssc/Documents/Code/ops/platform/docs/plans/v1.0-operational-analysis-completion.md)。

- **NFR-023 PostgreSQL 与 Evidence Archive 恢复**（未单列）：延期/禁用/后续增强；不运行/禁用。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:2015)。归档一致性测试不能证明 RPO≤15min/RTO≤4h；生产 HA/灾备独立延期。

### 来源与图降级 · `degradation`

**入口：** source health / graph fresh-partial-stale-rebuilding。

**实现：** [domain.go](/Users/mssc/Documents/Code/ops/platform/internal/evidence/domain.go)、[source_health.go](/Users/mssc/Documents/Code/ops/platform/internal/graph/source_health.go)、[response.go](/Users/mssc/Documents/Code/ops/platform/internal/graph/response.go)。

**测试入口：** [graph_test.go](/Users/mssc/Documents/Code/ops/platform/internal/graph/graph_test.go)、[sp05_source_domain_test.go](/Users/mssc/Documents/Code/ops/platform/test/integration/sp05_source_domain_test.go)。

**UI：** [CapabilityStatus.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/components/CapabilityStatus.tsx)、[DiagnosisAnswer.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/features/assistant/DiagnosisAnswer.tsx)。

**Profile：** 非虚拟化 core；来源/模型/执行能力按配置与资格启用。

**证据或边界：** [README.md](/Users/mssc/Documents/Code/ops/platform/docs/evidence/five-source-reverify-20261018/round5/README.md)。

- **NFR-031 Evidence Source 降级**（未单列）：主机制已实现；验收目标未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:2038)。来源缺失不补零，图不合格不冒充 fresh；远程错误已明确保留。
- **NFR-032 Graph 降级**（未单列）：主机制已实现；验收目标未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:2044)。来源缺失不补零，图不合格不冒充 fresh；远程错误已明确保留。

### PyRCA 禁用与降级 · `pyrca`

**入口：** 保持 disabled，不阻断确定性 RCA。

**实现：** [sp05_config.go](/Users/mssc/Documents/Code/ops/platform/internal/app/sp05_config.go)、[engine.go](/Users/mssc/Documents/Code/ops/platform/internal/rca/engine.go)。

**测试入口：** [recipe_test.go](/Users/mssc/Documents/Code/ops/platform/internal/rca/recipe_test.go)。

**UI：** 后台/CLI/合同路径，不另造页面。

**Profile：** PyRCA disabled/excluded。

**证据或边界：** [0020-sp05-pyrca-disabled-gain-decision.md](/Users/mssc/Documents/Code/ops/platform/docs/adr/0020-sp05-pyrca-disabled-gain-decision.md)。

- **NFR-033 PyRCA 降级**（未单列）：主机制已实现；不运行/禁用。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:2050)。按 ADR-0020 明确禁用 PyRCA；确定性规则不依赖排序器。不是已启用 PyRCA 的证明。

### 平台自身可观测与 Runbook · `operations`

**入口：** /healthz、/readyz、metrics；运行日志；Runbook。

**实现：** [runtime.go](/Users/mssc/Documents/Code/ops/platform/internal/observability/runtime.go)、[metrics.go](/Users/mssc/Documents/Code/ops/platform/internal/observability/metrics.go)、[tracing.go](/Users/mssc/Documents/Code/ops/platform/internal/observability/tracing.go)、[runtime_observe.go](/Users/mssc/Documents/Code/ops/platform/internal/app/runtime_observe.go)、[deployments.yaml](/Users/mssc/Documents/Code/ops/platform/deploy/charts/ops-platform/templates/deployments.yaml)、[business.go](/Users/mssc/Documents/Code/ops/platform/internal/observability/business.go)、[business_metrics.go](/Users/mssc/Documents/Code/ops/platform/internal/app/business_metrics.go)、[audit_runtime.go](/Users/mssc/Documents/Code/ops/platform/internal/app/audit_runtime.go)、[Makefile](/Users/mssc/Documents/Code/ops/platform/Makefile)、[check-test-report.py](/Users/mssc/Documents/Code/ops/platform/scripts/check-test-report.py)。

**测试入口：** [platform_metrics_port_test.go](/Users/mssc/Documents/Code/ops/platform/test/contract/platform_metrics_port_test.go)、[observability_middleware_test.go](/Users/mssc/Documents/Code/ops/platform/internal/httpapi/observability_middleware_test.go)、[business_test.go](/Users/mssc/Documents/Code/ops/platform/internal/observability/business_test.go)、[business_metrics_test.go](/Users/mssc/Documents/Code/ops/platform/internal/app/business_metrics_test.go)、[verification_targets_test.go](/Users/mssc/Documents/Code/ops/platform/test/contract/verification_targets_test.go)。

**UI：** [CapabilitiesPage.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/pages/CapabilitiesPage.tsx)。

**Profile：** 非虚拟化 core；来源/模型/执行能力按配置与资格启用。

**证据或边界：** [sp03-foundation-runtime.md](/Users/mssc/Documents/Code/ops/platform/docs/runbooks/sp03-foundation-runtime.md)、[sp04-resource-evidence-runtime.md](/Users/mssc/Documents/Code/ops/platform/docs/runbooks/sp04-resource-evidence-runtime.md)、[sp07-command-remediation.md](/Users/mssc/Documents/Code/ops/platform/docs/runbooks/sp07-command-remediation.md)、[core-workflow-closeout.md](/Users/mssc/Documents/Code/ops/platform/docs/release/core-workflow-closeout.md)、[development-tests.md](/Users/mssc/Documents/Code/ops/platform/docs/runbooks/development-tests.md)。

- **OPS-001 平台自身指标**（未单列）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:2058)。补入真实 Evidence/聚合处理时长与 Worker 有界数据库业务快照，包括队列、终态调查记录均值、工具/准入/命令状态、DB Ping。失败为 NaN，固定标签；统计口径有明确限制，不代表 SLO 或生产可用性验收。 对应 G-005。
- **OPS-002 全链路关联**（未单列）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:2074)。当前已具备健康/就绪探针，旧 PWR-008 不再作为未实现；OPS-001 全量业务指标仍有差距。
- **OPS-003 平台自身故障可发现**（未单列）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:2090)。当前已具备健康/就绪探针，旧 PWR-008 不再作为未实现；OPS-001 全量业务指标仍有差距。
- **OPS-004 Runbook**（未单列）：待核对；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:2096)。各阶段 Runbook 文件已有，但尚未逐条确认每个关键告警均绑定 Runbook。不能将目录存在等同全量满足。 对应 C-003。
- **DEV-003 本地功能检查与真实环境检查分离**（专项/交付）：主机制已实现；完整运行未验证。[需求依据](/Users/mssc/Documents/Code/ops/10-智能运维平台1.0具体编码实施方案-最终版.md:1)。Go cmd/internal/gen、Contract、Web、Python 与安全检查可独立运行，并由 test-local/check-local 聚合；真实来源、数据库集成、浏览器、原生工具、回放和容量入口分开，保留拒绝 skip/空报告/失败的门禁。 对应 G-006。
  专项实现：[Makefile](/Users/mssc/Documents/Code/ops/platform/Makefile)、[check-test-report.py](/Users/mssc/Documents/Code/ops/platform/scripts/check-test-report.py)；专项测试：[verification_targets_test.go](/Users/mssc/Documents/Code/ops/platform/test/contract/verification_targets_test.go)。

### 准确性目标与黄金数据 · `accuracy`

**入口：** resolver/关系/Inspection/RCA 黄金与回放集合。

**实现：** [resolver.go](/Users/mssc/Documents/Code/ops/platform/internal/resource/resolver.go)、[ariadne_adapter.go](/Users/mssc/Documents/Code/ops/platform/internal/graph/ariadne_adapter.go)、[precision_test.go](/Users/mssc/Documents/Code/ops/platform/internal/finding/precision_test.go)、[engine.go](/Users/mssc/Documents/Code/ops/platform/internal/rca/engine.go)。

**测试入口：** [resolver_test.go](/Users/mssc/Documents/Code/ops/platform/internal/resource/resolver_test.go)、[graph_replay_test.go](/Users/mssc/Documents/Code/ops/platform/test/contract/graph_replay_test.go)、[inspection_replay_test.go](/Users/mssc/Documents/Code/ops/platform/test/contract/inspection_replay_test.go)、[recipe_test.go](/Users/mssc/Documents/Code/ops/platform/internal/rca/recipe_test.go)。

**UI：** 后台/CLI/合同路径，不另造页面。

**Profile：** 非虚拟化 core；来源/模型/执行能力按配置与资格启用。

**证据或边界：** [sp05-golden-slices.md](/Users/mssc/Documents/Code/ops/platform/docs/runbooks/sp05-golden-slices.md)。

- **NFR-050 Entity Resolver**（未单列）：主机制已实现；验收目标未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:2145)。有固定样例与回放入口；不能用单元样例替代规定数据集上的召回率/Top-3 命中率报告。
- **NFR-051 关键 Graph Relation**（未单列）：主机制已实现；验收目标未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:2156)。有固定样例与回放入口；不能用单元样例替代规定数据集上的召回率/Top-3 命中率报告。
- **NFR-052 Deterministic Finding**（未单列）：主机制已实现；验收目标未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:2166)。有固定样例与回放入口；不能用单元样例替代规定数据集上的召回率/Top-3 命中率报告。
- **NFR-053 RCA**（未单列）：主机制已实现；验收目标未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:2175)。有固定样例与回放入口；不能用单元样例替代规定数据集上的召回率/Top-3 命中率报告。

### PVC/CSI 纵向场景 · `pvc-scenario`

**入口：** PVC Finding → 诊断图/recipe → 原生 Post-check。

**实现：** [recipe.go](/Users/mssc/Documents/Code/ops/platform/internal/rca/recipe.go)、[official.go](/Users/mssc/Documents/Code/ops/platform/internal/inspection/kubernetes/official.go)、[sp07_postcheck.go](/Users/mssc/Documents/Code/ops/platform/internal/app/sp07_postcheck.go)。

**测试入口：** [sp05_golden_vertical_test.go](/Users/mssc/Documents/Code/ops/platform/test/integration/sp05_golden_vertical_test.go)、[sp07_command_test.go](/Users/mssc/Documents/Code/ops/platform/test/integration/sp07_command_test.go)。

**UI：** [IncidentWorkspace.tsx](/Users/mssc/Documents/Code/ops/platform/web/src/pages/IncidentWorkspace.tsx)。

**Profile：** 非虚拟化 core；来源/模型/执行能力按配置与资格启用。

**证据或边界：** [README.md](/Users/mssc/Documents/Code/ops/platform/docs/evidence/sp07-20261009/README.md)、[sp05-pvc-csi-failure.md](/Users/mssc/Documents/Code/ops/platform/docs/runbooks/sp05-pvc-csi-failure.md)。

- **ACC-004 PVC/CSI 故障**（未单列）：主机制已实现；验收目标未验证。[需求依据](/Users/mssc/Documents/Code/ops/02-智能运维平台1.0产品需求规格说明书-PRD-正式版.md:2247)。历史 PVC Pending→Bound 与 Post-check 有实测；不等于本次助手 PVC 实时任务已验证。

## 状态维护和范围

后续修复应更新对应需求和 G/C 编号，附实际受影响测试结果；新源码与历史证据分开。没有真实运行回执时保留“未验证”。新的生产目标、延期能力或额外授权另行决策。

全平台清单已同步第二项核心流程补齐状态。正式 Task 9.1 的完整 Contract owner、开源复用逐项物料映射和发行兼容门禁仍需单独建设；本清单不能使 SP-09 自动通过。根目录 00～10 与旧审查/失败记录保持原样。

2026-10-19 已补充[原生场景验证与回退目标](scenario-postcheck-closeout.md)；FR-144 保持部分实现，不把服务后端就绪当成真实网络连通，也不把 PVC 原生状态当成应用读写验证。

受控探测已补充[认证回执与专用 PVC 写读](controlled-verification-closeout.md)；远程运行仍未验证，统一安装功能补齐仍是后续阶段。

统一安装[功能接线已完成](unified-installer-closeout.md)：可选 Web/助手/故障链复用签名 Bundle 与三阶段配置回执，默认禁用；Web 当前仅 amd64。前述探测阶段的后续安装开发已完成，真实安装、逐架构交付和发行验收仍未执行，需求完成统计不因此扩大。
