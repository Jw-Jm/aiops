# DeepSeek 外部模型专项验收

2026-10-16；源码基线 main `eab5c35af79925829468394c517e6300469d5ca7`。
用户明确提供 `deepseek-flash` 用于验收。所有模型、Docker、数据库与 MCP 运行
均在 `192.168.0.63`；本地仅编译、单元与离线评分。未生产发布。

## 接入与修复

用户提供的 Anthropic 端点和同服务 OpenAI-compatible `/v1/chat/completions`
均实际返回 200 及对应模型响应。为复用现有 HolmesGPT/LiteLLM，专项走后者。
密钥只保存于远程 `runtime-private/deepseek-acceptance.key`，权限 0600；
不进入源码、Worker 或普通日志。Worker 只取得随机回环网关令牌。

仅集成测试增加可选回环网关，固定上游、模型、路径及每个测试运行 24 次请求
上限；原 Job/Step、官方 MCP、mTLS、OpenBao、预算和平台校验继续执行。
无新依赖、生产模型客户端、队列、迁移或权限扩张。现有生产内网准入未放开。
测试 Profile 的许可证、权重和 zero-charge 是 Fixture 材料；上游实际付费，
不能将它们作为外部模型生产准入或免费声明。模型别名也不证明权重不可变。

实际探测 `json_schema` 为 400（该 response_format 不可用），`json_object`
为 200。测试适配层将原 Schema 原样加入输出指令并使用 JSON 模式；服务端
依然严格校验结果合同、来源、身份、窗口、引用及 R3/R4/R5，不修改数据。
64-token 简单探测仍未解析成有效 JSON，不计作质量通过。

真实排障请求进一步暴露共用格式化入口的缺陷：旧工具输入附带未使用的输出
定义，引用私有 `https://ops.local/schemas/tool-response/v2`，被上游拒绝。
现在仅在模型输入视图剔除旧输出定义与合同元数据；输入属性、必填、边界、
本地输入定义、原 MCP 合同及目录保持。回归覆盖全部 16 个旧工具和本地定义。

默认思考模式两次占满 2048 输出，以 length 截断，修复 JSON 一次后仍失败。
最终专项固定 `thinking.type=disabled`，保持原 16k/2048/120s/零重试，未增加
预算。上游错误正文保存在独立远程 0600 文件，已替换已知凭据，不导出 Git。

## 最终结果：调用机制可用，验收未通过

[r6 Ledger 回执](r6/ledger-receipt.json)：中文五用例 5 次上下文读取、5 次模型
调用均 succeeded；独立烟测另有上下文和模型各 1 次 succeeded。
五用例实际观测全部记录，[评分](r6/report.json)为 FAIL：

- zh-001 返回不必要的 clarification，没有执行冻结的故障数量查询。
- zh-002、zh-003 返回有缺口的 partial 诊断，自动评分未通过。
- zh-004 两轮 partial，身份/冻结窗口多轮检查 2/2、引用检查 4/4，仍不是整体通过。
- zh-005 多义目标澄清通过，此项是平台澄清路径，不需要模型。

[最终日志](r6/runtime.log)中的五用例 Test PASS 只表示完整收集了观测；评分器
仍以退出 2 报 FAIL。三个排障/多轮用例的人类帮助性评审仍 pending，不由模型
代填。独立烟测返回有明确缺口的 partial/unsupported，不符合原烟测对 partial
的窄预期，因此 Test FAIL；断言没有放宽。

这是一组 Fixture 业务事实上的实际模型/MCP测试，不是实际五来源非空故障链
复验，也不证明整体准确率、中文质量通过、容量、发行或 V1.0 全面验收。

## 检查与失败保留

- [Python 单元](python-unit.txt)：73 项通过；两个既有 typer/click 废弃警告。
- [验收网关 race 检查](go-gateway-race.txt)：通过；格式转换、输入保留、鉴权、
  固定路径及调用上限均有断言。Linux amd64 集成二进制编译通过。
- [离线评分器单元](scorer-unit.txt)：8 项通过；未改冻结问题、黄金值或评分规则。
- [r1](r1-startup-failed.log)：首次误改旧 ModelContract 引发启动拒绝，未调用模型；
  后续保留原旧路径配置，仅替换助手 Profile。
- [r2](r2-format-failed.log)、[观测](r2-observations.jsonl)：原 json_schema 被拒绝。
- [r3](r3-thinking-failed.log)、[观测](r3-observations.jsonl)、[评分](r3-report.json)：
  JSON 适配后问数仍耗尽预算，排障工具仍有私有输出定义引用。
- [r4](r4-thinking-smoke-failed.log)：默认思考导致两次 length 截断及 JSON 修复失败；
  原 Ledger 和数据库保留于远程。
- [r5](r5/runtime.log)、[评分](r5/report.json)：关闭思考后烟测通过，但旧工具
  输出定义仍被拒绝，五例质量 FAIL；不以这次烟测替代最终版本结果。
- [最初新增单元断言失败](python-test-initial-failed.txt)：误以为所有旧工具都有
  required；改为保留原 required 的有无，产品约束不变。

远程每轮输出为 `/opt/ops-assistant-dev-20261012/deepseek-acceptance-20261016-rN`。
Git 中日志仅去除行尾空白；原始文件仍在远程保留，失败和评分内容不改。
受保护数据库、归档及失败记录保留。测试进程和回环网关已结束；无常驻代理，
原 Ollama、来源和预览不变。KubeVirt/CDI 延期，Trace/PyRCA 禁用。
