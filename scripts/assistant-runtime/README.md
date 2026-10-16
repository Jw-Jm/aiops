# 真实来源与运行机制验收

所有容器、数据库、模型、Kubernetes 和浏览器操作只在 `192.168.0.63` 执行。
本机可编辑、编译和运行无外部服务的单元/合同检查；不使用 OrbStack。
阶段边界见 ADR-0039、ADR-0040，模型回答质量不作为机制验收标准。

远程基目录 `/opt/ops-assistant-dev-20261012`，私有配置与协调目录为
`runtime-private`（0700），数据库、Bao、OIDC 密码和证书文件为 0600。
禁止把凭据、完整 kubeconfig、原始全集群资源或浏览器 bearer 日志提交到 Git。

`collect_live.py` 从专属 canary namespace 的真实 cAdvisor、Kubernetes UID/
容器状态和带时间戳的 Pod 日志采集，仅写入专属 VM/VL 后端；保留原始观测供
独立 golden 计数。`--output` 必须使用新目录，不能覆盖旧轮次。
Kubernetes 对象/容器发生变化时重新核对身份，不补零或伪造采样。

`run.py` 读取远程私有凭据，执行预先编译并复制的 Linux amd64 集成测试二进制
`runtime-stage.test`，每轮用新的 `--name`，日志独占创建。真实服务必须先启动。
当前 VM/VL、PostgreSQL、Bao、OIDC、Ollama 使用回环端口，固定版本/镜像来自
既有锁定材料；本轮不升级依赖、不生成发行 Bundle。

```sh
python3 scripts/assistant-runtime/run.py --source live-source-20261013-r3 \
  --name sources-new-round --tests '^TestAssistantActualCollectedSourcesAndCapacity$'
```

实际进程接管测试 `TestAssistantActualProcessKillAndTakeover` 使用子进程
SIGKILL、过期租约和替代 Worker，验证已提交 Step 复用、旧 generation 围栏。
子进程的上下文工具事实明确为机制 Fixture，不能当作生产 HA/灾备验收。

DeepFlow 部署与 1 GiB Agent 默认值见 [开发部署说明](../deepflow-dev/README.md)。
Controller/Querier 仅经远程环回 port-forward，真实采集用两个有 ownerRef 的
HTTP canary Deployment，既有 core/final namespace 不参与写入。
运行实际签名 MCP 测试需设置：

```sh
export OPS_ASSISTANT_DEEPFLOW_GRAPH_ONLY=1 OPS_ASSISTANT_MANUAL_MCP=1
export OPS_TEST_ARCHIVE_HOST_NETWORK=1
export OPS_TEST_ARCHIVE_IMAGE=sha256:31d61f5e8771cbd5993912cd051be0c7bcdc207faaa12c50e1a3b8371631c927
export OPS_ASSISTANT_DEEPFLOW_MCP_REPORT=/opt/ops-assistant-dev-20261012/deepflow-new-report.json
python3 scripts/assistant-runtime/run.py --name deepflow-new \
  --tests '^TestAssistantActualDeepFlowSignedMCP$'
```

Archive 使用独立 TLS/IAM 依赖 Fixture，数据、身份及精确 bind storage 保留，
不据此宣称生产归档准入；测试结束只停止进程。MCP 使用真实 API/Worker
数据库角色、官方 SDK、mTLS、Bao 注册签名、真实查询和持久 Step。

`web_gateway.py` 代理真实 OIDC 和动态 API 地址并提供构建后的静态 UI。
浏览器测试运行于远程既有 Chromium CDP，不注入令牌、不 mock 路由。
Node 必须通过 `NODE_EXTRA_CA_CERTS` 信任任务根 CA；不忽略 TLS 校验。
启动 `TestAssistantActualOIDCAndBrowserRuntime` 后，用远程 Playwright 执行
`assistant_runtime.spec.ts`。API 写入随机 runtimeId，回执必须匹配本轮，
不得复用旧 profile 相同的回执。验收真实 PKCE、角色隔离、持久轮次、SSE、
刷新恢复和 L4/L7 能力显示，答案仅校验协议与引用合同。

失败轮次保留。来源无有效证明、过期、撤销或实际身份变化时如实不可用；
测试未运行、Fixture 通过和配置开关不能替代真实运行通过。


### 机械盘阻塞时的同版本工具路径

