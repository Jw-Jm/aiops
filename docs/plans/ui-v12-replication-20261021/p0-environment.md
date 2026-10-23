# P0-04 独立业务数据库与 Fixture（2026-10-21）

状态：**r2 已在任务专属数据库执行并通过 46/46 检查；P0-04 数据准备完成，G0 仍未通过**。本文件记录任务专属环境的约束、测试数据定义、r1 失败事实、r2 复验结果和运行方式。r1 失败证据保留；原生登录/历史主体迁移仍属 P2 依赖。

## 1. 任务与环境

| 项 | 值 |
| --- | --- |
| 任务 | P0-04 独立业务验收数据准备及小验证 |
| 任务标记 | `ui-v12-p0-04-r2` |
| 远程主机 | `ssh mssc-direct`（同一主机的 `10.10.10.2` 与 `192.168.0.63`） |
| 任务目录 | `/data/ops-ui-v12-20261021-r2`（0700） |
| 容器 | `ops-ui-v12-pg-20261021-r2` |
| 镜像 | `sha256:589f742565e4f6d22149176b032974f55f8201a8a37b71ed587ec5c6902e689f` |
| PostgreSQL | 17.11，schema version 43 |
| 远程回环 | 客户端 `127.0.0.1:15449` → 容器内 PostgreSQL `5432`（Docker 端口映射） |
| 数据库 | `ui_v12_development`（精确名称，非共享库） |
| 数据库 comment | `ops.task=ui-v12-20261021-r2; isolated synthetic fixtures only` |
| 数据库所有者 | `ui_v12_owner`（仅本任务 bootstrap） |
| 独立 LOGIN | `ui_v12_api`、`ui_v12_worker`、`ui_v12_migration`，均 `NOINHERIT` / `NOBYPASSRLS` |
| 私密配置 | `/data/ops-ui-v12-20261021-r2/private/{api.env,worker.env,migration.env,database-logins.json,postgres-password}` |
| 磁盘 | ROOT 约 59G 可用；`/data` 约 1.6T 可用；任务文件继续只写 `/data/ops-ui-v12-20261021-r2` |
| 共享资源 | 共享数据库/旧 repo 只读，禁止写入或改成任务库 |

`api.env`、`worker.env`、`migration.env` 的格式是 `DATABASE_URL=原始 URL`。URL 的
`options=-c role=<duty>` 与 `sslmode=disable` 是既有职责切换方式。**不要 source 这些文件**：
URL 中可能含 `&`，shell 会截断或解释它。测试进程按 `KEY=VALUE` 读取，只取第一个 `=` 之后
的完整值。

共享数据库、旧 repo、其他任务的容器/目录一律只读，本任务不写入。

## 2. 纳入版本库的文件

| 文件 | 用途 |
| --- | --- |
| `platform/test/integration/ui_v12_fixture_test.go` | Fixture 数据准备、guard、小验证、无秘密证据输出 |
| `platform/docs/plans/ui-v12-replication-20261021/p0-environment.md` | 本说明 |
| `platform/docs/plans/ui-v12-replication-20261021/evidence/ui-v12-fixture-p0-04-r2-plan.json` | 未执行的 Fixture 定义与预期 |
| 运行时证据 | 由远程命令写到任务目录 `evidence/ui-v12-fixture-p0-04-r2-<run-id>.json`，确认无秘密后再复制 |

未修改：业务产品代码、迁移、依赖、部署文件、根目录 00～10 文档、虚拟化/Trace 范围。
P0-02、P0-03 和主进度表由其他任务维护，本任务未触碰。

### 2.1 首轮运行事实（r1，失败证据保留）

- 独立 Linux/amd64 编译二进制 SHA256：`cd51072e2ae5327958f097045c220dbd616973de6161504103c54ab89d9b3225`。
- 远程执行 46 项检查，43 通过、3 失败：
  `history_owner_a`/`history_owner_b` 返回 count=0、`history_owner_read` 返回
  `INVESTIGATION_NOT_AUTHORIZED`。
- 已实际落库：2 clusters、3 sources、6 entities、3 bindings、2 conversations、
  9 audit records、0 qualification；重复 seed 未新增 audit/revision。
- 根因：首轮手工拼的 `GrantSnapshot` 缺少真实 `AuthorizationRevision`，`Namespaces`
  为空；`assistant.authorizeConversation` 强制比较当前 grant digest 并拒绝，属于既有
  安全设计，不修改业务校验。
- 修正：新会话改走 `assistant.Repository.Create`（固定 creation key 幂等）；r1 的两个
  固定 ID、无消息/无 turn 会话按第 4.4 节的受限绑定修正 + 正式 `Rebind` 收敛。
