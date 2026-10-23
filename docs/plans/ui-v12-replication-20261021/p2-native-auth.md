# P2-02 原生登录、服务端会话与 Web 接入

状态：**in_progress（2026-10-22 GPT直接续作）**。独立正式API/Web预览已运行在 https://192.168.0.63:30462/login ，使用任务15452/PG17.11/schema46。真实Chrome已验证首次改密、刷新、退出撤销/重放拒绝，以及API重启后的会话持久化。正式Foundation原生路由漏接已修复，集成测试改用正式装配并通过。SL-001视觉仍failed；来源服务旧授权事务复检403，待P2-04/05，不宣称完整产品可用。运行/凭据读取见[preview-runtime.md](preview-runtime.md)，最新证据[evidence/preview-r1/result.json](evidence/preview-r1/result.json)。以下R1/R2及direct-r1为历史范围记录；30451和其他实例未改。

## 代码边界

- 前滚迁移45已执行并冻结；46修复过期限流窗口与清理失败计数的精确RLS。44未修改。当前SQL变更必须继续新增迁移，不改写已执行45/46。
- 服务端：`internal/identity/session.go`、`internal/auth/native.go`、`internal/httpapi/native_auth.go`。
- 配置/装配：`internal/app/bootstrap.go`、`internal/app/api_runtime.go`。
- OpenAPI/生成物：`api/openapi/platform-v1.yaml`、`gen/api/openapi.gen.go`、`web/src/api/generated/*`。
- Web：`AuthProvider.tsx`、`guards.tsx`、`Login.tsx`、`FirstPassword.tsx`、`AppShell.tsx`、`client.ts`。
- 测试：`internal/auth/native_test.go`、`web/tests/native_authprovider.test.tsx`、`web/tests/native_auth.test.tsx`、默认跳过的 `test/integration/native_http_session_test.go`。

## 迁移45（隔离实例已执行，现冻结）

- 新增 `identity.login_entry_attempts` 单行持久入口围栏，按固定窗口/阈值锁定；不按攻击者输入增长，API 无表权限。
- 新增 `identity.sessions.issued_session_version`（已有行保守默认 1），创建/查找/续期/撤销/改密都绑定该版本。
- RLS 只增加精确操作：login lookup 仅 `find_login_account`；已知失败仅 `record_password_failure`；session 操作仅 `find/touch/revoke/create/change`；入口围栏仅 `admit_login`。保留 ENABLE/FORCE RLS，无 BYPASSRLS。
- session 函数显式返回结果行：`revoke_native_session` 与 `change_own_password` 在设置 OUT 变量后 `RETURN NEXT`。
- `touch_native_session` 将 idle 截止封顶到 absolute 前 1 微秒，并同步封顶 `last_seen_at`，避免绝对到期边界触发 44 的 CHECK。
- `change_own_password` 在事务内 `FOR UPDATE` 旧 session+account，复检旧 session 未撤销、未过期、issued credential/authorization/session 版本；随后写新 credential、提升 account/session 版本、撤销全部旧 session、插入轮换 session，并返回结果行。
- known/unknown 失败共用入口围栏；known 失败由 `record_known_login_failure` 在 SQL 内阈值判定，锁定账户即使由错误入口触发也不放行。
- 新增 `resolve_cluster_ids` 与 `list_authorized_clusters`：只返回本 tenant active cluster registration 中的真实 cluster ID/UID/display name；不接受浏览器租户、角色或任意 ID。
- 新函数固定 `search_path`、SECURITY DEFINER、精确 preauth/session GUC；无动态 SQL。

## 服务端行为

