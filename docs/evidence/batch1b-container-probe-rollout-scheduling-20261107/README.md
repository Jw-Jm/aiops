# 第一批后续：CrashLoopBackOff、OOMKilled、探针失败、发布卡住与网络异常诊断验收

2026-11-07。五个新专项 recipe 复用第一批确立的链路（信号→事实→finding→故障→签名 recipe→RCA→恢复），在同一真实集群上完成证据充分/不足/恢复三状态验收。

## 交付内容

1. **五个专项 recipe**（schema 枚举 + const 分支 + builtin + `recipes/<name>/v1/recipe.yaml` + 冻结金样）：

| recipe | candidateType | 必需事实 | 终态 |
|---|---|---|---|
| pod-crashloop-failure | pod_crash_loop | pod-crash-failing（等待原因 CrashLoopBackOff 且 lastState 非 OOM）+ container-backoff-error（kubelet BackOff 事件）+ pod-path | confirmed |
| pod-oom-failure | pod_oom_killed | oom-killed（lastState.terminated OOMKilled）+ oom-exit-observed（退出码 137）+ pod-path | probable（单一权威源：kubelet 容器状态） |
| pod-probe-failure | pod_probe_failure | probe-failing + probe-kind-observed（kubelet Unhealthy 事件，消息含 "probe failed"）+ pod-path | probable（单一权威源：kubelet 探针事件） |
| deployment-rollout-stuck | deployment_rollout_stuck | rollout-stuck（Progressing=False/ProgressDeadlineExceeded）+ blocking-pod（图谱 owned 链上门控的后端 Pod 故障事实）+ rollout-path（Pod→RS→Deployment managed_by 链） | confirmed |
| pod-scheduling-failure | pod_scheduling_failure | pod-unschedulable（PodScheduled=False/Unschedulable）+ scheduling-blocked + scheduling-cause-detail（调度器 FailedScheduling 事件，消息即原因细节；hostPort 冲突为网络类确定性注入） | confirmed（未调度 Pod 无拓扑锚点，事实即证明） |

2. **信号与事实**（`official.go`/`sp05_inspection.go`）：Pod 等待原因按 lastState.terminated 区分 OOM 与崩溃循环；容器 Running+ready 结算全部 pod 症状及 kubelet 事件 occurrence；Deployment Progressing 条件；kubelet BackOff/Unhealthy 事件按有界消息鉴别。

3. **多 EndpointSlice 聚合**（`sp05_inspection.go`）：ServiceNoEndpoints 按服务聚合全部 slice 的 ready 计数后提交单次观测（独立组 `endpointslices/<service-uid>`），消除单 slice 空集翻转。

4. **对象删除结算**（`informer.go`/`snapshot_scope.go`/`sp05_inspection.go`）：Snapshot 新增 Deleted（watch 墓碑 + 权威重列差异，均经命名空间过滤），被删对象的全部故障症状按 resolved 结算（subject-gone 语义，非健康恢复声明）。作用域变更隐藏的对象不会被误判为删除。

5. **间歇性故障语义修正**（`engine.go`）：crashloop/OOM/探针的通过窗口只结算生命周期，不构成对故障的反驳（resolved 事实为中性证据）；PVC Bound/节点 Ready/发布完成/已调度仍为反驳（非间歇条件）。冻结测试与金样相应更新。

6. **Worker 选方修正**（`sp05_worker.go`）：按故障主体上全部 finding 症状选择 recipe（OOMKilled 优先于 ContainerBackOffError——kubelet 对 OOM 重启容器同样发 BackOff 事件）。

7. **环境事件与排查**（`docs/operations/current-environment.md` 已记录）：
   - 主机网络变更为 10.10.10.2（netplan），以非持久辅助地址恢复 192.168.0.63；生产采集自愈；持久化或整体迁移待决策
   - `lease_cas` 慢告警根因定位：共享限速预算槽位排队（QPS 10 控制类让位 ≈2 槽 ≈200ms）被计入阶段计时；apiserver 写路径实测健康（27–34ms），良性误归因，无需修复

## 三状态验收（真实集群，最终通过 run14，541.13s）

注入：busybox 无命令（崩溃循环）、busybox awk 倍增串 + 64Mi 限制（确定性 OOMKilled/137）、失败就绪探针、坏镜像 Deployment（progressDeadlineSeconds=150）、hostPort 31888 冲突对。恢复：镜像修复（crash）、发布完成（rollout）、真实删除（OOM/探针/端口持有者——验证删除结算）。

