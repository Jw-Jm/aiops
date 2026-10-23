# P2-01 原生身份与受限首管理员初始化（代码交付）

状态：**P2-01 数据/初始化范围已完成实际隔离 DB 验证；P2-02~05 与 native HTTP/CSRF 未实现。** 全新 PG17.11 实例已执行迁移 1..44、NOINHERIT 职责、FORCE RLS、复合 FK、并发初始化和真实 CLI；未切 OIDC/Web/Worker 运行链，未触碰 15449 Fixture 或共享库。产品六项仍为 `not_verified`。

## 代码边界

- 前滚迁移：`migrations/00044_native_identity.sql`（迁移 44，无 Down）。
- 数据/凭据域：`internal/identity`（登录名规范化、Argon2id 固定参数、首管理员仓储、受限查找/失败计数仓储）。
- 受限本地初始化：`opsctl bootstrap native-administrator`（`cmd/opsctl/bootstrap_native_administrator.go`）。
- 数据库边界测试（默认跳过）：`test/integration/native_identity_test.go`。
- 已同步当前交付基线：`docs/release/delivery-baseline.json` 的迁移 latest/count 为 44。

## 初始化与职责路径

- `CreateFirstAdministrator` 先做输入与 Argon2id 计算，再打开一个事务；事务内先 `SET LOCAL ROLE migration_role` 并核对 `session_user != current_user`、`current_user = migration_role`、登录为 `NOINHERIT/NOSUPERUSER/NOBYPASSRLS`。
- 随后对 tenant 加 `pg_advisory_xact_lock`，先查初始化标记，再按立即 FK 可行顺序写：`accounts` -> `credentials` -> `platform_admin` grant -> `administrator_initializations` -> 审计。任何一步失败整体回滚，不残留账号、凭据、grant、初始化标记或审计。
- 并发同 tenant 被 advisory lock 串行化；后来者读到标记后返回 `ErrAlreadyInitialized`。全局唯一 login 让跨 tenant 复用返回 `ErrLoginNameUnavailable`。
- CLI 先调用 `identity.RequireNativeBootstrapDuty` 检查专用 LOGIN：`rolcanlogin=true`、`rolsuper=false`、`rolinherit=false`、`rolbypassrls=false`、`session_user=current_user`、member of `migration_role`。事务再次核对真实 `SET LOCAL ROLE` 路径。
- 迁移给 `migration_role` 精确授予 `identity` schema USAGE、identity 表所需 SELECT/INSERT/UPDATE 和审计 v2 函数执行权；没有把 runtime 连接提升为 schema_owner。

## 数据设计

- 所有 user 子表（credentials、password_attempts、sessions、role_grants、subject_mappings、administrator_initializations）都使用复合 FK `(tenant_id,user_id) -> identity.accounts(tenant_id,user_id)`；accounts 主键是 `(tenant_id,user_id)`，`user_id` 仍全局唯一。Tenant B 不能用 UUID 挂到 Tenant A 账户。
- `identity.accounts`：`tenant_id/user_id` 不可变；登录名先去空白再 ASCII 小写（无 NFKC/Unicode folding），库内仅接受 `^[a-z0-9][a-z0-9._-]{0,127}$`，全局唯一，防跨租户同名歧义；`subject` 由服务端生成为 `user:<user_id>`，不接受客户端授权主体；`display_name` 可改但不是授权标识。
- 版本围栏分离：`account_revision`、`credential_version`、`authorization_version`、`session_version` 各自独立列，不允许一个 revision 冒充所有撤销围栏。
- `password_state='change_required'` 与 `must_change_password=true` 用数据库 CHECK 绑定，禁止非法组合。
- `identity.credentials`：Argon2id 版本化编码，当前版本由 account 行指向；历史版本保留，不通过 Down 删除。
- `identity.password_attempts`：持久化失败计数、窗口与锁定时间；HTTP 登录限流策略在 P2-02 接入。
- `identity.sessions`：只存 SHA-256 token 摘要与独立 CSRF 摘要，二者唯一并绑定同一行；记录 `mode/full|first_password_only`、签发时 credential/authorization 版本、绝对/空闲到期、撤销时间和原因；明文 token/CSRF 不落库。
- `identity.role_grants`：operator 与 platform_admin 独立；同一租户同一用户同一角色只允许一条 active grant；scope 由服务端写入。
- `identity.subject_mappings`：legacy subject 只能按 `verified_inventory` + SHA-256 evidence 显式映射；不按登录名/displayName 自动继承；表只增不改。
- `identity.administrator_initializations`：每租户一行，作为并发首管理员初始化唯一闸门。

