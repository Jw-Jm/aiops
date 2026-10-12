# SP-08 页面开发阶段收尾（2026-10-12）

按用户最新要求完成当前修复组后停止本轮页面开发，提交阶段源码。当前访问入口：https://192.168.0.63:18443/ （增量预览）。30443 正式发行部署尚未完成。登录凭据继续保存在仓库外。

## 修复与复测

- 普通运维工作流：中文导航、故障事实及下一步入口；完整 ID、技术信息与深层 JSON 按需展开；真实证据详情直接打开。统一设计 token、状态文字说明与 UTC+08:00 时间展示。
- 当前修复组：凭据 epoch 阻止旧 401/403 影响新会话；时间筛选进入公共 API 并保留 URL/cursor；调查输入摘要使用有界白名单并省略 prompt、原始查询及凭据；历史步骤明确未记录；证据按钮定位；无集群授权范围区别于空数据。
- web-full-r38：62 项组件/边界测试与 5 项 Node 客户端 Contract 测试通过。web-build-r38：TypeScript strict 与 Vite 构建通过。
- closeout-go-r2：调查及工具、HTTP API、公共 Contract 测试通过。早先 closeout-go-r1 中错误的包路径导致命令失败，保留记录；不计为通过。
- closeout-browser-r1：7 项实际浏览器测试通过，包含真实管理草稿 409 恢复、管理页及来源状态、1440/1280/390 布局与键盘；其中 3 项为明确标注 Fixture，覆盖图谱循环/孤立点/TTL、SSE 过期游标/去重/有界输出/不确定执行、四种建议匹配状态，不能替代真实部署业务验收。
- 先前 readability-browser-r9：真实 OIDC、角色隔离与撤权、Incident 区域、Evidence、两类 Kubernetes 只读命令及实际后检查、审计分页等 10 项通过。实际后检查为 completed/not_resolved，命令执行成功不表示故障恢复。
- 实际模型调用成功及 Last-Event-ID 续传已有独立回执；MODEL_UNAVAILABLE 故障注入与恢复已有实际回执。重开持久失败记录的复验与新模型调用分别标记。小模型存在“bounded explanation”无效结论，根因准确率未验收。
- 真实有限时间范围审计链已验证；超预算范围保持 partial。

## 状态与限制

本轮阶段提交不宣称 Task 8.1～8.7 完整验收通过。正式发行构建、最终许可证/镜像准入回执、统一部署及正式发行后完整业务验收仍未完成。现有镜像和 18443 静态资源为可追溯增量版本。

全仓 go test 的既有失败包括未配置隔离 PostgreSQL/live 环境及 Python 版本 PATH 错误；具体失败日志保留。针对本轮受影响路径使用锁定工具链复测，不以跳过门禁代替通过。

KubeVirt/CDI/VM 保持 disabled/unverified 延期；无 BMC 的硬件实际验收未完成；未实施 SP-09、生产 HA 或灾备。原有数据库、PVC、原有身份、注册及归档/恢复材料保留。此前未提交 R12 源码与本轮源码一并保留，提交描述明确区分基线与页面阶段。

## 最终阶段回执

输入摘要实际隔离 PostgreSQL 迁移及持久化测试 input-summary-integration-r3 PASS，零值与敏感字段过滤通过。r1 端口转发失败、r2 JSONB 空格断言失败保留；r3 使用解析值判断，未降低断言。受审计保护的隔离测试数据库保留于 /data/ops-sp08-20261011/cache/summary-test-pg。

closeout-current-r3 实际筛选时区与 URL/API 参数、历史输入缺失说明、调查步骤证据详情定位 PASS。closeout-live-r1 在 API6/worker7 上实际 OIDC/Incident、两类 Kubernetes 命令与后检查、密码 OTP、角色隔离、撤权确认失效 7 PASS。临时 QA 身份已停用并退出会话，记录保留，原身份未改。

完整生成复现检查在隔离副本触发重复依赖安装后按本轮停止要求中断，没有完成比对，明确 NOT PASS；正式发行门禁保留。独立只读阶段审核确认无新提交阻断，但未重新验收全部既有 R12 改动。

源码 whitespace 检查通过；完整 staged whitespace 提示来自保持原样的第三方许可证/源码补丁及原始测试日志，不改写出版物和历史证据。另一本地未提交的 docs/plans/ai-data-query-and-conversational-troubleshooting.md 属本轮范围外，保留在工作树，不纳入本阶段提交。
