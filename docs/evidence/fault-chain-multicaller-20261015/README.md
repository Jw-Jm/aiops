# 多调用方竞争解释与共同依赖：2026-10-15

基线 main 为 b685d51；本轮只补齐真实多调用方验收和验收脚本的身份/独立计数校验。不恢复两跳、跨 namespace、Trace、虚拟化或生产发布，不修改正式 00～10 文档。

## 范围与判据

实际运行仅在 192.168.0.63 的 `ops-assistant-deepflow-20261013` namespace，沿用 `fault-chain-20261014` release 的两个客户端和一个服务端、锁定镜像及原资源上限。

- mixed：两客户端同一 60 秒半开窗，各完成 1000 响应，5xx 分别 50/0。每条链路的源聚合必须与独立客户端计数一致；固定流程分别保留 R1 异常线索和非空正常总体的 R6 反证。
- both-abnormal：两客户端各完成 1000 响应/50 个 503，确有共同下游和共享 Node，也不能仅据同窗 5xx 输出 R3。实际来源仍无可靠时间证明，当前共同资源健康不提供自身异常；必须保留 `shared_resource_cause_not_checked`。
- 两端及两个调用方使用当前 Kubernetes UID/容器运行身份。Probe 在采集前后核对身份；产品路径仍通过 LiveMapper、签名 Worker RPC、双端范围及来源栅栏。不存在将独立后端 Pod ID 注入产品映射的路径。
- 持久 QueryResult 的目标、对端、方向、冻结窗口、响应数和错误数逐项读回；Evidence 归档摘要/版本验证、跨用户及撤权拒绝沿既有运行验收执行，证据模式模型预算为零。
- 读取每个调用方自己的已提交一跳依赖，核对两条 `scheduled_on` 指向同一 Node；读取 Node 与共同下游的合格当前健康快照并保存 Step/来源引用。当前快照不能填造原故障窗的历史关系或健康证明。

## 最终源码绑定的真实验收

源码提交 `56946d32c867c80890ad339bfd192b4848d357f0`（tree `41e92cd084311a8dd67c586bc6590e035cf85c1d`），二进制 SHA256 为 `768c620ea250ec9921a4898d74355deb40b73c7025527029b926314a6eb15ea7`；完整绑定见 [binding.json](binding.json)。最终回执只采用以下两个场景：

- [mixed 源报告](mixed-r3.json)：1000/50 与 1000/0，前后 UID/容器/Node 身份一致；[完整运行 PASS](fault-chain-multicaller-mixed-r3-final2-20261015.log) 41.05 秒，[持久上下文和原生 Step 回执](runtime-mixed-r3-final2.json)。R1/R6 分别对应异常/正常链路，不将正常解释为全面健康。
- [both-abnormal 源报告](both-r1.json)：两条链路均为 1000/50，前后身份一致；[完整运行 PASS](fault-chain-multicaller-both-r1-final2-20261015.log) 39.79 秒，[持久上下文和原生 Step 回执](runtime-both-r1-final2.json)。两条链路均保留 R1，无错误的正常反证；R3 未输出候选并保留缺口。
- 每次都有 3 个成功提交的依赖 Step、4 个独立健康 Step（Node、共同下游、两个调用方），依赖 Step 不充当健康 Step。当前共享 Node 与共同下游均为 Ready=True；未获得同窗自身异常与可靠网络时间。
- 两次均验证两条链路各自错误率/耗时的 4 个持久 QueryResult，独立 UID、incoming 方向、原冻结窗口、1000 响应及 50/0 或 50/50 个错误一致。父 Evidence 归档为已验证版本，归档明文/内容摘要一致；跨用户和撤权读回拒绝，模型调用 0、模型端点离线、模型 key 不存在。
- 本地仅编译、定向 investigation 单元、Python 2 项独立校验及集成包 vet；未在本地运行数据库、容器或集群。没有拿 Fixture、跳过或 HTTP 200 作为上述真实语义通过。

## 本轮修复

