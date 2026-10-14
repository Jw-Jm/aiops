# AI 专项开发进展

更新：2026-10-13。此次开发基线为 `b51abe1`。授权见
[ADR-0038](../adr/0038-ai-assistant-development.md)，需求见
[实施方案](ai-data-query-and-conversational-troubleshooting.md)。

本轮剩余六类功能已完成源码、接口、界面及专项验证。运行验证全部在
`192.168.0.63` 的 `/opt/ops-assistant-dev-20261012`；本机只编辑、编译和
执行单元/合同检查，未使用 OrbStack。此结论是功能开发完成，不代表生产
采集、中文模型质量、离线发行或 P0～P6 全阶段验收通过。

## 六类功能结果

1. 派生统计：新增 `incidents.recovery-duration` 和
   `executions.success-rate`。恢复均值只使用可信 started_at/resolved_at，
   缺失时间会降级；成功率为本人当前授权目标的成功数 /（成功数 + 失败数），
   排除取消、未知及未终结任务。空分母为 no_data，状态过滤不能改变分母。
2. 指标与日志扩展：CPU 使用固定 5 分钟 rate 趋势及窗口平均；重启使用每步
   窗口的 increase，支持计数器复位；日志使用独立半开时间桶。提供等长窗口
   compare、非零基线 compare_percent、内存/phase/Ready 的采样窗口 average。
   缺失不补零，部分输入不生成变化量。日志样例最多 20 条、单条最多 2048
   字符、响应最多 64 KiB，重新验证标签/UID并脱敏；样例数不当作日志总量。
   新操作分别需要来源 qualification，Fixture 不授予生产采集准入。
3. 多轮交互：平台只从已验证 EntityRef 或当前绑定解析“它/该资源”。多义指代
   由持久 Worker 路径直接返回澄清，不调用模型；选项必须有已验证历史身份。
   每轮冻结 requestedAt、时区及已识别的相对时间，昨天按本地日历计算并处理
   DST。提供同会话显式目标/namespace/时间重绑，带版本与幂等围栏，只允许
   空闲会话；越权、旧版本及活动轮次拒绝。历史保留原 Job 策略和数据上限，
   范围外历史不进入后续输入，历史引用不会因加载上下文变成新观测。
4. 无 Incident 入口与来源能力：自由症状只提取明确资源类型、名称、namespace；
   无明确目标或多目标时先澄清，再经授权精确查询并确认真实 UID。
   `/assistant/capabilities?clusterUid=...` 经签名 Worker RPC 返回当前配置的
   dataset/operation、source revision、采集版本、覆盖窗及证明摘要；无证明、
   过期、失效范围或 Worker 不可用时不宣称可查询。`qualification_configured`
   表示有效准入配置，不表示刚完成实时采集或生产质量验收。
5. 故障链和正式引用：关系链接必须来自成功提交的固定 Graph 工具，并匹配
   from/to/kind/relationDigest。每条保持 hypothesis，支持/冲突引用必须有
   已提交 Evidence 且互不重复，缺失因果证据不可省略。Incident follow-up
   增加只读 `get_formal_references`：RCA 指定 revision，ActionPlan 指定
   revision 和内容摘要。引用/侧栏读取重新验证 Incident、Finding、资源、
   来源和 Evidence 数据分类。历史 RCA 显式标识；变更后的 ActionPlan 旧引用
   返回不可用，不用当前版本替换。没有新增 ActionPlan 历史版本仓库。
6. 保留与清理：迁移 41/42 增加重绑幂等和独立管理员法律保留入口。Worker
   每分钟执行有界物理清理，普通 Message/QueryResult/v3 Step 默认 30 天。
   法律保留、有效 QueryResult、活跃任务、正式 Evidence 的保留/归档状态及
   递归依赖闭包优先。历史/parent/query 引用保护祖先，先删除过期叶轮次，
   再清理其 Job/工具正文及过期空会话标题、症状、绑定和幂等键。清理函数
   使用租户、保留锁和会话锁，只有 Worker 有执行权；管理员保留操作写审计，
   不赋予读取他人运维正文的权限。成功 Step 正文继续不可变；迁移只能缩短
   v3 TTL 元数据，受限清理函数才能删除候选 Step，v2 原有保留语义不变。

