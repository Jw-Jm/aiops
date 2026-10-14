# 故障链调查增强：开发同步检查点

日期：2026-10-14。状态：开发实现已同步，专项验收尚未全部完成。依据 [ADR-0041](../../adr/0041-fault-chain-investigation-enhancement.md) 和 [开发方案](../../plans/fault-chain-investigation-enhancement.md)。本记录不能作为生产发行准入或全部 P0～P7 完成证明。

已实现固定 Querier L4/L7 聚合、analytics 合同与分母事实、冻结窗口比较、签名 evidence_only 收集、已提交 Step 的有限评估、持久化上下文与当前授权重校验，以及助手中的调查入口、窗口时间线、链路选择、支持/冲突/缺口和统计事实展示。空流量不构造反证，错误率差值以百分点事实单位表示，相关性不写成确认根因。来源能力按数据集、操作、覆盖窗口与当前来源绑定开放。

## 已有验证

- Go 内部单元测试通过；针对最新修改的 DeepFlow、analytics、investigation、HTTP API、app 复测通过。
- `make check-generated`、`make test-contract`、相关 `go vet` 通过。
- 前端构建通过；19 个测试文件、63 项测试通过。Python 固定流程 4 项回归通过。
- [http-counts.json](http-counts.json)：192.168.0.63 独立 canary 发出 1000 个完成响应、50 个 503，单个 s-p 采集点的固定 HTTP 聚合与独立计数一致，超过旧明细 limit。记录只含 D1 元数据。
- [runtime.json](runtime.json)：远程 r7 首次完整固定流程通过，取得 3 个 QueryResult、3 份 Evidence 并读回持久化上下文。模型端点不可用、密钥缺失且模型调用为零。租户/角色/PKI 初始化及 TLS/IAM 归档依赖使用明确标注的辅助 Fixture。这是首次通过时的历史快照，后续代码的最终稳定验收仍待完成。

## 尚未完成的验收与能力边界

2026-10-14 后续 r15 已通过强化固定流程和真实 OIDC/浏览器验收，见 [runtime-qualified.json](runtime-qualified.json) 与 [browser-qualified.json](browser-qualified.json)：1000/50/5% 的真实统计与持久化读数一致，刷新恢复、跨角色隔离和授权撤销后读取拒绝通过。后续合并快照与完整三窗口比较的最终流水线仍需验收。失败运行和受保护数据库保留在远程任务目录，不改写为成功。

最新代码将双端身份置于同一实时验证快照，查询后再做完整双端刷新；去掉重复的第三次 native 列表读取，保持来源、UID/容器生命周期及预算检查。临时失败最多重试一次，每次重新经过既有 MCP 预算准入。一次尝试将测试 QPS 配成 30，被已有双 Worker 的 QPS≤10/burst≤25 准入拒绝，已恢复 QPS=10、burst=20；没有放宽准入。

[windows.json](windows.json) 的独立受控三窗口证明已通过：HTTP 各 1000 个完成响应、5xx 为 10/50/0，平均耗时约 0.063/20.079/0.063 ms；L4 s 采集点记录数 1000/1100/1000，status=3/4 错误观测为 0/100/0，重传计数为 0/0/0。HTTP 的合格点为 s-p，不能套用到 L4。原有仅 status=3/4 的计数是客户端/服务端错误观测，含传输重置、超时等，不是独立失败连接数或失败请求数。周期重传计数的定义来自锁定源码，尚未完成非零重传真实刺激验证。compare/trend/恢复、SIGKILL/取消和并发与上限容量矩阵尚未完成全部流水线验收。采样、去重、完整覆盖与源时间未经验证时保持具体能力缺口；不提供未经证明的 P95、重传率、丢包率或传播先后。共享资源与变更线索在缺少合格来源时只保留待补证据，不输出原因候选。

源码闭包检查的 Go 部分已通过；既有迁移来源证明对 41/42 的漂移仍导致完整检查失败，本轮没有改写旧准入摘要。模型中文回答质量与生产来源、镜像/离线物料发行准入分开验收。

## 运行与回滚

仅使用 192.168.0.63 的任务目录 `/opt/ops-assistant-dev-20261012/fault-chain-20261014` 和独立 namespace `ops-assistant-deepflow-20261013`。新 canary release 为 `fault-chain-20261014`，既有生产、core/final 和旧 canary 配置不变。

