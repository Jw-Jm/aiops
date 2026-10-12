# ADR-0034：SP-08 非虚拟化 Web 产品界面授权

日期：2026-10-11。状态：Accepted（阶段授权；实现及实际验收待完成）。

用户明确授权正式方案 Task 8.1～8.7，覆盖 AGENTS.md、ADR0026/0028/0032 的旧阶段限制。根目录 00～10 正式文档只读。ADR0002 双角色无继承、ADR0003 人工输入与本人确认、ADR0007 生成公共客户端持续有效。

KubeVirt/CDI/VM 依 ADR0008 保持 deferred/disabled/unverified；无 BMC 的硬件真实验收待完成；PyRCA disabled/excluded。没有授权 SP-09、生产 HA、灾备或容量扩容。Fixture 不替代真实验收；功能成功不等于 RCA 准确率通过。

目标为 192.168.0.63 amd64，现有 R12 部署命名空间 ops-amd64-final-20261010，工作副本 /data/ops-amd64-final-20261010/working-runtime/platform。新增修改先在目标机完成及测试，然后同步本地。保留既有修改、失败证据、数据库、卷、身份、注册配置与归档恢复材料。构建、缓存、大文件使用 /data/ops-sp08-20261011 独立目录；秘密保持仓库外。

React/TypeScript strict/Vite/Ant Design/React Router/TanStack Query，OIDC PKCE 与内存 token，non-root Nginx 同源 API/SSE。界面依真实公共 Contract 实现；正式数据缺失如实展示，不制造计数。依赖来源、版本/摘要及许可证准入在正式发行前完成。

逐模块及整体进行代码、浏览器视觉交互、真实业务复审，记录现象、影响、修复与复测。沿用独立只读审核规则：最终由未参与实现的审核者复审全部交付，确认问题修复后复核。必要门禁及最终复审通过之前不得宣称完成。阶段结束集中构建正式发行、统一部署及端到端复验。
