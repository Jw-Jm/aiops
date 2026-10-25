# r11 独立复审（2026-10-25）

后续处理：本报告保留r11复审时点的失败结论；3项问题已在 [r13修复与验收](codex-r13-three-fixes-e2e.md) 中修复并验证，历史证据不改写。

结论：**本轮返修尚未通过验收，发现3项需处理的问题。** 两项 OpenBao 问题已经用独立受控探针复现；一项数据库测试夹具问题由初始化调用链与迁移权限静态确认。未修复产品代码，未调用 CodeBuddy，未提交或推送。

## 复审范围与基线

- 工作树：`/Users/mssc/Documents/Code/ops/.worktrees/main-review-fixes-20261025`，分支 `fix/main-review-20261025`，主分支基线 `a2fa14f419abdc4587f56a1cbae59eb6cbb48c4e`。
- 对象：CodeBuddy 完成任务 `1a245441-f2aa-45b6-a5f1-b8d1da7e77c9` 的实际 r11 增量及关联调用方、测试、夹具和交付材料；覆盖 MR-005/007/008/010/013/015。MR-006 复用此前 Codex r12 独立修复及实测证据，不重复运行离线重建。
- r11 增量索引和补丁保存在该任务目录的 `codex-review.index`、`incremental.patch`。报告与执行者自述分开判断。
- 本次不是对 MR-001～020 全部问题的再次完整验收；总表中的其他“待审核”状态继续保留。虚拟化延期范围不变。

## 发现1 · P2 · 待提交文件未独立约束路径，符号链接可把私钥写入仓库

