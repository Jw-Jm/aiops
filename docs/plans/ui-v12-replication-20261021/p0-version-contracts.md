# P0-03 版本化合同与补充安全状态（2026-10-21，初审修正 r2）

范围：仅原生账号/会话/再次验证、独立观测查询、四字段模型连接与运行绑定。文档中的 HTTP 路径与方法均为**拟定**，除标注“已有”者外均未实现、未加入 OpenAPI，也不得被前端当成现有接口调用。本文不修改根目录 00～10 正式文档、不改 `contracts.csv`，新增操作清单见文末，由主线程统一同步。

已核实依据：`internal/auth/stepup.go`（服务端 step-up 绑定、ACR/时间窗、事务内重检）、`internal/action/service.go`（高风险写入在事务内 `TouchCurrentStepUpSession` 与风险确认）、`internal/analytics/{domain,scope,capabilities,log_samples}.go`（查询计划、来源资格、样例与总体语义）、`internal/modelprofile/profile.go`（地址/准入约束）、`internal/app/sp06_runtime.go`（Profile 冻结与 Worker 能力心跳）、`services/investigator/src/investigator/assistant_binding.py`（Python 侧 Profile 校验）、`test/contract/schema_compatibility_test.go`（既有合同测试方式）。

## 零、初审修正记录

初版合同评审提出六项问题，本版已修正，且逐项补了反例：

1. 账号状态更新不再由客户端提交最后管理员计数/目标标记，改为服务端事务内判定并返回冲突投影 `accountUpdateConflict`；显示名可独立修改，不强制同时切换状态。
2. 删除 `initialPasswordMode` 与一次性链接流程；创建账号只接受登录名、显示名、初始密码，首次改密由服务端强制。
3. 再次验证绑定改为“显式 `operation` + `target`（含 revision）+ 仅命令执行才有的 `command` 绑定”。管理员/MFA 操作不再需要虚构 `riskAssessmentId`/`commandDigest`，命令操作继续复用真实目标、执行配置、策略版本与 Incident。
4. `requestedMethods` 从请求中移除，由服务端按 operation 策略派生；验证结果显式返回本次挑战累计的 `completedMethods`，只有累计 password+TOTP 才是 `loa:2`。验证响应不再伪造业务事务复检，复检记录 `writeRecheck` 只出现在实际高风险写结果里。
5. MFA 设置拆成“创建响应”和“状态视图”：`otpauthUri` 只出现在 `mfaEnrollmentCreated`，后续 `mfaEnrollmentView` 没有该字段。
6. 四个 Schema 的根文档改为不接受任何实例（`not: {}`），所有校验必须指向 `#/$defs/...`；新增 `TestUIV12RootRejectsUnscopedPayloads` 防止根 Schema 被当作通配符接受垃圾。其余 Schema 中“`const true` 即安全证明”的字段一并移除：CSRF Cookie 属性、`requiredBeforeRead`、`lateResultsRejected`、`samples.redacted`、`credential.returned`、`manualMayOnlyTighten`、`admissionUnchanged`、`qualityAssessed`、`workerCapability.required` 等改为“字段不存在 + 运行实现检查”。
7. 模型连接请求的 `contextWindow` 改为整数或 `null`（自动探测）；`source`、`providerReportedMax` 等测量事实只出现在响应投影中，客户端不能提交，也不新增第五个用户配置。
8. 凭据更新改为显式 `credential.action`：`keep` 保留已有密钥，`replace` 必须携带新 `apiKey`；普通编辑不强制重输，`keep` 不得携带密钥。响应仍只有受控引用与 `configured`，不含 `mode`/`returned` 声明。
9. 资源身份用 `namespaceScope` 判别：`namespaced` 必须有 namespace 名称与 UID，`cluster_scoped` 必须显式为 `null`/`null`；Node、PV、PhysicalServer 不再被迫伪造 namespace UID。
10. `queryView` 只有 `completed` 可以发布 `resultRef`；`running`、`cancel_requested`、`terminated`、`failed`、`denied`、`expired` 一律 `null`，终止/失败不发布迟到结果，过期读取不泄露旧结果。
11. `resultView` 增加 `status`（`complete`/`partial`/`no_data`/`degraded`）与 `exact`：`complete` 必须有精确总体且计数 ≥1；`no_data` 必须是精确测得 0 且样例为 0；空样例本身不等于无匹配，样例行永远不能推导总体。
12. 管理员密码重置单列 `adminPasswordResetRequest`/`adminPasswordResetResult`，不再与用户资料/状态修改共用 Schema：请求带初始密码、`expectedRevision`、`expectedCredentialVersion`；响应带新凭据版本、强制改密状态、会话撤销投影与审计 ID。
13. `challengeVerifyResult.completedMethods` 允许空数组，用于表达第一次密码或 TOTP 即失败；空集合与任何未同时累计 password+TOTP 的结果都只能是 `loa:1` 且没有 step-up 会话。
14. `commandBinding` 改用真实字段：`assessmentId`、`riskAssessmentVersion`、`executionRequestDigest`、`policyVersion`、`incidentId`、`executionProfileId`（UUID）、`executionProfileVersion`（整数）、`actualCommandDigest`；不再使用 `exec/v1` 字符串或虚构字段名。
15. 再次验证操作扩展到来源、集群、registry、执行配置等管理写，并为每类操作定义精确目标投影（source_registration / cluster_registration / registry_object / execution_profile），不强迫它们虚构 userId、command 或 enrollmentId。
16. 管理员 MFA 重置的 `sessionsRevoked` 允许 0，并新增 `revocation` 版本投影；安全保证来自凭据/会话版本更新，不靠“至少撤销 1 个会话”的人数断言。MFA 设置确认要求先以当前密码+TOTP 完成绑定当前 enrollment 与当前会话的挑战，再提交新 TOTP。
17. 恢复码验证请求新增 `challengeId`，避免服务端无法定位当前恢复挑战。
18. `resultView` 增加 `resultKind`，日志类结果才要求精确日志总体；指标/资源/硬件结果使用 `population.basis: not_applicable`，不人为构造日志计数。

