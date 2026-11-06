# 第一批：Pod 镜像拉取失败与 Service 无可用后端真实诊断验收

2026-11-06。候选代码为本目录记录时的 Git 工作树（提交号见下方记录）。所有结果来自真实集群 `mssc`（192.168.0.63）与一次性隔离 fixture（PG17.11 / OpenBao 2.7.0 / Keycloak 26.7.4，均本地镜像 `--pull=never`），无 fixture 冒充真实链路。

## 交付内容

1. **两个专项诊断 Recipe**（`pod-image-pull-failure/v1`、`service-no-endpoints/v1`，candidateType `pod_image_pull_failure` / `service_no_ready_endpoints`）：
   - schema：`api/schemas/recipe-registry-v2.schema.json`（candidateType 枚举 + 两个 const 分支）
   - builtin：`internal/rca/recipe.go` `BuiltinVersion`；声明镜像 `recipes/<name>/v1/recipe.yaml`
   - 引擎事实谓词与图谱路径：`internal/rca/engine.go`
2. **信号提取**（`internal/inspection/kubernetes/official.go`）：
   - Pod `containerStatuses/initContainerStatuses` 等待原因 `ErrImagePull`/`ImagePullBackOff`/`ErrImageNeverPull` → `ImagePullFailure`（firing）；容器 Running+ready → 同时 resolved `ImagePullFailure` 与 `ImagePullError`
   - kubelet 拉取失败 Event（Warning、involvedObject=Pod、reportingComponent=kubelet、消息含拉取特征）→ `ImagePullError`
   - EndpointSlice（controller ownerRef→Service UID）ready 端点计数 0 → `ServiceNoEndpoints`（firing，目标为 Service）；ready>0 → resolved
3. **Worker 故障→Recipe 匹配**（`internal/app/sp05_worker.go`）：按链接 finding 的 normalizedSymptom + 资源类型选择专项 recipe，Pod 不再默认落到 node-failure
4. **Web**：`RCAPanel.tsx` 新增两个候选类型中文标签（tsc + 267 前端单测通过）

## 修复的三个真实缺陷（本批暴露，均先在无改动基线复现或定位）

| 缺陷 | 位置 | 影响 | 修复 |
|---|---|---|---|
| 投影读取 `reportingController`，而真实 core/v1 Event 字段为 `reportingComponent`；`message` 完全未投影 | `projector.go` / `observations.go` / `sp05_inspection.go` | 活链路所有 Event reporter 恒为空：`csi-error` 永不匹配（PVC 链路无法 confirmed，冻结单测掩盖此缺陷）；拉取错误无法归因 kubelet | 投影/fact 改读 `reportingComponent`（legacy `source.component` 兜底），投影增加有界 `message`（≤1024）；历史投影仍刻意不含 message |
| 命名空间故障的 RCA 图谱收窄时丢弃集群级对象（Node/StorageClass/CSIDriver），`scope_limited` 置 Partial 一票否决路径判定 | `sp05_worker.go` / `engine.go` | 命名空间故障结构性无法 confirmed：本批 Pod 路径与 PVC `storage-path` 均受阻；**金样垂直链 PVC/valid 在无本批改动的基线即失败（b3d7208b5 起预存回归）** | worker 命名空间范围保留 `""`（集群级路径上下文，与冻结金样契约 `nativeEntityCanonicalIds`/`impactPartial:false` 一致）；引擎采纳 analytics 既定准则：仅 `scope_limited` 的 Partial 不降级可见证据判定 |
| 30 秒窗口新证据恒为 `archive_pending`，被计入 DegradedSources → 恒 Partial | `engine.go` | 归档验证延迟期间 confirmed 结构性不可达（生产归档跟得上时掩盖，fixture 环境暴露） | `archive_pending` 不投票也不算已验证证据的降级；归档永不验证时 required fact 保持 Missing |

另修复金样 fixture 与真实 API 字段漂移：`pvc-csi-failure/v3/input.json` Event 改用 `reportingComponent` + 补真实消息文本，重算 manifest SHA。

## 三状态验收结果（真实集群，测试 `TestSP05ActualPodImagePullAndServiceNoEndpointsDiagnosis`）

注入：命名空间 `sp04-chain-*`（测试自有，含真实采集器绑定）。

| 场景 | 证据充分 | 证据不足 | 恢复 |
|---|---|---|---|
| Pod 镜像拉取失败 | `batch1-pull-a`（registry.example.invalid 不可达）：status=**confirmed**，candidate=`pod_image_pull_failure`，missing=[]，双独立证据（Pod 等待原因 + kubelet Failed 事件）+ 图谱锚点 | `batch1-pull-b`（`imagePullPolicy: Never` + 本地不存在镜像）：status=unresolved，missing=[image-pull-error]（症状在、拉取事件证据缺） | `set image` 修复为本地 pause 镜像 → Pod Running → 两症状 finding resolved → 故障跨 settle 窗口转 **resolved** |
| Service 无可用后端 | `batch1-svc-a`（选择坏镜像后端）：status=**confirmed**，candidate=`service_no_ready_endpoints`，missing=[]（EndpointSlice ready=0 + 选择器关联后端未就绪事实 + selects_pod 路径） | `batch1-svc-empty`（选择器无匹配）：status=unresolved，missing=[backend-unready, service-selection] | 后端修复 ready>0 → resolved | 
（恢复列以本文件下方运行记录为准；service 空选择器恢复通过 patch 选择器指向已修复后端验证。）

## 回归

- `TestRecipeFrozenGoldenConfirmation`：5 recipe × 9 模式（含 missing/conflict/degraded/expired/duplicate）通过
- `TestBuiltinRecipesMatchSchemaContract`：10 个 builtin 声明与 schema 一致
- `TestSP05FrozenNonvirtualGoldenVerticalAndReplay`：12/12 子测试通过（修复前基线 PVC/valid 即失败，已修复）
- `TestSP05ActualOrbStackInspectionAPIWorkerArchiveAndRestart`（r55 PVC 活门禁）：PASS（89s）
- `internal/...` 全部单测、web tsc + 267 单测通过

## 运行记录（2026-11-06，最终通过 run10）

- **本批验收 `TestSP05ActualPodImagePullAndServiceNoEndpointsDiagnosis`：PASS（360.98s）**，摘要见 `run10-summary.txt`；四状态结果：

| 断言 | 结果 |
|---|---|
| pull-a 证据充分 | confirmed / pod_image_pull_failure / missing=[] |
| pull-b 证据不足 | unresolved / missing=[image-pull-error] |
| svc-empty 证据不足 | unresolved / missing=[backend-unready, service-selection] |
| svc-a 证据充分 | confirmed / service_no_ready_endpoints / missing=[] |
| 四故障恢复 | 全部跨 settle 窗口转 resolved |

- 金样垂直链 `TestSP05FrozenNonvirtualGoldenVerticalAndReplay`：12/12 PASS（修复前基线 PVC/valid 失败）
- Node 硬件垂直链 `TestSP05FrozenNodeHardwareUpstreamVerticalAndReplay`：4/4 PASS（69.42s）
- r55 PVC 活门禁 `TestSP05ActualOrbStackInspectionAPIWorkerArchiveAndRestart`：PASS（89.07s）
- `internal/...` 全部单测、web tsc + 267 单测通过

## 已知边界

- 对象删除不产生 resolved 信号是既有语义（当前库 15 个残留 open 故障即源于已删除对象）；本批未改变该行为。
- 多 EndpointSlice 服务按 slice 独立观测，可能出现单 slice 空集翻转（代码内已注明 shortcut）。
- `lease_cas` 慢告警为既有已知问题，与本批无关。
