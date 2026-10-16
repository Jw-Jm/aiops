# 最终版本多源与比较窗口复验

2026-10-16；环境仅 `192.168.0.63`；基线 main `3f5bd659352de7bb1d70067c2b5d2232498d085d`。本轮复用固定收集器、原生来源、analytics、Job/Step/预算、HolmesGPT 和官方 MCP，不生产发布。

## 实现

可选关联资源历史、基线/恢复历史和变更原先在主目标网络统计前消耗工具预算。现在先收集主目标故障/恢复统计和必要健康反证，再按原预算读取可选关联历史；Pod、Node、PVC 仍共用同一主路径，未增加默认上限。预算不足继续记录未收集项，已提交 Step/QueryResult/Evidence 不丢弃。

定向回归固定 18 次工具预算、4 个 peer，验证可选历史不能先于主目标网络查询耗尽预算；Node/PVC 仍保留关联历史入口；[Python 全部单元](python-unit.txt) 71 项通过。[Go 定向检查](go-unit.txt) 通过；Linux amd64 集成测试二进制编译通过，未以跳过测试作为运行证明。前端和合同未改动，不重复无关浏览器检查。

## 运行边界

实际三窗口独立流量各 1000 个 HTTP 响应，503 分别 10/50/0；TCP 失败观测分别 0/100/0，严格按单观察点统计。实际指标/日志从同一 owned namespace 的 Kubernetes/cAdvisor/带时间戳 Pod 日志采集，不伪造历史或重启。窗口之后的有界健康流量仅维持实时来源，不进入冻结窗口。

v1 独立单向比较请求的 maxEdges=1，原 10 次 rawQueries 预算内仍要求 HTTP/TCP 的故障和恢复 4 项比较。v2 同时优先读取主目标重启比较，预算内要求重启、HTTP/TCP 故障比较与 HTTP 恢复比较；其余 TCP 恢复必须明确标记预算缺口。不能把已提交总查询数当成网络查询数，也不能用网络 oracle 验证资源指标。重启零增量独立核对各容器在两个冻结窗口中的实际计数恒定且每窗至少两个观察点。

归档和租户/PKI 为既有明确的隔离依赖 Fixture；指标、日志、Kubernetes 身份、网络流量及内网模型为实际路径。实际来源可用、模型工具机制、中文回答质量和版本验收分别报告。

模型旧运行复核显示历史消息已经携带全部 11 个 Fact 引用；因此未保留重复的历史引用转换。既有中文质量 FAIL 不因本次机制复验改变。


## 本轮证据

- [优先级修复前后](priority-before-after.txt)：同一新回归在 main 基线失败，在修复后通过，未修改预算。
- [独立三窗口](window-proof.json)：实际错误率 1% → 5% → 0%，TCP 失败观测 0 → 100 → 0；时间资格仍 unreliable，不能凭指标改善确认完整恢复。
- [v1 比较运行](comparison-v1.log)/[回执](comparison-v1.json)：HTTP/TCP 故障与恢复四项比较通过，4 查询/8 Evidence；归档、所有者隔离和撤权读取拒绝通过。
- [真实采集回执](collection-receipt.json)：132 周期，3168 指标样本，1793 条真实日志，0 采集错误。原始数据与身份配置仍在远程独立目录，不将敏感正文导出到 Git。
- [v2 首轮失败](comparison-v2-before-failed.log)：测试未区分日志数据集与网络比较数据集；没有改成忽略日志，而是增加按原始采集记录逐目标、逐窗口、结构化 error 等级计数的独立 oracle。数据库 `sp03_mig_2490c88b4a7d44888f6e3b53452f9fa3` 保留，模型解释尚未执行到，不能当成质量失败或通过。

日志入库时仅脱敏和去除行尾空白；原始日志、受保护数据库、归档及采集记录保留。KubeVirt/CDI 延期，Trace/PyRCA 禁用；全产品 SP-08 暂不测试范围、发行物料、生产 HA/灾备及 V1.0 全面验收未恢复。


## 模型结果与校验补齐

v2 r2 的确定性收集通过：5 个查询（主目标重启、HTTP/TCP 故障比较、HTTP 恢复比较及结构化日志计数）、6 个 Evidence、11 个来源 Step。模型有 2 次实际调用和 1 次上下文读取，正常结束为 completed；但回答仅复述 tenant/cluster/namespace，返回 unsupported，Fact/QueryResult 引用、缺口及下一步均为空。这是本例解释质量未达标，不是有用的中文诊断，也不能以运行测试 PASS 抹去。

最终按用户“能力机制跑通、质量不阻断”的确认，服务端将空缺口 unsupported 识别为解释质量不足：输出 partial、平台标记的 model_unsupported_reason_not_provided 缺口及 model_explanation_incomplete 警告，并明确说明模型未提供可用解释；不把它当成正常诊断完成。非法引用、身份和来源校验仍拒绝。旧历史答案仍可读；未改模型 Profile、任务身份、窗口或统计，也未增加重试/Agent 循环。合法 unsupported 的 Fixture 明确标注没有观测；新增真实数据库检查验证空缺口结果变为有明确质量标记的 partial，不能生成数值事实；同时复验原范围、历史和保留清理职责。中文提示明确要求解释已有历史观察并引用事实，不将无法确认因果误作没有可解释结果。

[Go 校验单元](model-guard-go-unit.txt) 与 Python 71 项通过。最终实际模型复验单独记录，不以提示词修改或服务端拒绝错误输出代替中文质量通过。


