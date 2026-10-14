# 助手真实来源与 DeepFlow 开发验收证据

验收环境仅为 `192.168.0.63` 的独立开发实例；日期为 2026-10-13～14。
依据 ADR-0039、ADR-0040。模型回答质量不作为机制验收标准，五例质量报告
仍保持 FAIL；不代表生产准入、HA/灾备、全协议或正式因果诊断质量通过。
Trace 禁用，KubeVirt/CDI 延期，core/final 发布环境未修改。

## 已通过的有界验收

- `sources.json`：原生 cAdvisor/Kubernetes/容器日志采集，窗口聚合、趋势、
  比较、样例脱敏、来源撤销。100 次查询并发 4；P50 3.76ms、P95 5.85ms
  仅代表两个开发 Pod、最长 60 秒查询窗口，不推断生产容量。
- `mechanisms-first.log`：会话生命周期、真实子进程 SIGKILL/租约接管、旧
  generation 拒绝、已提交步骤复用、显式范围及物理 TTL 通过。此文件整体
  FAIL（首次来源查询失败），后续来源修复以 runtime-sources-r7 为准。
- `browser.json`、`oidc-runtime.log`：原 VM/VL 场景真实 Keycloak PKCE/RS256、
  数据库角色、API/Worker、持久轮次/SSE、刷新和管理员隔离通过，非接口模拟。
- `deepflow-live.json`：实际原生 UID/容器身份映射、六项 L4 查询、HTTP
  200/503 L7 元数据、范围/来源撤销。路径只标记 L4 不完整观测；不返回
  请求参数、头、正文、SQL，不声明 Trace。
- `deepflow-mcp.json`、`deepflow-mcp.log`：r16 官方 Go MCP SDK、mTLS、
  OpenBao 注册签名上下文；无 Incident 的实际 L4/L7 调用有 EvidenceRefs，
  两个持久步骤、wire replay 拒绝、Committed 分配复用通过（34.63 秒）。
  单目标查询隐藏对端身份，因此结果可为 partial；不构造虚假图边。
  独立 tenant/role/PKI 引导及 TLS/IAM Archive 明确为依赖 Fixture。
- `agent-defaults-final.json`：重复同步 changed=false，Agent Group 回读
  1024 MiB / 1000 millicpus；`agent-health.json` 回读实际 Pod 1Gi / 1 CPU，
  requests 256Mi / 100m、Ready、零重启。资源默认值与 renderer 共用项目
  `scripts/deepflow-dev/agent-defaults.json`，不是仅手工修改 DaemonSet。
- `deepflow-images.json`：实际开发镜像锁，开发实例仍为 candidate。

## 追加浏览器网络场景

`browser-network.json`、`browser-network.log`（r7，1.3 分钟）及
`oidc-network-runtime.log`（r4，187.42 秒）均通过，回执匹配本轮随机 runtimeId
`303ccaae-bff2-47e4-ab8c-bd9a9d1248e8`。真实 PKCE/RS256 与当前角色、L4/L7
live/Trace 禁用页面、持久轮次、SSE（两次）、刷新恢复和跨角色隔离通过。

轮次为有效 `partial`，未评价模型回答质量。运行中记录了 Graph 租约读取/
watch 暂时不可用、MODEL_RESULT_SETTLEMENT_REJECTED 及 investigator dispatch 错误；此通过证明页面、
协议、持久状态和隔离机制，不声明此次浏览器模型诊断效果或持续健康通过。
实际签名 L4/L7 MCP 调用的独立证据是 r16，原 VM/VL 模型协议闭环另见 r6。

保留数据库 `sp03_mig_406012eb37764aa99cf236a1e56eb5a3`；Archive
`41ef0409a870308dbab3f95afc848ac5afb58036ef4e6aa662a54d8b44d02aa6`
已停止，存储与身份保留。`server-config.json` 是当前开发探针/资源回读；
`storage-observation.json` 记录 PVC 的 NVMe 路径和机械盘瞬时阻塞，后续低
I/O 样本不代表主机永久修复。

