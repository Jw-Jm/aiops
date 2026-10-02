# SP-04 非虚拟化开发验收

状态：Task4.1～4.7 非虚拟化实现与本轮必要门禁完成，最终完整独立复审 **PASS**。全部确认缺陷与证据缺口关闭。

受测源码：`52d490cb30943a899f46361a51f388ff9f801bb0`。实际起始基线为 `4738c1148c6bec7003dd85f1831a6059dfab86e1`，初始工作树为空，没有已有用户修改。父目录 ops 不是 Git 仓库；本轮没有改写根目录00～10正式文档。用户2026-10-01的SP-04明确授权覆盖旧SP-02阶段限制；ADR-0008仍然有效。SP-05～SP-09没有提前实现。

[源码逐文件绑定](final-source-binding.json)记录2,394个已跟踪源码、Contract、部署、复用、Runbook与测试文件（排除持续追加的证据目录），集合摘要 `2e0d68b42fac4940b6005fb7252be73668a2fcfbfaddbdd0645c99f1af45ba11`。收尾提交仅追加证据，必须保持该集合完全一致。[门禁账本](final-gates.json)含真实命令、退出码、范围、原始输出摘要和样本方法；原始失败保留其失败含义，历史验收报告不计本轮证明。

## Task → 规格 → 实现 → 测试 → 本轮证据

- **4.1 已完成**。04§5～6、08§4～5、10Task4.1/§14.2 → `internal/resource/{canonical_id,entity,alias,resolver}.go`、`internal/resourcestore`、迁移00018。确定性UID/UUID身份、规则版本/provenance、别名和冲突记录；name和重复serial不构成静默合并。PhysicalServer与Node为不同ID，通过hosts关联；跨租户/集群、删除重建、大小写、前导零与未解析覆盖。`TestDeterministicIdentityGolden10000`及真实PG身份冲突/tombstone回归 → [黄金集](final-identity-golden.log)、[集成](final-integration.jsonl)。10,000来源身份+10,000重建UID、10租户/100集群作用域，确定性召回100%，错误合并0；这是规则黄金集，不是现实全量身份质量统计。
- **4.2 已完成**。10Task4.2/§14.3、08§49.3 → `internal/integrations/kubernetes/{informer,projector}.go`、`internal/app/sp04_collector.go`。17个core非虚拟化GVR；List/Watch、投影、UID/RV幂等事件、terminal tombstone、410重列、403、429、静默Watch与projection lag显式退化。每集群共享预算，两个Worker各QPS≤10/burst≤25，合计≤20/50，Lease不另开无限请求通道。Fake故障/rate公平race在make check及专项红绿；真实OrbStack17GVR/30Pod → [原生采集E2E](final-collector-e2e.jsonl)、[真实运行链](final-runtime-performance.jsonl)。恢复期旧图不会被标为fresh。
- **4.3 已完成**。10Task4.3/§14.3、07§30、08§49 → `internal/graph`、`api/internal/graph-v1.yaml`、Worker启动和路由。锁定Ariadne/ontology为唯一Graph内核，薄reader/诊断投影；API通过mTLS+签名有效用户scope访问现有Active Worker，不在API重复构图，不新增Graph服务。Kubernetes Lease唯一选主，PG仅路由/观测镜像；ownerEpoch、generation原子切换、LeaseUID/process identity fencing、响应/缓存/cursor重校验、scope过滤前置于计数/分页、观测时间TTL边界。Active停止、Standby未同步/接管、Pod内重启、失Lease/分区、Watch静默/410、并发重建和ObservationOnly覆盖。固定纳秒输入使用上游MicroTime decoder避免容器时间合同被宿主机时钟掩盖。→ [20k黄金集](final-20k-golden.log)、[上游replay](final-upstream-replay.log)、[原生签名Chart](final-native-chart-offline-reinstall.jsonl)、[实际双Worker故障与版本验证](final-runtime-performance.jsonl)。
- **4.4 已完成**。10Task4.4/§14.2、08§50 → `internal/evidence`、VictoriaMetrics/VictoriaLogs Adapter和范围验证库。固定版本模板、显式参数、服务端不可覆盖mapping、时间/行/字节/并发预算、脱敏、统一evidence/partial/degradedSources/warnings/queryHash。actual Adapter源端正/负验证后才产生能力证据；空scope、缺tenant、OR/regex/discovery/top-k及缓存/归档/撤权均拒绝或明确退化；ADR范围声明没有替代查询授权。→ [真实Victoria隔离与撤权](final-integration.jsonl)、[统一API及预算/观测/源故障](final-runtime-performance.jsonl)、security11项。
- **4.5 已完成冻结Fixture准入范围**。10Task4.5、ADR-0006、DeepFlow复用锁 → `internal/integrations/deepflow`。锁定v7.2 Server/Querier六个固定网络语义操作、绑定参数/预算和Canonical映射；业务层没有ClickHouse DSN、直连或通用SQL。冻结字段、缺额外字段/超时/空/unknown、操作谓词及作用域的合同覆盖；真实API→Worker→Adapter运行接线 → `TestDeepFlowFrozenContractMissingExtraTimeoutEmptyAndUnknown`、`TestSP04FixtureAdaptersThroughActualRuntime`，[运行证据](final-runtime-performance.jsonl)。[本轮只读原生/Docker环境清单](final-deepflow-environment-inventory.log)未发现可用注册live Server；能力明确fixture_only，L7不可用、Trace固定CAPABILITY_DISABLED，没有把Fixture当live Contract。
- **4.6 已完成规定Fixture/选定源码范围**。10Task4.6、inspection-reuse-lock → `internal/integrations/redfish`、`internal/inspection`、`internal/upstream/{corootcheck,metal3}`、`internal/app/sp04_hardware.go`。Gofish只读Redfish身份/健康/inventory，三组厂商、UUID缺失/重复UUID或serial、超时/鉴权/部分inventory及unknown/degraded；按锁消费获准Coroot Check/Audit、NPD规则和Metal3硬件模型。IPMI/SMART走Exporter→Victoria，核心没有任意本机命令执行。unsupported mapping在BMC I/O之前拒绝。→ `TestRedfishThreeVendors`及scope/失败测试、[API/Worker Fixture运行链](final-runtime-performance.jsonl)、[8项上游复用细节](final-upstream-replay-details/summary.json)。不宣称真实厂商硬件或整个上游组件运行/分发准入。
- **4.7 已完成**。10Task4.7/§14.4、05§25.2/25.5、08§51 → `internal/httpapi/resource_handlers.go`、`internal/app/sp04_runtime.go`、`internal/evidence/{archive,repository,retention}.go`、迁移00018～00023、生成Go/TS/DB消费者。Resource/Neighbors/Impact/DiagnosticGraph/Evidence/LegalHold接入实际API和Worker；统一鉴权、cursor/freshness/partial/degradedSources与版本Contract。PG仅元数据/有限pending材料，不存全量Metrics/Logs/Flow。pending intent→Transit→幂等上传→cipher/plain摘要与对象版本核验→元数据提交；ObjectRef含逻辑后端/version/digest和key version；pending不等于verified。四个IAM职责分别上传/读取/保护/清理；采集阶段归档短寿命事实/必要关系；上传/事务两侧崩溃、孤儿、损坏、源过期/撤权、恢复、Hold并发、跨租户以及Incident/archive/ref closure、365天Action/Audit依赖受保护。→ [实际PG/Transit/TLS四角色S3故障集成](final-integration.jsonl)、[API/Worker源撤权/故障](final-runtime-performance.jsonl)、[原生离线重装旧证据回放](final-native-chart-offline-reinstall.jsonl)。

