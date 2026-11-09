# P5-02 逐动作实现 / 测试 / 现场验收清单

范围：AI 会话历史与流恢复（P5-02）。本清单只覆盖 P5-02；P4-05、P5-01、P6-01 仍是后续阶段，未实现、未标完成。

日期：2026-11-09（第三轮返修，修复现场发现的原生 legal hold 缺陷与一条不稳定断言）。本轮为本地实现 + 隔离 PostgreSQL 实例上的真实自测，未提交、未推送、未部署。现场验收仍由 Codex 重做，本清单不代替现场结论。

## 1. 会话搜索（服务端过滤）

| 项 | 内容 |
| --- | --- |
| 实现 | `internal/assistant/repository.go`：`ConversationFilter{Query,Archived}`、`Normalize()`、`escapeLike()`；`List`/`ListPage` 增加过滤参数，SQL 使用 `title ILIKE '%'||escaped||'%' ESCAPE '\'` 与 `archived` 三态（exclude/include/only）。`internal/httpapi/assistant_handlers.go`：列表接口新增 `q`/`archived` 参数，**归一化后的过滤值同时用于仓储查询与游标摘要**（`ScopeDigest`），语义相同的空值/空白不再产生假冲突，真正变过滤仍然 403。`web/src/pages/AssistantPage.tsx`：搜索框 300ms 防抖后走服务端检索，不做前端假过滤。 |
| 语义裁决 | 过滤游标摘要与仓储查询使用同一规范值（`TrimSpace` + 通配符转义 + 归档态归一）。`q=' Pod '` 与 `q='Pod'`、`archived` 省略与 `archived=exclude` 是同一个过滤：游标摘要相同、可续页；`q` 换成别的词则 403。搜索仅覆盖标题子串，不引入正文全文检索依赖。 |
| 自动化测试 | Go：`internal/assistant/rename_test.go`（过滤归一化/通配符转义）。**已在隔离 PostgreSQL 实跑通过**：`test/integration/assistant_conversation_rename_test.go::TestAssistantSearchFiltersAndCursorStayConsistent`（字面 `%`、字面 `_`（并断言 `100X` 不被 `_` 命中）、字面 `\`、大小写、过滤+分页一致性、exclude/include/only 三态、非法归档态拒绝）、`::TestAssistantListCursorUsesTheNormalizedFilter`（HTTP 层：`q=' Pod '`+省略 archived → 有游标；`q='Pod'`+`archived=exclude` → **解码后的游标载荷**（租户/主体/kind/`scopeDigest`/位置）与前一次一致且可用原游标续页；改成 `q=节点` → 403。第三轮修正：原断言比较完整签名字符串，而游标 `expiresAt` 按秒生成、跨秒即假失败，改为比较载荷，不改产品游标过期语义）。Web：`web/tests/assistant_conversation_isolation.test.tsx`（搜索失败状态下仍可编辑/清除并重新发起查询；过滤变更后不携带旧游标）。 |
| 现场验收 | 1) 输入标题片段，观察 `GET /api/v1/assistant/conversations?q=...` 带参；2) 搜索 `%` 只能命中标题里真实含 `%` 的会话；3) 翻页后改搜索词，确认不带旧 cursor；4) 切换“已归档”后再翻页；5) 切换账号后确认旧结果不出现。 |

## 2. 会话改名

| 项 | 内容 |
| --- | --- |
| 实现 | `internal/assistant/rename.go`：`CanonicalTitle()` 复用创建路径的 `tools.RedactText` 脱敏边界并再次校验；`Rename()` 复核 owner 与当前 grant（`authorizeConversation`），`expectedConversationRevision` 版本围栏（不一致返回 `ErrStale`→409），复用 `investigation.assistant_binding_requests` 幂等账本。HTTP：`PATCH /api/v1/assistant/conversations/{id}`（原生 CSRF + Idempotency-Key）。Web：`AssistantPage.tsx` 改名弹窗。 |
| 语义裁决 | 同标题 = no-op：不改 revision、不改 updated_at，仅登记幂等账本并写 `assistant.conversation.renamed`（payload `noop=true`）审计；同 key 同载荷 = 重放：不二次写库、不二次审计，返回当前会话；同 key 不同载荷（含与重绑等其他操作撞键）= 409 `IDEMPOTENCY_CONFLICT`，明确拒绝不放行。改名返回的是提交后的 title/revision/updated_at（`UPDATE ... RETURNING`）。归档会话允许改名（标题元数据），公开 UI 与服务端一致。 |
| 自动化测试 | **已在隔离 PostgreSQL 实跑通过**：`test/integration/assistant_conversation_rename_test.go::TestAssistantRenamePersistsAndAudits`（持久化 + 审计 + 版本围栏 + 重放不二次写 + 同 key 异载荷拒绝）、`::TestAssistantRenameNoopKeepsRevisionAndTimestamp`、`::TestAssistantRenameAppliesTheCreationRedaction`、`::TestAssistantRenameRefusesForeignOwnerAndRevokedGrant`（**通过平台支持的状态 `status='disabled'` 撤权**，不改冻结迁移、不放松 CHECK；断言撤权后返回 `ErrDenied`（且不是 `ErrStale`），标题/版本/幂等账本/审计均未被写入）、`::TestAssistantRenameRejectsAForeignLedgerKey`（**跨操作撞键**：先用 Rebind 占用 key，再 Rename → `ErrConflict` 且标题未变；**跨租户**：另一租户用同一 key 改名成功，账本互不干扰）。Web：`assistant_conversation_isolation.test.tsx`（打开时冻结目标与 revision；后台刷新出新 revision 也不覆盖；scope 变化中止并丢弃旧响应与草稿；切换会话关闭弹窗；409 不偷偷取最新版本重试）。 |
| 现场验收 | 1) 改名后刷新，标题持久；2) 标题填 `token=xxxx`，确认库内与列表均为脱敏值；3) 两人同改一个会话，后者收到 409 且不覆盖；4) 断网/切账号时弹窗关闭后旧响应不落地；5) 撤权后改名返回 403。 |

## 3. 会话归档

| 项 | 内容 |
| --- | --- |
| 实现 | 复用既有 `Repository.Archive`（不级联删除引用）；忙态守卫保留：存在 `queued`/`running` 轮次的会话不可归档。Web 改为 Popconfirm 二次确认，确认目标在打开确认时冻结（`archiveIntent`），导航后不归档另一会话。 |
| 自动化测试 | **已在隔离 PostgreSQL 实跑通过**：`::TestAssistantArchiveKeepsTheFixedReferences`——先独立断言**忙态拒绝**（`ErrBusy`，未绕过产品守卫），再通过既有 Job repository 生命周期（`Claim` + `Complete`）合法结束该隔离 fixture 任务后归档；随后断言**真实固定引用未被破坏**：提交时写入一条真实 `platform.resource_entities` 引用（`resolve_resources` 已提交步骤 + `entityRefs`）与原始摘要，归档前后 `TurnReadable` 回读的提交答案**逐字节相同**、摘要与引用不变、轮次 `input_digest` 未被改写、引用目标仍可解析、消息与归档后读取仍被授权。`::TestAssistantSearchFiltersAndCursorStayConsistent` 内覆盖归档三态过滤与归档后仍可读。Web：`assistant_conversation_isolation.test.tsx`（确认归档目标正确；导航前打开的确认不归档当前会话）；`assistant_archive_view_switch.test.tsx`（**切换“已归档”后请求带 `archived=only`**，此前缺失的稳定交互用例）。 |
| 现场验收 | 1) 归档后历史消息与固定引用仍可读；2) 归档列表项不再出现在默认视图、在“已归档”视图出现；3) 归档会话改名不影响归档态。 |

## 4. 范围重绑

| 项 | 内容 |
| --- | --- |
| 实现 | 复用既有 `BindingEditor` + `Repository.Rebind`（frozen grant、revision 围栏、既有轮次不可变）。本轮把**当前身份/授权范围生命周期**纳入编辑器：新增 `scopeKey` 入参（会话身份 + 授权范围 + 集群范围代次），`AssistantPage.tsx` 以 `key={`${id}:${scopeKey}`}` 挂载，`BindingEditor` 的重置 effect 依赖 `[conversation.conversationId, scopeKey]`，意图对象冻结 `scopeKey`；范围变化即清稿、abort 在途请求、丢弃迟到响应。仍保留：打开时冻结 conversationId 与 revision、重复提交被拒、请求带 AbortSignal、401/403 清理 `["assistant"]` 缓存、409 提示刷新。 |
| 自动化测试 | Web：**新增独立行为用例** `web/tests/assistant_binding_editor_scope.test.tsx`（5 条）：同 id 不同 scope → 清稿 + abort（`signal.aborted`）+ 迟到成功不触发 `onChanged`；打开时 revision 固定（后台刷新到 5 仍提交 3）；迟到失败不在新 scope 报错；401/403 触发 `removeQueries({queryKey:["assistant"]})` 并给出权限提示；409 给出“会话已被其他操作修改，请刷新后重试”且不静默重试。 |
| 现场验收 | 1) 打开编辑器后切换会话再提交，不得改动另一会话；2) 收窄范围后范围外历史不进入后续回答；3) 授权撤销后重绑返回 403；4) 同一会话在切换账号/范围后重开编辑器，草稿不得来自上一个范围。 |

## 5. 停止 / 流续传 / 断连恢复

| 项 | 内容 |
| --- | --- |
| 实现 | 复用既有 `useConversationStream`（Last-Event-ID 续传、410 过期游标复位重连、不新建 Job）与既有取消/Job 停止链路，本轮未改动，仅做回归。 |
| 自动化测试 | Web：`tests/assistant_stream.test.tsx`、`tests/investigation_stream.test.tsx`（既有用例，通过）。 |
| 现场验收 | 1) 断网重连不重复建 Job；2) 过期游标复位后可继续；3) 停止后不再产生新轮次。 |

## 6. 个人历史及保留

| 项 | 内容 |
| --- | --- |
| 实现 | 复用既有 `retention.Cleanup` / `SetLegalHold` / `LegalHoldControl` 与历史授权重验。**第三轮修复**：原生平台管理员设置/解除 legal hold 的数据库围栏（见第 9 节），未新增表，未扩大命名空间范围。 |
| 现场验收 | 1) 保留期内会话可读；2) legal hold 会话不被清理；3) 普通 operator 无管理员视图；4) **原生 platform_admin 用真实会话设置/解除 legal hold 返回 200 且落库**（第三轮修复后待 Codex 现场重验）。 |

## 7. 第三轮：原生 legal hold 拒绝的修复（现场真实缺陷）

| 项 | 内容 |
| --- | --- |
| 现场现象 | 原生 platform_admin（真实会话、platform_admin 授权版本 3）用有效 csrf.token 调用 `POST /api/v1/assistant/legal-holds` 仍得业务 403（不是 CSRF 拒绝）；命名空间 operator 被拒是正确的。 |
| 根因 | `internal/assistant/retention.go` 先调用支持原生身份的 `investigation.AuditScopeTx`（原生分支正确），随后调用 SQL `investigation.assistant_set_hold`（迁移 42），后者内部只调用迁移 39 的 `platform.assistant_lock_audit_authority`，该函数只认旧表 `platform.role_bindings`，因此原生管理员在数据库层被再次错误拒绝。 |
| 修复 | 新增 **迁移 75** `migrations/00075_native_assistant_legal_hold.sql`：`investigation.assistant_set_hold_native(tid,turn,user,session,authorization_version,credential_version,session_version,subject,hold,reason)`，SECURITY DEFINER，围栏为：① 事务租户栅栏（`app.tenant_id`）；② subject 必须等于 `identity.accounts.subject` 本身（不信任调用方传入的 subject 前缀）；③ `identity.authorize_native_session_role` 重验活跃账号、未撤销未过期的真实会话、当前授权版本上的 platform_admin 授权，并按 account→session→grant 顺序 FOR SHARE 钉住行（撤权并发时等待而非竞态）；④ `platform.native_authority_shared` 复读当前授权版本上的有效授权集合；⑤ 迁移 42 的租户 advisory 锁与轮次行锁保持不变。Go 侧 `SetLegalHold` 按**已证明的身份来源**分派：原生走新的 native 围栏，旧管理员继续走冻结的迁移 42 围栏；Go 不绕过任何数据库检查、不新增管理员权限、不伪造旧绑定。 |
| 权限边界 | 新函数 `REVOKE ALL … FROM PUBLIC`，只 `GRANT EXECUTE … TO api_runtime_role`；API 角色对 `investigation.assistant_legal_holds` 仍只有 SELECT，不能直接改写保留标志。迁移 1..74 字节未改。 |
| 新增自动化测试 | `test/integration/native_assistant_legal_hold_test.go`（真实 PostgreSQL、真实原生账号/授权/会话，调用走 api_runtime_role）：`::TestNativeAdminAssistantLegalHoldPersistsAndAudits`（原生管理员设置成功 → 落 `assistant_legal_holds`、`assistant_messages.legal_hold`、`assistant_query_results.legal_hold`、审计 `assistant.legal_hold.changed` 一致；解除成功；断言**没有**为原生账号生成任何旧 `platform.role_bindings`）；`::TestNativeAssistantLegalHoldRefusesWithoutAuthority`（原生 operator、账号 disabled、授权版本失效、授权撤销、伪造 subject、无会话、跨租户全部 `ErrDenied` 且**无任何写入**：hold 行、消息、查询结果、审计均为 0）；`::TestNativeAssistantLegalHoldKeepsTheRuntimeRoleFenced`（API 角色直接 UPDATE 保留行被数据库拒绝；直接调用 native 围栏函数伪造 subject 返回 false）。 |
| 本地/隔离实跑 | 见第 10 节。旧 `platform.role_bindings` 正例（`TestAssistantExplicitScopeChangesAndPhysicalTTL` 内的 legacy hold/release）未退化。 |

## 8. 第三轮验证结果（真实执行）

- `go build ./...`：通过；`go test ./internal/assistant/... ./internal/httpapi/... ./internal/investigation/...`：全部通过。
- `go vet ./test/integration ./internal/assistant/...`：通过（integration 包编译通过）。
- **真实 PostgreSQL 实跑**（隔离实例 `ops-p502-qa-20261109`，127.0.0.1:15584，迁移 1..75 实跑通过，与 `ops-current/ops_current` 隔离）：**14 条用例全部 PASS、无 SKIP**——8 条既有 P5-02 用例（`TestAssistantRenamePersistsAndAudits`、`TestAssistantRenameNoopKeepsRevisionAndTimestamp`、`TestAssistantRenameAppliesTheCreationRedaction`、`TestAssistantRenameRefusesForeignOwnerAndRevokedGrant`、`TestAssistantRenameRejectsAForeignLedgerKey`、`TestAssistantSearchFiltersAndCursorStayConsistent`、`TestAssistantListCursorUsesTheNormalizedFilter`、`TestAssistantArchiveKeepsTheFixedReferences`）+ 3 条既有范围/保留/取消回归（`TestAssistantExplicitScopeChangesAndPhysicalTTL`、`TestAssistantHistoryRetentionAndPlatformClarification`、`TestAssistantCancellationAndBudgetKeepCommittedNumbers`）+ 3 条本轮新增原生 legal hold 用例。日志：`/mnt/ops-sdb/ops-p502-qa-20261109/results-p502-r4.log`（本地脱敏副本：`docs/evidence/four-task-coverage-20261109/p502-r4-isolated-pg.log`，无 DSN）。
- 迁移边界门禁：`TestMigrationsAndRoles`（角色/RLS/最小权限）在同一隔离实例实跑 **PASS**；`TestMigrationScriptsAreForwardOnlyAndContiguous`（新迁移只含正向 Up 段、序号连续、交付基线登记为 75）与 `TestMigrationCLIHasNoDownPath` 在**本机仓库树**实跑 **PASS**——这两条只读 `../../docs`、`../../cmd` 文件，隔离实例的仓库镜像只含 `migrations/policies/recipes/test`，因此在远端跑会因缺文件失败（环境范围限制，非产品失败，已用本机实跑替代）。迁移 75 无 Down 段（仓库规则：新迁移不得带 Down），迁移 1..74 字节冻结未改，goose 版本表保证只前进不重放（同一迁移在 15 个新建测试库上各应用一次，全部成功）。日志：`/mnt/ops-sdb/ops-p502-qa-20261109/results-p502-mig75.log`（本地脱敏副本：`docs/evidence/four-task-coverage-20261109/p502-r4-migration-gate.log`）。
- `scripts/check-generated.py "$(command -v go)" gen web/src/api/generated internal/persistence/dbgen services/investigator/src/investigator/output.schema.json services/investigator/src/investigator/assistant-output.schema.json`：通过（本轮**未改** API 合同与生成物，生成目录无字节变化）。
- Web：本轮**未改任何 Web 文件**，无需重跑 Web 用例；`LegalHoldControl` 仍是既有组件，调用既有生成客户端 `setAssistantLegalHold`。
- **不稳定断言已修**：`TestAssistantListCursorUsesTheNormalizedFilter` 原来比较两个新签游标的完整字符串；游标 `expiresAt` 按当前秒生成，跨秒即假失败。现在改为比较**解码后的游标载荷**（租户、主体、kind、`scopeDigest`、位置 `before`/`beforeId`），并保留"规范化过滤可用原游标续页""续页无重复""改变过滤携旧游标 403"三条断言；未改产品游标过期语义（24 小时过期与 `CURSOR_EXPIRED` 行为不变）。
- 隔离实例自测只是**合成机制验证**，不等于业务现场验收通过；原生真实会话的现场复验仍待 Codex 完成。

## 9. 第二轮验证结果（真实执行，已被第三轮取代）

- `go build ./...`：通过
- `go test ./internal/assistant/... ./internal/httpapi/...`：通过
- `go vet ./test/integration`：通过（integration 包编译通过）
- **真实 PostgreSQL 实跑**（隔离实例 `ops-p502-qa-20261109`，127.0.0.1:15584，PG17，迁移 1..74 实跑通过，与 `ops-current/ops_current` 隔离）：新增/修改的 8 条 DB 用例 **全部 PASS、无 SKIP**——
  `TestAssistantRenamePersistsAndAudits`、`TestAssistantRenameNoopKeepsRevisionAndTimestamp`、`TestAssistantRenameAppliesTheCreationRedaction`、`TestAssistantRenameRefusesForeignOwnerAndRevokedGrant`、`TestAssistantRenameRejectsAForeignLedgerKey`、`TestAssistantSearchFiltersAndCursorStayConsistent`、`TestAssistantListCursorUsesTheNormalizedFilter`、`TestAssistantArchiveKeepsTheFixedReferences`。
  结果日志：`/mnt/ops-sdb/ops-p502-qa-20261109/results-p502-r3.log`（QA 隔离实例根目录）。
- `web`：`tsc --noEmit` 通过；`vitest run tests/` **287 条全部通过**（56 → 58 个测试文件：新增 `assistant_binding_editor_scope.test.tsx` 5 条、`assistant_archive_view_switch.test.tsx` 1 条）；`vite build` 通过
- `scripts/check-generated.py`：第二轮**未能执行**，当时的解释有误——脚本的第一个参数是 Go 可执行文件，不是包管理器。第三轮按 `python3 scripts/check-generated.py "$(command -v go)" gen web/src/api/generated internal/persistence/dbgen services/investigator/src/investigator/output.schema.json services/investigator/src/investigator/assistant-output.schema.json` 实跑**通过**，生成目录无字节变化。
- 隔离实例自测只是**合成机制验证**，不等于业务现场验收通过；活数据现场引用证明仍待 Codex 现场验证。

## 10. 真实缺项（不得当作已验收）

1. **浏览器现场验收未做**：归档视图切换已在 jsdom 用例中覆盖（请求带 `archived=only`），但真实浏览器中的下拉交互、像素比对仍未做，**未标 visual passed**。
2. **活数据现场引用证明未做**：隔离 fixture 用例证明的是机制（提交引用、归档后引用与摘要不变、授权回读不变）；真实数据现场的固定引用可读性由 Codex 现场验证，本清单不代替该结论。
3. **原生 legal hold 修复尚未现场复验**：本轮在隔离 PostgreSQL 上用真实原生账号/授权/会话证明原生 platform_admin 可以设置与解除 legal hold、且所有越权形态均无写入；**真实部署环境的原生管理员现场复验仍待 Codex 重做**（候选未部署，本清单不代表现场通过）。
4. **P4-05、P5-01、P6-01 仍未实现**，本地 ready 只表示 P5-02 本地实现可审，不代表四项整体完成。
5. 本轮未提交、未推送、未部署；未改动当前部署环境、业务库与生产源码目录；未改动当前业务账号、角色与来源；未新增旧 `platform.role_bindings` 绑定。
6. 未覆盖并发撤权与保留写入的锁竞争实测：围栏按 account→session→grant 的 FOR SHARE 顺序钉住行（与行政撤权路径同序），但本轮未做并发压测用例。

## Codex最终复核与现场补验

参见../p502-live-20261109/README.md、acceptance.json、native-legal-hold-final.json和release-manifest.json。最终候选179ca6a，schema75，原生管理员页面保留/解除200，operator403，持久标记和审计一致。前文“未做现场”是实现阶段历史状态，已由这些真实回执补验；像素未验仍有效。游标不再比较包含时间戳的完整签名。生成脚本需传Go可执行文件，npm/pnpm不可执行的解释已纠正，Codex独立生成检查通过。14条P5-02主切片共有的输出动作仍需P5-01完整收口，未据此标整项完成。