## 一、新增 Schema

四个契约文件均位于 `api/schemas/`，由 `api/embed.go` 的 `schemas` 目录自动嵌入；`internal/contract.Validate` 只解析嵌入集合，不访问网络。`$id` 遵循既有 `https://ops.local/schemas/<name>/vN` 规则。每个根文档都带 `not: {}`，应始终用 `$id#/$defs/<name>` 校验。

| 文件 | `$id` | 主要定义 |
| --- | --- | --- |
| `ui-v12-identity-v1.schema.json` | `https://ops.local/schemas/ui-v12-identity/v1` | `loginRequest`、`firstPasswordRequest`、`passwordChangeRequest`、`accountCreateRequest`、`accountUpdateRequest`、`accountUpdateConflict`、`roleGrantRequest`、`roleGrantView`、`subjectMappingRequest`、`subjectMappingView`、`sessionView`、`sessionRevocation{Request}`、`csrf`、`accountView` |
| `ui-v12-stepup-v1.schema.json` | `https://ops.local/schemas/ui-v12-stepup/v1` | `target`、`commandBinding`、`challengeCreateRequest`、`challengeView`、`challengeVerifyRequest`、`stepUpSessionView`、`challengeVerifyResult`、`writeRecheck`、`mfaEnrollmentCreateRequest`、`mfaEnrollmentCreated`、`mfaEnrollmentView`、`mfaEnrollmentVerify*`、`recoveryCodeSet`、`recoveryCodeVerify*`、`adminMfaReset*` |
| `ui-v12-observability-v1.schema.json` | `https://ops.local/schemas/ui-v12-observability/v1` | `namespaceScope`、`resourceIdentity`、`sourceBinding`、`window`、`coverage`、`budget`、`filter`、`createRequest`、`queryView`、`resultRef`、`termination`、`reauthorization`、`population`、`samples`、`statistic`、`resultView`、`cancelRequest`、`cancelResult` |
| `ui-v12-model-v1.schema.json` | `https://ops.local/schemas/ui-v12-model/v1` | `apiKeyInput`、`credentialUpdate`、`connectionUpsertRequest`、`contextWindow`、`credentialState`、`connectionState`、`admissionState`、`connectionView`、`connectionTest{Request,Result}`、`capabilityRecord`、`runOptionView`、`assistantJobBinding` |

四个文件都使用 `additionalProperties: false`。未知字段一律拒绝，而不是忽略：这是“客户端自授 tenant/roles/假 LoA/第五字段/伪造总量”等拒绝路径的技术基础。

## 二、原生账号、会话与授权

| 合同 | 方法/路径 | 状态 | 请求 → 响应 |
| --- | --- | --- | --- |
| C-009 | `POST /api/v1/auth/login` | 拟新增 | `loginRequest` → `sessionView` |
| C-010 | `POST /api/v1/auth/logout` | 拟新增 | `sessionRevocationRequest` → 204 |
| C-007 | `GET /api/v1/auth/session` | 已有-改造 | — → `sessionView`（保留既有业务字段，替换身份来源） |
| C-011 | `POST /api/v1/auth/password:change` | 拟新增 | `firstPasswordRequest` 或 `passwordChangeRequest` → `sessionView` |
| C-014～C-016 | `/api/v1/admin/users[...]` | 拟新增 | `accountCreateRequest` / `accountUpdateRequest` → `accountView`，最后管理员冲突 → `accountUpdateConflict` |
| C-017 | `POST /api/v1/admin/users/{userId}/password:reset` | 拟新增 | `adminPasswordResetRequest` → `adminPasswordResetResult` |
| C-018 | `POST /api/v1/admin/users/{userId}/sessions:revoke` | 拟新增 | `sessionRevocationRequest` → 204 |
| C-019、C-020 | `/api/v1/admin/role-bindings[...]` | 已有-适配 | `roleGrantRequest` → `roleGrantView` |
| C-021 | `PATCH .../role-bindings/{bindingId}/status` | 已有-适配 | 只沿用既有 revision/status 合同，本次不新增 Schema |
| 新增 | `POST /api/v1/admin/subject-mappings` | 拟新增 | `subjectMappingRequest` → `subjectMappingView` |
| 新增 | `GET /api/v1/admin/subject-mappings` | 拟新增 | 查询参数 `userId`/`legacySubject` → `subjectMappingView` 列表 |

