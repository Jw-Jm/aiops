# 代码修复与真实闭环补验（2026-10-18）

## 本轮功能开发收口

用户于 2026-10-18 明确调整范围：仅完成功能任务开发，远程机器性能导致的问题仅作记录。
本轮功能改动及本地回归已完成：能力查询整体超时降级、canary 持续运行、窗口证明绑定与校验、
页面解释/追问账本校验、已提交 QueryResult 导出，以及同范围长上下文复核工具。
远程性能调优、追加诊断和重复远程验收不再作为本轮开发待办或完成条件。

以下真实运行结果原样保留，FAIL/NOT_RUN 不改为 PASS；本轮功能开发完成与完整运行验收分开记录。
部分超时的具体根因尚未证明，不将全部失败直接归因为机器性能。Node/PVC 实时授权与对象、
满 1M 上下文质量和人工质量评审不计为本轮新增功能开发缺口，仍保留原有未覆盖状态。

按新范围继续核对实际实现并完成本地功能回归：

- 后端 Assistant、HTTP API、analytics、investigation/MCP/tools、DeepFlow、合同和安全测试全部通过。
  Node/PVC 已有类型适用性和显式 Node 集群范围校验；不为运行验收新增权限或对象。
- investigator 完整测试 93 项通过，覆盖固定证据收集、恢复、缺口检查、Pod/Node/PVC 类型分流、
  长上下文投影与解释读取约束。
- 前端 23 文件/75 项组件测试、5 项 SSE 协议测试及 `tsc --noEmit` 通过。
- `go build ./...`、受影响包及 integration 的 `go vet` 通过；窗口证明回归与两组 Python
  工具测试（6 项、10 项）通过。

本次没有发现需新增实现的本轮功能缺口；没有追加远程访问、性能调整或验收重跑。
具体命令见 [checks.json](checks.json) 中 `functionalCloseout`；真实页面、远程来源与模型质量
不由上述本地测试替代。

## 先更正旧结论

round4 的长上下文“6/6 通过”撤回，旧文件保留并加复核说明。
[原始响应](prior/long-context-r1.json)的 HTTP 恢复段是 `current=0, baseline=5, increase=5`，
[已提交结果](prior/committed-results-r2.json)则是 `decrease=5`；不能把它写成模型正确保留了 `−5`。
已提交上下文来自 r3 租户 `3fb34947`、01:28:14Z～01:31:14Z，拼接的原始观测来自 r4 租户
`2984df90`、02:50Z 后的采集，不能称为产生同一份已提交结果的观测。

[重算结果](prior-retention-reassessment.json)：内容检查 5/6，集合窗口覆盖 false、租户一致 false、
总结果 false。119,962 为输入与缓存读取合计（410 + 119,552），输出 2,895，正常结束。
这不改变原容量探针“917,288 输入被接受、截断、只作容量证据”的有限事实；该次 265.4s
也不能证明当前 120s assistant 配置可以完成同等请求。

复核入口：

```sh
python3 scripts/assistant-runtime/verify_long_context.py \
  --runtime-report docs/evidence/five-source-reverify-20261018/joint-r2-runtime-report.json \
  --query-results docs/evidence/five-source-reverify-20261018/round5/prior/committed-results-r2.json \
  --collection-receipt docs/evidence/five-source-reverify-20261018/round4/live-source-r4/receipt.json \
  --response docs/evidence/five-source-reverify-20261018/round5/prior/long-context-r1.json \
  --output /tmp/ops-long-context-reassessment.json
```

该命令应退出 1；输出文件使用独占创建，不覆盖已有证据。

## 本轮代码

- 能力查询的 2s 上限覆盖两次路由数据库读取和签名请求，不再只限制网络子请求。
  新增阻塞路由读取反例：修复前耗尽外层 3s，修复后 2s 内降级并保留外层期限。
- 专属 client 从 24h 睡眠改为持续等待；副本仍为 1，滚动更新 `maxSurge=0/maxUnavailable=1`，
  不借助增加副本消除环境问题。
- 三窗口证明导入核对 stable、来源错误、各窗 HTTP/TCP 总体、端口唯一性、连续窗口、
  五分钟指标回看覆盖、故障段文件/摘要绑定、租户，以及当前原生 UID、容器 ID 和启动时间。
- 页面实际模型验收核对两个不同的解释/追问 Turn；数据库必须分别有成功模型响应和一次上下文读取。
  解释不得增加来源读取；追问必须恰有三项成功读取，保留原 UID/窗口，预留预算归零。
- 联合验收报告直接保存实际读回的完整 QueryResult，避免再次用手工 SQL 文本导出作为类型依据。
- 长上下文校验区分增加/减少；新探针只选本轮同租户/集群/namespace/UID、半开冻结窗口内的真实行，
  单次请求上限 120s、输出 8192。独立探针与平台完整运行验收仍分别记录。

## 已执行检查

- `go test ./internal/httpapi ./internal/graph ./internal/integrations/deepflow ./internal/investigation/...` 通过；
  Graph 的 20K 测试在本机耗时约 192s。
- `go vet ./internal/httpapi ./test/integration`、三窗口导入的正例及 10 个反例通过。
- `scripts/assistant-runtime` 6 项、`scripts/deepflow-dev` 10 项通过。
- [远程隔离 PostgreSQL 回归](context-regression.json)通过、无跳过：能力读取超时后返回已提交历史，
  `get_assistant_context` 成功提交。慢能力服务为明确的协议 Fixture，不冒充页面闭环。

## 真实运行