- r1 的 `evidence/fixture-run-r1.log`、`ui-v12-fixture-run-r1.json` 原样保留；
  r2/r3 及以后必须使用新的证据路径，不覆盖 r1。

### 2.2 第二轮运行事实（r2，通过）

- 源码：当前工作树 `test/integration/ui_v12_fixture_test.go`；Linux/amd64 交叉编译
  `GOOS=linux GOARCH=amd64 go test -c ./test/integration`。
- 二进制：远程 `bin/ui-v12-fixture-linux-r2.test`，SHA256
  `f0af7faf474d9e786dbcb686233725be6740eadbfcab42c8e5bd40e8bc60681c`。
- 运行：`OPS_UI_V12_FIXTURE_RUN=1`、`OPS_UI_V12_FIXTURE_TASK=ui-v12-p0-04-r2`、
  `OPS_UI_V12_ENV_DIR=/data/ops-ui-v12-20261021-r2/private`、新证据路径
  `/data/ops-ui-v12-20261021-r2/evidence/ui-v12-fixture-run-r2-20261021T1355.json`；
  `O_EXCL` 写入，未覆盖 r1。
- 结果：`fixture_verified_not_accepted`，46/46 检查通过；`history_owner_a`、
  `history_owner_b`、`history_owner_read` 全部通过；跨 owner 读取仍被拒绝。
- 数据：2 clusters、3 sources、6 entities、3 bindings、2 conversations、9 audit records、
  0 qualification proofs；第二次 seed 未新增 audit/revision，固定 ID 稳定。
- 旧会话修正只作用于本任务固定 ID 的两个空会话（无 turn、无 message、无 event、
  无 binding request）；通过事务内当前 grant 收敛后走正式 `assistant.Repository.Rebind`，
  未删除或重建行，未修改业务授权校验。
- 证据：[本地 r2 JSON](evidence/ui-v12-fixture-run-r2.json)、
  [本地 r2 日志](evidence/fixture-run-r2.log)；远程原文件在任务目录 `evidence/`。
- 仍未验证：原生用户/登录/会话/主体映射（P2-01/P2-02）、真实 adapter 资格（P8-02）、
  真实 API/Worker/Kubernetes/模型/浏览器；G0 与产品六项不因 Fixture 通过而关闭。

## 3. Guard（拒绝条件）

测试在打开任何业务写入之前校验：

1. `OPS_UI_V12_FIXTURE_TASK` 必须精确等于 `ui-v12-p0-04-r2`；`OPS_UI_V12_FIXTURE_RUN=1` 才执行。
2. 三个 env 文件各自只有一个 `DATABASE_URL`，且不能被 shell 预处理。
3. 每个 URL 必须：`postgres://` / `postgresql://`；登录名精确匹配；**客户端**地址
   `127.0.0.1:15449`；路径精确为 `/ui_v12_development`；只允许 `sslmode=disable` 与
   `options=-c role=<api_runtime_role|worker_runtime_role|migration_role>`；
   出现其他查询参数（如 `hostaddr`、`host`、`port`、`role`）即拒绝。
4. 连接后核对服务端事实：`session_user` 是任务 LOGIN，`current_user` 是职责角色，
   `current_database()` 精确匹配；**服务端** `inet_server_port()` 必须是容器内原生
   5432，`inet_server_addr()` 记录为容器地址。Docker 端口映射使客户端看到 15449、
   PostgreSQL 内部仍是 5432，这不是错误，也不修改运行端口。
5. 三个 LOGIN 必须彼此独立，LOGIN 与职责角色均须
   `rolinherit=false`、`rolbypassrls=false`、`rolsuper=false`，且三条连接到达同一
   服务端地址与端口。
6. `ui_v12_development` 的所有者必须是 `ui_v12_owner`；数据库 comment 必须**精确等于**
   `ops.task=ui-v12-20261021-r2; isolated synthetic fixtures only`，不接受大小写、
   空白、子串或轮次变体（例如 `r20`）；`public.goose_db_version` 最大版本必须是 43。
7. 任何一项不满足即停止，不创建 tenant、cluster、source 或会话数据。

这样不会把共享 DSN、旧回环端口或其他数据库误当成任务库。若远程数据库所有者不是
`ui_v12_owner` 或迁移版本不是 43，测试会明确失败，而不是继续写入。

## 4. Fixture 数据

所有 ID 固定，重复执行按业务键幂等收敛；不覆盖其他 tenant。Fixture tenant 为
`ui-v12-p0-04-r2`，bootstrap tenant 为 `ui-v12-r2-bootstrap`。

Bootstrap 是唯一的 schema_owner 直写，且不吞掉所有权冲突：先按固定 tenant_id 读取，
再核对 slug、display_name、status；不存在时先确认该 slug 未被其他 tenant 占用，再普通
INSERT（不使用 `ON CONFLICT`）。管理员 binding 也必须恰好是固定 binding_id 且 active；
出现其他 ID、重复 binding、disabled 或 tenant 元数据不符时直接拒绝，不接管、不改名。

