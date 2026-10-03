# ADR-0021：SP-05 固定 Recipe 查询组合

- 状态：实施中；需要纵向、兼容性及最终独立审核，不表示 SP-05 验收通过。
- 日期：2026-10-02
- 依据：ADR-0017 的 SP-04 有界查询；Task 5.5–5.7 的非虚拟化诊断链路。

失败测试发现初版 Recipe 的 graphDepth=3 不符合现有 Graph.Query 的最大两层约束，实际 Worker 返回退化结果。保留两层公共查询上限，不扩展通用遍历内核。

三份尚未交付的 recipe-registry/v2 声明均设 graphDepth=2。PVC/CSI 和 Node 使用 single-query/v1。DIMM 使用 dimm-hosted-pods/v1：第一次查询 DIMM 的两层 impact；只选择已返回的 component_of(DIMM, PhysicalServer)、hosts(PhysicalServer, Node) 固定形状；对这些 Node 各调用一次同一 Ariadne/ontology 内核的一层 impact。组合共享 3 秒上下文、200 个节点和 400 条边总预算。第二次查询使用第一次的 graphRevision；版本变化、预算不足、撤权、源退化和超时不能返回 confirmed。每次内核查询仍不超过两层。RCA 返回 graphPlan，impact.budgets.maxDepth 表示单次内核深度而非组合路径长度。

component_of、hosts 的物理边方向与原生 Kubernetes dependent→dependency 引用不同：故障传播沿 component_of、hosts 正向，依赖沿反向。仅调整这两种明确关系的投影方向，neighbors 的 in/out 仍表示原始边方向。保留 contains 关系，并为 DIMM 增加有来源、时间和 TTL 的 component_of。

初版 DIMM 黄金预期把 Pod 写为直接影响，这是不符合已有 SP-04 一跳 direct 定义的手工规格错误。保留 v1 和失败证据；新的 v2 预期必须在新增纵向实现之前冻结为：直接影响 PhysicalServer，间接影响 Node、Pod。新预期依据既有关系与 direct/indirect 定义人工编写，不从运行结果生成。硬件仍限定协议 Fixture，不能宣称真实 DIMM/BMC 运行验收。

性能专项保持用户豁免；本决定不提高阈值、不恢复虚拟化、不解除 PyRCA disabled。
