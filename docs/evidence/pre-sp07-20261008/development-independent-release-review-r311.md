# R13 开发发布最终独立只读审核

审核者：`/root/functional_readonly_review`。主代理按审核者最终回复原意落盘。审核者未参与实现、构建、部署或恢复操作，只读取源码、公开及私有原始证据，进行只读摘要/签名核验和 Kubernetes 状态查询，没有修改环境、启动测试或调用模型。

结论：**PASS，仅适用于本次 main 源码交付、R13 开发环境更新及必要业务冒烟。** 未发现该范围内尚待修复的确认缺陷或必要证据缺口。不能据此宣布完整 R0～R6、全冷离线安装、DeepFlow bundled 或正式生产升级验收通过。

## 完整交付复核

1. **源码与 Git 对应成立。** 审核时本地 main、HEAD 与只读 `git ls-remote origin refs/heads/main` 均为 `95ae3dad9d80897f814eed5fb411e50f0c9044d9`。直接核对 4749 个源码绑定文件，均与当前文件一致；源码归档内这 4749 个文件逐项内容摘要也与绑定一致。此次发布未改变 r288 已通过独立功能审核的实现。r318 确认剩余修改仅为文档和证据，秘密检查无命中，`git diff --check` 为 0。
2. **签名物料、导入和运行镜像形成对应链。** Bundle 为 `pre-sp07-dev-20261008-r13`，32 项物料、91 个认证文件，payload 为 `sha256:977b969618daa42b0dad1c8de52e1a410872a3f9b8d489047467ab9cd2cb0117`。直接使用原独立信任公钥核验 detached signature，OpenSSL 退出 0；公开清单与实际包清单字节相同。API、Worker、源码归档及 Chart 实际字节摘要符合签名清单；两个 OCI 的 manifest/config/layer 闭包有效，镜像中的 `/ops-process` 与本次准备的二进制一致。正式 `opsctl bundle import` r302 退出 0。最终实际 runtime imageID 分别为 API `cc4d7393…bfcba`、Worker `2f519bbd…978b`，与新 OCI manifest 一致；HolmesGPT 保持原锁定镜像。
3. **更新范围和数据保护符合授权。** 独立比较 r305 原始前后清单，488 个保护对象保持一致，控制器声明的变化严格限于 API/Worker 两个镜像字段。没有重跑迁移或 bootstrap，没有改写初装 checkpoint。首次失败 r303 保留退出 1；r304、r313 使用原安装恢复材料经正式 status/unseal/status 解封，未重新初始化或轮换历史 key。最新原始快照中 68 个既有清理对象的控制器 UID 均仍为零副本；investigator 临时缩容后恢复原 Deployment UID、完整 spec 和单副本 Ready。
4. **必要业务冒烟及失败表达准确。** r306 在 r308 撤权之前完成既有调查、归档、Audit 和持久 SSE 读取，退出 0；r307 实际 aggregated Node/Pod 指标投影和 r308 部署 API 的 step-up、身份/scope 替换拒绝、撤权/恢复及幂等路径均退出 0。r314 旧 Job 读取失败、r315 令牌过期 401 均保留原始退出 1，没有覆盖成成功。r316 使用新真实 LoA-2 身份，实际证明旧 Job/Steps/SSE 返回 403，同一身份读取历史归档 Evidence 返回 200，摘要与对象版本不变。
5. **当前授权下的新 Job/SSE 正向控制通过。** r316 在 investigator 正常缩容并确认无 Pod 后，经正式 API 创建、取消新 Job `01a11b7a-551a-77e9-b8c9-71bd1c60cca0`，没有业务表直写；事件与 Audit 各 2 条，SSE 读取 2 条、cursor 续传 1 条，模型/工具调用均为 0、预算无残留，最终恢复 investigator。旧授权快照未回写。正式 09 §1167、07 §32 及当前 SP06 的不可变授权快照规则支持此 fencing 行为；未发现要求撤权恢复后自动重新绑定旧 Job 的正式条款，因此该 403 不列为功能缺陷。
6. **最终现场状态已重新核验。** r317 原始快照及审核者随后独立 Kubernetes GET 均显示当前 11 个 Pod Ready；新 API/Worker runtime digest 正确。r309 仅作为历史时点记录，未代替暂停后最新状态。

## 边界与 Git 收尾

本次没有重跑模型、性能或全冷安装。性能与全部 Ollama 真实模型验收继续为 **USER_WAIVED_NOT_PASS**；原 `make check` 退出 2 不改写。KubeVirt/CDI 延期、PyRCA disabled/excluded、ActionPlan 仅建议及 SP-07 未开始的边界保持有效。原历史 Transit 恢复缺口、DeepFlow 准入/运行及完整 R0～R6 未完成事实继续保留。

本审核时发布证据和报告尚待主代理完成最后的文档提交、普通 push、远端一致性及工作树 clean 核验。这是审核落盘后的 Git 收尾，不是本报告已经完成的证明；最终用户报告须给出实际文档交付提交及干净工作树结果。

相关证据：[R13 发布记录](development-release-r13-r310.md)、[当前授权冒烟](development-current-authority-job-sse-smoke-r316.json)、[最终运行状态](development-final-environment-state-r317.json)、[源码与秘密检查](development-final-source-and-secret-check-r318.json)。
