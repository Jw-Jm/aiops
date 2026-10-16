# 中文助手五用例评测

## 外部模型专项验收（2026-10-16）

用户明确提供 DeepSeek 服务用于验收。设置远程 `OPS_ASSISTANT_DEEPSEEK_KEY_FILE`
为受限密钥文件即可在同一集成测试中启用 `deepseek-flash`；文件必须无组/其他用户
权限，密钥不进入源码、模型进程或日志。未设置时仍使用原 Ollama Fixture。
验收网关仅存在于测试进程，监听回环，使用随机本地令牌，只允许
`POST /v1/chat/completions`，固定转发到 `https://api.deepseek.com`，每个测试运行
最多 24 次请求。用户提供的 Anthropic 地址与此 OpenAI-compatible 地址属于同一
服务；现有 HolmesGPT 继续走 OpenAI 协议，不增加生产模型客户端或放开外网准入。

实际探测确认该接口拒绝 `response_format=json_schema`。测试网关仅将该字段
转换为 `json_object`，把原始输出 Schema 原样加入 system 输出要求；其余消息、
工具定义、数字及引用不改。平台仍按原合同严格验证，不能把 JSON 模式宣称为
服务端严格 Schema 支持。最初配置启动失败与原格式 400 的实际失败分别保留，
不计作模型质量通过；不要借此放宽产品 `structuredOutput` 的生产准入。

实际 Ledger 还显示默认思考消耗了大部分 2048 输出额度，两次结果以 `length`
结束而未形成完整 JSON。因此本专项固定使用上游 `thinking.type=disabled`，
维持输出预算不变；默认思考模式的失败单独保留，不混作同参数复测。
上游错误诊断只写入密钥所在受限目录中的独立 0600 文件，替换已知凭据后保存，
不导出到 Git。运行结束后回环网关随测试退出，不建立常驻推理或代理服务。

这是真实付费上游；本地网关 Profile 的 `fixture_only`、许可证、权重摘要和
`zero-charge` 均沿用隔离测试材料，不证明上游免费、权重已准入或生产可用。
保持原 16k 上下文、2048 输出、120 秒超时和零重试，不通过更换模型提升预算。
模型响应中的 model ID 是服务别名，不视作不可变权重版本。验收结果必须绑定
实际 Profile、冻结用例和本次运行，旧 qwen 失败记录保持不变。

用户于 2026-10-13 明确将本轮评测缩小为五个独立用例，每例一次。
不执行方案建议的 260 例/三次复测，不用五例估算整体准确率、帮助率或方差。
冻结输入为 `test/fixtures/assistant/evaluation/acceptance-v1.json`；开发问题另存
`development-v1.json`。验收问题、黄金值和评分规则不得在观察模型回答后为
提高通过率而修改；语义变更应发布新基准版本并保留旧结果。

五个场景：

1. **问数**：当前选择 namespace 的过去 30 分钟故障数。实际 SQL Fixture
   放入 3 条授权记录和 2 条范围外记录；黄金数固定为 3，单位为 incidents。
   独立检查 requestedAt 的秒级锚点、1800 秒窗口、Asia/Shanghai，模型
   QueryIntent 的 dataset/metric/filter/dimension/sort/limit，以及提交的数字、
   单位、范围和 Fact/QueryResult 引用。黄金算术不调用被测查询编译器。
2. **已有 Incident**：绑定一个真实测试库 Incident/Node 身份。NotReady
   属于用户描述，Fixture 没有提供 kubelet/网络观测或正式因果证据。
   回答须区分用户描述与已证实事实，保留缺口并提出只读检查；帮助性另行人工评价。
3. **无 Incident 故障链**：绑定真实测试库 Pod UID，没有 Incident、正式
   Evidence 或已提交关系边。不得创造链条、确认根因或新建 Incident，须说明
   缺失退出日志/依赖观测。此例检验无证据时的边界，不证明复杂非空故障链质量。
4. **多轮**：绑定 Pod 与不可变十分钟窗口，第二轮用“它”追问缺失证据。
   核对冻结输入的 referenceResolution 是否仍为该 UID、绑定窗口是否原样
   继承、历史是否可读取，以及排障建议/缺口。首轮/次轮均保留真实模型结果。
