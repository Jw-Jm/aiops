# r30：P2-02/P2-03 原生认证与账号安全整组复刻及真实持久化/权限验收（2026-10-27）

> **返修状态（2026-10-27 复审后）**：本目录的 17 张 `mfa-*` 截图已隔离且未回仓库，9 个验收身份的因子已全部失效（见 `containment.json`）；SL-001 的 `visual` 已从旧 PASS 纠正为 `not_verified`；`04` 脚本"提交了已确认因子正确码"的结论撤回，改由 `12-idle-expiry-confirmed.mjs` 与 `10-absolute-expiry.mjs` 分别实证闲置/绝对到期。逐项说明见 `repair.md`，截图规则见 `harness/screenshot-policy.md`。

沿用 main/082f87d66c5b、r23～r29 未提交改动。本轮不使用 CodeBuddy 之外的模型调用，不重跑 r28 已通过的审计 Worker 链路，不重采 r3 参考图，不恢复虚拟化/Trace/生产。所有数据库、Kubernetes、业务浏览器操作只在 `ssh mssc-direct`（hostname mssc / 10.10.10.2）执行。

## 1. 本轮范围

P2-02/P2-03 整组：原生登录（空闲/加载/错误/网络中断/限流/锁定/同源与 CSRF）、首次改密、自助改密、退出与会话撤销、MFA 登记-确认-恢复-再次验证-失效重启-取消、管理员 MFA 重置与强制重绑，以及刷新后的服务端持久回显与零权限失败；含必要的 P1 公共壳适配（面包屑、焦点环、对话初始焦点）。

## 2. 本轮修复的真实缺陷

1. **登录网络/传输失败文案误导**（`web/src/pages/Login.tsx`、`web/src/pages/FirstPassword.tsx`）：`NETWORK_UNAVAILABLE`/`INVALID_RESPONSE`/5xx 原先直接回显 API 客户端通用文案「接口连接中断，请先核对持久记录」——登录尝试并不写持久记录，且该文案可能被读成凭据结论。现改为登录/改密各自的可重试说明，且不改变任何服务端判定。
2. **登录成功后强制重绑 MFA 存在路由竞态**（`web/src/pages/Login.tsx`）：`nativeLogin` 返回 `/account/security` 的同时，`useAuth` 已写入会话，Login 自身的 `auth.session` 重定向可能先执行并落到普通首页（实测同一账号在 r30 与 debug 中分别落到 `/access/sources` 与 `/account/security`）。现两处目标一致：`mustReenrollMfa` 时一律进入账号安全页。回归：`tests/native_auth.test.tsx`「sends an already restored session with a forced factor reset to the security page」。
3. **账号安全页面包屑为「未匹配页面」**（`web/src/app/navigation.tsx`）：原型 NAV 冻结为 7 模块，`/account/security` 是原生账号扩展页，此前 `navGroup/navItem` 查不到即显示「未匹配页面」；同时顶层页面的 `navModule` 回落会把模块名重复一次（原型 SL-006 为「工作空间 / 运行总览」，实现会输出「工作空间 / 运行总览 / 运行总览」）。现扩展页登记为「账号 / 账号安全」，`navModule` 只对子项返回模块名。回归：`tests/v12/p1_shell.test.tsx`。
4. **焦点环与对话初始焦点偏离原型**（`web/src/design/v12.css`、`web/src/components/v12/Layer.tsx`）：原型 `shared/platform.css:62-70` 为 `3px solid #aaa7ef` + `2px` 偏移；实现继承旧 V1 的 `#208f89` + `3px`。原型对话是原生 `<dialog>`，打开时首个可聚焦控件（头部关闭按钮）获得焦点，参考图 SL-003/004/005 都能看到该环；实现此前由 antd 自行聚焦容器且无环。现 v12 容器统一原型焦点环，`V12Modal/V12Drawer` 初始焦点按「`[data-autofocus]` → 关闭控件 → 首个正文控件」落点，并在叠加层挂载完成后复核一次。回归：`tests/v12/p1_layer.test.tsx`。

以上第 1、2、3 项为行为/可用性缺陷，第 4 项为设计一致性缺陷；均未改动产品文案所依据的业务语义，也未放宽密码、CSRF、版本或会话策略。

## 3. 真实运行时与身份

