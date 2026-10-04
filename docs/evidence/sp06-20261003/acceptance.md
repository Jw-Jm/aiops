# SP-06 非虚拟化授权范围：开发、运行验收与最终完整独立审核 PASS

2026-10-04 首轮独立审核发现 IR-01～IR-05，结论 FAIL，见 [首轮完整报告](independent-review-r1.md)。首轮 r8 运行与源码快照已保留为历史记录。当前交付为修复后的 r10 Bundle，以下采用本轮最新门禁；最终原生门禁已退出 0，最终完整独立复审明确 PASS，五项首轮发现全部关闭，见 [最终独立审核](final-independent-review.md)。

本轮实际 Git 基线为 `1c748b5b1f0bf33fb54774c2068a735abe2be599`，分支为 `sp06/nonvirtual-investigation`。初始工作树无用户修改；保留其他工作树、共享 OrbStack 服务和既有数据。根目录 00～10 正式文档只读。阶段授权与裁决见 ADR-0026、ADR-0027；ADR-0008 与 ADR-0020 继续有效。

授权范围开发、必要运行验收与最终完整独立审核已通过。实现已提交为 `422d9717404cf8b75b9df948a7797777bcd4d0ea`，fast-forward 合入 main 并非强制推送；实际远端 main 查询与实现提交一致。见 [Git 交付证据](delivery-verification.json)。随后仅提交这些交付回执，最终远端 HEAD 在该证据提交推送后再次验证，并在交付报告列出；受审运行/完整源码不变。

## Task 与正式 Contract

[Task 账本](task-ledger.json)记录正式章节、实现入口、具体测试入口及红绿证据。本轮公共 Contract 使用 InvestigationJob/v2、InvestigationStep/v2、InvocationContext/v2，以及 InvestigationResult/v1、Budget/v1、Usage/v1、Scope/v1、PlatformCapabilities/v1；保留旧版本并更新 Go/TypeScript 生成物及消费者。ActionPlan 继续使用正式 v2，只允许 suggested 建议，禁止执行句柄。

- Task 6.1：持久 Job、Dispatcher、Claim/Heartbeat、Step 成功唯一提交、有效 Lease fencing、Cancel/Fail/Expire、事务故障及恢复通过。默认 600 秒、40 次工具调用，Profile/Policy 只能下调。真实 SIGKILL 后接管 generation=3，已成功工具仍只有一次，未知调用保守结算，恢复诚实返回 partial。
- Task 6.2：真实 OpenBao Transit ES256 签发、JOSE 编码、audience/issuer/kid、工作负载身份、Job/tenant/Incident/scope、版本与 leaseGeneration 绑定通过。VerifyContext 不消费 nonce；BeginCall 在同一事务内落实 nonce/Step/预算，唯一约束最终防重放。密钥未导出，Context 未进入模型参数。
- Task 6.3：正式 Go MCP SDK v1.1.0 与 Python SDK 1.28.1，协议 2025-06-18，真实 Streamable HTTP 初始化、工具发现和调用通过。15 个非虚拟化语义工具具有当前授权、OPA、Schema/查询范围、原子预留、事务外执行、脱敏截断、Ledger/Audit 结算；未知、写工具、跨租户、范围扩大及撤权拒绝。VM 工具明确 disabled/unverified。
- Task 6.4：锁定 HolmesGPT 0.42.0、提交 `bfd33247f154484bc2f734a0da709da133c6a434` 的官方 Provider、调查循环、ToolExecutor、工具扩展点复用通过；未复制核心循环或 LLM client。真实 Ollama `llama3.1:8b-16k` 经 API/Worker/常驻 investigator/MCP 闭环验证。429、超时、不可用、非法 JSON、写工具企图分别通过实际 Provider HTTP 边界故障测试；这些故障 Fixture 不计作真实模型证明。
- Task 6.5：policy、trusted context、untrusted evidence、tool results、output schema 分离，脱敏、工具/模型预算原子预留及未知用量上限记账通过。模型输出 Evidence ID 的 Schema 候选只来自已提交成功语义工具 Ledger，注入文本与模型结果不能增加候选；最终 Go Validator 仍独立拒绝伪造引用、越权目标和执行句柄，并检查当前来源权限、保留闭包、Recipe/候选。模型不直接产生 confirmed 或修改确定性 RCA。
- Task 6.6：真实部署、统一身份、API、单调 eventId、fetch Bearer 客户端、持久 Ledger 重建、Last-Event-ID 续传、重复/缺口、慢客户端、撤权、取消及重启通过。原生模型网络撤回时调查诚实失败，Graph/Finding/Incident/归档 Evidence/RCA 主链路保持可用。

