# 当前 R9 交付后续验收记录

2026-10-07。本记录是进行中的实际分项验收，**不是 R0～R6 完成或独立审核 PASS**。源码基线仍为 `pre-sp07/nonvirtual-readiness@cd577dfc4e9d1d53f93b5c031d9b0a303ef32dc8` 加保留的工作树修改；受测源码文件映射为 `153297868b1d4bdd23d4c0f74138d4414d2839c89106e2ab85fa1da8a1d286c9`。本批操作没有修改产品源码或提高调查预算。

## 当前正式安装与调查

新 Bundle `pre-sp07-core-20261007-r9`，payload `sha256:416cae45546a94a1d71d28d82565b481a44526b7deea0fbebcc5bb248a624c6f`，构建及独立公钥验签均退出 0。用户要求“再次尝试”后已完成，10 GiB 安全下限没有降低。新 namespace `ops-pre-sp07-core-r7-20261007`，UID `df53016a-e1fa-4ada-9628-34136140a9dc`；正式依赖、空库 35 条 forward-only 迁移、身份、Tenant/Cluster/Source/签名 Registry 及业务安装分别通过。见[安装](current-core-r7-business-install-r2-command.json)、[迁移](current-core-r7-empty-migration-receipt-r1.json)和[注册](current-core-r7-formal-api-initialization-r5.json)。所选 OCI 引用已有缓存，**未证明冷导入**。

真实调度故障产生 Finding/Incident 后，经正式 API 启动 Job `01a1145b-78db-7741-b14c-3d244ecade18`。当前签名 investigator 实际报告 `MODEL_TIMEOUT`，Job 诚实失败，模型调用 1、工具调用 0。未知用量消耗完整预留，余额释放，6 个持久 SSE 事件、精确后缀续传及跨租户 403 通过；见[失败分类](current-core-r7-real-provider-failure-classification-r1.json)、[预算与 SSE](current-core-r7-failed-investigation-sse-r4.json)。这不证明成功模型/MCP 调查。

## 调度目标与来源事实

[独立预期](current-core-r7-scheduling-independent-expectations-r1.json)在创建之前记录。原 Pending Pod UID `0d7ecc8c-8e24-4cce-9841-066fbf77b505` 没有卷、控制器或服务账户投影。核验归属后使用 UID/resourceVersion 前置条件正常删除，再创建去掉不可满足选择器的新 Pod，UID `dc261acd-e559-45bc-b473-4511b1615d0b`，实际 Ready。没有修改共享 Node 或强制清除 finalizer。旧 UID 的两个已归档 Evidence 保留原 digest/版本和 365 天截止时间，未自动绑定新 UID。

首次完整 Graph 恢复断言退出 1：新节点事实 fresh，但两个 Victoria 范围探针未验证，Graph 为 partial；原失败保留在[完整门禁](current-core-r7-scheduling-native-recovery-r1.json)。随后[独立状态观测](current-core-r7-scheduling-state-observation-r2.json)退出 0，仅证明新原生 Pod Ready、新 UID 已可授权查询、旧 Evidence 可读，以及两个来源退化如实表达，不把 partial 变成完整恢复。

为补齐来源正向事实，创建了自有有限启动日志 Pod（[清单](current-core-r7-owned-source-canary-manifest-r1.json)），实际 UID `41561dd5-4da3-4763-acb9-cfdfcabf32cd`，1800 秒截止，无服务账户投影或持久卷。通过真实 stdout、原生日志时间戳和前后 UID 核验采集日志，通过严格 Service SAN/CA 验证的 TLS 写入本安装独立 Victoria 存储，受保护查询的错误凭据返回 401；[准备](current-core-r7-real-victoria-canary-r2.json)退出 0。它是有限操作方采集，不代表常驻采集链交付，也没有虚构故障。

正式安装器拒绝把探针换成该新 UID，退出 1，原因是既定初始化回执的目标身份绑定；[原始拒绝](current-core-r7-business-canary-install-r3.log)保留，未修改回执或放宽校验。因此该 Pod 的真实日志没有被记为当前日志适配器准入通过。

原探针 Pod UID `3a913fd3-5ce5-4b39-a47a-99046fe6929e` 的阶段样本来自初始化时实际保存的原生快照，采集时间 **02:54:45.178363 UTC**，落在既定探针窗口。原时间戳原样写入 VictoriaMetrics，明确作为历史样本；[原始样本](current-core-r7-original-phase-native-sample-r3.json)、[采集与写入证明](current-core-r7-original-native-phase-ingestion-r3.json)。未找到原窗口的真实日志或 Event，未制造日志、回填当前时间或更改 checkpoint。

[正式 VictoriaMetrics API 门禁](current-core-r7-victoria-phase-formal-api-r2.json)退出 0：实际正向范围证明、授权 API→正式适配器→Transit→不可变版本 Archive→digest/版本读回，以及相同幂等键返回同一 Evidence/版本；跨租户 403、未准入日志来源 `503 SOURCE_SCOPE_UNVERIFIED`。实时 Graph 仍 fresh/partial，退化列表仅含 VictoriaLogs。**历史 canary 不作为恢复后的最新事实；Logs 未验证、完整 Graph 门禁和完整调查未通过。**

保留了操作方失败：公开 `/health` 返回 200，改为真实受保护查询路径验证凭据；Victoria `factSlice` 按 Contract 为数组，修正操作方读取形状后，保留并增强 UID/tenant/namespace/phase/value 检查。没有删产品断言或改阈值。实际操作程序见[源码索引](operators/operator-source-index-r1.json)，秘密、认证值和私有恢复材料未复制入仓库。

## 尚未完成

R0～R6 均未整体完成。原历史 27 条 Evidence 对应 Transit v1 已丢失，用户确认无匹配材料；原 PostgreSQL、对象和存储继续保留。重新安装不依赖这些旧密钥，但历史解密和受保护原地清理不能因此 PASS。当前 R6 的独立数据库与 Raft 恢复只证明各自分项，不替代物理对象版本恢复、Audit 密码学验证及联合 C 门禁。

DeepFlow 仍需锁定 ClickHouse 内嵌 NLP 数据的许可覆盖、剩余物料裁决、原生 UID 映射、签名 bundled 安装和真实流量全链；不修改 candidate/installable 绕过。Metrics 原生采集/保留/API 已通过，MCP 及正式来源撤权/恢复等闭环未完成。`make check` 当前退出 2，真实模型用例超时；十个并发实际调查与主链路共同运行的成功闭环仍未通过。独立 IPv6 正向控制缺失，完整网络验收未完成。

性能基准、持续压测、容量、追加 P95：**用户豁免，不是 PASS**。KubeVirt/CDI/VM 继续 disabled/unverified；PyRCA disabled/excluded；BMC/DIMM/NIC 现场未验证。SSH 仅有隔离身份/CA/principal/权限边界准备，不执行命令链，也未宣称未来 Task 7.5 通过。完整独立只读审核尚未启动，未提交、合入或推送，没有进入 SP-07。
