# UI 审查（2026-10-16）

> **重要声明：本轮未进行任何浏览器运行验证。** 未启动 dev server、未安装依赖、未访问 `192.168.0.63`、未做截图或交互操作。以下全部结论为**静态源码判断**（含前后端契约比对）；凡涉及渲染效果、焦点行为、对比度实测、canvas 图谱、终端组件、窄屏布局的结论一律标注"动态未验证"。

## 一、页面与路由清单（由 `web/src/app/router.tsx` 真实路由建立）

| 路由 | 组件 | 守卫/角色 | 已实现状态 | 备注 |
|---|---|---|---|---|
| `/auth/callback` | `Callback` | 公开 | error（Alert）/loading（Spin） | OIDC 回调 |
| `/auth/silent-callback` | `SilentCallback` | 公开 | 无 UI | 静默续期 |
| `/` | → `/overview` | 登录 + AppShell | — | — |
| `/overview` | `OverviewPage` | 登录 | loading / error / 无集群范围(`noScope`) / 部分失败(`resourceError`) | — |
| `/resources` | `ResourcesPage` | 登录 | loading / error / empty / `noScope` | — |
| `/resources/detail` | `ResourceDetailPage` | 登录 | loading / error / empty（缺 canonicalId） | tab=detail/graph |
| `/findings` | `FindingsPage` | 登录 | loading / error / empty / `noScope` | 见 PWR-020 |
| `/incidents` | `FindingsPage`（incidents 模式） | 登录 | 同上 | — |
| `/incidents/:incidentId` | `IncidentWorkspace` | 登录 | loading / error / 11 个 tab；`RegionBoundary` 局部隔离 | action 需 operator；audit 需 platform_admin |
| `/assistant` | `AssistantPage` | 登录 + ≥1 角色 | loading / error / 无能力 / none-pending / stream-error | 见 PWR-011；troubleshoot 需 operator |
| `/capabilities` | `CapabilitiesPage` | 登录 | loading / error / `noScope` | 延期/禁用能力如实呈现 |
| `/admin/sources`、`/admin/policies`、`/admin/execution-profiles`、`/admin/deployment` | `AdminPage(section)` | `role=platform_admin` | loading / error / empty；step-up 门控 | 侧栏仅暴露 sources |
| `/admin/audit` | `AuditPage` | platform_admin | loading / error / empty | 也可内嵌于 `IncidentWorkspace` |
| `*` | 404 兜底 | 登录 | — | — |

**后端存在但 UI 无入口**（能力缺口，非缺陷）：`/api/v1/admin/tenants`、`/api/v1/admin/role-bindings`、`/api/v1/admin/recipes`、`/api/v1/admin/tools`、`/api/v1/admin/host-onboardings` 在生成的客户端中存在函数但 `web/src` 未引用。建议在能力/来源说明中注明"管理端未提供 UI"，或明确这些能力只经 API 使用。

**导航 vs 可达性**：`/admin/policies`、`/admin/execution-profiles`、`/admin/deployment` 无侧栏入口，但可经 `AdminPage` 的 Tabs 到达（`AdminPage.tsx:179-189`），属可接受但发现性较低的设计。

## 二、客观缺陷（可复核，均静态确认）

1. **图谱图例与实际线型不一致**（PWR-032.1，已按复核收窄）：`web/src/features/graph/legend.tsx:18` 称动态关系为"点划线"，实现为 `dashed`（`GraphView.tsx:136-138`）。另有 `GraphView.tsx:293`"虚线：TTL / 有效期已过期"一句，暗示"虚线 ⇔ 已过期"，但**未过期的动态边同样是虚线**，措辞不严谨。
   **已撤回**原先"同一线型承载互斥语义/仅凭线型无法区分"的判断：`layout.ts:5-10` 的 `edgeExpired()` 同时依据 `validTo` 与 `observedAt + ttl` 判定，说明**关系类别与过期状态是可叠加的两个维度**（`ttl=0` 且 `validTo` 已过的边即"过期但非动态"，并非互斥）；且关系表已分别呈现状态——`GraphView.tsx:458-463` 的"观测时效"列用 `edgeExpired(e, now)` 渲染 `Status`，`:464-467` 的"动态有效期"列对 `ttl===0` 显示"非动态关系"。因此影响限于**画布线型与图例用词**，不是"用户无法区分"。