## 最终受测内容与实际门禁

[最终门禁清单](final-gates.json)含实际命令、退出码和原始证据 SHA256，明确区分运行通过与待审核状态。此前失败日志保留，只有该清单选中的最终门禁计作本轮证明；历史 SP-01～SP-05 报告不计作本轮运行证据。

实际受测内容绑定于 [r10 源码清单](tested-native-source-binding-r10.json)：基线提交加 4529 个运行/Contract/部署/物料相关文件哈希，明确 `uncommittedDevelopmentBuild=true`。[完整交付源码快照](final-source-snapshot.json)另绑定测试与文档等 5276 个文件；证据/审核记录排除以避免循环绑定。交付提交必须与受测运行内容一致，不能把后续提交冒充镜像构建基线。

原生签名 Bundle `sp06-nonvirtual-20261004-r10`，payload digest `sha256:863b2381784b189c2bfbd94db26b6414ed96efeb416bd7e465d0f69ef0c7ee96`；78 个文件、6315198359 字节完成签名及逐文件校验。原生镜像为 API `8f86e3684298ca8ea72701cc56f781706af85d7ddcb4a3799eccda340d52d09e`、Worker `ee316c8f9ccebbbd8904508781429a62c9c3048c02442b3f73678798975fd5b7`、investigator `41916431835e1319e25dad000c54454f5565f9347bdf9d175f313d3837591af6`。

`check-toolchain`、`check-generated`、`check-runtime-source`、`make check`、`test-security`、实际 `test-replay`、本轮集成/E2E、签名 Bundle、实际受影响离线路径均退出 0。SP-06 集成 18 个顶层测试、含子测试共 26 条 pass，零 skip，安全 11 个零 skip，复用 replay 两个门禁零 skip，其中 8 个巡检上游入口成功；受影响 SP-04 实际运行测试两个零 skip、ListWatch E2E 一个零 skip。Python 边界测试 6 个通过。细节见原始清单和报告。

当前 r10 [原生全链路](native-chart-offline-r13.log)证明真实 API→Job→Dispatcher→常驻 investigator→Ollama→标准 MCP→原生 Incident/归档 Evidence→fenced Ledger/Audit→Go Validator→SSE；重装前后各完成新的调查，均 1 次成功工具调用、2 次成功模型调用、11 个持久事件。实际 CNI 允许 API/模型，拒绝数据库/公网直连，具有独立正向控制。离线重装只对归属确定的本轮选定镜像、release 和 namespace 操作，保留共享依赖。

原生 10 个调查同时 running，最终 9 个模型失败、1 个成功；每个实际调用模型，所有预算预留结清并在预算内，Ledger/Audit 一致。期间 Graph、Finding、Incident、归档 Evidence、确定性 RCA 均持续 HTTP 200。这是并发正确性与降级保护证明，不是模型准确率、容量或性能通过。网络撤回后的失败及恢复后的新调查均真实执行。

## 独立审核发现与修复

[首轮审核](independent-review-r1.md)的五项问题均补充失败复现并修复，[红色证据](independent-review-boundaries-red-r1.log)退出 1，[最新绿色证据](independent-review-boundaries-green-r6.log)退出 0。独立审核者已重新检查完整交付，明确 PASS，IR-01～IR-05 全部关闭。

