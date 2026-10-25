# 原生账户 MFA 重置与断链恢复

本运行手册覆盖两件事：

1. 当前行为：管理员重置目标账户的第二因素后，**原密码保持不变并且仍可登录**，登录后只进入第二因素重新登记门禁；登记完成后原有授权自动恢复。
2. 历史断链：修复前已经出现过“账户凭据版本与密码行断链”的账户，用**租户限定的只读诊断**确认，用**现有受审计的管理员密码重置**恢复。

相关实现：`migrations/00064_native_mfa_reset_credential.sql`（重置函数）、`internal/identity/mfa.go`、`internal/httpapi/native_mfa.go`。

## 1. 正常重置流程（修复后）

1. 管理员对目标账户发起 `mfa_reset` 的 password+TOTP step-up，再调用 `POST /api/v1/admin/users/{userId}/mfa:reset`。
2. 同一事务内：目标账户行锁 → 读取当前版本的有效密码散列 → 写入 `credential_version + 1` 的新凭据行 → 退役旧版本 → 递增账户凭据/会话版本 → 撤销全部会话、step-up 证明、pending 挑战 → 撤销第二因素与恢复码 → `mfa_reset_required = true`。
3. 目标用户用**原密码**登录（`201`），会话 `mustReenrollMfa = true`、`authorization.grants = []`，业务接口返回 `403 MFA_SETUP_REQUIRED`。
4. 用户完成 TOTP 重新登记（`POST /api/v1/auth/mfa/enrollments` → `step-up-challenges` 的 `mfa_enrollment` → `enrollments/{id}:verify`）后，`mfa_reset_required` 清除，原有角色授权立即恢复，无需管理员再次改密。

不变量：

- 账户 `credential_version = N` 时必须存在且仅存在一条 `status = 'active'` 且 `credential_version = N` 的凭据行；
- 旧版本凭据行必须是 `retired`，不得继续登录；
- 版本号只增不减；
- 已撤销的会话与 step-up 证明不得复活。

## 2. 识别断链账户（租户限定、只读、可执行）

断链表现：账户当前版本没有对应的有效密码行，登录统一返回 `401 UNAUTHENTICATED`（与“密码错误”不可区分），`identity.mfa_state` 返回 `reset_required`。

权限与上下文（关键）：

- `identity` 相关表对 `migration_role` 只有受控的 SELECT/INSERT/UPDATE，并且全部 `FORCE ROW LEVEL SECURITY`；
- **必须显式设置 `app.tenant_id`**，否则在 FORCE RLS 下看不到任何行（这不是“没有断链账户”，而是看不到）；
- 诊断只读，不需要 `schema_owner`，也不修改任何数据。用实例受控的迁移值班连接（`migration_role`）在单事务内执行：

```sql
BEGIN;
SELECT set_config('app.tenant_id', '<TENANT_UUID>', true);

-- 当前版本没有有效密码行的账户（断链）
SELECT a.user_id,
       a.login_name,
       a.status,
       a.credential_version,
       a.mfa_reset_required,
       a.updated_at,
       (SELECT count(*) FROM identity.credentials c
         WHERE c.tenant_id = a.tenant_id AND c.user_id = a.user_id
           AND c.credential_version = a.credential_version
           AND c.status = 'active') AS active_rows_at_current_version,
       (SELECT max(c.credential_version) FROM identity.credentials c
         WHERE c.tenant_id = a.tenant_id AND c.user_id = a.user_id) AS max_credential_version
FROM identity.accounts a
WHERE a.tenant_id = current_setting('app.tenant_id')::uuid
  AND a.status <> 'archived'
  AND NOT EXISTS (
    SELECT 1 FROM identity.credentials c
     WHERE c.tenant_id = a.tenant_id AND c.user_id = a.user_id
       AND c.credential_version = a.credential_version
       AND c.status = 'active');

-- 单个账户的凭据版本分布（确认没有版本 N 的行、旧行是否仍是 active）
SELECT c.credential_version, c.status, c.created_at
FROM identity.credentials c
WHERE c.tenant_id = current_setting('app.tenant_id')::uuid
  AND c.user_id = '<USER_UUID>'
ORDER BY c.credential_version;

COMMIT;
```

租户隔离的现场核对必须**绑定同一个目标 user**：把上面 `WHERE … a.user_id = '<USER_UUID>'`（或第二个查询）保持目标不变、只把 `app.tenant_id` 换成另一个租户，必须返回 0 行。不要用“换租户后整租户列表为空”作为隔离证据——那只能说明查询按租户过滤，不能说明目标账户不可跨租户读取。