## Pre-auth 查找与 FORCE RLS

- `identity.find_login_account`、`record_password_failure`、`clear_password_failure` 是固定 `search_path` 的 `SECURITY DEFINER` 函数，无动态 SQL，均先 `set_config('app.identity_preauth','disabled',true)` 再设置精确操作名；客户端预设的 GUC 不会被信任。
- RLS 策略仍 ENABLE/FORCE。租户匹配外，只有 `identity.preauth_context_active(<精确函数>)` 才允许函数内部访问；该 helper 要求 `current_user=schema_owner`、`session_user!=current_user`、session user 是 `api_runtime_role` 成员且 GUC 精确匹配。没有 BYPASSRLS/SUPERUSER，没有移除 FORCE RLS，API/Worker 仍无 identity 表直接权限。
- 查找只按全局唯一 login 返回真实账户；失败计数和清除都先确认 `(tenant_id,user_id)` 对应真实非 archived 账户。`record_password_failure` 使用 `GREATEST(existing.locked_until,p_locked_until)`，不会把已有效锁降低或清空；`clear_password_failure` 只清除未生效/已过期锁。
- `schema_owner` 例外只存在于这些 SECURITY DEFINER 函数执行期间；runtime API/Worker 不获得 schema_owner，CLI 事务使用 migration_role，不是 schema_owner。迁移/CLI操作者可以在受控迁移中切换 schema_owner，但这不是 runtime 授权路径。

## SQL 权限边界

- 复用既有职责角色，不在迁移 44 内创建新角色（迁移 1 之后 migration_role 不具备 CREATEROLE）。
- `api_runtime_role` 只获得 `identity` schema USAGE 和三个 SECURITY DEFINER 函数执行权：`find_login_account`、`record_password_failure`、`clear_password_failure`；无任何 identity 表直接权限，不能读 hash/token 或跨租户查询。
- `migration_role` 是受控本地 bootstrap/migration 职责：显式获得 identity 表所需 SELECT/INSERT/UPDATE、`platform.tenants` SELECT 与审计 v2 函数执行权；仍受 FORCE RLS 约束，不能 BYPASSRLS。
- 受限查找函数固定 `search_path=pg_catalog,identity`，无动态 SQL，不接受任意租户上下文提升；`record_password_failure`/`clear_password_failure` 也固定 search_path。
- API/Worker 现有业务表授权不变；共享库配置不变。

## 首管理员 CLI

### 前置条件

1. 迁移 44 已由受控 migration 登录在**任务专属库**应用。
2. 目标 tenant 已存在且 `status='active'`；CLI 不会创建组织或同名主体。
3. 连接 DSN 使用专用 migration/bootstrap LOGIN（member of `migration_role`，非 superuser，非 BYPASSRLS，且没有 `SET ROLE` 后的 `current_user` 漂移）。普通 API/Worker/readonly LOGIN 会被拒绝。
4. 初始密码放在仓库外 0600 文件，内容为密码字节且不含换行；不使用命令行参数、不使用生产默认值。

### 输入示例（不是真实秘密）

`/secure/native-admin-input.json`（0600，仓库外）：

```json
{
  "databaseUrl": "postgresql://<migration-bootstrap-login>@<task-db-host>:<port>/<task-db>?sslmode=require",
  "tenantSlug": "example-task-tenant",
  "loginName": "root-admin",
  "displayName": "Task Root Administrator"
}
```

`tenantSlug` 与 `tenantId` 必须二选一。数据库密码建议放在 connection URL 所指的私密 pgpass/secret 文件或环境化 secret 管理中；不要把数据库密码或账号密码写进本 JSON、命令参数、终端历史或证据。

`/secure/native-admin-password`（0600，仓库外，无换行）：

```text
<generated-initial-password>
```

### 执行命令（本轮未运行）

```bash
chmod 600 /secure/native-admin-input.json /secure/native-admin-password
opsctl bootstrap native-administrator \
  --input /secure/native-admin-input.json \
  --password-file /secure/native-admin-password
```

成功输出只包含非秘密结果：

```json
{"tenantId":"...","userId":"...","loginName":"root-admin","subject":"user:...","accountRevision":1,"credentialVersion":1,"authorizationVersion":1,"sessionVersion":1}
```