界面已接入范围编辑、指代候选、症状解析、来源能力、日志样例、故障链、
具体正式版本侧栏及独立管理员保留表单。OpenAPI、Go/SQLC 和 TS 客户端已
重新生成。模型输出仍走锁定 HolmesGPT、官方 MCP、租约与平台引用校验，
未增加 Agent 循环、模型 HTTP client、自动执行或正式 RCA 确认路径。

## 专项验证证据

- 受影响 analytics、assistant、investigation、tools、httpapi、app、evidence
  Go 包及合同测试通过，受影响包 go vet 通过。生成器重新运行且生成产物
  一致性检查通过；TypeScript/Vite 构建通过，runtime 源码闭包检查通过。
  额外全 internal 广域运行遇到本机链接空间不足并中止，未宣称其通过；
  清理本专项旧临时编译产物后，必要受影响包和合同检查串行复跑通过。
- 远程真实 PostgreSQL 迁移 1～42，真实 API/Worker 角色；专项测试验证
  恢复时长、75% 成功率和 owner/namespace 分母排除，重绑幂等/忙碌拒绝、
  历史隔离、平台澄清及显式选项后续提交，正式引用版本/摘要/目标范围，
  法律保留、历史依赖、正式 Evidence 闭包、保留 QueryResult 和物理清理。
- 真实专项 VictoriaMetrics/VictoriaLogs 使用带租户标签的人工数据验证
  CPU 0.5 核趋势、重启复位、Pod phase 0/1/2、窗口平均、日志 1/2/3 分桶、
  200% 比较、脱敏/截断样例，以及之前的内存缺口和 Node Ready 趋势。
  这些全部为 fixture_only，不代表生产采集、完整性或性能已获准入。
  最新 11 项非模型顶层专项测试通过，日志 `six-integration-latest.log`。
- 故障链持久提交、拒绝伪造关系摘要、多义澄清及显式资源选项的三轮闭环
  通过，且没有更改 Incident 的正式 RCA revision。
- 远程 cached investigator 容器运行更新 Python 源码：48 项通过，包括
  Profile、预算、实体/事实和新增关系/正式引用的 provider schema 约束。
- 远程 Chromium 隔离接口 Fixture 三项通过：范围编辑请求、故障链/日志样例、
  正式版本侧栏、键盘指代选项和明确提交、管理员保留原因与独立角色显示。
  同时验证 1440/390 像素无横向溢出及日志作为文本显示。日志为
  `ui/browser-latest.log`；这是真实浏览器渲染证据，不是生产 OIDC/API 验收。
- 更新源码的实际 `qwen2.5:1.5b` + HolmesGPT + 官方 MCP + OpenBao + mTLS
  烟测通过：一次上下文读取，平台校验后的 unsupported，耗时 50.19 秒，
  保留数据库 `sp03_mig_56d7eeaf0031431a84576cca5a2d0b1d`，日志
  `six-model-smoke-latest.log`。该烟测只证明协议闭环，不证明问数/排障质量。

保留清理首轮失败（成功 Step 不可变触发器阻止删除）保留于
`six-integration-retention-first.log`，数据库
`sp03_mig_a8b7ba69b74d40818158ebd7a5777a9a`。修复后复验通过，失败记录未
改为成功。早先 Profile 联调失败数据库
`sp03_mig_92b99e1466044992a8f73598a5da950f`（重复 timeout）和
`sp03_mig_9621049c0ea347b1a4f61aefba4d4507`（非法假设引用）继续保留。

专项配置、密码和令牌只在远程受限目录中，不进入源码、文档或发行物料。

## 正式上线所需的独立门禁

### 五个独立中文评测用例（2026-10-13）

用户明确要求本轮只测试五个独立用例，每例一次。已完成冻结输入、独立黄金
算术、实际 QueryIntent/Step 采集、逐例评分与人工复核模板；开发问题与验收
输入分开保存。工具见 [评测运行说明](../../scripts/assistant-eval/README.md)。

远程实际 qwen2.5:1.5b、HolmesGPT、官方 MCP、OpenBao/mTLS、PostgreSQL
运行角色和持久 Worker 路径完整收集五例，耗时 300.07 秒。运行工具 PASS
仅表示采集结束；评分器返回退出码 2，整体质量为 **FAIL：1 例通过、4 例失败**。

- 问数失败：模型只读 get_assistant_context，返回 unsupported；没有提交
  QueryIntent/查询结果，未得到黄金结果 3，不能宣称数字/单位正确性实测通过。
