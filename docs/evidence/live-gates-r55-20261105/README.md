# r55：15 个活环境门禁集成测试的启用与修复

2026-11-05。`artifacts/test-reports/integration-r55d.jsonl` 中 15 个失败全部为活环境门禁（无代码回归）。本轮为其中可在本机满足依赖的测试提供一次性隔离 fixture（PG17.11 / OpenBao 2.7.0 / Keycloak 26.7.4，全部 `--pull=never` 本地镜像），并修复四处只在真依赖下暴露的缺陷。fixture 供给脚本：`/data/ops-v12-native-visual-r24-20261026/r55/prepare-live-gates.py`（PG+OpenBao）与 `prepare-keycloak.py`（Keycloak）。

**最终结果：12 通过 / 3 环境阻塞。**

## Fixture（一次性隔离容器，标签 `ops.platform.test=r55-live-gates`）

| 容器 | 镜像（本地，never pull） | 用途 |
|---|---|---|
| `ops-r55-pg-20261105` | `ops.local/amd64-poc/postgresql-bookworm:17.11`（`589f742565e4`） | `SP03_TEST_DATABASE_URL`，一次性库迁移到 74 |
| `ops-r55-openbao-20261105` | `ops.local/amd64-poc/openbao:2.7.0`（`c2c7ce2b656d`） | `SP03_TEST_OPENBAO_URL/TOKEN/CA_FILE`，inmem+TLS，单 share init/unseal |
| `ops-r55-keycloak-20261105` | `ops.local/amd64-poc/keycloak:26.7.4`（`b2f3e1b85071`） | `SP03_KEYCLOAK_TEST_ISSUER/ADMIN_USERNAME/ADMIN_PASSWORD`，导入 `deploy/keycloak/realm-ops.json` |

凭证只落在 /tmp 下 mode-0600 env 文件（目录 0700），不进 Git、不打印。Archive fixture 镜像：`OPS_TEST_ARCHIVE_IMAGE=sha256:31d61f5e…`（本地 SeaweedFS 4.47，即提交 `7b4b85332` 解锁的镜像）。

## 结果（15 项）

**通过 11**：

- SeaweedFS 组（`7b4b85332` 修复后）：`TestRealArchiveTenantIAMTLS`、`TestIntegrationArchiveFixtureSurvivesCleanupAndActualRestart`。
- PG+OpenBao 组：`TestSP05FrozenNonvirtualGoldenVerticalAndReplay`、`TestSP05FrozenNodeHardwareUpstreamVerticalAndReplay`、`TestSP05GoldenGraphOnlySourceWithdrawAndFindingRevisionFence`、`TestSP06ActualArchiveRetentionHoldAndReferenceRevocation`。
- OpenBao 组：`TestSP06RealTransitScopedSigningRotationAndTakeoverContext`、`TestSP06StandardMCPToolStateReplayAndDenialMatrix`。
- 本轮修复后通过：`TestSP06NarrowedContextCannotReadOrCommitD1`（66s）、`TestSP06ActualCurrentRCARecommendationValidator`（23s）、`TestSP05ActualOrbStackInspectionAPIWorkerArchiveAndRestart`（76.2s，Keycloak+PG+OpenBao+archive 镜像+真实 k8s；全链：原生采集→inspector→统一摄取→事件→真实归档→OIDC API 重放）、`TestSP05OfficialObservationsActualOrbStackAndControlledProbeFailure`（57.0s，真实 Node/来源→官方探测→统一摄取→事件→归档，含 503/403 受控观测故障与真实恢复）。

**环境阻塞 3**（非代码缺陷）：

- `TestSP05LockedAnalyzerRealOrbStackSandboxAndNativeFaults`：锁定哈希 `e6ce7073…`（amd64）的 K8sGPT 二进制在本机不存在（唯一候选哈希不符），离线无法构建。
- `TestSP05ActualLinuxWorkerAuthorizedAnalyzerDurableChain`：需要指针文件 `/tmp/ops-sp05-k8sgpt-current-dir` 指向锁定 arm64 CLI（`6f9152ff…`），且测试硬编码 `GOARCH=arm64` 构建镜像，而唯一集群节点 `mssc` 为 amd64——即使取得二进制也无法调度运行。
- `TestSP06RealHolmesModelMCPJobAndSSE`：Ollama 本地无 `llama3.1:8b-16k`（registry 报 file does not exist），`services/investigator/.venv` 缺失且主机无 `uv`。

