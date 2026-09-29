# ADR-0008: Defer KubeVirt/CDI development and acceptance

Status: Accepted

## Decision

用户于 2026-09-28 批准：KubeVirt 和 CDI 的开发、部署及运行验收整体延期；当前先完成 SP-02 非虚拟化部分。该范围包括 core 离线安装、非虚拟化图/证据基线和非虚拟化 Inspection/Incident/硬件复用裁决。

延期分项不作为独立非虚拟化任务的前置阻断。原方案的最终虚拟化需求仍保留，延期不构成需求删除或验收通过。

KubeVirt/CDI 保持 candidate；core resolved Profile 对两者保持 disabled，运行兼容性保持 unverified。两组件不进入 core Bundle。版本矩阵支持和控制面健康均不能证明 VM 可运行。

## Context and rationale

已有 PoC 中控制面和 DataVolume 成功未能产生 Running VMI，记录的失败涉及 ARM64 CPU 模拟和 Pod 网关。当前本机环境尚不能完成 VM 生命周期验收，详见[原 PoC 证据](../poc/kubevirt-cdi-orbstack.md)。core 与通用非虚拟化能力可以独立开发和验证。

## Alternatives considered

- 等待 VM 运行环境解决后再推进所有任务：会阻断独立 core 工作。
- 本轮继续开发虚拟化功能，仅延期 live 验收：与用户要求先完成非虚拟化范围不符。
- 移除最终虚拟化需求或降低其验收条件：不采用，最终需求和准入条件保留。

## Consequences

非虚拟化部分按其实际门禁独立验收与提交。范围内条件全部满足后可以报告“SP-02 非虚拟化部分通过”；KubeVirt/CDI 分项仍为“延期、未验证”，原 SP-02 完整验收不能据此宣称通过。

组件准入、合格 Bundle、公网出口隔离、空缓存安装和干净重装等非虚拟化门禁仍须满足。代码检查、Fixture 或跳过的 live 测试均不能替代这些实际证据。

## Implementation boundaries

- Task 2.6 整体延期；Task 2.8 的新增虚拟化关系适配，以及 Task 2.9 的 KubeVirt 指标/规则、must-gather、KubeVirt MCP toolset 实现和准入延期。
- 后续 VM/VMI/DataVolume 采集、诊断、工具和界面，以及 virtualization/full Profile 的实施与验收延期。
- 保留已有源码、Schema、Fixture、版本矩阵及失败证据；不新增虚拟化功能或运行 live VM PoC。
- core 发现可只读记录已有虚拟化资源，并维护必要的 disabled 合同；不安装、升级或重新部署 KubeVirt/CDI。
- 所有开发入口遵循仓库 [AGENTS.md](../../AGENTS.md) 的本轮边界；共用代码中的虚拟化能力隔离，不作为恢复开发的理由。

## Rollback conditions

只有用户明确恢复虚拟化开发阶段后，才重新开展 KubeVirt/CDI 开发。组件升级、发现已有 CRD、环境变化或 Fixture 通过不自动解除延期。

恢复后的运行准入须在明确选择的环境中验证虚拟化和网络条件，锁定兼容 KubeVirt/CDI 版本及架构 digest，并通过 VM/VMI/DataVolume 生命周期。开发环境结果不构成生产兼容性或 HA 验收。
