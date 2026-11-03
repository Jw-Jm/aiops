# r43 关系拓扑正式入口与模型门禁核对

GPT 直接执行，无 CodeBuddy/子代理、提交或推送。main HEAD 082f87d66c5b；保留既有未提交修改。30471 静态 Web 前滚为 ops-v12-native-r43-web-30471，index SHA256 559e621aa885c1504ec19822efe787d7125ad565e14f5f66eaeded53b409c8be；r42b 停止保留。API/Worker r39c、PG17.11/schema71、调查器及30472均未变更。

## 实现与范围

开放 /resources/topology 与导航，资源目录增加入口并在详情链接携带当前 clusterUid。复用 C002 当前集群授权列表（每页50，显式加载更多）、C004 邻居查询（深度1/2、200节点/400边）、C003 详情重新读取；没有新增 API、迁移、依赖或业务存储。指定根通过 clusterUid+canonicalIds 列表核对，拒绝/缺失不回退。URL 保存根/深度/图列表；集群切换按 scopeKey 卸载，迟到结果隔离。图与表完全使用返回节点/边，不按名称拼关系，不称因果；列表保留来源/规则/时效/原始关系详情，单节点/部分/截断明确呈现。图使用既有 Cytoscape/布局器及原型176×58节点、350px画布，提供缩放、重置、键盘节点选择及重新读取的详情抽屉。

## 分层实证

- 本地24项 Vitest（topology 8、overview/resources、graph TTL/budget、P1 shell；按 ui-passed.log 的4文件汇总）与 TypeScript/Vite 正式构建通过。拓扑新用例覆盖分页、指定根缺失/拒绝、同名不同UID/namespace、深度、局部视图不发请求、详情读取、迟到集群结果和拒绝结果隐藏。首次缺少测试身份、resetAllMocks清除全局matchMedia、fixture类型/ByRole参数等失败保留；并非真实业务失败。
- 真实远程 Chrome/operator A：Pod ops-seaweedfs-0 两层5节点4边，一层4节点；实际 Cytoscape节点ID与API逐项一致；放大0.38390→0.46068、缩小/重置回原缩放。图/列表切换不发新邻居请求；抽屉重新GET，完整详情链接保留规范ID/集群；刷新保留URL深度/列表。跨集群根真实403、不显示图；同集群未授权B命名空间的真实ConfigMap返回空根且无邻居请求；未授权集群被公共壳拒绝且无资源请求。0业务写入、0页面异常。该读权限验证不替代两个真实集群/运行中撤权全组。
- 独立 READ ONLY PG + Kubernetes：5节点的canonical/UID/namespace/kind/name与PG和当前API对象一致，4条边由实际ownerReferences/Service selector/ConfigMap volume独立核验；未借被测关系计算器生成oracle。API/Worker/Python三个真实Pod均挂同一r28-runtime模型材料。旧r41/r42日志结果摘要不变；没有新日志查询、会话或模型调用。没有重启/历史图/持续来源完整性验收。
- 截图没有通过：旧严格捕获和单独限制 /resources/topology 的精确公开UUID协议均命中 recovery-code 形状，未产出图、未删除保护检查或通用豁免。7项捕获协议自检仅验证未知标识/因子/晚到秘密会拒绝，**不代表视觉通过**；r40日志专用helper未用于其他页面。UI参考仍为既有r3，未重采736图。两次浏览器定位失败（关闭控件语言及公共壳未授权文案）保留并按实际DOM修正。

## 模型门禁的准确结论

model-gate-audit.json 从当前 ConfigMap 与实际Pod挂载、r28 admission/protocol原文件及READ ONLY已发布策略交叉核对。API/Python同Profile，taskKinds仅data_query，evaluationDigest实际指向r27协议探针；没有resource_troubleshooting专项评测/准入实证。签名r28策略**已有**受scope约束的operator/agent只读工具允许规则，不应误称完全缺失运维策略。能力API仍enabled=true/taskKinds=[data_query]。现阶段不直接扩taskKinds，不伪造材料、不POST绕门禁；下一步需实际资源排查工具/结构化结果评测、当前策略决策验证，再按相同引用更新API/Go Worker/Python并做创建/持久化/执行验收。用户受信DeepSeek授权继续有效，未要求重测CodeBuddy/模型连接。

## 验收与接续

P3-01从not_started转in_progress；SL009只追加实际链及证据，不提升六维。完整总览/目录/抽屉视觉、图完整像素、来源追溯全部状态、真实两集群/重启、P3日志预置与持续合格来源、r42实际AI创建/执行及P4～P8仍未完成。当前Kubernetes图源有source_continuity_unverified/scope_limited，绝不改为完整覆盖。日志快照原资格2026-10-29 10:45(+08)未延长。自然过期与启动间歇503仍按历史限制记录；本轮正常读不证明永久解决。

复验脚本只适用于本任务namespace/UID/账号文件/PG15454。readonly-runtime.py只导出公开模型/策略和资源安全元数据，不复制private或凭据；verify-oracle.py不保存ConfigMap data/容器env。deploy-web.py仅替换30471静态站点，失败恢复旧Web；不得用旧API/身份或整库回滚替代前滚。
