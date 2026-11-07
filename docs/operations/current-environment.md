# Current 开发环境与迭代流程

用户于 2026-11-05 授权重建，2026-11-06 完成入口切换。当前只有一套活跃 OPS 业务环境；历史目录、数据库和证据保留归档，不再承载入口。此处是开发运行环境，不代表正式离线 Bundle、生产或高可用验收。

## 当前入口与身份

- 主机：`mssc-direct`（192.168.0.63）。集群 namespace：`ops-current`。
- 页面：<https://192.168.0.63:30471/login>。
- 数据库：独立容器 `ops-current-pg`，`127.0.0.1:15554/ops_current`，当前 schema 74。API、Worker、身份 bootstrap 和只读 CLI 使用独立 NOINHERIT 登录。
- 新管理员：`ops.admin`。已通过正常首次改密、MFA 绑定，并通过密码/TOTP 绑定的管理 API 显式授予当前 namespace 的 operator 权限；admin 不隐含 operator。
- 密码位于远程 `/data/ops-current/private/browser-account.json`；MFA 初始绑定及恢复材料位于同目录 `browser-mfa.json`。这些文件仅存放在部署主机的私有目录和私有备份内，不进 Git，也不通过公开验收输出传递。
- 原账号、历史故障和配置未从损坏的旧库整库复制。模型供应商的既有 API 授权凭证是配置重用项；工作负载 PKI、数据库凭证、Registry 签名密钥和本地管理员均重新建立。

## 固定位置与发布身份

```text
/data/ops-current/
├── releases/<完整 Git 提交>/
│   ├── bin/                 # 对应源码构建的工具和应用
│   ├── web/                 # 此提交的静态页面
│   ├── materials/           # 必须随发行保存的对应源码
│   ├── images.json          # 不可变镜像引用和二进制摘要
│   ├── manifest.json        # 已验收的运行版本、schema、配置摘要
│   ├── deployments.json    # 不含 Secret 值的部署清单
│   └── runtime-config.json # 公共配置快照
├── current -> releases/<完整 Git 提交>
├── config/                  # 环境公共配置
├── private/                 # 凭证、密钥及恢复材料
├── data/                    # PostgreSQL、Bao、归档、指标和日志数据
├── backups/                 # 私有数据库、Bao 和配置备份
└── evidence/                # 当次实际部署验收，不混用 fixture 结果
```

`current` 是已验收版本的原子指针；Pod 镜像和 Web 的 bind mount 仍明确引用不可变版本，不通过改 symlink 偷换运行中的文件。业务数据不放进 release，不因升级而重建。应用使用 Deployment；依赖镜像及应用镜像均固定内容摘要，禁止自动拉取未准入材料。

自 2026-11-06 存储迁移起，`/data/ops-current` 是指向 `/mnt/ops-sdb/ops-current` 的符号链接；后者位于 GPT/ext4 `/dev/sdb1`（UUID `43598b77-4d08-4dbe-be52-331005a03ff1`），`/etc/fstab` 使用 `noatime,nofail,x-systemd.device-timeout=10s` 挂载。原内部盘数据保留在 `/data/ops-current.sda-rollback-20261106`，仅代表停机时点，切换后不再同步；回滚必须再次停栈并反向切换。未做 USB 断电安全验收，重启后 OpenBao 仍需按既有私有流程手工解封。

当前应用源码版本为 `6f3cf535f2f4b48e0e49fbd6cb1c22d47ea383b9`（2026-11-07 部署并记录；含 7 个 Pod 故障专项 recipe、对象删除结算与投影 v4 身份升级；10 个 recipe 已用 current-registry 密钥签名并在租户范围激活）。上一已验收版本 `b3d7208b5410f72bc4adc22a0e0423e3f6e0ab3f` 等保留为回滚发布。实际版本以部署主机的 `current/manifest.json` 为准。

应用、Bao、归档、KSM、指标和采集器归属 `ops-current`；PostgreSQL、VictoriaLogs 和 Web 是同一环境下的独立 Docker 容器。它们的数据和配置全部位于 `/data/ops-current`，不能依赖旧 namespace 或旧代际 hostPath。共享 Kubernetes 控制面、CNI 和独立观测目标不是第二套 OPS 业务栈，不随业务升级重建。

## 日常检查与备份

在部署主机上运行仓库工具，或已部署的同一份 `/data/ops-current/release.py`：

```sh
python3 scripts/current/release.py status
python3 scripts/current/release.py backup
```

工具会核对正式 bootstrap 的 namespace UID、实际查询 `ops_current` 的迁移版本。备份使用私有权限，包含 PG 自定义格式 dump、角色定义、私有配置、namespace 资源和 OpenBao Raft 快照。归档 payload 留在独立 `data/archive`；本工具不宣称已额外复制该目录或完成整机灾难恢复演练。备份格式已用 PG 自身的 `pg_restore --list` 验证。

## 每次代码迭代

