# DeepFlow 单链路来源与签名统计收口：2026-10-15

本次完成“恢复采集/范围资格 → 新受控窗口 → 单链路统计 → 签名 RPC、归档与授权读回”这一阶段。最终代码连续两次通过真实语义验收；不宣称多调用方、共同资源、完整恢复、模型质量、OIDC 浏览器或容量专项完成。

## 最终通过的证据

- [windows-r1.json](windows-r1.json)：新生成三个连续半开窗口，独立客户端各收到 1000 个 HTTP 响应，503 为 10/50/0；DeepFlow 单一 s-p 观测点分别统计 1000/1000/1000、10/50/0。TCP 单一 s 点记录为 1000/1100/1000、失败观测为 0/100/0，与独立拒绝连接刺激一致；重传全部为 0，本次没有证明真实非零重传。
- [http-r1.json](http-r1.json)、[http-r2.json](http-r2.json)：两次独立新流量均为 1000 响应/50 个 503，与真实固定聚合一致。第二次用于最终范围资格复验；没有沿用 10-14 或本日早期已经失效的窗口证明。
- 最终连续 PASS：[r12](fault-chain-source-signed-20261015-r12.log) 36.88 秒、[r13](fault-chain-source-signed-20261015-r13.log) 35.31 秒；结果分别为 [signed-r12.json](signed-r12.json)、[signed-r13.json](signed-r13.json)。通过官方 MCP SDK、mTLS、已注册 OpenBao 上下文和签名 Worker RPC 读取真实 DeepFlow。
- 两次都验证趋势 1%/5%/0%、相对增幅 400%、零基线返回 no_data 且不编造百分比；HTTP/TCP 原值与分子分母、耗时进入持久 Fact/QueryResult。原始统计子查询预算为 3/2/2，来源 Evidence 引用数与之相同；每个父 Evidence 均已 archived_verified，归档明文摘要与内容摘要一致；授权读回值及结果摘要一致，其他 owner 的 QueryResult 读回被拒绝。
- 来源时间始终为 unreliable / timeReliable=false，不输出传播先后或因果结论。统计总体是合格采集点的完成会话/周期观测，不外推所有用户请求或唯一失败连接。

## 修复与门禁

Agent/Server 当时均在运行、Agent STATE=1/ENABLE=1/EXCEPTIONS=null；旧日志可见 Server 重建期间旧 Ingester 地址连接失败。没有通过 Running 或已有历史表判定恢复，而是用以上新流量重新证明采集及当前原生身份/范围资格。镜像、UID、资源上限见 [runtime-before.json](runtime-before.json)，本次没有重启 Agent/Server、增加内存/CPU、提高 QPS 或放宽超时。

验收脚本原先把整个 release 当作恰好两个 Pod，新增 client-b 后会误拒绝单链路；现明确选原 client/server，并保留唯一性、Running、UID 和容器身份检查。不同操作的取消现使用不同幂等键，修复测试自己的 IDEMPOTENCY_CONFLICT，不修改服务端幂等规则。

实际签名路径暴露重复原生身份读取的成本：既有窗口批次已经统一提交，原先仍在批次两端分别执行完整映射校验，且解析整个 namespace 的标签。共享 Kubernetes 预算下最终校验可能被调用方取消，旧错误处理把原因折成范围失败。

现在批次用完整原生快照包围所有后端元数据及窗口读取，只解析请求的两个端点；批次结束再次解析当前后端 ID、重读完整原生快照及来源绑定，全部一致才发布/归档。调用内快照不跨请求复用，也不覆盖旧流读取的映射。新增回归验证容器在窗口读取期间改变时不发布任何统计，并验证旧流映射仍可读。单 namespace 的批次原生读取从 12 次减为 6 次是合同计数，不是容量实测。2.5 秒映射、3 秒统计/MCP、共享 Kubernetes QPS、字节、内存、身份和来源门禁均保留。

诊断只记录阶段、耗时、错误类别和取消状态，不记录 SQL、HTTP 载荷、上下文令牌或凭据。身份栅栏的超时原因现在保留在错误链中，仍拒绝结果。

## 失败与限制

所有 r1～r13 日志保留。r1 缺少指定本地归档 Fixture 镜像；r2～r5、r8、r10 在签名相对变化阶段降级/取消；r6 数值及归档断言已过，但取消幂等键冲突；r7 为本轮中间实现误覆盖旧映射而导致来源资格失败，已修复并增加回归；r9 是最终优化前的一次 PASS，不能覆盖随后的 r10 FAIL；r11 是前置报告尚未写完导致的启动错误，未进入有效验收。最终只用 r12/r13 证明本轮代码连续通过，不用失败重试或返回 200 代替语义断言。

冷启动时仍可能先记录一次 native_before 2.5 秒超时；现有资格刷新在原预算内重试，未就绪时仍不可用。两次连续通过仅证明本次单链路受控范围，不能外推并发稳定性或 P50/P95。S3 的 TLS/IAM 后端、租户/角色/PKI 引导是明确辅助 Fixture；DeepFlow、Kubernetes UID、统计、OpenBao、签名传输、持久化和授权读回为真实路径，不宣称生产归档产品准入。

两个额外资格流量过程均有界、位于冻结窗口之后，分别完成 900 和 180 个健康响应，失败为 0；现已自然结束，见 [runtime-after.json](runtime-after.json)。受审计保护的测试数据库及归档材料保留，未清理。测试 API/Worker 为临时运行，验收结束已停止；本次没有把网页登录后端作为常驻服务发布。

本地相关 Go 单元、vet、合同检查通过；Probe 语法检查及远程实际执行通过。没有在本地运行容器、数据库、集群或浏览器。

## 运行、复验与回滚

源码/二进制/回执绑定见 [binding.json](binding.json)。最终源码提交后的重编译与远程通过验收的二进制 SHA256 完全相同。

远程私有材料根目录 `/opt/ops-assistant-dev-20261012`，本次工作目录 `fault-chain-source-20261015`。复验需重新生成尚在保留期内的控制窗口和独立计数，使用当前 Controller/Querier 环回 port-forward、原生 UID/namespace UID/容器身份。不得把 report 的 PASS 或 qualification 的有效时间手工延长。

执行入口为 `scripts/deepflow-dev/fault-chain-window-probe.py`、`fault-chain-probe.py`，以及 `TestAssistantActualNetworkOperationsSignedMCP`。本次开关为 OPS_FAULT_CHAIN_RUNTIME=1、OPS_ASSISTANT_MANUAL_MCP=1、OPS_ASSISTANT_DEEPFLOW_GRAPH_ONLY=1；两个 PROOF 环境变量分别指向当前 http/windows JSON。归档复验显式选择已缓存不可变 SeaweedFS image ID 和 host network；凭据只由远程私有 runner 读取，不提交。

无需迁移或新增依赖。运行入口沿用默认关闭的 AssistantRuntimeConfig.faultChainEnabled；回滚先关闭新调查入口，再回退代码，保留历史 Fact/QueryResult/Evidence 与受保护材料。未修改正式 00～10 文档，KubeVirt/CDI 延期、Trace 禁用，无自动处置或生产发布。
