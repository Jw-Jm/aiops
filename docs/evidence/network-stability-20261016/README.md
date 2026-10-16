# 联合网络调查稳定性复验

2026-10-16；独立开发环境 `192.168.0.63`；基线 main `6aefb14cbbe543c2731d12f88cfdec60ec49e9cf`（PR #9）。本次只收口运行数据关联调查的联合网络稳定性，不代表中文模型质量、V1.0 全面版本验收或生产发布。

## 修复与语义

1. 已提交结果读取会产生审计事件 `turn.result_read`，旧 SSE 处理把它作为全会话失效信号，造成重复读取。现在读取审计只推进游标；Step 变化刷新当前会话数据，完成/状态变化仍刷新授权会话，410 重同步、401/403 撤销缓存保持原约束。
2. DeepFlow 旧详情读取使用可变共享映射，还在后端读取中间重复读取原生快照。现在一次请求使用独立映射，原生快照从后端解析前包围至全部返回行完成后，每次授权和最终身份比较继续执行。单 namespace 的原生 API 读取从 9 次减为 6 次，没有提高 QPS、超时或放宽 UID/容器寿命、来源绑定和撤销门禁。安全诊断仅记录阶段、模板、耗时和 deadline 状态。
3. 同一 Pod 对存在双向关系时，旧收集器按关系摘要排序覆盖同一 peer 的方向；摘要随观察时间变化，因此时而读到反向空总体。现在保留各自 `(peer, direction)`，稳定排序并受同一 maxEdges/预算限制；健康与关系读取仍按唯一 peer 复用。平台 evaluator 把统计挂到相同方向的关系，受限边数时使用与收集器一致的方向顺序；R3/R4/R5 谓词未修改。原只读补证仍保留其冻结方向。

反向没有完成 HTTP 会话时，保留 `no_data / exact=false / responses=0`，不能解释成正常或健康。正向仍必须满足独立实际计数 1000 个响应、50 个 503、5% 错误率，未放宽为返回 200 或非空即可通过。

## 必要验证

- [Go](go-unit.txt)：deepflow、investigation、analytics、HTTP API、assistant 定向检查通过；[方向与预算最终补查](direction-go-unit-final.txt) 验证 maxEdges=1 时收集和关联方向一致。
- [DeepFlow race](deepflow-race.txt)：共享映射锁不阻塞独立请求；读取次数、读取中容器替换、来源撤销仍拒绝发布 Evidence；保留 deadline 原因。
- [Python 全部单元](python-unit.txt)：62 项通过；[固定收集器](collector-unit.txt) 14 项通过，含双向摘要顺序变化、边数预算、原只读方向和已提交步骤恢复。
- [SSE 定向](stream-unit.txt)、[前端单元](web-unit.txt)：22 文件/74 项通过，含连续读取审计不触发失效、Step 局部刷新、终态刷新、410、401/403；[TypeScript/Vite](web-build.txt) 通过，原 chunk 提示保留。
- [独立 HTTP 实际计数](deepflow-source-proof.json)：本次新窗口 1000 响应/50 错误/0 传输失败。后续有界健康请求只维持当前实时来源与拓扑，均在冻结故障窗之外；未用当前流量回填历史。
- [远程浏览器连续三轮](browser-repeat3.log)、[三轮回执](browser-receipts.json)、[API/Worker 原日志](browser-api-runtime.log)：真实 PKCE/OIDC、原生 UID、冻结绝对窗口、正反向总体、4 个 QueryResult/4 个 Evidence、来源抽屉、刷新恢复、解释草稿不改证据、键盘/窄屏、所有者隔离全部通过，耗时 19.6/20.9/20.7 秒。各轮 `retainedReads=7`、`capabilityReads=9`。收集不调用模型，未把解释草稿当成中文回答质量通过。

浏览器三轮在最终 maxEdges 排序对齐前执行；其 maxEdges=4、双向完整结果已通过。排序对齐有最终单元和[最终产品二进制运行](archive-runtime.log)/[回执](archive-runtime.json)验证：实际归档父证据逐项校验、固定窗口计数、零模型调用、所有者隔离与授权撤销通过；该 v1 路径提交 2 个查询/2 个 Evidence。不将旧运行冒充最终二进制运行。比较窗口的既有独立单向 oracle 使用显式 maxEdges=1，仍要求原两协议与恢复检查；本次未重跑该比较场景。

## 失败保留与限制

- 最终归档首次[运行失败](archive-before-failed.log)：操作时漏设报告输出路径，全部语义检查后写回执失败；保留数据库 `sp03_mig_999cc3f66f324139b302c3e157a7e784`。补齐运行参数后重新执行通过，没有改变实现或断言。

- [旧 UI 基线失败](baseline-failed.log)：最终身份/来源复核预算耗尽；原始远程日志 `network-stability-20261016-browser-api.log` 和数据库 `sp03_mig_8d1f854260054f57bdbe86c0ef2f8989` 保留。
- [方向修复前连续三轮](direction-before-failed.log)：1 通过、2 失败；[实际已提交反向结果](direction-before-query.json) 和[失败回执](direction-before-receipt.json) 保留。该轮已有网络 Evidence，但读的是 incoming 空总体，不再把这个失败归咎于来源不可用。数据库 `sp03_mig_d084fcc1e2be4a4ba54ab0afc0f5ca51` 保留。
- 初始化早期原生证据仍可能因来源/身份资格未就绪严格失败，日志保留；测试在真实准入路径就绪后操作，没有通过扩大时间/QPS或绕过门禁解决。
- 归档、租户和 PKI 使用原隔离运行夹具；网络对象、OIDC、统计查询和 Kubernetes 身份实际运行。VictoriaMetrics/VictoriaLogs 的本次窗口未重跑；五来源实现与上一轮实际验证见 [2026-10-15 证据](../operational-analysis-20261015/README.md)。
- 中文模型回答质量、R3/R4/R5 真实正向场景资格、容量矩阵、发行物料/升级回滚和 V1.0 全面验收仍未通过本次复验。KubeVirt/CDI 延期，Trace/PyRCA 禁用，不自动处置 Incident，不生产发布。

入库日志去除授权头和行尾空白；远程原日志、失败数据库与受保护归档保留。本次拥有的端口转发已停止，原预览网关已恢复，既有 Chrome 不受影响。