### 4.1 两个集群、同名资源

| 集群 | cluster UID | namespace | namespace UID | Pod | Pod UID |
| --- | --- | --- | --- | --- | --- |
| A | `0c1a0000-…-a1` | `prod` | `0a110000-…-a1` | `api-0` | `90d00000-…-a1` |
| A | 同上 | `canary` | `0a110000-…-a2` | `api-0` | `90d00000-…-a2` |
| B | `0c1a0000-…-b1` | `prod` | `0a110000-…-b1` | `api-0` | `90d00000-…-b1` |

- 同名 Pod/namespace 在不同集群或不同 namespace 下具有不同 UID，因此 canonical ID 不同。
- canonical ID 形如
  `k8s+v1://<tenant>/<clusterUid>/core/Pod/<k8sUid>`；namespace 是实体属性，namespace UID
  由 `Namespace` 实体的 stable ID 表达。没有把 namespace 名称拼进 Pod 身份。
- 资源通过现有 `resourcestore.Repository.Resolve` 写入 `resource_entities` /
  `resource_aliases`，worker 职责连接；不是直接插行冒充图数据。

### 4.2 角色与授权

| subject | 角色 | 范围 | 预期 |
| --- | --- | --- | --- |
| `ui-v12-r2-bootstrap-admin` | `platform_admin` | 无 operator scope | 管理接口可用；`graph.Authorization.Effective` 返回 `FORBIDDEN`（管理员不继承运维权限） |
| `ui-v12-r2-operator-a` | `operator` | cluster A + namespace `prod`/`canary` | A 的 scope 可读 A 的 `prod`/`canary`；对 B 或其他 namespace 返回 `FORBIDDEN` |
| `ui-v12-r2-operator-b` | `operator` | cluster B + namespace `prod` | B 的 scope 可读 B 的 `prod`；对 A 或 `canary` 返回 `FORBIDDEN` |

Operator 调用管理员来源列表接口必须得到 `source.ErrUnauthorized`；绑定表里不能出现
`ui_v12_api` / `ui_v12_worker` / `ui_v12_migration` 同名授权。

### 4.3 来源状态

| instance key | 状态 | 用途 |
| --- | --- | --- |
| `ui-v12-r2-a-native` | active，revision 2 | 同一来源的旧 revision 1 被固定复核为拒绝 |
| `ui-v12-r2-b-native` | active，revision 1 | 绑定有效但**无**资格证明，`evidence.Authorize` 必须拒绝 |
| `ui-v12-r2-b-revoked` | disabled，revision 2 | 已撤销来源，绑定与授权都必须拒绝 |

没有向 `platform.adapter_scope_verifications` 写入任何行。资格通过证明不能由 Fixture
捏造；真正合格的 live 来源属于 P8-02。本 Fixture 只验证“未资格被实际拒绝”和状态区分。

### 4.4 个人历史与主体

- 两条 `investigation.assistant_conversations` 分别归 operator A / B，`owner_subject`
  保持原 subject；A 只能列出/读取自己的，B 的会话对 A 不可见，反之亦然。
- `ui_v12_api` 只是数据库 LOGIN，不是业务 subject；按该名称查询历史必须为空。
- 当前迁移 43 没有原生 `users` / `user_accounts` / `user_credentials` / `principals`
  表，也没有原生登录、登录密码、登录会话或 subject 映射表（`platform.step_up_sessions`
  是既有再次验证记录，不是原生登录账号）。测试会探测并如实记录为“缺失”，
  不会创建假原生账号、不会按同名继承权限。
- 明确保留依赖：P2-01（用户/凭据/主体映射）与 P2-02（原生登录与会话）。在它们完成前，
  不能把本 Fixture 当作“新旧主体映射已验收”。
- 新会话通过现有 `assistant.Repository.Create` + 固定 creation key 幂等创建，绑定由
  `graph.EffectiveTx`/`analytics.RestrictScope` 生成，不再手工拼 `GrantSnapshot`。
- r1 已写入的两个固定 ID、无消息、无 turn 会话需要收敛：`Rebind` 在授权前会按设计拒绝
  无效旧绑定，因此先做一次**受限**绑定修正（仅这两个固定 ID、同 tenant/owner/cluster、
  archived 为空、turns/messages 为 0、且 events/bindingRequests 为 0 时才允许），再用
  既有 `Repository.Rebind` 走正式修订与事件路径。不删除、不重建、不触碰其他会话；任何
  历史或身份不符即失败。运行报告记录 `legacyRepairs`（revision、BindingRepaired、计数）。