旧 Probe 的最终 PASS 表达式没有再次核对独立客户端响应数/错误数；现与源计数共同校验，补充两调用方都异常、传输失败和时间窗结束条件，并在前后原生身份变化时失败。两个无集群单元用例覆盖上述计数错误及容器替换；不替代真实来源证明。

旧多调用方运行测试按客户端名称判断预期错误数，且给 Pod 的 ResourceContext 设置 ClusterScoped=true，违反既有身份合同、尚未进入调查就被拒绝。现按控制流量报告中的 UID 校验每条链路；Pod 锚点仍为 namespace 资源，Node 读取范围通过会话已有显式 IncludeClusterScoped 选择获得。未修改权限、来源、身份、内存或超时门禁。

## 过程记录与失败

早期 mixed-r1/r2 源计数均为 1000/50、1000/0；它们不具备最终脚本新增的完整身份快照字段，不能代替最终回执。早期 runtime r6 在新增严格字段前通过（40.64 秒），保存为过程证据，不能替代最终源码绑定复验。

- launcher r1/r2：私有 runner 替换路径时的 shell 引号错误，仍指向旧测试二进制，未找到目标测试；见转录记录 `launcher-preflight-failures.txt`。
- runtime r3：辅助归档 Fixture 使用缩短的 image ID，被已有不可变镜像门禁拒绝；改用完整实际 SHA256，未放宽校验。
- runtime r4：当前 DeepFlow SOURCE_SCOPE_UNVERIFIED / Graph owner 未就绪，76.87 秒后失败。
- mixed-r3 首次最终复验：investigator 未在既有启动时限内就绪，53.30 秒后失败，未进入收集；日志 `fault-chain-multicaller-mixed-r3-final-20261015.log` 保留，未延长启动时限。
- runtime r5：来源就绪后，Pod ResourceContext 的错误 ClusterScoped 字段被拒绝为 INVALID_INVESTIGATION。修正测试输入，产品校验保持原状。

全部失败日志保留。辅助健康流量在早期控制窗结束后有界产生，首轮每客户端 180 个健康响应，自然完成后才生成最终控制窗；第二轮每客户端 60 个健康响应位于两个最终窗口之后，只用于当前来源资格复验，不进入原窗口计数。来源 qualification 只绑定本次新报告和有效窗口，不延长旧证明。

## 限制与复验

R3 正向来源验收仍受限：缺少可靠时间与同窗共同资源自身异常。没有制造 Node 故障、修改 Ready 状态、伪造变更或时间资格，也不将健康当前快照解释为历史健康。真实多调用方及拒绝误判通过不代表 R3 正向候选通过。

租户/角色/PKI 引导和 TLS/IAM S3 后端为明确辅助 Fixture；DeepFlow、Kubernetes 身份、固定收集、签名 RPC、持久化、归档校验和授权读回为实际路径，不构成生产归档准入。实际模型、OIDC 浏览器、非零重传、完整恢复正向、UNKNOWN_OUTCOME、容量矩阵和整体专项均不在本轮通过范围。

远程工作目录 `/opt/ops-assistant-dev-20261012/fault-chain-multicaller-20261015`。复验先运行 `scripts/deepflow-dev/fault-chain-multicaller-probe.py --scenario mixed|both-abnormal --report <new-report>`，独立计数 PASS 后以 OPS_FAULT_CHAIN_MULTI=1、OPS_FAULT_CHAIN_RUNTIME=1 运行 `TestAssistantActualFaultChainWithoutModel`；OPS_FAULT_CHAIN_PROOF 指向新报告，不混用旧三窗证明。需要当前原生来源、Controller/Querier 环回转发及受控流量资格；凭据仍只由远程私有 runner 读取。

能力仍由默认关闭的 AssistantRuntimeConfig.faultChainEnabled 控制。没有迁移、新依赖、持久服务发布或生产操作；回滚验收代码不影响产品数据，保留已提交 Fact/QueryResult/Evidence 和受保护数据库/归档，不能删除历史失败。