- 预览 `https://192.168.0.63:30471`，本轮 Web 容器 `ops-v12-native-r30-web-r14`（`/data/ops-v12-native-visual-r24-20261026/web-r14`，index SHA256 `8465ddec2b4632b7cc5ac518f39506255c9a65b17b7a58e085fd217377075ba8`，构建归档 SHA256 `39bd606ef502affab37df9eb88fbdc1f2c5640381301f9bbfd3075ad1acc4c4b`）。r8～r13 容器停止保留。
- API/Worker/investigator 仍为 r28 已验收版本，PG17.11/15454、schema70、namespace `ops-native-v12-r24-20261026` 未改动；本轮无模型调用、无迁移。
- 新独立验收身份通过真实管理员 UI（密码+TOTP 操作证明）创建，零隐含角色、服务端强制首改密；`v12.r24.admin` 与 `r24.member.*` 的密码/MFA/角色未被改动，也未给管理员自动授 operator。凭据仅存远程 `private/`（0600），未写入本目录。

## 4. 逐状态真实证据

见 `runs/`：`02.log`/`login-first-password.json`（登录与首次改密）、`03.log`/`mfa-enroll.json`（登记确认）、`04.log`/`mfa-expiry.json`（取消/错误码/自然到期）、`05.log`/`recovery-permissions.json`（管理员重置与恢复码）、`07.log`/`self-password-boundary.json`（自助改密与零权限）、`08.log`/`lock-first-password.json`（锁定与首改密边界）、`09.log`/`csrf-entry-limit.json`（同源/CSRF/入口限流），截图在 `shots/`。

| 状态 | 真实证据 |
| --- | --- |
| 登录空闲 | 空白提交仅前端校验、零身份请求 |
| 登录加载 | 真实请求挂起期间唯一提交控件 `aria-busy=true` 且 disabled |
| 登录失败（400/401） | 统一文案「用户名或密码错误，或账号不可用。」；重试可用；不泄露账号是否存在 |
| 登录网络中断 | 真实 abort → 「登录服务暂时不可用，请稍后重试。」，不当作凭据结论 |
| 同源/CSRF | 跨源与无 Origin 登录 403；无/错/异源 CSRF 的登出 403，绑定令牌子 200 |
| 入口限流 | 第 N 次快速登录起 429 + `Retry-After`，窗口内正确密码同样 429；窗口后 201 |
| 账号锁定 | 5 次真实失败后正确密码仍返回与错误密码**完全相同**的 401（不暴露锁定）；服务端时钟到期后同一密码 201 |
| 首次改密 | 刷新仍为 `first_password_only` 且零业务请求；当前密码错误 401 保留会话；不一致/短密码不发请求；取消 200 → 旧 Cookie 401；真实改密 201 → 旧 Cookie 401、刷新仍为新会话 |
| 首改密权限边界 | 该会话对业务、MFA 状态、管理员用户接口一律 403 |
| 自助改密 | 错误当前密码 401；不一致前端拦截；成功 201、旧 Cookie 401、刷新为 full/LoA1 会话 |
| 退出撤销 | 账号菜单退出 200、旧 Cookie 401、刷新仍停留登录页 |
| MFA 登记 | 未启用 → 生成一次性 otpauth；刷新后服务端 `pending_confirmation` 且不再返回密钥 |
| MFA 再次验证 | 密码+动态码后服务端会话 `acr=urn:ops:loa:2`；错误密码/错误动态码均保持挑战、不漏成功 |
| MFA 确认 | 同密钥动态码确认 200 → 10 个恢复码仅显示一次；刷新后 `confirmed`、剩余 10、`confirmedAt` 持久 |
| MFA 取消 | 取消绑定零 MFA 请求、清除密钥、既有 confirmed 因子不受影响 |
| MFA 自然到期 | 挑战闲置 660203ms 后提交即得真实 409（服务端在评估任何因子前就拒绝，脚本当时提交的是待绑定密钥而非已确认因子，故该行不声称"正确码被拒"）；页面清除待绑定密钥与旧表单；DB 侧 `created_at → idle_expires_at` 为 601s，因为密码因子推进刷新了闲置窗口——准确表述是"最后一次活动起 600s"。修正后的实证见 `runs/idle-expiry-confirmed.json`（无活动 600s 整 + 同身份新挑战用已确认因子通过）与 `runs/absolute-expiry.json`（900s 绝对窗口） |
| MFA 再绑定 | 用已确认因子完成再次验证、用新密钥确认 → 200、10 个新恢复码、`confirmedAt` 前进、刷新后持久；旧因子的恢复码随后被拒 |
| 管理员 MFA 重置 | 目标 accountRevision/credentialVersion 绑定、200、撤销真实会话数；目标 `reset_required`、恢复码 0、登录后强制进入账号安全页 |
| MFA 恢复码 | 未知码 refused；一次性码 verified → 撤销全部会话、整套恢复码删除、`reset_required`；旧 Cookie 401；重新登录仍强制重绑 |
| 零权限失败 | `/settings/permissions`、`/overview` 受限页；`/api/v1/admin/users` 403、`/api/v1/resources` 403，本人安全状态 200 |

