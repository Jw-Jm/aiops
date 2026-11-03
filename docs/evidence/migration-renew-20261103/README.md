# r50：迁移来源闭包续期与全新库迁移修复

2026-11-03，承接 r49 发现的迁移包 71/72 缺口。保留已有工作树；没有修改 00001～00072 SQL、迁移 CLI 源码或依赖版本，没有提交/推送，也没有升级当前 30471 的 API/Worker、任务数据库或生产。全部 Docker/PostgreSQL 运行仅在 `ssh mssc-direct` 的新隔离目录 `/data/ops-migration-renew-r50-20261103`。

## 本轮结果

重新生成 arm64/amd64 对应源码归档和 inventory，补入 00071/00072。脚本逐项确认旧 firstPartyFiles 内容摘要不变、其他依赖/标准库/许可证锁字段不变，仅新增这两个 SQL。`third_party/admission` 与 `bundle/evidence/third_party/admission` 同步相同材料；原历史证据不覆盖、不改成功结论。

锁定 Go 1.27.1 Linux 工具链通过 Go 模块校验取得，出处/校验见 `provenance.json`。两架构均使用 `--network none`、空 GOMODCACHE/GOCACHE、GOPROXY=off、GOSUMDB=off、GOTOOLCHAIN=local 从归档重建。新归档 SHA256：

- amd64：`bec5f664f5d80a392b0369b02bfed45ea0bf122211a38918504b245a6a3719e3`
- arm64：`cdf705b0558534ea81395da0336e9983b1726034dd71f764dacda746c13c0d29`

二进制与原准入逐字节一致：amd64 `d8360db3…`、arm64 `5e9a23d8…`；SQL 不嵌入迁移工具二进制。实际命令、隔离网络和初始缓存状态见 `offline-rebuild-{amd64,arm64}.json`。构建容器已删除，归档与二进制留在远程专属目录。

## 真实失败与修复

首次全新库在受限迁移账号下执行到 00044 时失败：`schema_owner` 缺少当前数据库 CREATE 权限，不能创建 `identity` schema。先前 70→72 的开发库升级不足以证明全新库能通过。失败日志保留于 `restricted-schema-permission-failed.log` 和 `forward-migration-initial-schema-permission-failed.log`。

最小修复位于 `ProvisionDatabaseLogins`：在既有超级用户身份、恰好 version 1、租户为空、三类 LOGIN 不存在的守卫和同一事务中，给固定 NOLOGIN `schema_owner` 授予当前数据库的 CREATE 权限。用既有 pgx.Identifier 引用数据库名；没有给 migration LOGIN 设置 CREATEDB，没有授予 API/Worker 数据库 CREATE 或 schema_owner 身份，账号引导的重复拒绝保持。此权限允许在当前库创建 schema，与创建数据库的 PostgreSQL CREATEDB 属性不同。

扩展既有三账号集成用例，核对 schema_owner 可创建 schema、API/Worker 不可；完整集成包本轮仅编译，真实受限行为由远程正式 opsctl + 精确离线迁移 CLI 脚本执行，并未将未运行的 Go 集成测试记成通过。新 opsctl 源码/二进制绑定在 `provenance.json`。

另修两个交付对齐缺口：delivery-baseline 迁移计数 71→72；安装业务参数 schema 原本禁止 Helm/运行时已要求的 `Namespaces`，现声明为显式非空 DNS label 数组，同步唯一共享测试 fixture，新增缺失/空范围拒绝。没有放松 Helm 或运行时守卫。完整 internal/bundle 测试因此可通过。

## 远程实际迁移

`forward-migration.py` 创建新的断网 PostgreSQL 容器，无发布端口、无接触现有数据库。使用精确离线 amd64 CLI 初始化 version 1，再调用本轮正式 opsctl 引导三类不同的 NOINHERIT、NOSUPERUSER、NOCREATEDB、NOCREATEROLE、NOBYPASSRLS LOGIN；后续仅 migration LOGIN 配置 `role=migration_role`：

- 全新库 1→72 成功；再次迁移、请求较低目标 70 后仍为 72，负数目标拒绝。
- 另一全新隔离库先到 70，保留一条真实测试 tenant 后逐步 70→71→72，再次迁移成功；tenant 完整行摘要不变。
- 71 的观察查询表 RLS/FORCE RLS 生效，72 的原生来源锁函数定义及 Worker 执行权限存在。
- 核对只有 schema_owner 获得数据库 CREATE；测试结束删除专属 PostgreSQL 容器，临时数据库随 tmpfs 一并消失。没有业务 Evidence/Audit 生成、没有绕过留存保护。

见 `forward-migration-amd64.json`。没有执行 ARM64 原生 SQL；arm64 历史原生证据继续单独保留，新 SQL 验证只链接 amd64。没有安装/签名新的完整 Bundle。受控历史测试 tenant 不冒充业务数据迁移。

已有低版本安装若未做过 CREATE 授权，需要部署数据库管理员针对精确当前数据库向 schema_owner 补授予后再前滚；不能为此重跑账号初始化或改 Goose 历史。已完成的 70/72 任务库不需要重新初始化。操作说明见 `docs/runbooks/database-migrations.md`。

## 验证和范围

本地 bootstrap/opsctl/db-migrate 单测、完整 bundle 包、迁移连续/只前滚静态合同、交付/业务合同、生成物、平台/迁移来源闭包均核对；最终结果以 `result.json` 列出的精确日志和回执为准（早期名称带 final 的失败日志也保留），保留旧 fixture/schema 和基线计数错误的失败日志。旧来源摘要被拒绝的新负例通过，不以原二进制未变允许漏掉 71/72 的旧源码包。

本轮只关闭迁移来源材料缺口和相关引导/安装参数不一致。未做 ARM64 原生执行、完整签名 Bundle 安装、全栈重启或产品视觉，P3/V1.2 和六维状态不提升。r49 持续日志环境只读确认 API/Worker 仍 Ready、0 重启；未重跑日志故障实验、未启用模型/SP05，也未恢复 KubeVirt/CDI/Trace。
