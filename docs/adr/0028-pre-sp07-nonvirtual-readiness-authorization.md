# ADR-0028：进入 SP-07 前的非虚拟化前置实施

状态：Accepted（授权与实施边界；验收仍在进行）。日期：2026-10-04。

用户明确授权实施 [R0～R6 清单](../plans/pre-sp07-nonvirtual-readiness.md)，覆盖旧 AGENTS.md 的阶段限制。根目录 00～10 继续只读。实际基线、阶段决定、失败与运行证据记录在 [本轮证据目录](../evidence/pre-sp07-20261004/)。本轮完成后停止，不自动进入 SP-07。

ADR-0008 的 KubeVirt/CDI/VM 延期与 ADR-0020 的 PyRCA disabled/excluded 继续有效。DeepFlow bundled 与 Metrics-server 的实际正向交付属于本轮必做项。物理 BMC/DIMM/NIC、生产 HA 等条件能力按现场证明表达，不以 Fixture 或本机结果代替。

安装器新增独立的 `installation-business-values/v1` 输入，保持冻结 DeploymentProfile/v1。当前安装必须显式启用 SP-04～SP-06，并提供 tenant、Policy、模型、源身份、凭据引用、预算与精确私网范围。只允许认证 Bundle 的 investigator 镜像。旧基础安装通过显式 `--foundation-only` 保留，其通过不构成当前业务交付通过。

首次 Tenant 使用一次性受控 `opsctl bootstrap first-tenant`：独立 bootstrap 数据库身份、真实 HTTPS OIDC、ops-api audience、匹配主体与 tenant membership、有效且最近一小时的 LoA-2 身份。空库检查、Tenant、初始管理 RoleBinding 与 Audit 在同一事务完成。初始管理身份不隐式取得工作负载范围；后续 Tenant/RoleBinding/Source/Registry 配置使用正式 API。并发首次初始化最多一名成功；已有 Tenant 时拒绝重新 bootstrap。

API/Worker 新增显式的 `openbao-kubernetes` 运行身份模式，复用既有进程内 CertReloader。独立预置 workload CA 约束初始签发与后续续期，签发方漂移失败关闭；CRL 由该生命周期获取，不依赖测试刷新线程。Context Transit 客户端按租期读取投影 token 重新认证，串行化 token 更新与签名请求，认证失败不复用旧 token。investigator 与正式安装的完整持续身份启用仍须后续真实部署门禁，当前单项 PKI 通过不能替代全依赖冷装。

调查继续复用锁定 HolmesGPT 与官方扩展点，Go Validator 保持最终校验。ActionPlan 仅建议。本轮不新增执行业务、执行句柄、Runner、Ansible 执行、SSH 执行、kubectl exec、通用 SQL 或任意 URL Fetch 工具。

专门性能基准、持续压测、容量验收、追加 P95 为**用户豁免**，不得记为 PASS。关系正确性、有界查询、预算、超时、恢复及十个并发调查与主链路仍须实际验证。

清理必须证明逐资源归属、消费者和保留义务，实际恢复先于数据删除；备份不解除 Compliance retention、Legal Hold 或依赖保护。2026-10-04 查明历史 Docker OpenBao 使用 inmem，当前有效恢复身份查询历史 `evidence-archive` 返回 404，旧恢复身份返回 403。用户确认没有对应恢复材料。27 条历史 Evidence 的原 Transit v1 解密恢复因此阻断；原数据库、对象和底层存储保留。隔离数据库字节一致恢复不构成完整 Evidence/Audit 恢复通过，也不授权删除原件。详见 [恢复证明](../evidence/pre-sp07-20261004/postgres-isolated-restore.json)与[账本](../evidence/pre-sp07-20261004/task-ledger.json)。

只有 R0～R6 必要门禁全部满足且最终完整独立只读审核明确 PASS 后，才提交、合入 main 并推送。本轮尚未达到该条件。