- `HUMAN_AUTH_MODE=oidc` 缺省兼容；`native` 必须提供固定 `NATIVE_AUTH_TENANT_ID`；不回退 Bearer/OIDC，不接受客户端 tenant/role/subject。
- 登录先过入口围栏，再执行固定组织/规范化 login/Argon2id；未知 login 执行一次 dummy Argon2id，返回与错误密码相同 401；已知/未知都受持久围栏。
- cookie：`__Host-ops-session` HttpOnly/Secure/SameSite=Strict/Path=/，token 只在登录/改密响应出现一次；登录/改密先写 `Set-Cookie` 再写 JSON body；logout 先清 cookie 再写 body。
- DB 只存 token/CSRF 的 SHA-256 digest；CSRF token 由 session token 经 HMAC 稳定派生，所以页面刷新/GET session 能恢复同一 CSRF，不落库、不进 URL/localStorage/日志。
- 每次请求从 DB 复核 account status、credential/authorization/session 版本、绝对/空闲到期、撤销并续期；不是只信 cookie。
- middlewares 对所有 unsafe cookie 请求统一校验 exact Origin/Sec-Fetch-Site + CSRF；配置 `NATIVE_AUTH_PUBLIC_ORIGIN` 时以该显式 origin 为准，不信任 Forwarded 头；未配置时只接受直接请求 scheme/host。
- C-010 logout 使用 CSRF+expectedSessionVersion，服务端撤销/递增 session version/清 cookie，写审计。
- C-011 self/first 都要求 currentPassword/newPassword/expectedCredentialVersion；事务内锁行、复检旧 session 版本，成功提升 credential/session version、撤销旧 session、插入新 session；失败回滚。
- session 创建、改密、撤销与对应 audit 在同一 tenant transaction 内；audit 失败则操作回滚，不会只发 cookie 不落审计。
- login/auth 的响应均 `Cache-Control: no-store`；秘密不进日志/响应/审计/URL。
- `/auth/session` 的服务端视图恢复 CSRF token，并返回 `authorizedClusters`；Web 不再用管理员集群列表自行过滤。first_password_only 返回空 grants、空 authorizedClusters，middleware 仅允许 session/改密/退出。
- P2-03 未实现 MFA/step-up；ACR 固定 `urn:ops:loa:1`，敏感 step-up fail-closed。

## Web 接入

- `/runtime-config.json` 是唯一模式来源；请求失败/字段非法时不启动 OIDC、不静默降级，Guard 显示配置不可用。
- native 模式不加载 OIDC manager/userLoaded/accessTokenExpired，不显示 OIDC 按钮，不生成 Bearer。
- 登录/首改密/退出使用公开合同；首改密要求当前密码；改密和退出均带 recovered CSRF。
- 登录响应 `authorizedClusters` 直接用于 session 投影，不调用管理员 `getClusters`；角色/范围来自服务端 session。
- native expired/forbidden 事件与 OIDC 分开注册并在卸载时清理；过期/退出先递增 generation 并中止旧请求，迟到 refresh 不复活旧身份。
- native refresh 串行化；同 session 刷新不触发授权变更清缓存，服务器发现版本/授权变化才通知。
- 幂等键仅 mutation；不新增依赖，保留既有 epoch/SSE 取消。

## R2交付声明（历史，后经GPT复审补修B6/B7等漏接）

- B1：`NewNativeAuthHandlers` 安装 EntryLimit/EntryWindow/EntryLock 默认值；登录和改密都不再因零值 503。
- B2：改密 wrapper 返回 `passwordInput`，HTTP 事务收到真实 tenant/user/session/version/digest；不再发送零值。
- B3：middleware 从已验证 cookie 派生稳定 CSRF 并与 DB digest 比对后写入 `NativeSessionContext`；GET session 恢复同一 token，unsafe 请求仍校验请求头。
- B4：`clearCredentials(false)` 用于主动登录清理，不派发 expired；`expireIdentity` 仍是唯一过期/撤权递增 generation 的路径。真实 AuthProvider 测试覆盖 nativeLogin 成功、登出后迟到成功/失败。
- B5：cluster resolver/list 通过 `WithTenantTx` 设置可信 `app.tenant_id`；middleware 只投影 operator grants 的 cluster/namespace，admin scope 不并入 operator；`AuthorizedClustersForSession` 同样只取 operator。Web 的 clusterScopes/namespaceScopes.clusterId 使用服务端 authorizedClusters 的 registered internal ID，不把 UID 当 ID。
- B6：`record_known_login_failure` 全限定 attempts 列，account 使用 `FOR UPDATE` 串行化首次失败；改密与登录共用 entry admission，Argon 前先限流。
- B7：session 创建/改密事务内完成 `find_active_session`、operator cluster 投影和 `administrator_state`，失败整体回滚；`find_active_session` 补 `issued_session_version=account.session_version`。
- B8：`loadAuthMode` 兼容无 auth.mode 的既有 SP08 `{oidc,release}`，显式 native/非法配置仍严格；Helm 最小生产方补 `auth.mode=oidc`；`/login` 门控 loading/config/mode/session，默认 home 按角色，`safeReturnTo` 保留合法深链并拒绝外站/反斜杠。
- B9：`administrator_state` 只统计 active account + 当前 grant version，去掉 `greatest(...,1)`；schema minimum 改为 0；补 `REVOKE PUBLIC`。

## R2本地验证记录（历史）

