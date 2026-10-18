# 独立复验结论核对与修正版（2026-10-18）

后续经用户授权的实际执行结果见 [2026-10-18 复验记录](../../five-source-reverify-20261018/README.md)：联合及真实进程恢复通过，独立页面因超时未通过；本文件保留此前只读核查时的结论。

本次通过 `ssh mssc-direct` 只读核对源码、远程留存报告、数据库结果及环境状态；没有重跑模型或页面，也没有重建验收环境。

## 可替换的结论

**专属 Pod canary 的五来源联合机制闭环，包含真实 DeepSeek 模型解释与追问，已由独立 r21 运行通过。页面闭环有完成方的真实通过证据；独立页面重跑尚未完成，当时缺少运行中的配套验收环境，不能据此宣称独立页面验收通过，也不能仅凭环境阻断排除全部代码问题。**

### 独立联合运行

远程 `/opt/ops-five-source-20261017/joint-r21-runtime-report.json` 与日志确认 `TestAssistantActualFaultChainWithoutModel` PASS（89.47s）。报告完整 SHA256 为 `8ee9dc60bfdd444176c30c3fef92e6d0a635a3ccf2f8a3041cb62fa87bd77e53`，本地留档为 [independent-r21-runtime-report.json](independent-r21-runtime-report.json) 和 [independent-r21.log](independent-r21.log)。此 r21 与 closure 目录此前失败的 output8k-r21 是不同运行，不混用编号。

- 已提交 5 个 QueryResult、6 条 Evidence 引用。数据库结果 `exact=true`：HTTP 故障对照 1%→5%（增加 4pp），恢复对照 5%→0%（减少 5pp）；TCP 失败观测 0→100；Pod 重启增量 baseline/current 均为 0，logs.records 查询计数为 0。
- `exact` 表示声明范围、观测总体内的计算精确性。网络结果仍保留事件时间不可靠、观测总体不等于全部用户请求、TCP 失败观测不等于唯一失败连接数等警告。受控 canary 发起了 100 次 refused 场景，不能将查询指标泛化为 100 个唯一失败连接；日志查询的 0 也不能证明应用没有异常。
- 解释轮 deepseek-flash：1 次调用、1 次成功模型响应、1 次上下文读取，无新增来源读取。追问轮同样 1 次成功响应，冻结身份和故障窗口保持，resource_history/findings/changes 各新提交 1 次读取。两轮均为有效 diagnosis/partial，错误码为空；未声称确认因果。
- 撤权后的访问拒绝与不同主体访问拒绝断言通过。**本次未运行进程故障恢复分支：`processRecovery=false`、`killedGeneration=0`，不得写成“进程恢复随用例通过”。**

验收密钥文件当前权限核对为 0600；本次核查未输出明文。未进行对所有历史日志的穷尽检查，不作“任何日志绝未输出”的保证。

### 增量改动与验证边界

源码确认两类改动：LiveMapper 的 beginSummaryPair/finishSummaryPair 上限由 2500ms 调至 3500ms，refresh 由 2500ms 调至 6000ms，仍受父 context 截止时间约束；解释/追问测试观察等待上限均改为 480 次一秒轮询，原断言保留。live.go 当前 diff 为增加 9 行、删除 3 行，即 12 行变动，不是新增 12 行。

独立 r20 日志确有 `native_after elapsedMs=2501 contextDone=true`；r19 三次 scopeUnverified 中并非全部标记 deadlineExceeded。延长时限后 r21 通过，支持改善负载单机时间余量的判断，不能证明所有 scopeUnverified 都来自相同超时，也不等同于所有改动均有确定性回归“旧代码必失败、新代码必通过”。

480s 比原等待上限更宽裕，但不是所有允许调用序列的最坏情况覆盖：默认预算允许 8 次模型请求，单次 deadline 为 120s，另有整轮时间预算约束。应写“延长测试观察等待”，不写“已覆盖最坏情况”。

删除临时 api-address.json 属于运行环境清理，不是第三项代码缺陷修复。所列 Go 包、Python 91 项、探针 8 项、build/vet 的通过数量在本次核对中未重新执行，也未取得对应增量运行的完整日志绑定；这些数量暂作为提交方报告，不冒充本次独立确认结果。

### 页面通过证据与独立重跑状态

完成方最终 `browser-output8k-ui-r11` 与所属 `browser-context1m-output8k-r3` 均通过。当前 [browser-receipt.json](browser-receipt.json) 对应这一轮：6 QueryResult、2 Evidence、HTTP oracle 1000/50，真实 OIDC、解释与追问、重载、跨主体隔离、键盘和窄屏检查通过；解释/追问成功模型响应为 2+1，最终 Fact 引用为 13/8。r10 是先前另一轮，不能把其结果与 r11 拼成同一次运行。完成方联合 r22 另有独立报告通过。

独立页面重跑未完成。临时 API 已退出、closure 网关未运行、18681 临时 OIDC 已停止、api-address.json 已删除，与完成后的恢复记录一致；原网关已恢复，并非所有服务或验收材料被拆除。两个共享实例的 issuer/令牌不匹配及其 401 应作为提交方当时的诊断记录，不当作本次重新验证的事实，也不足以单独证明无代码缺口。

现有 run-fault-chain-joint.py、web-gateway-closure.py、受保护身份配置及已停止但未删除的独立 OIDC 容器仍在。临时 API 地址由启动验收运行器后生成，不是必须向完成方索取的固定部署地址。后续独立页面复验可以基于这些材料恢复专属环境；ADR-0042 规定关系读取设计，不是环境启动手册。无需据此修改共享实例，重建耗时也不保证为 30–60 分钟。

### 时效与尚未覆盖项

2026-10-18 本次核对时，远程 18643、19228 可连接，21416/21417 与 18681 未监听；因此“DeepFlow 端口转发保持”不能作为当前状态。19228 可连接也不能替代本次浏览器测试通过。

r6 三个窗口为 **2026-10-17 12:20:10–12:23:10（Asia/Shanghai）**。按此前 6 小时保留配置推算，各窗口约于 **2026-10-17 18:20–18:23** 超出保留期；这是历史预计时间，不是 2026-10-18 尚未到来的截止时间，也不是对实际清理时刻的精确保证。新的完整实时重跑应先确认来源可读性；无法读取旧窗口时重新采集及生成 oracle，不得把空结果解释为观测到零。

结论限于上述专属 Pod 和本轮机制验收，不覆盖新增 Node/PVC 实时矩阵、进程故障恢复、满 1M 容量或人工模型质量评测，也不代表生产发布或 V1.0 全面验收通过。
