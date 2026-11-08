# 同一故障的受控命令闭环 · 2026-11-08

环境为 `mssc-direct` 的独立 `ops-current`，页面 `https://192.168.0.63:30471`。最终应用候选 `0d19b045f979af1fb47155e450dc8d2145ef27a7`；API、Worker 使用本候选二进制及精确镜像摘要，入口文件另行提取并核对 SHA256。Web 复用未变化的 c67bebf 构建，Investigator 复用 b878891。完整镜像来源、执行配置、RBAC 和网络边界见 `runtime-public.json`。

沿用已完成页面调查的真实故障 `01a1b3b6-7b32-7eb2-a67f-4c5e6a0384ad`，目标为 `ops-current/ops-metrics-server-metrics-6d46c4f8f5-7ztrn`，UID `9d925158-f0c9-4ce5-84b8-830959e148dc`。同一故障保留调查会话、历史证据、处置记录及后检查。目标此前已恢复，历史根因仍未确认；本次只读命令验证机制，不宣称它修复了业务故障。

## 页面与 Runner 验收

- 本人独立输入实际 Bash 命令，选择已签名、发布的精确配置版本，进行风险评估、密码及 TOTP 二次验证、本人风险确认，再提交。命令只读取该 Pod 的名称、UID、阶段、Ready 和累计重启数。没有自动填入或执行模型建议。
- 配置 `4ce9b2d0-7260-4e84-9bd6-bc9bd5a174bc@1` 仅允许上述 canonical target。Runner 的目标凭据只能 `get` 该命名空间内该命名的 Pod；读取 OpenBao Pod 被拒绝。Runner 不自动挂载服务账号 token；工作负载身份、目标短期凭据、默认拒绝网络策略分别约束执行。
- 第一条执行 `01a1bad0-f2d2-7355-9e86-1344fdcd0635` 退出码 0、219 字节输出、归档完成，但原生采集未完成，后检查诚实记录 `inconclusive`，未改写历史记录。
- 通过页面明确关联原执行，重新输入、验证、确认的后续执行 `01a1bad8-1432-7a5f-a1b1-f91bfc229a22` 成功，后检查 `resolved`。六条归档证据逐条读取为 200、`archived_verified`。冻结报告记录执行完成时间早于原生观测，授权和来源可用、采集完整、精确配方版本与证据引用齐全。
- 最终候选再次从页面执行 `01a1bade-200d-7cbd-be53-28455ba39875`，退出码 0，219 字节输出完整归档，后检查取得六条执行后的证据、`pod-health` 配方 `01a1bacf-e4e6-7088-88b1-94b39e4c15ff`，结论 `resolved`。详见 `final-submission.json`、`final-execution.json` 和最终重启记录。

配方检查覆盖目标 Pod、其原生关联控制器健康以及执行后的相关 Warning，不证明历史重启根因，也不证明所有应用功能健康。

## 未知结果与防重复

演练执行 `01a1bad8-e6c2-7a0d-a2fb-453acf5db8dc` 使用同一个只读查询加 `sleep 90`，配置超时 60 秒。Runner 完成领取并回传 219 字节后，仅强制删除本次演练的 Runner Pod，以模拟丢失最终回报；未删除或修改业务目标。

Worker 对账实际失败的 Job，将结果记为 `execution_unknown`、退出码空值、`automaticRetry=false`。已接收输出仍归档，`outputComplete=false`。页面保留输出并提示结果不确定、禁用新提交。相同幂等键的重放返回原准备响应及同一个执行 ID（202），不会刷新为当前状态或创建新尝试；当前状态必须通过 GET 获取。换用新幂等键、复用已消费确认返回 409。数据库和 Kubernetes 均证明该执行只有一次 attempt、一个 Job，API/Worker 重启后仍未再次执行。

目标当前 UID、Running/Ready 与已归档输出和 Job 终态分别核对。目标健康不能证明丢失的命令结果，因此未知记录保持未知，不伪造退出码，也不将其替换为成功。`unknown-result.json`、重启记录及 `execution-audit-final.json` 保留完整对账依据。

## 本轮产品修复与失败保留

- c67bebf：页面所有原生命令要求密码和 TOTP；接受的原生验证证明在异步 dispatch 和 claim 前重新核验，撤权不能沿用旧证明。既有 namespace API 不要求 MFA 的合同仍保留，不能将页面增强误报为所有 API 已强制 MFA。
- 3e248cf：服务端策略评估与持久原生授权保持相同的叠加范围语义，修复本人确认时的 403；传统权限交集边界保持不变。策略测试覆盖原生 cluster/namespace 授权、越界拒绝及 live/frozen decision 一致。
- 65fa5d3：验证合格 Kubernetes List 后补齐列表项省略的类型信息；按精确关联对象 UID 查询 Warning，避免无关命名空间事件耗尽证据预算。分页、不完整身份、类型冲突及越界仍拒绝判为恢复。
- 0d19b04：识别 `kubectl` 动词前已知的字面量全局参数，消除实际只读查询的修改风险误报；未知参数、动态展开及修改命令仍保守处理，风险分析版本升级为 v2。人工 Bash 的整体风险下限不因此降低。
- 原生集成测试的 TOTP 注册助手复用已算出的证明码，避免计算跨时间边界导致偶发失败；初次失败后已修复并完整重跑通过。
- 最终镜像首次打包写错文件路径，运行入口仍为上一版；该候选未登记接受，摘要保留在 `rejected-images.json`。修正后从配置入口 `/ops-api`、`/ops-worker` 提取二进制并独立核对摘要，再通过真实页面 v2 风险评估及执行验证。
- Worker 启动阶段出现过明确的 `archive worker unavailable` 503，保留在 `restart-transient-reads.json`；就绪后重新读取全部旧引用通过，未将短暂不可用掩饰成全程正常。

## 检查与范围

最终受影响 Go 包 `action`、`httpapi`、`app`、`policy` 通过，见 `unit-final.log`。真实独立 PostgreSQL 上的 NativeCommandHTTP 集成用例全部通过，覆盖并发、确认消费、namespace 兼容合同、已接受证明的撤权、会话到期及绑定变更，见 `native-command-integration.log`；该批运行早于最后仅风险分类的修复。此前本轮 Web 定向测试及 TypeScript/构建通过，后续修复未改动 Web。

最终版本重读原调查会话、十个持久 QueryResult、三条证据和冻结 RCA 811 均为 200；来源注册已升为版本 2 的版本 1 历史归档仍为 `archived_verified`。未授权集群读取 403；未登录读取旧证据及命令记录均 401。没有以重新采集冒充历史复盘。详见 `investigation-regression.json`、`anonymous-denied.json`。

数据库保持 schema 74。命令阶段前已有独立备份 `20261108T103430Z`，执行前配置及配方状态另有备份 `20261108T105948Z`；归档载荷保留在独立数据目录，不宣称数据库备份含完整载荷。隔离集成数据库因审计保留，停止专属容器后不自动删除。账号密码、TOTP 种子、Registry 私钥、Runner claim token 均未收入证据。

本记录完成独立开发环境内调查衔接及受控只读命令机制验收。未知演练记录有意保持未知；未验收修改业务资源的处置。22 个菜单逐项收口、HTML 1:1、离线安装/升级回退等完整发行演练仍需单独推进。KubeVirt/CDI、Trace 和生产发布继续延期。
