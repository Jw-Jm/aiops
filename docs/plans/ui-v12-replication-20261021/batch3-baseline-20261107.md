# 第三批基线：22 菜单真实功能与 HTML 1:1 核验起点（P0-01 / P0-02）

2026-11-07。本文件是 tasks.csv P0-01/P0-02 的落盘产出，为后续逐菜单收口与 1:1 视觉核验提供固定基准。不做半成品页面：每个菜单按"真实接入 → 六项核验 → 1:1 对比"完整收口后才标记完成。

## P0-01 固定基准

| 项 | 值 |
|---|---|
| 代码基线 | 本文档提交时的 main（生产 current 为 `6f3cf535f2f4b48e0e49fbd6cb1c22d47ea383b9`，schema 74） |
| 设计包 | `/opt/ops-assistant-dev-20261012/ui-handoff/ops-platform-v1.2-20261020/`（manifest + shared/platform.css + slices/SL-001..SL-458[+SPEC.md/index.html/replay.html/spec.json] + previews PNG + source/prototype.html） |
| 设计包校验 | baseline.json：source_sha256 `c6651e1d…`，22 路由 / 458 切片 / 39 任务 / 138 合同操作（既有 62） |
| 运行入口 | https://192.168.0.63:30471（ops.admin，operator+platform_admin） |
| 1:1 核验画布 | 1440×960 CSS px、缩放 100%、与参考同一浏览器与系统字体（SPEC 通用要求） |

## P0-02 逐切片核实结论（458 项全量统计）

- **direct 355**：正式前端已有对应实现（多为既有验收过的功能面）
- **adapt 26**：已实现但与原型有视觉/交互偏差，需按切片 SPEC 对照修正
- **missing 77**：未实现，分布如下（任务、路由、数量）：

| 任务 | 路由 | 数量 | 内容 |
|---|---|---|---|
| P2-02 | /overview、/resources、/settings/permissions | 7 | 登录/改密/身份查看切片（内置账号、登录失败态） |
| P2-04 | /settings/permissions | 12 | 用户与权限完整态（重置密码、创建成功等） |
| P4-05 | /events/incidents/* | 6 | 故障详情·调查与证据（发起智能调查对话框、证据抽屉）——与第二批页面侧同源 |
| P5-03 | /access/models | 9 | 模型服务页（接入/编辑/测试/停用全流程） |
| P6-01/02 | /jobs/commands | 9 | 新建人工执行、作业模板（使用/修订） |
| P6-03/04 | /jobs/inspections | 16 | 巡检任务页（运行/报告/计划/固定报告） |
| P7-04/05 | /knowledge | 16 | 知识库（版本、检索、AI 固定引用） |
| P7-06 | /settings/deployment | 2 | 部署信息运行状态检查 |

### 当前路由实现状态（router.tsx 实测）

- 真实页面：overview、assistant、resources(+detail/topology)、events/alerts、events/incidents(+/:id)、observe/logs|metrics|network|hardware、jobs/commands、access/clusters|sources|readiness、settings/permissions|policies|execution|deployment|audit(+assistant)、account/security
- **unavailable 占位**：/access/models、/jobs/inspections、/knowledge/cases、/knowledge/runbooks

### 收口顺序建议（依赖+价值）

1. P2-02/P2-04（账号与权限完整态）——小缺口，先行
2. P5-03 模型服务（呼应模型连通性；MiniMax 充值后可直接在页面接入）
3. P4-05 调查与证据（依赖第二批智能调查）
4. P6-01..04（命令执行增强+巡检）——依赖受控命令目标（方案已定：ops-cmdtest 专用 namespace+pause pod）
5. P7-04/05 知识库、P7-06 部署信息
6. 每项收口即做对应切片 1:1 对比（previews PNG 基准在位）

## HTML 1:1 核验方法（P1-04 基线已具备）

- 每切片：同一浏览器开 `slices/SL-xxx/index.html`（定格参考）与正式页，1440×960、100% 缩放、同字体，逐项对照布局/间距/色值/图标/滚动，截图存 `docs/evidence/ui-1to1/<SL-ID>/`（参考图+实测图+差异说明）
- 交互态：以 `replay.html` + SPEC 的按钮/参数清单驱动，覆盖弹窗、抽屉、空态、错误、权限不足、取消
- 已批准偏差（如账号简化）单独登记，不混入差异
