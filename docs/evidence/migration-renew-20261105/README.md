# r55：迁移来源闭包续期（00073/00074）

2026-11-05，承接工作树新增的 00073_incident_collaboration 与 00074_investigation_admission 两个前向迁移。没有修改 00001～00072 SQL、迁移 CLI 源码或依赖版本，没有升级当前部署的 API/Worker、任务数据库或生产。全部 Docker/PostgreSQL 运行仅在本机新隔离目录 `/data/ops-migration-renew-r55-20261105`。

## 本轮结果

重新生成 arm64/amd64 对应源码归档和 inventory，补入 00073/00074。逐项对比新旧 lock：firstPartyFiles 仅新增这两个 SQL；go.mod 摘要变化仅为 `go-containerregistry` 由 indirect 改为直接声明（模块集合不变）；modules/standardLibraryFiles/许可证等其余字段逐字节不变。`third_party/admission` 与 `bundle/evidence/third_party/admission` 同步相同材料；原历史证据不覆盖、不改成功结论。

复用 r50 锁定的 Go 1.27.1 Linux 工具链（`/data/ops-migration-renew-r50-20261103/toolchain`，出处/校验见 r50 `provenance.json`）。两架构均使用 `--network none`、空 GOMODCACHE/GOCACHE、GOPROXY=off、GOSUMDB=off、GOTOOLCHAIN=local 从归档重建。新归档 SHA256：

- amd64：`15b74f2d37bece08d35cc6b872d08b174f5ae4982824ead325aa5a5ac8d91897`
- arm64：`89cd8668e55114464c33a2e9154082bf020fef010da150cd47d6fef4c3d9a877`

二进制与原准入逐字节一致：amd64 `d8360db3…`、arm64 `5e9a23d8…`；SQL 不嵌入迁移工具二进制，CLI 源码与 go.sum 本轮未变。实际命令、隔离网络和初始缓存状态见 `offline-rebuild-{amd64,arm64}.json`。构建容器已删除，归档与二进制留在专属目录。

## 远程实际迁移

`forward-migration.py` 创建新的断网 PostgreSQL 容器（`--network none`，tmpfs 数据目录），无发布端口、无接触现有数据库。使用精确离线 amd64 CLI 初始化 version 1，再调用 r50 准入的正式 opsctl 引导三类不同的 NOINHERIT、NOSUPERUSER、NOCREATEDB、NOCREATEROLE、NOBYPASSRLS LOGIN（bootstrap 源码本轮未变，沿用 r50 绑定二进制）；后续仅 migration LOGIN 配置 `role=migration_role`：

- 全新库 1→74 成功；再次迁移、请求较低目标 72 后仍为 74，负数目标拒绝。
- 另一全新隔离库先到 72，保留一条真实测试 tenant 后逐步 72→73→74；tenant 完整行摘要不变。
- 73：`incident.followups` RLS/FORCE RLS 与 `tenant_isolation` 策略生效，`incident.records` 新增 `assignee_subject/assigned_at` 两列，`incident.transfer_eligible` 为 SECURITY DEFINER 且定义含 `identity.accounts` 与 `platform.sp05_lock_operator_scope` 双路径，api/worker 两个 runtime role 均持 EXECUTE。
- 74：`investigation.jobs` 新增 `admission_digest` 列，`investigation_job_identity` 部分唯一索引存在。

见 `forward-migration-amd64.json` 与逐案日志 `r55_*-*.log`。没有执行 ARM64 原生 SQL；arm64 历史原生证据（pwr004-20261016）继续单独保留，新 SQL 验证只链接 amd64。没有安装/签名新的完整 Bundle。受控历史测试 tenant 不冒充业务数据迁移。测试结束专属 PostgreSQL 容器与 tmpfs 一并删除。没有业务 Evidence/Audit 生成、没有绕过留存保护。

已有低版本安装若未做过 CREATE 授权，需要数据库管理员针对精确当前数据库向 schema_owner 补授予后再前滚（`docs/runbooks/database-migrations.md`）；已按 r50 修复引导的安装不需要重新初始化。

## 验证和范围

本地 `make check-delivery`（生成物、平台/迁移来源闭包、交付合同）通过；完整 Go 集成回归（一次性隔离 PostgreSQL 17.11）212 pass / 46 skip / 15 fail，15 个失败全部为活环境门禁（OpenBao/Keycloak/K8sGPT/SeaweedFS 固定镜像/SP06 真实链），无代码回归；新增 `TestSP05IncidentCollaborationClaimTransferCommentFollowUps`、`TestSP05IncidentCollaborationAlertLinks`、`TestSP07ExecutionListFiltersByAuthorization` 真实执行通过，报告在 `artifacts/test-reports/integration-r55d.jsonl`。

本轮只关闭 00073/00074 迁移来源材料缺口。未做 ARM64 原生执行、完整签名 Bundle 安装、全栈重启或产品视觉，P3/V1.2 和六维状态不提升；未启用模型/SP05，也未恢复 KubeVirt/CDI/Trace。