远程 `/data` 是旋转介质，浏览器与 Node 原工具路径会受其读取延迟影响。
2026-10-14 追加验收将既有 Chromium 1248 和 Node v24.21.0 复制到
`/opt/ops-assistant-browser-20261014`（NVMe），没有下载/升级依赖。
浏览器仍以 `ops-sp08-qa` 非 root 用户运行，实际 TLS 校验保持开启。
Ubuntu 的 user namespace 策略要求将 `chromium-apparmor` 安装到
`/etc/apparmor.d/ops-assistant-qa-chromium` 并用 `apparmor_parser -r` 加载；
它复用原 QA 浏览器的 userns 许可，仅匹配新工具的精确执行路径。
不要使用 `--no-sandbox` 或全局关闭 AppArmor/userns 限制。

```sh
# 仅远程执行；先复制现有工具并配置精确路径的 AppArmor profile。
runuser -u ops-sp08-qa -- env HOME=/data/ops-sp08-20261011/browser-home \
  /opt/ops-assistant-browser-20261014/chrome-linux64/chrome \
  --headless --remote-debugging-address=127.0.0.1 --remote-debugging-port=19226 \
  --user-data-dir=/dev/shm/ops-assistant-browser-20261014-nvme/profile \
  --disk-cache-dir=/dev/shm/ops-assistant-browser-20261014-nvme/cache \
  --noerrdialogs --no-first-run --ozone-platform=headless --use-angle=swiftshader-webgl
```

Playwright 设置 `OPS_WEB_QA_CDP=http://127.0.0.1:19226`，使用复制后的 Node。
该浏览器仅用于本专项；收尾按 PID/执行路径/profile 核对后停止，保留日志。

### 运行关联 v2 的同范围来源复验

固定收集入口仍受 `faultChainEnabled` 控制，新请求为 `fault-chain-request/v2`，新任务目录为 `assistant-tools/v2`。先应用增量迁移并同步 API、Go/Python Worker 和 UI；保留旧任务、v1 归档与失败记录。回滚边界见 [开发记录](../../docs/plans/v1.0-operational-analysis-progress.md)。

`collect_live.py --canary-set fault-chain` 只允许既有开发 namespace `ops-assistant-deepflow-20261013` 及固定 `fault-chain-20261014` 标签，复用相同 cAdvisor/UID/日志采集器。新目录保存实际观测，至少采满既有的 60 次/5 分钟门禁。控制 HTTP 流量安排在采集窗口后段，使实际产生日志的服务端 UID 在末分钟内有正样本；闲置客户端没有日志，不能拿它代替服务端正样本验证，也不能伪造日志。

远程测试设置 `OPS_ASSISTANT_OPERATIONAL_SOURCES=1`，使原 native Graph runtime 在同一冻结范围中接入实际 VM/VL 与 DeepFlow；实际模型解释用 `OPS_FAULT_CHAIN_MODEL=1`。`TestAssistantActualFaultChainWithoutModel` 的收集阶段仍断言模型调用为零，后续解释有独立 Job。所有 source qualification 仍需实际正样本、范围隔离和已冻结 proof；无数据保留缺口。

新增历史界面分项使用 `web/tests/e2e/operational_history_runtime.spec.ts`，需要本轮实际 partial 轮次的 `OPS_OPERATIONAL_HISTORY_TURN`，验证缺失历史不被回填、冻结窗口/UID、安全 Step 定位、刷新保留和所有者隔离。其回执显式标记 `networkOracleAcceptance=false`；原 `fault_chain_runtime.spec.ts` 的独立 HTTP 计数断言继续保留，两个结果分别报告，不互相代替。


### 模型解释的必需上下文

自然语言助手任务先通过同一已发布 `get_assistant_context` MCP 工具、Job/Step 分配和结算读取冻结输入，再进入原 HolmesGPT 循环。恢复时复用同 Job 已提交上下文；读取失败保留失败 Step，模型不启动。不能把 provider 接受 `tool_choice` 参数当成模型实际完成了工具读取。旧 Incident v2 路径和 evidence-only 收集入口保持原职责。

新模型答案若返回 `unsupported` 且没有说明缺口，服务端将其标为 `partial`，添加 `model_unsupported_reason_not_provided` 和 `model_explanation_incomplete`，明确说明模型未提供可用解释；不能作为正常诊断完成。非法事实引用、身份和来源门禁不变。旧历史结果读取不变。预读机制、模型正常结束、中文解释质量和版本验收分别记录，提示词或校验改变不等于质量通过。


模型展示层对上下文来源资格作无损分组，保留各操作的独立有效期；校验摘要在已提交 Step 中保留，解释输入不重复携带。事实、窗口、质量、来源 revision、QueryResult/Fact/Evidence 引用不变，固定收集器和 UI 仍使用原始结果。其目的是在已冻结模型上下文上限内工作，不以扩大上下文或放宽来源门禁解决问题。
