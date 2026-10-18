# 五来源独立页面与进程恢复复验（2026-10-18）

**结果：新三窗口来源对账、真实 DeepSeek 联合验收及实际进程故障恢复通过；独立页面复验未通过。** 页面首次资源解析 503，第二次解释成功、追问在 get_assistant_context 超时，未调用追问模型。不宣称本轮独立页面闭环完成，也不拼接此前页面通过结果。

用户追加允许远程性能/资源问题只记录、不处理。本轮未调整机器资源、服务阈值、图租约或来源资格；页面的 watch/lease/上下文超时如实保留，不再追加调优或重跑。未进一步证明其底层机器性能根因。

## 环境、制品与范围

通过 `ssh mssc-direct` 在 `/opt/ops-five-source-reverify-20261018` 运行，目标为 `ops-assistant-deepflow-20261013` 的专属 Pod canary。原 tenant/source 标识复用前重新核对实际 cluster/namespace，所有观测均重新采集，未用过期 r6 数据填充结果。

Go 验收二进制与生产 dispatcher 从当前工作树交叉编译；Python 同步到专属快照。联合、页面和进程恢复使用同一二进制/Python 与第三轮冻结窗，见 `artifact-binding.json`。此前两轮探针制品另存 `artifact-binding-r1.json`、`artifact-binding-r2.json`。真实模型为 deepseek-flash，验收 Profile 1M/8192、120s；密钥只引用远程受保护文件，不导出。内置模型配置未冒充 1M。

## 重新采集与探针前提修复

三轮各采集 11 分钟；冻结三窗口在至少 340 秒真实采集后才启动，覆盖 Victoria compare 的五分钟回看。第三轮 `live-source-r3` 为 132 周期、3168 指标、2512 日志，0 错误、0 身份漂移丢弃。完整覆盖门禁见 `source-gate.json`。

- 首轮 HTTP 1000/10、1000/50、1000/0 和 TCP 失败观测 0/100/0 均吻合，但基线 L4 总体为 962，故 FAIL（`windows-r1.json`）。D1 端口元数据独立复核：1000 HTTP 响应只有 962 个不同源端口，38 个端口各出现两次，恰与 L4 记录数一致。
- 第二轮只加 5ms 请求间隔，仍 FAIL：基线/故障总体已吻合，恢复窗 1000 HTTP、986 源端口、986 L4，14 个端口复用。见 `windows-r2.json`、`stimulus-port-diagnostic-r2.json`。限速本身未被宣称解决根因。
- 原刺激未保证不同 TCP 四元组，不能直接假定一次 HTTP 请求对应一条 L4 记录；不能把上述不一致都归为来源丢观测。修复仅在专属探针：每个 HTTP/refused 刺激先绑定本轮未用的源端口，并持有 socket 至连接结束；每窗额外要求 uniqueClientPorts=1000+refused。保留 5ms 间隔降低峰值，请求总数、错误比例、三段 60 秒窗口、观测点、精确总体及复读判据均不变。
- 新增重复端口判据回归先失败（`unique-ports-before-fix.log`），修复后探针 7 项通过（`probe-tests.log`）。
- **第三轮完整 PASS**：HTTP 响应均 1000，错误 10/50/0；TCP 失败观测 0/100/0，总记录 1000/1100/1000，一次轮询、11s 确认、stable=true、0 来源错误。独立 D1 查询再确认每窗 HTTP 不同源端口均 1000，TCP 不同源端口为 1000/1100/1000，均与刺激和记录一致（`stimulus-port-diagnostic-r3.json`）。

联合和页面共用 `fault-window-r3.json`：它只投影完整已通过三窗口 oracle 的故障段，包含原报告 SHA256、原生 Pod 身份和稳定性标记，不是从失败轮拼接出的 PASS。当前时间可靠性仍为 false；exact 仅代表声明观测总体内的计算，不代表所有用户请求或唯一失败连接，也不证明因果。

## 联合验收：PASS

`joint-r2` / `TestAssistantActualFaultChainWithoutModel` PASS（97.78s）。5 QueryResult、6 Evidence；HTTP fault/recovery 与 TCP fault 独立对账通过，Victoria 对照通过。撤权和不同主体访问拒绝通过。

真实模型解释/追问各一次成功响应，终态 diagnosis/partial、错误码为空，Fact 引用 16/15；输入 25432/29399、输出 2868/2780，均 finish_reason=stop。解释无新增来源读取；追问 history/findings/changes 恰好各一次新提交读取，UID/窗口不变，预留归零。见 `joint-r2-runtime-report.json`、`joint-ledger-verification.json`。

