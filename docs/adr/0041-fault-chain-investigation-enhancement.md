# ADR-0041：故障链调查增强

日期：2026-10-14。状态：Accepted（用户明确要求“完成开发工作”）。

按 [详细方案](../plans/fault-chain-investigation-enhancement.md)完成非虚拟化故障链调查增强：固定 L4/L7 统计、有限上下游关联、窗口比较、证据/冲突/缺口联动与不依赖模型请求的证据收集。

首期范围为同一授权 namespace 内、身份连续的 Pod、已验证 HTTP 与 L4 观测、一跳关系。复用既有 analytics、Evidence、Graph、MCP、Job/Step/租约、官方 SDK 和助手界面。统计、采样、身份、时间与来源未验证时按具体能力拒绝或降级，不扩大为根因确认。

开发和真实运行验证沿用 192.168.0.63 的独立开发 namespace/来源/身份；不操作本机 OrbStack，不修改 core/final 配置，不自动进入 Bundle 或生产发布。中文模型质量、生产来源及发行准入分别报告。

根目录 00～10 保持只读。ADR-0008 的 KubeVirt/CDI 延期及 ADR-0040 的 Trace 禁用、D1 元数据和人工处置边界继续有效。