入口由 `AssistantRuntimeConfig.faultChainEnabled` 控制，默认关闭。启用前配置与实际 sourceRevision、bindingDigest、proofDigest、collectorVersion、coverage、observationPoint 匹配的 `SP04Source.analysisQualifications`；仅有静态目录成员不代表来源操作合格。窗口最长 1 小时，最多 8 边，原始查询预算沿用现有账本；比较收费 2 次，trend 按实际子查询数收费，余额不足显式保留缺口。

冻结故障、基线、恢复窗口；基线/恢复须等长且不重叠。当前通信关系读取使用可信 requestedAt 前 120 秒，与历史窗口统计分别处理，不把当前关系标为历史已验证。按协议新增 network.http-summary 与 network.tcp-summary 合并快照资格，仍需分别配置操作、采集点与覆盖证明；它们复用同一统计总体产生不同单位的 Facts。比较/trend 的身份按整批子窗口开始前、结束后进行完整 native/backend 验证，不使用跨请求身份缓存；变化时整批拒绝。原始明细在单个查询内只解析每个重复 backend ID 一次，并保留结果最终来源/身份校验。暂停新增调查时关闭功能入口，保留兼容的历史读取组件和不可变证据；不删除历史数据、不回退迁移、不自动处置 Incident。

KubeVirt/CDI 继续延期，Trace 继续禁用，根目录 00～10 正式文档未修改。首次固定查询的 `Count(*)`/条件聚合不受该 Querier 支持，已改为固定 `Count(row)`、`Sum(response_duration)` 与有界分组；不直接访问 ClickHouse。字段依据锁定版本 [L4 源码](https://github.com/deepflowio/deepflow/blob/e567b167453ffa99f08f26def20379b4f831e073/server/ingester/flow_log/log_data/l4_flow_log.go) 与 [L7 源码](https://github.com/deepflowio/deepflow/blob/e567b167453ffa99f08f26def20379b4f831e073/server/ingester/flow_log/log_data/l7_flow_log.go)。

后续 r16/r17 未得到可用统计上下文，r18 在实时来源范围证明阶段失败；失败及受保护数据库继续保留。合并快照曾因 statistics 的 milliseconds/retransmissions 不在合同枚举中被拒绝，已补齐并增加线协议回归。make generate 现同步助手 OpenAPI 后再生成客户端，避免源合同与客户端漂移。最新单元、生成合同、前端构建/63 项测试通过；最终真实三窗口流水线仍未通过。

r19/r20 的实际 L4 直接诊断取得 fresh、非 partial Evidence，Graph 路由就绪。固定流程中底层 sourceErrorCode 曾被外层 SOURCE_DEGRADED 遮住，导致 L7→L4 降级未执行；现优先使用具体来源错误码，新增冻结窗口和独立准入回归，Python 共 5 项通过。r20 已执行独立 L4 降级，但原始明细因 evidence_byte_budget 被丢弃，未形成 Pod 通信关系，仍没有统计查询结果。32 KiB 预算保持不变；下一步需在预算内保留可用且明确标记部分覆盖的明细，不能把明细截断用于精确统计。失败日志与受保护数据库继续保留，未计作最终验收通过。

r21 已完成上述明细预算修正并通过完整三窗口固定调查，见 [runtime-comparison.json](runtime-comparison.json)。明细按完整行保留预算内前缀，标记 partial/evidence_byte_budget_exhausted；保留行的时间范围和内容摘要重新计算。独立聚合统计不使用这个前缀。真实 API、签名 MCP、Worker、Querier、持久 Fact/Evidence 取得故障与恢复的 HTTP/TCP 共 4 个比较结果：HTTP 1%→5%（+4 个百分点）、5%→0%（下降 5 个百分点），TCP 错误观测 0→100、100→0。R1 应用响应/耗时、R2 传输观测和 R5 改善观察均有引用；源时间仍为 unreliable。模型端点离线、密钥缺失、调用为零，跨所有者读取和撤销授权后的上下文/统计读取拒绝通过。辅助 Fixture 仍只用于租户/角色/PKI 初始化与 TLS/IAM 归档。

后续新增关闭额外字段的 network-summary/v1 Schema，并在归档前执行合同校验；HTTP/TCP 总体、计数单位、未经验证的时间状态及未知字段有正反回归。界面已将总体、统计项和主要缺口翻译为中文，所选关系观测时间加入时间轴。合同测试、相关 Go 单元测试及界面测试/构建通过；r21 回执早于这两项后续修改，不能视作后续代码的运行绑定证明。趋势、相对变化、生命周期、并发/容量矩阵及其他规则仍按原方案继续验收。

[runtime-comparison-schema.json](runtime-comparison-schema.json) 为 r23 的后续真实通过回执：归档前 summary 合同已启用，完整固定调查、4 个统计比较、R1 响应/耗时、R2 传输和带支持证据的 R5 改善观察均通过；空改善的 R5 仍保留无支持证据，未升级成恢复结论。时间状态仍为 unreliable。该回执不包含最新界面真实浏览器验收。相对变化 1%→5%=400%、非空总体零基线不构造百分比、三桶趋势原边界与 2/3 次实际子查询已通过新增单元回归；真实趋势、相对变化与非零重传验证仍未完成。

本轮最终本地验证：`CGO_ENABLED=0 go test ./internal/...` 全部通过（Graph 大重建测试耗时约 243 秒，正常结束）；`go test ./test/contract` 和新增 summary 正反 Fixture 通过；相关 `go vet`、`make check-generated`、`git diff --check` 通过；Python 5 项、前端 19 文件/63 项测试及构建通过。没有运行本地集群、容器或数据库；真实调查只在 192.168.0.63 执行。受控健康刺激为限时任务，现已正常结束。

后续 R4 有限谓词已接到 evaluator：仅从同一 Job 的成功 get_recent_changes Step 的 EvidenceRefs 读取元数据，再检查当前来源、披露范围和允许数据类。变更须已归档且有版本/摘要证明、同端点 Canonical UID、位于已冻结基线内、结束严格早于故障窗口；变更与对应完整网络统计的时间均须可靠，且故障统计已经产生带事实与支持引用的 R1/R2 观察。只输出关联背景并保留 change_causal_mechanism_unverified。正反 Fixture 覆盖错 UID/租户、时间重叠、窗口越界、未归档、归档证明缺失、错误类型、缺少基线、partial 和任一来源时间不可靠；相关 Go 测试与 vet 通过。当前 resource_troubleshooting 没有已发布 get_recent_changes 工具，DeepFlow 时间仍 unreliable，因此当前真实流水线继续保留变更缺口；本项尚没有真实正向来源验收，也没有新增或伪造变更来源。

共享资源 R3 仍待完成：当前 get_resource_health 只提供当前资源状态，不能用来证明历史故障窗口健康；需要可引用的同窗自身异常与共享关系，不能仅以同时异常输出共同根因。后续机制、容量、真实趋势/相对变化与重传、最新界面运行验收继续保留为未完成项。

后续 R3 谓词与健康来源绑定已实现：签名 Graph 响应增加可选 observationSources，只返回实际贡献实体/关系的配置来源版本与范围摘要，旧结果不含此字段时不能补造健康来源证明。固定流程经已准入 get_resource_health 读取已授权 Pod，以及范围中已明确可见的 Node；evaluator 只接受同 Job 成功 Step，并复用原有来源锁/版本/摘要检查。共同资源候选要求两名不同调用方的完整、时间合格、同窗 HTTP 5xx 观测，已提交的同窗共享 Node/依赖关系，以及共同资源自身同窗 Ready=False 快照。引用包含原始健康 Step、UID、观测时间、来源版本、共享关系、QueryResult/Fact/Evidence；读回时重校验父 Step、来源、实体及披露范围。只支持进一步调查，继续保留 shared_resource_causal_mechanism_unverified。界面显示状态快照和共享路径，不将同时异常写成共同根因。

R3 正反 Fixture 覆盖一个调用方、错 UID、窗口外健康、缺少关系、错误关系来源、不可靠网络时间、partial、缺少自身健康、旧 Worker 无来源元数据及原始健康快照 partial。相关 Go/合同检查、Python 6 项、界面测试和构建通过。[runtime-resource-health.json](runtime-resource-health.json) 为 r24 真实固定流程通过回执；远程持久账本另确认 get_resource_health=succeeded、1 个来源版本、1 个资源节点，原 4 个三窗口统计、模型离线与授权撤销读取拒绝继续通过。本 canary 没有 NotReady，且 DeepFlow 时间仍不可靠，因此它只验证健康读取和原有统计机制，未验证 R3 正向故障场景。多调用方的固定采集优先级、R3 真正故障刺激与来源时间证明、生命周期与容量矩阵仍待完成。

固定流程后续改为稳定的窗口/数据集/链路阶段：所有已选择链路先做故障窗 HTTP，再检查传输与恢复，资源健康在故障统计后按余额读取；不把第一条链路的全套恢复置于其他链路首要观测之前。仅选择当前操作已合格的数据集，合并快照缺席时使用合格的个别指标，避免将已有替代能力误列为缺口。恢复窗即使没有基线也比较故障窗。新增消费实际余额的 Python 回归证明：5 次原始查询预算中，1 次关系读取后，两条链路各取得 2 次子查询的故障 HTTP 比较，传输/恢复保留未完成缺口；无基线恢复仍是比较且比较窗口冻结为 fault。Python 共 8 项通过。目标自身也经准入读取健康，支持目标作为共同下游的情形；没有新增范围权限。

[runtime-priority.json](runtime-priority.json) 为 r25 的真实通过回执：使用 r24 后端和调整后的 Python 固定采集顺序，原 4 个协议/窗口比较、持久证据、授权撤销拒绝和模型离线检查通过。它仍是单对端场景，不能证明真实多调用方、4/8 边容量或 R3 正向故障。下一步继续覆盖这些场景，以及趋势/相对变化、非零重传和取消/接管/清理闭包。

[lifecycle-repository.log](lifecycle-repository.log) 为独立远程 PostgreSQL/最小权限角色与真实 Repository 子进程验收：TestAssistantEvidenceOnlyProcessKillAndTakeover 的 complete/cancel/budget 三分支均通过，提交 Step 后真实 SIGKILL、等待真实 2 秒租约到期、generation 增长接管、相同参数已提交 Step 复用且总成功次数为 1、旧 generation 回答拒绝、原冻结 FaultChainRequest 摘要及 evidence_only/零模型预算保持、模型 Step 数为零。取消分支拒绝接管者迟到回答；预算分支返回 partial、明确缺口且没有未提交数字。身份与工具内容是明确的协议 Fixture，不用于来源准确性或模型帮助性证明。首轮 Fixture 缺少单目标绑定而被现有校验拒绝，修正 Fixture 后通过；没有放宽生产校验。失败及受保护数据库继续保留。完整 Dispatcher/Python 进程链路的断点、保留已提交数字的预算耗尽与清理闭包仍待验收，不以本 Repository 子进程证明替代。

[runtime-process-recovery.json](runtime-process-recovery.json) 为 process-r3 的真实 Dispatcher/Python 进程恢复验收回执（94.72 秒）：在已提交上下文后、下一次 MCP 请求转发前真实终止两个进程，等待生产 45 秒租约自然到期，generation 从 1 增至 2；上下文成功 Step 总数仍为 1，旧 generation 的迟到回答被拒绝。保留 3 个精确统计比较，另一次查询 SOURCE_DEGRADED 后返回 partial 和明确缺口；每个保留协议/窗口均按独立三窗口计数核对，R1/R2/R5 支持只按实际已提交结果校验，缺失数据不得产生对应支持结论。模型调用为零、跨所有者及撤销授权后的读取拒绝仍通过。process-r1/r2 失败保留在远程：旧断言错误地要求降级后的恢复必须仍有全部 4 个结果或 TCP 规则；修正的是验收断言，未放宽生产预算、来源或身份校验。

本回执证明实际进程终止后的已提交上下文复用及后续真实采集；断点在网关转发前，不作为执行中来源请求 UNKNOWN_OUTCOME 的证明。取消/预算保留已提交正向数字、清理闭包、多调用方、4/8 边容量、真实趋势与非零重传、最新界面运行仍待专项验收。当前修复后检查：相关 Go 单元/合同、integration vet、生成一致性、Python 8 项、前端 19 文件/63 项及构建通过。代码按用户要求同步当前开发分支，未作生产发布或全专项完成声明。