固定要求：

- `loginRequest`、`accountCreateRequest`、`accountUpdateRequest`、`roleGrantRequest` 不含 `tenantId`、`subject`、`roles`、`acr`。任何这类字段都是未知字段并被拒绝；组织只从安装配置与服务端成员关系解析。
- 创建账号只接受 `loginName`、`displayName`、`initialPassword`，没有 `initialPasswordMode`、邮件或一次性链接流程。服务端始终标记首次登录必须改密；该标记不是客户端可选择的字段。
- 显示名修改与状态变更互相独立：`accountUpdateRequest` 至少包含 `displayName` 或 `status` 之一，均不强制另一项；仅修改显示名不得触碰账号状态。
- 管理员密码重置是独立操作：`adminPasswordResetRequest` 只接受 `initialPassword`、`expectedRevision`、`expectedCredentialVersion`；目标用户来自路径，客户端不能提交 `mustChangePassword` 或 `userId`。响应 `adminPasswordResetResult` 返回新 `credentialVersion`、`passwordState: change_required`、`sessionsRevoked`（允许 0）与 `revocation` 版本投影；强制改密与撤销会话是服务端行为。
- 最后管理员保护完全在服务端：请求不包含 `lastAdministratorCheck`、`activeAdministrators`、`targetIsLastActive`。若事务内判定目标为最后活跃管理员且请求要停用，返回 `accountUpdateConflict`（`code: last_active_administrator`、事务时实际 `activeAdministrators`、`attemptedStatus: disabled`、`effectiveStatus: active`）。客户端篡改任何计数或标记都不会进入判定。
- 角色以单条授权表达，`role` 只允许 `operator` 或 `platform_admin`。`platform_admin` 不继承运维权限；两角色分别授予、分别撤销，并各自带 `authorizationVersion`。
- 历史主体只允许显式映射：`basis` 固定 `verified_inventory`、必须带 `evidenceDigest` 与 `expectedUserRevision`；显式性由专用写操作与证据摘要表达，不使用恒真确认字段。`matchBy`/`loginName` 等按名匹配字段被拒绝；`subjectMappingView` 没有角色字段，映射不携带也不继承角色。
- 会话 `acr: urn:ops:loa:2` 必须同时包含 `password` 与 `totp` 两种 `authMethods`；仅密码登录只能是 `loa:1`。首次设密会话 `mode: first_password_only` 必须 `mustChangePassword: true` 且 `authorization.grants` 为空，只允许改密与退出。
- 会话 `revocation` 同时携带 `credentialVersion`、`sessionVersion`、`authorizationVersion`，三者均从 1 起。改密、撤销、停用或角色变更必须递增对应版本并使旧会话失效。
- CSRF 响应字段只描述浏览器需要回传的 `headerName`、`token`、`cookieName` 与 `sameSite: Strict`。HttpOnly、Secure、Origin 校验、服务端会话存储属于运行实现要求，不再用 `const true` 字段冒充已证明的安全属性。
- `accountView`/`sessionView` 的 `activeAdministrators`、`isLastActiveAdministrator` 只是只读服务端事实，供界面解释冲突，不被任何请求接受。

## 三、再次验证、MFA 与恢复

| 合同 | 方法/路径 | 状态 | 说明 |
| --- | --- | --- | --- |
| C-008 | `POST /api/v1/auth/step-up-sessions` | 已有-改造 | 请求体保持为空，身份来自已验证会话；返回 `stepUpSessionView`。客户端不能提交 LoA |
| C-012 | `POST /api/v1/auth/step-up-challenges` | 拟新增 | `challengeCreateRequest` → `challengeView` |
| C-013 | `POST /api/v1/auth/step-up-challenges/{challengeId}:verify` | 拟新增 | `challengeVerifyRequest` → `challengeVerifyResult` |
| 新增 | `POST /api/v1/auth/mfa/enrollments` | 拟新增 | `mfaEnrollmentCreateRequest` → `mfaEnrollmentCreated`（唯一一次返回 `otpauthUri`） |
| 新增 | `POST /api/v1/auth/mfa/enrollments/{enrollmentId}:verify` | 拟新增 | `mfaEnrollmentVerifyRequest` → `mfaEnrollmentVerifyResult` |
| 新增 | `GET /api/v1/auth/mfa/enrollments/{enrollmentId}` | 拟新增 | — → `mfaEnrollmentView`（无 `otpauthUri`） |
| 新增 | `POST /api/v1/auth/mfa/recovery-codes:verify` | 拟新增 | `recoveryCodeVerifyRequest` → `recoveryCodeVerifyResult` |
| 新增 | `POST /api/v1/admin/users/{userId}/mfa:reset` | 拟新增 | `adminMfaResetRequest` → `adminMfaResetResult` |

