# r55：事件协同与调查准入的真环境验收

2026-11-05，对本轮部署到真环境（命名空间 `ops-native-v12-r24-20261026`，库 `ui_v12_r24_native`，schema 74）的 r55 API/Worker/Web 做 HTTP 级验收。语义对齐 `test/integration/sp05_collab_test.go`，数据库侧用 psql 交叉核对 timeline/audit/link/followup 事实。脚本 `acceptance.py`，原始结果 `acceptance-results.json`（32 项检查全部通过，3 项环境限制）。

## 部署对象

- 迁移：00073（incident 协同）、00074（调查准入），真库 72→74 前滚成功。
- API/Worker：r55 二进制（源提交 `378112cf8`），Pod 单副本替换并就绪。
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
- **调查准入门禁**：admin 与 op 创建调查均 403 FORBIDDEN 且不产生 Job 行；拒绝点在校验之后（错 revision 仍 400、不存在事件仍 404）。
- **执行列表**：SP07 为 configurationOnly，三账号统一 409 CAPABILITY_DISABLED。

## 环境限制（非本轮缺陷，未覆盖项）

1. **unlink 删除路径**：真环境每个 finding 都是其事件的唯一 finding，删除路径无法用 HTTP 在不破坏真数据的前提下触发；删除路径覆盖留在集成测试。
2. **调查准入去重（00074）**：本部署 `platform.role_bindings` 为 0 行，legacy 主体重证恒失败，且原生主体同样在策略评估被拒——调查准入对全主体关闭（与本轮"未启用模型/SP05 分析器"的环境状态一致；r55 对该路径的改动只有 focus/admission_digest 入参）。去重语义由集成测试与 `docs/evidence/migration-renew-20261105` 覆盖。
3. **执行列表授权过滤**：SP07 configurationOnly 且 `action.executions` 0 行，按主体过滤无法在真数据上演示；覆盖留在 `TestSP07ExecutionListFiltersByAuthorization`。

验收副作用已复原：事件 A assignee=admin、revision=2；事件 B revision 4→5（仅 CAS 演示）；跟进事项全部 open；link 关系净不变。timeline/audit 中的验收记录按留存策略保留，不清理。
