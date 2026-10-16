# 问数、追问补证与 partial 状态收口

2026-10-16；基线 main `535cb5e1b30052d91da13dcfc4b04500425a0bc7`。
用户本轮授权完成剩余清单 1～3。模型、数据库、容器及浏览器运行仅在
`192.168.0.63`，使用用户提供的 DeepSeek 隔离验收路径；不生产发布。

## 实现与复用

1. 当前问题从已提交上下文取出，以 user 消息交给原 HolmesGPT；历史、日志、
   注解仍作为不可信数据。上下文提供按授权域过滤的数据集摘要，控制面 SQL
   查询不再被外部指标/日志资格状态误作不可用。明确数值请求且时间已冻结时，
   原 BudgetedLLM 要求先读定义、再提交 QueryIntent；存在必填过滤条件时保留
   真正澄清，定义或查询失败后不强制重复。普通问数的模型工具视图只暴露 intent，
   不暴露资源覆盖参数；服务器范围、身份校验与原 MCP 合同保持，不删除非法参数
   来绕过校验。全部数值仍由原 analytics 计算。
2. 资源调查和补证复用同一主目标历史/Finding/变更请求列表，通过原 MCP/Step
   读取服务器冻结 UID 和 fault 窗口；Incident 读取原上下文与正式引用。新轮次
   形成新 Step，接管继续依靠原分配幂等。明确只解释或不重新读取时不自动补采。
   失败 Step 的静态回执进入解释上下文，不能成为成功引用；成功来源引用由
   服务器附入答案并重新授权，模型不得复制历史句柄。
3. 平台按提交回执标识来源读取失败、来源覆盖不完整和非精确查询；模型自己
   声明 partial 而无来源原因时标为 model_answer_incomplete。未知因果本身
   不强制 partial。JSON/引用 Schema 校验复用已锁定 jsonschema，沿用一次、
   计入原预算的修正；仍无效时返回固定 MODEL_OUTPUT_INVALID。Dispatcher
   复用现有预算/取消的提交结果保留路径，生成有明确模型失败原因的 partial，
   不接受无效模型文本，也不丢弃已授权 Fact/QueryResult/来源引用。界面分开
   展示缺口、状态原因、退化来源和“下一步只读检查（尚未执行）”。

无新队列、Agent 循环、模型客户端、依赖安装、迁移或 API 合同形状变更。
既有 v1 历史、身份/范围/时间及 R3/R4/R5 门禁保持。默认内网 Profile 不变。
新的解释归因不重写 evidence_only 状态；数据集摘要仅进入问数上下文。

## 最终语义与运行证据

[最终语义回执](r6/semantic-receipt.json)：

- 明确问数 completed，实际执行 incidents.records 查询，返回 3 incidents、
  正确冻结窗口和一个有效 Fact/QueryResult 引用。测试数据库含 3 条授权记录
  和 2 条范围外记录；这是实际 SQL、模型和 MCP 路径，业务事实为隔离 Fixture。
- 多轮沿用同一身份和冻结窗口，每轮实际提交历史/Finding/变更各一个新 Step；
  两轮 Step ID 无交集。缺失历史和覆盖资格继续 partial，三个来源原因均显示。
- 未创建新 Incident。模型建议没有当作已执行检查。

[最终运行](r6/runtime.log)七项顶层检查全部执行通过：冻结五用例观测收集、
实际模型/MCP烟测、部分状态原因、取消/预算/模型格式失败时数字保留、显式范围
修改/物理 TTL、历史/平台澄清及清理依赖闭包。数字保留三分项通过真实运行角色
提交固定的 7，再读取同一个 Fact 与摘要；该 7 是协议 Fixture，不作为来源准确性
证明。新格式失败分支也保持 partial 和 MODEL_OUTPUT_INVALID，原失败文本仍
由模型 Step 保存，不伪装成正常解释。

普通问数去掉资源覆盖参数后，[r4](r4/observations.jsonl)、r5 和最终 r6 三轮
都实际查询并通过原数值/窗口/引用检查；各轮 Profile 单独记录，不估算总体准确率。

[最终五例评分](r6/report.json)仍为 FAIL：zh-001 问数与 zh-005 澄清 PASS，
zh-002～004 因 partial 不通过，三项人工帮助性评审 pending。Node 场景标识
model_answer_incomplete，Pod 两轮为来源覆盖不完整。没有把合法 partial 改成
completed，没有放宽数值、时间、身份、引用或 partial 的评分断言。

评测器此前未登记已发布的 resource_history/resource_changes/evaluate_fault_chain，
会将合法只读工具误报为 non_readonly_tool。现在仅同步这三个封闭名称，单元仍
拒绝 execute_command；[旧 r4 评分](r4/report.json)保留，最终新评分仍 FAIL。
冻结五例问题、黄金值和 Suite 摘要未改。

## 定向检查

- [Go 三个相关包](go-unit.txt)：通过；包含固定错误类别和来源状态回归。
- [Python](python-unit.txt)：81 项通过，包括必需工具调用、真实缺参、拒绝
  重新读取、伪造身份不进入问数工具、失败回执及平台句柄拒绝；两个既有弃用警告。
- [前端](web-unit.txt)：6 项通过，[类型检查与编译](web-build.txt)通过；保留既有
  大 chunk 提示，未为本任务改打包策略。
- [评分器](scorer-unit.txt)：9 项通过；新旧数值与安全负例保持。
- [远程浏览器](browser/browser.log)、[报告](browser/report.json)：1 项真实 Chrome
  测试通过、无 skip，检查来源/模型/预算原因、格式失败提示和未执行建议，覆盖
  窄屏。这是声明的 UI transport Fixture，不是生产 OIDC/真实来源端到端验收。
- [制品与清理回执](r6/cleanup-and-artifact-receipt.json)：本地/运行 Python 摘要一致，
  Go 二进制摘要一致；密钥 0600 且未进入可导出记录，测试进程/助手测试容器结束。

## 未隐藏的失败与范围

- [r1](r1/runtime.log)：仅提示与预读不足以解决问数澄清；一次追问复制旧来源
  句柄被平台拒绝，原模型提案保留在远程 r1/rejected-proposal.json。
- [r2](r2/runtime.log)：问数已实际查询；Incident 解释先 length 截断、修正后
  多余 JSON 内容，[诊断](r2/incident-output-diagnostic.json)保留。不是数据源失败。
- [r3](r3/runtime.log)：模型提出了 execute 调用，但将数据集拼成资源 canonicalId，
  分配前被拒绝；反复尝试耗尽预算，没有实际查询。[参数](r3/query-tool-arguments.json)
  和[Ledger 摘要](r3/query-ledger-diagnostic.json)保留，随后收窄模型参数视图修复。

完整原始记录、受保护数据库和归档保留于远程
`/opt/ops-assistant-dev-20261012/execution-closure-20261016-rN`。
Git 日志仅去除行尾空白；未导出密钥或私有诊断配置。原长驻来源、模型及预览
保持，测试回环 HTTP 服务随测试关闭。功能 1～3 的开发和机械语义收口不等同于
中文帮助性、真实五来源非空联合场景、容量、发行、升级回滚或 V1.0 全面验收。
KubeVirt/CDI 延期，Trace/PyRCA 禁用。
