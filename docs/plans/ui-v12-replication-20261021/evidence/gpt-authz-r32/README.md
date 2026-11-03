# r32：采集收敛、短期凭据自动轮换、503 根因与原生管理接续（2026-10-27）

环境：`ssh mssc-direct`，namespace `ops-native-v12-r24-20261026`，任务根 `/data/ops-v12-native-visual-r24-20261026`（本轮目录 `r32-worker/`、`r32/`），PG 15454/schema70，入口 30471（Web r14）。API/Worker 已升级为本轮源码构建的 r32 二进制（r32-worker/ops-api、r32-worker/ops-worker，hostPath 挂载），清单 `r32-worker/api-pod-r32.json`、`r32-worker/worker-pod-r32.json`。无迁移（1～70 不变），无模型调用，不涉及虚拟化。

## 1. 采集 RBAC 收敛（review 项 1）

- 新身份绑定 Worker 自身 SA `ops-worker`（kubelet 只能为 Pod 自身 SA 投射令牌；独立 SA 只能回到静态令牌或授予签发任意 SA 令牌的升级权限，两者都被拒绝）。
- 命名空间 Role `r32-sp04-collect`：任务 ns 内 11 类 namespaced Kind get/list/watch。
- ClusterRole `ops-native-v12-r24-20261026-r32-sp04-cluster-read`：仅 6 类无命名空间 API（nodes、persistentvolumes、storageclasses、csidrivers、csinodes、volumeattachments）。保留原因：锁定 17-GVR 合同要求全部 GVR 才能进入 graph ready；去掉 PV/VolumeAttachment 会让图永远 unready。仅元数据引用被投影，Secret 载荷/环境变量/命令在投影层被剥离。
- 旧全局凭据链已回收：ClusterRole/ClusterRoleBinding `…-r31-sp04-read` 删除、SA `ops-r31-sp04-read` 删除、Secret `r31-cluster-read` 仅存 ca.crt；旧静态令牌（`r31/cluster-read-token`，0600 留档）直接调 API 返回 401。
- `kubectl auth can-i` 实测（SA 绑定后）：任务 ns list pods/deployments/events/endpointslices=yes；`default` ns 与 `--all-namespaces`=no；secrets/get、create/delete pods=no；六个集群级资源=list yes；lease 仅 `r31-graph-lease` get/update=yes、其他 lease=no。
- 采集端一致化：`SP04Cluster.Namespaces`（配置必填，启动校验 namespaced GVR 的 scope 必须与登记完全一致，多余/缺失/重复/非法名/漂移拒绝，lease ns 也必须在 scope 内）；namespaced GVR 走真实 `/api/v1/namespaces/<ns>/<res>` 路径，集群级 GVR 不带 namespace；resourcestore 过滤仍保留为第二道防线。
- chart 同步：`deploy/charts/ops-platform/templates/sp04.yaml` 改为相同模型（原全局 ClusterRole 删除；新增命名空间 Role+Binding、受限 ClusterRole+Binding、lease Role 增加 patch、cluster 值必须带 Namespaces 否则渲染失败）；`deployments.yaml` 投射令牌 3600s→600s。合同测试 `test/contract/helm_sp04_scope_test.go`（渲染 RBAC 断言 + 无 Namespaces 拒绝）、`sp04_chart_test.go` fixture 更新。

## 2. 短期凭据自动轮换（review 项 2）

- Worker 采集凭据改为 Pod 投射 ServiceAccount 令牌（`/etc/r31-projected/token`，600s，kubelet 自动轮换），CA 来自 Secret `r31-cluster-read` 仅 ca.crt；令牌文件逐请求读取，缺失/空/多行/非 JWT/长度异常全部拒绝（`internal/app/sp04_credentials.go`，单测 `sp04_credentials_test.go`）。
- 轮换证据（worker 日志，只记录摘要与时刻）：2545bbc4（iat 08:21:25/exp 08:31:25）→ ee3622d6（08:30:04）→ df0b6b85（08:38:18），观察器 5s 内捕获新文件。
- 跨轮换持续采集：`r32-worker/rotation-reads.json` 08:28:23→08:33:52 连续 22 次资源读全部 200、55 条、outsideScope=0，跨越 08:31 轮换边界；期间无采集失败日志。不再需要手工续 24h 令牌。