固定要求：

- 绑定由三部分组成：显式 `operation`（`command_execution`、`user_administration`、`mfa_enrollment`、`mfa_reset`、`recovery_code_regenerate`、`source_administration`、`cluster_administration`、`registry_administration`、`execution_profile_administration`）、`target`、以及仅命令执行才允许的 `command`。
- `target` 是判别式投影：`k8s_object`（canonicalId、UID、clusterUid、namespace、revision）、`user`、`mfa_enrollment`、`source_registration`、`cluster_registration`、`registry_object`（draft/version/activation）、`execution_profile`。来源/集群/registry/执行配置的管理写使用各自目标，不需要虚构 userId、command 或 enrollmentId。
- `command` 复用现有命令绑定事实：`assessmentId`、`riskAssessmentVersion`、`executionRequestDigest`、`policyVersion`、`incidentId`、`executionProfileId`（UUID）、`executionProfileVersion`（整数）、`actualCommandDigest`。非命令操作**不得**携带 `command`，也不需要虚构风险评估；命令操作缺少 `command` 或目标不是 `k8s_object` 会被拒绝。
- `requestedMethods` 不在请求中，客户端无法下调验证方式。`challengeView.requiredMethods` 由服务端 operation 策略派生且固定为 password 与 TOTP；`completedMethods` 只累计当前挑战实际完成的步骤，允许为空数组（第一次因子即失败）。
- `challengeVerifyResult` 表达的是**写入前**的挑战状态：`completedMethods` 由服务端累计，可为 `[]`；`verified: true` 必须同时包含 password 与 TOTP、`acr: urn:ops:loa:2` 且有绑定当前 operation/target 的 `stepUpSession`。仅 TOTP、仅密码、空集合或用上一次普通登录都不能提升；部分完成只能是 `challengeState: pending`、`acr: loa:1`、无 step-up 会话。
- 验证响应没有事务复检字段。`writeRecheck` 只属于实际高风险写的结果（如 `adminMfaResetResult`），绑定 `writeAttemptId`、operation、`checkedAt`、会话摘要、授权/凭据版本、对象摘要；命令操作还必须带 `actualCommandDigest`。服务端必须在同一写事务内产生并校验它。
- 挑战状态 `expired`/`used`/`revoked` 时 `remainingAttempts` 必须为 0；`verified` 必须累计两种方法且有 `verifiedAt`。
- MFA 设置创建响应 `mfaEnrollmentCreated` 才含 `otpauthUri`（pending 密钥只显示一次）；状态读取 `mfaEnrollmentView` 没有该字段，`confirmed` 必须有 `confirmedAt`。确认必须先以当前密码+TOTP 完成绑定该 enrollment 与当前会话的 `mfa_enrollment` 挑战，`mfaEnrollmentVerifyRequest.stepUp` 携带该 `challengeId` 与 `sessionRef`；仅凭已登录 Cookie 加攻击者新 TOTP 不足以完成绑定。确认响应一次性签发 10 个恢复码，服务端只存摘要。
- 恢复码通过只得到 `loa:1`，`stepUpSession` 固定 `null`，必须撤销既有会话并强制重新设置 MFA。请求必须带 `challengeId` 与 `expectedChallengeRevision`，服务端按该 ID 定位当前恢复挑战，不能只凭 Cookie 猜测。
- 管理员 MFA 重置属于高风险：需要绑定目标用户的 `mfa_reset` step-up、`confirmLoginName` 与目标一致、`expectedUserRevision`/`expectedCredentialVersion`；结果返回 `writeRecheck`、`revocation` 版本投影与审计记录 ID。`sessionsRevoked` 允许 0（目标可能无活跃会话），安全保证来自凭据/会话版本递增，而不是会话人数。

## 四、独立观测查询

| 合同 | 方法/路径 | 状态 | 请求 → 响应 |
| --- | --- | --- | --- |
| C-024 | `POST /api/v1/observability/queries` | 拟新增 | `createRequest` → 202 `queryView` 或同步完成的 `queryView` |
| C-025 | `GET /api/v1/observability/queries` | 拟新增 | 个人历史列表 → `queryView` 列表 |
| C-026 | `GET /api/v1/observability/queries/{queryId}` | 拟新增 | — → `queryView` |
| 新增 | `GET /api/v1/observability/queries/{queryId}/results` | 拟新增 | — → `resultView`（每次读取重新授权） |
| C-027 | `POST /api/v1/observability/queries/{queryId}:cancel` | 拟新增 | `cancelRequest` → `cancelResult` |
| C-028 | `GET /api/v1/observability/capabilities` | 拟新增 | 授权范围 → 可选来源安全投影 |