失败不输出密码/hash/DSN。重复初始化返回 `administrator already initialized; existing account was not changed`，不会 upsert 或重置既有管理员。事务内插入初始化闸门、账号、credential、唯一 `platform_admin` grant 和审计记录；operator grant 不在初始化中授予。`must_change_password=true`；first password change 与 native session HTTP 属于 P2-02。

## 实际远程隔离验证（2026-10-21）

- 实例：mssc-direct 上新建 `/data/ops-ui-v12-native-20261021-r1`，容器 `ops-ui-v12-native-pg-20261021-r1`，仅 bind `127.0.0.1:15450 -> 5432`；复用固定镜像 `sha256:589f742565e4f6d22149176b032974f55f8201a8a37b71ed587ec5c6902e689f`（PostgreSQL 17.11）。
- 数据库：`ui_v12_native_identity`，owner `ui_v12_native_owner`，comment 精确 `ops.task=ui-v12-p2-01-r1; isolated native identity only`；私密材料只在该目录 `private/` 0600。
- 迁移：PostgreSQL 17 下 migration 1 由实例 bootstrap super 执行（创建/ALTER 全局 role 属性需要）；migration 2..44 由新建 `NOINHERIT/NOSUPERUSER/NOBYPASSRLS` migration LOGIN 执行，最终 schema 44。migration 44 曾因权限/audit schema USAGE/payload 审计规则失败，失败日志保留；修正后重建库完整通过。
- DB 边界测试：`TestNativeIdentityRepositoryAndRoles` PASS。覆盖 guard、NOINHERIT 职责、API/Worker 拒绝 bootstrap、并发一次成功/重复不重置/跨租户 login 拒绝、复合 FK、跨租户 FK/RLS 拒绝、GUC spoof 不授权、API 直接读拒绝、受限查找可用、失败计数/有效锁保留/清除、持久审计和 platform_admin-only。
- 真实 CLI：使用独立 synthetic tenant 和 `NOINHERIT` `ui_v12_native_cli`（member of migration_role）交叉编译二进制实际执行成功；重复执行返回 `administrator already initialized; existing account was not changed`，未改变既有账户。
- 非秘密结果：schema 44；六个复合 account FK；identity 三函数 owner schema_owner 且 security definer；active platform_admin grant 存在、operator 0；审计已持久化。测试多轮造成 4 个 synthetic account/初始化/审计记录，均为本实例测试数据。
- 证据：`evidence/remote-native-r1/`（成功、失败、SQLSTATE、CLI输出和最终非秘密验证）；远程同目录保留。最终源码包 SHA256 `2e7ff1544b74b764dc28606c152aeeb4a49967bc8b9b694cc2da14b2fe5811d8`；DB test 二进制 `92f28d49d61fc5df0328f3948328e3c819ba445b9f053cefad6a11c9ce48d129`；CLI 二进制 `ae5c8f704ca42ddd61951aa5348661cc20848b6ee2ff1a82a47a0aa24a40283d`。

## 私密材料与脱敏（2026-10-21）

- 本轮所有生成密码、DSN和CLI输入只保留在远程任务目录 `private/`，权限全部 0600；仓库和 `evidence/remote-native-r1/` 只留脱敏副本。
- 已将私密值与远程证据、本地 `remote-native-r1/`、`p2-native-identity.md`、`progress.md`、`task-progress.csv` 做精确比对；未发现凭据回显，因此未轮换。
- 失败轮日志保留原位，用于说明权限、审计 payload 与 lookup 扫描修正过程；证据中只有非秘密状态、角色属性、计数与错误码。
- 如后续复制证据或远程交接，必须重新做同样的精确比对；不得把 `private/` 原件放入 Git/evidence。

## 本地已验证（P2 R1）

```bash
go test ./internal/identity -count=1
go test ./cmd/opsctl -run 'NativeAdministrator|PrivatePassword' -count=1
go test ./test/contract -count=1
go test ./test/integration -run 'NativeIdentity' -count=1 -v
GOOS=linux GOARCH=amd64 go test -c ./test/integration -o /tmp/ops-ui-v12-native-identity-linux.test
```

最后一项显示 `TestNativeIdentityRepositoryAndRoles` 是 `SKIP`；skip 不是 DB 通过。本地只编译/跳过，不连库。DB 测试不会再复用通用 `newMigrationDatabase` 帮助函数，必须通过任务专属 guard。

