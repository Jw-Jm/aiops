# r51 真实 Pod 指标链路验收（2026-11-03）

用户授权：接通并验收真实指标链路。执行环境仅 `ssh mssc-direct` / `192.168.0.63`，单节点 mssc、namespace `ops-native-v12-r24-20261026`。本轮由 GPT 直接执行，无模型生成数据、手动导入指标或新增 SQL 迁移；正式 00～10 文档及虚拟化延期边界保持。

## 结果与入口

开发预览 [指标查询](https://192.168.0.63:30471/observe/metrics) 已可用，需已有运维账号及当前集群授权。可从资源 Pod 详情的指标入口进入，或在指标页选择 Pod。

真实链路：kubelet/cAdvisor CPU counter、内存 working set + kube-state-metrics 原生 Pod restart counter/phase → VictoriaMetrics 内置 15s scrape → 真实管理员密码与 TOTP 登记的来源 → 原生会话、CSRF、当前授权、签名 owner RPC → 固定模板 → 趋势及数值表。无新 vmagent、AI Job、模型调用或指标查询持久历史合同。

来源 `01a1a003-476d-7e02-9752-bf89d2dd0349`，revision 1，logical ID `r51-continuous-pod-metrics`。API r51、Worker r51c、Web r51b 已实际部署；前版本容器/二进制保留。schema 仍为 72。四类指标均返回 `partial`、`exact:false` 和 `collection_coverage_unknown`；无样本返回 `no_data`，不生成零值。图中缺失步长不连线，时间位置按实际时间戳绘制。

## 实测证据

- [source-qualification.json](source-qualification.json)：SeaweedFS 原生 Pod UID、containerd ID 与 cAdvisor cgroup id 一致；两条 CPU/内存原始序列正值；KSM restart count/phase 与 Kubernetes 实时状态一致。四类原始采样时间均小于 45s，错误 UID、其他 namespace 无序列。
- [browser-metrics.json](browser-metrics.json)：真实 Chrome 运维账号登录，CPU、内存、重启增量、phase 四种趋势各 3 点；均由真实来源返回。跨集群 403、任意 PromQL 附加字段 400、缺 CSRF 403、不存在 UID 为无数据。零业务写入、零页面异常。
- [oracle-check.json](oracle-check.json)：独立直连指标库，在页面的同一时间窗口执行四条固定原生表达式，时间戳及数值共 12 点逐点匹配（误差阈值 1e-9）。CPU 为 5m rate 的 cores，内存为 bytes；重启是每 60s counter increase；phase 是为 1 的状态数量，并非 phase 枚举或健康裁决。
- [recovery-check.json](recovery-check.json)、[check-outage.json](check-outage.json)：只停止本轮 KSM，指标库 HTTP 仍 200，但资格退出、查询 503。恢复后原证明自动恢复；重建本轮 TSDB、保留同一 hostPath 后仍可读取故障前固定窗口。未改业务 Pod，也未解除来源/加密/授权校验。
- [identity-check.json](identity-check.json)：利用本轮故障试验的同名 KSM Pod 真实重建验证 UID 隔离。旧 UID 在旧窗口 3 点、新窗口无数据；新 UID 在旧窗口无数据、新窗口 3 点。不是按 Pod 名称拼接历史。
- [final-health.json](final-health.json)：两个新 Pod 均 Running/Ready、容器零重启；实际投射 JWT 的 iat 晚于 Pod 创建，600s token 已自动轮换，绑定 Pod UID 匹配，轮换后四类样本仍新鲜。只保留 JWT 时间/绑定判断，不保存 token。8 项原生 RBAC 正反例通过，包括允许本节点 nodes/metrics，拒绝 nodes/proxy、secrets、pods/log、删除 Pod。
- [runtime-health.json](runtime-health.json)：API/Worker、两个指标 Pod、原日志 collector 均 Ready/零重启，30471、30472 与原日志库运行；记录实际 API/Worker 制品哈希。
- [log-history-regression.json](log-history-regression.json)：原 r49 日志持久结果仍可读，既有结果及 qualification digest 保持。
- `runs/`：涉及的 Go 三包测试、连续指标 race 测试、16 个页面测试、TypeScript/Vite 构建、OpenAPI/UIV12/来源合同、代码生成及交付来源门禁。静态检查不代替上述真实链路。

## 为解除真实阻碍的最小修复

首次部署 Worker 后，部分 GVR 长期 `watch_initializing/list_missing`，页面无法查询，失败见 [browser-metrics-first-owner-notready.json](browser-metrics-first-owner-notready.json)。真实库/运行日志定位：初始 relist 对已验证且完全一致的归档重复封装，3s prepare/recover 截止时间又短于共享保留保护维护的 8s 有界持锁周期。

`prepareProjection` 仅在当前来源身份、UID、revision、content/query/template/scope 和归档 backend 全部匹配且 `archived_verified` 时复用既有对象；仍排队 Recover/Read 物理核验。prepare/recover 期限设为 12s（小于 35s watch 初始化期限），保留原加密、保留保护及当前来源守卫。仅复用修复的 r51b 仍不足，r51c 期限修复后 owner 恢复、真实四指标通过；两版部署回执均保留。测试覆盖未验证/不同存储/内容冲突不能绕过 Prepare。不声称历史归档积压或所有来源退化已清零。

kubelet 首轮误用自签旧 CA 被严格 TLS 拒绝；改为实际 serving cert 签发 CA，保留 SAN 校验和 server_name=mssc，未使用跳过验证。首次 UID 历史试验的子秒窗口返回 503，失败保存在 [identity-check-first.json](identity-check-first.json)；整秒窗口最终通过。本轮只验收整秒趋势。截图探针把页面公开 UUID 命中恢复码形状，截图被拒绝且未保存；功能数据断言通过，不据此提升视觉验收。

## 来源、许可证与运行约束

[KSM v2.17.0 官方发布](https://github.com/kubernetes/kube-state-metrics/releases/tag/v2.17.0)，源码 commit `f9f0a178a486dd1cc339ecebf40f27c6d5a48364`，Apache-2.0。精确源码归档、许可证、官方 index/amd64 image、每个 OCI blob/config、发布者 SPDX/OpenVEX asset digest、真实二进制及 Go1.24.6 buildinfo 已核对。[material.json](material.json)、[ksm-buildinfo.txt](ksm-buildinfo.txt) 记录哈希及 Go 模块依赖；SPDX 中两个缺少许可证声明的 Go 模块补核精确源码 Apache notice。大体积原始源码、SPDX、镜像 blob 保留本机 `/tmp/ops-r51-material` 与远程 `/data/ops-v12-native-visual-r24-20261026/r51/materials/`，未把新候选升级为正式 core Bundle。

远程直接拉镜像失败后，以数据方式取得并验证官方 amd64 OCI，离线转入 Docker/containerd。转换媒体类型产生不同 manifest digest，本地 alias 为 `docker.io/library/ops-r51-kube-state-metrics:v2.17.0`，config 保持 `sha256:32acec6855323f46b0b3ed1b193d38312db76b46a107390f6ddaefc9d8a51713`；不声称转换 manifest 就是官方原 manifest。VM 使用既有固定镜像，运行二进制确认 v1.116.0。

运行配置参考 [VictoriaMetrics 单机文档](https://docs.victoriametrics.com/victoriametrics/single-server-victoriametrics/)、[MetricsQL](https://docs.victoriametrics.com/victoriametrics/metricsql/) 和 [relabel](https://docs.victoriametrics.com/relabeling.html)。资格每 10s 核验四个 metric family 的真正存储样本时间（不是 evaluation time/HTTP up），45s 以内才刷新 30s lease，任何错误撤销；固定证明不随水位改变。该机制不保证每个 Pod 每个时间点都完整，故始终未知覆盖。

- VM `https://127.0.0.1:19448`，TLS+Basic；认证配置在远程私有目录及真实 Bao `openbao://secret/data/ops/r51-victoriametrics/source`，不入证据。KSM 仅 loopback 19809/19810。
- CA、token 文件均只读；投射 token 600s、自动挂载禁用；非 root、只读根、drop capabilities。KSM RBAC 仅任务 namespace 的 Pod list/watch，VM 仅 mssc 的 nodes/metrics get。
- namespace 在存储前过滤，丢弃 POD/空容器，以 cgroup 真实 UID 或 KSM 原生 UID 标记，强制 tenant/cluster/collector 标签。查询在聚合前追加当前授权 namespace/UID。
- 数据目录远程 `r51/metrics-data`，VM `-retentionPeriod=1`（1 个月），SourceBinding 30d；本轮只验证短时存储重启，未验证到期清理。不得复跑 prepare 脚本覆盖正在运行的私密凭据；后续操作先读取现状。
- 新 source TLS 证书有效期 30d，仍需正式续期；证书/凭据替换时同时更新只读挂载、Bao 与来源校验并实际复验。

## 边界

通过的是单节点、任务 namespace、四类 Pod 指标的真实链路，P4-01 仅 in_progress。未验收多节点/双集群、Node 指标、长时磁盘满与缺口时间线、子秒趋势、持久指标查询历史、完整页面视觉或正式 Bundle/离线安装。未恢复 KubeVirt/CDI/Trace/生产发布，不提升整套 V1.2 或六维结论。本轮无 Git 提交/推送；前次 GitHub/远程仓库同步基线仍为 `0d6caa2`，本轮运行部署与未提交代码变更见回执。