## 失败与留存

`run-index.json` 记录远程运行路径、实际测试数和结束标记。DeepFlow live-r3
及 MCP-r3 无匹配测试，空 PASS 不计入验收；runner 现已拒绝零测试运行。
其他失败记录保留在 `/opt/ops-assistant-dev-20261012/`，包含最初 L7 rrt
字段错误、身份/范围和签名框架修复、Archive 辅助端口冲突、来源/证明
续约及远程 I/O/CDP 超时。失败不改写为成功，也不删除受保护数据库或存储。

本目录只归档必要回执和脱敏日志（去掉行尾空白，原始远程日志保留），不包含密码、令牌、完整 Cloud 资源清单、
TLS 私钥、签名 Secret 或渲染安装物料。真实材料留在远程受限目录。
`failure-browser-network-r4/r5.log` 保留 CDP 超时；r6 保留静态 Fixture CRL
过期拒绝。`failure-runtime-network-r3.json` 记录停止旧等待轮次并保留资源，
其结果不是 PASS。修复仅补齐测试 CRL 周期获取模拟，签名/有效期/撤销检查
保持开启，生产 PKI 不变。
运行脚本见 `scripts/assistant-runtime/README.md` 和 `scripts/deepflow-dev/README.md`。

## 代码检查与发行边界

受影响 Go 包及合同测试、go vet、生成产物一致性、Python 采集器两项测试、
TypeScript/Vite 构建与 runtime 源码闭包检查通过。
既有迁移 41/42 导致旧 migration-source 准入摘要失配；数据库真实前向迁移
已通过，但未重签旧材料或续建 Bundle。不将专项验收等同正式发行验收。


## 收尾状态

`cleanup.json`：核对 release 标签和 UID 后，两个专属网络 canary 缩容为 0，
停止本轮及上一阶段遗留的专用 QA 浏览器，保留 profile/日志。DeepFlow
candidate plane 和受保护证据/数据库/Archive 身份与存储保留。后续要复验
live L7，须重新应用 canary manifest 并取得新的有效非空来源证明；归档回执
不代表停止刺激后仍持续 live。未修改 core/final 工作负载。


## 用户授权的开发系统负载阈值覆盖

2026-10-14 用户允许针对远程机器配置绕过 DeepFlow 阈值，开发默认配置设
relative_sys_load.trigger_threshold=0。`agent-defaults-bypass.json` 回读一致且
重复同步 changed=false；Agent 1 GiB / 1 CPU 保留，来源/范围/非空数据门禁
不绕过。实际采集恢复后，r9 的六项 L4、L7 200/503、范围/来源撤销、Trace
禁用通过（5.56 秒），回执见 `deepflow-live-after-bypass.json`。
`agent-health-after-bypass.json` 如实记录一次 09:44、exit 255 的 Agent 重启，
早于此覆盖值，旧零重启快照保留。

补验收中的 OIDC network r5/r6 与 direct live r6/r7 无有效新采集而失败；
direct r5 是 SKIP，不计入通过；r8 已完成查询却缺少输出路径而 FAIL。
runner 现自动分配 DeepFlow/MCP 报告路径，拒绝零实际通过或任何 SKIP。
失败记录和资源继续保留，最终补验收状态以追加回执为准。


最终补验收说明：浏览器 r8（对应 OIDC network r7）正常模型轮次发生
MODEL_TIMEOUT，状态 failed，未通过；分别保留 failure-browser-network-r8.log、
failure-oidc-network-r7.log、failure-runtime-network-r7.json。模型位于本机
NVMe，冻结 Profile 的 120 秒超时保持；未替换模型、放宽 Schema、将 failed
计为通过或修改五例质量结果。此前 r7 浏览器 / r4 Go 的有效 partial 机制
通过结果继续保留，但不用于宣称最新正常模型轮次或持续运行健康通过。
