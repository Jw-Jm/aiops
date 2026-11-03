# r28：真实原生业务Worker链路（2026-10-27）

已完成本任务环境的**真实审计问数业务链路**。GPT直接实施，无CodeBuddy；main/082f87d66c5b仍有r23～r28未提交修改，未重置、提交或推送。

## 查看结果

[审计问数](https://192.168.0.63:30471/settings/audit/assistant)；从“系统管理 → 操作审计 → 审计问数”进入。当前管理员只有platform_admin，没有operator；资源API仍403。首条完成会话01a17b2e-7810-7c90-9875-76be31a09542，刷新仍显示服务端真实查询事实。数据回答使用服务端验证的数值和引用，不直接展示模型自由计算的摘要。

## 已完成的实际链路

原生管理员真实登录/CSRF/幂等 → 独立签名只读策略（原生密码+TOTP分别验证创建、发布、启用）→ 会话201/任务202 → 独立Go Worker领取真实数据库任务 → Kubernetes600秒投射身份登录真实OpenBao Transit → 两个登记的签名Context → 真实工作负载CSR/PKI与双向TLS → 原有锁定HolmesGPT循环 → DeepSeek官方OpenAI API → get_assistant_context/describe_analysis_dataset/execute_analysis_query → 实际审计PostgreSQL统计与快照 → 结果/事实持久化 → 浏览器读取、SSE和刷新回显。

真实运行中取消通过：模型Step已running，HTTP取消推进epoch1→2，15秒后平台终态/部分结果未被迟到模型回答覆盖。真实Worker及调查器在模型调用中崩溃，重建两个Pod，未修改租约SQL；实际租约到期后epoch1→2，同一任务恢复已提交读并完成查询。真实撤权通过：给本任务member临时独立管理员授权、提前完成精确撤权证明，在模型Step运行中撤销；用户读取401/403，Worker最终AUTHORIZATION_REVOKED且无结果，原管理员仍可读取管理API，临时授权已撤销。具体Job/Step/context/generation、模型响应ID和usage见runtime/final-runtime.json。

## 代码及验证

- 新增明确的Profile v2兼容材料，按用户“deepseek就当作是内部模型”作为本任务受信模型直接使用现有流程。只允许已实测的精确DeepSeek地址和deepseek-flash，仍固定每个Job的Profile引用、上下文/输出/请求预算及零自动重试。托管别名与实际计费如实记录，不伪造自托管权重、许可证或零费用；modelCostMicros仍是未定价历史字段，不是账单0。v1摘要与旧配置保持兼容。模型ID别名由供应商管理，Profile冻结请求路由和已声明版本，不声称冻结供应商权重。
- 修复查询工具误用浏览器/legacy授权：复用CheckAssistantJobTx，在查询事务内锁定复检任务持久原生来源/账号/版本、冻结grant摘要及范围。真实PG回归覆盖无浏览器调用和撤权拒绝。
- 修复JSON模式未传递Schema：在现有Holmes初始/一次修复提示中传准确输出Schema；本地验证与引用门禁保留，未增加Agent循环。
- 补操作审计页的审计问数入口与管理员守卫路由，复用AssistantPage，双角色账户从该入口默认审计域；不自动授予operator。

30项远程Python相关测试、Go modelprofile/httpapi/app/contract、真实PG查询回归、181 Vitest+6 SSE与TypeScript/Vite构建通过。模型回答质量与完整视觉另验，截图仅作功能回显证据。

## 当前运行和恢复

沿用目录 `/data/ops-v12-native-visual-r24-20261026` 和namespace `ops-native-v12-r24-20261026`；PG15454/ui_v12_r24_native/schema70不变。API为r28-worker/ops-api-query、manifest api-pod-query.json；Worker为r28-worker/ops-worker、manifest worker-pod-business.json；调查器使用同一已存在精确镜像，只读挂source-4/services/investigator/src，manifest investigator-pod-query.json。Web为ops-v12-native-r28-web-r6/web-r6，旧r4/r5容器停止保留。任务Secret r28-model只挂调查器，API/Worker没有模型凭据；scope私钥只挂API，Worker只读公钥。策略信任只向本任务原有公钥清单追加本任务签名者，没有修改共享信任或服务。

`source-final.json`绑定完整当前源码快照ops-r28-source-final.tgz、API/Worker哈希及来源；远程r28-worker/source-final保存同版本源码，不代表新签名OCI/Bundle。恢复先核对任务namespace/UID/文件哈希，只用上述任务manifest恢复；不要重跑prepare、Bao init、首租户或已完成策略发布脚本，不恢复旧数据库/身份快照。DeepSeek Key仅原任务private/deepseek-api-key与命名空间Secret，文档/代码/报告均无Key。

## 保留失败与剩余

首次真实任务保留partial/MODEL_OUTPUT_INVALID及SOURCE_DEGRADED；第二次在调查器滚动期间发起，保留EOF/INVESTIGATOR_FAILURE，显式等待Ready后第三次通过。取消/接管通过后的测试因错误字段grants中断，保留原失败并只接续撤权；没有重跑已完成场景。首次UI脚本错误期待模型摘要，改为核对既定服务端数值展示。Python安全tar拒绝venv绝对链接、缺测试依赖及修正记录保留。

本轮完成实际审计data_query链路；资源/日志等其他来源、P1/P2全部视觉与完整状态、正式不可变Bundle/离线安装/切换、P3 SL-049和P4～P8仍按原清单推进。未恢复虚拟化、Trace、生产发布；未提升458行产品六维状态，P2-05继续in_progress。

最终核对：临时管理员授权已revoked，仅保留原有效管理员1名；6991个最终源码文件、API/Worker/归档哈希及已部署Python来源一致。458状态/177合同结构与六维计数保持，go list和差异空白检查通过。
