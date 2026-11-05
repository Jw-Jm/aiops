# r55：事件协同与调查准入的真环境验收

2026-11-05，对本轮部署到真环境（命名空间 `ops-native-v12-r24-20261026`，库 `ui_v12_r24_native`，schema 74）的 r55 API/Worker/Web 做 HTTP 级验收。语义对齐 `test/integration/sp05_collab_test.go`，数据库侧用 psql 交叉核对 timeline/audit/link/followup 事实。脚本 `acceptance.py`，原始结果 `acceptance-results.json`（32 项检查全部通过，3 项环境限制，其中限制 2 当日随后修复并复核通过）。修复与复核脚本 `admission-fix.py`，原始结果 `admission-fix-results.json`（14 项检查全部通过）。

## 部署对象

- 迁移：00073（incident 协同）、00074（调查准入），真库 72→74 前滚成功。
- API/Worker：r55 二进制（源提交 `378112cf8`），Pod 单副本替换并就绪；验收后于同日以含 `Allows` 修复的工作树重建并再部署（部署回执记录新 sha256，源基线 `d968eac39` + 本修复）。
- Web：r55 nginx 容器（宿主 30471 端口）服务正常。

## 验收账号与覆盖面

- `v12.r24.admin`：legacy `user:` 主体路径 + platform_admin（覆盖 override 分支）。
- `r31.op.1793086009209`：原生 `native:` 主体路径的 operator。
- `r24.member.*`：无任何授权，用于负例。

## 结果摘要（32/32 通过）

- **指派/转移**（事件 A）：自认领幂等（timeline/audit 不增）；他人认领 409 ASSIGNMENT_CONFLICT；过期 revision 409 STALE_CONTEXT；转移给无资格主体 400；转移给自己 400；admin→op 转移成功且 state/resolvedAt/recoveryKnownAt/suppressedUntil/revision 全部不变；非 admin 的原处理人再转移 403 FORBIDDEN；platform_admin override 转移允许；最终恢复 assignee=admin，revision 全程保持 2。
- **评论**：impact 落库于 comment timeline 条目，incident revision 不变。
- **跟进事项**：默认属主=创建人；指定无资格属主 400；无授权账号完成 403；属主完成成功且 DB state=done；重复完成 400；reopen 清空 completed_at/completer_subject/resolution_note；platform_admin override 完成允许；最终恢复 open。
- **告警关联**：唯一 finding 解除关联被拒 400（事件至少保留一个 finding）；不存在的 finding 404；过期 revision 409；以当前 revision 重新关联走 CAS revision +1，link 行不变，timeline/audit 各 +1。
- **调查准入门禁**：验收时 admin 与 op 创建调查均 403 FORBIDDEN 且不产生 Job 行；拒绝点在校验之后（错 revision 仍 400、不存在事件仍 404）。该统一 403 当日根因修复并复核通过，见下文限制 2。
- **执行列表**：SP07 为 configurationOnly，三账号统一 409 CAPABILITY_DISABLED。

## 环境限制（非本轮缺陷，未覆盖项）

1. **unlink 删除路径**：真环境每个 finding 都是其事件的唯一 finding，删除路径无法用 HTTP 在不破坏真数据的前提下触发；删除路径覆盖留在集成测试。
2. **调查准入（00074）——已修复并复核**：验收时的统一 403 是两层缺陷叠加：(a) 环境授权——全部 operator 授权均为命名空间级，而准入策略评估要求 cluster 级 scope（`evaluator.go` scopeAllowed），恒拒；(b) 代码缺陷——`graph.Scope.Allows`（`internal/graph/ariadne_adapter.go`）对 `ClusterScoped=true` 的 scope 仍要求命中命名空间列表，而原生授权模型中 cluster 级与命名空间集合互斥，导致任何原生授权态势下 namespaced finding 的准入在仓储层（`repository.go` CreateJob）必拒。注意：2026-10-27 库中 6 行调查 Job 是 schema_version `investigation-job/v3`、effective_scope 为空的种子数据（含无授权 member 提交），并非本仓库 HTTP 准入路径产物，不能作为"准入曾工作"的证据。修复动作：(a) 经真实管理 API（step-up 密码+TOTP）为 admin 授予 cluster 级 operator 授权，op 有意保持命名空间级作为拒绝数据点；(b) `Allows` 一行改为 cluster 级 scope 覆盖全集群（含 namespaced 资源），并补单元测试钉住语义。复核（`admission-fix.py`，14/14 通过）：admin 创建 202、同 Idempotency-Key 重放返回同一 jobId、新 key 同准入摘要自然去重（Job 数 +1）、op/member 仍 403 FORBIDDEN、Job 行携带 admin 主体/活跃策略/触发 revision、两条幂等键映射同一 Job、验证 Job 已取消清理。
3. **执行列表授权过滤**：SP07 configurationOnly 且 `action.executions` 0 行，按主体过滤无法在真数据上演示；覆盖留在 `TestSP07ExecutionListFiltersByAuthorization`。

验收副作用已复原：事件 A assignee=admin、revision=2；事件 B revision 4→5（仅 CAS 演示）；跟进事项全部 open；link 关系净不变。timeline/audit 中的验收记录按留存策略保留，不清理。