## 3. 503 根因实测与最小修复（review 项 3）

- 探针构建（仅加测量，不改语义，同一部署其余部分相同）：60 次连续读（间隔 50ms）→ 3×503 GRAPH_NOT_READY；worker 日志 4 条 `Graph Lease confirm unavailable reason=gate_timeout renewing=true`；续约分段耗时 `lease_read≈230ms`、`lease_cas≈252ms`（每 10s 与 17 informer 的连通性探测叠加时出现），即续约全程 ~500ms 超过 Confirm gate 300ms 等待——读取请求在续约持锁窗口内超时。原始证据 `r32-worker/probe-tight-reads.json`。
- 修复（`internal/graph/lease.go`）：查询路径不再排队/受限于续约栅栏；所有权快照在 `lease_read` 阶段一次捕获，Confirm 用同一快照做 UID/epoch/holder/renewTime 复检，失败仍 fail-closed（ErrNotReady），所有权变更/服务断开/过期行为不变；租约 SQL 未改。
- 修复后同脚本 60 连读 0 失败、0 confirm 失败；续约仍可见 497–717ms 的 slow 告警（记录用），但读取不再被栅栏挡住。部署后 15 分钟内 0 采集失败。

## 4. kubernetes 来源 nativeTenant 输入校验（review 项 4）

- `internal/source`：新哨兵 `ErrScopeIncompatible`；`validateTypeScope` 在 Register/UpdateRegistration/RotateCredential/RollbackRegistration 全部入口执行（k8s 来源拒绝 nativeTenant、仅接受 cluster+namespace scope 且 cluster 必须等于登记 UID；rotation 对存量不兼容行拒绝并提示先修 mapping，避免把不可采集来源重新激活）。
- 存量不兼容行的呈现：`queryCapability.reason=incompatible_data_scope_mapping`（OpenAPI enum 同步），更新路径可修复。
- 单测 `internal/source/scope_test.go`、`internal/httpapi/source_scope_test.go`；PG 集成用例 `test/integration/source_kubernetes_scope_test.go`（本地构建通过；本轮未在远程任务库执行，执行环境见该文件头部注释）。

## 5. 原生管理接续（review 项 5）

- B（新身份停用/恢复/并发反例）：`r32/account-lifecycle.json` 全部通过——真实 UI 创建零角色新身份 `r32.acc.1793091626814`；停用后登录 401 UNAUTHENTICATED；恢复后登录+首改密 201+会话 200；并发 disable/restore 与并发同向写（同一 expectedRevision，两次独立 challenge、TOTP 各自独立 30s 步）恰好一个 200、败者 409 CONFLICT；全程每次管理写均为密码+TOTP 步升（TOTP 每 30s 步只可消费一次，见 00051 `mfa_record_totp_step`）；结束后 fixture active（revision 7、0 角色）。
- C（历史主体）：live 环境无旧主体，r31 已实测 inventory 导入被守卫拒绝（正确）；本轮未新增，正反例 API/UI 路径仍需一次性独立测试库环境，登记为剩余。
- D：chart 渲染/配置一致性本轮完成（见第 1 节）；无 OIDC 隔离冷启动（整栈新 namespace+新库+chart 安装+first-tenant）仍未执行，正式 Bundle 物料缺口继续保留。

## 版本与回退

- r32 Worker sha256 `2752842d…`、API sha256 `5057b8d3…`；源码为 platform/main 082f87d66c5b + r23..r32 未提交工作树。
- 回退：`kubectl apply -f r31/api-pod-r31.json`、`r31/worker-pod-r31.json`，configmap r28-runtime 的 sp04.json 恢复为 `r32-worker/sp04.before.json`（Namespaces/TokenFile 回退）；RBAC 回退 `kubectl apply -f r32-worker/collection-rbac.yaml` 反向（删除 r32-* 并恢复 r31 ClusterRole 需重建，见 r31/rbac.yaml）。回退不影响数据库与身份数据。
- 本轮无截图、无秘密入库；凭据只在远程 private/。