r3 Ledger 进一步揭示模型启动缺口：虽然请求指定了初始工具，但两次成功模型响应均没有 tool_calls，整个解释 Job 只有两个 model Step，根本未读取 get_assistant_context。不能把同时存在的 Graph/来源告警直接当成该轮“上下文读取失败”的原因。[Ledger 摘要](model-before-preload-ledger.json) 保留，无模型原文或凭据。

现通过现有 PlatformTool/MCP/JobAPI.allocate 预先提交必需的上下文读取，再交原 HolmesGPT 循环解释。已提交上下文直接复用；失败或缺少已发布工具时，在模型调用前拒绝并保留失败 Step；不同任务的来源选择仍由原工具和授权控制，没有新增模型客户端或 Agent 循环。单元覆盖首次读取、无关历史 Step 不冒充上下文、恢复复用、读取失败及缺少工具。原 evidence-only 收集入口不调用模型，旧 v2 Incident 模型路径不变。

r3 保留早期拒绝式处理的实际数据库记录；最终处理方式按用户后续确认改为明确 partial，单独复验。模型虽返回 completed，但缺少上下文读取，mechanismPassed=false；中文说明与引用仍不达标，不能把运行日志总 PASS 当作模型机制或质量 PASS。


r4 三窗口严格复验失败，未执行到模型解释：一次 TCP SOURCE_DEGRADED 后按原规则重试成功，且 L7→L4 的独立回退也消耗 rawQueries；原默认 10 次预算因此只能提交两个网络故障比较，没有剩余预算完成 HTTP 恢复。原 Step、统计和缺口仍提交；测试仍要求三项而失败，没有改成通过或提高上限。数据库 `sp03_mig_512bc90926cc4143bbf8edd7c8658953`、[Step 摘要](r4-step-diagnostics.json)和[原日志](comparison-v2-budget-failed.log)保留。v1 四比较和 v2 三比较的前轮实际通过仍分别保留，不代表持续无退化。

预读上下文的最终真实验证采用单独的已提交故障统计解释场景，不带可选比较窗口；仍以独立实际 1000 响应/50 错误校验网络数值，并独立报告 source/context/model 状态。没有取消三窗口失败断言或把该分项替代窗口验证。


用户确认继续使用当前 qwen2.5:1.5b，以能力机制跑通为本轮目标，中文质量不作为阻断项。未安装模型、修改推理服务或更换 Profile。

r5 真实预读成功：1 个已提交上下文读取、1 次实际模型调用，原证据保持不变；模型轮次 failed/INVESTIGATOR_FAILURE，诊断为 HolmesGPT CompactionInsufficientError，不能当成正常解释完成。[运行日志](grounded-before-projection.log)/[回执](grounded-before-projection.json)保留。

仅在模型展示层增加有限投影：相同来源资格按共同元数据、dataset/operation 分组，每个操作独立 verifiedUntil 原样保留；完整来源身份、revision、覆盖窗、状态不扩张。校验用 proof/plan/catalog/scope 摘要保留在原 Step，不重复进入解释输入；历史 QueryResult 的值、Fact ID、引用、窗口、统计、SourceManifest、exact/warnings/degradedSources 保持。历史数值查询与 UI/固定收集器继续读原始数据。模型后续工具输出和恢复输入使用同一投影，不建第二套事实仓库。

[实际输入大小](model-projection-size.json)为 46074 字节 → 紧凑模型视图 30619 字节；不修改原始已提交数据。71 项 Python 检查覆盖投影不改数值/引用/质量/有效期、多个资格不合并状态、失败及 null 载荷保留。未提高 16k 上下文、输出 token、超时或模型预算。


复杂历史解释的 r7 仍受冻结的 16k 上下文与 120 秒模型超时限制：上下文读取已成功，原 Holmes 压缩调用超时，150 秒观察结束时解释仍 running；mechanismPassed=true 仅证明实际上下文和模型调用，不能表示正常解释或质量通过。原日志、回执、保留数据库及压缩失败记录均保留。

小上下文 r8 因漏设既有归档镜像参数在依赖 Fixture 初始化时失败，未执行到模型；未下载/安装镜像。r9 的上下文与实际模型调用成功，但在早期拒绝式质量校验下失败，Ledger 显示模型返回 unsupported 且缺口为空。最终按用户确认将此类质量不足显式降级为 partial，不放宽事实/来源/规则门禁，不把它当成有用解释。


## 最终机制结果

[最终实际模型/MCP及范围保留运行](model-final-runtime.log)、[最小回执](model-final-receipt.json)通过：实际 qwen2.5:1.5b、官方 MCP、mTLS、OpenBao、真实数据库运行角色，get_assistant_context 和 model 各一个成功 Step；最终答案是带明确平台质量缺口/警告的 partial，不是正常中文诊断完成。Fact/QueryResult 引用均为 0，没有为通过补造数值或引用。用户已明确质量不作为本轮机制收口阻断。

最终相同源码的范围编辑、历史保留/平台澄清、Evidence/QueryResult 清理闭包共三项实际数据库检查通过。早期空缺口拒绝方案及其失败与当前显式 partial 处理分别记录；失败没有改成成功。模型复杂历史上下文压缩/超时限制、三窗口来源退化耗尽预算的失败、中文质量和版本验收仍独立保留。

本地 Go 定向与 Python 71 项通过；UI、HTTP 合同形状、生成客户端和迁移未变，无额外依赖、开关或队列。回滚沿用既有助手入口关闭和进程同步规则；不要删除已提交结果、归档、受保护数据库或法律保留。
