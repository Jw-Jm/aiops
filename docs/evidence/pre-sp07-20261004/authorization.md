# 本轮授权及边界

2026-10-04 用户明确授权 pre-sp07-nonvirtual-readiness.md R0～R6 的开发、部署、验收和修复，覆盖旧 AGENTS.md 阶段文字。正式文档 00～10 只读。完成本轮后停止，SP-07～SP-09 不实施。

ADR-0008 虚拟化延期，disabled/unverified；禁止 full Profile。ADR-0020 PyRCA disabled/excluded。ActionPlan 仅建议，无执行句柄。

专门性能基准、持续压测、容量验收和追加 P95：**用户豁免，不是 PASS**。十并发正确性、预算、超时、恢复和实际链路仍必做。

已有计划、工作树和用户内容保留。所有删除须有资源级归属证明，受保护数据不因备份而允许提前删除；实际隔离恢复验证先于数据删除。秘密与备份仅在仓库外 mode 0700 私有位置。
