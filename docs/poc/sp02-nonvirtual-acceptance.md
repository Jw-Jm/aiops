# SP-02 非虚拟化验收范围与证据

范围依据：AGENTS.md 与 ADR-0008。KubeVirt/CDI、VM 生命周期及虚拟化分项延期、未验证；不以 Fixture、控制面状态或开发机版本矩阵代替运行验收。

## Task 状态

- **2.1 Deployment Profile**：提交 `8e53958`；digest-only 实例解析由 `27ced22` 修复。Schema、探测、精确解析及拒绝条件已有测试；本轮 `go test ./... -count=1` 退出 0。core resolved Profile 保留 KubeVirt/CDI disabled、compatibility unverified。
- **2.2 Core Charts**：提交 `f880754`。本地 Chart、digest 镜像、Secret 和 NetworkPolicy 约束由 Chart/Bundle 测试验证；真实 core 安装与重装使用这些 Chart，见 2.7。
- **2.3 OpenBao**：提交 `6b37c4e`。实际持久化、人工 Shamir 解封、错误密钥、配置漂移和重启恢复记录见 `docs/runbooks/openbao-bootstrap.md`。2.7 重放再次验证真实 TLS 与 unsealed 状态；不声称生产 HA/auto-unseal 已验收。
- **2.4 Victoria**：提交 `0c5d429`。core 复用既有 external VictoriaMetrics/VictoriaLogs，无重复部署；标准 Bundle 保留 qualified fallback 镜像、Chart、对应源码、许可证和 SBOM。现场版本与 fallback 物料各自精确锁定。
- **2.5 DeepFlow**：完成提交 `1109306`；现场验收和最终修复证据见 `docs/poc/deepflow-v7.2.0-orbstack.md`。该报告明确区分历史 fixture_only 失败与新的实际 live 结果，不以 Pod Ready 代替 Agent/Querier/公网拒绝门禁。
- **2.6**：整体延期，不属于本轮验收通过项。
- **2.7 Core 离线安装/干净重装**：实现提交 `7121fd5`，真实证据提交 `f3ef3c4`，源绑定记录 `0eadb4b`。`task-2.7-live-core-r5-replay.log` 的显式 live 测试退出 0，用独立信任根验签、完整 OCI 摘要校验、六项待安装镜像缓存为空检查、离线导入、初装、带 release 标识的清理、再安装和真实 SQL/OIDC/S3/Victoria/OpenBao 检查完成验收。现有 Victoria、OpenBao 与保护 PVC 身份保持不变。详情见 `docs/runbooks/dev-offline-install.md`。
- **2.8 非虚拟化图基线**：提交 `11e7da9`。实际 Ariadne/ontology 上游断网运行、2 万对象回归与适配测试退出 0；证据为 `task-2.8-completion-live-v2.log`，精确来源及许可证闭包见 `docs/poc/graph-reuse-lock.yaml`。虚拟化适配延期，20 万对象正式压测属于 SP-09。
- **2.9 非虚拟化复用边界**：八项上游 source/model/rule/Fixture 离线重跑全部退出 0；K8sGPT 使用已闭合 233 模块许可证的上游 v0.3.41 CLI，无 LLM/--explain。完整记录见 `docs/poc/inspection-reuse.md`、`inspection-reuse-lock.yaml` 与 ADR-0013。硬件 PoC 是明确标注的上游模型/解析器/合成 Fixture 验证，不宣称访问了实际 BMC/磁盘。

## 最终检查

`artifacts/test-reports/sp02-nonvirtual-final-check-20260929-v2.log` 记录以下真实命令，均退出 0：

- `make check-toolchain`：Go 1.27.1、Node 24.21.0、pnpm 12.7.0、Python 3.12.14、uv 0.12.17。
- `go test ./... -count=1`。
- `make check-generated`。
- `make check`，包含 Go vet、Go/Contract 与三项 SSE 测试。
- `git diff --check`。

提交范围的独立副本验证见 `artifacts/test-reports/task-2.9-final-staged-source-check-20260929.log`：排除用户原有未提交改动后，锁定工具链、离线冻结安装前端依赖、`make check-generated`、`make check` 和 `git diff --check` 全部退出 0。默认跳过的 live 测试不计入运行验收；实际运行依据仍为各任务的显式 PoC 日志。

`task-2.7-bundle-verification-final-20260929.log` 记录本轮再次验签与全量摘要验证退出 0：Bundle payload 66 文件、3,593,486,674 字节。安装/重装的实际通过依据是源绑定的显式 live 重放，而不是这次仅验签或默认测试中的 skip。

API/Worker 仍为进程骨架，进程 Ready 不代表业务 HTTP 就绪。组件候选状态、来源复用资格和生产部署资格按各自门禁区分；Task 2.9 的选定源码资格不将 candidate 偷换为 Bundle qualified。

## SP-02 退出条件核对

- core 可离线安装：2.7 显式 live 证据覆盖空缓存、有效公网 TCP 拒绝、导入和干净重装。
- 既有 Victoria 实例被复用且未重复部署：2.7 现场资源/Release/PVC 身份证据。
- DeepFlow 可复现 PoC：2.5 报告及显式 live 测试；仅声明实际覆盖的 arm64 开发环境和 L4 查询范围。
- 失败能力不伪装通过：历史失败日志保留；KubeVirt/CDI 继续延期、未验证、candidate 且不进入 core Bundle。

本记录仅覆盖用户批准的 **SP-02 非虚拟化部分**，不宣称原 SP-02 的虚拟化退出条件、生产 HA、未来业务实现或 SP-03 已通过。用户原有未提交内容保留，不混入本轮提交。
