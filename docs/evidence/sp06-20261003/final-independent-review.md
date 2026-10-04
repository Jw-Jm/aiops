# SP-06 最终完整独立只读审核：PASS

审核者 `/root/sp06_independent_review` 未参与实现。以下为该审核者返回的最终完整报告，主代理仅保存；审核期间未修改文件、写数据库或集群、调用模型、提交或推送。审核覆盖全部 SP-06 非虚拟化授权交付、Task 6.1～6.6 和根目录 10 第 14.6 节，包括正式 Contract、完整运行入口、部署、共用路径、物料和验收证据，没有仅检查最后修复 diff。

## 受审版本与独立绑定核对

实际 Git HEAD 为 `1c748b5b1f0bf33fb54774c2068a735abe2be599`，分支 `sp06/nonvirtual-investigation`，工作树 `/Users/mssc/Documents/Code/ops/platform`。其他既有工作树保留。审核时交付尚未提交；镜像和证据明确为基线提交加工作树内容构建，没有冒充后续提交构建。

独立计算并核对：

- [运行源码绑定](tested-native-source-binding-r10.json)：4529 个文件，零哈希偏差。
- [完整源码快照](final-source-snapshot.json)：5276 个文件，零哈希偏差，快照 SHA256 `f4d34034e5d8e575ae9b7f338aab980934a74d31ea83b9231a8a86748e7a9e97`。
- [最终门禁](final-gates.json)：25 项选定原始证据 SHA256 和实际 `.exit` 一致，全部退出 0。
- 当前对应源码归档实际 SHA256 `2c302b34780f884db3dfdddae5d8d8e355695a7d00b621a21c1a773c7d7d8114`，与复用锁、Catalog 和分发准入一致。

受审签名 Bundle 为 `sp06-nonvirtual-20261004-r10`，payload digest `sha256:863b2381784b189c2bfbd94db26b6414ed96efeb416bd7e465d0f69ef0c7ee96`。验签报告记录 78 个 payload 文件、6315198359 字节逐文件验证通过。

## 全部 Task 的审核结果

- **Task 6.1：PASS。** 核对 Job 的可信主体、租户、Incident、trigger、policy 和 scope 绑定；Claim/Heartbeat、成功 Step 唯一提交、租约接管、取消/到期/完成竞态及 fencing。工具、模型和 investigator 调用位于数据库事务外。默认 600 秒、40 次工具调用，Profile/Policy 只能下调。真实 SIGKILL 恢复证明接管 generation=3、已提交成功工具未重复执行、未知调用保守结算并诚实终止为 partial。
- **Task 6.2：PASS。** 核对真实 OpenBao Transit ES256 签发及轮换、issuer/audience/kid、版本、工作负载身份、Job/Incident/tenant/scope/tools/data classes、nonce 和 leaseGeneration。VerifyContext 只验证；BeginCall 同事务落实防重放、Step 和预算，数据库唯一约束承担最终边界。收窄后的数据等级贯穿准入、异步结算及缓存读取。
- **Task 6.3：PASS。** 使用锁定官方 Go/Python MCP SDK 和 `2025-06-18` Streamable HTTP 协议。核对标准握手、工具发现、JSON-RPC 调用，以及验签/Job/Lease、当前授权/OPA、Schema/范围、原子预算预留、事务外执行、脱敏截断、fenced Ledger/Audit 的顺序。15 个获准非虚拟化工具具有状态矩阵；另有实际语义 API、Graph、归档和 RCA 调用证明。未知工具、写请求、跨租户、范围扩大和当前撤权均受限制。
- **Task 6.4：PASS。** HolmesGPT 0.42.0 锁定提交 `bfd33247f154484bc2f734a0da709da133c6a434`。复用官方 DefaultLLM、ToolCallingLLM、ToolExecutor 和工具扩展点，没有复制核心调查循环或 LLM client。重新比较当前已安装的 197 个 Holmes Python 文件，全部与官方 publisher sdist 字节一致。Model Contract 仅含五个正式字段。实际 Ollama `llama3.1:8b-16k` 完成模型与 MCP 工具闭环。
- **Task 6.5：PASS。** 核对 policy、trusted context、untrusted evidence、tool results 和 output schema 分离；脱敏、注入防护、调用前原子预留、未知用量按上限记账及预算耗尽终止。模型输出 Schema 的 Evidence 候选只取认证 Ledger 中成功语义工具的顶层引用；Go Validator 独立检查引用、当前来源权限、tenant/scope、版本、保留依赖、Recipe、候选及建议目标。模型排名或建议不能产生 confirmed，ActionPlan 不能产生执行句柄。
- **Task 6.6：PASS。** 核对真实 API、Worker、常驻 investigator、身份和 Chart 启动链路；SSE 从持久事件重建，支持单调 eventId、fetch Bearer、Last-Event-ID、重复/缺口处理、终态多批次、慢客户端和重启恢复。每条事件发送前重新检查到期及当前授权。真实模型网络撤回时调查诚实失败，主业务链路继续运行。