## 5. 视觉对照（1440×960/100%，复用 r3 参考，无掩码）

`visual-comparisons.json` 为本轮本地静态像素计算（参考图未重采、页面未改样式、未遮蔽）：

- SL-001 登录：整图 6703/1382400 像素不同（阈值 >20 者 4623），差异全部落在原型演示凭据预填、「内置账号 · 交互原型」标签与两行演示页脚；字段标签/输入框位置逐像素一致。
- SL-002 登录失败：错误条区域 [902,540,1260,600] 内 0 像素差异（逐像素相同）；整图 7639/5410，差异为真实账号名文本、标签与页脚文案，另有约 200 px 的按钮字形次像素抗锯齿差异。
- SL-003 我的账号：对话尺寸 660×423.8125、头部 76、关闭按钮 31×31（偏移 22/25）与参考完全一致；整图差异主要来自背景页（参考为 /overview + 页面滚动条，实现为零权限/配置页）。对话前景 20089/279840（>20 者 14064）；按参考滚动条导致的整体 7 px 水平位移对齐后为 12651/7278，剩余差异为真实账号数据、说明文案与页脚按钮字形。
- SL-004 修改我的密码：对话 660×492.625；前景 15628/325380（>20 者 11856），按 -7 px 对齐后 9257/5235（>20 者 1.6%），剩余为「密码」字段辅助文案差异（原型演示文案 vs 真实 12～256 策略）与字形。
- SL-005 首次登录改密：本页背景复用登录背景，整图差异仅落在对话内（207775/77696）；缺失来源是**登记在案的安全扩展字段「当前密码」**导致对话更高（660×492.625 vs 参考 660×408），因此该状态整页视觉不通过。

结论：SL-003/SL-004 的对话前景已达到可核对的一致度（残差可逐处归因），SL-001/SL-002/SL-005 的剩余差异为**演示数据/演示文案/已登记安全扩展**，不通过整页视觉，也不以遮罩或阈值放宽掩盖。

## 6. 未验与剩余

- 逐状态整页视觉（含 SL-001/002/005 的演示文案与扩展字段）仍未通过；未提升 SL-001/002/005 的 visual。
- operator 侧真实运行未覆盖：本任务环境无任何已登记集群，无法为验收身份建立合法 operator 范围，`/overview`、`/assistant` 的运营态只做了单元/逻辑核对，未做业务浏览器验收（未伪造集群注册或来源）。
- 入口限流/锁定窗口为真实服务端状态，但等待期内不接受登录，属受限实验；未做多实例并发压测。
- 15 分钟绝对到期已由返修补验（`runs/absolute-expiry.json`，真实墙钟 t+15.8min 拒绝正确已确认因子码）；本行原文「未单独等待验证」只对 r30 首次提交成立。返修同时纠正了该样本的毫秒截断：`idle_expires_at` 比 `absolute_expires_at` **早 1 微秒**（迁移 65 的 `absolute - interval '1 microsecond'`），不是数据库完全相等。
- 再绑定的首次尝试因测试脚本把「待绑定密钥」当作「已确认密钥」提交而失败（真实规则是：账号已有确认因子时，再次验证校验的是已确认因子），失败回执保留在 `runs/mfa-expiry.json`、`runs/mfa-expiry-attempt1.json` 与 `runs/rebind-a8.json`；修正脚本后 `runs/rebind.json` 通过。
- 正式不可变 OCI/Bundle、离线安装与切换、SP06 业务 Worker、完整 Trace、KubeVirt/CDI、生产发布仍不在本轮范围。
- P1 其余公共控件与 458 状态全量视觉、P3～P8 依赖项未在本轮推进；不以本组通过宣称 P1/P2 或 V1.2 完成。

## 7. 运行与恢复

- 回到本轮 Web：`docker start ops-v12-native-r30-web-r14`（先停止占用 30471 的容器，同 host 网络）。旧 `r29-web-r9`、`r30-web-r10..r13` 容器停止保留，镜像未删除。
- 回滚行为变更只需换回 `ops-v12-native-r29-web-r9`；本轮无迁移、无 API/Worker 变更，数据库无需前滚或恢复。
- 任务身份 `r30.accept.*` 为本轮验收身份，保留其状态用于复核；`v12.r24.admin`、`r24.member.*` 未改动。