本轮联合用例未启用进程恢复，不将其 PASS 冒充故障恢复证明。首次 `joint-r1` 在 2.99s 因恢复目录漏复制 deepflow-domain.json 失败；核对原文件与 Controller 身份路由并补齐后重跑，非性能问题。另有一次门禁脚本错误将 previousMeasurements 当作确认读数组：探针首轮轮询即稳定时该历史数组为空；门禁改用实际 stable 判定及独立 D1 复核，保留原失败日志。未删除失败或放宽统计判据。

## 独立页面：FAIL，按用户要求留档

真实 OIDC、API/Worker/MCP/Holmes 路径，无 route mock，不绕过 TLS。临时 API 使用本轮全新回执目录，未引用旧 browser-receipt 作为本轮通过证据。

- `browser-ui-r1`：资源解析收到 503；同时有 watch_disconnected、lease_before_query，耗时约 3004ms，未进入模型。
- `browser-ui-r2`：解析成功（lookupAttempts=2），完成采集与解释；解释为有效 diagnosis/partial，13 Fact 引用，一次成功模型响应、正常 stop。追问在 get_assistant_context Step 返回 SOURCE_TIMEOUT，同时记录租约读取超时、STALE_CONTEXT，turn=failed/INVESTIGATOR_FAILURE，追问模型调用为 0，三项补证尚未执行。
- 全部预留归零（`browser-failure-ledger.json`）。UI 第二轮最终等待超时而 FAIL，已向 Go harness 提交明确 passed=false 的回执使其停止；`browser-r1` 因该失败回执退出，不宣称通过。日志、退出码与 `browser-failed-receipt.json` 留存。

本轮未完成追问后的最终页面/隔离闭环。2026-10-17 完成方 r11 的历史通过证据保持原状，不能代替本轮独立复验。此处仅记录具体超时/可用性现象，未作机器资源根因确认或服务调优。

## 实际进程故障恢复：PASS

`process-recovery-r1` PASS（103.14s），独立于页面用例。启用生产 dispatcher OS 进程入口，实际 SIGKILL dispatcher 和 investigator，等待真实 45 秒租约到期后接管。

报告 processRecovery=true、killedGeneration=1；数据库 fencingEpoch=2，get_assistant_context 成功提交仅一次，旧代完成请求被 ErrLease 拒绝。此轮禁用模型、modelEndpointOffline=true、modelSteps=0，预留归零。见 `process-recovery-r1-runtime-report.json`、日志与 `recovery-ledger-verification.json`。这验证恢复机制，不据此声称所有统计/模型矩阵均通过。

## 回归、恢复与复现入口

- Python pytest 91 项通过；标准 unittest 40 项；新增端口判据后探针 7 项通过。DeepFlow/MCP Go 包通过；命令与对应阶段见 `local-checks.json`，原始日志按文件留存。未声称本轮重跑了此前列出的所有 Go 包或完整 build/vet 矩阵。
- 运行器：远程 run-fault-chain-joint.py，最终参数 sources=live-source-r3、http-proof=reports/fault-window-r3.json、windows-proof=reports/windows-r3.json。联合测试名如上，页面为 TestAssistantActualOIDCAndBrowserRuntime。
- 私有运行目录需要 identities.json、deepflow-domain.json（0600）；临时 api-address.json 由运行器产生，不是固定部署地址。页面加载已有真实 UI、根 CA、远程 CDP 和独立 OIDC fixture。进程恢复加 OPS_ASSISTANT_DISPATCHER_BINARY 指向本轮 dispatcher，并设 OPS_FAULT_CHAIN_MODEL=0，取消外部 key 环境变量。
- 临时 API 已停止，独立 OIDC fixture 已停止但未删除；原网关命令恢复，原 UI 与原 OIDC discovery 均 200。新恢复的 DeepFlow 环回转发保留，未改共享 API/IdP 或生产配置。见 `environment-restoration.json`。
- 私有浏览器原始日志、JWT、密码和密钥不导出。失败数据库/归档、原始采集及各轮失败均保留。

未覆盖新增 Node/PVC 实时矩阵、满 1M 容量和人工模型质量评测，不代表 V1.0 全面验收或生产发布。KubeVirt/CDI 延期、Trace/PyRCA 禁用不变。
