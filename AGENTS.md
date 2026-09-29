# 本轮开发边界：SP-02 非虚拟化部分

本规则适用于整个 `platform/` 仓库。用户于 2026-09-28 批准 KubeVirt/CDI 开发与验收整体延期；正式决定见 [ADR-0008](docs/adr/0008-defer-kubevirt-cdi-development.md)。

## 当前范围

- 完成 core Profile、核心组件准入、Bundle、离线导入安装、干净重装和健康/能力检查。
- 完成 Task 2.8 的非虚拟化图、诊断与证据基线，以及 Task 2.9 的非虚拟化复用裁决。
- 按最终方案解决这些任务的阻碍并完成实际验证；不因 Task 2.6 延期而停止独立的非虚拟化工作。
- 本轮范围为 SP-02；后续阶段由用户另行指定。

## 延期范围

- Task 2.6：KubeVirt/CDI PoC、安装、升级、重新部署及 VM/VMI/DataVolume 生命周期。
- Task 2.8 的新增虚拟化关系适配；Task 2.9 的 KubeVirt 指标/告警、must-gather 和 KubeVirt MCP toolset 实现与准入。
- 后续阶段的 VM/VMI/DataVolume 采集、诊断、工具与界面，以及 `virtualization/full` Profile 的实施和验收。
- 本轮不设置 `OPS_KUBEVIRT_ORBSTACK_POC=1`，不创建或修改集群中的 KubeVirt/CDI CRD、Operator、VM、VMI 或 DataVolume。

## 保留和禁用约定

- 保留已有源码、Schema、Fixture、版本矩阵和失败证据；不为使检查通过而删除这些内容或把失败改为成功。
- core Profile 中 `kubevirt`、`cdi` 保持 `mode: disabled`，运行兼容性保持 `unverified`；两组件保持 `candidate`，不进入 core Bundle。
- 虚拟化特有能力保持 disabled，并注明开发延期、运行未验证；已有 Fixture 或控制面测试通过不代表 live VM 验收通过。
- core 发现流程可以只读记录现有虚拟化资源；只允许保障 core 禁用状态所需的最小配置/合同维护，不扩展虚拟化功能。
- 共用代码存在虚拟化依赖时，隔离该能力并继续非虚拟化路径，不通过实现虚拟化功能来解除本轮阻碍。

## 验收和恢复

- 非虚拟化组件仍须满足精确来源、许可证、依赖闭包和真实 PoC/Fixture 门禁；candidate 不得进入 Bundle。
- core 离线验收仍须有合格 Bundle、有效公网出口阻断、空镜像缓存安装及 release 范围内的干净重装证据；`make check` 或跳过的 live 测试不能替代这些证据。
- 范围内门禁全部满足后，可以报告“SP-02 非虚拟化部分通过”；同时保留“KubeVirt/CDI 延期、未验证”，不宣称原 SP-02 完整验收通过。
- 延期持续到用户明确恢复虚拟化开发阶段。恢复后的运行准入另需实际环境、兼容版本和 VM/VMI/DataVolume 生命周期证据。
