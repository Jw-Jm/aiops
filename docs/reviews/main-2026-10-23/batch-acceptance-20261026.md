# 集中验收中止记录（2026-10-26）

代码基线：4094dc564db1e2e4edc6103dcb67204cbb95de80。运行环境：mssc-direct 上独立 PostgreSQL 容器 `ops-main-acceptance-20261026-pg`。未修改 r15 服务、GitHub 或产品代码，未调用 CodeBuddy。

## 本批结果

- 账户、MFA、授权、Incident 集中测试：22 个顶层测试通过，11 个顶层测试失败，无跳过。完整日志及退出码保存在 `evidence/batch-20261026/accounts-authorization-*`。
- 真实 Argon2id：128 个并发请求，KDF 并发上限 2，126 个忙碌拒绝，2 个完成；峰值 RSS 216708 KiB。512 MiB 容器限制下退出码 0、OOMKilled=false。单次突发结果，不代表长期负载容量；证据为同目录 `password-memory.go.source`、`memory-run.log`、`memory-result.json`。
- 先前通过且代码未变的全量 Go、前端及 r13 链路证据继续保留，本次未重复执行。

## 已确认产品缺陷：过期 MFA 预算窗口保留旧锁定时间

位置：`migrations/00065_native_stepup_budget.sql`，`identity.step_up_budget_fail` 的冲突更新分支。

当 failed_count=5、window_started_at 为 16 分钟前、locked_until 为 1 分钟前时，下一次错误密码会将 failed_count 重置为 1、window_started_at 更新为现在，却保留旧 locked_until。该记录违反 `locked_until IS NULL OR locked_until > window_started_at`，整个写入事务失败，无法记录本次失败。

在隔离数据库中，以实际函数单独复现（非 HTTP 测试失败的推断）：

```sql
BEGIN;
-- 使用隔离库已有账户的 tenant_id/user_id，设置对应 app.tenant_id。
-- 将 password 因子的预算设为上述已过期状态。
SELECT * FROM identity.step_up_budget_fail(tenant_id, user_id, 'password');
-- ERROR: new row for relation "step_up_failures" violates check constraint "step_up_failures_check"
-- CONTEXT: PL/pgSQL function step_up_budget_fail(uuid,uuid,text) line 17 at SQL statement
```

psql 使用 ON_ERROR_STOP=1，错误后连接退出，事务自动回滚。该复现不更改正在运行的 r15 数据。

建议修复：新增向前迁移，窗口重置且下一失败计数低于锁定阈值时清除过期 locked_until；已应用的 00065 不原地修改。补充实际数据库回归，覆盖冷却与窗口均过期、仍有效锁定、窗口内达到阈值等边界；修复后只重跑受影响验证组，再继续尚未执行的验收。

## 其余失败与尚未完成

另发现测试夹具可能存在：重复角色授权、UUIDv7 时间前缀造成来源实例名冲突、挑战创建顺序、未发布的 profile 版本、配置激活 revision、Incident 自动事件引起 revision 变化、以无权角色读取挑战表，以及管理写入 proof 使用固定密码。上述分类未全部完成独立复现，不能认定均为测试问题，也不能把这些失败计为通过。

真实浏览器、完整已安装来源替换及完整离线 Bundle 验收尚未完成。未执行共享节点镜像缓存清理或全局断网，虚拟化开发仍按 ADR-0008 延期。

依据用户此前“再出现问题，请停下来，告诉我是什么问题”的要求，在确认上述产品缺陷后停止；本次没有修改测试夹具或实现修复。