- IR-01：可信 Context 的 D0/D1 收窄传播至语义 API、原子 admission、异步提交、工具恢复、Step 和模型缓存。迁移 00035 将数据等级固化于 admission；真实归档 D1 的四条泄漏路径拒绝，并保留真实 D0 MCP/缓存正向成功证明。
- IR-02：SSE 在每条事件写入前重新检查 JWT 到期和当前角色/来源授权，写入期限不得超过 token 或连接期限；批内三种撤权/到期复现均不再发送第二条事件。
- IR-03：恢复与缓存读输出原子计入 ResultBytes，工具次数不重复计费；20 个并发缓存读遵守下调后的字节上限，耗尽与 partial/Lease fencing 在同一事务内生效。首次新鲜工具响应采用完成事务返回，避免重复记账。
- IR-04：预算 HTTP 429；创建和取消请求冲突为 409 IDEMPOTENCY_CONFLICT。取消键同事务绑定主体/Job/操作，重复请求仍检查当前授权；nonce 重放保留 REPLAY_REJECTED。
- IR-05：正式复用锁、Catalog、分发准入与签名 Bundle 的精确 investigator 镜像和对应源码一致，并加入合同回归防止再次漂移。

修复后 `make check`、安全、SP-06 集成、真实 SIGKILL/接管、五种 Provider 故障、受影响 SP-04 实际运行和新签名 Bundle 均退出 0。r11 回合中断没有退出码、不计通过；r12 因环境重启后的隔离数据库停止而退出 1、不计通过。已按确定的 UID/标签记录并清理本轮遗留资源，恢复本轮依赖。[r13 原生验收](native-chart-offline-r13.log)退出 0，验证了修复后的签名 r10 Bundle；重装前后均为新的真实调查，1 次工具、2 次模型、11 个持久事件，10 个并发调查及主链路正确性通过，模型撤回诚实失败并恢复。

## 主要修复与失败证据

保留了 informer 重复发布事实、Graph 持续 generation 变化造成读请求 409、初始健康快照未进入巡检、RCA Fixture 原生 Lease 过期、模型引用未提交 Evidence ID 的失败与修复证据。健康/Bookmark/未变化重同步只更新观测；当前 Graph 响应在授权/Lease 检查后重新取得完整一致快照并在本地锁内序列化，不把远程调用放入事务或 Graph 锁。初始事实在 Watch 健康后仅巡检一次，不重新发布事实。

还修复了不完整 Go 包形式的嵌入验收源码、manifest YAML 结构、验收 Bearer 真实重新登录、CNI 撤回的连接复用与重装后误复用旧 Job。未删除断言、放宽业务阈值或延长 token 有效期。测试环境重启清除 `/tmp` 后按精确身份保留旧容器/数据，并恢复独立私有配置、原始源码/工具、重新探测 Profile 和重新签名隔离 Bundle。

## 复用、限制与明确未通过项

197 个 Holmes Python 文件与原始 publisher sdist 字节一致；其中 196 个与锁定 Git tag 一致，唯一 release version stamp 是官方发布流程的 `__version__=0.42.0`，也与 sdist 一致。见 [当前复用核对](upstream-installed-source-r2.json)。175 个 Python 运行包、87 个 Debian 二进制包、CPython/pip、767 个 Cargo 声明源码超集及 8 个 C 来源都有精确来源与原始许可证。源码超集不等于所有包实际链接。GPL wrapper 的源码、许可证、SBOM、三个明确平台重建 wheel 的复现及分发裁决已保留；PyRCA 从运行闭包排除。

本轮没有 RCA 准确率门禁或生产容量结论。工具状态矩阵具有明确 protocol/source-state Fixture，另有实际语义 API/Graph/归档/RCA 路径；它不冒充未配置外部 DeepFlow、硬件环境的现场运行。既有原生正向 Metrics-server 能力仍 unverified。慢客户端使用真实标准库 HTTP/net.Pipe 做确定性背压，不计作容量测试。

性能项为用户豁免，不计 PASS，不开展专门基准、持续压测、容量验收或追加 P95。本轮一次漏带豁免变量的已有 P95 入口已在产生结果前终止，失败/中断证据保留于 `graph-health-republication-green-r1.log`；所有最终 Go 入口均设置豁免变量。虚拟化继续延期 disabled/unverified，不计 PASS。PyRCA disabled，不以评估任务结束解释为启用条件通过。SP-07～SP-09 未实施。