## 本轮代码修复（4 处，均为只在真依赖下暴露）

1. **fixture 预算**（`sp06_job_test.go`/`sp06_review_boundaries_test.go`/`sp05_golden_vertical_test.go`）：`sp06JobFixture` 增加 Timed 变体；D1 边界与 current-Graph validator 用 15 分钟预算。根因：默认 3 分钟 fixture ctx 在慢 docker 守护（每个保留 fixture 回收约 65s）下，第一条查询前就到期。
2. **Graph 探针时效**（`sp05_golden_vertical_test.go`）：validator 分支在进入原生 Lease 前以真实时钟重戳 required GVR 的 `LastListCompletedAt/LastConnectivityProbeAt`。根因：`lease.Tick` 按真实 `time.Now()` 校验 45s 探针新鲜度，而偏移后的 golden 时间线此时已数分钟旧 → `GRAPH_NOT_READY`。
3. **巡检 scope 与契约漂移**（`sp05_live_test.go` + `api/schemas/incident-{v2,summary-v2}.schema.json`）：
   - `collector.Namespaces = []string{ns}`——`validateCollectorScope` 拒绝隐式广播，要求 worker 的 namespace scope 显式且等于注册 scope；该测试是本机第一个走 `StartSP04Worker`+真实 collector 的用例。
   - schema 补 `assigneeSubject`（string）与 `assignedAt`（nullable date-time，均 required）：r55 migration 73 起 `Incident` 结构体与列表 SQL 都发出这两个字段，但两个 `additionalProperties:false` 的 schema 未声明——活门禁首次走到公共 API 响应校验时暴露。OpenAPI（`platform-v1.yaml:3078`）与生成 TS（`incidentV2.ts`）此前已声明，仅 JSON schema 落后。
4. **官方观测快照缺投影注解**（`sp05_official_observations_test.go`）：`resourcestore.Repository.observe` 要求对象带 informer 经 `kube.Project` 打上的 `ops.internal/observed-at` 溯源注解，该测试此前直接喂原始 API list 对象（golden fixture 均自带此注解）——改为逐对象过生产投影 `kube.Project`。

另有一处测试基础设施改进：`sp04Kubectl` 失败时带出 kubectl stderr（本轮两次环境类失败均靠它直接定位，不再需要手工复现）。

## 环境事件（与仓库代码无关，记录备查）

**kube-scheduler crash-loop（影响 OrbStack 门禁的一次偶发）**：排查一次"Unschedulable 摄取为 0"的失败时，用临时探针证实产品代码与 watch 语义均正常（LIST→初始事件→bookmark→MODIFIED 全链可复现），根因是本实验集群的 `kube-scheduler-mssc` 自 2026-10-26 起 crash-loop（当日 417 次重启，约 9 分钟周期、每次宕机约 60s）。失败 run 的 pod 恰好创建于一次宕机窗口（07:26:29Z–07:27:35Z）内，调度器在测试最后一次轮询前 1-2 秒才恢复。调度器存活窗口内重跑即通过（76.2s）。如需稳定验收，应先修复 kube-scheduler 的崩溃循环。

**apiserver openapi/v2 瞬时停摆与主机 IO 饱和**：官方观测门禁两次重跑在 fixture 创建即失败：kubectl 客户端校验拉取 `/openapi/v2?timeout=32s` 等待响应头超时（手工同 URL 仅 0.05s）。同时段主机 load ≈15-30、iowait ≈74%、sda %util ≈89%（读延迟 50-70ms），apiserver 聚合层因 `v1beta1.metrics.k8s.io` APIService 503（metrics-server 缺失）反复重排队。第三次重跑自然通过（56.95s）。

## 运行方式（复现）

```bash
source /tmp/ops-r55-live-gates-f4y49x2e/credentials.env      # PG+OpenBao+archive image
source /tmp/ops-r55-keycloak-ndxi8d_n/credentials.env        # Keycloak（仅 OrbStack 巡检需要）
/opt/go/bin/go test ./test/integration -run '^<TestName>$' -count=1 -timeout 60m -v
```

fixture 容器与 `/root/.cache/ops-protected-integration/` 下保留材料（protected-archive-fixture）有意保留，不删除。凭证 env 文件为一次性，会话结束即失效于 /tmp。
