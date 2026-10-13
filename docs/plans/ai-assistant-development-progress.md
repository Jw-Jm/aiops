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