## 实际门禁与范围

全部执行记录、精确测试选择和退出码见[final-gates.json](final-gates.json)。

- `check-toolchain`、`check-generated`、`check-runtime-source`、`make check`：退出0。工具版本与锁定值一致；完整Go包/vet/web/Contract入口通过。make check包含未变化包的真实cache；其默认opt-in skip没有计作live通过。
- `make test-security`：11项、0skip，退出0。[输出](final-security.log)。
- 上游原始源码replay：2项、0skip，393.33秒；8项inspection源码/模型/Fixture各退出0；原始pin/patch/notices/许可证闭包保持，[输出](final-upstream-replay.log)、[细节](final-upstream-replay-details/summary.json)。工具image缺缓存的第一次失败保留；重新准备精确Go/Python后运行，tool holder仅防止GC，无网络/listener，最后按本轮ID/label/image清理。
- SP-04真实集成：15 case/subcase、0skip、44.276秒，包含native Lease恢复和实际IAM/PG/Transit/Victoria。[输出](final-integration.jsonl)。
- SP-01～SP-03选择性共用路径回归：17顶层、19 case/subcase、0skip、89.298秒；完整选择见账本，涉及迁移、RLS/Audit/idempotency/config/source/tenant/RBAC/step-up、runtime角色及S3 v1/OIDC兼容。[输出](final-shared-regression.jsonl)。另真实Audit Worker投射身份/中断/SIGKILL恢复23.64秒，0skip，[输出](final-shared-audit-worker.jsonl)。不宣称所有SP-01～SP-03历史PoC都重跑。
- 真实API/双Worker、OIDC/mTLS/Lease/PG/Adapter/归档故障链：Native80.49秒、Fixture36.60秒，2项0skip，[输出](final-runtime-performance.jsonl)。没有LLM主链路依赖。
- 本轮原生签名Chart离线回归：167.45秒、0skip；两个受影响镜像与其本轮build tag实际空cache→OrbStack Import，Never，实际CNI公网正/负控制与私有依赖正控制，Pod-bound投射OpenBao登录，真实Evidence archived_verified，owned release卸载/导入/重装后旧Evidence GET成功，元数据50→83。[输出](final-native-chart-offline-reinstall.jsonl)。七个已有external依赖/共享数据保留，未重新部署共享ops-system；这项门禁只声称本轮受影响API/Worker/Chart的离线导入、原生运行和干净release重装。