固定要求：

- `createRequest` 不含 `conversationId`/`turnId`，也不含自由查询表达式；任何这类字段都被拒绝。手动查询不创建 AI 会话。
- 资源身份固定 `canonicalId`、`uid`、`clusterUid` 与判别式 `namespaceScope`：`namespaced` 必须同时给出 namespace 名称与 UID；`cluster_scoped`（Node、PV、PhysicalServer）必须显式 `namespace: null`、`namespaceUid: null`。不得把 cluster-scoped 对象伪造成某个 namespace 的资源。来源固定 `registrationId`、`registrationRevision`、`credentialRevision`、`qualificationDigest` 与 `collectorVersion`。
- 时间窗为绝对半开窗口：`bounds` 固定 `"[from,to)"`，并带 `timezone`。`to` 必须晚于 `from`，由运行实现检查。
- `coverage.basis: unknown` 时 `complete` 必须为 `false`；缺口逐段记录。
- 总体与样例分离：`population.basis: exact` 必须有 `count` 且 `method` 为 `source_total`/`full_scan`；`basis: not_measured` 时**不得**出现 `count`。样例固定 `selection: bounded_non_representative`、`inferredPopulation: null`。样例数量、`returned` 或截断都不能推导总体；脱敏是运行实现要求，不设置“已脱敏”的布尔断言字段。
- 查询状态：只有 `completed` 可以发布 `resultRef`；`running`、`cancel_requested`、`terminated`、`failed`、`denied`、`expired` 的 `resultRef` 必须为 `null`，终止/失败不得发布迟到结果，过期读取不得泄露旧结果。`failed`/`denied` 必须有 `failure`；`cancel_requested` 必须有 `cancelRequestedAt` 且不得有 `termination`；`terminated` 必须同时有 `cancelRequestedAt` 与 `termination`（含 `deliveredAt`、`sourceStopped`、`receiptDigest`）；`accepted`/`running` 不得携带取消或终止字段。取消请求不等于后端已终止，迟到结果不得重新进入当前页面。
- 结果视图用 `resultKind` 限定总体口径：`log_samples`/`log_statistics` 的 `complete` 需要精确日志总体且计数 ≥1，`no_data` 需要精确测得 0 且样例 0；`metric_series`/`resource_state`/`hardware_state` 没有日志总体，必须使用 `population.basis: not_applicable` 且不得出现 `count`，其 `complete` 只要求 `exact: true` 与样例 0。`partial`/`degraded` 的 `exact` 必须为 `false`。空样例不代表无匹配，`returned`、样例行或截断都不能推导总体。
- 每次读取 `queryView`/`resultView` 都带 `reauthorization` 记录：实际 `checkedAt`、`authorizationVersion`、`sourceRegistrationRevision`、`result`、`denialCode`。`result: allowed` 时 `denialCode` 必须为 `null`，否则必须给出拒绝原因（撤权、来源撤销、凭据轮换、资格过期、结果过期）。“每次读取必须重检”是运行规则，不用 `const true` 字段代替。
- 结果必须带 `expiresAt`；过期结果不得返回 `allowed`。

## 五、模型四字段、准入与运行绑定

| 合同 | 方法/路径 | 状态 | 请求 → 响应 |
| --- | --- | --- | --- |
| C-050 | `GET /api/v1/admin/model-connections` | 拟新增 | — → `connectionView` 列表 |
| C-051 | `POST /api/v1/admin/model-connections` | 拟新增 | `connectionUpsertRequest` → `connectionView` |
| C-052 | `PATCH /api/v1/admin/model-connections/{modelConnectionId}` | 拟新增 | `connectionUpsertRequest` → `connectionView` |
| C-053 | `POST /api/v1/admin/model-connections/{modelConnectionId}:test` | 拟新增 | `connectionTestRequest` → `connectionTestResult` |
| C-054 | `GET /api/v1/admin/model-connection-tests/{testId}` | 拟新增 | — → `connectionTestResult` |
| 新增 | `GET /api/v1/model-run-options` | 拟新增 | 授权能力投影 → `runOptionView` 列表 |
| 新增 | `assistantJobBinding`（无独立 HTTP 路径） | 拟新增 | API/Go Worker/Python 新 Job 共用合同 |

固定要求：

