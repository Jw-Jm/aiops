# 独立 Current 环境迁移验收

2026-11-05 开始，2026-11-06 完成入口切换和旧业务栈停用。初始应用提交为 `9372674a87201a4ccaab6737a1019808ea41ffd5`；随后按集群账号范围与日志发现修复验收切换到 `dd9da1cca0f87d5bd943faee74f883648c916413`，再按故障链/DIMM 范围修复验收切换到 `b3d7208b5410f72bc4adc22a0e0423e3f6e0ab3f`。namespace 为 `ops-current`，独立数据根 `/data/ops-current`，干净数据库 `ops_current`（schema 74）。旧 `ui_v12_r24_native` 未整库复制。

## 已完成

- 从固定源码快照离线编译 API、Worker、opsctl、迁移工具并构建固定摘要镜像；investigator 使用当前源码与已准入依赖，Web 使用同一源码的构建结果。
- 经正式 `opsctl bootstrap environment` 创建 namespace。新建 PG duty 登录、管理员、MFA、Registry 签名密钥、Bao 持久化实例/Transit/工作负载 PKI、TLS/IAM 归档。模型外部供应商凭证作为既有授权配置重用，不重用旧工作负载身份或旧数据库。
- 通过密码/TOTP 绑定的原生 API 注册集群和三个真实来源、显式授予当前 namespace 的 operator 权限、签名发布只读策略/诊断配方及命令配置。admin 与 operator 仍独立。
- 指标源核验了原生 Pod UID/容器 ID、cAdvisor CPU 与内存、KSM 重启和状态；错误 UID 和其他 namespace 为空。页面完成四类 trend 查询及跨范围、原始查询、CSRF 和 no-data 拒绝/语义检查。
- VictoriaLogs 中的日志与原生 Kubernetes 消息、namespace UID 和容器身份匹配。页面查询返回脱敏样例，刷新后历史可恢复；另恢复 namespace 限定的原生 Pod 日志入口。
- 真正创建 Pending PVC，观察原生告警和故障，通过页面受理/处置；创建精确 claim UID 的 PV 使 PVC Bound，保留系统原有恢复窗口，系统自动恢复后通过页面关闭。时间线、namespace 内 RCA、故障告警及归档证据读取成功。
- 真实审计问数完成模型、Worker 和事实引用闭环。资源问数的 403 失败独立留存，不算作通过。
- 原 `https://192.168.0.63:30471` 入口切到 current，并在此入口复验。r24/r34 旧业务工作负载及旧 Docker Web/PG/日志/fixture 共 24 项停用动作留有回执，定义、数据和证据保留；共享集群基础设施未停用。
- 最终依赖审计确认仅三个 current Docker 容器活跃、八个 current Pod Ready；应用/采集器 hostPath 除系统日志读取目录外均在 current 根内。API/Worker 都连接 `127.0.0.1:15554/ops_current`。旧业务 namespace 无 Running Pod。
- 已建立私有 PG dump、角色定义、配置和 Bao Raft 备份；PG dump 由 `pg_restore --list` 验证格式。已生成固定发布清单并原子设置 `current` 指针。
- 集群账号范围后续验收确认 operator 默认整集群授权、授权页三个角色、来源 revision 2 的 31 个 namespace、四类 CoreDNS 指标、两次集群级列表的日志资源发现、20 条脱敏日志样本，以及新故障详情/时间线/证据/RCA。证据见 [cluster-account-scope](cluster-account-scope/README.md)。
- 环境恢复后续完成 metrics APIService 迁移、一次性 namespace 自然删除验证、scheduler/CNI/路由复核、amd64 K8sGPT 门禁复跑和签名 Bundle 目标判定；`/dev/sda1` 与 USB `sdb` 的 IO 证据显示当前不应直接迁移活数据。证据见 [environment-recovery](environment-recovery/README.md)。
- b3d 后续修复通过源码级回归后完成现场验收和 current 记录；首次 final smoke 在 Graph 冷启动窗口返回 503，保留原失败证据后重试通过，记录后 final smoke 再次通过。

入口、身份材料位置和后续升级/回退步骤见 [运行说明](../../operations/current-environment.md)。`runtime/acceptance.json` 的 `passed_core` 仅代表其中列出的核心检查，不代表产品全部专项或全局健康。

## 证据说明

`runtime/` 为脱敏运行回执，`ui/` 为真实页面/API 查询结果与守卫检查，`tools/` 为本次一次性初始化/验收脚本快照。脚本快照可能引用构建前的旧材料作为输入，不能将其误作现有运行依赖，也不能作为未来升级入口盲目重跑。当前可重复使用的运维入口是仓库 `scripts/current/release.py`。

不收录任何 Secret 内容、密码、MFA 种子、恢复码、私钥、PG/Bao 私有备份、镜像 tar 或大体积源码包。完整材料留在部署主机的 private/backups 与 releases/materials 中。

## 初始化期间的问题与保留边界

- 控制面曾拒绝 6443 连接，影响 TokenReview、MFA 和采集；恢复后短期投影 token 重新有效，真实来源经重新核验。
- 临时 bootstrap namespace 的内容删除后，被失效 metrics API discovery 阻塞收尾。核对该本轮 namespace 的原 UID、内容与 finalizer 均已清空后，仅完成其删除收尾，再走正式 bootstrap；未修改共享 APIService 或清除旧业务数据。
- 修正过 scratch Nginx 临时目录、采集器显式投影 token、VM 数据目录 UID 及 Worker 1 CPU/512 MiB 限制。哈希、身份和沙箱门禁未放宽。
- 管理写入必须串行使用 TOTP；并发脚本曾使未完成挑战的写入被拒绝，保留拒绝结果后串行重试。策略签名按 canonical content digest 和明确 schemaId 重新核对。
- 日志验收脚本曾误指向旧入口并产生失败输出；该失败存入新环境记录，旧 r54 运行回执由 Git 中的规范归档副本恢复，随后仅对 current 重跑。未将旧入口的失败当作 current 成功。
- 旧故障证据在来源从 revision 1 升到 revision 2 后继续被来源修订校验拒绝；新故障证据在同一来源 revision 下读取通过。全局能力、资源问数策略和普通调查状态仍按各自回执记录，不因本次核心 smoke 通过而扩大结论。真实命令执行、虚拟化、Trace、生产/HA、空缓存离线 Bundle 均不在本次通过范围。
- Worker Graph Lease 慢告警保留。现查显示共享块设备写入/同步排队是主要根因，未通过放宽阈值或删除告警掩盖。
