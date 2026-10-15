# 故障链增强继续开发与验收：2026-10-15

专项尚未完成。本文按当前实现区分功能、真实验收和来源限制；不能把历史回执或本轮早期回执升级为最新整个专项通过。根目录 00～10 未修改，KubeVirt/CDI 延期、Trace 禁用，不自动处置 Incident、不发布生产。

## 本轮实现

- R3 继续要求多调用方同窗异常、共享关系、共同资源自身异常及合格时间/来源。收集器补选中调用方自己的、经过准入的一跳依赖读取，提供共享 Node 路径；明确记录未选链路，不查询额外对端。健康读取覆盖选中调用方、目标及已经披露的 Node，预算不足保留缺口。正反规则回归新增正常第二调用方与共同下游。
- R5 保留独立的“改善观察”，另加完整恢复谓词：HTTP/TCP 合并快照都完整且时间合格，HTTP 非空，错误率、平均耗时、传输错误观测及重传计数不高于同口径冻结基线，相关资源的恢复窗内最新 Ready=True。没有配置容差时使用严格基线，不发明通用阈值。缺少个别指标的来源仍不可完成此有限谓词。后续 NotReady/Unknown、半开终点、源策略变化、部分覆盖、空响应均拒绝。Ready 状态保留 True/False/Unknown，重复/冲突条件不能成为合格快照。
- 普通 Incident follow-up 仅接受摘要绑定的已解析资源指代、同一 Incident 和冻结 requestedTimeRange；未明确时间/目标时不可评估，不从名称、时间接近或 Incident ID 猜测目标。资源排障沿用单一 ResourceContext。
- 已发布 get_assistant_context 现在提供重新授权的历史统计原值、Fact 和持久引用，最多 8 个结果/24 KiB，超出时标记 partial；未执行新的来源查询，未刷新旧证据时间。模型仍不能提交替代身份、窗口、规则或统计值。
- 取消与预算耗尽共用已提交事实校验路径。取消保留 cancelled 状态并写入正常保留消息，源已撤销/不可披露时仍停止执行，不能披露数字。界面显示停止后保留的回答；证据/解释为不同轮次，已提交证据不会被解释失败覆盖。解释与只读健康检查按钮只准备下一轮消息，旧轮次没有切换当前解释的按钮；范围缺口链接进入现有范围编辑器，不自动扩大范围。
- 增加真实双调用方独立计数脚本、签名 MCP 趋势/相对变化/零基线及模型解释的验收入口，并扩展 OIDC 浏览器测试的键盘、390px 窄屏、解释准备与证据一致性断言。这些新增入口不等于已通过。

## 验收结果

**最新源码绑定的机制 PASS**：见 [binding.json](binding.json) 和 [fault-chain-mechanisms-closeout-r7.log](fault-chain-mechanisms-closeout-r7.log)。只在远程 PostgreSQL/最小权限角色执行：

- 取消、预算耗尽都保留已提交 Fact=7、原 QueryResult 摘要及正常消息历史；未执行项仍是缺口，状态分别 cancelled/partial。
- 真实 Repository 子进程 SIGKILL、自然租约过期、接管后成功 Step 只复用一次，旧 generation 被拒绝；complete/cancel/budget 三分支通过。
- evaluate_fault_chain Step 的 Evidence 依赖受到保留保护，QueryResult 有效期内不可清理，保护解除且 TTL 过期后按闭包清理，官方 Evidence 不被助手清理器删除。
- Incident 显式单目标指代及相对窗口冻结回归通过，未修改正式 RCA。

上述工具内容/资源及身份是明确协议 Fixture，证明持久化、权限、状态和数字保留机制，不证明真实来源数字准确性、执行中签名 RPC 的 UNKNOWN_OUTCOME 或实际模型帮助性。初轮数字 Fixture 的范围/目录和 dataset 写错被现有校验拒绝；修正 Fixture 后通过，没有放宽生产校验。失败日志保留。

**本轮早期真实统计 PASS**：新产生 [windows-r1.json](windows-r1.json)、[http-r1.json](http-r1.json)，独立响应 1000/1000/1000、5xx 10/50/0、传输错误观测 0/100/0。[runtime-r2.json](runtime-r2.json) 与 [fault-chain-closeout-r2.log](fault-chain-closeout-r2.log) 为早期固定调查 42.71 秒通过，4 个比较、零模型调用、持久引用及授权撤销拒绝通过。它早于后续恢复/取消/历史数字读取修改，不是最新完整运行绑定证明。