- 用户输入仍是四项：`baseURL`、`modelID`、凭据和 `contextWindow`，加 `expectedRevision`。`temperature` 等额外运行字段是未知字段并被拒绝。
- `contextWindow` 输入只有两种形态：整数（管理员明确值）或 `null`（由平台自动探测）。`source`、`providerReportedMax`、实际生效值只出现在 `connectionView.contextWindow` 服务端投影中；客户端提交这些字段会被拒绝。
- 凭据更新用 `credential.action` 表达：`keep` 保留已有密钥且不得携带 `apiKey`，`replace` 必须携带新 `apiKey`（`writeOnly`）。新建连接必须 `replace`；普通编辑不强制重输。响应 `credential` 只含 `store: openbao`、`vaultPathRef`、`configured`，没有 `mode`/`returned` 之类的自我声明；回显密钥原文即违反 `additionalProperties: false`。
- 响应投影中 `contextWindow.source: provider_reported` 必须有 `providerReportedMax`；`administrator_entered` 时只能收紧，无法测量时保持未知。该比较是运行实现检查，不设置 `manualMayOnlyTighten` 之类的自我声明。
- 连接、准入、可执行任务、回答质量分别记录，且互不携带对方字段：`connectionTestResult` 没有 `admission`/`quality` 字段；`capabilityRecord` 四个子状态各自独立；`quality.state: assessed` 必须有评分版本与证据摘要。
- `admission.state: admitted` 必须有 `evidenceDigest`、`profileReference` 与 `evaluatedAt`；`pending`/`unknown`/`rejected` 的 `profileReference` 必须为 `null`。未知模型不得伪装为可运行。
- `runOptionView` 只列已准入（`admission.state: admitted`）且 Worker 能力就绪（`worker.state: ready`、`readyUntil`）的选项；连接记录存在不等于可提交。
- 新 Job 合同 `assistantJobBinding` 固定 `jobContractVersion: ui-v12-assistant-job/v1`，保存 `profile.reference`（`版本@sha256`）、`admissionEvidenceDigest` 与 `workerCapability.modelProfileVersion`。API 创建时冻结；Go Worker 与 Python dispatcher 执行时必须比对同一引用与 `catalogVersion: assistant-tools/v2`，不一致按不可用处理。旧 `investigation-job/v3` 不变，不使用新版本字段回填或改写旧 Job。

## 六、必须由运行实现检查的跨字段语义

JSON Schema 只能静态约束结构与常量；以下语义必须在 P2-02/P2-03/P3-02/P5-03 实现，并用实现测试验证，不能以本文或合同测试代替：

1. 登录名在安装实例内唯一、组织解析只来自安装配置；密码使用 Argon2id 参数版本，失败限速持久化，未知用户与错误密码返回一致错误。
2. 会话轮换、HttpOnly/Secure Cookie、Origin/CSRF 校验、写请求幂等；撤销版本递增后旧会话、旧 SSE 与迟到响应立即失效。
3. 首次设密会话只能访问改密与退出；其他业务接口必须拒绝；首次改密由服务端强制，不依赖客户端标记。
4. 历史主体映射的唯一性、证据引用、旧 subject 历史读取与“不按同名授予角色”。
5. 最后管理员判定必须在写事务内对账号行加锁复核；计数与目标标记只来自事务内查询，冲突时返回 `accountUpdateConflict`，客户端输入不参与判定。
6. 挑战/验证的尝试次数、绝对与空闲过期、单次使用、会话绑定、operation/target/revision 绑定；命令操作还须匹配 `assessmentId`、`riskAssessmentVersion`、`executionRequestDigest`、`executionProfileId/Version`、`actualCommandDigest`、`policyVersion` 与 Incident。
7. 服务端按 operation 策略派生所需方法；`completedMethods` 只累计当前挑战已通过的步骤；仅当 password 与 TOTP 均通过时才产生 `loa:2` 与 step-up 会话。
8. 每个高风险写在其自身事务内执行并记录 `writeRecheck`；验证响应不得预写未来的业务事务结果，复检失败必须整体回滚。
9. 管理员 MFA 重置的目标登录名比对、凭据/会话版本递增、恢复码失效与审计同事务提交；`sessionsRevoked` 可以为 0，不能用人数断言代替版本更新。MFA 创建后状态读取不得再次返回 provisioning secret。
24. 管理员密码重置必须校验 `expectedRevision` 与 `expectedCredentialVersion`，写入新的初始密码、强制下次登录改密并递增会话版本；响应不得包含密码原文。
25. MFA 确认顺序：先以当前密码+TOTP 完成绑定该 enrollment 与当前会话的挑战，再校验新 TOTP 并签发恢复码；不允许仅凭已登录 Cookie 完成绑定。
26. 恢复码验证按请求中的 `challengeId` 定位当前挑战，并校验 `expectedChallengeRevision`；过期、已使用或不属于当前会话的挑战直接拒绝。
27. `resultKind` 决定总体口径：日志类才计算日志总体与分桶；指标/资源/硬件类使用 `not_applicable`，不得人工构造日志计数，`complete` 只表示该类型结果自身精确。
10. 观测窗口 `to > from`、最大窗口不超过来源 `maxWindowSeconds`、时区合法；资源 UID/namespace UID 与登记表一致，重建资源的新 UID 不得命中旧结果。
11. 来源登记为 active、凭据版本为当前版本、资格摘要匹配；否则拒绝或标记 stale。
12. 查询状态机与迟到结果丢弃；取消回执未确认时不得显示“已终止”。
13. 读取历史结果时重新授权并重检来源资格，过期结果返回 `result_expired`。
14. `population.count` 必须来自总体查询（聚合/全量扫描），不得由样例条数、`returned` 或截断状态推导。
15. `baseURL` 继续服从现有 Profile 约束（http/https、`/v1`、私网/回环或集群域名、无用户信息/查询/片段）；凭据写入 OpenBao 受控路径且不回读。
16. 手工上下文只能收紧；无法证明上限时保持未知，不得以手工值扩大真实能力。
17. 新 Job 创建时冻结 Profile 引用；API、Go Worker、Python dispatcher 三方比对一致；Worker 心跳有效期过期即不可提交。
18. 连接测试不得写入或变更准入证据；质量评估单独记录，不因连接或机制通过而标记通过。
19. 凭据 `keep` 不得触碰 OpenBao，`replace` 写入新的受控版本；新建连接必须 `replace`，响应与审计都不得包含密钥原文。
20. `contextWindow` 为 `null` 时才由平台探测；探测结果、来源与上限只写入响应投影。手工整数大于已测上限时按上限收紧，不得扩大。
21. 资源身份解析必须按对象实际 scope 生成 `namespaceScope`：Node、PV、PhysicalServer 等 cluster-scoped 对象不得带 namespace 名称或 UID；namespaced 对象必须同时校验名称与 UID。
22. 只有 `completed` 查询发布结果；`terminated`/`failed`/`denied`/`running`/`expired` 的读取返回空引用，迟到结果直接丢弃，过期读取返回 `result_expired` 而不是旧结果。
23. 结果状态由真实总体推导：`complete` 需要精确总体计数 ≥1，`no_data` 需要精确测得 0 且无样例，`partial`/`degraded` 的 `exact` 必须为 false；空样例页只表示未返回样例，不能改变总体。