第 14.6 节正确性、安全、恢复、真实模型/MCP、完整集成链路、并发与离线要求均具有本轮源码和实际证据支撑。

## 首轮发现的独立关闭结论

[首轮报告](independent-review-r1.md)对原 r8 的 FAIL 结论继续保留。以下关闭结论仅适用于当前冻结 r10：

- **IR-01，P1，已关闭。** `internal/investigation/read_limits.go:14`、`migrations/00035_sp06_admission_data_classes.sql:3` 和 `internal/investigation/recovery.go:37` 将 Context 收窄落实到语义调用、结算、Step 和模型缓存。真实归档 D1 的泄漏路径拒绝，D0 正向 MCP/恢复仍成功。
- **IR-02，P1，已关闭。** `internal/httpapi/investigation_handlers.go:263` 逐事件检查消除批内授权失效后继续披露问题；JWT 到期、角色撤权、来源撤权三种复现均在第一条后停止。写期限同时受 token 和连接期限约束。
- **IR-03，P2，已关闭。** `internal/investigation/read_limits.go:40` 和 `internal/investigation/allocation.go:96` 在 Job 锁及同事务中计入恢复/模型缓存 ResultBytes、事件和 Audit。20 个并发恢复读遵守字节上限，不重复增加工具或模型调用次数；耗尽同事务产生 partial 和 fencing。
- **IR-04，P2，已关闭。** `internal/httpapi/investigation_handlers.go:57` 符合预算 429、幂等冲突 409。`internal/investigation/lifecycle.go:45` 取消幂等绑定租户、主体、操作和 Job；相同请求可重放，跨 Job 复用拒绝，撤权后重放拒绝。
- **IR-05，P2，必要物料一致性缺口，已关闭。** `docs/poc/holmes-investigator-reuse-lock.yaml:8` 与当前 Catalog、准入记录的镜像和对应源码一致，`test/contract/sp06_reuse_lock_test.go:10` 防止再次漂移。

[失败复现](independent-review-boundaries-red-r1.log)退出 1，[修复回归](independent-review-boundaries-green-r6.log)退出 0。审核重新跟踪完整调用路径及断言，没有仅接受修复说明。未发现新增已确认的范围内缺陷或必要证据缺口。

## 实际验收与物料核对

`make check-toolchain`、`make check-generated`、`make check-runtime-source`、`make check`、`make test-security`、实际上游 replay、本轮 SP06 集成、SIGKILL 恢复、受影响 SP04 实际回归及 ListWatch E2E、离线构建和签名 Bundle 门禁均退出 0。完整命令和原始证据见最终门禁清单。

SP06 aggregate 为 **18 个顶层测试、含子测试 26 条 pass、零 skip**。五种 Provider 故障分别具有实际 HTTP 边界测试：429、timeout、unavailable、illegal JSON 和写工具企图；故障 Fixture 未计作真实 Ollama 证明。

[原生 r13](native-chart-offline-r13.log)证明签名 Chart/API/两个 Worker/investigator→真实 Ollama→标准 MCP→现有 Incident/归档 Evidence→Ledger/Audit→Go Validator→持久 SSE。离线重装前后分别完成新的 succeeded Job，各有 1 次工具、2 次模型调用、11 个持久事件。实际 CNI 有正向控制，允许 API/模型，拒绝 investigator 直连数据库及公网。

10 个原生调查峰值同时 running，最终 9 failed、1 succeeded；预算结清、Ledger/Audit 一致，Graph/Finding/Incident/归档 Evidence/确定性 RCA 持续 HTTP 200。该结果满足并发正确性和降级保护，没有被描述为准确率、容量或性能通过。

当前依赖闭包含 1040 条来源记录；独立重算 1069 个许可证文件哈希，零偏差。175 个 Python 包、87 个 Debian 包、CPython/pip、声明 Cargo 源码超集和 C 原始来源具有锁定材料、许可证及分发裁决；平台重建 wheel 与 publisher wheel 的区别明确。PyRCA 未进入该运行依赖闭包。

## 结论边界

性能基准、持续压测、容量和 P95 为用户豁免，**不计 PASS**。KubeVirt/CDI 和 VM 调查继续延期、disabled/unverified，既有源码、Fixture 和失败证据保留，**不计 PASS**。PyRCA 保持 disabled/excluded。SP-07～SP-09 未实施。

小模型 RCA 准确率、未配置外部 DeepFlow/硬件现场以及既有原生正向 Metrics-server 仍未验证。工具状态 Fixture 和 Provider 故障 Fixture 的证明范围表达准确，没有替代真实模型及原生链路验收。

本次 **最终完整独立审核 PASS** 绑定上述冻结 r10 交付。审核时提交、合入 main、非强制推送和远端一致性尚待主代理完成，本报告不宣称这些 Git 动作已经发生。后续实际交付动作另记 delivery-verification.json。
