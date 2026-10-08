# main 修复集与开发环境发布 R13

状态：源码已合并并推送 GitHub；开发环境滚动更新及必要业务冒烟通过。[最终独立发布审核 PASS](development-independent-release-review-r311.md)，范围仅限本次源码交付、开发更新与必要业务冒烟。本次不代表完整 R0～R6、通用 Kubernetes 离线交付或全冷安装通过。

## 源码与物料

fetch 后远端 main 仍为 `cd577dfc4e9d1d53f93b5c031d9b0a303ef32dc8`。此前保留的计划、实现、锁、生成物和证据共 3377 个文件提交为 `95ae3dad9d80897f814eed5fb411e50f0c9044d9`，通过 fast-forward 合入 main，普通 push 成功；当时 `git ls-remote origin refs/heads/main` 与本地 HEAD 一致。未 reset/clean 或 force push，其他 worktree 保留。新增证据随后作为独立文档提交推送，最终远端一致性在交付时再次确认。

新包 `pre-sp07-dev-20261008-r13` 包含 32 项物料、91 个认证文件，payload 为 `sha256:977b969618daa42b0dad1c8de52e1a410872a3f9b8d489047467ab9cd2cb0117`。4749 个运行源码/物料输入文件绑定上述已提交源码，`uncommittedSource=false`；精确源码归档为 `sha256:a3a9c8d2e267507825e5c037700f7d75810f961bfa089076da9ecbf76a2b3aaf`。使用既有仓库外独立验签信任，未将签名私钥、凭据或私有备份装入包/仓库。Ollama 模型权重不在包中。

新 API/Worker 从该提交离线编译，以 `--network=none --pull=false` 构建镜像；保留既有已准入依赖、HolmesGPT 及 Analyzer 精确物料。相对 R12 源码绑定的变化仅为八个 internal 功能实现/测试文件，涵盖此前已独立 PASS 的 Node 生命周期校验及已删除 ActionPlan 目标拒绝；没有升级上游运行时。

## 开发环境更新

目标 namespace `ops-pre-sp07-core-r9-20261008`，UID `8ef6f18e-0621-45d0-877e-4e575cb34603`。正式 `opsctl bundle import` 验签并导入新包，退出 0。随后采用已验签的当前 Chart 和原安装值进行开发环境 Helm 滚动更新，revision 4；渲染语义差异严格限定为 API/Worker 两处镜像字段：

- API：`ops.local/task27/platform-api@sha256:cc4d73936cc0eca3442ecfc609a974594a40d80e3c43cb432bf7f7111b8bfcba`。
- Worker：`ops.local/task27/platform-worker@sha256:2f519bbdeb4bbead55143f64e53bea883dec68ce9b08d3e27bf1ae71046a978b`，保留两个副本。

实际 Pod runtime imageID 与新 OCI manifest digest 一致。当前 5 个 Deployment、5 个 StatefulSet、11 个 Pod 均 Ready；存储、信任、身份、APIService、依赖及其他控制器声明保持一致，68 个旧环境控制器仍为零副本。初装 checkpoint 原样保留，没有迁移、bootstrap 重跑、改写 checkpoint 或重建数据库。此为已授权的开发环境滚动更新，不是正式安装器的 fresh install，也不是提前完成 SP-09 升级能力。

首次更新前检查退出 1，未修改部署：现场所有当前依赖容器在 07:43 UTC 左右重启，OpenBao initialized/sealed，业务进程出现 Kubernetes login/PKI 初始化失败。未推断重启原因。使用正式 `opsctl openbao status/unseal/status` 与本安装原有两份不同 share，持久 Raft 恢复到 ready；没有重新初始化、轮换历史 key 或启用旧环境。三个业务 Deployment 自行重启后恢复 Ready，随后更新成功。首次失败与恢复证据保留。

## 必要业务冒烟