位置：[bootstrap.go:925](../../../internal/integrations/openbao/bootstrap.go#L925)、[bootstrap.go:1016](../../../internal/integrations/openbao/bootstrap.go#L1016)。

`EnsureBootstrapTLS` 仅校验 `caFile`，随后派生 `<caFile>.pending`。`readPendingMaterial` 将悬空符号链接的 ENOENT 当作“没有待提交材料”，`writePendingMaterial` 的 `os.OpenFile(O_CREATE|O_TRUNC)` 随后会跟随这个链接。

实际触发条件：CA 路径合法且位于受保护目录外；预先存在 `.pending` 悬空符号链接，目标是受保护仓库内尚不存在的文件。首次 bootstrap 生成身份时，叶私钥被写入目标仓库，突破明确的仓库/Bundle 外部保存约束。该问题需要已有本地符号链接条件，不声称是无前置条件的远程漏洞。

独立探针 `TestCodexReviewPendingSymlinkEscape` 已复现：fake kubectl 在发送前失败，返回提交未知状态；受保护的临时仓库内仍出现含 PrivateKey 的文件。所有密钥均在自动清理的临时目录内生成，没有向实际仓库写入私钥。

修改方案：对 pending 文件本身及解析后的父目录执行保护边界检查，并拒绝符号链接/非普通文件；写入须采用不能跟随已有链接的创建或安全原子替换方式。单独调用一次路径校验不能消除检查与写入之间的替换窗口。保留既有 CA 与待提交身份的重试语义。

验收：悬空链接和指向已存在受保护文件的链接均被拒，目标不创建也不截断；正常首次创建、未知提交后同一身份重试仍通过，权限保持0600。

## 发现2 · P2 · 成功后重试接受错误私钥，已有回归测试未覆盖此场景

位置：[bootstrap.go:1035](../../../internal/integrations/openbao/bootstrap.go#L1035)、[bootstrap_tls_test.go:218](../../../internal/integrations/openbao/bootstrap_tls_test.go#L218)。

成功 bootstrap 会删除 pending 材料。后续已有 Secret 分支仅验证 `tls.crt` 的信任链与服务名；私钥比较仅在匹配 CA 的 pending 尚在时执行。因此保留有效原证书、仅替换或删除 `tls.key`，仍返回成功，实际 TLS 服务却无法使用该证书和私钥组合。

独立探针 `TestCodexReviewExistingSecretKeyMismatch` 已复现：首次正常 bootstrap 后保留真实有效的证书，只将 Secret 私钥换成其他内容，第二次调用返回 nil。

现有 `TestEnsureBootstrapTLSRefusesKeyMismatch` 在正常成功且 pending 已删除后，用 `Y2VydA==`（内容为 cert）替代原证书。它会因证书解析失败被拒，无法证明私钥不匹配会被拒，造成测试假阳性。

修改方案：每次接纳已有 Secret 前，都解码证书和私钥并使用标准库 `tls.X509KeyPair` 校验证书/私钥匹配，再保留 CA、SAN 和已知 pending 身份比较。回归测试从 fake Secret 读取真实有效证书，仅改变私钥；补充缺失私钥情况，并断言具体错误原因，避免以任意 OPENBAO 错误充当成功覆盖。

验收：有效原证书配错误、缺失或格式错误的私钥均被拒；正确证书/私钥对在 pending 已删除后仍能幂等成功。

## 发现3 · P2 · MR-005 数据库夹具跳过特权初始化，无法验证关系查询

位置：[sp05_observed_relations_test.go:21](../../../test/integration/sp05_observed_relations_test.go#L21)。关联：[migrations_test.go:425](../../../test/integration/migrations_test.go#L425)、[sp05_finding_test.go:31](../../../test/integration/sp05_finding_test.go#L31)。

新测试从 `newMigrationDatabase` 获取空数据库后直接调用 `runRemainingMigrationsAsMigrationRole`。后者创建隔离登录并切换为 `migration_role` 执行全部未应用迁移，但没有先以管理员执行00001角色初始化，也没有给 `schema_owner` 对本次新数据库的 CREATE 权限。

在角色尚未建立的环境，GRANT migration_role 即失败；若已有共享角色，空库仍会尝试以无 CREATEROLE 的迁移角色执行00001，且后续 identity schema 创建还依赖新库 CREATE 授权。测试无法到达本应覆盖的跨集群、同名、UID、API group 与时间边界断言。

证据等级：静态确认测试初始化调用链和角色/数据库权限要求；本机未启动 PostgreSQL，不将推断写成已实测的 SQLSTATE。现有 `sp05DatabaseTimed` 已显式进行管理员版本1初始化与数据库 CREATE 授权，可作为复用入口。

修改方案：复用既有 SP05 数据库初始化夹具，或按其职责顺序完成管理员角色引导、版本1与当前数据库 CREATE 授权，再让受限 migration_role 前滚剩余迁移。确保完全新环境下角色尚不存在时也能引导，不扩大生产运行角色权限。

验收：真实空库执行该测试并到达所有关系断言；同时验证从已应用63版本的数据库前滚64～69。仅编译成功不能关闭此项。

## 其余复审结果

- MR-005 产品查询：cluster_id 精确限制、canonical 字面前缀、UID 与 API group、观测时间上界的实现方向可接受；真实数据库验收被上述夹具缺口阻断。
- MR-010：注册在启动前批量校验，注销归属 Worker goroutine 生命周期，重复登记保留故障状态；代码与本地 race 测试通过。现有 startup 测试后续健康状态包含手动上报，不能将它描述为完整真实采集失败/恢复链路的证明。
- MR-013：父页从会话、范围、绑定 revision、turn 派生归属，子请求在异步返回时核对 epoch；本轮未发现进一步确定性缺陷，真实 AssistantPage 定向测试通过。
- MR-007：18个历史 Down 精确摘要例外、仅前进 CLI、69版本发行基线及两架构源码准入静态检查通过；已应用1～63的跟踪文件未改。空库/63前滚尚未执行，历史原生前滚证据仍只覆盖原版本范围。
- MR-008：独立运行 `make check-generated` 通过。
- MR-006：引用 [r12独立修复记录](codex-r12-runtime-fix.md)；六个二进制空缓存离线重建一致的结论维持，原生执行与完整 Bundle 验收仍未完成。

## 独立验证及证据

具体命令与结果：[verification-summary.json](evidence/independent-r11-review/verification-summary.json)。

- Go race：app、observability、resourcestore、resource、openbao 五包通过。
- 前端定向：4文件、19用例通过。
- 交付合同及 bundle 包通过；迁移连续性、CLI仅前进和 native task scope 静态测试通过。
- `make check-generated`、`git diff --check` 通过；迁移源码 arm64/amd64 检查通过。
- 两个独立 OpenBao 探针复现上述缺陷。探针 PASS 表示断言当前错误行为成功，不表示问题修复。

可重放探针（仓库根执行，无须真实集群）：

```sh
go test -overlay "$PWD/docs/reviews/main-2026-10-23/evidence/independent-r11-review/overlay.json" ./internal/integrations/openbao -run '^TestCodexReview' -v -count=1
```

[探针源码](evidence/independent-r11-review/bootstrap_probe.go.source) 通过 Go overlay 注入，不修改产品源码或正式测试文件，不含实际密钥。

## 停止边界

按照用户要求，发现问题后不再派发 CodeBuddy。本轮只完成独立复审与报告落盘，不自动开始修复。没有执行真实 PostgreSQL、Kubernetes、浏览器端到端、mssc-direct 远程验收；其他 MR 的待审/业务 fixture、全量前端冻结原型路径失败、最终集成与提交仍保留待办。本报告不能作为全部20项已关闭的证明。
