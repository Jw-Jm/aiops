# V1.0 运行数据关联分析：本轮证据

日期：2026-10-15；环境：192.168.0.63 独立开发环境。基线 main `53b0b9e91dd32d71d955ec940929952f8434530b`。本目录区分功能实现、真实来源、模型质量与版本验收，不代表生产发布。

## 功能与合同

- [Go 单元与合同](go-unit-contract.txt)：HTTP、investigation、工具合同、assistant、analytics、app、资源仓库及 contract 通过。另有定向 `go vet` 通过。
- [Python](python-unit.txt)：61 项通过；[固定收集器](python-fixed-collector.txt) 13 项、[采集工具](python-collector.txt) 2 项定向单元检查通过。
- [前端单元](web-unit.txt)：21 文件、68 项通过；[TypeScript/Vite](web-build.txt) 通过，仅保留现有 chunk 大小提示。
- [成对身份复核](pair-fence-unit.txt)、[race 检查](pair-fence-race.txt)：独立快照不等待后台共享映射锁，全部原有身份替换/撤销门禁继续通过。
- [生成合同](generated-clients.txt)：`make check-generated` 通过。
- [迁移与历史](history-runtime.log)：真实 PostgreSQL 运行角色执行迁移 43；Pod/Node/PVC 有界读取、半开窗口、不可变 Finding 修订、版本与字段差异、Watch 缺口、来源撤销、取消/预算保留、清理引用闭包、旧 Worker v2 隔离通过。此日志的合成异常只验证机制，不代替真实故障资格。
- [生命周期](mechanisms-runtime.log)：既有显式范围、模型失败/预算与取消保留、进程接管、保留闭包及 v1 兼容路径本轮复验。
- [真实 Kubernetes](kubernetes-runtime.log)：生产投影/SyncSnapshot 路径写入真实 Pod、Node、PVC 观察；测试仅创建并删除独立开发 namespace 内本次拥有的 PVC，未伪造 Node 故障或回填过去。

## 五条真实来源

[实际采集回执](collection-receipt.json) 对应远程 `operational-five-source-r2-20261015`：63 周期、1512 指标样本、600 条实际日志、0 采集错误。VictoriaMetrics/VictoriaLogs 与 Kubernetes、DeepFlow 使用同一租户授权范围。

[独立 HTTP 证据](deepflow-source-proof.json)：实际 1000 个响应、50 个 503、0 传输失败，冻结窗口与观察点 `s-p`；后续健康请求仅用于刷新实时拓扑，不进入冻结窗口。

[五来源运行报告](five-source-runtime.json)、[原日志](five-source-runtime.log)：同一固定收集主路径提交 5 个 QueryResult（重启、内存、日志、HTTP 错误率和耗时）、2 个归档 Evidence、8 个资源来源 Step；收集阶段模型调用为 0。来源按身份、授权与窗口资格执行，不把接口 200 当作语义证明。

后续独立解释通过真实内网 OpenAI-compatible 模型、HolmesGPT 和官方 MCP：观察到 2 次模型调用及 1 次已提交上下文读取，原证据保持不变。150 秒观察结束时解释仍为 running，`chineseQualityAcceptance=false`；只证明调用/工具读取和证据隔离，不证明完整中文回答质量。

## 真实浏览器分项

[历史界面日志](history-browser.log)、[本轮回执](history-browser-receipt.json)、[实际 API/Worker 日志](browser-api-runtime.log)：真实 OIDC PKCE 后读取已提交 partial 轮次，校验原 UID、绝对冻结窗口、空历史的明确缺口、原始 Step 定位、刷新与解释草稿后上下文不变；键盘、窄屏及跨所有者读取 context/Step 拒绝通过。`networkOracleAcceptance=false`，该结果仅覆盖新增历史/缺口界面和隔离，不能代替联合网络用例。

[新增独立 HTTP 计数](browser-deepflow-proof.json) 再次达到实际 1000 响应/50 错误。浏览器 r10 的[已提交网络结果](browser-network-committed.json) 包含 2 个网络 QueryResult、2 个 Evidence、5 个来源 Step，原始步骤抽屉已显示；该轮因关闭按钮测试选择器仍使用英文而中止，随后修正为中文/英文可访问名称。

但[联合网络浏览器末次失败](network-browser-failed.log)仍保留：部分后续轮次因实时来源/端点复核或关系证据不足，没有提交网络统计。未改为接受空统计，未删除断言，也未把分项历史界面通过计为联合网络通过。此独立环境的持续联合运行稳定性尚未验证通过。

## 失败、受限与未验收

[尝试索引](attempts-index.json) 保留本轮远程路径、状态与摘要；失败日志、受保护数据库和归档夹具仍留在独立环境，不因后续通过而删除。认证材料和诊断中的授权头不进入 Git。

- 早期迁移/测试授权夹具、网络结果空 canonicalId 合同拒绝已修复，相关失败保留。
- 旧 Victoria 窗口已过期；额外 CPU 五分钟回看超出本次采集覆盖，严格返回 SOURCE_UNAVAILABLE。没有降低来源门禁；完整容量矩阵未通过/未宣称通过。
- 一轮新 HTTP oracle 检测到与健康刷新重叠的 12 个额外响应，严格记录为失败；重新生成无重叠窗口，没有改计数期望。
- 浏览器早期失败包括独立采集租户与 OIDC 原绑定不一致、测试端证书链配置、DeepFlow 转发中断导致的来源退化。均未修改登录授权或放宽来源规则。
- R3/R4/R5 输入路径完整，但当前实际场景不满足全部共同依赖、可靠变更事件时间或完整恢复资格；保留明确缺口，没有把共现、模型推测或指标改善升格为因果/恢复结论。
- 归档、租户与 PKI 等依赖使用明确的隔离运行夹具；不代表生产归档、HA、灾备或发行物料准入。未重跑无关全量验收，未进行生产发布。
- 中文模型质量、全容量矩阵、最新源码发行物料及升级回滚和 V1.0 全面版本验收仍须分别完成。KubeVirt/CDI 延期，Trace/PyRCA 维持禁用。
