# ADR-0032：SP-07 非虚拟化人工命令处置授权

日期：2026-10-09。状态：Accepted（阶段授权；实现与实际验收待完成）。

用户明确授权实施正式方案 Task 7.1～7.6、§14.7，覆盖 AGENTS.md、ADR0026、ADR0028 中不得进入 SP-07 的旧阶段限制。根目录 00～10 正式文档只读；ADR0003 的人工输入真实 Bash、风险确认、权限及 step-up、持久执行、审计及 Post-check 模型继续适用。没有授权 SP-08/SP-09。

ADR0008 的 KubeVirt/CDI/VM 延期 disabled/unverified、ADR0020 的 PyRCA disabled/excluded 继续有效。不得新增 Agent Runtime、复制 HolmesGPT 循环或 LLM client；模型、Agent 和后台调查永远没有执行权限或执行凭据。

真实 Ollama llama3.1:8b-16k 仅作有限串行功能测试；模型功能与 RCA 准确率分别记录。十个并发调查、模型并发/吞吐/P95/持续压测/容量及相关扩容为 USER_WAIVED_NOT_PASS。不得修改正式预算、超时、阈值或安全策略。重放、单次确认、幂等冲突、原子预算、撤权、Lease/Worker 接管、超时、重复投递及 execution_unknown 仍须正确性验证。

四类 Profile 必须落实目标权限、固定物料、网络和短期凭据；高权限使用真实 Keycloak step-up。真实 Kubernetes 与隔离 Linux SSH 正向验证不可用 Mock 代替。新物料必须在首次真实使用前准入。ARM64 实际部署与业务验收、amd64 构建兼容分别记录，未部署的 amd64 为 unverified。历史 Transit 恢复、完整 amd64 第三方离线包、DeepFlow 等既有缺口不自动扩大为本轮前置；路径实际依赖缺失能力时只阻断该路径。

工作基线 main@ba5e1754335178de138052b757fa8a50a814a128，fetch 后 origin/main 一致，初始工作树干净。实际 Git 根为 platform；工作分支 sp07/nonvirtual-command-remediation。证据写入 docs/evidence/sp07-20261009，秘密与恢复材料在仓库外。现有开发安装与受保护数据保留，不全冷装、reset/clean、全局 prune 或 force push。

完成实现及必要验收后，启动未参与实现的独立只读审核，修复确认缺陷并复核至限定范围 PASS，再提交、合入 main、普通推送并核对远端。若真实门禁缺失，应如实记录未完成，不把部分交付记作 PASS。

公共 Contract 兼容：CommandExecution/v1 响应保留冻结字段，新增绑定、归档预览和恢复状态元数据使用 extensions。请求新增 incidentId、executionProfileVersion、executionOptions 为 v1 可选加法；旧消费者省略时只能从同一主体已有的不可变确认恢复精确值，不重新选择版本、不使用服务端默认值。显式值必须匹配完整确认摘要。新的风险评估请求仍要求完整值；风险确认绑定版本为 execution-request/v1。ExecutionProfile/v2 与 HostOnboarding/v1 使用独立版本 Schema；OpenAPI/Go/TypeScript/SQL 生成物同步更新。