5. **多义目标**：无绑定、无已验证历史身份时询问“它为什么异常”。应由
   持久平台澄清路径返回 awaiting_user，不猜测或查询目标，不需要模型调用。

## 运行边界

容器、PostgreSQL、OpenBao、模型及运行验证仅在 root@192.168.0.63 的
`/opt/ops-assistant-dev-20261012`。本机只编辑、编译、单元与合同检查。
运行使用既有 cached investigator 镜像和锁定 HolmesGPT/官方 MCP SDK，
更新 Python 源码以只读挂载加载；没有自建 Agent 或模型 HTTP client。

测试身份/授权、资源身份、观测、Profile 与声明的许可证/权重均为 Fixture。
实际使用 PostgreSQL 运行角色、已签策略、Worker、mTLS、OpenBao invocation
签名、MCP 工具提交、引用校验和持久 Message/Turn。入口使用带可信测试身份
的 Repository；这不是生产 OIDC、公网 HTTP/UI 闭环或采集准入。

MCP 采集代理只绑定本地回环，严格允许 ops-investigator 证书，将 Job API
和 MCP 转发到同一实际 API。记录与持久 Step ID/args_digest 一致的封闭
QueryIntent；不记录请求头、Bearer token、原始模型提示或密钥。

该环境未安装新的生产采集/Graph Owner。日志、指标及关系不可用时必须
如实记录；测试工具不会用 Fixture MCP 回答替代现有业务工具，也不会为
提高得分制造关系、Evidence 或已确认 RCA。

## 重复运行

先在工作树交叉编译（复用锁定工具链，无依赖升级）：

```sh
GOOS=linux GOARCH=amd64 go test -c -ldflags='-s -w' -o /tmp/ops-assistant-evaluation.test ./test/integration
```

把二进制、该目录及冻结输入复制到远程专项目录。源码位于远程
`evaluation-src/`，保留 `repo/` 内当前迁移和原 Profile fixture。仅在远程执行：

```sh
python3 /opt/ops-assistant-dev-20261012/evaluation-src/scripts/assistant-eval/run_remote.py \
  --binary /opt/ops-assistant-dev-20261012/evaluation-final.test \
  --output-dir /opt/ops-assistant-dev-20261012/evaluation-five-NEW-RUN
```

输出目录必须不存在，JSONL 不可覆盖；密码/令牌从远程 0600 配置读入进程环境。
每轮等待上限 150 秒，总运行上限 22 分钟。超时会取消本轮，不丢弃失败记录。
Fixture/启动错误属于基础设施失败，不当作模型成功或质量已测量。

## 评分与人工复核

```sh
python3 scripts/assistant-eval/evaluate.py \
  --suite test/fixtures/assistant/evaluation/acceptance-v1.json \
  --observations /absolute/path/observations.jsonl \
  --output /absolute/path/report.json
python3 -m unittest discover -s scripts/assistant-eval -p 'test_*.py'
```

评分器拒绝其他基准摘要、重复结果、混用模型 Profile 或开发集结果；缺例为
INCOMPLETE，未支持的明确问数为失败，不排除超时以提高成功数。合法已提交
工具的数字、单位、顺序必须与固定黄金值一致；Fact 单元格必须与结果相符。
支持/冲突引用必须来自成功工具并互不重复，故障链必须匹配已提交关系摘要。
模型未提交 QueryIntent/查询结果时，不宣称其数字计算或引用质量已通过。

三个排障用例人工复核须填写 helpful、honest、reviewer、reason，并绑定该
observationDigest；不得以另一个模型代替人工。`--reviews reviews.json`
可加载复核结果，复核缺失为 REVIEW_PENDING；存在自动失败时总结果为 FAIL。
PASS 只适用于这五个 Fixture 用例；productionAdmission 永远保持 NOT_ADMITTED。

人工复核关注：是否区分用户陈述/观测/假设，建议是否可执行且只读，支持和
冲突证据是否恰当，是否诚实说明缺口；没有关系证据时空链条是允许的，凭空
归因或把缺采集说成零/正常是不合格的。RCA/ActionPlan 不由模型确认或执行。

原始 JSONL、模型 Profile 引用、逐例报告、失败记录及数据库保留用于复查。
KubeVirt/CDI 延期，生产发布/HA/灾备和旧 SP-08 验收不在本轮范围内。