## 性能与黄金集的实际方法

所有延迟组分别由测试对墙钟样本排序取P95，门槛保持Graph≤1s、change→query≤60s、semantic Evidence≤5s。最终测量在本轮准备/20k构建/replay结束后执行；此前并发重任务时小Graph P951.060257041s是失败，[完整输出保留](final-runtime-performance-concurrent-failed.jsonl)，不计通过。

- 真实公开API小Graph：30样本，P95 **802.360333ms**。
- 真实公开API深度2、200节点、199边（一个native Service和199个受SchedulingGate保护的Pod）：30样本，P95 **692.801625ms**。
- native来源label变更→公开API/ActiveWorker可查询：30样本，P95 **791.78525ms**。
- semantic Evidence API→Victoria/Transit/四角色S3/PG：30样本，P95 **127.894291ms**。
- 独立直接采集E2E：17GVR/30Pod，30收敛样本P95225.018833ms；300个实际较小子图P95651.417µs，不混为公开API链时延。
- 冻结20,000输入→19,998投影节点+2项外部Fixture单独叠加：初始构建 **12m1.321115708s**；100次深度2/maxNodes200的较小冻结诊断子图P95184.542µs。关键关系9期望/9TP/0FP/0FN，Precision/Recall100%。9边样本的Wilson95%下界约70.085%，不构成统计意义生产可靠性证据。Synthetic owner仅用于离线conformance，不能证明12分钟内真实Lease连续选主。

这是本地合约、故障和明确规模的验收。20k冷构建耗时、较小native来源和少量关系黄金样本是明确限制；没有将其改写为生产规模初始化/60s收敛/容量验收。

## Bundle、交付与复审

[最终Bundle源绑定](final-bundle-source-binding.json)、[构建校验](final-signed-bundle-build.log)、[独立CLI校验](final-bundle-independent-verify.log)绑定上述受测源码。23材料/69认证文件，解压字节3,694,128,118；runtime原始源码闭包 `sha256:243c5151a0f8678782f902ccbdd0bcdb673c0d2d38892e46be01e6fea50d3ab6`。95模块/2,943module文件/1,377标准库文件/16选定上游文件，准入只覆盖获准消费范围。

实际签名Bundle保存于忽略目录 `platform/artifacts/bundles/sp04-nonvirtual-52d490c/`（从测试tmp同卷搬移，未改变任何字节）；payload `sha256:d974c453f2e54f1513221ccef419484c666fe7810f6740169e769291941a0044`。外部可信公钥位于 `platform/artifacts/profiles/sp04-52d490c-trust.pem`，指纹 `sha256:c91e8ee7fea7a80b6b3e503d429d26f3704d71d6d96440455e8a10bce353a9ad`；Bundle内部不携带自身信任根，私钥未入仓/Bundle。

独立只读审核者 `/root/sp04_independent_review` 从未参与实现。[审核—修复记录](independent-review.md)含R01～R18：16项真实缺陷与2项证据缺口，保留各轮失败、具体触发/要求/红绿及独立复审；所有代码修复已分别接受。审核者已对最终完整交付重新审核并明确给出 **PASS**，亲自执行最终签名 Bundle CLI 验证退出0；R01～R18 全部关闭。[实际最终独立结论](final-independent-review.md)。

KubeVirt/CDI与VM/VMI/DataVolume专属采集、关系、工具与界面继续延期，disabled/unverified、未计作通过。DeepFlow fixture_only、L7不可用/Trace disabled、硬件三厂商Fixture和选定规则/模型均按实际范围声明，没有恢复延期组件或扩大上游整体准入。

用户收尾指令（2026-10-02）：“性能不用测”。收到后不再运行性能测试；已完成的原始测量与失败记录保留，不要求追加测量。