## 5. 运行的检查

1. `guard`：客户端 15449、服务端 5432、库名、所有者、database comment 任务标记、
   schema 版本、三个独立 LOGIN 与职责角色的属性。
2. 集群/来源/绑定/资源/会话的期望数量，且第二次 seed 后计数完全不变。
3. 第二次 seed 不新增 audit、source revision 或 cluster revision；来源 revision/status
   保持第一次的值。
4. 没有重复的 `(subject, role)` 绑定、没有重复的 `(source_type, instance_key)` 来源。
5. 既有 tenant 的 slug→id 不变；新增 tenant 只能是本任务的两个固定 slug。
6. 2 集群 + 3 namespace + 3 同名 Pod：canonical ID 与持久化 namespace 正确区分。
7. 管理员无 operator scope；operator A/B 互斥且 namespace 授权显式；operator 调管理接口被拒绝。
8. 来源 revision 2/1、disabled、无资格证明四种状态分别验证；旧 revision 与已撤销被拒绝。
9. 个人历史按 `owner_subject` 隔离；LOGIN 名称不产生历史或授权；新会话来自
   `Repository.Create`，r1 空会话经受限绑定修正 + `Rebind`，且 `legacyRepairs` 有记录。
10. 原生身份表缺失、LOGIN 同名绑定为零，作为 P2 依赖记录；既有 step-up 表不计入原生账号。

## 6. 运行方式（本地只编译，远程只运行本任务）

本地编译与纯单元检查：

```sh
cd /Users/mssc/Documents/Code/ops/platform
gofmt -l test/integration/ui_v12_fixture_test.go
go vet ./test/integration
go test ./test/integration -run '^TestUIV12FixtureDSNGuard$' -count=1
go test -c -o /tmp/ui-v12-fixture.test ./test/integration
shasum -a 256 /tmp/ui-v12-fixture.test
```

不要把本地任何测试连到远程数据库；只传编译后的测试二进制。

远程运行（明确专属库，不用 shell source）：

```sh
scp /tmp/ui-v12-fixture.test \
  mssc-direct:/data/ops-ui-v12-20261021-r2/bin/ui-v12-fixture.test

ssh mssc-direct
cd /data/ops-ui-v12-20261021-r2
chmod 700 bin/ui-v12-fixture.test
env OPS_UI_V12_FIXTURE_RUN=1 \
    OPS_UI_V12_FIXTURE_TASK=ui-v12-p0-04-r2 \
    OPS_UI_V12_ENV_DIR=/data/ops-ui-v12-20261021-r2/private \
    OPS_UI_V12_FIXTURE_EVIDENCE=/data/ops-ui-v12-20261021-r2/evidence/ui-v12-fixture-p0-04-r2-<run-id>.json \
    ./bin/ui-v12-fixture.test -test.run '^TestUIV12FixtureSeedAndVerify$' -test.v -test.count=1
```

证据路径每次必须是**新的唯一文件**：写入使用 `O_CREATE|O_EXCL`，已存在的历史文件
不会被覆盖；创建/写入/关闭任一失败都会使测试失败，不会以日志代替失败。

第二次运行使用另一个新路径，再核验幂等与无重复。第二次的计数、ID、tenant 快照必须与
第一次一致，且 audit/revision 不新增。

证据文件只含 tenant/cluster/source/conversation ID、计数、检查名和结论，不含 DSN、密码、
令牌或私密路径内容。复制到仓库前仍需人工确认。

## 7. 未满足项与边界

- **原生账号未实现**：没有 users、登录凭据、登录会话或主体映射表（既有
  `platform.step_up_sessions` 不是原生账号）；本任务不伪造登录，也不宣称历史
  subject 已完成显式映射迁移。阻断范围：P2-01、P2-02、P2-05。
- **来源资格未通过**：没有 `adapter_scope_verifications` 证明；“未资格被拒绝”不等于
  “资格已通过”。合格 live 来源与真实查询属于 P8-02。
- **不是 live 验收**：没有启动 API/Worker/浏览器/模型，也没有连接真实 Kubernetes 来源；
  数据是隔离业务 Fixture，不能冒充 live 来源、事件或告警。
- **不是 P0-04/G0 通过**：只准备了任务专属数据库与最小业务数据，并验证了数据形状和几条
  现有授权路径；没有完成 458 切片、完整六项验收或阶段门。
- **数据库所有权、comment 与版本是硬门禁**：若客户端 DSN 不是
  `ui_v12_development@127.0.0.1:15449`，或服务端不是容器内 5432、owner 不是
  `ui_v12_owner`、comment 没有本任务标记、schema 不是 43，测试会拒绝运行。
  不会通过修改 PostgreSQL 运行端口或容器映射来“适配”测试。
