# 核心流程功能补齐记录

日期：2026-10-18。对应全平台工作建议的第二项，仅处理功能清单 G-001～G-005；G-006 属于第三项检查入口拆分，未在本轮修改。

## 已开发

- G-001：总览增加 active Finding、Open/Critical Incident、我的运行中 Investigation 计数；Incident 与其关联 Finding 都受当前租户/集群/namespace/资源范围约束。硬件服务器数来自已观测 PhysicalServer，缺来源保持未知。
- G-002：增加 Hardware/Kubernetes/Network/Storage 资源领域与健康分布、携带领域/健康条件的列表链接和筛选。汇总在授权过滤后、分页前计算；`includeDomains` 显式启用扩展，签名游标绑定领域参数。网络资源健康不代表链路质量。
- G-003：首页展示最多五条 Open 且 critical/warning 的 Incident，按严重度、开始时间、ID 稳定排序，链接故障与资源详情。没有真实开始时间则不推算持续时长；只有通过当前引用授权的冻结 RCA 才提供影响摘要，冻结结果不代表当前状态。
- G-004：完成适用范围披露，风险确认前、Execution 与后检查均提供同一声明。保留 Node/PVC 原生可信判据，其余目标明确不支持并保持 inconclusive。未用退出码替代修复判据，未扩展权限；FR-144 全目标自动检查仍为部分实现。详见 SP-07 Runbook。
- G-005：在实际主路径记录 Evidence 查询/读取与 Incident 聚合处理时长；Worker 现有循环有界采样队列、调查时长、工具/准入/命令状态和数据库可达性。固定标签，失败值为 NaN，语义详见 SP-03 Runbook。

## 本地验证

- Go：受影响 HTTP、Action、Observability、Incident、App 单元测试，以及 Contract/Security 测试通过。Graph 全包通过；最终领域/游标/扩展合同专项通过。
- Web：23 个测试文件、77 项组件测试及 5 项 SSE 协议测试通过；TypeScript 检查通过。
- Go 全仓构建、受影响包与 Integration 的 vet、生成物一致性检查通过。
- 新增总览数据库集成用例覆盖当前主体任务、其他主体/越界 namespace 排除、已解决故障过滤及关联 Finding 撤权。该用例仅编译，未运行 PostgreSQL；SQL 真实执行和真实页面闭环仍未验证。

本轮没有远程访问、部署、调优或追加性能测试。历史联合失败、页面/新同轮长上下文 NOT_RUN、Node/PVC 真实助手授权限制与人工模型质量 pending 继续保留，不因本地测试通过而改为 PASS。KubeVirt/CDI、Trace、生产 HA/灾备和正式文档 00～10 均未恢复或修改。