- 真实 HTTPS Keycloak OTP/LoA-2 身份及正式 Tenant API：退出 0。
- 更新后的正式 API 读取既有真实调查、Steps、归档 Evidence 摘要/对象版本、Ledger/Audit 原子记录、持久 SSE 及 cursor 续传，跨租户拒绝：退出 0。读取的是已存在成功调查，没有发起新的模型请求或重测模型能力。
- 真实 aggregated Metrics API 的当前 Node 与新 API Pod UID、timestamp/window/Quantity/原生容量关联，使用该提交 Inspector：退出 0。没有删除 Node、触发高负载或宣称完整 R3 验收通过。
- 正式部署 API 的 Step-up、角色状态更新、五类身份/scope 替换拒绝 400、外租户 binding 404、撤权后历史 Evidence 403、旧 revision 409、恢复后归档摘要/版本不变和幂等重放不撤销恢复：退出 0，Operator 最终保持 active。

没有重复全冷安装、性能/容量/P95、十并发实模型或 Ollama 验收。性能和 Ollama 为用户豁免，不记 PASS。KubeVirt/CDI 仍延期 disabled/unverified，PyRCA disabled/excluded；ActionPlan 仅建议，SP-07 未开始。

用户继续后的现场复核（12:15～12:28 UTC）：容器再次重启、OpenBao sealed，仍使用原安装恢复材料通过正式 status/unseal/status 解封，11 个当前 Pod 恢复 Ready，见 [r313](development-resumed-openbao-recovery-r313.json)。没有重建数据库或密钥。r306 在角色撤权/恢复冒烟 r308 **之前**通过；r308 将 Operator revision 从 3 推进到 5，既有 Job 的不可变授权快照因此失效。再次读取该旧 Job 的 r314 退出 1 原样保留，不代表恢复后的当前授权调查通过。r315 在缩容和业务变更前因令牌过期返回 401，退出 1 保留。

随后以新的真实 HTTPS OTP/LoA-2 身份运行 [r316](development-current-authority-job-sse-smoke-r316.json)，退出 0：旧 Job、Steps、SSE 均实际返回 403；同一当前身份读取归档 Evidence 为 200，摘要及对象版本不变。正式 API 在 revision 5 下创建自有 Job `01a11b7a-551a-77e9-b8c9-71bd1c60cca0`（202），取消并读回 cancelled（200）；持久事件 2 条、Audit 2 条，SSE 完整读取 2 条、cursor 续传 1 条，无丢失或重复。创建前 investigator 正常缩容至无 Pod，finally 恢复原 Deployment UID、完整 spec、单副本 Ready；模型和工具用量为零、预算无残留。没有改写旧 Job、scope 或业务表。最终 [r317](development-final-environment-state-r317.json)确认 11 个当前 Pod Ready，68 个旧控制器保持零副本。

## 离线交付能力的当前边界

当前 core 已有锁定、签名离线包及安装/bootstrap 入口，并在 OrbStack 的独立 namespace、独立依赖和空业务库上有分项实际验证。正式运行导入/安装器目前只支持 OrbStack 的 `orbstack_shared_store` 开发目标；镜像缓存复用、DeepFlow bundled 准入及运行、原历史 Transit 缺失等缺口仍保留，不能宣布任意干净 Kubernetes 上完整离线部署已通过。真实模型是明确的独立内网前置，不能把权重或模型运行证明归入 Bundle。

证据：

- [提交前秘密材料检查](main-delivery-precommit-secret-check-r297.json)、[用户发布授权与基线](main-development-delivery-r298.md)。
- [离线物料准备](development-update-preparation-r299.json)、[源码绑定](current-core-material-signing-input-r13.json)、[独立验签](current-core-signed-bundle-receipt-r13-r301.json)、[签名清单](current-core-signed-manifest-r13.json)。
- [正式镜像导入](development-signed-image-import-r302.json)。
- [未修改环境的首次失败](development-rolling-update-result-r303.json)、[原密钥服务恢复](development-openbao-existing-storage-recovery-r304.json)。
- [滚动更新计划](development-rolling-update-plan-r305.json)、[运行镜像和保护核验](development-rolling-update-result-r305.json)。
- [正式 API/归档/Audit/SSE](current-core-r9-investigation-chain-live-receipt-r306.json)、[命令/退出码](current-core-r9-investigation-chain-live-command-r306.json)。
- [真实 Node/Pod 指标冒烟](development-current-metrics-smoke-r307.json)、[角色及拒绝路径冒烟](development-operator-api-smoke-r308.json)、[最终运行状态](development-final-environment-state-r309.json)。
