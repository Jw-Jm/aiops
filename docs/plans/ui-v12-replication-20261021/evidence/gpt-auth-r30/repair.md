# r30 返修（2026-10-27，依据独立复审 changes_required）

本轮只做复审列出的五项缺口，不重跑已通过的改密/MFA 重置/r28 审计 Worker 链路，不改迁移 1-70，不放松任何身份/CSRF/作用域/事务复检，不动 `v12.r24.admin` 与既有 member。

## 1. 敏感截图：隔离、失效与安全捕获

- 复审发现 `03-mfa-enroll.mjs` 等脚本在 otpauth URI 或恢复码可见时整页截图。17 张 `mfa-*` 已由复审移入旧任务 `private-evidence-quarantine`（目录 0700/文件 0600，清单 `quarantine.json`）；本轮**未打开、未展示、未复制回仓库、未提交给模型**。
- 仓库内该批次图片已不在：`shots/` 仅剩 13 张无秘密状态（登录空闲/加载/400/网络中断、首改密与错误当前密码、我的账号与修改密码、受限页、退出后、账号安全管理员页）。文本证据（`runs/*.json`、`runs/*.log`、`*.md`）经 `otpauth://`、base32、`XXXX-XXXX`、6 位动态码四类模式扫描，无命中（UUID 命中的是 base32 正则误报，非秘密）。
- **泄露材料失效**：无法逐图判定，因此把 9 个 `r30.accept.*` 验收身份全部经真实管理员 UI（密码+TOTP 操作证明）重置 MFA。只读核对：九个身份现在都是 `mfa_reset_required=t`、活动登记 0、未用恢复码 0、活动会话 0；任何隔离图片里出现过的 URI/恢复码/动态码都已不可用。管理员与 member 未改动（`v12.r24.admin` 仍 1 个确认因子/10 恢复码，`r24.member.*` 未变）。回执与前后快照见 `containment.json` 与 `runs/quarantine-reset*.json`。
- **捕获策略**：新增 `harness/safe-capture.mjs` 与 `harness/screenshot-policy.md`。`safeShot()` 在截图前探测页面文本（`otpauth://`、`XXXX-XXXX`）与非密码输入框（6 位动态码、base32 密钥），命中即**拒绝写图并抛错**；本来就含秘密的状态（待绑定 URI、恢复码一次性面板）改用 `describeSecretState()` 只记录机检断言，不产图。未使用任何遮罩来"补一张通过图"。
- 安全重采（`runs/safe-captures.json`，图在远程 `r30-auth/shots-safe/`）：已启用态、取消绑定后、管理员账号安全页，均在探测为空后拍摄；故意对"待绑定 URI 可见"状态调用 `safeShot()` 以证明它会被拒绝。

## 2. SL-001 visual 旧 PASS 纠正

`acceptance.csv` 的 SL-001 `visual` 由 `passed` 改为 `not_verified`。旧 PASS 属于更早基线版本的双遮罩历史，r30 的整图对照明确不一致（差异全部落在演示凭据预填、演示标签与两行演示页脚），不能继承。现全表 visual `passed` 计数为 **0**，progress/结果文档同步改写，不再出现"visual 1"。

## 3. 自然到期脚本误用 pending 因子：结论拆分与改法

原 `04-mfa-expiry.mjs` 在账号已有确认因子时，把 secret 换成"新绑定的 pending 密钥"再 `totp()` 提交，却宣称提交了正确动态码——这是错误结论，已拆分：