- Incident 排障失败：返回 unsupported，未明确列出 missingEvidence/nextChecks。
- 无 Incident 排障失败：有缺口/目标引用，但仍返回 unsupported，未产出合格诊断。
- 多轮排障失败：UID 指代与绑定时间窗检查通过，且没有新增 Incident；两轮
  模型回答均为 unsupported，不能把平台记忆正确当作排障效果通过。
- 无验证目标的“它”由平台持久澄清路径返回 awaiting_user/clarification，通过；
  该例没有调用模型，不作为模型理解成功证据。

[逐例报告](../evidence/assistant-evaluation-20261013/report.json)、
[原始轮次](../evidence/assistant-evaluation-20261013/observations.jsonl) 和
[人工复核模板](../evidence/assistant-evaluation-20261013/reviews.template.json)
随源码保存。三个排障用例人工帮助性/诚实性复核尚未填写，不计为通过。
五例不估算总体准确率、帮助率或三次复测方差，不代替完整中文质量准入。

评分器八项回归测试通过，覆盖错误数字/单位/范围/时间/排序、伪造引用与
Fact 单元格、遗漏/重复用例、基准漂移、多轮 UID/时间及副作用检查。
受影响 Go 集成包编译与 go vet 通过；本机未运行数据库/模型/容器。

远程证据目录为 `/opt/ops-assistant-dev-20261012/evaluation-five-20261013-final`，
保留数据库 `sp03_mig_471d6b9acbda442f9c33c8220e5153f7`。采集框架初轮
MCP/API 地址约束失败、测试身份缺少 cluster scope、跨用例幂等键冲突分别
保留在 `evaluation-five-20261013-first`、`-latest`、`-run`；修复评测框架后
重新收集五例，未将这些基础设施失败伪装成模型成功。没有修改验收问题、
降低规则、调整生产 Prompt 或替换模型来提高得分。

该 Fixture 环境缺少新的 Graph Owner/真实采集和非空正式因果证据；无证据
链例只检验诚实缺口和禁止编造，不证明复杂故障链帮助性。模型、来源与发行
准入仍为下面的独立门禁，生产开关保持关闭。

本轮六类功能没有剩余编码项。以下工作属于正式放行，不能用上述 Fixture
或 unsupported 模型回答替代：

- 真实生产采集的来源 qualification、标签完整性、事件时间和容量测量。
- 合格模型权重与许可证材料、冻结中文评测集、问数/排障/多轮质量独立复测。
- 与此源码对应的签名镜像、离线物料和 Bundle；实际升级/回滚、进程崩溃
  接管、生产 OIDC/API 浏览器闭环及正式发行验收。
  迁移源码包 `prepare-migration-source.py --check` 对旧准入摘要的检查失败：
  新增 41、42 后源码闭包已改变。真实数据库向前迁移已通过，但本次没有
  重签旧发行证据或构建新 Bundle；发布时必须重新完成迁移物料准入。

[运行手册](../runbooks/assistant-runtime.md) 已随功能更新。正式助手开关
保持默认 false。KubeVirt/CDI 持续 deferred/disabled/unverified，根目录
00～10 正式文档保留，人工处置继续本人输入真实 Bash 并确认。


## 2026-10-14：真实来源、运行机制与 DeepFlow L4/L7

本次增量依据 ADR-0039、ADR-0040，用户已明确模型回答质量不作为机制验收
标准。2026-10-13 五例质量整体 FAIL 与未填人工评审保持原样。

已完成实际来源验证：独立 namespace 两个真实 Pod、原生 cAdvisor CPU/内存、
Kubernetes 重启/phase 和原生带时间戳日志。96 轮采集得到 1536 个指标点、
1344 条日志，采集错误为 0；窗口聚合、趋势、比较、日志样例和独立 golden
计数通过。100 次查询、并发 4 的 P50/P95 只代表此有限开发范围。
VictoriaLogs 固定查询增加实际 `_time` 约束，避免仅 HTTP start/end 无效。

运行机制通过实际单子进程 SIGKILL、租约过期和接管验证；旧 generation
不能提交，已提交 Step 不重复。真实 Keycloak PKCE/RS256、当前数据库角色、
API/Worker、持久轮次、SSE、刷新恢复和跨角色隔离已通过原 VM/VL 运行闭环。
Archive 的 TLS/IAM 依赖明确为独立 Fixture，不代表生产归档或 HA/灾备通过。