## 七、需主线程同步 `contracts.csv` 的拟新增操作

以下操作已于 2026-10-21 同步到 `contracts.csv`（C-168～C-176），仍为拟新增、未实现；同步时已补 `primary_task`、授权、请求/响应键与“未实现”说明：

1. `POST /api/v1/admin/subject-mappings`（P2-04，管理员 + 再次验证）
2. `GET /api/v1/admin/subject-mappings`（P2-04，管理员）
3. `POST /api/v1/auth/mfa/enrollments`（P2-03）
4. `POST /api/v1/auth/mfa/enrollments/{enrollmentId}:verify`（P2-03）
5. `GET /api/v1/auth/mfa/enrollments/{enrollmentId}`（P2-03）
6. `POST /api/v1/auth/mfa/recovery-codes:verify`（P2-03）
7. `POST /api/v1/admin/users/{userId}/mfa:reset`（P2-03，管理员 + 高风险再次验证）
8. `GET /api/v1/observability/queries/{queryId}/results`（P3-02）
9. `GET /api/v1/model-run-options`（P5-03，运维授权能力投影）

C-012/C-013（step-up challenge 创建与验证）与 C-050～C-054 已在清单中，本次只补版本化请求/响应合同，不新增操作。R5 已按实际 Schema 修正 CSV：C-051/C-052 使用 `credential{keep|replace}` 与整数/null `contextWindow`，C-170 不含 `confirmExplicit`，C-173 必需 `stepUp{challengeId;sessionRef}`，C-175 必需 `challengeId`；未放宽 Schema 迁就旧 CSV。

R6 表单复用说明：`native-password-form` 被 self/first/reset 复用，但 Schema 不合并。self/first 走 C-011（`passwordChangeRequest`/`firstPasswordRequest`），SL-094/370 reset 走 C-017（`adminPasswordResetRequest`，userId 在 path，initialPassword，双 revision）；SL-005 first 固定原型缺 `currentPassword`，作为 P2-02 补充控件/状态，不放宽 Schema。逐片绑定见 `evidence/password-form-bindings.csv`。

## 八、验证方式与未验证声明

新增的 `test/contract/ui_v12_contract_test.go` 使用既有 `internal/contract.Validate` 对四个 Schema 的正例、反例做静态校验，并交叉核对 `evidence/p0-schema-inventory.json` 与 `evidence/p0-schema-rejections.json`，防止 Schema 与证据漂移。每个反例先确认基线合法，再只做一处变更并必须被拒；根 Schema 另有独立测试，确保 `$id` 本身不能作为通配符接受任意负载。执行命令：

```bash
cd platform && go test ./test/contract -run 'TestUIV12' -count=1
```

当前结果：4 个 Schema、55 个正例、87 个反例全部通过；`TestUIV12RootRejectsUnscopedPayloads` 对 `{}`、`{"schemaVersion":"x"}`、字符串、数组与 `null` 均拒绝。该测试不连接数据库、Kubernetes、模型或浏览器，只证明合同与静态拒绝路径成立。所有涉及功能仍为 `not_verified`：