**真实多调用方 FAIL**：[multicaller-r2.json](multicaller-r2.json) 保存独立计数 1000/50 与 1000/0，之后 Querier 超时，来源尚未验证。[multicaller-r3.json](multicaller-r3.json) 在重启独立开发 DeepFlow Server 后仍读到 0/0，与独立计数不一致，按 FAIL 保留。两个调用方和服务器确实位于同一 Node，但没有合格自身异常与可靠网络时间，因此 R3 正向候选不可用；不能用这次失败证明正常链路或共同根因。没有改采集资格、内存、超时门禁。

**真实模型/签名统计未通过**：[fault-chain-model-closeout-r1.log](fault-chain-model-closeout-r1.log) 为 Controller 身份映射超时；[fault-chain-model-closeout-r2.log](fault-chain-model-closeout-r2.log) 为辅助 TLS/IAM 归档 Fixture 未在门禁内就绪，均未进入可用模型解释。签名趋势首轮到达真实工具后被拒绝，见 [fault-chain-signed-operations-closeout-r1.log](fault-chain-signed-operations-closeout-r1.log)；后续实际重新准入因 DeepFlow SOURCE_SCOPE_UNVERIFIED 失败，见 [fault-chain-signed-operations-closeout-r3.log](fault-chain-signed-operations-closeout-r3.log)。没有趋势/相对变化的签名 RPC 正向归档回执。模型机制与中文回答质量都未通过，不能沿用旧模型质量失败作为新测试回执。

本地：相关 Go 单元、合同检查和 vet、生成一致性通过；Python 固定流程 9 项通过；前端 19 文件/63 项和 5 项 SSE 协议测试、构建通过。没有在本地运行数据库、容器、集群或浏览器。旧/本轮失败及受保护数据库继续保留。最新 OIDC 浏览器矩阵未运行，前端构建和测试不能替代真实浏览器验收。

## 剩余工作与明确限制

- 恢复当前开发 DeepFlow 的实际采集与范围资格，重新产生受控窗口，之后验证多调用方正常/异常竞争解释、共享 Node/共同下游；R3 正向还需要合格时间与同窗自身异常。
- R4 有限变更谓词已存在；普通资源工具目录没有 get_recent_changes，网络时间 unreliable，因此变更规则的正向来源仍不可用。没有新增或伪造变更来源。R5 的完整恢复正向真实证明也受来源/历史健康限制。
- 完成实际模型通过工具读统计/规则/历史，以及模型失败后真实数字保留的完整链路；分别报告机制与中文质量。
- 完成最新真实 OIDC/API 浏览器的节点/关系/比较值/事件证据定位、旧轮次、SSE、刷新、取消、接管与窄屏/键盘矩阵。现有证据入口仍可使用，完整联动尚未完成。
- 真实非零重传、不等量桶的比例/耗时加权、半开边界、无响应及来源退化的端到端验收；趋势/相对变化的签名 RPC、归档、持久 Fact/Evidence、授权读回。
- 执行中来源请求终止后的 UNKNOWN_OUTCOME；真实采集进程中途取消/预算耗尽后的非零数字保留；本轮数据库机制不能替代这些真实来源链路。
- 单边、4 边、8 边、并发调查的查询数、字节、映射成本、CPU、P50/P95、取消隔离矩阵尚未实测。单个早期 42.71 秒不得外推容量。默认 4 边、上限 8、原始查询/内存/超时门禁保持，不在来源异常时盲调默认值。

## 运行、能力开关、回滚与同步

远程工作材料位于 `/opt/ops-assistant-dev-20261012/fault-chain-20261015`。新增 client-b 使用既有锁定 canary 镜像、原资源限制与同一独立 namespace；未操作生产。只重启该 namespace 的 ops-assistant-deepflow-server，并重新绑定本次 loopback port-forward，Server 仍为 CPU=1、memory=1536Mi；没有提升源时间可靠性。

AssistantRuntimeConfig.faultChainEnabled 默认关闭。开启仍需要当前来源 revision、binding/proof digest、collector version、覆盖窗、操作与采集点资格。暂停调查时关闭入口，保留历史 API/不可变证据；无需回退迁移。本次没有迁移、图数据库或依赖增量。回滚代码须保留对新增 Ready=True/Unknown 历史健康引用的读取兼容性，不能删除历史 Step/QueryResult/Evidence。

源码、构建与最新机制绑定见 binding.json；后续文档提交不改变该实现。按 PR 合并后核对 GitHub main、本地及远程 main 的提交与 tree。源码同步不表示运行服务已发布或来源已准入。
