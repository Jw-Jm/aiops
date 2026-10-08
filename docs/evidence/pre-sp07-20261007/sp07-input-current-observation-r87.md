# SP-07 输入更新：当前观测与尚未完成门禁

本记录只准备事实与目标，不创建执行句柄，不证明未来 Task 7.5 或 Post-check 已通过。

- 原 R7 调度目标按原生 UID 正常重建并曾恢复 Ready；最新原生观测中三个有限目标均已正常结束为 Succeeded/Ready=false。最新 Graph 与原生同 UID 阶段一致，历史 Evidence 继续保留。不能沿用此前 Ready 作为当前状态。参见 [最新事实](current-core-r7-latest-target-facts-r77.json)。
- R7 实时 Node Metrics timestamp/window/capacity/allocatable 已与 Node UID 对照；完成后的 Pod 不再具有当前 Pod Metrics。普通指标读取不能证明高利用率症状通过。
- VictoriaMetrics 的旧观察窗 canary 与历史归档已获正式 API 证明；R7 VictoriaLogs 因初始 UID 的日志范围证据缺失仍未通过，Graph 明确 partial，不据此判定目标故障。参见 [正式历史指标查询](current-core-r7-victoria-phase-formal-api-r2.json)。
- R8 为 R10 独立 namespace 创建了专属有限 Linux Pod，其初始原生 UID、Ready 阶段和真实 stdout 在配置绑定前采集。依赖安装和后续正式 Source/Registry 初始化仍进行中，未宣称新交付业务通过。参见 [初始目标](current-core-r8-native-target-preparation-r1.json)。
- SSH 目标已用独立 host key、TrustedUserCAKeys 与分离的普通/root/sudo principal 完成 server-only 私有材料隔离和无命令链认证验证；证书有效期有限，后续必须再次按有效期核对，不代表永久授权。参见 [认证准备](ssh-target-server-only-projection-renewal-r4.json)。
- 物理 BMC/DIMM/NIC 无真实目标及证据，继续未验证。四类 ExecutionProfile 的目标/Runner/Ansible/AST parser/镜像计划沿用既有前置计划，执行功能未实施。

真实模型调查仍有首个模型调用超时，当前十并发链路尚未通过；DeepFlow bundled 准入/真实业务链路仍未完成；受保护原始 27 条 Evidence 缺匹配 Transit 恢复材料，原数据与存储保留。性能门禁为用户豁免，不能记录 PASS。KubeVirt/CDI 延期 disabled/unverified；PyRCA disabled/excluded。