- 原生账号、会话、CSRF、主体映射、双角色、最后管理员：P2-01～P2-04 未实现。
- 密码 + TOTP 再次验证、MFA 设置确认、恢复码、管理员重置：P2-03 未实现；现有 `/auth/step-up-sessions` 仍依赖旧提供方会话。
- 独立观测 create/get/results/cancel 与来源资格：P3-02 未实现；现有 analytics 为内部控制面，浏览器不可直接调用。
- 模型连接、OpenBao 受控写入、准入关联与新 Job 路由：P5-03 未实现；现有 `modelprofile` 仍为启动时冻结的单一 Profile。
- 未运行迁移、未生成客户端、未改 OpenAPI；未恢复虚拟化/Trace，未触碰根 00～10 文档与历史证据。

## GPT运行核实后的增补（2026-10-22）

- 管理写入目标补充 `write`（HTTP方法、精确路径、规范JSON body的SHA-256）。服务端在幂等回放之前和实际业务事务内重算并验证，避免同一对象/revision的其他动作或字段借用证明；请求头 `X-Ops-Step-Up-Challenge` 只定位挑战，不自授认证强度。原有operation/对象/版本要求继续有效。来源PATCH/rollback先接入，其余调用方按依赖继续。
- `sessionView.mustReenrollMfa` 由持久账户状态决定。恢复或管理员重置后，仅可设置MFA、读取本人的认证状态与退出；不返回业务grants，服务端同样拒绝业务接口。确认新因子后解除，恢复码不升级LoA2。
- 恢复失败响应可带 `challengeRevision`/`challengeState`，失败预算在账户维度跨挑战持久保留15分钟，不能通过重建挑战归零。挑战因子响应补充 `remainingAttempts`。
- 既有MFA替换：旧因子在新因子确认成功前保持有效；挑战验证当前密码及旧因子，确认请求另验证新因子。初次绑定仍验证密码及新因子。

### 实施补充：尚未创建对象的操作绑定

管理写入新增administrative_write目标，仅含kind与write(method/path/bodyDigest)。不伪造对象ID、revision=1或命令assessment；真实预期revision、不可变版本/摘要和作用域在规范化请求内绑定，业务事务继续检查。现有source_registration等目标保持兼容。MFA设置、恢复、重置及命令不接受此目标。执行配置退役保留C-139专用接口。密码类管理请求不可把低成本密码摘要持久化为证明，后续实现须排除秘密字段并保留目标/版本绑定。

### P2-04原生用户实施补充（r8，运行未验证）

- C-014～018源码路由已声明；C-019原生模式只列原生授权，旧主体只在历史映射专项中处理，不复制为原生权限。
- C-021原生模式使用userId、expectedRevision（当前authorizationVersion）、status=disabled和reason，撤销精确binding；重新授予使用C-020。旧OIDC调用保持旧status-only合同。
- 密码请求中的initialPassword先以当前已验证会话CSRF token为HMAC-SHA256密钥替换为hmac-sha256值，再计算规范化JSON的write.bodyDigest；HTTP写入在同一会话重新计算。证明和幂等表不持久化可独立猜解密码的快速校验值。其他管理操作的摘要不变。
- 原型的密码10～128字符与可选首次改密被原生安全合同覆盖为12～256 Unicode字符、创建/管理员重置后强制改密；模态验证是安全补充状态，单独进行视觉/交互验收。

### r9源码演进：C-170/C-171

现已在OpenAPI、正式Foundation native分支、identity SQL/仓库和用户管理界面声明接线，故当前清单改已有-新增；此状态仅指源码存在，不代表运行通过。C-170要求精确userId/legacySubject/basis=verified_inventory/evidenceDigest/expectedUserRevision，并以实际请求完成再次验证；清单先由专用migration-duty受控导入且逐项匹配。无confirmExplicit、同名推断或旧角色复制。C-171仅原生管理员当前会话授权分页读取。源码迁移55/56未执行；历史读取范围复检、全栈及产品六维仍待验收，详见subject-mapping-runbook.md。

### r10源码核对：C-134

执行配置列表原生管理员遗漏已在action.ListProfiles改共享当前会话/角色事务内复检；独立operator仍需当前原生角色和目标范围，不因管理员身份获得执行权限。现有OpenAPI的cursor/limit声明与处理器分页不一致，本轮保留为未完成缺口，不把已有-复用当成字段/运行验收。C-139专用退役保持。原生chart配置与正式安装器支持范围见native-deployment-runbook.md；无新增API操作，仍177项。

### r11源码演进：C-134分页及角色版本保持

C-134补权限过滤后的分页与meta.nextCursor：默认limit100、最大200，游标最大128，精确区分同profile ID不同version；锚点退役/失权后400，需回首页重取。OpenAPI与生成客户端同步，GET支持原生Cookie；真实DB/浏览器及大目录性能仍未验收。55的独立角色版本同步现仅作用于变更前有效版本，防止旧active但失效角色复活；新增SQL集成断言仅编译，55/56未执行。本轮不新增接口，仍177项。
