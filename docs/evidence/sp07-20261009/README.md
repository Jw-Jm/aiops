# SP-07 非虚拟化交付证据（2026-10-09）

用户授权 ADR-0032；Task 7.1～7.6 到正式 Contract、入口、测试、原始证据和完成条件见 task-ledger.json。独立审核首轮NOT PASS，两项确认缺陷修复后第二轮完整限定范围PASS。根目录00～10正式文档只读。使用正式API，无测试专用业务入口、常驻执行服务或新Agent/LLM循环。

## 交付和真实验收

current-development-deployment-r9.json：最终签名增量 Bundle sp07-nonvirtual-20261009-r9，ARM64 Helm revision 12，API1/Worker2实际imageID核对。r9更新第一方API/Worker/opsctl/Chart及其源码闭包；固定Runner、迁移CLI及其对应源码复用已签名r6，不重新准入或改动第三方。final-source-and-secret-check.json确认运行源码与r8当时签名源快照一致；最终review-r9-source-binding.json核对r9，秘密和SSH私钥在仓库外。

native-correctness-batch.json 与逐项原始日志：四类Profile有限串行真实Kubernetes/Ansible/OpenBaoSSH证书正反例、namespace/cluster RBAC拒绝、普通用户/root/sudo边界、输出10MiB截断、15秒超时、取消、UID受限Job丢失，不确定执行不重发。native-gates-r2-result.json为真实Keycloak LoA2和平台门禁、未确认/错digest/改命令/越权目标/幂等冲突/撤权/admin不继承operator。native-retire-v1-r8-corrected-result.json为正式API停用本任务旧v1后的404拒绝；当前v2保留。

先前自有PVC Pending：native-execute-k8s_namespace-pending-with-plan-r4-result.json记录退出0仍not_resolved。native-execute-k8s_cluster-actual-pvc-repair-result.json记录操作者确认实际修复，最新原生事实PVC Bound→resolved。SSH未接入可信原生规则/事实源，真实命令成功仍inconclusive。native-execute-k8s_namespace-noninteractive-eof-r8-result.json证明stdin EOF命令退出1，但最新PVC状态仍resolved，退出码与修复判据独立。

native-sse-r8-result.json为数据库持久事件、Last-Event-ID续传、全流seq、stdout/stderr和归档digest；command-sse-withdrawal-red-r2.log→green.log为先失败后修复的逐事件撤权和写入期限回归。native-persistence-audit-final.json为修复前真实数据库只读检查（有9条归档未完成，首轮独立审核因此NOT PASS）；native-output-archive-r9-progress.json证明修复后28条终态全部归档核验，native-sse-outputcap-r9-result.json验证10MiB真实输出digest与续传；原检查：实际Bash Transit密文、摘要绑定、确认消费、最多一次dispatch attempt及归档状态。original-protected-objects-final.json确认原488对象保留，仅自有PVC授权修复改变volumeName，没有删除原对象或Secret。

## 模型和正确性

real-model-functional-r1.json：真实Ollama llama3.1:8b-16k、既有HolmesGPT+标准MCP+Evidence+Go Validator串行功能成功；两次模型请求，仅一条有限调查，无ActionPlan返回。不声称模型建议生成或RCA准确率通过。sp07-current-validator-authority-green.log及final-transactions-validator-recovery-r8-targeted.log分别验证合法模型协议建议经当前Go Validator持久化平台UUID；假Evidence、tenant/target变更和执行handle拒绝，调查不生成risk ack或execution。该协议fixture不是实际模型准确率证据。

final-validation-commands.json列最终受影响单测、安全、Contract、真实数据库事务/标准MCP/Validator/Lease/两个竞争预算，以及工具链/生成物/源码闭包的实际命令与退出码（均0）。amd64四进程源码构建通过，最新API/Worker-r8构建0，amd64实际部署未验证。

## 真实失败与限定范围

失败日志保留。风险确认时钟约束、Worker权限、SSH解释器、原生Post-check归档接线、模型建议非UUID持久化、确认幂等绑定及SSE逐事件撤权均先有失败证据再修复及回归；不降低正式预算、期限或阈值。

final-affected-units-security-contract-r2曾因未受影响的历史20KGraph默认10分钟超时退出1，不能记PASS或套用模型性能豁免；受影响范围最终测试0。final-upstream-replay退出2：GraphReplay通过，历史InspectionReplay缺少精确锁定SDK镜像；没有删断言或扩大为历史离线包全补齐。final-transactions-validator-recovery-r8的宽选择器意外纳入另需SP06_REAL_CHAIN环境的历史模型fixture退出1；最终精确选择器0，原生模型结果单独保留。native-retire-v1-r8断言403不符合正式404，保留失败并修正客户端断言后验证0，没有改变门禁实现。

r7构建监测器在签名完成后返回1，CLI退出码未捕获；独立verify/import均0。r8首次尝试触及既有10GiB空间保护线退出1，清除精确本任务未签名临时目录和核验过的旧任务payload后重新build/verify/import0；不全局prune，不删除归属不明或受保护资源。旧任务payload的签名manifest、digest及处理回执保留，原始pre-SP07包不动。

十并发调查、模型并发/吞吐/P95/持续压测/容量和为这些测试扩容：USER_WAIVED_NOT_PASS。KubeVirt/CDI/VM disabled/unverified/deferred，PyRCA disabled/excluded；完整amd64离线交付、DeepFlow及缺失历史Transit恢复继续保留，未伪写PASS。本轮停止于SP-07，不进入SP-08/SP-09。

独立审核首轮 NOT PASS 和两项修复详见 independent-review-first-round.md。真实错误hostkey负例与逐字节恢复在 native-hostkey-negative-restoration.json；其他OpenBao响应仍1MiB，正式输出10MiB不变。最终审核限定范围PASS，见 independent-review-final.md；29条终态执行全部归档核验见native-output-archive-final.json。