需要审计上下文时，用只读的 `api_runtime_role` 会话（同一租户 GUC，只读）：

```sql
BEGIN;
SELECT set_config('app.tenant_id', '<TENANT_UUID>', true);
SELECT record_id, created_at, subject, record
FROM audit.records
WHERE tenant_id = current_setting('app.tenant_id')::uuid
  AND event_type = 'identity.mfa.reset_by_administrator'
  AND entity_id = '<USER_UUID>'
ORDER BY created_at DESC;
COMMIT;
```

## 3. 恢复：走现有受审计的管理员密码重置

断链账户的密码散列已经与账户版本脱钩，历史版本可能已经被多次重置/改密覆盖，**无法证明“哪一个旧散列才是用户当前密码”**。因此本手册**不提供**旧散列回填 SQL：那需要直接写 `identity.credentials`，既超出 API/值班角色的正常授权，也无法在多次重置后保证正确性。

统一恢复路径（已实现、有审计、可重复）：

1. 管理员先创建一次 `user_administration` 的 password+TOTP step-up，并把目标绑定到这次具体写入。`password_reset` **不是**允许的 step-up 操作名；服务端 `internal/httpapi/native_write_stepup.go` 只接受 `user_administration`（角色绑定、原生用户管理、租户创建这一类写入），并要求：

   - challenge 的 `operation` 为 `user_administration`；
   - challenge 的 `target` 为
     `{"kind":"administrative_write","write":{"method":"POST","path":"/api/v1/admin/users/<USER_UUID>/password:reset","bodyDigest":"sha256:<digest>"}}`；
   - `bodyDigest` 是**规范化后请求体**的 SHA-256，其中 `initialPassword` 先用当前会话 CSRF 令牌做 HMAC：
     `"initialPassword" = "hmac-sha256:" + hex(HMAC-SHA256(csrfToken, 明文初始密码))`；
   - 实际写请求必须带 `X-Ops-Step-Up-Challenge: <challengeId>` 且 URL 不得带查询串；
   - 浏览器 UI 的“管理员重置密码”会按上述规则生成 challenge/header/digest；脚本化操作必须复刻同一绑定，不能只发裸请求体。

   请求体（写入时使用明文，绑定摘要里用上面的 HMAC 形式）：

   ```json
   {
     "initialPassword": "<新初始密码，满足密码策略>",
     "expectedRevision": <账户当前 account_revision>,
     "expectedCredentialVersion": <账户当前 credential_version>
   }
   ```

   该调用在数据库侧：校验两个版本 → 退役现有 active 凭据行 → 在 `credential_version + 1` 写入新散列 → `password_state = 'change_required'`、`must_change_password = true` → 清空登录失败计数 → 撤销全部会话，并写审计记录。
2. 用户用管理员交付的新初始密码登录，按 `first_password_only` 会话改密。
3. 如果账户仍处于 `mfa_reset_required`，完成 TOTP 重新登记后原有授权恢复。

恢复后核对（只读，同一租户 GUC）：

- `identity.credentials` 在账户当前版本上恰好一条 `active` 行，且旧版本均为 `retired`；
- `identity.mfa_state` 不再返回 `reset_required` 或已经完成重新登记；
- 第 2 节的诊断查询对该账户返回 0 行；
- 旧密码（包括用户记忆中“原来那个密码”）不再能登录，这是预期结果：断链账户的密码由管理员重置，必须由用户重新设置；
- 管理员密码重置只清空**登录**失败计数（`identity.password_attempts`），不会清除 `identity.step_up_failures` 的因子冷却。若该账户此前已把某个 step-up 因子打到 5 次失败，它仍需等待该窗口（最多 15 分钟）自然过期，或完成一次完整 password+TOTP 铸造后由 `step_up_session_mint` 清除。

## 4. 禁止事项

- 不由应用角色或值班角色直接 `INSERT/UPDATE identity.credentials` 来“补回”历史散列；
- 不把 `credential_version` 回退到旧值，不把历史 `retired` 行改回 `active`；
- 不恢复已撤销会话、step-up 证明或第二因素；
- 不以“最后一次 reset 之前的那一行”推断用户密码；
- 不为恢复而修改已应用的 `migrations/00044`、`00051`、`00052`；
- 不跳过 `app.tenant_id` 直接把空结果当成“无断链账户”。