| 断言 | 结果 |
|---|---|
| crash 证据充分 | confirmed / pod_crash_loop / missing=[] |
| OOM 证据充分 | probable / pod_oom_killed / missing=[]（单一权威源设计） |
| 探针失败 证据充分 | probable / pod_probe_failure / missing=[] |
| 端口冲突（网络）证据充分 | confirmed / pod_scheduling_failure / missing=[] |
| 发布卡住 证据充分 | confirmed / deployment_rollout_stuck / missing=[] |
| 全命名空间恢复 | 修复 + 发布完成 + 删除结算后全部跨 settle 窗口 resolved |

证据不足状态由各 recipe 的 missing 语义与冻结金样 missing 模式覆盖（此五场景的"不足"注入即修复前的中间修订，如 rollout 在阻塞 Pod 事实到达前 missing=[blocking-pod, rollout-path]，见 run7/8 日志记录）。

## 排查记录（实现过程中定位并修复的真实缺陷）

1. **Deployment canonical 组名错误**：信号侧硬编码 `core/Deployment`，采集侧为 `apps/Deployment` → 图谱查询 ErrScope → rollout 永远图谱不可用。修复：`CanonicalGroupFor(kind)` 共享推导。
2. **OOM 注入的运行时陷阱**：内存 tmpfs（emptyDir medium=Memory）在此 containerd 上任何限制下都在 runc init 期 OOM（StartError/128 而非 OOMKilled/137）；32Mi 直接 dd 同样 init 期被杀。确定性注入 = busybox awk 字符串倍增 + 64Mi 限制。
3. **间歇性故障的矛盾语义**：短暂 Running+ready 窗口产生的 resolved 事实在 5 分钟窗口内与 firing 冲突 → 永久 unresolved。修复：crashloop/OOM/探针的通过窗口为中性生命周期证据。
4. **ContainerBackOffError 事件的选方歧义**：kubelet 对 OOM 重启容器同样发 BackOff 事件 → OOM 故障误选 crashloop recipe。修复：按故障主体全部症状选方，OOMKilled 优先。
5. **删除结算的三处断点**（逐一实测定位）：namespaceUnion 重发布丢弃 Deleted 增量；结算观察时钟用了陈旧重列时间被防翻转守卫拒绝；watch 墓碑增量在采集器重启/重连窗口会丢失（rollout 级联删除实测丢失）——以缺席兜底（与身份退役同基）补全。
6. **环境**：主机网络变更（10.10.10.2）后 kubeconfig/生产配置指向的 192.168.0.63 失联；以非持久辅助地址恢复。`lease_cas` 慢告警定位为共享限速预算槽位排队的计时归因（详见 docs/operations/current-environment.md）。

## 回归

- 冻结金样 `TestRecipeFrozenGoldenConfirmation`：10 recipe × 9 模式通过
- `TestBuiltinRecipesMatchSchemaContract`：15 个 builtin 与 schema 一致
- 删除结算探针 `TestSP05DeletionSettlesFindings`：PASS（20-24s）
- informer 删除转发单测 `TestWatchDeletionSettlesThroughNamespaceUnion`：PASS
- 第一批两场景活链路验收、金样垂直链（12/12）、r55 PVC 活门禁：随本批最终代码重跑，结果见本目录与各自记录

## 生产部署与激活（2026-11-07）

- 候选版本 `6f3cf535f2f4b48e0e49fbd6cb1c22d47ea383b9`（batch1b + 投影 v4 升级）按迭代流程部署：预部署备份（schema 74 无迁移）→ 摘要钉扎镜像滚动 ops-api/ops-worker → Web 切换 → `release.py record` 原子更新 current 指针。
- 中间版本 a0764bae6 暴露**投影归档身份冲突**：事件投影新增字段（reportingComponent/有界 message）改变了内容但身份版本未升，与不可变 v3 历史归档冲突使采集源持续降级。修复为投影身份升级 `kubernetes-projection/v4`（提交 6f3cf535f），历史 v3 归档原样保留；降级清除后实测 `degraded=[]`。
- 10 个 recipe 经管理员会话（密码+TOTP step-up，逐请求挑战绑定）用 current-registry 密钥签名发布并在租户范围激活；辅助工具 `cmd/activate-recipes`（仅签名）入库。
- **生产实际入口端到端验收**：注入 registry 不可达 Pod（acceptance-pull）→ 两个故障（ImagePullFailure/ImagePullError）→ 当前 RCA **confirmed / pod_image_pull_failure/v1 / missing=[]** → 修复镜像后双故障跨 settle 窗口 **resolved**；未认证 API 访问 401；resources/incidents/overview/capabilities/audit-records 200；回执见部署主机 `evidence/acceptance.json`。
- 部署后已知事项：Web 容器挂载改用解析路径 `/mnt/ops-sdb/ops-current/...`（record 工具 root.resolve() 与 sdb 符号链接一致）；192.168.0.63 辅助地址仍为非持久（见 current-environment.md 网络决策项）。