运行目录 `/opt/ops-five-source-reverify-20261018/round5`，不覆盖先前轮次。
第一次更新遇到 `http2: client connection lost`，随后只读核对又遇 TLS 握手超时；失败保留。
恢复读取后确认原 Deployment 未变更，才执行一次有界重试；专属 client 已滚动更新成功。
转发曾因 `lost connection to pod` 退出，第一轮新窗口在首次 Querier 读取时遇到 connection refused；
该轮见 [失败日志](failed-before-forward-repair/windows.log)，不产生合格窗口。恢复原有专属
`127.0.0.1:21416/21417` 转发后，核对 Querier SUCCESS 和当前两端容器 ID 一致，再生成新窗口。
恢复的转发保留；不修改服务、组件资源上限或生产发布配置。

最终状态见 [acceptance-status.json](acceptance-status.json)：

- [采集](live-source/receipt.json)：126 周期、3024 指标、2196 日志，0 采集错误、0 身份漂移丢弃。
- [三窗口](windows.json)：HTTP 1000/10、1000/50、1000/0，TCP 失败观测 0/100/0，stable=true。
  第一轮查询超时、第二轮完整对账并复读一致；`sourceErrors` 保留一次 TimeoutError，不能称为 0 来源错误。
  [独立刺激记录](windows.golden.json)在首次后端查询之前落盘，防止断连后丢失 oracle。
- 初版新增导入校验误把历史查询错误全部当作永久失败，导致一次投影断言失败；该失败保留。
  校验现与探针原有有界恢复判据一致：错误必须属于已过去的轮询，最终轮询必须完整对账、稳定复读，
  当前身份、窗口覆盖及故障段绑定仍全部验证；新增已恢复超时的正例，未放宽产品身份/时间/来源门禁。
- [联合前置正样本](qualification-joint.json) 300 响应/15 个 5xx 精确对账并复读一致，独立于三窗口。
- [联合验收](joint.log) FAIL：`DeepFlow identity refresh rejected stage=native_after elapsedMs=6000`，
  `native client identity not qualified context deadline exceeded`。身份验证在第二次原生快照阶段达到整体 6s 预算；
  尚未区分前序请求耗尽预算与该快照本身缓慢，不将它直接归因为机器性能或排除软件问题。
  失败库 `sp03_mig_9e88544a137846fabc473a29e3ec53a6` 因 audit.records 保留，未自动清理。
- 页面、新的同轮长上下文 NOT_RUN：共享身份前置条件失败，没有产生新的已提交上下文。
  不用旧联合结果替代本轮，也不修改 6s 或其他门禁来制造通过。

## 后续分段诊断（06:40Z～06:42Z）

已补全身份刷新失败日志的分段耗时，以及慢原生读取的总耗时；本轮测试 transport
逐项记录原生 API、Controller、标签请求的路径、状态与响应头耗时，不记录请求体或凭据。
原生读取总耗时包含本地限流等待和响应体读取，transport 耗时不能替代它。
新增日志字段回归、DeepFlow 相关测试、能力/窗口回归、Go vet 与两组 Python 测试通过。

执行一次带计时的联合诊断，复用本轮仍在保留期内、当前身份核验通过的窗口，未重新刺激、
未扩容、未修改超时/门禁。见 [诊断摘要](identity-timing-diagnostic.json)、
[完整日志](identity-timing-r1.log)和[同刻只读主机快照](identity-timing-host.json)。

- 原生 client 映射 PASS，L4 读取返回 1 条 fresh、非 partial 证据。前置原生 HTTP 请求约
  0～4ms，两次 Controller 请求为 638/5ms，最慢标签请求 2435ms（后续 L4 读取的标签请求最高 278ms）；原来的 6s 超时未复现。
- 后台工作启动后出现原生读取 6822ms、Querier 8001ms 超时和 Lease 续约失败。
  联合最终在 115.11s FAIL：`actual native Graph owner did not qualify`。
  失败库 `sp03_mig_298b37aa46aa44ff851b1cf2c02dc170` 保留；归档 Fixture 进程已停止，
  受保护存储与身份依现有清理规则保留。诊断测试进程已退出。
- 同刻独立 kubectl 读取 readyz/namespace/Pod 为 117/33/34ms；主机 I/O pressure
  `some avg10=84.10/full avg10=79.05`。该采样说明存在明显 I/O 压力，但不能据此排除
  软件问题，也不能区分共享限流队列、请求传输、数据库和磁盘各自的贡献。
- 页面和新同轮长上下文仍 NOT_RUN，没有新的已提交联合上下文；本轮不反复重跑。

上述 Graph 就绪、超时与主机 I/O 观测按用户新范围仅留档，本轮不再追加定位、调优或远程重跑。
若后续单独恢复真实运行验收，仍须保留身份前后栅栏、现有超时和来源证明门禁；
窗口身份/覆盖/保留期失效时重采，再分别记录联合、真实页面解释/追问和同范围长上下文结果。

网关证书能由既有 `operational-browser-root-ca.pem` 验证。页面运行通过 `NODE_EXTRA_CA_CERTS`
信任该 CA，不设置 `NODE_TLS_REJECT_UNAUTHORIZED=0`，浏览器不忽略证书错误。
页面脚本、配置和二进制已准备本轮副本，但页面未启动；不升级 Node、Chromium 或依赖。

## 边界

Node/PVC 真实 assistant 任务仍需此前未获批的 cluster-scoped 会话权限与专属 PVC 对象；本轮不扩大授权。
人工质量评审保持 pending。时间证明仍不可靠，不能从窗口数值改善推导传播先后或合格的 R5 恢复结论。
KubeVirt/CDI 延期、Trace 禁用、根目录正式文档只读及不修改 core/final 发布配置的边界保持有效。
