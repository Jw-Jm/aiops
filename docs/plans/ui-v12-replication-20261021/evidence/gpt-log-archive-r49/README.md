# r49：任务环境持续日志归档

2026-11-03，GPT 直接实施。用户“继续”承接持续归档工作。开发环境仍为 `ssh mssc-direct`，任务 namespace `ops-native-v12-r24-20261026`，页面 https://192.168.0.63:30471；schema 72 不变。无模型调用、无 SP05、无 30472 改动，不涉及 KubeVirt/CDI、Trace 或生产。此次代码未提交/推送。

已从按需日志推进到持续读取 kubelet CRI 文件并写入独立 VictoriaLogs。现同时保留两个显式可选来源：原 Kubernetes 当前单容器按需读取，以及新 `r49-continuous-pod-logs`。无自动回退、手工导入或伪造日志。旧 r37 快照、旧组件制品与历史结果均保留。

## 实现和物料

- 沿用已经锁定的 VictoriaLogs v1.52.0 源码 `46a54c976fa3d404396050e8a5ee6c5b0320efc5`，Apache-2.0；使用上游 vlagent Kubernetes collector、检查点、轮转处理和持久队列。`scripts/logging/build-vlagent.py` 检查固定源码/许可证摘要、测试并交叉构建。源码归档 SHA256 `66671ddad5d614eda7cec045e500cdaf48248cd68d4be2d454f4ba21bd6c6767`。
- 最小补丁 `ops-native-identity.patch`：原生元数据携带 Pod/Namespace UID；以 CRI 时间为准，保留完整原始消息；阻止消息 JSON 覆盖保留身份字段，规范 level；hostNetwork 通过 downward API 的 `POD_NAME` 定位自身 Pod。补丁/二进制摘要与构建信息见 `runtime/material.json`。采集器为任务 canary，未晋升 core Bundle 或正式 OCI 准入。
- `ops-r49-log-collector` DaemonSet 固定本机节点，日志文件只读挂载。过滤器在开文件前排除其他 namespace 和自身日志；上游元数据接口需要读取本节点 Pod 的跨 namespace 元数据，故专用只读 ClusterRole 仍允许 Pod/Namespace get/list/watch 和节点 mssc get。没有 secrets、pods/log 子资源或资源写权限；这不是“无跨 namespace 元数据访问”。运行容器以 root 读取 kubelet 0640 文件，禁提权、只读根、移除 capabilities。
- 独立存储 `ops-v12-r49-victorialogs`，仅 `127.0.0.1:19438`、TLS + Basic，30d 保留。采集器指标仅本机 `127.0.0.1:19429`。凭据只在远程 private、Secret 和真实 OpenBao 路径；管理员密码+TOTP 对精确请求执行登记，Source ID `01a19f70-d86a-70d7-a29d-dc148a60be6a` revision 1。
- requiredLabels 固定 tenant/cluster/collector；可信 collector 标记确定原生字段映射。查询同时检查固定 UID、namespace、授权、来源版本和返回行；样例脱敏后才持久化，附带 container/containerId/namespaceUid/pod 来源字段，页面详情可见。
- 每 10s 查询最近一分钟真正已存储的事件，成功才更新事件水位和 30s 资格租约。空数据、错误标签、未来时间、网络错误、当前来源失权均撤销租约；近期静默也会保守撤销。collector/材料摘要固定，动态水位不进入不可变来源摘要。固定请求和结果不会随刷新重写。

## 真实验收

- `source-qualification.json`：独立 kubectl 比对原消息、Pod/Namespace UID 和容器 ID；错误 UID 和其他 namespace 查询为空。`native-nanosecond-check.json` 另核对原生 CRI 时间到纳秒。没有生成测试消息。
- `live-success.json`：真实 Cookie/CSRF/原生授权创建查询，实际归档总体 14、样例 14，`partial`、`coverage.complete=false`、`basis=unknown`。总体是来源固定窗口的实测数量，不代表全部原生日志完整性。
- `browser-archive-logs.json`：真实页面选择来源、两次创建、刷新、两次各 20 条样例；第二窗口 4 条自然新增，首次结果摘要不变。重新登录后按原 queryId 从历史回放，外集群身份 403、无额外业务写入、无页面异常。历史回放恢复脚本没有重建查询。
- `recovery-check.json`：真实暂停新存储 32s；归档来源撤销资格、结果暂不可读 503，原生按需来源仍可用。在暂停期间优雅重启采集器，使检查点/待发送数据落盘；恢复后找回期间自然发生的 3 条日志。原查询摘要不变，固定闭合窗口仍 14 条，未观察到重放膨胀；不据此宣称所有故障都严格一次投递。
- `identity-check.json`：任务专属双容器 Pod 执行真实 `weed version`；观察两个容器及重启前后 4 个 container ID，平台创建查询成功。删除后同名重建，UID 更新、新容器不混入旧 UID 选择；探针最终删除。`check-deleted-resource.json` 验证删除资源的旧结果按当前权限/身份重新核对后 403。
- `runtime-final.json`：API/Worker/collector 均 Running、ready、0 重启；存储 Running 且未暂停，检查点存在，探针已删除。当前 Web `ops-v12-native-r49-web-30471`，前版 r48 停止保留。API/Worker 二进制与 Pod UID 见 `backend-runtime.json`。

本地四个 Go 包、连续来源 race、19 个日志页用例、TypeScript/Vite 构建、相关 Query 合同通过；上游 rename/create 和 copy/truncate 轮转单测通过。生成物核对首次发现既有 migration 71 的 `InvestigationObservationQuery` model 遗漏，按现有 sqlc 生成器补齐后通过；未新增迁移。保留日志在 runtime/。`make check-runtime-source` 的平台运行依赖闭包通过，但迁移包闭包失败：既有锁未包含已在 HEAD 的 00071/00072，所有已锁文件与 HEAD 一致。本轮没有新迁移，也未重写正式准入证据；完整交付门禁未通过。

## 失败、边界和下一步

首次集群没有 Docker 中同名镜像的 containerd 缓存，改用已缓存平台镜像承载静态二进制；hostNetwork 的主机名不是 Pod 名，补 downward API 后运行。浏览器首轮用毫秒比较纳秒半开区间，修为纳秒；第二轮已完成两个查询后，历史按钮 exact 定位超时，按原 queryId 单独恢复并验证，未重复创建。identity 首轮误要求版本命令的每行都含 version，但上游同时输出企业版提示，改为每个容器至少一个真实版本行。上述失败证据保留。RBAC 最终用显式 `--subresource=log` 核对，未把 `pods/log` 对象名称解析误当日志子资源权限。

持续归档仅覆盖一个节点、一个任务 namespace；覆盖仍 unknown/partial，未实现已知缺口时间线。1GB 队列满时上游可能丢弃旧数据，采集前删除/轮转、长时间停机、磁盘满和全部异常交付未验；没有严格一次、完整覆盖或集群全覆盖结论。真实容器重启/新文件已验，kubelet 按大小轮转仅有上游单测，未在节点制造高负载轮转。TLS 证书有效期 30d，后续需纳入正式凭据续期/部署机制。

P3-02 保持 in_progress，六维通过数不提升；两真实集群、完整视觉、全栈重启、资源聚合/预置视图及 AI 实际创建等剩余未关闭。下一步可做明确缺口记录、节点轮转/磁盘故障与正式组件准入，仍不恢复延期虚拟化。