DeepFlow 实时 UID/容器身份映射、6 项 L4 查询、HTTP 200/503 的 L7 元数据、
范围与来源撤销均通过独立真实适配器验证。固定 L7 模板只返回协议/状态/耗时，
不返回 URL 参数、头、正文、SQL 或 Trace。来源验证和 L7 证明分别续约；
没有实际非空 L7 证明时不标记 live。MCP 新增 get l7 context，接口及页面
返回当前范围的 networkCapabilities，Trace 始终 CAPABILITY_DISABLED。
单目标查询不披露范围外对端身份，也不把不透明统计转换成图关系。

开发部署与 Agent Group 共用 checked-in 默认资源上限：1024 MiB（1 GiB）、
1000 millicpus；requests 为 256Mi / 100m。通信地址从独立 Service 解析，
脚本保留已有配置并回读核对。远程同一 Agent Pod 在 2026-10-14 08:21
核对时已运行约 16 小时、重启 0 次。Server 增加独立启动探针以等待数据库
初始化，配置见 scripts/deepflow-dev。此部署仍是 candidate，不进入 core
Bundle，不修改既有 core/final 发布配置。

签名 MCP 的 L4/L7 持久调用已于 r16 通过（34.63 秒）：官方 SDK、mTLS、
OpenBao 注册上下文、两个已提交步骤、非空 EvidenceRefs、wire replay 拒绝及
Committed 分配复用；Incident 数为 0。依赖的 tenant/role/PKI 和 TLS/IAM
Archive 仍明确为 Fixture。

网络能力页面追加真实浏览器验收已通过：Playwright r7（1.3 分钟）、
Go OIDC runtime r4（187.42 秒），随机 runtimeId 回执一致；真实 PKCE/RS256、
当前角色、L4/L7 live/Trace 禁用页面、持久轮次、SSE、刷新与跨角色隔离通过。
轮次为协议有效 partial，运行日志保留 Graph 暂时不可用和 investigator
可用性错误，不宣称本次模型诊断效果或持续健康通过。2026-10-14 曾发生 91%～93%
I/O wait、Controller 超时、Graph 租约续约失败及 CDP 超时，失败轮次保留。
实查本专项三个 PVC 在 NVMe，但原浏览器工具和 containerd 在机械盘 /data，
机械盘读取等待 231～417ms，NVMe 仅 0.07～0.16ms。独立同版本工具复制到
NVMe；开发 Server 存活探针改为 5 秒 timeout、10 秒周期、12 次失败窗口，
readiness 和实际来源准入不变。长时测试静态签名 CRL 增加 Fixture 周期
刷新，保留浏览器 r6 的 stale CRL 拒绝；不调整生产撤销检查。真实最终结果
以 evidence 回执为准。

证据见 ../evidence/assistant-runtime-20261013；脚本见
../../scripts/assistant-runtime/README.md 与 ../../scripts/deepflow-dev/README.md。
迁移闭包的旧准入摘要仍因既有 41/42 增量失配；不修改旧准入或以 make check
替代正式发行。根目录 00～10、Trace 禁用和 KubeVirt/CDI 延期继续有效。


2026-10-14 补充授权：用户允许因远程机器配置绕过 DeepFlow 阈值。独立开发
Agent Group 增加 `global.circuit_breakers.relative_sys_load.trigger_threshold=0`，
checked-in 默认值、幂等同步脚本与 Controller 回读一致；1 GiB / 1 CPU
上限及平台 UID/权限/范围/非空 L7 证明保持。原高 load15 使 Agent 进入
melt_down；真实数据恢复后，完整 L4/L7 适配器 r9 通过（5.56 秒），含
HTTP 200/503、来源撤销与 Trace 禁用，见 deepflow-live-after-bypass.json。
历史 09:27 的 Agent 零重启回执保留；最终回读记录 09:44 的一次退出 255
重启，发生在负载阈值覆盖之前，不宣称当前仍零重启。

补充正常模型浏览器轮次 r8（OIDC network r7）因 MODEL_TIMEOUT / failed
未通过，失败与停止保留回执随源码归档；原浏览器 r7 / Go r4 的有效 partial
机制通过保留，但不替代正常模型成功路径。冻结 120 秒模型协议未放宽，
五例质量 FAIL 原样保留；模型正常轮次可用性仍是单独待验证项。