## 远程复验说明（已执行一轮；复验需新任务实例/新 evidence）

只允许在 mssc-direct 的全新任务专属库执行；禁止共享库、Fixture 库或生产库。测试不再使用 `SP03_TEST_DATABASE_URL`。远程操作者必须提供：

```bash
export OPS_UI_V12_NATIVE_IDENTITY_DBTEST=1
export OPS_UI_V12_NATIVE_IDENTITY_TASK=ui-v12-p2-01-r1
export OPS_UI_V12_NATIVE_IDENTITY_DB_PORT='<task loopback port>'
export OPS_UI_V12_NATIVE_IDENTITY_ADMIN_DSN='postgresql://ui_v12_native_owner:<private>@127.0.0.1:<same-port>/ui_v12_native_identity?sslmode=disable'
export OPS_UI_V12_NATIVE_IDENTITY_BOOTSTRAP_DSN='postgresql://postgres:<instance-bootstrap-private>@127.0.0.1:<same-port>/ui_v12_native_identity?sslmode=disable'
go test ./test/integration -run 'NativeIdentity' -count=1 -v
```

Guard 在任何 mutation 前核对：DSN 只能 loopback、精确端口、精确库名/owner、无 query override、数据库 comment 精确等于 `ops.task=ui-v12-p2-01-r1; isolated native identity only`、owner 是 NOSUPERUSER/NOBYPASSRLS 的 CREATEROLE、服务端原生 5432 与 PostgreSQL 15+。随后创建的 migration/API/Worker/bootstrap LOGIN 都是 `NOINHERIT/NOSUPERUSER/NOBYPASSRLS`，并按真实 transaction/函数路径执行。

覆盖包括：迁移 44、能力角色属性、schema/table 权限、FORCE RLS、复合 `(tenant_id,user_id)` FK、同/跨 tenant 写拒绝、首管理员并发只成功一次、重复不重置、登录名全局唯一、只授 platform_admin、审计一条、API 直接表拒绝但受限查找可用、失败计数持久化、有效锁不被清空或降低。`skip` 只表示未执行。

## 前滚与恢复

- 迁移 44 没有 Down；不允许用 DROP/恢复旧凭据/整库快照撤回原生身份或撤销状态。
- 迁移只添加 schema/表/函数/授权，不修改既有 tenant、grant、step-up、业务数据、OIDC 配置或共享存储。
- 事务失败整体回滚；初始化闸门不会留下部分账号。若必须恢复，走新的前滚修正迁移并保留审计，不得 downgrade 或删除审计。
- 后续 P2-02~04 接入时不得放宽双角色、CSRF、credential/authorization/session 三围栏或事务内复检。

## 保留的失败归因

- `go test ./cmd/opsctl -count=1` 的 `TestBundleBuildCLIAndIndependentVerify` 仍报 `opa-sdk-source` corresponding-source lock 差异。本轮未修改 Bundle/OPA source lock；在上轮 dirty tree 中不能将其归因为 HEAD 既有失败，归因保持 unresolved。如需定论，只能在隔离只读 HEAD 副本复现，不 reset 当前工作树。

## 明确未验证

- 新实例 runtime schema 已为 44，且真实首管理员测试账户已初始化；这仅限该隔离任务实例。旧 15449 Fixture 与共享库仍为 43（本轮未改），delivery-baseline 44 是源码元数据。未初始化生产管理员。
- 未实现或切换 native HTTP login、session cookie、CSRF、MFA、first password change、账号管理接口。
- 未建立 legacy subject mapping 执行接口（P2-04）；表已预留但无执行调用者。
- 未验证 OIDC 运行链迁移或 Web 端到端；产品六项仍 `not_verified`。

## 当前独立验收结论（2026-10-22）

Codex使用当前源码独立编译，在mssc-direct新建/data/ops-ui-v12-native-20261022-codex-r2、独立容器及回环15451，以PG17.11从空库应用1..44并通过真实NativeIdentity测试。独立CLI同样首次成功、重复拒绝。前文逐轮未执行/待复核描述保留为历史；当前仅P2-01数据与初始化范围通过，证据evidence/codex-p2-review-r3.json，原生HTTP等仍未实现。新实例只供此任务验证，保留private/秘密与失败记录，不与15450/15449或共享环境混用。