- Go：`internal/identity`、`internal/auth`、`internal/httpapi`、`internal/app` 全部通过。
- 合同：`test/contract` 通过；delivery baseline latest/count=45。
- 集成：`TestNativeHTTPSessionFlow`/`TestNativeIdentityRepositoryAndRoles` 编译并 SKIP；skip 不是 DB/HTTP 通过。
- Linux 交叉编译：integration test 与 platform API 通过，摘要见 `evidence/go-tests-p2-r1.log`。
- Web：31 个 Vitest 文件 / 129 项通过，另 6 项 Node SSE 合同通过；TypeScript/Vite build 通过。
- 真实 AuthProvider/Login/generated 客户端测试（只替身 fetch）覆盖实际登录 URL/body、first-only、CSRF 改密、legacy OIDC、malformed mode、nativeLogin 成功及 logout 后迟到成功/失败；另有 cluster UID/ID 投影和 operator/admin 角色隔离测试。
- 上述均为本地；不是 live HTTP/DB/browser 验收。

## R2受限验证计划（历史，现已执行其中DB/处理链部分）

只允许全新任务专属实例；禁止 15449/15450/15451 和共享环境。

1. 从空库执行 migrations 1..45；migration 1 仍需实例 bootstrap super，2..45 使用 NOINHERIT migration LOGIN。
2. P2-01 CLI 建 synthetic tenant 首管理员。
3. 配置 `HUMAN_AUTH_MODE=native`、`NATIVE_AUTH_TENANT_ID`、`NATIVE_AUTH_PUBLIC_ORIGIN`、api_runtime DSN 和 runtime-config `auth.mode=native`。
4. 执行真实 HTTP：登录成功/失败、未知/已知失败入口围栏、已锁定账户、cookie flags/顺序、GET session CSRF 恢复、CSRF/Origin 拒绝、首改密旧 cookie 失效、logout 后不可用、audit 原子性、operator cluster UID→真实 ID、first_password_only 空 grants。
5. 通过后再评估 Web live；不把单元/合同通过写成产品六项 passed。

## GPT直接修正及当前验证（2026-10-22）

- 修复失败计数SQL返回列与Go查询不一致、active session漏查issued_session_version、入口限额少允许一次，以及测试退出后版本/固定token夹具问题。
- 首次真实复跑发现窗口过期后旧locked_until违反新窗口约束、45遗漏clear_password_failure的accounts RLS权限。保留45，新增前滚46；登录/改密清理失败计数与会话、凭据、审计在同一事务完成，移除吞错的提交后清理。
- 前端点击退出立即清理身份、中断旧HTTP/SSE；撤销请求使用独立的15秒截止信号，迟到退出不清除新身份。nativeLogin也受凭据取消信号控制；同一有效session刷新不误取消。
- 登录未知/锁定账号统一401及错误消息，走同一有界Argon验证成本路径；429用于入口整体限流。本人改密的已认证限流可以返回429。
- 统一原生响应header/body requestId，非秘密的实际tenant/user/request ID保留在最终测试日志。

当前真实环境：`ssh mssc-direct` → `mssc`，新目录`/data/ops-ui-v12-http-20261022-r1`，容器`ops-ui-v12-http-pg-20261022-r1`，仅`127.0.0.1:15452`，PG17.11、schema46。数据是隔离合成夹具，未作来源资格或产品数据证明。数据库名/owner/comment沿用既有严格测试guard的精确值，实例、目录、端口和标签独立，不指向旧P2-01实例。

已通过：
- 真实DB + HTTP handler/middleware：登录Cookie、恢复CSRF、首次会话无业务grants、Origin/CSRF拒绝、改密轮换、旧cookie拒绝、退出及重复退出审计计数。
- 真实注册UID/internal-ID映射、同名集群下双角色范围分离、namespace允许/拒绝、跨租户拒绝。
- 并发入口限额、首次并发失败精确计数、锁定/未知错误一致、账号及入口窗口重置、独立sessionVersion失效。
- 审计存储失败回滚密码与会话轮换、投影失败回滚新session、临近绝对到期touch及过期读取/改密拒绝。
- P2-01原生账号/初始化/NOINHERIT/FORCE RLS/GUC伪造边界在schema46回归通过。
- Go四包、完整合同检查、131项Vitest +6SSE、Web构建及Linux API/test交叉编译通过。

失败历史未删除：首次macOS AppleDouble归档错误；45下过期锁约束失败及旧44测试版本断言；复跑固定token唯一键冲突。SQL-only归档、前滚46及每次运行独立夹具分别解决，见`evidence/codex-http-r1/`。