- **站得住的部分**（原回执保留，逐条列）：取消绑定零 MFA 请求且清除密钥、既有因子不受影响；错误动态码保持挑战；挑战闲置 660203ms 后用提交请求得到真实 409；页面清除旧密钥与表单并可重新开始。
- **不成立的部分**：不能声称"提交了已确认因子的正确码"。该脚本的 409 发生在密码步，服务端在评估任何因子之前就因窗口失效拒绝；后续再绑定失败也因此不可用作证据。
- **DB 时间描述纠正**：不是"恰好 600 秒"。`created_at → idle_expires_at` 为 601 秒，因为密码因子推进后刷新了闲置窗口；正确的表述是"最后一次活动起 600 秒"。
- **替代实证**（脚本 `12-idle-expiry-confirmed.mjs`，独立身份 + 本 harness 生成的确认因子）：无活动 11 分钟后下一次提交返回真实 409 且 challenge 落 `expired`，DB 侧 `idle-expires_at - created_at = 600s`；随后在**新挑战**上用**同一个已确认因子**的正确动态码通过并达到 `urn:ops:loa:2`，证明 409 来自窗口而不是错误的秘密。

## 4. 15 分钟绝对到期与并发/跨账号（实测）

- **闲置到期**（`runs/idle-expiry-confirmed.json`，独立身份 A11）：建挑战后**完全无活动** 11 分钟 → 下一次提交 `409 CONFLICT`；DB 侧该 challenge `state=expired`，`created_at → idle_expires_at` **恰好 600s**。随后在**新挑战**上用**同一个已确认因子**的正确动态码 → `urn:ops:loa:2`，证明上一次 409 是窗口而不是错误秘密。
- **绝对到期**（`runs/absolute-expiry.json`，独立身份 A12，真实墙钟 948s）：t+9min 完成 password 方法（200），只读核对闲置窗口被推后 300s 并**被夹到绝对上限内 1 微秒**（`idle_expires_at = absolute_expires_at - 1µs`，即活动最多把闲置推到绝对截止前一微秒，不能越过）；`absolute_expires_at - created_at = 900s` 全程不变；t+15.8min 提交**已确认因子**的正确动态码 → `409 CONFLICT`，challenge 落 `expired`，UI 清除表单。全程只用真实时间与真实提交，没有 SQL 改时间/状态。
- **并发/跨账号**（`runs/cross-account-concurrency.json`，A12 与 A11 同时登录）：
  - 身份 B 用**自己的**密码验证 A 的 challenge → `404 NOT_FOUND`（不泄露存在性），随后 A 仍能正常完成 password 方法；
  - 同一 revision 上并发两次提交同一个一次性动态码 → 恰好一个 `urn:ops:loa:2`、另一个 `409`；
  - 已消费的一次性码在同一 challenge 上重放 → `409`；
  - B 在自己的 challenge 上并发两次密码提交 → 一个 200、一个 409（revision 栅栏）；
  - B 的恢复码提交到 A 的恢复挑战 → `200 verified=false`（不消费、不通过）。

## 5. 运行手册与 SP06 范围纠偏

- `native-deployment-runbook.md` 增补 r30 段：当前 Web 为 `ops-v12-native-r30-web-r14`（index SHA `8465ddec…`），API/Worker/调查器 r28，PG15454/schema70 未改；写明安全前滚/恢复边界（前端回滚只换容器、禁止回灌已撤销的会话/因子数据、截图策略、身份语义不回滚、数据库恢复后必须重跑）。
- 剩余范围表述纠偏：SP06 未完成限于**资源/业务工具路径**（如实时资源查询与命令执行工具链）；审计 `data_query` 真实链路在 r28 已通过，不再笼统写成"SP06 业务 Worker 未完成"。

## 6. 本轮之后的验收面

- 六维仍只认有实证者：visual 0（本轮把 SL-001 从 passed 纠正为 not_verified）、interaction 5、api 5、persistence 5、authorization 3、live 5；458 状态/177 合同结构检查通过。
- 未通过/未验项目单独登记：SL-001/002/005 整页视觉（演示数据与登记的安全扩展字段）、operator 真实范围（无合法集群，不伪造注册）、正式不可变 OCI/Bundle 与离线切换、SP06 资源/业务工具路径、完整 Trace 与虚拟化（继续延期）。
