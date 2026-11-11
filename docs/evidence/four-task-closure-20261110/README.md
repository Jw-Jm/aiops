# 调查与人工处置四项功能验收进展

2026-11-10 至 11-11，环境 `mssc-direct / ops-current`，数据库 schema 75。沿用故障 `01a1bdac-eaef-7730-a73a-90738956cc96` 和 [11-09 已验收的真实处置证据](../incident-remediation-20261109/README.md)。本轮没有创建新的受控命令执行，没有重做历史变更、回退或未知结果测试。

## 实测结果

- P4-05 在同一故障页认领、转派、归还、留言和创建跟进任务。原操作者转派后失去转派入口。修复后的页面刷新能重新读到留言和跟进任务，任务完成、重开、再完成后刷新仍为完成。故障保持 resolved rev6。图谱缩放和重置改变画布，节点抽屉链接带入 Pod canonical ID 和集群，另有返回同一故障入口。[协作与图谱记录](live/)
- P4-05 的真实 PVC 和 Node 故障从冻结图打开准确资源抽屉，并从故障页发起同故障调查。PVC 返回 partial、1 个固定证据和 1 个正式引用。Node 首轮在结果结算失败，归档恢复后从原对话显式重试，返回 partial、13 个固定证据和 RCA rev2848。两条成功轮次均从同一故障历史回读，故障状态分别仍为 open 和 resolved。[图谱](live/incident-pvc-node-read.json) · [PVC 与 Node 首轮](live/incident-pvc-node-turns.json) · [Node 重试](live/node-investigation-after-archive.json)
- P5-01 / P5-02 在同一真实故障的既有对话中显式重试。公开步骤确认 `execute_analysis_query` 成功。持久化 `metrics.pod-cpu` 结果 `01a1c82a-2f91-7470-8d7d-a6b6ee82dae8` 使用固定 60 秒步长，返回 5 个 cores 实测点，首个时间点无观测。界面保留 partial 声明。点抽屉的时间、值、单位、行号和结果标识与持久化数据一致，数值表 5 行与导出逐行相符。日志读取并导出原结果 `01a1bdc5-6cb6-794d-a7f6-fb4ffe21f2c7` 的固定样本与来源版本。[趋势记录](live/trend-verified.json) · [趋势导出](live/trend-export.json) · [日志导出](live/real-log-export.json)
- P5-01 的 Pod 图谱入口在真实页面创建资源调查会话，HTTP 201，保存的 UID、窗口与入口一致。发送一次只读问题后，资源历史、告警和变更步骤成功，回答为 partial，未生成指标查询结果。指标点验收来自上面的同故障趋势轮次。[会话创建](live/resource-session.json) · [调查结算](live/resource-session-turn.json)
- P5-01 的告警严重级别统计在调用模型时收到供应商 HTTP 402。数据库中没有可复用的该数据集结果。产品修复把计费限制作为固定错误类别从调查器传到 Worker 和会话页，真实重试验证页面提示与持久错误一致，且未生成查询结果。最新运行版 `37ab3d1` 仍能读取历史趋势、固定 RCA 和已恢复归档。[初次拒绝](live/finding-count-provider-rejection.json) · [新版反馈](live/finding-count-provider-feedback.json) · [历史读回](live/provider-rollout-retained-reads.json)
- P5-02 的个人历史、改名、归档、绑定、断线恢复、撤权与法律保留沿用 [同环境既有验收](../p502-live-20261109/README.md)。复用 [已验收的结果复制和同范围下钻](../p502-output-actions-20261110/README.md)。
- P6-01 读取 13 条原有执行记录。全局按执行 ID 可定位未知状态，字面搜索无匹配显示空态，清空恢复 13 条。未知执行 exitCode 为空，取消、后续操作和回退均禁用。旧执行成功的独立验证仍是 not_resolved，其关联回退执行的报告仍是 resolved / rollback_goal_met。全局新建先重新授权并进入同一故障处置页，命令为空，未发起写操作。[列表](live/execution-list.json) · [筛选](live/execution-empty-clear.json) · [新建入口](live/execution-create-handoff.json) · [历史验证与回退](live/execution-postcheck-links.json)
- PostgreSQL 隔离库实测 `TestNativeExecutionSearchAndRevocation` 和 `TestSP07ExecutionListFiltersByAuthorization` 通过，覆盖搜索、授权与撤权。远端日志 `/mnt/ops-sdb/ops-p502-qa-20261109/codex-p601-list2.log`。Web 全量 65 文件、354 用例及 6 条流解析用例通过。Go 调查与资源绑定、执行、归档队列回归通过。[测试](checks/)

## 远程环境与故障修复

外接 SSD 发生 USB/UAS 断连。通过原文件系统 UUID 确认同一设备，重新挂载后完成 ext4 日志恢复。原 PostgreSQL 实例自动重放 WAL，OpenBao 用原实例恢复解封，原数据库中的 13 条执行与固定归档仍可读。Web 临时目录以 `1777` 恢复可写。[恢复后业务读取](live/storage-recovery-business-reads.json)

新归档上传失败的直接原因是 SeaweedFS 32 个卷槽位全部用尽。当前环境使用原目录、原锁定设置，将已支持的 `components.seaweedfs.volumeMax` 从 32 调整为 64，并只滚动该归档实例。两条此前写入失败的记录读回为 archived_verified，历史归档摘要一致。正式运行版本为 `e9f6939b685e70376eff6952e3a553d41f7e6635`，先于归档容量调整记录。外接 SSD 的物理连接问题仍需独立处理。[归档读回](live/archive-after-capacity.json) · [扩容记录](checks/archive-capacity.txt) · [版本登记](live/release-activation.jsonl)

代码修复包括跳过已经验证的图谱归档投影、Pod 更换后恢复指标与日志隔离证明、提交结果丢失时读取现有幂等回执，以及创建资源调查时传递原生会话证明。归档积压时只允许从新鲜观测绑定一个已确认的准确 UID。缺失 UID、名称匹配、来源失效和身份冲突仍要求完整观测。

## 待收口

- P5-01 的告警统计下钻等待模型供应商账户恢复。其他结构化分支和入口仍待逐片验证。PhysicalServer/Disk 入口可以进入助手页面，但当前资源调查解析只接收支持的 Kubernetes 类型，硬件调查合同和现场验收待补。P6-01 的 Service/PVC 人工验证仍需合格的实际目标和回执。已有 13 条执行均为 Pod 或 Deployment。
- 共享的故障转案例和知识原文分别由 P7-04 / P7-05 的 C-092、C-102、C-108 承接，模板库由 P6-02 的 C-089 承接。相关真实功能仍未交付。
- 这次的真实页面截图用于功能验收。89 张主切片的逐片视觉比对与六项整体验收仍待完成。