### 重跑隔离DB/处理链测试

仅在上述专属实例操作，当前最终binary/source版本见`evidence/codex-p2-direct-r1.json`。不从共享库获取写授权，不把DSN输出到终端或日志，不source包含URL的环境文件：

```sh
ssh mssc-direct 'python3 -' <<'REMOTE'
from pathlib import Path
import os, subprocess
root = Path('/data/ops-ui-v12-http-20261022-r1')
env = os.environ.copy()
env.update({
    'OPS_UI_V12_NATIVE_IDENTITY_DBTEST': '1',
    'OPS_UI_V12_NATIVE_IDENTITY_TASK': 'ui-v12-p2-01-r1',
    'OPS_UI_V12_NATIVE_IDENTITY_DB_PORT': '15452',
    'OPS_UI_V12_NATIVE_IDENTITY_ADMIN_DSN': (root/'private/owner.dsn').read_text().strip(),
    'OPS_UI_V12_NATIVE_IDENTITY_BOOTSTRAP_DSN': (root/'private/bootstrap.dsn').read_text().strip(),
})
subprocess.run([
    str(root/'bin/native-http-codex-privacy.test'),
    '-test.run', '^TestNative(HTTPSessionFlow|IdentityRepositoryAndRoles)$',
    '-test.v', '-test.timeout', '10m',
], cwd=root/'source-r3/platform/test/integration', env=env, check=True)
REMOTE
```

这是实际DB+HTTP处理链验证，未启动正式API网络监听，不是可供用户登录的页面。夹具版本随测试重新生成，不能作为正式登录账号。

## 仍未验证/未实现

- 常驻native API/Web预览已存在（preview-r1 30462、preview-r2 30463），上文“未部署”只描述各自历史代码交付轮次；真实浏览器Cookie/TLS代理/刷新已执行，HTML像素比较仅完成SL-001，其余切片保持`not_verified`。
- 并发改密、退出与账号禁用的所有交错时序尚未穷尽；已检查SQL锁与事务复检，验证了到期/版本失效及失败回滚。
- 未进行统计时序侧信道测量；本轮只验证错误状态/消息一致及共同有界密码验证路径。
- P2-03 MFA/再次验证、P2-04用户生命周期与历史主体映射、P2-05全栈切换未实现；native LoA1敏感操作继续fail-closed。
- P1视觉/live未验；现有OPA source-lock失败归因仍未定，未修改无关锁。

## P2-02收尾与P2-04授权切片（2026-10-22，preview-r2）

SL-001右栏不再靠目测调参，而是按`shared/platform.css`实测值落地：表单网格gap 18px、标签行高19.8px加6px间距、输入框最小高度38px、tag高度24.14px、标题27px/1.65字重650、按钮43px圆角6px。选择器比antd的`:where()`复位更深，因为antd的adopted样式表排在`v12.css`之后，浅选择器会静默输给组件样式。

在同一台远程Chrome中把冻结原型以`file://`直接打开对照，除刻意缩短的tag外每个元素y/x/宽/高差值均为0.00；截图差异21/1363300像素（0.00154%），只遮罩登记的两处文案区（tag标签、页脚说明）。此前的`visual=failed`是真实的9.12px卡片高度差，不是纯文案差异。

`nativeLogin`现接受abort信号并在调用方取消后拒绝迟到成功，`Login`卸载即中止；退出按钮有稳定的可访问名称（`退出`），自动化不再依赖图标字形。

403根因确认并修在共享层而非逐个处理器：`internal/authz.RequireAdmin`对原生会话只用`identity.authorize_native_role`（迁移47，48修正）按会话授权版本并`FOR SHARE`复检，对历史会话只用`platform.role_bindings`，两者不互相回退。迁移七处调用点后暴露出第二个独立缺陷：迁移47用`ALTER POLICY`重写`identity.role_grants`策略时丢掉了`find_session`分支，导致会话读取返回`grants: []`，管理员路由仍全部403。迁移49恢复该分支后，真实浏览器`/api/v1/admin/clusters`返回200，且`platform.role_bindings`仍为0行，证明没有复制旧授权来伪装通过。

原生用户创建、角色授予/撤销、密码重置、会话撤销和历史主体映射仍未实现，因此还没有独立测试身份；这也阻塞首次改密的现场复验、`graph.EffectiveTx`的operator范围测试身份、MFA现场流程与入口围栏429状态。