1. **确定提交并保存产物。** 从审核过的完整 Git 提交建立新 `releases/<commit>`，同一份源码构建 API、Worker、investigator 与 Web。记录镜像及文件摘要，保留所需源码、许可证和依赖准入材料。目标架构按实际环境选择，第三方哈希锁不得随意更改。
2. **备份与兼容性检查。** 运行备份，检查当前 schema、下一版要求及配置变化。若有数据库迁移，使用迁移工具要求的专用 duty 执行；不能使用 API/Worker 登录，也不能将身份 bootstrap 登录误作结构迁移账号。优先增量兼容迁移，不在普通升级中重建库或重新建租户。
3. **更新同一 namespace。** 使用固定摘要更新 `ops-api`、`ops-worker`、`ops-investigator` 的 Deployment，Web 指向新 release 的静态文件。配置变更有快照和审核记录，服务仍使用同一 data/private 目录。不要覆盖正在运行的二进制，也不要新建长期 `rNN` 栈。
4. **验收实际入口。** 核对 PG 可查询、工作负载身份、原生采集、四类指标、日志查询与历史恢复、真实故障与加密归档、权限拒绝。单独记录模型/调查及命令执行的实际状态；fixture 结果不得替代 current HTTP 验收。
5. **记录已验收版本。** 准备与实际入口、namespace UID 和完整提交一致的 `evidence/acceptance.json` 后执行：

   ```sh
   python3 scripts/current/release.py record --release <完整的40位Git提交>
   ```

   工具会检查三个应用 Deployment 的实际固定镜像、Web 挂载、schema 和核心验收回执，保存不可变清单后原子更新 `current`。`record` 本身不会部署、迁移或将未通过功能改为通过。

保留当前版和上一已验收版本，历史证据、对应源码和受保护数据另行保留。清理更早构建缓存前，先确认离线构建、镜像父层、法务分发和回退都没有引用，不能直接执行全局 prune。临时 fixture 只按需启动，使用专用库、端口和标签，结束后停用并按各自保护规则留存材料。

## 回退

回退应用前比较目标清单的 schema 与当前数据库，核对公共配置摘要及外部依赖兼容性。兼容时恢复上一已验收发布的镜像和 Web 目录，并重新验收；不自动回滚数据库、不恢复已撤销的凭证或权限。存在破坏性迁移或不兼容配置时，先评审恢复方案，必要时从受保护备份恢复。

初始 current 发布时没有上一份健康 current 可自动回退。现在回退仍必须先核对 schema、配置摘要和外部依赖，不能只改指针。旧 r24 数据库存在坏页，旧栈仅用于取证和人工恢复参考，不作为正常回退版本。

## 已知边界

- 命名空间授权下的资源问数提交仍返回 403；这是已有策略作用域衔接问题，未通过扩大权限规避。审计域实际模型/Worker 问数已完成并返回带事实引用的结果。
- 全局能力接口仍可报告 Graph partial/degraded；不能将 Pod Ready 或部分页面 200 称为全局健康。当前 namespace 的指标、日志、资源和故障链路分别验收。
- 命令配置可发布；真实命令执行仍为 configuration-only/disabled。KubeVirt/CDI、Trace、生产、高可用和空缓存离线交付未恢复验收。
- b3d Web 构建使用全局 pnpm 10.18.3，仓库声明为 12.7.0；构建与验收通过，但工具链差异保留为风险。
- Worker Graph Lease 慢告警在存储迁移前与共享 `/data` 同步排队吻合；迁移后 PostgreSQL checkpoint sync 已降至 0.020s 内、sdb 写延迟约 0.1–0.3ms，但 `lease_cas` 仍有约 201–303ms 周期告警。2026-11-07 排查结论：`lease_cas` 阶段计时包含共享限速预算（默认 QPS 10，槽位 100ms）的排队等待，采集队列压力下控制类（Lease PUT）按让位规则等约 2 个槽位 ≈ 200ms，实测分布 p50=202ms/p90=204ms 紧贴告警阈值且恒定，与该机制完全吻合；apiserver 写路径本身健康（宿主 kubectl 对 Lease 真实写实测 27–34ms，etcd 在 NVMe 上 fsync 1–2ms）。该告警为良性的计时归因噪声，Lease 续约预算（8 秒续约期限）余量充足，不需要修复；若要消除告警需把限速等待从阶段计时中分离，属可选优化。
- 2026-11-07 环境网络变更与持久化：主机有线网卡 enp3s0 被改配为静态 `10.10.10.2/24`（新网段），kube-apiserver 证书 SAN、kubeconfig、平台采集配置（`sp04-worker.json`/`sp04-api.json`）及文档入口仍引用 `192.168.0.63`。经确认选择保留双地址：`192.168.0.63/24` 已作为第二地址写入 `/etc/netplan/00-installer-config.yaml` 并通过 `netplan apply` 生效；开机链路为 netplan-configure.service（enabled）→ NetworkManager（netplan-enp3s0 连接 autoconnect，双地址）——已核实 netplan 文件、生成的 NM 配置与激活状态三者一致。原配置备份于 `/data/ops-current/backups/netplan-00-installer-backup-20261107.yaml`。整机重启仍需按既有私有流程手工解封 OpenBao。若未来整体迁移到 10.10.10.2，需先更新 apiserver 证书 SAN 与全部配置/文档。期间另观测到从 Pod 网络访问 ClusterIP 10.96.0.1:6443 明文探测超时（TLS 端口对明文应答 400，节点 IP 路径即时应答），未影响业务，记录备查。
- 旧数据、历史失败证据和本次初始化期间的失败记录保留。迁移没有把失败改写成成功，也没有直接修复旧库坏页。

实际证据见 [current 验收记录](../evidence/current-environment-20261106/README.md)。