2. **图谱 stale 提示存在，冗余分支不可达**（PWR-032.2，已更正）：`GraphView.tsx:246-255` 在 `stale` 时**明确返回告警**——`title="收到过期图谱版本，请重新查询"`、`description="此结果早于当前查询已收到的版本，暂不展示节点和关系…"`，因此**界面上有独立的过期提示**（原报告"不存在独立过期提示"的结论错误）。`:259-265` 的 `{stale && <Alert title="拒绝展示旧 generation 图谱…"/>}` 才是**不可达的冗余代码**（`stale` 已在 `:247` 提前返回），建议删除，但不影响用户可见性。另注意"旧 generation"与"关系 TTL/有效期过期"是两个不同概念，不要混用。
3. **`Status` 组件的 `null` 分支永不匹配**（PWR-032.3）：`Status.tsx:40-47` 的 `off` 集合含 `"null"`，而 `data.ts:10-11` 的 `text(null)` 返回"空值（null）"，导致 `null` 被渲染为 warning 色而非中性，同时该分支为死代码。
4. **助手能力接口失败无任何可见反馈**（PWR-011，P2）：`AssistantPage.tsx:129-146` 未消费 `capabilities.error`；`:317-323`、`:643-648` 的提示只在拿到 data 时渲染；`:629-638` 创建按钮因此永久灰置。`:665-674` 的文案还会把"取不到能力"表述为"没有准入证明"，把失败当业务结论。
5. **故障页 RCA 状态过滤与真实枚举不一致**（PWR-020，P2）：`FindingsPage.tsx:90-91` 提供 `conflicted`（永不匹配）且缺 `probable`（真实状态），选择后静默返回空列表。
6. **法律保留"解除"缺少 loading 与确认**（PWR-032.4）：`LegalHoldControl.tsx:52-66`——"设置保留"有 `loading={busy}`，"解除保留"没有；两者均无二次确认，解除属破坏性操作。
7. **助手筛选控件缺可访问名称**（PWR-032.5）：`AssistantPage.tsx:429-441`（授权域 `Select` 无 `aria-label`/`<label>`）、`:444-452`（集群仅有 placeholder）、`:453-458`（namespace 仅有 placeholder）。屏幕阅读器无法获得稳定名称，placeholder 在输入后消失。
8. **会话 SSE 重连固定 2 秒、无退避与上限**（PWR-032.6）：`useConversationStream.ts:71-72` 对非 401/403/410 错误每 2 秒重连；`useInvestigationStream.ts` 已有指数退避与 `attempts<10`，两者不一致。
9. **未使用导入与重复工具函数**（PWR-032.7）：`AdminPage.tsx:36` 导入 `JSONView` 但未使用（`tsconfig` 未开启 `noUnusedLocals` 故可通过类型检查）；`ConversationContext.tsx:3-7` 的 `object()` 与 `components/data.ts:1-5` 的 `record()` 完全等价。

## 三、主观建议（非缺陷，不作为 bug）

- `styles.css` 中 `.sidebar-note`（`#a7b9c7` on `#172a3a`）与 `.footer`（`#6f7f8f` on `#f3f5f7`）的对比度接近临界，建议按 WCAG AA 用工具实测后调整（本环境无法度量）。
- `FindingsPage` 时间输入仅 `onBlur` 提交（`:134-153`），键盘用户无 Enter 提交路径，建议补 `onPressEnter`。
- `AssistantPage` 消息列表最多约 100 张 Card，长会话较重，建议虚拟滚动或折叠早期轮次。
- `code`/`json-view` 使用恒定 `max-height`，长证据需多次内滚动，建议统一"展开/复制"入口（`FullText` 已部分提供）。
- `AdminPage.tsx:359-368`"退役此精确版本"无前端二次确认（依赖 step-up 与服务端校验）——建议加确认，但不是契约问题。
- `/admin/*` 三个分区无侧栏入口，发现性偏低，可考虑补导航或在管理页首屏提示。

## 四、正面结论（静态确认）

- **前后端同源生成**：`web/orval.config.ts` 与后端 `oapi-codegen` 共用 `api/openapi/platform-v1.yaml`（spec `version: 1.4.0`，与生成头一致），路径/方法/参数名基本不可能漂移；手写 `record()` 访问经抽点比对与 schema/Go handler **一致**。
- **认证会话设计稳健**：token 仅存内存（`api/client.ts:2`），`oidc-client-ts` 使用 `InMemoryWebStorage`；`checked()` 用 `credentialEpoch` 抑制过期请求副作用（401 清凭据、403 通知）；`AuthProvider` 的 `generation/refreshing/refreshPending` 处理竞态。测试 `auth.test.tsx`、`auth_recovery.test.tsx`、`client_generation.test.ts` 覆盖"过期延迟响应不得影响新身份"。
- **错误与降级语义清晰**：`QueryState`、`EnvelopeMeta`、`Status`、`investigationFailure`、能力原因映射覆盖 `degraded/partial/conflicted/execution_unknown/disabled/unverified`，并明确"未知≠零""延期≠失败"。
- **延期能力呈现诚实**：KubeVirt/CDI 以 `disabled` + "开发和真实运行验收延期"呈现，`AssistantPage.tsx:688` 明确 Trace "未启用"，未发现假入口或占位数据。
- **未发现**：接口不存在、字段拼写导致静默错值、凭据泄露到前端存储、失败被展示为成功、延期能力被呈现为可用（静态范围内）。

## 五、测试与动态验证

- 前端单元测试：本轮实际执行 `vitest run` → **23 个测试文件全部通过**（退出码 0），另 `tsc --noEmit` 退出 0、`vite build` 成功（699ms）。日志见 [evidence/web-vitest.txt](evidence/web-vitest.txt)。
- 单测覆盖缺口：`web/tests/graph_view.test.tsx` 以 `vi.mock("cytoscape")` 空实现替代真实渲染；`CommandEditor`/`ExecutionOutput`（CodeMirror/xterm）在单测中被替代或仅测纯函数——这些路径只在 e2e 覆盖，而 e2e 需外部环境（PWR-017）。
- **动态未验证清单（需合规环境复验）**：真实登录与 step-up 跳转、SSE 断线重连行为、图谱 canvas 交互与线型实际观感、CodeMirror/xterm 渲染、窄屏（移动/平板宽度）布局、键盘可达性与焦点顺序、对比度实测、所有 e2e 门控用例（`role_isolation`、`live`、`fault_chain_runtime`、`assistant_runtime` 等）。
